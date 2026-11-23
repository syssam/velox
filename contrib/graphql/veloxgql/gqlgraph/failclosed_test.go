package gqlgraph_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

// TestNoSelectionSource: every read of generated code fails with
// ErrNoSelectionSource, and ErrNoDB, in a context no adapter made, and sends
// no statement: a connection of a query, though the query has its own DB,
// a connection of an edge, a to-one edge and a list edge. Nothing falls
// back to reading every column alone. A context an adapter made reads, and
// so does one veloxgql.Service made.
func TestNoSelectionSource(t *testing.T) {
	db, _, log := edgeDB(t)
	bare := context.Background()
	kids := gqlgraph.EdgeOf(&owner{ID: 1}, owners.Children, "Owner.children")
	childType := gqlgraph.TableType[child]("Child")
	keyed, _ := gqlgraph.TypeOf("Child", nil, childKey())
	childType.Key = keyed.Key
	reads := map[string]func(context.Context) error{
		"Paginate of a query": func(ctx context.Context) error {
			c, err := gqlgraph.Paginate(ctx, gqlgraph.Query(untyped.Query(db, untyped.TableOf[child]())), childType, gqlgraph.Args{})
			if err == nil {
				_, err = c.Page(ctx)
			}
			return err
		},
		"Paginate of an edge": func(ctx context.Context) error {
			c, err := gqlgraph.Paginate(ctx, kids, childType, gqlgraph.Args{})
			if err == nil {
				_, err = c.Page(ctx)
			}
			return err
		},
		"EdgeOne": func(ctx context.Context) error {
			_, err := gqlgraph.EdgeOneOf(ctx, &child{ID: 1, OwnerID: new(int64(1))}, children.Owner, "Child.owner")
			return err
		},
		"EdgeList": func(ctx context.Context) error {
			_, err := gqlgraph.EdgeListOf(ctx, &owner{ID: 1}, owners.Tags, "Owner.tags", 0)
			return err
		},
	}
	adapted := gqlgenx.NewExtension(db).NewContext(bare)
	service := veloxgql.Service(bare, db)
	for name, read := range reads {
		log.take("")
		err := read(bare)
		if !errors.Is(err, veloxgql.ErrNoSelectionSource) || !errors.Is(err, veloxgql.ErrNoDB) || !strings.Contains(err.Error(), "gqlgenx.NewExtension or graphqlgo.With") {
			t.Errorf("%s with no source: %v, want ErrNoSelectionSource and ErrNoDB, naming both adapters", name, err)
		}
		if sent := log.sent(); len(sent) != 0 {
			t.Errorf("%s with no source sent %q", name, sent)
		}
		if err := read(adapted); err != nil {
			t.Errorf("%s in an adapter's context: %v", name, err)
		}
		if err := read(service); err != nil {
			t.Errorf("%s in veloxgql.Service's context: %v", name, err)
		}
	}
}

// sent is every statement since the last take, of any table, and forgets
// them.
func (l *edgeLog) sent() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var all []string
	for _, s := range l.sql {
		all = append(all, s...)
	}
	l.sql = map[string][]string{}
	return all
}

// TestNilConnection: the nil *Connection, the zero connection type of
// generated code, is the empty connection: no rows, no page info, and a
// totalCount of 0, with no statement.
func TestNilConnection(t *testing.T) {
	var c *gqlgraph.Connection
	p, err := c.Page(context.Background())
	if err != nil || p == nil || len(p.Rows) != 0 || len(p.Cursors) != 0 || p.Info != (veloxgql.PageInfo{}) {
		t.Errorf("Page of nil = %+v, %v", p, err)
	}
	if n, err := c.TotalCount(context.Background()); n != 0 || err != nil {
		t.Errorf("TotalCount of nil = %d, %v", n, err)
	}
}
