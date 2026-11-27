package gqlgraph_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	_ "github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx" // registers the gqlgen reader
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	gqlcore "github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/untyped"
)

// owner, child, tag and ownerTag are hand-written entities, as velox-gen
// would write them, for the edges' tests: an owner has children (its
// one-to-many edge, whose inverse is a child's owner) and tags, through
// ownerTag (many-to-many).
type owner struct {
	ID       int64
	Name     string
	Age      int64
	Children velox.Many[child]
	Tags     velox.Many[tag]
	source   velox.Source
}

var owners = struct {
	ID       velox.NumberCol[owner, int64]
	Name     velox.StringCol[owner, string]
	Age      velox.NumberCol[owner, int64]
	Children velox.Rel[owner, child]
	Tags     velox.Rel[owner, tag]
}{
	velox.NumberCol[owner, int64]{I: 0}, velox.StringCol[owner, string]{I: 1}, velox.NumberCol[owner, int64]{I: 2},
	velox.Rel[owner, child]{I: 0}, velox.Rel[owner, tag]{I: 1},
}

var ownerMeta velox.MetaOnce[owner]

func (*owner) VeloxMeta() *velox.Meta[owner] { return ownerMeta.Load(ownerDescriptor) }

func ownerDescriptor() velox.Descriptor[owner] {
	return velox.Descriptor[owner]{
		Table:      "owners",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}, {Field: "Age", Name: "age"}},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[child]("children", 0, 1), velox.EdgeThrough[ownerTag, tag]("tags", 0, 0, 1, 0)},
		FieldPtr:   func(x *owner, i int) any { return [...]any{&x.ID, &x.Name, &x.Age}[i] },
		SourcePtr:  func(x *owner) *velox.Source { return &x.source },
		EdgeFieldPtr: func(x *owner, i int) any {
			switch i {
			case 0:
				return &x.Children
			case 1:
				return &x.Tags
			}
			return nil
		},
	}
}

type child struct {
	ID       int64
	OwnerID  *int64
	Title    string
	EditorID *int64
	Owner    velox.One[owner]
	Editor   velox.One[owner]
	source   velox.Source
}

var children = struct {
	ID       velox.NumberCol[child, int64]
	OwnerID  velox.NullNumberCol[child, int64]
	Title    velox.StringCol[child, string]
	EditorID velox.NullNumberCol[child, int64]
	Owner    velox.Rel[child, owner]
	Editor   velox.Rel[child, owner]
}{
	velox.NumberCol[child, int64]{I: 0}, velox.NullNumberCol[child, int64]{I: 1}, velox.StringCol[child, string]{I: 2}, velox.NullNumberCol[child, int64]{I: 3},
	velox.Rel[child, owner]{I: 0}, velox.Rel[child, owner]{I: 1},
}

var childMeta velox.MetaOnce[child]

func (*child) VeloxMeta() *velox.Meta[child] { return childMeta.Load(childDescriptor) }

func childDescriptor() velox.Descriptor[child] {
	return velox.Descriptor[child]{
		Table:      "children",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OwnerID", Name: "owner_id"}, {Field: "Title", Name: "title"}, {Field: "EditorID", Name: "editor_id"}},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[owner]("owner", 1, 0), velox.EdgeTo[owner]("editor", 3, 0)},
		FieldPtr:   func(x *child, i int) any { return [...]any{&x.ID, &x.OwnerID, &x.Title, &x.EditorID}[i] },
		SourcePtr:  func(x *child) *velox.Source { return &x.source },
		EdgeFieldPtr: func(x *child, i int) any {
			switch i {
			case 0:
				return &x.Owner
			case 1:
				return &x.Editor
			}
			return nil
		},
	}
}

type tag struct {
	ID   int64
	Name string
}

var tagMeta velox.MetaOnce[tag]

func (*tag) VeloxMeta() *velox.Meta[tag] { return tagMeta.Load(tagDescriptor) }

func tagDescriptor() velox.Descriptor[tag] {
	return velox.Descriptor[tag]{
		Table:      "tags",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		FieldPtr:   func(x *tag, i int) any { return [...]any{&x.ID, &x.Name}[i] },
	}
}

type ownerTag struct {
	OwnerID int64
	TagID   int64
}

var ownerTagMeta velox.MetaOnce[ownerTag]

func (*ownerTag) VeloxMeta() *velox.Meta[ownerTag] { return ownerTagMeta.Load(ownerTagDescriptor) }

func ownerTagDescriptor() velox.Descriptor[ownerTag] {
	return velox.Descriptor[ownerTag]{
		Table:      "owner_tags",
		Columns:    []velox.ColumnDescriptor{{Field: "OwnerID", Name: "owner_id"}, {Field: "TagID", Name: "tag_id"}},
		PrimaryKey: []int{0, 1},
		FieldPtr:   func(x *ownerTag, i int) any { return [...]any{&x.OwnerID, &x.TagID}[i] },
	}
}

// The order terms, as the generated code makes them.
func ownerKey() []gqlgraph.OrderTerm[owner] {
	return []gqlgraph.OrderTerm[owner]{gqlgraph.TermOf[owner]("id", veloxgql.OrderDirectionAsc, int(owners.ID.I))}
}

func childKey() []gqlgraph.OrderTerm[child] {
	return []gqlgraph.OrderTerm[child]{gqlgraph.TermOf[child]("id", veloxgql.OrderDirectionAsc, int(children.ID.I))}
}

func titleOrder() []gqlgraph.OrderTerm[child] {
	return []gqlgraph.OrderTerm[child]{gqlgraph.TermOf[child]("TITLE", veloxgql.OrderDirectionAsc, int(children.Title.I))}
}

// edgeSDL is the schema the edges' operations are parsed against.
const edgeSDL = `
interface Keyed { id: ID! }
interface Named { name: String! }
union Thing = Owner | Tag
type Query {
	owners(first: Int): OwnerConnection!
	owner: Owner
	children: ChildConnection!
	things: [Thing!]!
	other: Other
}
type Owner implements Keyed & Named {
	id: ID!
	name: String!
	age: Int!
	children(first: Int, last: Int, after: String, before: String, title: String): ChildConnection!
	tags: [Tag!]!
}
type Child implements Keyed {
	id: ID!
	title: String!
	owner: Owner
	editor: Owner
}
type Tag implements Keyed {
	id: ID!
	name: String!
}
type Other { name: String! }
type OwnerConnection { edges: [OwnerEdge!]! pageInfo: PageInfo! totalCount: Int! nodes: [Owner!]! }
type OwnerEdge { node: Owner! cursor: String! extra: String }
type ChildConnection { edges: [ChildEdge!]! pageInfo: PageInfo! totalCount: Int! }
type ChildEdge { node: Child! cursor: String! }
type PageInfo { hasNextPage: Boolean! hasPreviousPage: Boolean! }
`

var edgeSchema = gqlparser.MustLoadSchema(&ast.Source{Name: "edge.graphql", Input: edgeSDL})

// edgeLog records the statements a DB runs, by table, and fails those fail
// returns an error for.
type edgeLog struct {
	mu   sync.Mutex
	sql  map[string][]string
	fail func(*velox.Statement) error
}

func (l *edgeLog) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	l.mu.Lock()
	l.sql[st.Entity.Table] = append(l.sql[st.Entity.Table], st.SQL)
	fail := l.fail
	l.mu.Unlock()
	if fail != nil {
		if err := fail(st); err != nil {
			return err
		}
	}
	return next(ctx)
}

// take returns the SQL of table's statements since the last take, and
// forgets every table's.
func (l *edgeLog) take(table string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.sql[table]
	l.sql = map[string][]string{}
	return s
}

// edgeDB opens an in-memory SQLite database holding:
//   - owners 1 "a", 2 "b" and 3 "c";
//   - children (id: owner, title) 1: 1 "d", 2: 1 "a", 3: 1 "c", 4: 1 "b",
//     5: 2 "b", 6: 2 "a", and 7 of no owner, "z"; children 1 to 4 have the
//     editor 3, 5 and 6 the editor 1, and 7 none;
//   - tags 1 "go", 2 "db" and 3 "zz"; owner 1 has tags 1 and 2, owner 2 tag
//     2, owner 3 none.
func edgeDB(t *testing.T, opts ...velox.Option) (*velox.DB, *sql.DB, *edgeLog) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	for _, s := range []string{
		`CREATE TABLE owners (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`,
		`CREATE TABLE children (id INTEGER PRIMARY KEY, owner_id INTEGER, title TEXT NOT NULL, editor_id INTEGER)`,
		`CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE owner_tags (owner_id INTEGER NOT NULL, tag_id INTEGER NOT NULL, PRIMARY KEY (owner_id, tag_id))`,
		`INSERT INTO owners (id, name, age) VALUES (1, 'a', 30), (2, 'b', 20), (3, 'c', 10)`,
		`INSERT INTO children (id, owner_id, title) VALUES (1, 1, 'd'), (2, 1, 'a'), (3, 1, 'c'), (4, 1, 'b'), (5, 2, 'b'), (6, 2, 'a'), (7, NULL, 'z')`,
		`INSERT INTO tags (id, name) VALUES (1, 'go'), (2, 'db'), (3, 'zz')`,
		`INSERT INTO owner_tags (owner_id, tag_id) VALUES (1, 1), (1, 2), (2, 2)`,
		`UPDATE children SET editor_id = CASE WHEN owner_id = 1 THEN 3 ELSE 1 END WHERE owner_id IS NOT NULL`,
	} {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	l := &edgeLog{sql: map[string][]string{}}
	return velox.Open(velox.SQLite, raw, append([]velox.Option{velox.WithAround(l.around)}, opts...)...), raw, l
}

// operation returns ctx inside the operation query, parsed against
// edgeSchema: the context gqlgen gives the operation's resolvers. The
// registered gqlgen reader keeps the response's table for each root field
// that field (below) opens.
func operation(t *testing.T, query string) context.Context {
	t.Helper()
	doc, errs := gqlparser.LoadQueryWithRules(edgeSchema, query, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	return graphql.WithOperationContext(context.Background(), &graphql.OperationContext{RawQuery: query, Doc: doc, Operation: doc.Operations[0], Variables: map[string]any{}})
}

// fieldKey names the field context of a path in an operation.
type fieldKey struct {
	oc   *graphql.OperationContext
	path string
}

// fieldContexts are the field contexts field made, so that every call with
// one path, or a path under it, holds the one root field context, whose
// table the reader keeps.
var fieldContexts sync.Map

// field returns ctx inside the field at path, each step a response key, as
// gqlgen makes a field's context: the fields collected through fragments of
// every type, each with its parent. A path of one operation has one field
// context, so the fields of a root share the table of its response.
func field(t *testing.T, ctx context.Context, path ...string) context.Context {
	t.Helper()
	oc := graphql.GetOperationContext(ctx)
	sels := oc.Operation.SelectionSet
	var parent *graphql.FieldContext
	for i, key := range path {
		var found *graphql.CollectedField
		for _, f := range graphql.CollectFields(oc, sels, nil) {
			if f.Alias == key {
				found = &f
				break
			}
		}
		if found == nil {
			t.Fatalf("the operation has no field %s", strings.Join(path[:i+1], "."))
		}
		k := fieldKey{oc, strings.Join(path[:i+1], ".")}
		fc, ok := fieldContexts.Load(k)
		if !ok {
			fc, _ = fieldContexts.LoadOrStore(k, &graphql.FieldContext{Parent: parent, Object: found.ObjectDefinition.Name, Field: *found, IsMethod: true, IsResolver: true})
		}
		parent = fc.(*graphql.FieldContext)
		ctx = graphql.WithFieldContext(ctx, parent)
		sels = found.Selections
	}
	return ctx
}

// ownersPage reads the page of every owner as the root field owners does:
// the rows become a group.
func ownersPage(t *testing.T, ctx context.Context, db *velox.DB, q velox.Query[owner], args gqlgraph.Args) []*owner {
	t.Helper()
	ctx = field(t, ctx, "owners")
	c, err := gqlgraph.NewConnectionOf(ctx, q, "Owner", args, nil, ownerKey())
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Page(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return p.Rows
}

// childPage is x's page of the edge children, made at ctx (a field of the
// edge, or not), and its totalCount. The edge's arguments are args, and
// those of q are in more, if any.
func childPage(t *testing.T, ctx context.Context, db *velox.DB, x *owner, q velox.Query[child], args gqlgraph.Args, order []gqlgraph.OrderTerm[child], more ...any) (*gqlgraph.PageOf[child], int) {
	t.Helper()
	c, err := gqlgraph.NewConnectionOf(ctx, q, "Child", args, order, childKey())
	if err != nil {
		t.Fatal(err)
	}
	c = gqlgraph.EdgeConnectionOf(ctx, c, x, owners.Children, "Owner.children", []any{args, more})
	p, err := c.Page(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err := c.TotalCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return p, n
}

// ownPage is x's page of the children of q, read alone: the query of the
// connection filtered by Of, as before batching.
func ownPage(t *testing.T, db *velox.DB, x *owner, q velox.Query[child], args gqlgraph.Args, order []gqlgraph.OrderTerm[child]) (*gqlgraph.PageOf[child], int) {
	t.Helper()
	ctx := context.Background()
	c, err := gqlgraph.NewConnectionOf(ctx, q.Where(owners.Children.Of(x)), "Child", args, order, childKey())
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Page(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n, err := c.TotalCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return p, n
}

func childIDs(rows []*child) []int64 {
	out := []int64{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// samePage fails t when got is not want: its rows, cursors and page info.
func samePage(t *testing.T, name string, got, want *gqlgraph.PageOf[child]) {
	t.Helper()
	if g, w := childIDs(got.Rows), childIDs(want.Rows); !reflect.DeepEqual(g, w) {
		t.Errorf("%s: rows %v, want %v", name, g, w)
	}
	for i := range min(len(got.Rows), len(want.Rows)) {
		if !reflect.DeepEqual(*got.Rows[i], *want.Rows[i]) {
			t.Errorf("%s: row %d is %+v, want %+v", name, i, *got.Rows[i], *want.Rows[i])
		}
	}
	if !reflect.DeepEqual(got.Cursors, want.Cursors) {
		t.Errorf("%s: cursors %+v, want %+v", name, got.Cursors, want.Cursors)
	}
	if !reflect.DeepEqual(got.Info, want.Info) {
		t.Errorf("%s: page info %+v, want %+v", name, got.Info, want.Info)
	}
}

// oneEach checks that sql is one statement that contains each of has.
func oneEach(t *testing.T, name string, sql []string, has ...string) {
	t.Helper()
	if len(sql) != 1 {
		t.Fatalf("%s: %d statements, want 1: %q", name, len(sql), sql)
	}
	for _, h := range has {
		if !strings.Contains(sql[0], h) {
			t.Errorf("%s: %s, want it to contain %s", name, sql[0], h)
		}
	}
}

const childrenQuery = `{ owners { edges { node { children(first: 2) { totalCount edges { node { title owner { name } } } } } } } }`

// TestEdgeConnectionBatches: the edge of a page's three owners is one
// windowed load and one grouped count, each owner's page and totalCount
// those of its own query, and the rows read are a group: their own edge is
// one more statement.
func TestEdgeConnectionBatches(t *testing.T) {
	db, _, log := edgeDB(t)
	ctx := operation(t, childrenQuery)
	xs := ownersPage(t, ctx, db, db.Query[owner](), gqlgraph.Args{})
	log.take("owners")
	ectx := field(t, ctx, "owners", "edges", "node", "children")
	args := gqlgraph.Args{First: new(2)}
	var loads, counts []string
	var rows []*child
	for _, x := range xs {
		got, n := childPage(t, ectx, db, x, db.Query[child](), args, titleOrder())
		sql := log.take("children")
		for _, s := range sql {
			if strings.Contains(s, "COUNT(") {
				counts = append(counts, s)
			} else {
				loads = append(loads, s)
			}
		}
		want, wn := ownPage(t, db, x, db.Query[child](), args, titleOrder())
		log.take("children")
		samePage(t, x.Name, got, want)
		if n != wn {
			t.Errorf("%s: totalCount %d, want %d", x.Name, n, wn)
		}
		rows = append(rows, got.Rows...)
	}
	oneEach(t, "the load", loads, "ROW_NUMBER()")
	oneEach(t, "the count", counts, "GROUP BY")
	if got := childIDs(rows); !reflect.DeepEqual(got, []int64{2, 4, 6, 5}) {
		t.Fatalf("rows %v", got)
	}
	// The pages' rows are one group: each child's owner is one statement.
	octx := field(t, ctx, "owners", "edges", "node", "children", "edges", "node", "owner")
	for _, x := range rows {
		o, err := gqlgraph.EdgeOneOf(octx, x, children.Owner, "Child.owner")
		if err != nil {
			t.Fatal(err)
		}
		if o == nil || o.ID != *x.OwnerID {
			t.Errorf("child %d: owner %+v", x.ID, o)
		}
	}
	if sql := log.take("owners"); len(sql) != 1 {
		t.Errorf("the owners of the children: %d statements, want 1: %q", len(sql), sql)
	}
}

// TestEdgeConnectionBackwardAndAfter: last, after, before, a window between
// two cursors and a page of none batch too, each owner's page that of its own query.
func TestEdgeConnectionBackwardAndAfter(t *testing.T) {
	db, _, log := edgeDB(t)
	first, _ := ownPage(t, db, &owner{ID: 1}, db.Query[child](), gqlgraph.Args{}, titleOrder())
	// a(2), b(4), c(3), d(1) for owner 1.
	for _, c := range []struct {
		name string
		args gqlgraph.Args
	}{
		{"last", gqlgraph.Args{Last: new(2)}},
		{"after", gqlgraph.Args{First: new(2), After: wire(t, first.Cursors[0])}},
		{"before", gqlgraph.Args{Last: new(1), Before: wire(t, first.Cursors[2])}},
		// A window between two cursors, with first, with last and with neither.
		{"window", gqlgraph.Args{After: wire(t, first.Cursors[0]), Before: wire(t, first.Cursors[3])}},
		{"first and before", gqlgraph.Args{First: new(1), Before: wire(t, first.Cursors[2])}},
		{"last and after", gqlgraph.Args{Last: new(1), After: wire(t, first.Cursors[0])}},
		// No rows, and whether any are past them: one row read for each owner.
		{"first 0", gqlgraph.Args{First: new(0)}},
		{"last 0", gqlgraph.Args{Last: new(0)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := operation(t, childrenQuery)
			xs := ownersPage(t, ctx, db, db.Query[owner](), gqlgraph.Args{})
			ectx := field(t, ctx, "owners", "edges", "node", "children")
			log.take("")
			var loads []string
			for _, x := range xs {
				got, _ := childPage(t, ectx, db, x, db.Query[child](), c.args, titleOrder())
				for _, s := range log.take("children") {
					if !strings.Contains(s, "COUNT(") {
						loads = append(loads, s)
					}
				}
				want, _ := ownPage(t, db, x, db.Query[child](), c.args, titleOrder())
				log.take("")
				samePage(t, x.Name, got, want)
			}
			oneEach(t, "the load", loads, "ROW_NUMBER()")
		})
	}
}

// TestEdgeConnectionAlone: an owner in no group, or a context without the
// table or without a field, runs the connection's own queries, filtered by
// Of.
func TestEdgeConnectionAlone(t *testing.T) {
	db, _, log := edgeDB(t)
	args := gqlgraph.Args{First: new(2)}
	want, wn := ownPage(t, db, &owner{ID: 1}, db.Query[child](), args, titleOrder())
	op := operation(t, childrenQuery)
	grouped := ownersPage(t, op, db, db.Query[owner](), gqlgraph.Args{})[0]
	noTable := graphql.WithOperationContext(context.Background(), graphql.GetOperationContext(op))
	for _, c := range []struct {
		name string
		ctx  context.Context
		x    *owner
	}{
		{"no table", field(t, noTable, "owners", "edges", "node", "children"), &owner{ID: 1}},
		{"no group", field(t, op, "owners", "edges", "node", "children"), &owner{ID: 1}},
		{"no field", op, grouped},
	} {
		log.take("")
		got, n := childPage(t, c.ctx, db, c.x, db.Query[child](), args, titleOrder())
		samePage(t, c.name, got, want)
		if n != wn {
			t.Errorf("%s: totalCount %d, want %d", c.name, n, wn)
		}
		sql := log.take("children")
		if len(sql) != 2 || strings.Contains(sql[0]+sql[1], "ROW_NUMBER") || strings.Contains(sql[0]+sql[1], "GROUP BY") {
			t.Errorf("%s: %q, want a page and a count of the owner's own", c.name, sql)
		}
	}
}

// TestEdgeAliases is Review Focus 2: two aliases of an edge, with different
// arguments, are two batches, and neither's rows are in the other's pages.
func TestEdgeAliases(t *testing.T) {
	db, _, log := edgeDB(t)
	ctx := operation(t, `{ owners { edges { node {
		a: children(first: 1) { edges { node { title } } }
		b: children(title: "b") { edges { node { title } } }
	} } } }`)
	xs := ownersPage(t, ctx, db, db.Query[owner](), gqlgraph.Args{})
	actx := field(t, ctx, "owners", "edges", "node", "a")
	bctx := field(t, ctx, "owners", "edges", "node", "b")
	aq, aargs := db.Query[child](), gqlgraph.Args{First: new(1)}
	bq, bargs := db.Query[child]().Where(children.Title.EQ("b")), gqlgraph.Args{}
	log.take("")
	var sql []string
	for _, x := range xs {
		for _, c := range []struct {
			name string
			ctx  context.Context
			q    velox.Query[child]
			args gqlgraph.Args
		}{{"a", actx, aq, aargs}, {"b", bctx, bq, bargs}} {
			pc, err := gqlgraph.NewConnectionOf(c.ctx, c.q, "Child", c.args, nil, childKey())
			if err != nil {
				t.Fatal(err)
			}
			got, err := gqlgraph.EdgeConnectionOf(c.ctx, pc, x, owners.Children, "Owner.children", c.args).Page(c.ctx)
			if err != nil {
				t.Fatal(err)
			}
			sql = append(sql, log.take("children")...)
			want, _ := ownPage(t, db, x, c.q, c.args, nil)
			log.take("")
			samePage(t, x.Name+" "+c.name, got, want)
		}
	}
	if len(sql) != 2 {
		t.Fatalf("%d statements, want one per alias: %q", len(sql), sql)
	}
}

// TestEdgeScopes: rows of one group whose edge resolvers' contexts have
// different row scopes (shards, scope values) are batched apart, one
// statement per scope.
func TestEdgeScopes(t *testing.T) {
	for _, c := range []struct {
		name  string
		scope func(context.Context, string) context.Context
		loads int
	}{
		{"WithShard", velox.WithShard, 2},
		// A scope value: callers of different values batch apart.
		{"WithScopeValue", func(ctx context.Context, s string) context.Context { return velox.WithScopeValue(ctx, "org", s) }, 2},
	} {
		t.Run(c.name, func(t *testing.T) { testEdgeScopes(t, c.scope, c.loads) })
	}
}

func testEdgeScopes(t *testing.T, scope func(context.Context, string) context.Context, n int) {
	db, _, log := edgeDB(t)
	ctx := operation(t, childrenQuery)
	xs := ownersPage(t, ctx, db, db.Query[owner](), gqlgraph.Args{})
	ectx := field(t, ctx, "owners", "edges", "node", "children")
	scoped := map[int64]context.Context{1: scope(ectx, "a"), 2: scope(ectx, "b"), 3: scope(ectx, "a")}
	log.take("")
	var loads []string
	for _, x := range xs {
		c, err := gqlgraph.NewConnectionOf(scoped[x.ID], db.Query[child](), "Child", gqlgraph.Args{}, nil, childKey())
		if err != nil {
			t.Fatal(err)
		}
		got, err := gqlgraph.EdgeConnectionOf(scoped[x.ID], c, x, owners.Children, "Owner.children", nil).Page(ectx)
		if err != nil {
			t.Fatal(err)
		}
		loads = append(loads, log.take("children")...)
		want, _ := ownPage(t, db, x, db.Query[child](), gqlgraph.Args{}, nil)
		log.take("")
		samePage(t, x.Name, got, want)
	}
	if len(loads) != n {
		t.Errorf("%d statements, want %d: %q", len(loads), n, loads)
	}
}

// TestEdgeFromAppField is the final review's C1: an application's field on
// a generated type (Owner.name here), whose resolver calls edge methods,
// reads each call alone, by its own arguments: two pages of the children
// with different arguments, then the tags, an edge of another target type.
func TestEdgeFromAppField(t *testing.T) {
	db, _, log := edgeDB(t)
	ctx := operation(t, `{ owners { edges { node { name } } } }`)
	xs := ownersPage(t, ctx, db, db.Query[owner](), gqlgraph.Args{})
	nctx := field(t, ctx, "owners", "edges", "node", "name")
	log.take("")
	wantTags := map[int64][]int64{1: {1, 2}, 2: {2}, 3: {}}
	for _, x := range xs {
		for _, c := range []struct {
			name  string
			q     velox.Query[child]
			args  gqlgraph.Args
			order []gqlgraph.OrderTerm[child]
		}{
			{"first 1 by title", db.Query[child](), gqlgraph.Args{First: new(1)}, titleOrder()},
			{"title d, first 3", db.Query[child]().Where(children.Title.EQ("d")), gqlgraph.Args{First: new(3)}, nil},
		} {
			got, n := childPage(t, nctx, db, x, c.q, c.args, c.order)
			want, wn := ownPage(t, db, x, c.q, c.args, c.order)
			samePage(t, x.Name+" "+c.name, got, want)
			if n != wn {
				t.Errorf("%s %s: totalCount %d, want %d", x.Name, c.name, n, wn)
			}
		}
		rows, err := gqlgraph.EdgeListOf(nctx, x, owners.Tags, "Owner.tags", 0)
		if err != nil {
			t.Fatal(err)
		}
		got := []int64{}
		for _, r := range rows {
			got = append(got, r.ID)
		}
		if !reflect.DeepEqual(got, wantTags[x.ID]) {
			t.Errorf("%s: tags %v, want %v", x.Name, got, wantTags[x.ID])
		}
	}
}

// TestEdgeOneOwnerAndEditor: two to-one edges of one target type never share
// a read: called from an application's field (Child.title here) each reads
// alone, and two relations under one edge's name, from its field, are two
// batches.
func TestEdgeOneOwnerAndEditor(t *testing.T) {
	db, _, _ := edgeDB(t)
	ctx := operation(t, `{ children { edges { node { title owner { name } } } } }`)
	cctx := field(t, ctx, "children")
	c, err := gqlgraph.NewConnectionOf(cctx, db.Query[child]().Where(children.OwnerID.NotNull()), "Child", gqlgraph.Args{}, nil, childKey())
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Page(cctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path   []string
		editor string
	}{
		{[]string{"children", "edges", "node", "title"}, "Child.editor"},
		{[]string{"children", "edges", "node", "owner"}, "Child.owner"},
	} {
		path, editor := c.path, c.editor
		fctx := field(t, ctx, path...)
		for _, x := range p.Rows {
			o, err := gqlgraph.EdgeOneOf(fctx, x, children.Owner, "Child.owner")
			if err != nil {
				t.Fatal(err)
			}
			e, err := gqlgraph.EdgeOneOf(fctx, x, children.Editor, editor)
			if err != nil {
				t.Fatal(err)
			}
			if o == nil || e == nil || o.ID != *x.OwnerID || e.ID != *x.EditorID {
				t.Errorf("%s: child %d: owner %+v and editor %+v, want %d and %d", path[3], x.ID, o, e, *x.OwnerID, *x.EditorID)
			}
		}
	}
}

// TestEdgePerRowArguments: a resolver of the edge's own field that computes
// the arguments from its row (a forceResolver) gets each row's page: a row
// whose arguments are not the batch's reads alone. Here each owner's
// children are those not titled its name.
func TestEdgePerRowArguments(t *testing.T) {
	db, _, _ := edgeDB(t)
	ctx := operation(t, childrenQuery)
	xs := ownersPage(t, ctx, db, db.Query[owner](), gqlgraph.Args{})
	ectx := field(t, ctx, "owners", "edges", "node", "children")
	for _, x := range xs {
		q, args := db.Query[child]().Where(children.Title.NEQ(x.Name)), gqlgraph.Args{First: new(int(x.ID))}
		got, n := childPage(t, ectx, db, x, q, args, titleOrder(), x.Name)
		want, wn := ownPage(t, db, x, q, args, titleOrder())
		samePage(t, x.Name, got, want)
		if n != wn {
			t.Errorf("%s: totalCount %d, want %d", x.Name, n, wn)
		}
	}
}

// TestPaginateEdgeWhere: Paginate of an Edge batches the rows whose
// arguments are equal, Args.Where's input among them, and not its
// condition, which may hold a function (Table.Has does) that reflect.DeepEqual never finds equal:
// the owners whose where is the same are one load, each with its own page.
// A row whose Where differs (a resolver that computes it per row) reads
// alone, and gets its own page.
func TestPaginateEdgeWhere(t *testing.T) {
	db, _, log := edgeDB(t)
	typ, order := gqlgraph.TypeOf("Child", titleOrder(), childKey())
	typ.Table = untyped.TableOf[child]
	pred := func(not *string, _ context.Context) (velox.Predicate, error) {
		return untyped.And(velox.Predicate(children.Title.NEQ(*not)), untyped.TableOf[child]().Has(int(children.Owner.I), untyped.TableOf[owner]())), nil
	}
	for _, c := range []struct {
		name  string
		where func(x *owner) string
		loads int
	}{
		{"one where", func(*owner) string { return "b" }, 1},
		{"a where per row", func(x *owner) string { return x.Name }, 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := operation(t, childrenQuery)
			xs := ownersPage(t, ctx, db, db.Query[owner](), gqlgraph.Args{})
			ectx := field(t, ctx, "owners", "edges", "node", "children")
			log.take("")
			loads := 0
			for _, x := range xs {
				w := c.where(x)
				args := gqlgraph.Args{First: new(2), Order: order, Where: gqlgraph.WhereOf(&w, pred)}
				conn, err := gqlgraph.Paginate(ectx, gqlgraph.EdgeOf(x, owners.Children, "Owner.children"), typ, args)
				if err != nil {
					t.Fatal(err)
				}
				p, err := conn.Page(ectx)
				if err != nil {
					t.Fatal(err)
				}
				loads += len(log.take("children"))
				q := db.Query[child]().Where(children.Title.NEQ(w), children.Owner.Has())
				want, _ := ownPage(t, db, x, q, gqlgraph.Args{First: new(2)}, titleOrder())
				log.take("")
				samePage(t, x.Name+" "+w, gqlgraph.TypedPage[child](p), want)
			}
			if len(xs) != 3 || loads != c.loads {
				t.Errorf("%d owners, %d loads, want 3 and %d", len(xs), loads, c.loads)
			}
		})
	}
}

// TestEdgeOneBatches: the owners of a page of children are one statement,
// keyed by the owners' key with no window; a child of no owner has none;
// a group whose children have no owner sends no statement; and a child in no
// group reads its own.
func TestEdgeOneBatches(t *testing.T) {
	db, _, log := edgeDB(t)
	ctx := operation(t, `{ children { edges { node { owner { name } } } } }`)
	cctx := field(t, ctx, "children")
	c, err := gqlgraph.NewConnectionOf(cctx, db.Query[child](), "Child", gqlgraph.Args{}, nil, childKey())
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Page(cctx)
	if err != nil {
		t.Fatal(err)
	}
	octx := field(t, ctx, "children", "edges", "node", "owner")
	log.take("")
	got := map[int64]string{}
	for _, x := range p.Rows {
		o, err := gqlgraph.EdgeOneOf(octx, x, children.Owner, "Child.owner")
		if err != nil {
			t.Fatal(err)
		}
		if o != nil {
			got[x.ID] = o.Name
		}
	}
	if want := map[int64]string{1: "a", 2: "a", 3: "a", 4: "a", 5: "b", 6: "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("owners %v, want %v", got, want)
	}
	sql := log.take("owners")
	oneEach(t, "the owners", sql)
	if strings.Contains(sql[0], "ROW_NUMBER") {
		t.Errorf("%s: a load by the owners' key needs no window", sql[0])
	}

	orphan := *p.Rows[6]
	veloxgql.Batch(cctx, []*child{&orphan})
	if o, err := gqlgraph.EdgeOneOf(octx, &orphan, children.Owner, "Child.owner"); o != nil || err != nil {
		t.Errorf("the orphan's owner: %v, %v", o, err)
	}
	if sql := log.take("owners"); len(sql) != 0 {
		t.Errorf("the orphan's owner: %q, want no statement", sql)
	}

	alone := *p.Rows[4] // a copy of child 5, of owner 2, in no group
	if o, err := gqlgraph.EdgeOneOf(octx, &alone, children.Owner, "Child.owner"); err != nil || o == nil || o.Name != "b" {
		t.Errorf("alone: %v, %v", o, err)
	}
	if sql := log.take("owners"); len(sql) != 1 {
		t.Errorf("alone: %q, want one statement", sql)
	}
}

// TestEdgeListCap: a list edge is one statement for the group, in key
// order; an owner with more than MaxList fails naming the edge, and the
// others do not.
func TestEdgeListCap(t *testing.T) {
	db, _, log := edgeDB(t)
	const query = `{ owners { edges { node { tags { name } } } } }`
	list := func(t *testing.T, db *velox.DB, log *edgeLog) (map[int64][]int64, map[int64]error) {
		t.Helper()
		ctx := operation(t, query)
		xs := ownersPage(t, ctx, db, db.Query[owner](), gqlgraph.Args{})
		tctx := field(t, ctx, "owners", "edges", "node", "tags")
		log.take("")
		got, errs := map[int64][]int64{}, map[int64]error{}
		for _, x := range xs {
			rows, err := gqlgraph.EdgeListOf(tctx, x, owners.Tags, "Owner.tags", 0)
			if err != nil {
				errs[x.ID] = err
				continue
			}
			got[x.ID] = []int64{}
			for _, r := range rows {
				got[x.ID] = append(got[x.ID], r.ID)
			}
		}
		oneEach(t, "the tags", log.take("tags"))
		return got, errs
	}
	got, errs := list(t, db, log)
	if want := map[int64][]int64{1: {1, 2}, 2: {2}, 3: {}}; !reflect.DeepEqual(got, want) || len(errs) > 0 {
		t.Errorf("tags %v, %v; want %v", got, errs, want)
	}
	capped, _, cappedLog := edgeDB(t, veloxgql.WithLimits(veloxgql.Limits{MaxList: 1}))
	got, errs = list(t, capped, cappedLog)
	if want := map[int64][]int64{2: {2}, 3: {}}; !reflect.DeepEqual(got, want) {
		t.Errorf("tags under MaxList 1: %v, want %v", got, want)
	}
	if err := errs[1]; !errors.Is(err, velox.ErrTooManyRows) || !strings.Contains(err.Error(), "Owner.tags") {
		t.Errorf("owner 1 under MaxList 1: %v, want ErrTooManyRows naming Owner.tags", err)
	}
	if len(errs) != 1 {
		t.Errorf("errors %v, want owner 1's alone", errs)
	}
}

// TestBatchErrorShared: a batch that fails gives every row of the group its
// error, and runs once.
func TestBatchErrorShared(t *testing.T) {
	db, _, log := edgeDB(t)
	boom := errors.New("boom")
	ctx := operation(t, childrenQuery)
	xs := ownersPage(t, ctx, db, db.Query[owner](), gqlgraph.Args{})
	ectx := field(t, ctx, "owners", "edges", "node", "children")
	log.take("")
	log.mu.Lock()
	log.fail = func(st *velox.Statement) error {
		if st.Entity.Table == "children" {
			return boom
		}
		return nil
	}
	log.mu.Unlock()
	for _, x := range xs {
		c, err := gqlgraph.NewConnectionOf(ectx, db.Query[child](), "Child", gqlgraph.Args{}, nil, childKey())
		if err != nil {
			t.Fatal(err)
		}
		c = gqlgraph.EdgeConnectionOf(ectx, c, x, owners.Children, "Owner.children", nil)
		if _, err := c.Page(ectx); !errors.Is(err, boom) {
			t.Errorf("%s: page error %v, want boom", x.Name, err)
		}
		if _, err := c.TotalCount(ectx); !errors.Is(err, boom) {
			t.Errorf("%s: totalCount error %v, want boom", x.Name, err)
		}
	}
	if sql := log.take("children"); len(sql) != 2 {
		t.Errorf("%d statements, want one load and one count: %q", len(sql), sql)
	}
}

// TestBatchPanicShared: a batch whose load panics panics in the resolver
// that ran it, and fails the others; it runs once.
func TestBatchPanicShared(t *testing.T) {
	db, _, log := edgeDB(t)
	ctx := operation(t, childrenQuery)
	xs := ownersPage(t, ctx, db, db.Query[owner](), gqlgraph.Args{})
	ectx := field(t, ctx, "owners", "edges", "node", "children")
	log.take("")
	log.mu.Lock()
	log.fail = func(st *velox.Statement) error {
		if st.Entity.Table == "children" {
			panic("boom")
		}
		return nil
	}
	log.mu.Unlock()
	for i, x := range xs {
		c, err := gqlgraph.NewConnectionOf(ectx, db.Query[child](), "Child", gqlgraph.Args{}, nil, childKey())
		if err != nil {
			t.Fatal(err)
		}
		c = gqlgraph.EdgeConnectionOf(ectx, c, x, owners.Children, "Owner.children", nil)
		var p any
		func() {
			defer func() { p = recover() }()
			_, err = c.Page(ectx)
		}()
		switch {
		case i == 0 && p == nil:
			t.Errorf("%s: no panic", x.Name)
		case i > 0 && (p != nil || err == nil || !strings.Contains(err.Error(), "panicked")):
			t.Errorf("%s: panic %v and error %v, want the batch's panic as an error", x.Name, p, err)
		}
	}
	if sql := log.take("children"); len(sql) != 1 {
		t.Errorf("%d statements, want one: %q", len(sql), sql)
	}
}

// TestBatchRace: 50 goroutines resolve the edge of the 50 rows of one
// group, concurrently, as gqlgen does: one load and one count, and each row
// its own page. Run it with -race.
func TestBatchRace(t *testing.T) {
	db, raw, log := edgeDB(t)
	for id := 101; id <= 150; id++ {
		if _, err := raw.Exec(`INSERT INTO owners (id, name, age) VALUES (?, 'x', 1)`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO children (id, owner_id, title) VALUES (?, ?, 'p'), (?, ?, 'q')`, id*10, id, id*10+1, id); err != nil {
			t.Fatal(err)
		}
	}
	ctx := operation(t, childrenQuery)
	xs := ownersPage(t, ctx, db, db.Query[owner]().Where(owners.ID.GTE(101)), gqlgraph.Args{First: new(50)})
	if len(xs) != 50 {
		t.Fatalf("%d owners", len(xs))
	}
	ectx := field(t, ctx, "owners", "edges", "node", "children")
	log.take("")
	var wg sync.WaitGroup
	start := make(chan struct{})
	got := make([][]int64, len(xs))
	counts := make([]int, len(xs))
	for i, x := range xs {
		wg.Go(func() {
			<-start
			c, err := gqlgraph.NewConnectionOf(ectx, db.Query[child](), "Child", gqlgraph.Args{First: new(1)}, nil, childKey())
			if err != nil {
				t.Error(err)
				return
			}
			c = gqlgraph.EdgeConnectionOf(ectx, c, x, owners.Children, "Owner.children", nil)
			p, err := c.Page(ectx)
			if err != nil {
				t.Error(err)
				return
			}
			got[i] = childIDs(p.Rows)
			if counts[i], err = c.TotalCount(ectx); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()
	for i, x := range xs {
		if want := []int64{x.ID * 10}; !reflect.DeepEqual(got[i], want) || counts[i] != 2 {
			t.Errorf("owner %d: page %v and count %d, want %v and 2", x.ID, got[i], counts[i], want)
		}
	}
	if sql := log.take("children"); len(sql) != 2 {
		t.Errorf("%d statements, want one load and one count: %q", len(sql), sql)
	}
}

// TestNoFieldNoGroup: a page read outside a gqlgen field, where nothing
// batches, is no group: a context kept long keeps no rows.
func TestNoFieldNoGroup(t *testing.T) {
	db, _, _ := edgeDB(t)
	ctx := operation(t, childrenQuery)
	c, err := gqlgraph.NewConnectionOf(ctx, db.Query[owner](), "Owner", gqlgraph.Args{}, nil, ownerKey())
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Page(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fctx := field(t, ctx, "owners")
	f, ok := gqlcore.FieldOf(fctx)
	if !ok {
		t.Fatal("no field in a gqlgen field context")
	}
	if _, _, _, ok := gqlcore.Find(f.Table, any(p.Rows[0])); ok {
		t.Error("a page outside a field is a group")
	}
	c, err = gqlgraph.NewConnectionOf(fctx, db.Query[owner](), "Owner", gqlgraph.Args{}, nil, ownerKey())
	if err != nil {
		t.Fatal(err)
	}
	if p, err = c.Page(fctx); err != nil || len(p.Rows) == 0 {
		t.Fatalf("no owners: %v", err)
	}
	if _, _, _, ok := gqlcore.Find(f.Table, any(p.Rows[0])); !ok {
		t.Error("a page in a field is no group")
	}
}

// TestPagePanicShared: when a connection's Page or TotalCount panics, its
// other callers get an error, not a nil page.
func TestPagePanicShared(t *testing.T) {
	db, _, log := edgeDB(t)
	log.fail = func(*velox.Statement) error { panic("boom") }
	c, err := gqlgraph.NewConnectionOf(context.Background(), db.Query[owner](), "Owner", gqlgraph.Args{}, nil, ownerKey())
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() error{
		func() error { _, err := c.Page(context.Background()); return err },
		func() error { _, err := c.TotalCount(context.Background()); return err },
	} {
		func() {
			defer func() { _ = recover() }()
			_ = call()
			t.Error("no panic")
		}()
		if err := call(); err == nil || !strings.Contains(err.Error(), "panicked") {
			t.Errorf("the next call: %v, want the panic as an error", err)
		}
	}
}
