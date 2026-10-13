package shapes

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/syssam/velox/tests/orm/shapes/velox/predicate"

	client "github.com/syssam/velox/tests/orm/shapes/velox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/tests/orm/shapes/velox/article"
	"github.com/syssam/velox/tests/orm/shapes/velox/author"
	"github.com/syssam/velox/tests/orm/shapes/velox/entity"
	"github.com/syssam/velox/tests/orm/shapes/velox/locker"
	"github.com/syssam/velox/tests/orm/shapes/velox/note"
	"github.com/syssam/velox/tests/orm/shapes/velox/passport"
	"github.com/syssam/velox/tests/orm/shapes/velox/topic"
)

// lib is the fixture of the edge tests:
//
//	authors   ann(30) bob(40) cid(50) dee(60)          cid has no edge at all
//	articles  a1: author ann, editor bob, 100 words, topics go+sql, banned rust, notes n1+n2
//	          a2: author ann, no editor,  200 words, topics go
//	          b1: author bob, editor bob, 300 words, no topic, banned go+rust, note n3
//	          d1: author dee, editor ann,  50 words, topics sql
//	passports ann, bob          lockers  l1 held by ann, l2 held by nobody
type lib struct {
	ann, bob, cid, dee *entity.Author
	a1, a2, b1, d1     *entity.Article
	tgo, sql, rust     *entity.Topic
	n1, n2, n3         *entity.Note
	pAnn, pBob         *entity.Passport
	l1, l2             *entity.Locker
}

func library(t *testing.T, db velox.Executor) *lib {
	t.Helper()
	ctx := context.Background()
	l := &lib{}
	mk := func(name string, age int) *entity.Author {
		return must(client.Author.Create(db).Set(author.Name.Set(name), author.Age.Set(age)).Save(ctx))
	}
	l.ann, l.bob, l.cid, l.dee = mk("ann", 30), mk("bob", 40), mk("cid", 50), mk("dee", 60)
	tp := func(name string) *entity.Topic {
		return must(client.Topic.Create(db).Set(topic.Name.Set(name)).Save(ctx))
	}
	l.tgo, l.sql, l.rust = tp("go"), tp("sql"), tp("rust")
	// Every way to set a to-one edge on create: through the edge, through the
	// bound field; and M2M on create.
	l.a1 = must(client.Article.Create(db).Set(article.Title.Set("a1"), article.Words.Set(100), article.Author.Set(l.ann.ID),
		article.Editor.Set(l.bob.ID), article.Topics.Add(l.tgo.ID, l.sql.ID), article.BannedTopics.Add(l.rust.ID)).Save(ctx))
	l.a2 = must(client.Article.Create(db).Set(article.Title.Set("a2"), article.Words.Set(200), article.AuthorID.Set(l.ann.ID),
		article.Section.Set("sport"), article.Topics.Add(l.tgo.ID)).Save(ctx))
	l.b1 = must(client.Article.Create(db).Set(article.Title.Set("b1"), article.Words.Set(300), article.Author.Set(l.bob.ID),
		article.EditorID.Set(l.bob.ID), article.BannedTopics.Add(l.tgo.ID, l.rust.ID)).Save(ctx))
	l.d1 = must(client.Article.Create(db).Set(article.Title.Set("d1"), article.Words.Set(50), article.AuthorID.Set(l.dee.ID),
		article.Editor.Set(l.ann.ID), article.Topics.Add(l.sql.ID)).Save(ctx))
	nt := func(text string) *entity.Note {
		return must(client.Note.Create(db).Set(note.Text.Set(text)).Save(ctx))
	}
	l.n1, l.n2, l.n3 = nt("n1"), nt("n2"), nt("n3")
	// O2M from the one side, on update.
	must(client.Article.Update(db).Set(article.Notes.Add(l.n1.ID, l.n2.ID)).Where(article.ID.EQ(l.a1.ID)).Exec(ctx))
	must(client.Article.Update(db).Set(article.Notes.Add(l.n3.ID)).Where(article.ID.EQ(l.b1.ID)).Exec(ctx))
	// O2O from the side holding the key, and from the other side.
	l.pAnn = must(client.Passport.Create(db).Set(passport.Number.Set("P-ann"), passport.Owner.Set(l.ann.ID)).Save(ctx))
	l.pBob = must(client.Passport.Create(db).Set(passport.Number.Set("P-bob"), passport.Owner.Set(l.bob.ID)).Save(ctx))
	l.l1 = must(client.Locker.Create(db).Set(locker.Code.Set("l1"), locker.HolderID.Set(l.ann.ID)).Save(ctx))
	l.l2 = must(client.Locker.Create(db).Set(locker.Code.Set("l2")).Save(ctx))
	return l
}

func titles(as []*entity.Article) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Title
	}
	return out
}

func sorted(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

func topicNames(ts []*entity.Topic) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

func authorNames(as []*entity.Author) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.Name
	}
	return out
}

func notLoaded(t *testing.T, err error, edge string) {
	t.Helper()
	var nle *velox.NotLoadedError
	require.ErrorAs(t, err, &nle)
	assert.Equal(t, edge, nle.Edge())
}

func TestEdges_CreateReturnsKeysAndRejectsBadOnes(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	l := library(t, db)

	// The entity Create returns carries the key, whichever way it was set.
	assert.Equal(t, l.ann.ID, l.a1.AuthorID, "set through the edge")
	assert.Equal(t, l.ann.ID, l.a2.AuthorID, "set through the field")
	require.NotNil(t, l.a1.EditorID)
	assert.Equal(t, l.bob.ID, *l.a1.EditorID)
	assert.Nil(t, l.a2.EditorID)
	require.NotNil(t, l.l1.HolderID)
	assert.Equal(t, l.ann.ID, *l.l1.HolderID)
	assert.Nil(t, l.l2.HolderID)
	assert.Equal(t, 3, rawValue[int](t, db, "SELECT COUNT(*) FROM article_topics WHERE article_id IN (?, ?)", l.a1.ID, l.a2.ID))
	assert.Equal(t, 3, rawValue[int](t, db, "SELECT COUNT(*) FROM article_bans"))
	assert.Equal(t, 3, rawValue[int](t, db, "SELECT COUNT(*) FROM notes WHERE article_ref IS NOT NULL"))

	// A required to-one edge is required.
	_, err := client.Article.Create(db).Set(article.Title.Set("orphan")).Save(ctx)
	invalid(t, err, "author_id")
	// A key that references nothing is a constraint error.
	_, err = client.Article.Create(db).Set(article.Title.Set("ghost"), article.Author.Set(9999)).Save(ctx)
	require.True(t, velox.IsConstraintError(err), "got %v", err)
	_, err = client.Article.Create(db).Set(article.Title.Set("ghost"), article.AuthorID.Set(l.ann.ID), article.Topics.Add(9999)).Save(ctx)
	require.True(t, velox.IsConstraintError(err), "got %v", err)
	assert.Zero(t, must(client.Article.Query(db).Where(article.Title.In("orphan", "ghost")).Count(ctx)), "a failed create wrote nothing, not even the row before its edges")
	// O2O is unique: ann already has a passport and holds a locker.
	_, err = client.Passport.Create(db).Set(passport.Number.Set("P-ann-2"), passport.Owner.Set(l.ann.ID)).Save(ctx)
	require.True(t, velox.IsConstraintError(err), "got %v", err)
	_, err = client.Locker.Create(db).Set(locker.Code.Set("l3"), locker.Holder.Set(l.ann.ID)).Save(ctx)
	require.True(t, velox.IsConstraintError(err), "got %v", err)

	// O2M and O2O from the side that does not hold the key, on create. A row
	// that already belongs to another parent cannot be taken over (the SQL
	// layer's rule, as in velox and Ent): that is a constraint error and
	// nothing is written.
	_, err = client.Author.Create(db).Set(author.Name.Set("eve"), author.Articles.Add(l.a2.ID, l.d1.ID)).Save(ctx)
	require.True(t, velox.IsConstraintError(err), "got %v", err)
	assert.Zero(t, must(client.Author.Query(db).Where(author.Name.EQ("eve")).Count(ctx)))
	eve := must(client.Author.Create(db).Set(author.Name.Set("eve"), author.Edited.Add(l.a2.ID), author.Locker.Set(l.l2.ID)).Save(ctx))
	assert.Equal(t, []string{"a2"}, titles(must(eve.QueryEdited(db).All(ctx))))
	assert.Equal(t, "l2", must(eve.QueryLocker(db).Only(ctx)).Code)
	assert.Equal(t, []string{"a1", "a2"}, titles(must(l.ann.QueryArticles(db).OrderBy(article.Title.Asc()).All(ctx))), "ann kept her articles")

	// Bulk create with edges per row.
	rows := must(client.Article.CreateBulk(db,
		client.Article.Create(db).Set(article.Title.Set("x1"), article.Author.Set(l.cid.ID), article.Topics.Add(l.rust.ID)),
		client.Article.Create(db).Set(article.Title.Set("x2"), article.AuthorID.Set(l.cid.ID)),
		client.Article.Create(db).Set(article.Title.Set("x3"), article.Author.Set(l.dee.ID), article.Editor.Set(l.cid.ID), article.Topics.Add(l.tgo.ID, l.rust.ID)),
	).Save(ctx))
	require.Len(t, rows, 3)
	assert.Equal(t, []int{l.cid.ID, l.cid.ID, l.dee.ID}, []int{rows[0].AuthorID, rows[1].AuthorID, rows[2].AuthorID})
	assert.Equal(t, []string{"rust"}, topicNames(must(rows[0].QueryTopics(db).All(ctx))))
	assert.Empty(t, must(rows[1].QueryTopics(db).All(ctx)))
	assert.Equal(t, []string{"go", "rust"}, topicNames(must(rows[2].QueryTopics(db).OrderBy(topic.Name.Asc()).All(ctx))))
	assert.Equal(t, "cid", must(rows[2].QueryEditor(db).Only(ctx)).Name)
}

func TestEdges_EagerLoadEveryKind(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	l := library(t, db)

	// O2M, twice between the same pair, O2O twice, in one query. cid has
	// nothing: every edge is loaded and empty.
	authors := must(client.Author.Query(db).OrderBy(author.Name.Asc()).
		With(author.Articles.Load(client.Article.Query(db).OrderBy(article.Title.Desc()))).With(author.Edited).With(author.Passport).With(author.Locker).All(ctx))
	require.Len(t, authors, 4)
	ann, bob, cid, dee := authors[0], authors[1], authors[2], authors[3]
	assert.Equal(t, []string{"a2", "a1"}, titles(ann.Edges.Articles), "the order of the load's query")
	assert.Equal(t, []string{"d1"}, titles(ann.Edges.Edited), "edited is not articles")
	assert.Equal(t, []string{"b1"}, titles(bob.Edges.Articles))
	assert.Equal(t, []string{"a1", "b1"}, sorted(titles(bob.Edges.Edited)))
	assert.Equal(t, []string{"d1"}, titles(dee.Edges.Articles))
	arts, err := cid.Edges.ArticlesOrErr()
	require.NoError(t, err, "loaded and empty is not an error")
	assert.Empty(t, arts)
	arts, err = dee.Edges.EditedOrErr()
	require.NoError(t, err)
	assert.Empty(t, arts)
	p, err := ann.Edges.PassportOrErr()
	require.NoError(t, err)
	assert.Equal(t, "P-ann", p.Number)
	_, err = cid.Edges.PassportOrErr()
	assert.ErrorIs(t, err, velox.ErrNotFound, "a to-one edge that was loaded and is empty")
	assert.Nil(t, cid.Edges.Passport)
	lk, err := ann.Edges.LockerOrErr()
	require.NoError(t, err)
	assert.Equal(t, "l1", lk.Code)
	_, err = bob.Edges.LockerOrErr()
	assert.ErrorIs(t, err, velox.ErrNotFound)

	// Nothing requested: every OrErr says not loaded, whatever the row has.
	plain := must(client.Author.Query(db).Where(author.Name.EQ("ann")).Only(ctx))
	_, err = plain.Edges.ArticlesOrErr()
	notLoaded(t, err, "articles")
	_, err = plain.Edges.EditedOrErr()
	notLoaded(t, err, "edited")
	_, err = plain.Edges.PassportOrErr()
	notLoaded(t, err, "passport")
	_, err = plain.Edges.LockerOrErr()
	notLoaded(t, err, "locker")
	// Requesting one edge does not mark its neighbors in the struct loaded.
	one := must(client.Author.Query(db).Where(author.Name.EQ("ann")).With(author.Edited).Only(ctx))
	_, err = one.Edges.ArticlesOrErr()
	notLoaded(t, err, "articles")
	_, err = one.Edges.PassportOrErr()
	notLoaded(t, err, "passport")
	assert.Equal(t, []string{"d1"}, titles(one.Edges.Edited))

	// M2O required and optional, M2M twice between the same pair, O2M with
	// an overridden key column.
	articles := must(client.Article.Query(db).OrderBy(article.Title.Asc()).
		With(article.Author).With(article.Editor).With(article.Topics.Load(client.Topic.Query(db).OrderBy(topic.Name.Asc()))).With(article.BannedTopics.Load(client.Topic.Query(db).OrderBy(topic.Name.Desc()))).With(article.Notes).All(ctx))
	require.Len(t, articles, 4)
	a1, a2, b1, d1 := articles[0], articles[1], articles[2], articles[3]
	assert.Equal(t, []string{"ann", "ann", "bob", "dee"}, []string{a1.Edges.Author.Name, a2.Edges.Author.Name, b1.Edges.Author.Name, d1.Edges.Author.Name})
	assert.Equal(t, "bob", a1.Edges.Editor.Name)
	_, err = a2.Edges.EditorOrErr()
	assert.ErrorIs(t, err, velox.ErrNotFound, "a NULL key between two set ones")
	assert.Equal(t, "bob", b1.Edges.Editor.Name)
	assert.Equal(t, "ann", d1.Edges.Editor.Name)
	assert.Equal(t, []string{"go", "sql"}, topicNames(a1.Edges.Topics))
	assert.Equal(t, []string{"go"}, topicNames(a2.Edges.Topics))
	ts, err := b1.Edges.TopicsOrErr()
	require.NoError(t, err)
	assert.Empty(t, ts)
	assert.Equal(t, []string{"sql"}, topicNames(d1.Edges.Topics))
	assert.Equal(t, []string{"rust"}, topicNames(a1.Edges.BannedTopics))
	assert.Empty(t, a2.Edges.BannedTopics)
	assert.Equal(t, []string{"rust", "go"}, topicNames(b1.Edges.BannedTopics), "the second M2M has its own table and its own order")
	require.Len(t, a1.Edges.Notes, 2)
	assert.Empty(t, a2.Edges.Notes)
	require.Len(t, b1.Edges.Notes, 1)
	assert.Equal(t, "n3", b1.Edges.Notes[0].Text)

	// The inverse sides.
	topics := must(client.Topic.Query(db).OrderBy(topic.Name.Asc()).
		With(topic.Articles.Load(client.Article.Query(db).OrderBy(article.Title.Asc()))).With(topic.BannedIn.Load(client.Article.Query(db).OrderBy(article.Title.Asc()))).All(ctx))
	require.Len(t, topics, 3)
	assert.Equal(t, []string{"a1", "a2"}, titles(topics[0].Edges.Articles))
	assert.Equal(t, []string{"b1"}, titles(topics[0].Edges.BannedIn))
	assert.Empty(t, topics[1].Edges.Articles, "rust is in no article")
	assert.Equal(t, []string{"a1", "b1"}, titles(topics[1].Edges.BannedIn))
	assert.Equal(t, []string{"a1", "d1"}, titles(topics[2].Edges.Articles))
	assert.Empty(t, topics[2].Edges.BannedIn)
	passports := must(client.Passport.Query(db).OrderBy(passport.Number.Asc()).With(passport.Owner).All(ctx))
	require.Len(t, passports, 2)
	assert.Equal(t, "ann", passports[0].Edges.Owner.Name)
	assert.Equal(t, "bob", passports[1].Edges.Owner.Name)
	lockers := must(client.Locker.Query(db).OrderBy(locker.Code.Asc()).With(locker.Holder).All(ctx))
	require.Len(t, lockers, 2)
	assert.Equal(t, "ann", lockers[0].Edges.Holder.Name)
	_, err = lockers[1].Edges.HolderOrErr()
	assert.ErrorIs(t, err, velox.ErrNotFound)

	// With on First and Only.
	first := must(client.Article.Query(db).OrderBy(article.Words.Desc()).With(article.Author).With(article.Topics).First(ctx))
	assert.Equal(t, "b1", first.Title)
	assert.Equal(t, "bob", first.Edges.Author.Name)
	only := must(client.Author.Query(db).Where(author.ID.EQ(l.dee.ID)).With(author.Articles).Only(ctx))
	assert.Equal(t, []string{"d1"}, titles(only.Edges.Articles))

	// A query that matches no row loads nothing and does not fail.
	nobody := must(client.Author.Query(db).Where(author.Name.EQ("nobody")).With(author.Articles).With(author.Passport).All(ctx))
	assert.Empty(t, nobody)

	// With on a query of parents that has its own limit and offset.
	page := must(client.Author.Query(db).OrderBy(author.Name.Asc()).Offset(1).Limit(2).With(author.Articles).All(ctx))
	require.Len(t, page, 2)
	assert.Equal(t, []string{"b1"}, titles(page[0].Edges.Articles))
	assert.Empty(t, page[1].Edges.Articles)
}

func TestEdges_EagerLoadNestedLimitAndFilters(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	l := library(t, db)

	// Three levels: authors -> articles -> (topics, editor -> passport).
	authors := must(client.Author.Query(db).OrderBy(author.Name.Asc()).With(author.Articles.Load(client.Article.Query(db).OrderBy(article.Title.Asc()).With(article.Topics.Load(client.Topic.Query(db).OrderBy(topic.Name.Asc()).With(topic.BannedIn.Load(client.Article.Query(db).OrderBy(article.Title.Asc()))))).With(article.Editor.Load(client.Author.Query(db).With(author.Passport))))).All(ctx))
	require.Len(t, authors, 4)
	ann := authors[0]
	require.Equal(t, []string{"a1", "a2"}, titles(ann.Edges.Articles))
	a1, a2 := ann.Edges.Articles[0], ann.Edges.Articles[1]
	assert.Equal(t, []string{"go", "sql"}, topicNames(a1.Edges.Topics))
	assert.Equal(t, []string{"b1"}, titles(a1.Edges.Topics[0].Edges.BannedIn), "go is banned in b1")
	assert.Empty(t, a1.Edges.Topics[1].Edges.BannedIn)
	ed, err := a1.Edges.EditorOrErr()
	require.NoError(t, err)
	assert.Equal(t, "bob", ed.Name)
	pp, err := ed.Edges.PassportOrErr()
	require.NoError(t, err)
	assert.Equal(t, "P-bob", pp.Number)
	_, err = a2.Edges.EditorOrErr()
	assert.ErrorIs(t, err, velox.ErrNotFound)
	_, err = a1.Edges.AuthorOrErr()
	notLoaded(t, err, "author")
	// dee's article was edited by ann, whose passport is loaded there too.
	d1 := authors[3].Edges.Articles[0]
	assert.Equal(t, "P-ann", d1.Edges.Editor.Edges.Passport.Number)
	_, err = d1.Edges.Editor.Edges.ArticlesOrErr()
	notLoaded(t, err, "articles")

	// A predicate on the load filters the neighbors, not the parents.
	authors = must(client.Author.Query(db).OrderBy(author.Name.Asc()).
		With(author.Articles.Load(client.Article.Query(db).Where(article.Words.GTE(200)))).All(ctx))
	require.Len(t, authors, 4)
	assert.Equal(t, []string{"a2"}, titles(authors[0].Edges.Articles))
	assert.Equal(t, []string{"b1"}, titles(authors[1].Edges.Articles))
	assert.Empty(t, authors[3].Edges.Articles, "dee's only article is filtered out; the edge is still loaded")
	_, err = authors[3].Edges.ArticlesOrErr()
	require.NoError(t, err)
	// ...on a to-one edge too: the neighbor that does not match is absent.
	articles := must(client.Article.Query(db).OrderBy(article.Title.Asc()).
		With(article.Author.Load(client.Author.Query(db).Where(author.Age.GT(35)))).With(article.Editor.Load(client.Author.Query(db).Where(author.Name.EQ("bob")))).All(ctx))
	_, err = articles[0].Edges.AuthorOrErr()
	assert.ErrorIs(t, err, velox.ErrNotFound, "ann is 30")
	assert.Equal(t, "bob", articles[2].Edges.Author.Name)
	assert.Equal(t, "bob", articles[0].Edges.Editor.Name)
	_, err = articles[3].Edges.EditorOrErr()
	assert.ErrorIs(t, err, velox.ErrNotFound, "d1 is edited by ann")
	// ...and through an edge predicate of the neighbor.
	authors = must(client.Author.Query(db).OrderBy(author.Name.Asc()).
		With(author.Articles.Load(client.Article.Query(db).Where(article.Topics.HasWith(topic.Name.EQ("sql"))))).All(ctx))
	assert.Equal(t, []string{"a1"}, titles(authors[0].Edges.Articles))
	assert.Empty(t, authors[1].Edges.Articles)
	assert.Equal(t, []string{"d1"}, titles(authors[3].Edges.Articles))

	// A limit on the load is per parent, after the load's order.
	must(client.Article.Create(db).Set(article.Title.Set("a3"), article.Words.Set(150), article.Author.Set(l.ann.ID)).Save(ctx))
	must(client.Article.Create(db).Set(article.Title.Set("b2"), article.Words.Set(10), article.Author.Set(l.bob.ID)).Save(ctx))
	authors = must(client.Author.Query(db).OrderBy(author.Name.Asc()).
		With(author.Articles.Load(client.Article.Query(db).OrderBy(article.Words.Desc()).Limit(2))).All(ctx))
	assert.Equal(t, []string{"a2", "a3"}, titles(authors[0].Edges.Articles), "ann's two longest of three")
	assert.Equal(t, []string{"b1", "b2"}, titles(authors[1].Edges.Articles), "bob's two, not the two left over from a global limit")
	assert.Empty(t, authors[2].Edges.Articles)
	assert.Equal(t, []string{"d1"}, titles(authors[3].Edges.Articles))
	authors = must(client.Author.Query(db).OrderBy(author.Name.Asc()).
		With(author.Articles.Load(client.Article.Query(db).OrderBy(article.Words.Asc()).Limit(1))).All(ctx))
	assert.Equal(t, []string{"a1"}, titles(authors[0].Edges.Articles))
	assert.Equal(t, []string{"b2"}, titles(authors[1].Edges.Articles))
	// ...for M2M, from both sides.
	articles = must(client.Article.Query(db).Where(article.Title.In("a1", "a2", "d1")).OrderBy(article.Title.Asc()).
		With(article.Topics.Load(client.Topic.Query(db).OrderBy(topic.Name.Desc()).Limit(1))).All(ctx))
	assert.Equal(t, []string{"sql"}, topicNames(articles[0].Edges.Topics))
	assert.Equal(t, []string{"go"}, topicNames(articles[1].Edges.Topics))
	assert.Equal(t, []string{"sql"}, topicNames(articles[2].Edges.Topics))
	topics := must(client.Topic.Query(db).OrderBy(topic.Name.Asc()).
		With(topic.Articles.Load(client.Article.Query(db).OrderBy(article.Title.Desc()).Limit(1))).All(ctx))
	assert.Equal(t, []string{"a2"}, titles(topics[0].Edges.Articles))
	assert.Empty(t, topics[1].Edges.Articles)
	assert.Equal(t, []string{"d1"}, titles(topics[2].Edges.Articles))
	// ...and a limit with a predicate.
	authors = must(client.Author.Query(db).OrderBy(author.Name.Asc()).
		With(author.Articles.Load(client.Article.Query(db).Where(article.Words.LT(200)).OrderBy(article.Words.Desc()).Limit(1))).All(ctx))
	assert.Equal(t, []string{"a3"}, titles(authors[0].Edges.Articles))
	assert.Equal(t, []string{"b2"}, titles(authors[1].Edges.Articles))

	// Offset and Distinct are not supported on a load, and say so.
	_, err = client.Author.Query(db).With(author.Articles.Load(client.Article.Query(db).Offset(1))).All(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Offset")
	_, err = client.Author.Query(db).With(author.Articles.Load(client.Article.Query(db).Distinct())).All(ctx)
	require.Error(t, err)
	_, err = client.Article.Query(db).With(article.Topics.Load(client.Topic.Query(db).With(topic.Articles.Load(client.Article.Query(db).Offset(1))))).All(ctx)
	require.Error(t, err, "nested")
	// ...also when the query has no row to load the edge for.
	_, err = client.Author.Query(db).Where(author.Name.EQ("nobody")).With(author.Articles.Load(client.Article.Query(db).Offset(1))).All(ctx)
	require.ErrorContains(t, err, "Offset and Distinct are not supported on an eager load")
	_, err = client.Author.Query(db).Where(author.Name.EQ("nobody")).With(author.Articles.Load(client.Article.Query(db).With(article.Topics.Load(client.Topic.Query(db).Distinct())))).All(ctx)
	require.ErrorContains(t, err, "not supported on an eager load", "nested, without rows")
	_, err = client.Author.Query(db).Where(author.Name.EQ("nobody")).With(author.Articles.Load(client.Article.Query(db).With(article.Topics.Load(client.Topic.Query(db).Offset(2))))).First(ctx)
	require.ErrorContains(t, err, "not supported on an eager load", "before the query that would have said not found")

	// The query given to WithArticles is read when the call is made and not
	// kept: it can be changed and used again without changing the load.
	q := client.Article.Query(db).OrderBy(article.Title.Asc()).Where(article.Words.GTE(150))
	aq := client.Author.Query(db).OrderBy(author.Name.Asc()).With(author.Articles.Load(q))
	q.Where(article.Title.EQ("never"))
	authors = must(aq.All(ctx))
	assert.Equal(t, []string{"a2", "a3"}, titles(authors[0].Edges.Articles), "the load is the query as it was when Load was called")
	again := must(client.Author.Query(db).OrderBy(author.Name.Desc()).With(author.Articles.Load(q)).All(ctx))
	assert.Empty(t, titles(again[3].Edges.Articles), "the changed query describes the next load it is given to")
	// Without one, every neighbor is loaded.
	authors = must(client.Author.Query(db).OrderBy(author.Name.Asc()).With(author.Articles).All(ctx))
	assert.Len(t, authors[0].Edges.Articles, 3)
}

func TestEdges_HasAndHasWith(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	l := library(t, db)
	authors := func(ps ...predicate.Author) []string {
		return authorNames(must(client.Author.Query(db).Where(ps...).OrderBy(author.Name.Asc()).All(ctx)))
	}
	arts := func(ps ...predicate.Article) []string {
		return titles(must(client.Article.Query(db).Where(ps...).OrderBy(article.Title.Asc()).All(ctx)))
	}
	tops := func(ps ...predicate.Topic) []string {
		return topicNames(must(client.Topic.Query(db).Where(ps...).OrderBy(topic.Name.Asc()).All(ctx)))
	}
	none := []string{}

	// O2M and its second instance.
	assert.Equal(t, []string{"ann", "bob", "dee"}, authors(author.Articles.Has()))
	assert.Equal(t, []string{"cid"}, authors(author.Not(author.Articles.Has())))
	assert.Equal(t, []string{"ann", "bob"}, authors(author.Edited.Has()))
	assert.Equal(t, []string{"ann", "bob"}, authors(author.Articles.HasWith(article.Words.GTE(200))))
	assert.Equal(t, []string{"bob"}, authors(author.Edited.HasWith(article.Title.EQ("a1"))))
	assert.Equal(t, []string{"ann", "bob", "dee"}, authors(author.Articles.HasWith()), "HasWith without predicates is Has")
	// Several predicates must hold for the same neighbor.
	assert.Equal(t, none, authors(author.Articles.HasWith(article.Words.EQ(100), article.Section.EQ("sport"))))
	assert.Equal(t, []string{"ann"}, authors(author.Articles.HasWith(article.Words.EQ(200), article.Section.EQ("sport"))))
	// ...while two edge predicates may be satisfied by different neighbors.
	assert.Equal(t, []string{"ann"}, authors(author.Articles.HasWith(article.Words.EQ(100)), author.Articles.HasWith(article.Section.EQ("sport"))))

	// M2O required and optional.
	assert.Equal(t, []string{"a1", "a2", "b1", "d1"}, arts(article.Author.Has()))
	assert.Equal(t, []string{"b1", "d1"}, arts(article.Author.HasWith(author.Age.GT(35))))
	assert.Equal(t, []string{"a1", "b1", "d1"}, arts(article.Editor.Has()))
	assert.Equal(t, []string{"a2"}, arts(article.Not(article.Editor.Has())))
	assert.Equal(t, []string{"d1"}, arts(article.Editor.HasWith(author.Name.EQ("ann"))))
	assert.Equal(t, []string{"a1", "a2", "b1"}, arts(article.Or(article.Author.HasWith(author.Name.EQ("ann")), article.Editor.HasWith(author.Name.EQ("bob")))))

	// O2O, both sides, hidden key and bound key.
	assert.Equal(t, []string{"ann", "bob"}, authors(author.Passport.Has()))
	assert.Equal(t, []string{"bob"}, authors(author.Passport.HasWith(passport.Number.HasSuffix("bob"))))
	assert.Equal(t, []string{"ann"}, authors(author.Locker.Has()))
	assert.Equal(t, []string{"ann"}, authors(author.Locker.HasWith(locker.Code.EQ("l1"))))
	assert.Equal(t, none, authors(author.Locker.HasWith(locker.Code.EQ("l2"))))
	assert.Equal(t, 2, must(client.Passport.Query(db).Where(passport.Owner.Has()).Count(ctx)))
	assert.Equal(t, "P-ann", must(client.Passport.Query(db).Where(passport.Owner.HasWith(author.Age.LT(35))).Only(ctx)).Number)
	assert.Equal(t, "l1", must(client.Locker.Query(db).Where(locker.Holder.Has()).Only(ctx)).Code)
	assert.Equal(t, "l2", must(client.Locker.Query(db).Where(locker.Not(locker.Holder.Has())).Only(ctx)).Code)

	// M2M, both sides, both instances.
	assert.Equal(t, []string{"a1", "a2", "d1"}, arts(article.Topics.Has()))
	assert.Equal(t, []string{"a1", "d1"}, arts(article.Topics.HasWith(topic.Name.EQ("sql"))))
	assert.Equal(t, []string{"a1", "b1"}, arts(article.BannedTopics.Has()))
	assert.Equal(t, []string{"b1"}, arts(article.BannedTopics.HasWith(topic.Name.EQ("go"))))
	assert.Equal(t, []string{"a1"}, arts(article.Topics.HasWith(topic.Name.EQ("go")), article.BannedTopics.Has()))
	assert.Equal(t, []string{"go", "sql"}, tops(topic.Articles.Has()))
	assert.Equal(t, []string{"go", "rust"}, tops(topic.BannedIn.Has()))
	assert.Equal(t, []string{"rust"}, tops(topic.Not(topic.Articles.Has())))
	assert.Equal(t, []string{"sql"}, tops(topic.Articles.HasWith(article.Title.EQ("d1"))))
	assert.Equal(t, []string{"go", "rust"}, tops(topic.BannedIn.HasWith(article.Title.EQ("b1"))))
	// O2M with an overridden key.
	assert.Equal(t, []string{"a1", "b1"}, arts(article.Notes.Has()))
	assert.Equal(t, []string{"b1"}, arts(article.Notes.HasWith(note.Text.EQ("n3"))))

	// Nested: authors with an article in topic sql; topics of articles
	// edited by someone with a passport; three levels.
	assert.Equal(t, []string{"ann", "dee"}, authors(author.Articles.HasWith(article.Topics.HasWith(topic.Name.EQ("sql")))))
	assert.Equal(t, []string{"go", "sql"}, tops(topic.Articles.HasWith(article.Editor.HasWith(author.Passport.Has()))))
	assert.Equal(t, []string{"bob"}, authors(author.Edited.HasWith(article.Author.HasWith(author.Name.EQ("ann")), article.Topics.HasWith(topic.Name.EQ("sql")))))
	assert.Equal(t, []string{"ann", "bob"}, authors(author.Articles.HasWith(article.Or(article.Topics.HasWith(topic.Name.EQ("go")), article.Notes.Has()))))
	assert.Equal(t, []string{"dee"}, authors(author.Articles.HasWith(article.Not(article.Or(article.Topics.HasWith(topic.Name.EQ("go")), article.Notes.Has())))))
	// The same entity on both ends of the path.
	assert.Equal(t, []string{"ann", "bob", "dee"}, authors(author.Articles.HasWith(article.Author.Has())))
	assert.Equal(t, []string{"ann"}, authors(author.Articles.HasWith(article.Editor.HasWith(author.Articles.HasWith(article.Title.EQ("b1")))), author.Age.LT(35)))

	// Every terminal takes them.
	assert.Equal(t, 3, must(client.Author.Query(db).Where(author.Articles.Has()).Count(ctx)))
	assert.True(t, must(client.Author.Query(db).Where(author.Articles.HasWith(article.Title.EQ("d1"))).Exists(ctx)))
	assert.False(t, must(client.Author.Query(db).Where(author.Articles.HasWith(article.Title.EQ("zz"))).Exists(ctx)))
	assert.Equal(t, []int{l.ann.ID, l.dee.ID}, must(client.Author.Query(db).Where(author.Articles.HasWith(article.Topics.HasWith(topic.Name.EQ("sql")))).OrderBy(author.ID.Asc()).IDs(ctx)))

	// They restrict updates and deletes like queries.
	n := must(client.Article.Update(db).Set(article.Section.Set("banned")).Where(article.BannedTopics.HasWith(topic.Name.EQ("rust"))).Exec(ctx))
	assert.Equal(t, 2, n)
	assert.Equal(t, []string{"a1", "b1"}, arts(article.Section.EQ("banned")))
	got := must(client.Author.Update(db).Set(author.Age.Add(1)).Where(author.Passport.HasWith(passport.Number.EQ("P-bob"))).SaveOne(ctx))
	assert.Equal(t, 41, got.Age)
	n = must(client.Note.Delete(db).Where(note.Text.NEQ("zz")).Exec(ctx))
	assert.Equal(t, 3, n)
	n = must(client.Article.Delete(db).Where(article.Not(article.Topics.Has()), article.Author.HasWith(author.Name.EQ("bob"))).Exec(ctx))
	assert.Equal(t, 1, n)
	assert.Equal(t, []string{"a1", "a2", "d1"}, arts())
}

func TestEdges_QueryNeighbours(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	l := library(t, db)

	// From the entities Create returned.
	assert.Equal(t, []string{"a1", "a2"}, titles(must(l.ann.QueryArticles(db).OrderBy(article.Title.Asc()).All(ctx))))
	assert.Equal(t, []string{"d1"}, titles(must(l.ann.QueryEdited(db).All(ctx))))
	assert.Empty(t, must(l.cid.QueryArticles(db).All(ctx)))
	assert.Equal(t, "P-ann", must(l.ann.QueryPassport(db).Only(ctx)).Number)
	_, err := l.cid.QueryPassport(db).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	assert.Equal(t, "l1", must(l.ann.QueryLocker(db).Only(ctx)).Code)
	assert.Equal(t, "ann", must(l.a1.QueryAuthor(db).Only(ctx)).Name)
	assert.Equal(t, "ann", must(l.a2.QueryAuthor(db).Only(ctx)).Name, "the key was set through the field")
	assert.Equal(t, "bob", must(l.a1.QueryEditor(db).Only(ctx)).Name)
	assert.Equal(t, "bob", must(l.b1.QueryEditor(db).Only(ctx)).Name, "the key was set through the field")
	_, err = l.a2.QueryEditor(db).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	assert.False(t, must(l.a2.QueryEditor(db).Exists(ctx)))
	// A key the schema does not name is held by the returned entity too.
	assert.Equal(t, "ann", must(l.pAnn.QueryOwner(db).Only(ctx)).Name)
	assert.Equal(t, "ann", must(l.l1.QueryHolder(db).Only(ctx)).Name)
	_, err = l.l2.QueryHolder(db).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	assert.Equal(t, []string{"go", "sql"}, topicNames(must(l.a1.QueryTopics(db).OrderBy(topic.Name.Asc()).All(ctx))))
	assert.Equal(t, []string{"rust"}, topicNames(must(l.a1.QueryBannedTopics(db).All(ctx))))
	assert.Empty(t, must(l.b1.QueryTopics(db).All(ctx)))
	assert.Equal(t, []string{"a1", "a2"}, titles(must(l.tgo.QueryArticles(db).OrderBy(article.Title.Asc()).All(ctx))))
	assert.Equal(t, []string{"a1", "b1"}, titles(must(l.rust.QueryBannedIn(db).OrderBy(article.Title.Asc()).All(ctx))))
	assert.Empty(t, must(l.rust.QueryArticles(db).All(ctx)))
	assert.Equal(t, 2, must(l.a1.QueryNotes(db).Count(ctx)))

	// From queried entities, with the target's own predicates, order, limit
	// and eager loads on top.
	ann := must(client.Author.Query(db).Where(author.Name.EQ("ann")).Only(ctx))
	got := must(ann.QueryArticles(db).Where(article.Words.GT(100)).With(article.Topics).With(article.Author).All(ctx))
	require.Len(t, got, 1)
	assert.Equal(t, "a2", got[0].Title)
	assert.Equal(t, []string{"go"}, topicNames(got[0].Edges.Topics))
	assert.Equal(t, "ann", got[0].Edges.Author.Name)
	assert.Equal(t, "a2", must(ann.QueryArticles(db).OrderBy(article.Words.Desc()).First(ctx)).Title)
	assert.Equal(t, []int{l.a1.ID, l.a2.ID}, must(ann.QueryArticles(db).OrderBy(article.ID.Asc()).IDs(ctx)))
	pp := must(client.Passport.Query(db).Where(passport.Number.EQ("P-bob")).Only(ctx))
	assert.Equal(t, "bob", must(pp.QueryOwner(db).Only(ctx)).Name)
	a1 := must(client.Article.Query(db).Where(article.Title.EQ("a1")).Only(ctx))
	assert.Equal(t, []string{"sql"}, topicNames(must(a1.QueryTopics(db).Where(topic.Name.NEQ("go")).All(ctx))))
	assert.Equal(t, 1, must(a1.QueryTopics(db).Where(topic.BannedIn.Has()).Count(ctx)), "go is banned somewhere, sql is not")
	// Chained by hand.
	ed := must(a1.QueryEditor(db).Only(ctx))
	assert.Equal(t, "P-bob", must(ed.QueryPassport(db).Only(ctx)).Number)

	// A traversal is a query like any other: it can restrict nothing but its
	// own target. An entity that was never saved has no neighbors.
	fresh := &entity.Author{}
	assert.Empty(t, must(fresh.QueryArticles(db).All(ctx)))
	_, err = fresh.QueryPassport(db).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	_, err = (&entity.Article{}).QueryAuthor(db).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	assert.Empty(t, must((&entity.Article{}).QueryTopics(db).All(ctx)))
}

func TestEdges_UpdateAddRemoveClearSet(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	l := library(t, db)
	topicsOf := func(a *entity.Article) []string {
		return topicNames(must(a.QueryTopics(db).OrderBy(topic.Name.Asc()).All(ctx)))
	}

	// M2M: add, remove, clear, and several in one update.
	must(client.Article.Update(db).Set(article.Topics.Add(l.rust.ID)).Where(article.ID.EQ(l.a1.ID)).Exec(ctx))
	assert.Equal(t, []string{"go", "rust", "sql"}, topicsOf(l.a1))
	assert.Equal(t, []string{"rust"}, topicNames(must(l.a1.QueryBannedTopics(db).All(ctx))), "the other M2M is untouched")
	must(client.Article.Update(db).Set(article.Topics.Remove(l.tgo.ID, l.sql.ID)).Where(article.ID.EQ(l.a1.ID)).Exec(ctx))
	assert.Equal(t, []string{"rust"}, topicsOf(l.a1))
	assert.Equal(t, []string{"go"}, topicsOf(l.a2), "another article's rows are untouched")
	got := must(client.Article.Update(db).Set(article.Topics.Remove(l.rust.ID), article.Topics.Add(l.tgo.ID), article.Title.Set("a1!"), article.BannedTopics.Clear()).
		Where(article.ID.EQ(l.a1.ID)).SaveOne(ctx))
	assert.Equal(t, "a1!", got.Title)
	assert.Equal(t, []string{"go"}, topicsOf(l.a1))
	assert.Empty(t, must(l.a1.QueryBannedTopics(db).All(ctx)))
	assert.Equal(t, 2, must(l.b1.QueryBannedTopics(db).Count(ctx)), "Clear clears one article's rows")
	// Removing what is not there is not an error.
	must(client.Article.Update(db).Set(article.Topics.Remove(l.rust.ID)).Where(article.ID.EQ(l.a1.ID)).Exec(ctx))
	// Clear then add in one update replaces the set.
	must(client.Article.Update(db).Set(article.Topics.Clear(), article.Topics.Add(l.sql.ID, l.rust.ID)).Where(article.ID.EQ(l.a1.ID)).Exec(ctx))
	assert.Equal(t, []string{"rust", "sql"}, topicsOf(l.a1))
	// From the inverse side.
	must(client.Topic.Update(db).Set(topic.Articles.Add(l.b1.ID), topic.Articles.Remove(l.a1.ID)).Where(topic.ID.EQ(l.rust.ID)).Exec(ctx))
	assert.Equal(t, []string{"sql"}, topicsOf(l.a1))
	assert.Equal(t, []string{"rust"}, topicsOf(l.b1))
	// One update over several rows adds the edge to each.
	n := must(client.Article.Update(db).Set(article.Topics.Add(l.tgo.ID)).Where(article.Title.In("b1", "d1")).Exec(ctx))
	assert.Equal(t, 2, n)
	assert.Equal(t, []string{"go", "rust"}, topicsOf(l.b1))
	assert.Equal(t, []string{"go", "sql"}, topicsOf(l.d1))
	// Adding an edge that exists is not an error and does not add a row.
	must(client.Article.Update(db).Set(article.Topics.Add(l.tgo.ID)).Where(article.ID.EQ(l.d1.ID)).Exec(ctx))
	assert.Equal(t, []string{"go", "sql"}, topicsOf(l.d1))
	// An edge to a row that does not exist is a constraint error, and the
	// update is not half applied.
	_, err := client.Article.Update(db).Set(article.Title.Set("never"), article.Topics.Add(9999)).Where(article.ID.EQ(l.d1.ID)).Exec(ctx)
	require.True(t, velox.IsConstraintError(err), "got %v", err)
	assert.Equal(t, "d1", must(client.Article.Query(db).Where(article.ID.EQ(l.d1.ID)).Only(ctx)).Title, "the failed update is not half applied")

	// M2O: reassign through the edge and through the field; clear the
	// optional one both ways.
	got = must(client.Article.Update(db).Set(article.Author.Set(l.cid.ID)).Where(article.ID.EQ(l.a2.ID)).SaveOne(ctx))
	assert.Equal(t, l.cid.ID, got.AuthorID)
	assert.Equal(t, "cid", must(got.QueryAuthor(db).Only(ctx)).Name)
	got = must(client.Article.Update(db).Set(article.AuthorID.Set(l.ann.ID), article.EditorID.Set(l.dee.ID)).Where(article.ID.EQ(l.a2.ID)).SaveOne(ctx))
	assert.Equal(t, l.ann.ID, got.AuthorID)
	require.NotNil(t, got.EditorID)
	assert.Equal(t, "dee", must(got.QueryEditor(db).Only(ctx)).Name)
	got = must(client.Article.Update(db).Set(article.Editor.Clear()).Where(article.ID.EQ(l.a2.ID)).SaveOne(ctx))
	assert.Nil(t, got.EditorID)
	got = must(client.Article.Update(db).Set(article.Editor.Set(l.bob.ID)).Where(article.ID.EQ(l.a2.ID)).SaveOne(ctx))
	assert.Equal(t, l.bob.ID, *got.EditorID)
	got = must(client.Article.Update(db).Set(article.EditorID.Clear()).Where(article.ID.EQ(l.a2.ID)).SaveOne(ctx))
	assert.Nil(t, got.EditorID)
	_, err = client.Article.Update(db).Set(article.Author.Set(9999)).Where(article.ID.EQ(l.a2.ID)).Exec(ctx)
	require.True(t, velox.IsConstraintError(err), "got %v", err)

	// O2M from the one side: add takes free rows, remove and clear let go.
	must(client.Author.Update(db).Set(author.Edited.Add(l.a2.ID)).Where(author.ID.EQ(l.cid.ID)).Exec(ctx))
	assert.Equal(t, []string{"a2"}, titles(must(l.cid.QueryEdited(db).All(ctx))))
	// A row that belongs to another parent is not taken over.
	_, err = client.Author.Update(db).Set(author.Age.Set(99), author.Edited.Add(l.d1.ID)).Where(author.ID.EQ(l.cid.ID)).Exec(ctx)
	require.True(t, velox.IsConstraintError(err), "got %v", err)
	assert.Equal(t, 50, must(client.Author.Query(db).Where(author.ID.EQ(l.cid.ID)).Only(ctx)).Age, "the failed update is not half applied")
	assert.Equal(t, "ann", must(l.d1.QueryEditor(db).Only(ctx)).Name)
	// Removing a row that belongs to somebody else leaves it alone.
	must(client.Author.Update(db).Set(author.Edited.Remove(l.a1.ID)).Where(author.ID.EQ(l.cid.ID)).Exec(ctx))
	assert.Equal(t, "bob", must(l.a1.QueryEditor(db).Only(ctx)).Name)
	must(client.Author.Update(db).Set(author.Edited.Remove(l.a2.ID)).Where(author.ID.EQ(l.cid.ID)).Exec(ctx))
	assert.Empty(t, must(l.cid.QueryEdited(db).All(ctx)))
	must(client.Author.Update(db).Set(author.Edited.Add(l.a2.ID)).Where(author.ID.EQ(l.cid.ID)).Exec(ctx))
	must(client.Author.Update(db).Set(author.Edited.Clear()).Where(author.ID.EQ(l.cid.ID)).Exec(ctx))
	assert.Empty(t, must(l.cid.QueryEdited(db).All(ctx)))
	assert.Equal(t, 2, must(l.bob.QueryEdited(db).Count(ctx)), "Clear clears one author's rows")
	must(client.Article.Update(db).Set(article.Notes.Remove(l.n1.ID)).Where(article.ID.EQ(l.a1.ID)).Exec(ctx))
	assert.Equal(t, "n2", must(l.a1.QueryNotes(db).Only(ctx)).Text)
	must(client.Article.Update(db).Set(article.Notes.Clear(), article.Notes.Add(l.n1.ID)).Where(article.ID.EQ(l.a1.ID)).Exec(ctx))
	assert.Equal(t, "n1", must(l.a1.QueryNotes(db).Only(ctx)).Text, "clear and add in one update replaces the set")

	// O2O: set and clear from both sides.
	must(client.Author.Update(db).Set(author.Locker.Set(l.l2.ID)).Where(author.ID.EQ(l.bob.ID)).Exec(ctx))
	assert.Equal(t, "l2", must(l.bob.QueryLocker(db).Only(ctx)).Code)
	must(client.Author.Update(db).Set(author.Locker.Clear()).Where(author.ID.EQ(l.bob.ID)).Exec(ctx))
	_, err = l.bob.QueryLocker(db).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	assert.Equal(t, "l1", must(l.ann.QueryLocker(db).Only(ctx)).Code, "ann's locker is untouched")
	lk := must(client.Locker.Update(db).Set(locker.Holder.Set(l.cid.ID)).Where(locker.ID.EQ(l.l2.ID)).SaveOne(ctx))
	require.NotNil(t, lk.HolderID)
	assert.Equal(t, l.cid.ID, *lk.HolderID)
	lk = must(client.Locker.Update(db).Set(locker.Holder.Clear()).Where(locker.ID.EQ(l.l2.ID)).SaveOne(ctx))
	assert.Nil(t, lk.HolderID)
	_, err = client.Locker.Update(db).Set(locker.Holder.Set(l.ann.ID)).Where(locker.ID.EQ(l.l2.ID)).Exec(ctx)
	require.True(t, velox.IsConstraintError(err), "ann already holds l1; got %v", err)
	must(client.Passport.Update(db).Set(passport.Owner.Set(l.dee.ID)).Where(passport.ID.EQ(l.pAnn.ID)).Exec(ctx))
	assert.Equal(t, "P-ann", must(l.dee.QueryPassport(db).Only(ctx)).Number)
	_, err = l.ann.QueryPassport(db).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
}

// Deleting a row other rows reference fails with a constraint error, inside
// and outside a transaction.
func TestEdges_DeleteRestricted(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	l := library(t, db)
	_, err := client.Author.Delete(db).Where(author.ID.EQ(l.ann.ID)).Exec(ctx)
	require.True(t, velox.IsConstraintError(err), "got %v", err)
	assert.Equal(t, 4, must(client.Author.Query(db).Count(ctx)))
	n := must(client.Author.Delete(db).Where(author.ID.EQ(l.cid.ID)).Exec(ctx))
	assert.Equal(t, 1, n)
	var target *velox.ConstraintError
	_, err = client.Author.Delete(db).Exec(ctx)
	require.True(t, errors.As(err, &target))
	assert.Equal(t, 3, must(client.Author.Query(db).Count(ctx)), "a failed delete deletes nothing")
}

// A load is said at the edge: its conditions, order, limit, columns and the
// neighbors' own edges. It is the load a query of the neighbors describes
// (the edge's Load), which is what every other test of an eager load uses:
// both are run here and must read the same rows.
func TestEdges_EagerLoadSaidAtTheEdge(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	library(t, db)

	shape := func(as []*entity.Author) []string {
		var out []string
		for _, a := range as {
			line := a.Name + ":"
			for _, x := range a.Edges.Articles {
				line += " " + x.Title + "(" + strings.Join(topicNames(x.Edges.Topics), ",") + ")"
				if ed, err := x.Edges.EditorOrErr(); err == nil {
					line += "<" + ed.Name + ">"
				}
			}
			if p, err := a.Edges.PassportOrErr(); err == nil {
				line += " passport=" + p.Number
			}
			out = append(out, line)
		}
		return out
	}
	base := func() *entity.AuthorQuery { return client.Author.Query(db).OrderBy(author.Name.Asc()) }

	atEdge := must(base().With(
		author.Articles.
			Where(article.Words.GTE(100)).
			OrderBy(article.Title.Desc()).
			Limit(2).
			With(article.Topics.OrderBy(topic.Name.Asc()), article.Editor),
		author.Passport,
	).All(ctx))
	byQuery := must(base().With(
		author.Articles.Load(client.Article.Query(nil).
			Where(article.Words.GTE(100)).
			OrderBy(article.Title.Desc()).
			Limit(2).
			With(article.Topics.Load(client.Topic.Query(nil).OrderBy(topic.Name.Asc())), article.Editor)),
		author.Passport,
	).All(ctx))
	require.Equal(t, shape(byQuery), shape(atEdge))
	require.Len(t, atEdge, 4)
	var loaded int
	for _, a := range atEdge {
		assert.LessOrEqual(t, len(a.Edges.Articles), 2, "the limit is for each author")
		loaded += len(a.Edges.Articles)
	}
	assert.Positive(t, loaded, "the comparison is of loads that read something")

	// A load that starts at Limit, and one that reads some columns: the
	// others are left at their zero value.
	firsts := must(base().With(author.Articles.Limit(1).Columns(article.ColumnTitle)).All(ctx))
	for _, a := range firsts {
		assert.LessOrEqual(t, len(a.Edges.Articles), 1)
		for _, x := range a.Edges.Articles {
			assert.NotEmpty(t, x.Title)
			assert.Zero(t, x.Words, "a column that was not asked for")
		}
	}
	// To one neighbor: the neighbor that matches, or none.
	arts := must(client.Article.Query(db).OrderBy(article.Title.Asc()).With(article.Author.Where(author.Name.EQ("ann"))).All(ctx))
	var anns, others int
	for _, x := range arts {
		if a, err := x.Edges.AuthorOrErr(); err == nil {
			assert.Equal(t, "ann", a.Name)
			anns++
		} else {
			others++
		}
	}
	assert.Positive(t, anns)
	assert.Positive(t, others, "an article of another author has no author that matches")
}
