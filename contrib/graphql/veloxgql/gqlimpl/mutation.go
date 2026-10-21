package gqlimpl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/internal/shared"
)

// Atomic runs fn with the DB veloxgql.DBFrom(ctx) reads. When that DB is the
// transaction the Extension opened for a mutation operation, fn runs in it;
// otherwise fn runs in db.Tx(ctx, fn), so a mutation's writes are kept or
// dropped together outside a server too. For generated code only.
//
// In the Extension's transaction fn gets ctx and that DB as they are: no
// savepoint is made, and fn's error fails the operation, whose transaction
// then rolls back, as long as the resolver returns it: one that swallows
// the error commits fn's partial writes with the operation. Without a DB in ctx, Atomic fails with veloxgql.ErrNoDB
// and fn does not run.
func Atomic(ctx context.Context, fn func(ctx context.Context, db *velox.DB) error) error {
	db, err := veloxgql.DBFrom(ctx)
	if err != nil {
		return err
	}
	if db == shared.OperationTx(ctx) {
		return fn(ctx, db)
	}
	return db.Tx(ctx, fn)
}

// DecodeID decodes id, an ID of the GraphQL type typ, for the input field
// at ("CreatePostInput.authorID", "updatePost.id"). Its error is
// BAD_USER_INPUT, names at, and wraps veloxgql.ErrBadID or ErrWrongIDType.
// For generated code only.
func DecodeID[K any](at, id, typ string) (K, error) {
	k, err := veloxgql.DecodeID[K](id, typ)
	if err != nil {
		return k, shared.Within(at, err)
	}
	return k, nil
}

// DecodeIDAs is DecodeID of a K, converted to V by conv: for a key field
// whose Go type is a named type DecodeID does not take. For generated code
// only.
func DecodeIDAs[K, V any](at, id, typ string, conv func(K) V) (V, error) {
	k, err := DecodeID[K](at, id, typ)
	if err != nil {
		var zero V
		return zero, err
	}
	return conv(k), nil
}

// DecodeIDs decodes ids as DecodeID does, keeps the first of each repeated
// key, and refuses more than the MaxPage of veloxgql.LimitsFrom(ctx) IDs
// with BAD_USER_INPUT wrapping veloxgql.ErrPageTooLarge, before decoding any.
// An ID's error names its place in the list, as "<at>[2]". For generated
// code only.
func DecodeIDs[K comparable](ctx context.Context, at string, ids []string, typ string) ([]K, error) {
	if most := veloxgql.LimitsFrom(ctx).MaxPage; len(ids) > most {
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
// generated code only.
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

// Exist fails with NOT_FOUND, naming at and typ, unless q counts n rows:
// q selects the rows the IDs of at name, under the caller's tenant and
// Read policy. Its text is "veloxgql: <at>: <missing> of the <n> IDs name
// no <typ>", or NotFound's for n 1, and echoes no ID. n is the number of
// distinct IDs; for n 0, Exist runs no query. The query's own error is
// returned as it is. For generated code only.
func Exist[E any](ctx context.Context, q velox.Query[E], n int, at, typ string) error {
	if n == 0 {
		return nil
	}
	got, err := q.Count(ctx)
	if err != nil {
		return err
	}
	return Matched(got, n, at, typ)
}

// Matched fails as Exist does unless got, the rows a statement counted or
// changed, is n, the number of distinct IDs of at that it names: for an
// UPDATE that re-points the rows of a to-many edge, which the tenant and
// the Write policy scope. More rows than IDs is an internal error: the IDs
// were not distinct keys. For generated code only.
func Matched(got int64, n int, at, typ string) error {
	switch {
	case got < int64(n) && n == 1:
		return NotFound(at, typ)
	case got < int64(n):
		return shared.NotFoundf("%s: %d of the %d IDs name no %s", at, int64(n)-got, n, typ)
	case got > int64(n):
		return fmt.Errorf("veloxgql: %s: %d rows for %d IDs: the IDs are not distinct keys", at, got, n)
	}
	return nil
}

// Repointed checks an UPDATE that re-pointed the children the n distinct
// IDs of at name to a new owner of the GraphQL type owner, and changed got
// rows: it re-points only children that no owner holds, so a child another
// owner holds is not taken from it. When got is under n, linked counts the
// children of those IDs the caller may read that an owner holds; one or more
// is BAD_USER_INPUT "veloxgql: <at>: a <typ> is already linked to another
// <owner>", which tells the caller nothing it may not read. Otherwise the
// error is Matched's: NOT_FOUND for a child the caller may not change, or
// that does not exist. The count's own error is returned as it is. For
// generated code only.
func Repointed[E any](ctx context.Context, got int64, n int, linked velox.Query[E], at, typ, owner string) error {
	if got >= int64(n) {
		return Matched(got, n, at, typ)
	}
	c, err := linked.Count(ctx)
	switch {
	case err != nil:
		return err
	case c > 0:
		return InputErrorf("%s: %s %s is already linked to another %s", at, article(typ), typ, owner)
	}
	return Matched(got, n, at, typ)
}

// Disjoint fails with BAD_USER_INPUT "veloxgql: <input>: an ID is in both
// <add> and <remove>" when a key is in both a and b, the decoded IDs of the
// fields add and remove of the input named input: the input has no single
// meaning. It echoes no ID. For generated code only.
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

// article is the indefinite article of the name: "an" before a vowel.
func article(name string) string {
	if name != "" && strings.ContainsRune("AEIOUaeiou", rune(name[0])) {
		return "an"
	}
	return "a"
}

// NotFound is the NOT_FOUND error of a mutation whose ID names no row of
// typ the caller may change: "veloxgql: <at>: no <typ> has this ID". For
// generated code only.
func NotFound(at, typ string) error {
	return shared.NotFoundf("%s: no %s has this ID", at, typ)
}

// WriteError is err from a write of entity (the name velox reports, as
// "gen.Post") made from the input named input. A *velox.ValidationError of
// entity on a field name gives a GraphQL name for becomes BAD_USER_INPUT
// "<input>.<name>: invalid value: <the validator's text>", wrapping err.
// Every other error is returned as it is. So is a validation error joined
// with another error (errors.Join): only err itself and the chain of errors
// it wraps one by one are searched, so that ErrorPresenter still logs an
// internal error joined with the validation error. For generated code only.
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
	return &shared.Error{Code: shared.CodeBadUserInput, Msg: msg, Err: err}
}

// FromJSON decodes j into a T for the input field at; on failure, a
// BAD_USER_INPUT error naming at, which does not echo the decoder's text
// (it names Go types). For generated code only.
func FromJSON[T any](at string, j veloxgql.JSON) (T, error) {
	var t T
	if err := json.Unmarshal(j, &t); err != nil {
		var zero T
		return zero, InputErrorf("%s: invalid value: not a JSON value of the field's type", at)
	}
	return t, nil
}
