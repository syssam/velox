package graphqlgo_test

import (
	"context"
	"database/sql"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

// author, post, tag and postTag are hand-written entities, as velox-gen
// would write them, for the adapters' tests: an author has posts (a
// connection), a post has its author (to-one) and tags (a list, through
// postTag).
type author struct {
	ID    int64
	Name  string
	Posts velox.Many[post]
}

var authorMeta velox.MetaOnce[author]

func (*author) VeloxMeta() *velox.Meta[author] { return authorMeta.Load(authorDescriptor) }

func authorDescriptor() velox.Descriptor[author] {
	return velox.Descriptor[author]{
		Table:        "authors",
		Columns:      []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
		PrimaryKey:   []int{0},
		Edges:        []velox.EdgeDescriptor{velox.EdgeTo[post]("posts", 0, 1)},
		FieldPtr:     func(x *author, i int) any { return [...]any{&x.ID, &x.Name}[i] },
		EdgeFieldPtr: func(x *author, _ int) any { return &x.Posts },
	}
}

type post struct {
	ID       int64
	AuthorID int64
	Title    string
	Author   velox.One[author]
	Tags     velox.Many[tag]
}

var postMeta velox.MetaOnce[post]

func (*post) VeloxMeta() *velox.Meta[post] { return postMeta.Load(postDescriptor) }

func postDescriptor() velox.Descriptor[post] {
	return velox.Descriptor[post]{
		Table:      "posts",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "AuthorID", Name: "author_id"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[author]("author", 1, 0), velox.EdgeThrough[postTag, tag]("tags", 0, 0, 1, 0)},
		FieldPtr:   func(x *post, i int) any { return [...]any{&x.ID, &x.AuthorID, &x.Title}[i] },
		EdgeFieldPtr: func(x *post, i int) any {
			if i == 0 {
				return &x.Author
			}
			return &x.Tags
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

type postTag struct {
	PostID int64
	TagID  int64
}

var postTagMeta velox.MetaOnce[postTag]

func (*postTag) VeloxMeta() *velox.Meta[postTag] { return postTagMeta.Load(postTagDescriptor) }

func postTagDescriptor() velox.Descriptor[postTag] {
	return velox.Descriptor[postTag]{
		Table:      "post_tags",
		Columns:    []velox.ColumnDescriptor{{Field: "PostID", Name: "post_id"}, {Field: "TagID", Name: "tag_id"}},
		PrimaryKey: []int{0, 1},
		FieldPtr:   func(x *postTag, i int) any { return [...]any{&x.PostID, &x.TagID}[i] },
	}
}

// The gqlgraph types of the entities, as velox-graphql writes them.
var (
	authorType = gqlgraph.Type{
		Name:       "Author",
		Table:      untyped.TableOf[author],
		Key:        []gqlgraph.Column{{Name: "id", Index: 0}},
		AlwaysRead: []int{0},
		Fields:     []string{"id", "name"},
		Edges:      []string{"posts"},
		WhereFields: []gqlgraph.WhereField{
			{Name: "not", Kind: gqlgraph.WhereNot},
			{Name: "and", Kind: gqlgraph.WhereAnd},
			{Name: "or", Kind: gqlgraph.WhereOr},
			{Name: "name", Ops: gqlgraph.OpEQ | gqlgraph.OpHasPrefix, Col: 1},
		},
	}
	postType = gqlgraph.Type{
		Name:       "Post",
		Table:      untyped.TableOf[post],
		Key:        []gqlgraph.Column{{Name: "id", Index: 0}},
		AlwaysRead: []int{0},
		Fields:     []string{"id", "", "title"},
		Edges:      []string{"author", "tags"},
	}
	tagType = gqlgraph.Type{
		Name:       "Tag",
		Table:      untyped.TableOf[tag],
		Key:        []gqlgraph.Column{{Name: "id", Index: 0}},
		AlwaysRead: []int{0},
		Fields:     []string{"id", "name"},
	}
	nodeTypes = []*gqlgraph.Type{&authorType, &postType, &tagType}
)

// authorWhere is the where input of authors, as velox-graphql writes one.
type authorWhere struct {
	Not           *authorWhere
	And           []*authorWhere
	Or            []*authorWhere
	Name          *string
	NameHasPrefix *string
}

// Predicate is w's condition.
func (w *authorWhere) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, &authorType, w)
}

// conn is a connection, as velox-graphql writes one per type.
type conn[E any] struct{ c *gqlgraph.Connection }

// edge is an edge of a connection.
type edge[E any] struct {
	Node   *E
	Cursor veloxgql.Cursor
}

func (c *conn[E]) edges(ctx context.Context) ([]*edge[E], error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*edge[E], len(p.Rows))
	for i := range p.Rows {
		out[i] = &edge[E]{Node: p.Node(i).(*E), Cursor: p.Cursors[i]}
	}
	return out, nil
}

func (c *conn[E]) pageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

func (c *conn[E]) totalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// The resolvers both servers bind, as generated code and an application's
// root resolvers are.

func authors(ctx context.Context, first *int, after *veloxgql.Cursor, where *authorWhere) (*conn[author], error) {
	q := untyped.FromQuery(veloxgql.DB(ctx).Query[author]())
	c, err := gqlgraph.Paginate(ctx, gqlgraph.Query(q), &authorType, gqlgraph.Args{First: first, After: after, Where: gqlgraph.WhereOf(where, (*authorWhere).Predicate)})
	if err != nil {
		return nil, err
	}
	return &conn[author]{c}, nil
}

func authorPosts(ctx context.Context, a *author, first *int, after *veloxgql.Cursor) (*conn[post], error) {
	c, err := gqlgraph.Paginate(ctx, authorType.Edge(a, 0), &postType, gqlgraph.Args{First: first, After: after})
	if err != nil {
		return nil, err
	}
	return &conn[post]{c}, nil
}

func postAuthor(ctx context.Context, p *post) (*author, error) {
	x, err := gqlgraph.EdgeOne(ctx, &authorType, postType.Edge(p, 0))
	if err != nil || x == nil {
		return nil, err
	}
	return x.(*author), nil
}

func postTags(ctx context.Context, p *post) ([]*tag, error) {
	x, err := gqlgraph.EdgeList(ctx, &tagType, postType.Edge(p, 1), 0)
	if err != nil {
		return nil, err
	}
	return x.([]*tag), nil
}

func node(ctx context.Context, id int64) (any, error) {
	return gqlgraph.ResolveNode(ctx, veloxgql.DB(ctx), nodeTypes, id)
}

// addPost inserts a post of the author, and fails after the insert when
// fail is set.
func addPost(ctx context.Context, authorID int64, title string, fail bool) (*post, error) {
	p := &post{AuthorID: authorID, Title: title}
	if err := veloxgql.DB(ctx).Insert(p).Exec(ctx); err != nil {
		return nil, err
	}
	if fail {
		return nil, gqlgraph.InputErrorf("the post is refused after its insert")
	}
	return p, nil
}

// paritySDL is the schema both servers serve.
const paritySDL = `
scalar Cursor
interface Node { id: ID! }
type Query {
	authors(first: Int, after: Cursor, where: AuthorWhereInput): AuthorConnection
	node(id: ID!): Node
}
type Mutation {
	addPost(author: ID!, title: String!): Post!
	failPost(author: ID!, title: String!): Post
}
type Author implements Node {
	id: ID!
	name: String!
	posts(first: Int, after: Cursor): PostConnection!
}
type Post implements Node {
	id: ID!
	title: String!
	author: Author!
	tags: [Tag!]!
}
type Tag implements Node {
	id: ID!
	name: String!
}
type AuthorConnection { edges: [AuthorEdge!]! pageInfo: PageInfo! totalCount: Int! }
type AuthorEdge { node: Author! cursor: Cursor! }
type PostConnection { edges: [PostEdge!]! pageInfo: PageInfo! totalCount: Int! }
type PostEdge { node: Post! cursor: Cursor! }
type PageInfo { hasNextPage: Boolean! hasPreviousPage: Boolean! startCursor: Cursor endCursor: Cursor }
input AuthorWhereInput {
	not: AuthorWhereInput
	and: [AuthorWhereInput!]
	or: [AuthorWhereInput!]
	name: String
	nameHasPrefix: String
}
`

// stmtLog records each statement a DB sends, its SQL with " in tx" when it
// ran in a transaction, and how each transaction ended.
type stmtLog struct {
	mu    sync.Mutex
	stmts []string
	txs   []string
}

func (l *stmtLog) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	s := st.SQL
	if st.InTx {
		s += " in tx"
	}
	l.mu.Lock()
	l.stmts = append(l.stmts, s)
	l.mu.Unlock()
	return next(ctx)
}

func (l *stmtLog) txAround(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
	defer func() {
		l.mu.Lock()
		l.txs = append(l.txs, tx.Outcome.String())
		l.mu.Unlock()
	}()
	return next(ctx)
}

// take returns the statements, sorted, as concurrent fields send them in
// any order, and the transactions' outcomes, and forgets them.
func (l *stmtLog) take() (stmts, txs []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	stmts, txs = l.stmts, l.txs
	l.stmts, l.txs = nil, nil
	sort.Strings(stmts)
	return stmts, txs
}

// modelDB opens an in-memory SQLite database of three authors, six posts
// and three tags, keyed apart so that a Node's ID names one row:
//
//	authors 1 ann, 2 bob, 3 cy
//	posts 11-12 of ann, 13-15 of bob, 16 of cy
//	tags 21 go, 22 sql, 23 web; post 11 has go and sql, post 13 web
func modelDB(t testing.TB, opts ...velox.Option) (*velox.DB, *stmtLog) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	for _, s := range []string{
		`CREATE TABLE authors (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, author_id INTEGER NOT NULL, title TEXT NOT NULL)`,
		`CREATE TABLE tags (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE post_tags (post_id INTEGER NOT NULL, tag_id INTEGER NOT NULL, PRIMARY KEY (post_id, tag_id))`,
		`INSERT INTO authors VALUES (1, 'ann'), (2, 'bob'), (3, 'cy')`,
		`INSERT INTO posts VALUES (11, 1, 'a1'), (12, 1, 'a2'), (13, 2, 'b1'), (14, 2, 'b2'), (15, 2, 'b3'), (16, 3, 'c1')`,
		`INSERT INTO tags VALUES (21, 'go'), (22, 'sql'), (23, 'web')`,
		`INSERT INTO post_tags VALUES (11, 21), (11, 22), (13, 23)`,
	} {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	l := &stmtLog{}
	return velox.Open(velox.SQLite, raw, append([]velox.Option{velox.WithAround(l.around), velox.WithTxAround(l.txAround)}, opts...)...), l
}

// postCount is the number of posts, read on db outside the log.
func postCount(t testing.TB, db *velox.DB, l *stmtLog) int {
	t.Helper()
	n, err := db.Query[post]().Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	l.take()
	return int(n)
}

// contains reports whether one of ss holds sub.
func contains(ss []string, sub string) bool {
	return slices.ContainsFunc(ss, func(s string) bool { return strings.Contains(s, sub) })
}
