package gqlimpl

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/internal/english"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/internal/shared"
)

// Atomic runs fn in db.Tx(ctx, fn), on the DB veloxgql.DBFromContext(ctx)
// reads. For generated code.
//
// In the transaction the Extension opened for a mutation operation, db.Tx
// makes a savepoint, so fn's error rolls back fn's writes at once, and the
// operation's when the resolver returns it; a resolver that swallows it keeps
// the operation's other writes, and the operation commits. Outside one, fn runs in a transaction of its own, so a
// mutation's writes are kept or dropped together outside a server too.
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
		return k, shared.Within(at, err)
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
		return shared.NotFoundf("%s: %d of the %d IDs name no %s", at, int64(n)-got, n, typ)
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
	return shared.NotFoundf("%s: no %s has this ID", at, typ)
}

// CountUnknown is the internal error of a write at whose driver did not report
// the rows it changed (velox's count -1), when the count decides the result:
// "veloxgql: <at>: row count unknown". For generated code. Returned inside the
// mutation's transaction, it rolls the write back.
func CountUnknown(at string) error {
	return fmt.Errorf("veloxgql: %s: row count unknown", at)
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
	return &shared.Error{Code: shared.CodeBadUserInput, Msg: msg, Err: err}
}

// FromJSON decodes j into a T for the input field at; on failure, a
// BAD_USER_INPUT error naming at, which does not echo the decoder's text (it
// names Go types). For generated code.
func FromJSON[T any](at string, j veloxgql.JSON) (T, error) {
	var t T
	if err := json.Unmarshal(j, &t); err != nil {
		var zero T
		return zero, InputErrorf("%s: invalid value: not a JSON value of the field's type", at)
	}
	return t, nil
}
