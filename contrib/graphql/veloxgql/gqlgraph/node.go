package gqlgraph

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/untyped"
)

// The errors of ResolveNode, under velox-graphql's option compat=veloxe,
// whose generated ResolveNode and ResolveNodes call it. Test for them with
// errors.Is.
var (
	// ErrNodeNotFound: no type of Node has a row of the ID that the caller
	// may read. Its text is veloxe's. ResolveNode returns it in an error
	// with the code NOT_FOUND, which veloxgql.ErrorPresenter shows the client.
	ErrNodeNotFound = errors.New("could not resolve to a node with the global ID")
	// ErrAmbiguousNodeID: more than one type of Node has a row of the ID, as
	// veloxe refused it. A Node's ID holds no type, so node(id:) needs IDs
	// that are unique across tables, as a snowflake is.
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
	return nil, fmt.Errorf("gqlgraph: %w: the ID is a %s; node(id:) needs IDs unique across types", ErrAmbiguousNodeID, strings.Join(found, " and a "))
}

// ResolveNodes returns the row of each of ids, in order, as veloxe's
// Client.Noders did: ResolveNode of each, with nil for an ID that no type
// has, as Relay's nodes(ids:) asks. Any other error stops it and is
// returned. As in veloxe, the number of ids is not capped: an application
// that needs a cap checks len(ids) in its resolver.
func ResolveNodes[K any](ctx context.Context, db *velox.DB, types []*Type, ids []K) ([]any, error) {
	nodes := make([]any, len(ids))
	for i, id := range ids {
		n, err := ResolveNode(ctx, db, types, id)
		switch {
		case errors.Is(err, ErrNodeNotFound):
			continue
		case err != nil:
			return nil, err
		}
		nodes[i] = n
	}
	return nodes, nil
}
