package shapes

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/syssam/velox/tests/orm/shapes/velox/predicate"

	client "github.com/syssam/velox/tests/orm/shapes/velox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/tests/orm/shapes/velox/article"
	"github.com/syssam/velox/tests/orm/shapes/velox/author"
	"github.com/syssam/velox/tests/orm/shapes/velox/entity"
	"github.com/syssam/velox/tests/orm/shapes/velox/intkey"
	"github.com/syssam/velox/tests/orm/shapes/velox/person"
	"github.com/syssam/velox/tests/orm/shapes/velox/topic"
)

func intKeys(t *testing.T, db velox.Executor, names ...string) []*entity.IntKey {
	t.Helper()
	bs := make([]*entity.IntKeyCreate, len(names))
	for i, n := range names {
		bs[i] = client.IntKey.Create(db).Set(intkey.Name.Set(n))
	}
	return must(client.IntKey.CreateBulk(db, bs...).Save(context.Background()))
}

func keyNames(rows []*entity.IntKey) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

func TestQuery_Terminals(t *testing.T) {
	ctx := context.Background()
	db := open(t)

	// On an empty table.
	assert.Empty(t, must(client.IntKey.Query(db).All(ctx)))
	_, err := client.IntKey.Query(db).First(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	assert.True(t, velox.IsNotFound(err))
	_, err = client.IntKey.Query(db).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	assert.Zero(t, must(client.IntKey.Query(db).Count(ctx)))
	assert.False(t, must(client.IntKey.Query(db).Exists(ctx)))
	assert.Empty(t, must(client.IntKey.Query(db).IDs(ctx)))

	rows := intKeys(t, db, "e", "a", "d", "b", "c", "a")
	ids := make([]int, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}

	// Only: none, one, several.
	_, err = client.IntKey.Query(db).Where(intkey.Name.EQ("zz")).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	assert.False(t, errors.Is(err, velox.ErrNotSingular))
	assert.Equal(t, "e", must(client.IntKey.Query(db).Where(intkey.Name.EQ("e")).Only(ctx)).Name)
	_, err = client.IntKey.Query(db).Where(intkey.Name.EQ("a")).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotSingular)
	assert.False(t, velox.IsNotFound(err))
	_, err = client.IntKey.Query(db).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotSingular)
	// Only does not turn a caller's limit of one into "single".
	_, err = client.IntKey.Query(db).Where(intkey.Name.EQ("a")).Limit(1).Only(ctx)
	assert.ErrorIs(t, err, velox.ErrNotSingular, "two rows match; Limit(1) must not hide the second from Only")

	// First honors the order.
	assert.Equal(t, "e", must(client.IntKey.Query(db).OrderBy(intkey.Name.Desc()).First(ctx)).Name)
	assert.Equal(t, ids[1], must(client.IntKey.Query(db).OrderBy(intkey.Name.Asc(), intkey.ID.Asc()).First(ctx)).ID)
	assert.Equal(t, ids[5], must(client.IntKey.Query(db).OrderBy(intkey.Name.Asc(), intkey.ID.Desc()).First(ctx)).ID)
	assert.Equal(t, "d", must(client.IntKey.Query(db).OrderBy(intkey.Name.Desc()).Offset(1).First(ctx)).Name)

	// Order, limit, offset.
	assert.Equal(t, []string{"a", "a", "b", "c", "d", "e"}, keyNames(must(client.IntKey.Query(db).OrderBy(intkey.Name.Asc()).All(ctx))))
	assert.Equal(t, []string{"e", "d"}, keyNames(must(client.IntKey.Query(db).OrderBy(intkey.Name.Desc()).Limit(2).All(ctx))))
	assert.Equal(t, []string{"b", "c"}, keyNames(must(client.IntKey.Query(db).OrderBy(intkey.Name.Asc()).Limit(2).Offset(2).All(ctx))))
	assert.Empty(t, must(client.IntKey.Query(db).OrderBy(intkey.Name.Asc()).Offset(10).Limit(5).All(ctx)))
	// Offset without Limit.
	assert.Equal(t, []string{"d", "e"}, keyNames(must(client.IntKey.Query(db).OrderBy(intkey.Name.Asc()).Offset(4).All(ctx))))
	assert.Empty(t, must(client.IntKey.Query(db).OrderBy(intkey.Name.Asc()).Offset(6).All(ctx)))
	assert.Equal(t, ids[4:], must(client.IntKey.Query(db).OrderBy(intkey.ID.Asc()).Offset(4).IDs(ctx)))
	assert.True(t, must(client.IntKey.Query(db).Offset(5).Exists(ctx)))
	assert.False(t, must(client.IntKey.Query(db).Offset(6).Exists(ctx)))
	assert.Equal(t, []string{"a", "a", "b", "c", "d", "e"}, keyNames(must(client.IntKey.Query(db).OrderBy(intkey.Name.Asc()).OrderBy(intkey.ID.Desc()).All(ctx))), "OrderBy accumulates")
	assert.Equal(t, []int{ids[5], ids[1]}, must(client.IntKey.Query(db).Where(intkey.Name.EQ("a")).OrderBy(intkey.Name.Asc()).OrderBy(intkey.ID.Desc()).IDs(ctx)))

	// Count, Exist, IDs.
	assert.Equal(t, 6, must(client.IntKey.Query(db).Count(ctx)))
	assert.Equal(t, 2, must(client.IntKey.Query(db).Where(intkey.Name.EQ("a")).Count(ctx)))
	assert.True(t, must(client.IntKey.Query(db).Where(intkey.Name.EQ("a")).Exists(ctx)))
	assert.False(t, must(client.IntKey.Query(db).Where(intkey.Name.EQ("zz")).Exists(ctx)))
	assert.Equal(t, ids, must(client.IntKey.Query(db).OrderBy(intkey.ID.Asc()).IDs(ctx)))
	assert.Equal(t, ids[2:4], must(client.IntKey.Query(db).OrderBy(intkey.ID.Asc()).Offset(2).Limit(2).IDs(ctx)))

	// Clone is independent in both directions.
	base := client.IntKey.Query(db).Where(intkey.Name.NEQ("e")).OrderBy(intkey.Name.Asc())
	clone := base.Clone().Where(intkey.Name.NEQ("d")).Limit(2)
	base.Where(intkey.Name.NEQ("a"))
	assert.Equal(t, []string{"b", "c", "d"}, keyNames(must(base.All(ctx))))
	assert.Equal(t, []string{"a", "a"}, keyNames(must(clone.All(ctx))))
	// A query can be run more than once, and a terminal does not change it.
	q := client.IntKey.Query(db).Where(intkey.Name.EQ("a")).OrderBy(intkey.ID.Asc())
	_, err = q.Only(ctx)
	require.ErrorIs(t, err, velox.ErrNotSingular)
	assert.Equal(t, ids[1], must(q.First(ctx)).ID)
	assert.True(t, must(q.Exists(ctx)))
	assert.Len(t, must(q.All(ctx)), 2, "First, Only and Exist did not leave their limit behind")
	assert.Equal(t, 2, must(q.Count(ctx)))

	// A canceled context reaches the driver.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = client.IntKey.Query(db).All(cctx)
	require.Error(t, err)
}

func TestQuery_EmptyCombinators(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	intKeys(t, db, "a", "b")
	// And() without operands must not produce broken SQL: an empty
	// conjunction holds for every row.
	rows, err := client.IntKey.Query(db).Where(intkey.And()).All(ctx)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
	rows, err = client.IntKey.Query(db).Where(intkey.Name.EQ("a"), intkey.And()).All(ctx)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	// Or() without operands holds for no row: Or(filters...) over an empty
	// slice of user filters must not turn into "everything".
	var filters []predicate.IntKey
	assert.Empty(t, must(client.IntKey.Query(db).Where(intkey.Or(filters...)).All(ctx)))
	assert.Empty(t, must(client.IntKey.Query(db).Where(intkey.Name.EQ("a"), intkey.Or()).All(ctx)))
	assert.Len(t, must(client.IntKey.Query(db).Where(intkey.Or(intkey.Name.EQ("a"), intkey.Or())).All(ctx)), 1, "false is the identity of Or")
	assert.Empty(t, must(client.IntKey.Query(db).Where(intkey.And(intkey.Name.EQ("a"), intkey.Or())).All(ctx)))
	assert.Len(t, must(client.IntKey.Query(db).Where(intkey.Not(intkey.Or())).All(ctx)), 2)
	assert.Zero(t, must(client.IntKey.Update(db).Set(intkey.Name.Set("never")).Where(intkey.Or(filters...)).Exec(ctx)))
	assert.Zero(t, must(client.IntKey.Delete(db).Where(intkey.Or(filters...)).Exec(ctx)), "the delete removed nothing")
	assert.Equal(t, 2, must(client.IntKey.Query(db).Count(ctx)))
	// Not() without operands negates the empty conjunction: no row. A
	// delete built as Not(excluded...) over an empty list removes nothing.
	assert.Empty(t, must(client.IntKey.Query(db).Where(intkey.Not(filters...)).All(ctx)))
	assert.Len(t, must(client.IntKey.Query(db).Where(intkey.Not(intkey.Not())).All(ctx)), 2)
	assert.Len(t, must(client.IntKey.Query(db).Where(intkey.Not(intkey.And())).All(ctx)), 0)
	assert.Zero(t, must(client.IntKey.Delete(db).Where(intkey.Not(filters...)).Exec(ctx)), "the delete removed nothing")
	assert.Equal(t, 2, must(client.IntKey.Query(db).Count(ctx)))
}

func TestUpdate_SaveExecDeleteSemantics(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	rows := intKeys(t, db, "a", "b", "b", "c")

	// Save: exactly one row, by id or by any predicate.
	got := must(client.IntKey.Update(db).Set(intkey.Name.Set("a2")).Where(intkey.ID.EQ(rows[0].ID)).SaveOne(ctx))
	assert.Equal(t, "a2", got.Name)
	assert.Equal(t, rows[0].ID, got.ID)
	got = must(client.IntKey.Update(db).Set(intkey.Name.Set("c2")).Where(intkey.Name.EQ("c")).SaveOne(ctx))
	assert.Equal(t, rows[3].ID, got.ID)
	assert.Equal(t, "c2", got.Name, "the returned entity is the row after the update, although it no longer matches the predicate")
	got = must(client.IntKey.Update(db).Set(intkey.Name.Set("a3")).Where(intkey.ID.EQ(rows[0].ID), intkey.Name.EQ("a2")).SaveOne(ctx))
	assert.Equal(t, "a3", got.Name)
	// By id, guarded by a second predicate that does not hold: not found, and
	// nothing written.
	_, err := client.IntKey.Update(db).Set(intkey.Name.Set("never")).Where(intkey.ID.EQ(rows[0].ID), intkey.Name.EQ("a2")).SaveOne(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	_, err = client.IntKey.Update(db).Set(intkey.Name.Set("never")).Where(intkey.ID.EQ(rows[0].ID)).Where(intkey.Name.EQ("zz")).SaveOne(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	// None.
	_, err = client.IntKey.Update(db).Set(intkey.Name.Set("never")).Where(intkey.ID.EQ(9999)).SaveOne(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	_, err = client.IntKey.Update(db).Set(intkey.Name.Set("never")).Where(intkey.Name.EQ("zz")).SaveOne(ctx)
	assert.ErrorIs(t, err, velox.ErrNotFound)
	// Several: an error, and nothing written.
	_, err = client.IntKey.Update(db).Set(intkey.Name.Set("never")).Where(intkey.Name.EQ("b")).SaveOne(ctx)
	assert.ErrorIs(t, err, velox.ErrNotSingular)
	_, err = client.IntKey.Update(db).Set(intkey.Name.Set("never")).SaveOne(ctx)
	assert.ErrorIs(t, err, velox.ErrNotSingular)
	_, err = client.IntKey.Update(db).Set(intkey.Name.Set("never")).Where(intkey.ID.In(rows[1].ID, rows[2].ID)).SaveOne(ctx)
	assert.ErrorIs(t, err, velox.ErrNotSingular)
	assert.Zero(t, must(client.IntKey.Query(db).Where(intkey.Name.EQ("never")).Count(ctx)))
	// No-op by value: the row exists, so Save returns it.
	got = must(client.IntKey.Update(db).Set(intkey.Name.Set("a3")).Where(intkey.ID.EQ(rows[0].ID)).SaveOne(ctx))
	assert.Equal(t, "a3", got.Name)
	// The same builder can be saved twice.
	b := client.IntKey.Update(db).Set(intkey.Name.Set("a4")).Where(intkey.ID.EQ(rows[0].ID))
	must(b.SaveOne(ctx))
	assert.Equal(t, "a4", must(b.SaveOne(ctx)).Name)

	// Exec: the number of rows.
	assert.Equal(t, 2, must(client.IntKey.Update(db).Set(intkey.Name.Set("B")).Where(intkey.Name.EQ("b")).Exec(ctx)))
	assert.Zero(t, must(client.IntKey.Update(db).Set(intkey.Name.Set("B")).Where(intkey.Name.EQ("zz")).Exec(ctx)))
	assert.Equal(t, 4, must(client.IntKey.Update(db).Set(intkey.Name.Set("all")).Exec(ctx)), "no Where updates every row")
	// The last assignment of a column wins.
	got = must(client.IntKey.Update(db).Set(intkey.Name.Set("x"), intkey.Name.Set("y")).Where(intkey.ID.EQ(rows[0].ID)).SaveOne(ctx))
	assert.Equal(t, "y", got.Name)

	// Create.Exec, and the last setter wins on create too.
	require.NoError(t, client.IntKey.Create(db).Set(intkey.Name.Set("first"), intkey.Name.Set("last")).Exec(ctx))
	assert.Equal(t, 1, must(client.IntKey.Query(db).Where(intkey.Name.EQ("last")).Count(ctx)))
	// A builder creates a row each time it is saved.
	cb := client.IntKey.Create(db).Set(intkey.Name.Set("twice"))
	x, y := must(cb.Save(ctx)), must(cb.Save(ctx))
	assert.NotEqual(t, x.ID, y.ID)

	// Delete: the number of rows; no Where deletes every row.
	assert.Equal(t, 2, must(client.IntKey.Delete(db).Where(intkey.Name.EQ("twice")).Exec(ctx)))
	assert.Zero(t, must(client.IntKey.Delete(db).Where(intkey.Name.EQ("twice")).Exec(ctx)))
	assert.Equal(t, 1, must(client.IntKey.Delete(db).Where(intkey.ID.EQ(rows[0].ID)).Where(intkey.Name.EQ("y")).Exec(ctx)))
	assert.Equal(t, 4, must(client.IntKey.Delete(db).Exec(ctx)))
	assert.Zero(t, must(client.IntKey.Query(db).Count(ctx)))
}

func TestTx_CommitRollbackPanic(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	count := func(ex velox.Executor) int { return must(client.IntKey.Query(ex).Count(ctx)) }

	// Commit: every kind of statement inside one transaction.
	require.NoError(t, db.RunTx(ctx, func(tx *velox.Tx) error {
		use(t)
		rows := intKeys(t, tx, "a", "b", "c")
		must(client.IntKey.Create(tx).Set(intkey.Name.Set("d")).Save(ctx))
		assert.Equal(t, 4, count(tx), "the transaction sees its own writes")
		must(client.IntKey.Update(tx).Set(intkey.Name.Set("b2")).Where(intkey.ID.EQ(rows[1].ID)).SaveOne(ctx))
		_, err := client.IntKey.Update(tx).Set(intkey.Name.Set("never")).Where(intkey.ID.EQ(9999)).SaveOne(ctx)
		assert.ErrorIs(t, err, velox.ErrNotFound, "a miss inside a transaction does not end it")
		assert.Equal(t, 1, must(client.IntKey.Update(tx).Set(intkey.Name.Set("c2")).Where(intkey.Name.EQ("c")).Exec(ctx)))
		assert.Equal(t, 1, must(client.IntKey.Delete(tx).Where(intkey.Name.EQ("a")).Exec(ctx)))
		assert.True(t, must(client.IntKey.Query(tx).Where(intkey.Name.EQ("b2")).Exists(ctx)))
		return nil
	}))
	assert.Equal(t, []string{"b2", "c2", "d"}, keyNames(must(client.IntKey.Query(db).OrderBy(intkey.Name.Asc()).All(ctx))))

	// An error rolls everything back and is returned as it is.
	boom := errors.New("boom")
	err := db.RunTx(ctx, func(tx *velox.Tx) error {
		intKeys(t, tx, "x", "y")
		must(client.IntKey.Delete(tx).Where(intkey.Name.EQ("d")).Exec(ctx))
		must(client.IntKey.Update(tx).Set(intkey.Name.Set("gone")).Exec(ctx))
		return fmt.Errorf("wrapped: %w", boom)
	})
	require.ErrorIs(t, err, boom)
	assert.Equal(t, []string{"b2", "c2", "d"}, keyNames(must(client.IntKey.Query(db).OrderBy(intkey.Name.Asc()).All(ctx))))
	// An ORM error returned from fn keeps its identity.
	err = db.RunTx(ctx, func(tx *velox.Tx) error {
		intKeys(t, tx, "x")
		_, err := client.IntKey.Query(tx).Where(intkey.Name.EQ("nope")).Only(ctx)
		return err
	})
	require.ErrorIs(t, err, velox.ErrNotFound)
	assert.Equal(t, 3, count(db))

	// A panic rolls back and propagates.
	require.PanicsWithValue(t, "bang", func() {
		_ = db.RunTx(ctx, func(tx *velox.Tx) error {
			intKeys(t, tx, "x")
			panic("bang")
		})
	})
	assert.Equal(t, 3, count(db))
	// The handle works after a rolled-back and a panicked transaction.
	intKeys(t, db, "e")
	assert.Equal(t, 4, count(db))

	// Begin / Commit / Rollback by hand.
	tx := must(db.Begin(ctx))
	intKeys(t, tx, "f")
	require.NoError(t, tx.Commit())
	assert.ErrorIs(t, tx.Rollback(), velox.ErrTxDone, "a deferred Rollback after Commit is harmless")
	assert.ErrorIs(t, tx.Commit(), velox.ErrTxDone)
	assert.Equal(t, 5, count(db))
	tx = must(db.Begin(ctx))
	intKeys(t, tx, "g")
	require.NoError(t, tx.Rollback())
	assert.ErrorIs(t, tx.Rollback(), velox.ErrTxDone)
	assert.ErrorIs(t, tx.Commit(), velox.ErrTxDone)
	assert.Equal(t, 5, count(db))
	// A finished transaction runs nothing.
	_, err = client.IntKey.Query(tx).All(ctx)
	require.Error(t, err)
	_, err = client.IntKey.Create(tx).Set(intkey.Name.Set("late")).Save(ctx)
	require.Error(t, err)
	assert.Equal(t, 5, count(db))

	// fn may end the transaction itself.
	require.NoError(t, db.RunTx(ctx, func(tx *velox.Tx) error {
		intKeys(t, tx, "h")
		return tx.Commit()
	}))
	assert.Equal(t, 6, count(db))
	require.NoError(t, db.RunTx(ctx, func(tx *velox.Tx) error {
		intKeys(t, tx, "i")
		return tx.Rollback()
	}))
	assert.Equal(t, 6, count(db))

	// A canceled context cannot begin.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	require.Error(t, db.RunTx(cctx, func(*velox.Tx) error { return nil }))
}

// Everything the edge tests do, inside one transaction: the SQL layer wraps a
// multi-statement mutation in a transaction of its own, which must join the
// open one.
func TestTx_EdgesInsideATransaction(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	var l *lib
	require.NoError(t, db.RunTx(ctx, func(tx *velox.Tx) error {
		// Creates with M2M, O2M and O2O edges; updates that add and remove.
		l = library(t, tx)
		got := must(client.Article.Update(tx).Set(article.Topics.Remove(l.tgo.ID), article.Topics.Add(l.rust.ID), article.Title.Set("a1!")).Where(article.ID.EQ(l.a1.ID)).SaveOne(ctx))
		assert.Equal(t, "a1!", got.Title)
		must(client.Author.Update(tx).Set(author.Edited.Add(l.a2.ID), author.Locker.Set(l.l2.ID)).Where(author.ID.EQ(l.cid.ID)).Exec(ctx))
		must(client.Topic.Update(tx).Set(topic.BannedIn.Remove(l.b1.ID)).Where(topic.ID.EQ(l.rust.ID)).Exec(ctx))
		rows := must(client.Article.CreateBulk(tx,
			client.Article.Create(tx).Set(article.Title.Set("x1"), article.Author.Set(l.cid.ID), article.Topics.Add(l.rust.ID)),
			client.Article.Create(tx).Set(article.Title.Set("x2"), article.Author.Set(l.cid.ID), article.Topics.Add(l.tgo.ID, l.sql.ID)),
		).Save(ctx))
		require.Len(t, rows, 2)
		// Eager loads and edge predicates on the transaction's connection.
		authors := must(client.Author.Query(tx).OrderBy(author.Name.Asc()).
			WithArticles(client.Article.Query(tx).OrderBy(article.Title.Asc()).WithTopics(client.Topic.Query(tx).OrderBy(topic.Name.Asc()))).WithEdited().WithLocker().All(ctx))
		require.Len(t, authors, 4)
		assert.Equal(t, []string{"a1!", "a2"}, titles(authors[0].Edges.Articles))
		assert.Equal(t, []string{"rust", "sql"}, topicNames(authors[0].Edges.Articles[0].Edges.Topics))
		assert.Equal(t, []string{"x1", "x2"}, titles(authors[2].Edges.Articles))
		assert.Equal(t, []string{"a2"}, titles(authors[2].Edges.Edited))
		assert.Equal(t, "l2", authors[2].Edges.Locker.Code)
		assert.Equal(t, []string{"ann", "cid"}, authorNames(must(client.Author.Query(tx).Where(author.Edited.Has(), author.Articles.HasWith(article.Topics.HasWith(topic.Name.EQ("rust")))).OrderBy(author.Name.Asc()).All(ctx))))
		assert.Equal(t, []string{"go", "sql"}, topicNames(must(rows[1].QueryTopics(tx).OrderBy(topic.Name.Asc()).All(ctx))))
		assert.Equal(t, []string{"a1!"}, titles(must(l.rust.QueryBannedIn(tx).All(ctx))))
		return nil
	}))
	assert.Equal(t, 6, must(client.Article.Query(db).Count(ctx)))
	assert.Equal(t, []string{"rust", "sql"}, topicNames(must(l.a1.QueryTopics(db).OrderBy(topic.Name.Asc()).All(ctx))))
	joinRows := func() [3]int {
		return [3]int{rawValue[int](t, db, "SELECT COUNT(*) FROM article_topics"), rawValue[int](t, db, "SELECT COUNT(*) FROM article_bans"),
			rawValue[int](t, db, "SELECT COUNT(*) FROM notes WHERE article_ref IS NOT NULL")}
	}
	before := joinRows()

	// Rolled back: rows, join rows and keys on other tables alike, after an
	// error half-way through.
	boom := errors.New("boom")
	err := db.RunTx(ctx, func(tx *velox.Tx) error {
		y := must(client.Article.Create(tx).Set(article.Title.Set("y"), article.Author.Set(l.ann.ID), article.Topics.Add(l.tgo.ID, l.sql.ID, l.rust.ID), article.BannedTopics.Add(l.sql.ID)).Save(ctx))
		must(client.Article.Update(tx).Set(article.Topics.Clear(), article.Notes.Clear()).Where(article.ID.EQ(l.a1.ID)).Exec(ctx))
		must(client.Article.Update(tx).Set(article.Notes.Add(l.n1.ID)).Where(article.ID.EQ(y.ID)).Exec(ctx))
		must(client.Person.Create(tx).Set(person.Name.Set("p"), person.Friends.Add(must(client.Person.Create(tx).Set(person.Name.Set("q")).Save(ctx)).ID)).Save(ctx))
		must(client.Author.Delete(tx).Where(author.ID.EQ(l.cid.ID), author.Not(author.Articles.Has())).Exec(ctx))
		assert.Zero(t, must(l.a1.QueryTopics(tx).Count(ctx)), "the transaction sees its own edge changes")
		// A statement that fails does not end the transaction...
		_, err := client.Article.Create(tx).Set(article.Title.Set("ghost"), article.Author.Set(l.ann.ID), article.Topics.Add(9999)).Save(ctx)
		require.True(t, velox.IsConstraintError(err), "got %v", err)
		assert.Equal(t, 3, must(y.QueryTopics(tx).Count(ctx)), "...and what came before it is still there")
		return boom
	})
	require.ErrorIs(t, err, boom)
	assert.Equal(t, before, joinRows())
	assert.Equal(t, 6, must(client.Article.Query(db).Count(ctx)))
	assert.Zero(t, must(client.Article.Query(db).Where(article.Title.In("y", "ghost")).Count(ctx)))
	assert.Zero(t, must(client.Person.Query(db).Count(ctx)))
	assert.Zero(t, rawValue[int](t, db, "SELECT COUNT(*) FROM person_friends"))
	assert.Equal(t, 2, must(l.a1.QueryNotes(db).Count(ctx)))

	// A panic half-way rolls the edge changes back too.
	require.Panics(t, func() {
		_ = db.RunTx(ctx, func(tx *velox.Tx) error {
			must(client.Article.Update(tx).Set(article.Topics.Clear()).Exec(ctx))
			panic("bang")
		})
	})
	assert.Equal(t, before, joinRows())

	// (A failed multi-statement mutation inside a transaction whose error
	// the caller swallows: TestKnownGap_FailedEdgeMutationInsideTxIsHalfApplied.)
}

// Misuse of transactions: nothing may hang, and nothing may be half done.
func TestTx_Misuse(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	count := func() int { return must(client.IntKey.Query(db).Count(ctx)) }

	// Commit after a failed statement commits what succeeded.
	tx := must(db.Begin(ctx))
	intKeys(t, tx, "a")
	_, err := client.IntKey.Create(tx).Set(intkey.ID.Set(1), intkey.Name.Set("dup")).Save(ctx)
	require.True(t, velox.IsConstraintError(err), "got %v", err)
	require.NoError(t, tx.Commit())
	assert.Equal(t, 1, count())

	// The transaction handle outlives RunTx only as a dead handle.
	var leaked *velox.Tx
	require.NoError(t, db.RunTx(ctx, func(tx *velox.Tx) error { leaked = tx; return nil }))
	_, err = client.IntKey.Create(leaked).Set(intkey.Name.Set("late")).Save(ctx)
	require.Error(t, err)
	_, err = client.IntKey.CreateBulk(leaked, client.IntKey.Create(leaked).Set(intkey.Name.Set("late"))).Save(ctx)
	require.Error(t, err)
	_, err = client.IntKey.Update(leaked).Set(intkey.Name.Set("late")).Exec(ctx)
	require.Error(t, err)
	_, err = client.IntKey.Delete(leaked).Exec(ctx)
	require.Error(t, err)
	_, err = client.IntKey.Query(leaked).Count(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, leaked.Commit(), velox.ErrTxDone)
	assert.Equal(t, 1, count())

	// fn commits and then reports an error: the error comes back, the commit stands.
	boom := errors.New("boom")
	err = db.RunTx(ctx, func(tx *velox.Tx) error {
		intKeys(t, tx, "b")
		require.NoError(t, tx.Commit())
		return boom
	})
	require.ErrorIs(t, err, boom)
	assert.Equal(t, 2, count())
	// fn rolls back and returns nil: no error, nothing written.
	require.NoError(t, db.RunTx(ctx, func(tx *velox.Tx) error {
		intKeys(t, tx, "c")
		return tx.Rollback()
	}))
	assert.Equal(t, 2, count())

	// A context canceled during the transaction: the statement fails, RunTx
	// returns an error and nothing is written.
	cctx, cancel := context.WithCancel(ctx)
	err = db.RunTx(cctx, func(tx *velox.Tx) error {
		if _, err := client.IntKey.Create(tx).Set(intkey.Name.Set("d")).Save(cctx); err != nil {
			return err
		}
		cancel()
		_, err := client.IntKey.Create(tx).Set(intkey.Name.Set("e")).Save(cctx)
		return err
	})
	require.Error(t, err)
	assert.Equal(t, 2, count())

	// Many sequential transactions do not leak connections.
	for i := range 50 {
		require.NoError(t, db.RunTx(ctx, func(tx *velox.Tx) error {
			_, err := client.IntKey.Update(tx).Set(intkey.Name.Set(fmt.Sprint("n", i))).Where(intkey.Name.NEQ("zz")).Exec(ctx)
			return err
		}))
		_ = db.RunTx(ctx, func(tx *velox.Tx) error { return boom })
	}
	assert.Equal(t, 2, must(client.IntKey.Query(db).Where(intkey.Name.EQ("n49")).Count(ctx)))
}

// Queries of several entities from several goroutines on one handle: the
// per-entity runtime tables are prepared lazily and must be safe to share.
// Run under -race.
func TestQuery_ConcurrentReads(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	l := library(t, db)
	errs := make(chan error, 16)
	for i := range 16 {
		go func() {
			errs <- func() error {
				for range 20 {
					authors, err := client.Author.Query(db).OrderBy(author.Name.Asc()).
						WithArticles(client.Article.Query(db).WithTopics().WithEditor()).WithPassport().All(ctx)
					if err != nil {
						return err
					}
					if len(authors) != 4 || len(authors[0].Edges.Articles) != 2 {
						return fmt.Errorf("goroutine %d: got %d authors, %d articles of ann", i, len(authors), len(authors[0].Edges.Articles))
					}
					n, err := client.Article.Query(db).Where(article.Topics.HasWith(topic.Name.EQ("go")), article.Author.HasWith(author.ID.EQ(l.ann.ID))).Count(ctx)
					if err != nil {
						return err
					}
					if n != 2 {
						return fmt.Errorf("goroutine %d: count %d", i, n)
					}
					if _, err := l.a1.QueryTopics(db).All(ctx); err != nil {
						return err
					}
				}
				return nil
			}()
		}()
	}
	for range 16 {
		require.NoError(t, <-errs)
	}
}
