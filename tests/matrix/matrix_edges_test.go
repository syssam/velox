package matrix

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/runtime"

	"example.com/matrix/narrow"
	gen "example.com/matrix/velox"
	"example.com/matrix/velox/article"
	"example.com/matrix/velox/author"
	"example.com/matrix/velox/entity"
	"example.com/matrix/velox/intercept"
	"example.com/matrix/velox/node"
	"example.com/matrix/velox/passport"
	"example.com/matrix/velox/query"
	"example.com/matrix/velox/topic"
)

// graph is the fixture the edge and query tests share:
//
//	ann: a1 (10 words, news,  editor bob, topics go+db)
//	     a2 (20 words, news,  no editor,  topic  go)
//	     a3 (30 words, sport, editor bob, no topics)
//	bob: b1 (40 words, sport, no editor,  no topics)
//	cy:  nothing
//
// ann holds a passport; nobody else does.
type graph struct {
	ann, bob, cy   *entity.Author
	a1, a2, a3, b1 *entity.Article
	goT, dbT       *entity.Topic
	annPassport    *entity.Passport
}

func seedGraph(t *testing.T, client *gen.Client) graph {
	t.Helper()
	ctx := context.Background()
	var g graph
	g.ann = client.Author.Create().SetName("ann").SaveX(ctx)
	g.bob = client.Author.Create().SetName("bob").SaveX(ctx)
	g.cy = client.Author.Create().SetName("cy").SaveX(ctx)
	g.goT = client.Topic.Create().SetName("go").SaveX(ctx)
	g.dbT = client.Topic.Create().SetName("db").SaveX(ctx)
	g.a1 = client.Article.Create().SetTitle("a1").SetWords(10).SetAuthorID(g.ann.ID).SetEditorID(g.bob.ID).
		AddTopicIDs(g.goT.ID, g.dbT.ID).SaveX(ctx)
	g.a2 = client.Article.Create().SetTitle("a2").SetWords(20).SetAuthorID(g.ann.ID).AddTopicIDs(g.goT.ID).SaveX(ctx)
	g.a3 = client.Article.Create().SetTitle("a3").SetWords(30).SetSection("sport").SetAuthorID(g.ann.ID).
		SetEditorID(g.bob.ID).SaveX(ctx)
	g.b1 = client.Article.Create().SetTitle("b1").SetWords(40).SetSection("sport").SetAuthorID(g.bob.ID).SaveX(ctx)
	g.annPassport = client.Passport.Create().SetNumber("P-ann").SetOwnerID(g.ann.ID).SaveX(ctx)
	return g
}

func titles(as []*entity.Article) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Title
	}
	return out
}

func names(as []*entity.Author) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Name
	}
	return out
}

// Shape 14: O2M, and M2O bound to a column with Field() — one required key,
// one optional.
func TestEdges_O2MAndFieldBoundM2O(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	g := seedGraph(t, client)

	got, err := g.ann.QueryArticles().Order(article.ByID()).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"a1", "a2", "a3"}, titles(got))
	edited, err := g.bob.QueryEdited().Order(article.ByID()).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"a1", "a3"}, titles(edited))
	owner, err := g.a1.QueryAuthor().Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "ann", owner.Name)

	// The bound columns are the keys.
	a1 := client.Article.GetX(ctx, g.a1.ID)
	assert.Equal(t, g.ann.ID, a1.AuthorID)
	require.NotNil(t, a1.EditorID)
	assert.Equal(t, g.bob.ID, *a1.EditorID)
	assert.Nil(t, client.Article.GetX(ctx, g.a2.ID).EditorID)

	// Required key: a create without it fails before reaching the database.
	_, err = client.Article.Create().SetTitle("orphan").Save(ctx)
	require.True(t, gen.IsValidationError(err), "a create without the required author: %v", err)
	// ...and it cannot be cleared.
	upd := reflect.TypeOf(client.Article.UpdateOneID(g.a1.ID))
	_, hasClear := upd.MethodByName("ClearAuthorID")
	assert.False(t, hasClear, "a required key has no Clear")
	_, hasClear = upd.MethodByName("ClearEditorID")
	require.True(t, hasClear, "the optional key lost its Clear: the probe above proves nothing")
	// Reassignment through the column and through the edge setter.
	require.NoError(t, client.Article.UpdateOneID(g.a2.ID).SetAuthorID(g.cy.ID).Exec(ctx))
	assert.Equal(t, 1, must(g.cy.QueryArticles().Count(ctx)))
	assert.Equal(t, 2, must(g.ann.QueryArticles().Count(ctx)))

	// Optional key: set, then clear back to NULL.
	require.NoError(t, client.Article.UpdateOneID(g.a2.ID).SetEditorID(g.ann.ID).Exec(ctx))
	assert.Equal(t, g.ann.ID, *client.Article.GetX(ctx, g.a2.ID).EditorID)
	require.NoError(t, client.Article.UpdateOneID(g.a2.ID).ClearEditorID().Exec(ctx))
	assert.Nil(t, client.Article.GetX(ctx, g.a2.ID).EditorID)

	// A key that points nowhere is a constraint error.
	_, err = client.Article.Create().SetTitle("x").SetAuthorID(9999).Save(ctx)
	require.True(t, gen.IsConstraintError(err), "a dangling author_id: %v", err)
}

// Shape 14: O2O.
func TestEdges_O2O(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	g := seedGraph(t, client)

	p, err := g.ann.QueryPassport().Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "P-ann", p.Number)
	o, err := p.QueryOwner().Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, g.ann.ID, o.ID)
	_, err = g.bob.QueryPassport().Only(ctx)
	require.True(t, gen.IsNotFound(err))

	_, err = client.Passport.Create().SetNumber("dup").SetOwnerID(g.ann.ID).Save(ctx)
	require.True(t, gen.IsConstraintError(err), "a second passport for one owner: %v", err)
	_, err = client.Passport.Create().SetNumber("nobody").Save(ctx)
	require.True(t, gen.IsValidationError(err), "a passport without its required owner: %v", err)

	loaded, err := client.Author.Query().WithPassport().Order(author.ByID()).All(ctx)
	require.NoError(t, err)
	require.Len(t, loaded, 3)
	require.NotNil(t, loaded[0].Edges.Passport)
	assert.Equal(t, "P-ann", loaded[0].Edges.Passport.Number)
	assert.Nil(t, loaded[1].Edges.Passport)
	assert.True(t, loaded[1].Edges.PassportLoaded(), "an empty to-one edge is still loaded")

	holders, err := client.Author.Query().Where(author.HasPassport()).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"ann"}, names(holders))
	holders, err = client.Author.Query().Where(author.HasPassportWith(passport.NumberField.EQ("nope"))).All(ctx)
	require.NoError(t, err)
	assert.Empty(t, holders)
}

// Shape 14: M2M, from both sides.
func TestEdges_M2M(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	g := seedGraph(t, client)

	ts, err := g.a1.QueryTopics().Order(topic.ByName()).Select(topic.FieldName).Strings(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"db", "go"}, ts)
	as, err := g.goT.QueryArticles().Order(article.ByID()).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"a1", "a2"}, titles(as))

	require.NoError(t, client.Article.UpdateOneID(g.a1.ID).RemoveTopicIDs(g.goT.ID).Exec(ctx))
	require.NoError(t, client.Topic.UpdateOneID(g.dbT.ID).AddArticleIDs(g.b1.ID).Exec(ctx))
	loaded, err := client.Article.Query().WithTopics().Order(article.ByID()).All(ctx)
	require.NoError(t, err)
	want := map[string][]string{"a1": {"db"}, "a2": {"go"}, "a3": {}, "b1": {"db"}}
	for _, a := range loaded {
		got := []string{}
		for _, tp := range a.Edges.Topics {
			got = append(got, tp.Name)
		}
		assert.Equal(t, want[a.Title], got, a.Title)
	}
}

// Shape 14: a self-referencing tree.
func TestEdges_SelfReferenceTree(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()

	root := client.Node.Create().SetName("root").SaveX(ctx)
	c1 := client.Node.Create().SetName("c1").SetParentID(root.ID).SaveX(ctx)
	c2 := client.Node.Create().SetName("c2").SetParent(root).SaveX(ctx)
	g1 := client.Node.Create().SetName("g1").SetParentID(c1.ID).SaveX(ctx)
	lone := client.Node.Create().SetName("lone").SaveX(ctx)

	kids, err := root.QueryChildren().Order(node.ByID()).All(ctx)
	require.NoError(t, err)
	require.Len(t, kids, 2)
	assert.Equal(t, []int{c1.ID, c2.ID}, []int{kids[0].ID, kids[1].ID})
	up, err := g1.QueryParent().Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, c1.ID, up.ID)

	// An edge condition nested in a condition on the same edge: the nodes
	// that have a grandchild. sqlgraph.HasNeighbors used to give the inner
	// subquery the alias of the outer one and match nothing.
	grand, err := client.Node.Query().Where(node.HasChildrenWith(node.HasChildren())).IDs(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int{root.ID}, grand)

	// Two levels down in one query, with NULL parents in the result set.
	tree, err := client.Node.Query().
		Where(node.Not(node.HasParent())).
		WithChildren(func(q entity.NodeQuerier) {
			q.Order(node.ByID()).WithChildren()
		}).
		Order(node.ByID()).All(ctx)
	require.NoError(t, err)
	require.Len(t, tree, 2, "root and lone")
	require.Equal(t, root.ID, tree[0].ID)
	require.Len(t, tree[0].Edges.Children, 2)
	require.Len(t, tree[0].Edges.Children[0].Edges.Children, 1)
	assert.Equal(t, g1.ID, tree[0].Edges.Children[0].Edges.Children[0].ID)
	assert.Empty(t, tree[0].Edges.Children[1].Edges.Children)
	assert.Equal(t, lone.ID, tree[1].ID)
	assert.Empty(t, tree[1].Edges.Children)

	// Up the tree, including rows whose parent key is NULL.
	all, err := client.Node.Query().WithParent().Order(node.ByID()).All(ctx)
	require.NoError(t, err)
	require.Len(t, all, 5)
	parents := map[string]string{}
	for _, n := range all {
		if n.Edges.Parent != nil {
			parents[n.Name] = n.Edges.Parent.Name
		}
	}
	assert.Equal(t, map[string]string{"c1": "root", "c2": "root", "g1": "c1"}, parents)

	// (HasChildrenWith(HasChildren()) is the natural spelling; on a
	// self-referencing edge it matches nothing — see known_gaps_test.go.)
	withGrandkids, err := client.Node.Query().
		Where(node.HasChildrenWith(node.HasChildrenWith(node.NameField.EQ("g1")))).Select(node.FieldName).Strings(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"root"}, withGrandkids)
	leaves, err := client.Node.Query().
		Where(node.HasParent(), node.Not(node.HasChildren())).Order(node.ByID()).Select(node.FieldName).Strings(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"c2", "g1"}, leaves)
}

// Shape 14: eager loading, nested, with the back-references FeatureBidiEdgeRefs
// sets.
func TestEdges_EagerLoadNestedAndBackRefs(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	seedGraph(t, client)

	authors, err := client.Author.Query().
		WithArticles(func(q entity.ArticleQuerier) {
			q.Order(article.ByID()).WithTopics().WithEditor()
		}).
		WithEdited().
		Order(author.ByID()).All(ctx)
	require.NoError(t, err)
	require.Len(t, authors, 3)
	ann, bob, cy := authors[0], authors[1], authors[2]

	assert.Equal(t, []string{"a1", "a2", "a3"}, titles(ann.Edges.Articles))
	assert.Equal(t, []string{"b1"}, titles(bob.Edges.Articles))
	assert.Empty(t, cy.Edges.Articles)
	_, err = cy.Edges.ArticlesOrErr()
	require.NoError(t, err, "an empty to-many edge is loaded, not missing")
	assert.ElementsMatch(t, []string{"a1", "a3"}, titles(bob.Edges.Edited))

	a1, a2 := ann.Edges.Articles[0], ann.Edges.Articles[1]
	assert.Len(t, a1.Edges.Topics, 2)
	assert.Len(t, a2.Edges.Topics, 1)
	require.NotNil(t, a1.Edges.Editor)
	assert.Equal(t, "bob", a1.Edges.Editor.Name)
	assert.Nil(t, a2.Edges.Editor, "a NULL optional key loads as no editor")
	assert.Same(t, ann, a1.Edges.Author, "bidiedges: the child points back at its parent")

	// An edge that was not requested reports NotLoaded rather than empty.
	_, err = ann.Edges.PassportOrErr()
	require.True(t, gen.IsNotLoaded(err), "%v", err)

	// M2O side.
	arts, err := client.Article.Query().WithAuthor().WithEditor().Order(article.ByID()).All(ctx)
	require.NoError(t, err)
	require.Len(t, arts, 4)
	for i, want := range []string{"ann", "ann", "ann", "bob"} {
		require.NotNil(t, arts[i].Edges.Author, arts[i].Title)
		assert.Equal(t, want, arts[i].Edges.Author.Name, arts[i].Title)
	}
}

// Shape 14: a per-parent limit on an eager load caps each parent's rows; a
// flat LIMIT would starve the second parent.
func TestEdges_EagerLoadPerParentLimit(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	seedGraph(t, client)

	q := client.Author.Query().Order(author.ByID())
	fc, ok := q.(runtime.FieldCollectable)
	require.True(t, ok, "generated queries implement runtime.FieldCollectable")
	fc.WithEdgeLoad(author.EdgeArticles, runtime.Limit(2))
	authors, err := q.All(ctx)
	require.NoError(t, err)
	require.Len(t, authors, 3)
	assert.Equal(t, []string{"a1", "a2"}, titles(authors[0].Edges.Articles))
	assert.Equal(t, []string{"b1"}, titles(authors[1].Edges.Articles))
	assert.Empty(t, authors[2].Edges.Articles)
}

// Shape 14 under FeatureNamedEdges: two differently filtered loads of one
// edge, side by side.
func TestEdges_NamedEagerLoads(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	g := seedGraph(t, client)

	q, ok := client.Author.Query().Where(author.IDField.EQ(g.ann.ID)).(*query.AuthorQuery)
	require.True(t, ok)
	q.WithNamedArticles("sport", func(q *query.ArticleQuery) { q.Where(article.SectionField.EQ("sport")) }).
		WithNamedArticles("long", func(q *query.ArticleQuery) { q.Where(article.WordsField.GTE(20)).Order(article.ByID()) })
	ann, err := q.Only(ctx)
	require.NoError(t, err)

	sport, err := ann.NamedArticles("sport")
	require.NoError(t, err)
	assert.Equal(t, []string{"a3"}, titles(sport))
	long, err := ann.NamedArticles("long")
	require.NoError(t, err)
	assert.Equal(t, []string{"a2", "a3"}, titles(long))
	_, err = ann.NamedArticles("never-requested")
	require.True(t, gen.IsNotLoaded(err), "%v", err)
}

// Shape 14: HasXxx / HasXxxWith over every edge kind.
func TestEdges_Predicates(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	seedGraph(t, client)

	authorsWhere := func(ps ...func(*sql.Selector)) []string {
		t.Helper()
		q := client.Author.Query().Order(author.ByID())
		for _, p := range ps {
			q = q.Where(p)
		}
		return must(q.Select(author.FieldName).Strings(ctx))
	}
	articlesWhere := func(ps ...func(*sql.Selector)) []string {
		t.Helper()
		q := client.Article.Query().Order(article.ByID())
		for _, p := range ps {
			q = q.Where(p)
		}
		return must(q.Select(article.FieldTitle).Strings(ctx))
	}

	assert.Equal(t, []string{"ann", "bob"}, authorsWhere(author.HasArticles()))
	assert.Equal(t, []string{"cy"}, authorsWhere(author.Not(author.HasArticles())))
	assert.Equal(t, []string{"bob"}, authorsWhere(author.HasArticlesWith(article.WordsField.GT(30))))
	assert.Equal(t, []string{"bob"}, authorsWhere(author.HasEdited()))
	assert.Equal(t, []string{"ann"},
		authorsWhere(author.HasArticlesWith(article.HasTopicsWith(topic.NameField.EQ("db")))))

	assert.Equal(t, []string{"a1", "a3"}, articlesWhere(article.HasEditor()))
	assert.Equal(t, []string{"a2", "b1"}, articlesWhere(article.Not(article.HasEditor())))
	assert.Equal(t, []string{"b1"}, articlesWhere(article.HasAuthorWith(author.NameField.EQ("bob"))))
	assert.Equal(t, []string{"a1", "a2"}, articlesWhere(article.HasTopics()))
	assert.Equal(t, []string{"a1"}, articlesWhere(article.HasTopicsWith(topic.NameField.EQ("db"))))
	assert.Equal(t, []string{"a1", "a3"},
		articlesWhere(article.HasEditorWith(author.HasArticlesWith(article.SectionField.EQ("sport")))))
}

// Shape 15: client.Tx commit and rollback, and WithTx.
func TestTx_CommitRollbackAndWithTx(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	count := func() int { return must(client.Author.Query().Count(ctx)) }

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	kept, err := tx.Author.Create().SetName("kept").Save(ctx)
	require.NoError(t, err)
	_, err = tx.Article.Create().SetTitle("in-tx").SetAuthorID(kept.ID).Save(ctx)
	require.NoError(t, err)
	inTx, err := tx.Author.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, inTx, "the transaction reads its own writes")
	require.NoError(t, tx.Commit())
	assert.Equal(t, 1, count())

	// An entity created in a committed transaction reads through its edges
	// only after Unwrap.
	_, err = kept.QueryArticles().Count(ctx)
	require.Error(t, err, "the entity still holds the finished transaction")
	n, err := kept.Unwrap().QueryArticles().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	tx, err = client.Tx(ctx)
	require.NoError(t, err)
	_, err = tx.Author.Create().SetName("dropped").Save(ctx)
	require.NoError(t, err)
	require.NoError(t, tx.Author.UpdateOneID(kept.ID).SetName("renamed").Exec(ctx))
	require.NoError(t, tx.Rollback())
	assert.Equal(t, 1, count())
	assert.Equal(t, "kept", client.Author.GetX(ctx, kept.ID).Name)

	require.NoError(t, gen.WithTx(ctx, client, func(tx *gen.Tx) error {
		return tx.Author.Create().SetName("with-tx").Exec(ctx)
	}))
	assert.Equal(t, 2, count())

	boom := errors.New("boom")
	err = gen.WithTx(ctx, client, func(tx *gen.Tx) error {
		if err := tx.Author.Create().SetName("with-tx-dropped").Exec(ctx); err != nil {
			return err
		}
		return boom
	})
	require.ErrorIs(t, err, boom)
	assert.Equal(t, 2, count())

	require.Panics(t, func() {
		_ = gen.WithTx(ctx, client, func(tx *gen.Tx) error {
			tx.Author.Create().SetName("panicked").ExecX(ctx)
			panic("stop")
		})
	})
	assert.Equal(t, 2, count(), "a panic rolls the transaction back")
}

// Shape 15, the narrow path: code that never names the root package runs
// reads, RunTx and StartTx through runtime.TxRunner and From.
func TestNarrow_ReadsAndTransactsThroughTxRunner(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	ann := client.Author.Create().SetName("ann").SaveX(ctx)
	name := func() string { return client.Author.GetX(ctx, ann.ID).Name }
	articles := func() []string {
		return must(client.Article.Query().Order(article.ByID()).Select(article.FieldTitle).Strings(ctx))
	}

	got, err := narrow.AuthorByName(ctx, client, "ann")
	require.NoError(t, err)
	assert.Equal(t, ann.ID, got.ID)

	// A hook registered on the root client reaches mutations made through
	// From: both write to the same hook store.
	var hooked []string
	client.Author.Use(func(next gen.Mutator) gen.Mutator {
		return gen.MutateFunc(func(ctx context.Context, m gen.Mutation) (gen.Value, error) {
			hooked = append(hooked, m.Op().String())
			return next.Mutate(ctx, m)
		})
	})

	require.NoError(t, narrow.RenameWithArticle(ctx, client, ann.ID, "ann b", "kept", false))
	assert.Equal(t, "ann b", name())
	assert.Equal(t, []string{"kept"}, articles())
	assert.Len(t, hooked, 1, "the rename ran the client's hook")

	err = narrow.RenameWithArticle(ctx, client, ann.ID, "ann c", "dropped", true)
	require.ErrorIs(t, err, narrow.ErrRejected)
	assert.Equal(t, "ann b", name(), "RunTx rolls back as a unit")
	assert.Equal(t, []string{"kept"}, articles())

	// A failure inside RunTx (the validator on Article.title) rolls the
	// rename back too.
	err = narrow.RenameWithArticle(ctx, client, ann.ID, "ann d", "", false)
	require.True(t, gen.IsValidationError(err), "%v", err)
	assert.Equal(t, "ann b", name())

	require.NoError(t, narrow.Rename(ctx, client, ann.ID, "ann e", true))
	assert.Equal(t, "ann e", name())
	require.NoError(t, narrow.Rename(ctx, client, ann.ID, "ann f", false))
	assert.Equal(t, "ann e", name(), "a StartTx transaction that was not committed is not kept")
	err = narrow.Rename(ctx, client, ann.ID+100, "nobody", true)
	require.True(t, gen.IsNotFound(err), "%v", err)
}

// TestNarrow_DoesNotImportRoot pins what makes the narrow path worth having:
// the package depends on the entity client packages it names, not on the
// generated root package, whose Client reaches every entity's client.
func TestNarrow_DoesNotImportRoot(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not found")
	}
	out, err := exec.Command(goBin, "list", "-deps", "./narrow").CombinedOutput()
	require.NoError(t, err, string(out))
	deps := strings.Fields(string(out))
	require.Contains(t, deps, "example.com/matrix/velox/client/author")
	require.Contains(t, deps, "example.com/matrix/velox/client/article")
	for _, banned := range []string{
		"example.com/matrix/velox",
		"example.com/matrix/velox/client/wide",
		"example.com/matrix/velox/query",
	} {
		assert.NotContains(t, deps, banned, "narrow must not depend on %s", banned)
	}
}

// Shape 16: UpdateOne.Exec and UpdateOne.Save both persist, and both report
// a missing row.
func TestUpdateOne_ExecAndSave(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	a := client.Author.Create().SetName("a").SaveX(ctx)
	b := client.Author.Create().SetName("b").SaveX(ctx)

	saved, err := client.Author.UpdateOneID(a.ID).SetName("a-saved").Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, a.ID, saved.ID)
	assert.Equal(t, "a-saved", saved.Name)
	assert.Equal(t, "a-saved", client.Author.GetX(ctx, a.ID).Name)

	require.NoError(t, client.Author.UpdateOneID(a.ID).SetName("a-exec").Exec(ctx))
	assert.Equal(t, "a-exec", client.Author.GetX(ctx, a.ID).Name)
	require.NoError(t, client.Author.UpdateOne(a).SetName("a-entity").Exec(ctx))
	assert.Equal(t, "a-entity", client.Author.GetX(ctx, a.ID).Name)
	assert.Equal(t, "b", client.Author.GetX(ctx, b.ID).Name, "the other row is untouched")

	// A no-op update of an existing row is not NotFound.
	require.NoError(t, client.Author.UpdateOneID(a.ID).SetName("a-entity").Exec(ctx))

	missing := b.ID + 100
	err = client.Author.UpdateOneID(missing).SetName("x").Exec(ctx)
	require.True(t, gen.IsNotFound(err), "Exec on a missing id: %v", err)
	_, err = client.Author.UpdateOneID(missing).SetName("x").Save(ctx)
	require.True(t, gen.IsNotFound(err), "Save on a missing id: %v", err)
	err = client.Author.DeleteOneID(missing).Exec(ctx)
	require.True(t, gen.IsNotFound(err), "DeleteOne on a missing id: %v", err)

	// Exec runs the validators too.
	err = client.Author.UpdateOneID(a.ID).SetName("").Exec(ctx)
	require.True(t, gen.IsValidationError(err), "%v", err)
	assert.Equal(t, "a-entity", client.Author.GetX(ctx, a.ID).Name)
}

// Shape 17: Count / Exist / IDs / First / Only.
func TestQuery_Terminals(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	g := seedGraph(t, client)
	sport := func() entity.ArticleQuerier {
		return client.Article.Query().Where(article.SectionField.EQ("sport")).Order(article.ByID())
	}
	none := func() entity.ArticleQuerier {
		return client.Article.Query().Where(article.SectionField.EQ("none"))
	}

	assert.Equal(t, 4, must(client.Article.Query().Count(ctx)))
	assert.Equal(t, 2, must(sport().Count(ctx)))
	assert.True(t, must(sport().Exist(ctx)))
	assert.False(t, must(none().Exist(ctx)))
	assert.Equal(t, []int{g.a3.ID, g.b1.ID}, must(sport().IDs(ctx)))
	assert.Equal(t, g.a3.ID, must(sport().First(ctx)).ID)
	assert.Equal(t, g.a3.ID, must(sport().FirstID(ctx)))
	assert.Equal(t, g.b1.ID, must(sport().Offset(1).Limit(1).Only(ctx)).ID)

	_, err := none().First(ctx)
	require.True(t, gen.IsNotFound(err), "First on no rows: %v", err)
	_, err = none().Only(ctx)
	require.True(t, gen.IsNotFound(err), "Only on no rows: %v", err)
	_, err = sport().Only(ctx)
	require.True(t, gen.IsNotSingular(err), "Only on two rows: %v", err)
	_, err = sport().OnlyID(ctx)
	require.True(t, gen.IsNotSingular(err), "OnlyID on two rows: %v", err)
	only, err := client.Article.Query().Where(article.TitleField.EQ("a2")).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, g.a2.ID, only.ID)
}

// Shape 17: Select, Aggregate, GroupBy.
func TestQuery_SelectAggregateGroupBy(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	g := seedGraph(t, client)

	assert.Equal(t, []int{10, 20, 30, 40},
		must(client.Article.Query().Order(article.ByWords()).Select(article.FieldWords).Ints(ctx)))
	partial, err := client.Article.Query().Order(article.ByID()).Select(article.FieldTitle).All(ctx)
	require.NoError(t, err)
	require.Len(t, partial, 4)
	assert.Equal(t, "a1", partial[0].Title)
	assert.Zero(t, partial[0].Words, "an unselected column stays zero")

	assert.Equal(t, 100, must(client.Article.Query().Aggregate(gen.Sum(article.FieldWords)).Int(ctx)))
	assert.Equal(t, 70, must(client.Article.Query().Where(article.SectionField.EQ("sport")).
		Aggregate(gen.Sum(article.FieldWords)).Int(ctx)))
	var stats []struct {
		Min, Max, N int
		Mean        float64
	}
	require.NoError(t, client.Article.Query().Aggregate(
		gen.As(gen.Min(article.FieldWords), "min"),
		gen.As(gen.Max(article.FieldWords), "max"),
		gen.As(gen.Mean(article.FieldWords), "mean"),
		gen.As(gen.Count(), "n"),
	).Scan(ctx, &stats))
	require.Len(t, stats, 1)
	assert.Equal(t, 10, stats[0].Min)
	assert.Equal(t, 40, stats[0].Max)
	assert.Equal(t, 4, stats[0].N)
	assert.InDelta(t, 25.0, stats[0].Mean, 1e-9)

	var bySection []struct {
		Section string `json:"section"`
		Total   int    `json:"total"`
		Count   int    `json:"count"`
	}
	require.NoError(t, client.Article.Query().
		Order(gen.Asc(article.FieldSection)).
		GroupBy(article.FieldSection).
		Aggregate(gen.As(gen.Sum(article.FieldWords), "total"), gen.Count()).
		Scan(ctx, &bySection))
	require.Len(t, bySection, 2)
	assert.Equal(t, "news", bySection[0].Section)
	assert.Equal(t, 30, bySection[0].Total)
	assert.Equal(t, 2, bySection[0].Count)
	assert.Equal(t, "sport", bySection[1].Section)
	assert.Equal(t, 70, bySection[1].Total)
	assert.Equal(t, 2, bySection[1].Count)

	var byAuthor []struct {
		AuthorID int `json:"author_id"`
		Count    int `json:"count"`
	}
	require.NoError(t, client.Article.Query().
		Order(gen.Asc(article.FieldAuthorID)).
		GroupBy(article.FieldAuthorID).Aggregate(gen.Count()).Scan(ctx, &byAuthor))
	require.Len(t, byAuthor, 2)
	assert.Equal(t, g.ann.ID, byAuthor[0].AuthorID)
	assert.Equal(t, 3, byAuthor[0].Count)
	assert.Equal(t, 1, byAuthor[1].Count)
}

// The remaining enabled features, each with one assertion that fails if the
// feature generated something inert: sql/modifier, sql/execquery, intercept.
func TestFeatures_ModifierExecQueryAndIntercept(t *testing.T) {
	client := openClient(t)
	ctx := context.Background()
	seedGraph(t, client)

	// sql/modifier.
	got, err := client.Article.Query().Order(article.ByID()).
		Modify(func(s *sql.Selector) { s.Where(sql.GT(s.C(article.FieldWords), 25)) }).All(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"a3", "b1"}, titles(got))

	// sql/execquery.
	res, err := client.ExecContext(ctx, "UPDATE articles SET words = words + 1 WHERE section = ?", "sport")
	require.NoError(t, err)
	affected, err := res.RowsAffected()
	require.NoError(t, err)
	assert.EqualValues(t, 2, affected)
	rows, err := client.QueryContext(ctx, "SELECT SUM(words) FROM articles")
	require.NoError(t, err)
	require.True(t, rows.Next())
	var total int
	require.NoError(t, rows.Scan(&total))
	require.NoError(t, rows.Close())
	assert.Equal(t, 102, total)

	// intercept: a traverser narrows every read terminal of the entity.
	client.Article.Intercept(intercept.TraverseArticle(func(_ context.Context, q *query.ArticleQuery) error {
		q.Where(article.SectionField.EQ("news"))
		return nil
	}))
	assert.Equal(t, 2, must(client.Article.Query().Count(ctx)))
	assert.Equal(t, []string{"a1", "a2"},
		must(client.Article.Query().Order(article.ByID()).Select(article.FieldTitle).Strings(ctx)))
	ann := must(client.Author.Query().Where(author.NameField.EQ("ann")).WithArticles().Only(ctx))
	assert.Len(t, ann.Edges.Articles, 2, "the interceptor reaches the eager load")
}
