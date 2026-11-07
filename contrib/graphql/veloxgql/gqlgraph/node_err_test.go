package gqlgraph_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
)

// personKinds is person as a Node type of the ID type "Person", though
// person declares no IsNode: the generated code never makes it.
func personKinds(decodeErr error) func(string) (gqlgraph.NodeKind, bool) {
	return func(typ string) (gqlgraph.NodeKind, bool) {
		c := gqlgraph.InputType[person]("Person", int(people.ID.I), nil)
		if decodeErr != nil {
			return gqlgraph.NewNodeWith(c, func(string) (any, error) { return nil, decodeErr }), typ == "Person"
		}
		return gqlgraph.NewNode[int64](c), typ == "Person"
	}
}

// TestNodeErrors: a lookup fails, with no row read, without a DB in the
// context, on an ID its kind does not decode, and on an entity that is no
// Node.
func TestNodeErrors(t *testing.T) {
	db, _ := peopleDB(t)
	ctx := veloxgql.NewExtension(db).NewContext(context.Background())
	id := veloxgql.EncodeID("Person", int64(1))
	bad := errors.New("bad key")
	one := func(ctx context.Context, kinds func(string) (gqlgraph.NodeKind, bool)) error {
		_, err := gqlgraph.NodeByID(ctx, id, kinds)
		return err
	}
	many := func(ctx context.Context, kinds func(string) (gqlgraph.NodeKind, bool)) error {
		_, err := gqlgraph.NodesByID(ctx, []string{id}, kinds)
		return err
	}
	for name, f := range map[string]func(context.Context, func(string) (gqlgraph.NodeKind, bool)) error{"NodeByID": one, "NodesByID": many} {
		if err := f(context.Background(), personKinds(nil)); err == nil {
			t.Errorf("%s with no DB: no error", name)
		}
		if err := f(ctx, personKinds(bad)); !errors.Is(err, bad) {
			t.Errorf("%s, a key that does not decode: %v", name, err)
		}
		if err := f(ctx, personKinds(nil)); err == nil || !strings.Contains(err.Error(), "is no Node") {
			t.Errorf("%s of an entity that is no Node: %v", name, err)
		}
	}
}
