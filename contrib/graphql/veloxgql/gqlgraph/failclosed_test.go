package gqlgraph_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

// TestNoSelectionSource: in a context no adapter made, a connection of a
// query fails with ErrNoSelectionSource, and ErrNoDB, though the query has
// its own DB, and sends no statement; so do the edges (a connection of an
// edge, a to-one edge and a list edge) of a row built by hand, which has no
// DB of its own, with velox.ErrNoSource and ErrNoDB. A context an adapter
// made reads, and so does one veloxgql.Service made. (An edge of a row a
// statement read reads on its DB: TestEdgeReadsOnItsRowsDB.)
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
		want := velox.ErrNoSource
		if name == "Paginate of a query" {
			want = veloxgql.ErrNoSelectionSource
		}
		if !errors.Is(err, want) || !errors.Is(err, veloxgql.ErrNoDB) || !strings.Contains(err.Error(), "gqlgenx.NewExtension or graphqlgo.With") {
			t.Errorf("%s with no source: %v, want %v and ErrNoDB, naming both adapters", name, err, want)
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

// TestEdgeReadsOnItsRowsDB: an edge of a row a statement read reads on the
// DB that read it: with no adapter, one statement for the row, every
// column; in an adapter's context whose DB is another, still on the row's.
func TestEdgeReadsOnItsRowsDB(t *testing.T) {
	db, _, log := edgeDB(t)
	other, _, _ := edgeDB(t)
	if _, err := other.Exec(context.Background(), "test", "DELETE FROM children"); err != nil {
		t.Fatal(err)
	}
	bare := context.Background()
	o, err := db.Query[owner]().Where(owners.ID.EQ(1)).First(bare)
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.Query[child]().Where(children.ID.EQ(5)).First(bare)
	if err != nil {
		t.Fatal(err)
	}
	childType := gqlgraph.TableType[child]("Child")
	keyed, _ := gqlgraph.TypeOf("Child", nil, childKey())
	childType.Key = keyed.Key
	for name, ctx := range map[string]context.Context{"no adapter": bare, "another DB's adapter": gqlgenx.NewExtension(other).NewContext(bare)} {
		log.take("")
		tags, err := gqlgraph.EdgeListOf(ctx, o, owners.Tags, "Owner.tags", 0)
		if err != nil || len(tags) != 2 {
			t.Errorf("%s: EdgeList: %v, %v", name, tags, err)
		}
		up, err := gqlgraph.EdgeOneOf(ctx, c, children.Owner, "Child.owner")
		if err != nil || up == nil || up.ID != 2 {
			t.Errorf("%s: EdgeOne: %v, %v", name, up, err)
		}
		conn, err := gqlgraph.Paginate(ctx, gqlgraph.EdgeOf(o, owners.Children, "Owner.children"), childType, gqlgraph.Args{})
		if err == nil {
			var p *gqlgraph.Page
			if p, err = conn.Page(ctx); err == nil && len(p.Rows) != 4 {
				t.Errorf("%s: the edge's connection read %d rows, want 4", name, len(p.Rows))
			}
		}
		if err != nil {
			t.Errorf("%s: Paginate of an edge: %v", name, err)
		}
		// One statement each, on the rows' DB: the tags (the join table
		// and the target in one), the owner, and the page.
		if sent := log.sent(); len(sent) != 3 {
			t.Errorf("%s: sent %q, want 3 statements", name, sent)
		}
	}
}
