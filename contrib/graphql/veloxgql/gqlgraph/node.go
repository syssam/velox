package gqlgraph

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/untyped"
)

// The errors of ResolveNode, under velox-graphql's option compat=veloxe,
// whose generated ResolveNode and ResolveNodes call it. Test for them with
// errors.Is.
var (
	// ErrNodeNotFound: no type of Node has a row of the ID that the caller
	// may read. Its text is veloxe's. ResolveNode returns it in an error
	// with the code NOT_FOUND, which gqlgenx.ErrorPresenter shows the client.
	ErrNodeNotFound = errors.New("could not resolve to a node with the global ID")
	// ErrAmbiguousNodeID: more than one type of Node has a row of the ID, as
	// veloxe refused it. A Node's ID holds no type, so node(id:) needs IDs
	// that are unique across tables, as a snowflake is. ResolveNode returns
	// it in an error with the code BAD_USER_INPUT, which names the types the
	// caller may read and not the ID.
	ErrAmbiguousNodeID = errors.New("node id matches more than one type")
)

// LoadNode returns the row of type c whose key is id, a *E in an any, read
// with db as any query of c's entity is, so its Read policy, its scope and
// its interceptors apply. It returns nil and no error when c has no such row
// that the caller may read: none has the key, a policy hides it or denies
// the read, or the key's column cannot hold id. So a denial and a row that
// is not there look the same, and node(id:) tells a caller nothing of a row
// it may not read. Any other error, a driver's, is returned.
//
// It is the probe ResolveNode makes of each type; an application that
// resolves node(id:) its own way, as in an order of its own, calls it with
// the types the generated NodeTypes returns.
func LoadNode(ctx context.Context, db *velox.DB, c *Type, id any) (any, error) {
	if len(c.Key) != 1 {
		return nil, fmt.Errorf("gqlgraph: type %s has %d key columns, and a Node has one", c.Name, len(c.Key))
	}
	t, col := c.table(), c.Key[0].Index
	k, err := t.Convert(col, id)
	if err != nil {
		return nil, nil //nolint:nilerr // a key the column cannot hold names no row of the type
	}
	row, err := untyped.Query(db, t).Where(t.Pred(col, untyped.PredEQ, k)).One(ctx)
	switch {
	case errors.Is(err, velox.ErrNotFound), errors.Is(err, velox.ErrDenied):
		return nil, nil
	case err != nil:
		return nil, err
	}
	return row, nil
}

// ResolveNode returns the one row of the types that has the key id, as
// veloxe's Client.Noder did: it probes every type with LoadNode, in the
// order given, one query each. It fails with ErrNodeNotFound (NOT_FOUND)
// when none has it, with ErrAmbiguousNodeID when more than one does, naming
// them and not the ID, and with the first other error a probe returns.
func ResolveNode(ctx context.Context, db *velox.DB, types []*Type, id any) (any, error) {
	var node any
	var found []string
	for _, c := range types {
		n, err := LoadNode(ctx, db, c, id)
		if err != nil {
			return nil, err
		}
		if n != nil {
			node = n
			found = append(found, c.Name)
		}
	}
	switch len(found) {
	case 0:
		return nil, gqlcore.NotFoundf("%w", ErrNodeNotFound)
	case 1:
		return node, nil
	}
	return nil, gqlcore.InputErrorf("%w: the ID is a %s; node(id:) needs IDs unique across types", ErrAmbiguousNodeID, strings.Join(found, " and a "))
}

// ResolveNodes returns the row of each of ids, in order, as veloxe's
// Client.Noders did, with nil for an ID that no type has, as Relay's
// nodes(ids:) asks. It reads each type once, with one IN query of every
// ID its key column can hold, as entgql's Noders does: one statement per
// type, however many IDs. Each type's rows are one batch (veloxgql.Batch),
// so an edge of theirs is read for all of them in one statement.
//
// More IDs than the MaxPage of veloxgql.LimitsFromContext(ctx) is
// veloxgql.ErrPageTooLarge, BAD_USER_INPUT, before any statement. An ID
// that more than one type has fails the whole list with ErrAmbiguousNodeID,
// as ResolveNode fails; so does any other error.
func ResolveNodes[K any](ctx context.Context, db *velox.DB, types []*Type, ids []K) ([]any, error) {
	if most := veloxgql.LimitsFromContext(ctx).MaxPage; len(ids) > most {
		return nil, gqlcore.InputErrorf("%w: nodes(ids:) takes at most %d IDs", veloxgql.ErrPageTooLarge, most)
	}
	xs := make([]any, len(ids))
	for i, id := range ids {
		xs[i] = id
	}
	return resolveNodes(ctx, db, types, xs)
}

// resolveNodes is ResolveNodes of ids, already capped.
func resolveNodes(ctx context.Context, db *velox.DB, types []*Type, ids []any) ([]any, error) {
	nodes := make([]any, len(ids))
	found := make([][]string, len(ids)) // the types that have each ID
	ambiguous := -1                     // the first ID of two types
	for _, c := range types {
		rows, err := loadNodes(ctx, db, c, ids)
		if err != nil {
			return nil, err
		}
		for i, r := range rows {
			if r == nil {
				continue
			}
			nodes[i] = r
			found[i] = append(found[i], c.Name)
			if len(found[i]) > 1 && ambiguous < 0 {
				ambiguous = i
			}
		}
	}
	if ambiguous >= 0 {
		return nil, gqlcore.InputErrorf("%w: an ID is a %s; nodes(ids:) needs IDs unique across types", ErrAmbiguousNodeID, strings.Join(found[ambiguous], " and a "))
	}
	return nodes, nil
}

// loadNodes returns, for each of ids, the row of type c whose key it is, or
// nil, read with one IN query of the keys, as LoadNode reads one: a key the
// column cannot hold, a hidden row and a denied read are all none. The rows
// it read are one batch.
func loadNodes(ctx context.Context, db *velox.DB, c *Type, ids []any) ([]any, error) {
	if len(c.Key) != 1 {
		return nil, fmt.Errorf("gqlgraph: type %s has %d key columns, and a Node has one", c.Name, len(c.Key))
	}
	t, col := c.table(), c.Key[0].Index
	keys := make([]any, len(ids)) // each ID as the key column's Go type; nil for none
	var in []any                  // the distinct keys
	seen := map[any]bool{}
	for i, id := range ids {
		k, err := t.Convert(col, id)
		if err != nil || k == nil {
			continue // names no row of the type
		}
		if !reflect.TypeOf(k).Comparable() {
			return loadEach(ctx, db, c, ids)
		}
		keys[i] = k
		if !seen[k] {
			seen[k] = true
			in = append(in, k)
		}
	}
	out := make([]any, len(ids))
	if len(in) == 0 {
		return out, nil
	}
	rows, err := untyped.Query(db, t).Where(t.Pred(col, untyped.PredIn, in)).Limit(len(in)).All(ctx)
	switch {
	case errors.Is(err, velox.ErrDenied):
		return out, nil
	case err != nil:
		return nil, err
	}
	byKey := make(map[any]any, rows.Len())
	group := make([]any, rows.Len())
	for i := range rows.Len() {
		r := rows.At(i)
		byKey[t.Value(r, col)] = r
		group[i] = r
	}
	for i, k := range keys {
		if k != nil {
			out[i] = byKey[k]
		}
	}
	gqlcore.Add(ctx, group)
	return out, nil
}

// loadEach is loadNodes by LoadNode of each ID, for a key whose Go type a
// map cannot hold.
func loadEach(ctx context.Context, db *velox.DB, c *Type, ids []any) ([]any, error) {
	out := make([]any, len(ids))
	for i, id := range ids {
		n, err := LoadNode(ctx, db, c, id)
		if err != nil {
			return nil, err
		}
		out[i] = n
	}
	return out, nil
}
