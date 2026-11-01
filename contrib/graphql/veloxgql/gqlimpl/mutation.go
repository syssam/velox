package gqlimpl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/internal/english"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// Atomic runs fn in db.Tx(ctx, fn), on the DB veloxgql.DBFromContext(ctx)
// reads. For generated code.
//
// In the transaction the Extension opened for a mutation operation, db.Tx
// makes a savepoint, so fn's error rolls back fn's writes at once, and the
// operation's when the resolver returns it; a resolver that swallows it keeps
// the operation's other writes, and the operation commits. Outside one, fn
// runs in a transaction of its own, so a mutation's writes are kept or
// dropped together outside a server too.
// Without a DB in ctx, Atomic fails with veloxgql.ErrNoDB and fn does not
// run.
func Atomic(ctx context.Context, fn func(ctx context.Context, db *velox.DB) error) error {
	db, err := veloxgql.DBFromContext(ctx)
	if err != nil {
		return err
	}
	return db.Tx(ctx, fn)
}

// DecodeID decodes id, an ID of the GraphQL type typ, for the input field at
// ("CreatePostInput.authorID", "updatePost.id"). For generated code. Its error
// is BAD_USER_INPUT, names at, and wraps veloxgql.ErrBadID or ErrWrongIDType.
func DecodeID[K any](at, id, typ string) (K, error) {
	k, err := veloxgql.DecodeID[K](id, typ)
	if err != nil {
		return k, gqlcore.Within(at, err)
	}
	return k, nil
}

// DecodeIDAs is DecodeID of a K, converted to V by conv: for a key field whose
// Go type is a named type DecodeID does not take. For generated code.
func DecodeIDAs[K, V any](at, id, typ string, conv func(K) V) (V, error) {
	k, err := DecodeID[K](at, id, typ)
	if err != nil {
		var zero V
		return zero, err
	}
	return conv(k), nil
}

// DecodeIDs decodes ids as DecodeID does, keeps the first of each repeated key,
// and refuses more than the MaxPage of veloxgql.LimitsFromContext(ctx) IDs with
// BAD_USER_INPUT wrapping veloxgql.ErrPageTooLarge, before decoding any. For
// generated code. An ID's error names its place in the list, as "<at>[2]".
func DecodeIDs[K comparable](ctx context.Context, at string, ids []string, typ string) ([]K, error) {
	if most := veloxgql.LimitsFromContext(ctx).MaxPage; len(ids) > most {
		return nil, InputErrorf("%s: %w: %d IDs, and the most is %d", at, veloxgql.ErrPageTooLarge, len(ids), most)
	}
	out := make([]K, 0, len(ids))
	seen := make(map[K]struct{}, len(ids))
	for i, id := range ids {
		k, err := DecodeID[K](fmt.Sprintf("%s[%d]", at, i), id, typ)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out, nil
}

// DecodeIDsAs is DecodeIDs of K keys, each converted to V by conv. For
// generated code.
func DecodeIDsAs[K comparable, V any](ctx context.Context, at string, ids []string, typ string, conv func(K) V) ([]V, error) {
	ks, err := DecodeIDs[K](ctx, at, ids, typ)
	if err != nil {
		return nil, err
	}
	out := make([]V, len(ks))
	for i, k := range ks {
		out[i] = conv(k)
	}
	return out, nil
}

// CheckExists fails with NOT_FOUND, naming at and typ, unless q counts n rows:
// q selects the rows the IDs of at name, under the caller's tenant and Read
// policy. For generated code. Its text is "veloxgql: <at>: <missing> of the <n>
// IDs name no <typ>", or NotFound's for n 1, and echoes no ID. n is the number
// of distinct IDs; for n 0, CheckExists runs no query. The query's own error is
// returned as it is.
func CheckExists[E any](ctx context.Context, q velox.Query[E], n int, at, typ string) error {
	if n == 0 {
		return nil
	}
	got, err := q.Count(ctx)
	if err != nil {
		return err
	}
	return matched(got, n, at, typ)
}

// matched fails as CheckExists does unless got, the rows a statement counted or
// changed, is n, the number of distinct IDs of at that it names. More rows
// than IDs is an internal error: the IDs were not distinct keys. A count the
// driver did not report (-1) is CountUnknown's internal error, not a short
// count.
func matched(got int64, n int, at, typ string) error {
	switch {
	case got < 0:
		return CountUnknown(at)
	case got < int64(n) && n == 1:
		return NotFound(at, typ)
	case got < int64(n):
		return gqlcore.NotFoundf("%s: %d of the %d IDs name no %s", at, int64(n)-got, n, typ)
	case got > int64(n):
		return fmt.Errorf("veloxgql: %s: %d rows for %d IDs: the IDs are not distinct keys", at, got, n)
	}
	return nil
}

// Repointed checks an UPDATE that re-pointed the children the n distinct IDs of
// at name to a new owner of the GraphQL type owner, and changed got rows: it
// re-points only children that no owner holds, so a child another owner holds
// is not taken from it. For generated code. When got is under n, linked counts
// the children of those IDs the caller may read that an owner holds; one or
// more is BAD_USER_INPUT "veloxgql: <at>: a <typ> is already linked to another
// <owner>", which tells the caller nothing it may not read. Otherwise the error
// is CheckExists's: NOT_FOUND for a child the caller may not change, or that
// does not exist. The count's own error is returned as it is. A got of -1, a
// count the driver did not report, is CountUnknown's error: the UPDATE's rows
// cannot be told from the IDs that name no child.
func Repointed[E any](ctx context.Context, got int64, n int, linked velox.Query[E], at, typ, owner string) error {
	if got < 0 || got >= int64(n) {
		return matched(got, n, at, typ)
	}
	c, err := linked.Count(ctx)
	switch {
	case err != nil:
		return err
	case c > 0:
		return InputErrorf("%s: %s %s is already linked to another %s", at, english.Article(typ), typ, owner)
	}
	return matched(got, n, at, typ)
}

// StillLinked fails when q, run without the tenant scope or the Read policy,
// finds a row: a child of the GraphQL type typ that the owner of the type owner
// still holds after the caller's scoped unlink, which the caller may not
// change. For generated code. Its error is BAD_USER_INPUT "veloxgql: <at>: the
// <owner> is already linked to a or an <typ>" (as Repointed writes the
// article), which says nothing about the child. The query's own error is
// returned as it is.
func StillLinked[E any](ctx context.Context, q velox.Query[E], at, typ, owner string) error {
	const reason = "graphql: a hidden child of a one-to-one re-point"
	ok, err := q.Exists(velox.WithAllTenants(velox.WithoutPolicy(ctx, reason), reason))
	switch {
	case err != nil:
		return err
	case ok:
		return InputErrorf("%s: the %s is already linked to %s %s", at, owner, english.Article(typ), typ)
	}
	return nil
}

// Readable returns the keys, which key reads from a row, of the rows q selects:
// q names at most n distinct keys, under the caller's tenant and Read policy,
// so its rows are those of the keys the caller may read. For generated code.
// For n 0, Readable runs no query. The query's own error is returned as it is.
// remove<Edge>IDs and clear<Edge> of a many-to-many edge delete only the join
// rows of these keys.
func Readable[T, K any](ctx context.Context, q velox.Query[T], n int, key func(*T) K) ([]K, error) {
	if n == 0 {
		return nil, nil
	}
	ts, err := q.Limit(n).MaxRows(n).All(ctx)
	if err != nil {
		return nil, err
	}
	ks := make([]K, len(ts))
	for i := range ts {
		ks[i] = key(&ts[i])
	}
	return ks, nil
}

// UnlinkAll is clear<Edge> of a many-to-many edge. For generated code. links
// selects the join rows of the row the mutation changes, ordered by their
// primary key; UnlinkAll reads them the MaxPage of
// veloxgql.LimitsFromContext(ctx) at a time, and gives unlink the target keys
// of each page, which other reads from a join row. unlink deletes the join rows
// of the keys whose target the caller may read (Readable), and leaves the
// others: each page starts after the last row of the one before, so a join row
// left is not read again. The first error is returned as it is.
func UnlinkAll[J, K any](ctx context.Context, links velox.Query[J], other func(*J) K, unlink func(ks []K) error) error {
	page := veloxgql.LimitsFromContext(ctx).MaxPage
	q := links.Limit(page).MaxRows(page)
	for {
		js, err := q.All(ctx)
		if err != nil || len(js) == 0 {
			return err
		}
		ks := make([]K, len(js))
		for i := range js {
			ks[i] = other(&js[i])
		}
		if err := unlink(ks); err != nil {
			return err
		}
		if len(js) < page {
			return nil
		}
		q = q.After(&js[len(js)-1])
	}
}

// Disjoint fails with BAD_USER_INPUT "veloxgql: <input>: an ID is in both <add>
// and <remove>" when a key is in both a and b, the decoded IDs of the fields
// add and remove of the input named input: the input has no single meaning. For
// generated code. It echoes no ID.
func Disjoint[K comparable](input, add, remove string, a, b []K) error {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	in := make(map[K]struct{}, len(a))
	for _, k := range a {
		in[k] = struct{}{}
	}
	for _, k := range b {
		if _, ok := in[k]; ok {
			return InputErrorf("%s: an ID is in both %s and %s", input, add, remove)
		}
	}
	return nil
}

// NotFound is the NOT_FOUND error of a mutation whose ID names no row of typ
// the caller may change: "veloxgql: <at>: no <typ> has this ID". For generated
// code.
func NotFound(at, typ string) error {
	return gqlcore.NotFoundf("%s: no %s has this ID", at, typ)
}

// CountUnknown is the internal error of a write at whose driver did not report
// the rows it changed (velox's count -1), when the count decides the result:
// "veloxgql: <at>: row count unknown". For generated code. Returned inside the
// mutation's transaction, it rolls the write back.
func CountUnknown(at string) error {
	return fmt.Errorf("veloxgql: %s: row count unknown", at)
}

// NotZero refuses v, what the input field at gives a field that has a Default,
// when it is the field's zero value: an insert would store the Default in its
// place, as it does for a field left out. The client gets BAD_USER_INPUT. For
// generated code.
func NotZero[V any](at string, v V) error {
	if reflect.ValueOf(&v).Elem().IsZero() {
		return InputErrorf("%s: invalid value: the empty value cannot be stored on create, where it means the field's default; leave the field out, or set it with an update", at)
	}
	return nil
}

// NotNull refuses a null the client gave the input field name, of the argument
// arg of the running field, on a Nillable field that has a Default: an insert
// would store the Default in its place, as it does for a field left out, and an
// input's nil pointer cannot tell them apart. So NotNull reads the request. The
// client gets BAD_USER_INPUT, naming at. Called outside a gqlgen field, or from a
// field whose argument has another name, it finds no null and refuses nothing. For
// generated code.
func NotNull(ctx context.Context, at, arg, name string) error {
	if !nullGiven(ctx, arg, name) {
		return nil
	}
	return InputErrorf("%s: invalid value: null cannot be stored on create, where it means the field's default; leave the field out, or clear it with an update", at)
}

// NullClears refuses a null the client gave the input field name, of the argument
// arg of the running field, on an update: an update's nil pointer leaves the field
// as it is, and a client that says null means to clear it, which clearField, the input's
// other field, does. The client gets BAD_USER_INPUT, naming at and clearField. Called
// outside a gqlgen field, or from a field whose argument has another name, it finds
// no null and refuses nothing. For generated code.
func NullClears(ctx context.Context, at, arg, name, clearField string) error {
	if !nullGiven(ctx, arg, name) {
		return nil
	}
	return InputErrorf("%s: invalid value: null would leave it as it is; give %s: true to clear it, or leave the field out", at, clearField)
}

// nullGiven reports whether the running field's argument arg gives name as null.
func nullGiven(ctx context.Context, arg, name string) bool {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil || fc.Field.Field == nil || !graphql.HasOperationContext(ctx) {
		return false
	}
	a := fc.Field.Arguments.ForName(arg)
	return a != nil && givenNull(a.Value, name, graphql.GetOperationContext(ctx).Variables)
}

// givenNull reports whether the input object v, a literal or a variable, gives
// name as null: a variable left out gives nothing.
func givenNull(v *ast.Value, name string, vars map[string]any) bool {
	if v == nil {
		return false
	}
	switch v.Kind {
	case ast.Variable:
		m, _ := vars[v.Raw].(map[string]any)
		x, ok := m[name]
		return ok && x == nil
	case ast.ObjectValue:
		c := v.Children.ForName(name)
		switch {
		case c == nil:
			return false
		case c.Kind == ast.NullValue:
			return true
		case c.Kind == ast.Variable:
			x, ok := vars[c.Raw]
			return ok && x == nil
		}
	}
	return false
}

// WriteError is err from a write of entity (the name velox reports, as
// "gen.Post") made from the input named input. For generated code. A
// *velox.ValidationError of entity on a field name gives a GraphQL name for
// becomes BAD_USER_INPUT "<input>.<name>: invalid value: <the validator's
// text>", wrapping err. Every other error is returned as it is. So is a
// validation error joined with another error (errors.Join): only err itself and
// the chain of errors it wraps one by one are searched, so that ErrorPresenter
// still logs an internal error joined with the validation error.
func WriteError(err error, input, entity string, name func(goField string) string) error {
	var v *velox.ValidationError
	for e := err; e != nil && v == nil; {
		switch x := e.(type) {
		case *velox.ValidationError:
			v = x
		case interface{ Unwrap() error }:
			e = x.Unwrap()
		default:
			e = nil
		}
	}
	if v == nil || v.Entity != entity {
		return err
	}
	field := name(v.Field)
	if field == "" {
		return err
	}
	msg := input + "." + field + ": invalid value"
	if v.Err != nil {
		msg += ": " + v.Err.Error()
	}
	return &gqlcore.Error{Code: gqlcore.CodeBadUserInput, Msg: msg, Err: err}
}

// FromJSON decodes j into a T for the input field at; on failure, a
// BAD_USER_INPUT error naming at, which does not echo the decoder's text (it
// names Go types). For generated code.
//
// What it stores is what the client gave, or it refuses. A number in an any is a
// float64, as encoding/json and a hook expect, when that says the same number
// (exactFloat), and a json.Number when it would not, so that it keeps its digits. A key
// that no field of a struct has is refused, as json.Unmarshal would drop it; so is a null
// where the type holds none, a string in a []string as much as a field of a struct, as
// it would store the zero value; and a list that is not the length of a Go array, which
// it would pad or cut. A type with its own UnmarshalJSON decides for itself. Reading a
// row back into an any is velox's scan, which gives a float64.
func FromJSON[T any](at string, j veloxgql.JSON) (T, error) {
	var t, zero T
	dec := json.NewDecoder(bytes.NewReader(j))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		if key, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			return zero, InputErrorf("%s: invalid value: the key %s is not in the field's type", at, key)
		}
		return zero, InputErrorf("%s: invalid value: not a JSON value of the field's type", at)
	}
	typ := reflect.TypeFor[T]()
	if bytes.Contains(j, []byte("null")) || typeHolds(typ, reflect.Array, nil) {
		var v any
		dec := json.NewDecoder(bytes.NewReader(j))
		dec.UseNumber()
		// j decoded once into T, so it is a JSON value.
		if err := dec.Decode(&v); err == nil {
			if why := misfit(typ, v); why != "" {
				return zero, InputErrorf("%s: invalid value: %s", at, why)
			}
		}
	}
	if typeHolds(typ, reflect.Interface, nil) {
		floatNumbers(reflect.ValueOf(&t).Elem())
	}
	return t, nil
}

// typeHolds reports whether a value of the type t may hold one of the kind, in a place
// the decoder fills itself.
func typeHolds(t reflect.Type, kind reflect.Kind, seen map[reflect.Type]bool) bool {
	if t.Kind() == kind {
		return !t.Implements(jsonUnmarshaler)
	}
	if t.Implements(jsonUnmarshaler) || reflect.PointerTo(t).Implements(jsonUnmarshaler) || seen[t] {
		return false
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map, reflect.Struct:
		if seen == nil {
			seen = map[reflect.Type]bool{}
		}
		seen[t] = true
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		return typeHolds(t.Elem(), kind, seen)
	case reflect.Struct:
		for _, f := range reflect.VisibleFields(t) {
			if f.IsExported() && typeHolds(f.Type, kind, seen) {
				return true
			}
		}
	}
	return false
}

// floatNumbers makes each json.Number in an any under v a float64, when exactFloat says
// that keeps it. v is settable.
func floatNumbers(v reflect.Value) {
	if t := v.Type(); t.Kind() != reflect.Interface && (t.Implements(jsonUnmarshaler) || reflect.PointerTo(t).Implements(jsonUnmarshaler)) {
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.NumMethod() == 0 && !v.IsNil() {
			v.Set(reflect.ValueOf(floatsOf(v.Interface())).Convert(v.Type()))
		}
	case reflect.Pointer:
		if !v.IsNil() {
			floatNumbers(v.Elem())
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			floatNumbers(v.Index(i))
		}
	case reflect.Map:
		for it := v.MapRange(); it.Next(); {
			e := reflect.New(v.Type().Elem()).Elem()
			e.Set(it.Value())
			floatNumbers(e)
			v.SetMapIndex(it.Key(), e)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				floatNumbers(v.Field(i))
			}
		}
	}
}

// floatsOf is x, a value the decoder put in an any, with floatNumbers's change made.
func floatsOf(x any) any {
	switch x := x.(type) {
	case json.Number:
		if f, ok := exactFloat(string(x)); ok {
			return f
		}
	case []any:
		for i := range x {
			x[i] = floatsOf(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = floatsOf(x[k])
		}
	}
	return x
}

// exactFloat is the number s as a float64, and whether that says the same number: its
// shortest decimal, which is what JSON writes of it, has the value s has. So 0.1 is
// kept, and 9007199254740993, 1e400 and 1e-400 are not. It works on the text, whatever
// the exponent, as a big.Rat of 1e999999 takes milliseconds.
func exactFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, decimal(s) == decimal(strconv.FormatFloat(f, 'e', -1, 64))
}

// decimal is the JSON number s as its digits, without zeros at either end, and the
// exponent of the last: two numbers are equal when these are. Its sign is no part of
// it, as ParseFloat keeps that. An exponent out of range is "", which equals no number
// ParseFloat took.
func decimal(s string) string {
	s = strings.TrimPrefix(s, "-")
	exp := 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		var err error
		if exp, err = strconv.Atoi(s[i+1:]); err != nil {
			return ""
		}
		s = s[:i]
	}
	whole, frac, _ := strings.Cut(s, ".")
	digits := strings.TrimLeft(whole+frac, "0")
	exp -= len(frac)
	trimmed := strings.TrimRight(digits, "0")
	exp += len(digits) - len(trimmed)
	if trimmed == "" {
		return "0"
	}
	return trimmed + "e" + strconv.Itoa(exp)
}

// misfit says what in v, decoded JSON, a value of the type t cannot hold, so that
// decoding it gives another value: a null it has no way to hold, or a list that is not
// the length of its array. It is "" for none.
func misfit(t reflect.Type, v any) string {
	if t.Implements(jsonUnmarshaler) || reflect.PointerTo(t).Implements(jsonUnmarshaler) {
		return ""
	}
	const null = "null where the field's type holds none"
	switch t.Kind() {
	case reflect.Interface:
		return ""
	case reflect.Pointer:
		if v == nil {
			return ""
		}
		return misfit(t.Elem(), v)
	case reflect.Slice, reflect.Array:
		list, isList := v.([]any)
		if t.Kind() == reflect.Array {
			if v == nil {
				return null
			}
			if isList && len(list) != t.Len() {
				return fmt.Sprintf("a list of %d where the field's type holds %d", len(list), t.Len())
			}
		}
		for _, e := range list {
			if why := misfit(t.Elem(), e); why != "" {
				return why
			}
		}
		return ""
	case reflect.Map:
		m, _ := v.(map[string]any)
		for _, e := range m {
			if why := misfit(t.Elem(), e); why != "" {
				return why
			}
		}
		return ""
	case reflect.Struct:
		if v == nil {
			return null
		}
		m, _ := v.(map[string]any)
		for k, e := range m {
			if f, ok := jsonField(t, k); ok {
				if why := misfit(f.Type, e); why != "" {
					return why
				}
			}
		}
		return ""
	}
	if v == nil {
		return null
	}
	return ""
}

// jsonField is the field of the struct t that encoding/json decodes the key k into:
// by its tag or its name, the exact one first, then in any case.
func jsonField(t reflect.Type, k string) (reflect.StructField, bool) {
	var fold reflect.StructField
	found := false
	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch name {
		case "-":
			continue
		case "":
			name = f.Name
		}
		if name == k {
			return f, true
		}
		if !found && strings.EqualFold(name, k) {
			fold, found = f, true
		}
	}
	return fold, found
}

var jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()
