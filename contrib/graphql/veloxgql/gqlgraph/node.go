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

// The errors of ResolveNode, under velox-graphql's option node=on,
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
// It is the read ResolveNode makes of each type its lookup finds; an
// application that
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

// A NodeOption configures ResolveNode and ResolveNodes, as entgql's
// NodeOption configures its Noder. WithNodeType and WithFixedNodeType give
// an ID's type, so that no statement looks it up. A later option replaces
// an earlier one.
type NodeOption func(*nodeOptions)

// nodeOptions are the NodeOptions of one call.
type nodeOptions struct {
	typeOf func(ctx context.Context, id any) (string, error) // nil: look every ID up
}

// WithNodeType gives the type of an ID: f returns the GraphQL name of the
// type of Node that holds id, as entgql's WithNodeType does. ResolveNode
// and ResolveNodes then read only that type, under its policies, and run
// no lookup for the ID. An empty name means f does not know the type, and
// the ID is looked up as with no option. A name that is no type of Node
// fails the call before any statement, with an error that names it. An
// error from f is returned as it is. f is for IDs that carry their type,
// as a prefixed ID or one from its type's own range does; f decides what
// a client's ID may name. K is the key's Go type; an ID of another type
// is an error.
func WithNodeType[K any](f func(ctx context.Context, id K) (string, error)) NodeOption {
	return func(o *nodeOptions) {
		o.typeOf = func(ctx context.Context, id any) (string, error) {
			k, ok := id.(K)
			if !ok {
				return "", fmt.Errorf("gqlgraph: WithNodeType takes IDs of type %v, and is given a %T", reflect.TypeFor[K](), id)
			}
			return f(ctx, k)
		}
	}
}

// WithFixedNodeType gives every ID the type name, the GraphQL name of a type
// of Node, as entgql's WithFixedNodeType does: ResolveNode and ResolveNodes
// read only that type, with no lookup. It is WithNodeType of a function
// that always returns name.
func WithFixedNodeType(name string) NodeOption {
	return func(o *nodeOptions) {
		o.typeOf = func(context.Context, any) (string, error) { return name, nil }
	}
}

// typeIndex returns the index in types of the type o gives id, or -1 when
// o gives none.
func (o *nodeOptions) typeIndex(ctx context.Context, types []*Type, id any) (int, error) {
	if o.typeOf == nil {
		return -1, nil
	}
	name, err := o.typeOf(ctx, id)
	switch {
	case err != nil:
		return 0, err
	case name == "":
		return -1, nil
	}
	for i, c := range types {
		if c.Name == name {
			return i, nil
		}
	}
	return 0, fmt.Errorf("gqlgraph: the node type of an ID is %q, which is no type of Node", name)
}

// nodeOptionsOf applies opts.
func nodeOptionsOf(opts []NodeOption) nodeOptions {
	var o nodeOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// ResolveNode returns the one row of the types that has the key id, as
// veloxe's Client.Noder did. One statement finds which types hold the key,
// reading keys only, under no policy; then LoadNode reads each type found,
// in the order given, so a row that a policy hides or denies is none, as
// is one that is not there. A type whose key is not an integer or a string
// type, or has a codec, is read whatever the lookup finds. It fails with
// ErrNodeNotFound (NOT_FOUND) when no type has a row the caller may read,
// with ErrAmbiguousNodeID when more than one does, naming them and not the
// ID, and with the first other error a statement returns.
//
// When an option gives id's type (WithNodeType, WithFixedNodeType), it
// runs no lookup and reads that type alone, with LoadNode: a row that is
// not there, hidden or denied is ErrNodeNotFound.
func ResolveNode(ctx context.Context, db *velox.DB, types []*Type, id any, opts ...NodeOption) (any, error) {
	o := nodeOptionsOf(opts)
	at, err := o.typeIndex(ctx, types, id)
	if err != nil {
		return nil, err
	}
	if at >= 0 {
		n, err := LoadNode(ctx, db, types[at], id)
		switch {
		case err != nil:
			return nil, err
		case n == nil:
			return nil, gqlcore.NotFoundf("%w", ErrNodeNotFound)
		}
		return n, nil
	}
	read, err := lookupNodes(ctx, db, types, []any{id})
	if err != nil {
		return nil, err
	}
	var node any
	var found []string
	for i, c := range types {
		if !read[i] {
			continue
		}
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
// nodes(ids:) asks. One statement finds which types hold any of the IDs,
// as ResolveNode's does; then it reads each type found once, with one IN
// query of every ID its key column can hold, under the type's policies, as
// entgql's Noders does: one statement per type found, however many IDs.
// Each type's rows are one batch (veloxgql.Batch), so an edge of theirs is
// read for all of them in one statement.
//
// An ID whose type an option gives (WithNodeType, WithFixedNodeType) is
// read in that type alone and not looked up; when every ID's type is
// given, no lookup runs. The options are asked for every ID before any
// statement, so a name that is no type of Node fails the call first.
//
// More IDs than the MaxPage of veloxgql.LimitsFromContext(ctx) is
// veloxgql.ErrPageTooLarge, BAD_USER_INPUT, before any statement. An ID
// that more than one type has fails the whole list with ErrAmbiguousNodeID,
// as ResolveNode fails; so does any other error.
func ResolveNodes[K any](ctx context.Context, db *velox.DB, types []*Type, ids []K, opts ...NodeOption) ([]any, error) {
	if most := veloxgql.LimitsFromContext(ctx).MaxPage; len(ids) > most {
		return nil, gqlcore.InputErrorf("%w: nodes(ids:) takes at most %d IDs", veloxgql.ErrPageTooLarge, most)
	}
	xs := make([]any, len(ids))
	for i, id := range ids {
		xs[i] = id
	}
	return resolveNodes(ctx, db, types, xs, nodeOptionsOf(opts))
}

// resolveNodes is ResolveNodes of ids, already capped.
func resolveNodes(ctx context.Context, db *velox.DB, types []*Type, ids []any, o nodeOptions) ([]any, error) {
	given := make([]int, len(ids)) // the index of each ID's type an option gives, or -1
	var unknown []any              // the IDs no option gives a type
	for i, id := range ids {
		at, err := o.typeIndex(ctx, types, id)
		if err != nil {
			return nil, err
		}
		given[i] = at
		if at < 0 {
			unknown = append(unknown, id)
		}
	}
	var read []bool // by type, whether to read it for the unknown IDs
	if len(unknown) > 0 {
		var err error
		if read, err = lookupNodes(ctx, db, types, unknown); err != nil {
			return nil, err
		}
	}
	nodes := make([]any, len(ids))
	found := make([][]string, len(ids)) // the types that have each ID
	ambiguous := -1                     // the first ID of two types
	for j, c := range types {
		at := idsOfType(given, j, read != nil && read[j])
		if len(at) == 0 {
			continue
		}
		some := ids
		if len(at) < len(ids) {
			some = make([]any, len(at))
			for k, i := range at {
				some[k] = ids[i]
			}
		}
		rows, err := loadNodes(ctx, db, c, some)
		if err != nil {
			return nil, err
		}
		for k, r := range rows {
			if r == nil {
				continue
			}
			i := at[k]
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

// idsOfType returns the indexes of the IDs type j reads: those an option
// gives it, and with unknown those no option gives a type.
func idsOfType(given []int, j int, unknown bool) []int {
	var at []int
	for i, g := range given {
		if g == j || g < 0 && unknown {
			at = append(at, i)
		}
	}
	return at
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
