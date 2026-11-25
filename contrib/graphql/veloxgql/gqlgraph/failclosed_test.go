package gqlgraph_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

// TestNoSetup (decision 0173): in a context nothing was put in, outside any
// GraphQL field, a connection of a query reads on the query's DB, every
// column, in one statement; the edges (a connection of an edge, a to-one
// edge and a list edge) of a row built by hand, which has no DB of its own,
// fail with velox.ErrNoSource and send no statement. (An edge of a row a
// statement read reads on its DB: TestEdgeReadsOnItsRowsDB.)
func TestNoSetup(t *testing.T) {
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
	for name, read := range reads {
		log.take("")
		err := read(bare)
		if name == "Paginate of a query" {
			const want = `SELECT "id", "owner_id", "title", "editor_id" FROM "children" ORDER BY "id" LIMIT 101`
			if sent := log.sent(); err != nil || len(sent) != 1 || sent[0] != want {
				t.Errorf("%s with no setup: %v, sent %q; want one statement, %s", name, err, sent, want)
			}
		} else if !errors.Is(err, velox.ErrNoSource) {
			t.Errorf("%s of a row built by hand: %v, want ErrNoSource", name, err)
		} else if sent := log.sent(); len(sent) != 0 {
			t.Errorf("%s of a row built by hand sent %q", name, sent)
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
// DB that read it, never on another DB, a context's or a connection's: with
// no field, one statement for the row, every column.
func TestEdgeReadsOnItsRowsDB(t *testing.T) {
	db, _, log := edgeDB(t)
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
	for name, ctx := range map[string]context.Context{"no field": bare, "a gqlgen field": field(t, operation(t, childrenQuery), "owners")} {
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
			if err == nil && len(p.Rows) > 0 && untyped.SourceDB(untyped.TableOf[child](), p.Rows[0]) != db {
				t.Errorf("%s: a node of the page does not carry the DB that read it", name)
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
