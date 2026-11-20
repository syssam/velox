package apitest

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
)

// tracking returns a hook that calls Track with names before next, on the
// given kinds of write, and keeps what Changes yields after it in *got.
func tracking(got *[]velox.Change, names []string, ops ...velox.Op) velox.Hook {
	return velox.On(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			if err := m.Track(names...); err != nil {
				return err
			}
			if err := next.Mutate(ctx, m); err != nil {
				return err
			}
			*got = slices.Collect(m.Changes())
			return nil
		})
	}, ops...)
}

// showChanges prints changes as key: name old->new(*) ..., * for a field
// that changed.
func showChanges(cs []velox.Change) string {
	var b strings.Builder
	for _, c := range cs {
		fmt.Fprintf(&b, "%v:", c.Key)
		for _, f := range c.Fields {
			mark := ""
			if f.Changed {
				mark = "*"
			}
			fmt.Fprintf(&b, " %s %v->%v%s", f.Name, f.Old, f.New, mark)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// TestTrackStatements: a tracked update or delete reads the rows it will
// change, under its conditions, locked but on SQLite, in key order; sends
// the write narrowed to their keys; and reads an update's new values with
// RETURNING, or on MySQL by key, all in one transaction.
func TestTrackStatements(t *testing.T) {
	update := func(db *velox.DB) error {
		_, err := db.Update[sPost]().Where(sPosts.AuthorID.EQ(7)).Set(sPosts.Title.Set("q")).Exec(user)
		return err
	}
	del := func(db *velox.DB) error {
		_, err := db.Delete[sPost]().Where(sPosts.AuthorID.EQ(7)).Exec(user)
		return err
	}
	cases := []struct {
		name    string
		d       velox.Dialect
		op      velox.Op
		run     func(db *velox.DB) error
		results [][][]any
		want    []string
		changes string
	}{{
		"postgres update", velox.Postgres, velox.OpUpdate, update,
		[][][]any{{{int64(1), "a"}, {int64(2), "q"}}, {{int64(2), "q"}, {int64(1), "q"}}},
		[]string{
			"BEGIN",
			`SELECT "id", "title" FROM "s_posts" WHERE "author_id" = $1 ORDER BY "id" FOR UPDATE`,
			`UPDATE "s_posts" SET "title" = $1 WHERE "author_id" = $2 AND "id" IN ($3, $4) RETURNING "id", "title"`,
			"COMMIT",
		},
		"[1]: title a->q*\n[2]: title q->q\n",
	}, {
		"sqlite update", velox.SQLite, velox.OpUpdate, update,
		[][][]any{{{int64(1), "a"}, {int64(2), "q"}}, {{int64(1), "q"}, {int64(2), "q"}}},
		[]string{
			"BEGIN",
			`SELECT "id", "title" FROM "s_posts" WHERE "author_id" = ? ORDER BY "id"`,
			`UPDATE "s_posts" SET "title" = ? WHERE "author_id" = ? AND "id" IN (?, ?) RETURNING "id", "title"`,
			"COMMIT",
		},
		"[1]: title a->q*\n[2]: title q->q\n",
	}, {
		"mysql update", velox.MySQL, velox.OpUpdate, update,
		[][][]any{{{int64(1), "a"}, {int64(2), "q"}}, {{int64(1), "q"}, {int64(2), "q"}}},
		[]string{
			"BEGIN",
			"SELECT `id`, `title` FROM `s_posts` WHERE `author_id` = ? ORDER BY `id` FOR UPDATE",
			"UPDATE `s_posts` SET `title` = ? WHERE `author_id` = ? AND `id` IN (?, ?)",
			"SELECT `id`, `title` FROM `s_posts` WHERE `id` IN (?, ?)",
			"COMMIT",
		},
		"[1]: title a->q*\n[2]: title q->q\n",
	}, {
		"postgres delete", velox.Postgres, velox.OpDelete, del,
		[][][]any{{{int64(1), int64(7), "a"}}},
		[]string{
			"BEGIN",
			`SELECT "id", "author_id", "title" FROM "s_posts" WHERE "author_id" = $1 ORDER BY "id" FOR UPDATE`,
			`DELETE FROM "s_posts" WHERE "author_id" = $1 AND "id" IN ($2)`,
			"COMMIT",
		},
		"[1]: id 1-><nil>* author_id 7-><nil>* title a-><nil>*\n",
	}, {
		"mysql delete", velox.MySQL, velox.OpDelete, del,
		[][][]any{{{int64(1), int64(7), "a"}}},
		[]string{
			"BEGIN",
			"SELECT `id`, `author_id`, `title` FROM `s_posts` WHERE `author_id` = ? ORDER BY `id` FOR UPDATE",
			"DELETE FROM `s_posts` WHERE `author_id` = ? AND `id` IN (?)",
			"COMMIT",
		},
		"[1]: id 1-><nil>* author_id 7-><nil>* title a-><nil>*\n",
	}, {
		"sqlite delete", velox.SQLite, velox.OpDelete, del,
		[][][]any{{{int64(1), int64(7), "a"}}},
		[]string{
			"BEGIN",
			`SELECT "id", "author_id", "title" FROM "s_posts" WHERE "author_id" = ? ORDER BY "id"`,
			`DELETE FROM "s_posts" WHERE "author_id" = ? AND "id" IN (?)`,
			"COMMIT",
		},
		"[1]: id 1-><nil>* author_id 7-><nil>* title a-><nil>*\n",
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &fakeconn.Exec{Results: c.results, Affected: 2}
			var got []velox.Change
			db := openFake(c.d, e, velox.WithHooks[sPost](tracking(&got, nil, c.op)))
			if err := c.run(db); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(e.Log, c.want) {
				t.Errorf("statements:\n%s\nwant:\n%s", strings.Join(e.Log, "\n"), strings.Join(c.want, "\n"))
			}
			if s := showChanges(got); s != c.changes {
				t.Errorf("changes:\n%swant:\n%s", s, c.changes)
			}
		})
	}
}

// TestTrackPolicies: the read before a tracked update has the update's
// scope, the Read and Write policies; the re-read of the new values on
// MySQL has none, as the update may move the rows out of view.
func TestTrackPolicies(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{{int64(1), false}}, {{int64(1), true}}}, Affected: 1}
	var got []velox.Change
	db := openPolicied(velox.MySQL, e, velox.WithHooks[sComment](tracking(&got, nil, velox.OpUpdate)))
	C := velox.Col[sComment, bool]{I: 2}
	if _, err := db.Update[sComment]().Where(velox.NumberCol[sComment, int64]{I: 0}.EQ(1)).Set(C.Set(true)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"BEGIN",
		"SELECT `id`, `hidden` FROM `s_comments` WHERE `id` = ? AND `hidden` = ? AND `locked` = ? ORDER BY `id` FOR UPDATE",
		"UPDATE `s_comments` SET `hidden` = ? WHERE `id` = ? AND `id` IN (?) AND `hidden` = ? AND `locked` = ?",
		"SELECT `id`, `hidden` FROM `s_comments` WHERE `id` IN (?)",
		"COMMIT",
	}
	if !slices.Equal(e.Log, want) {
		t.Errorf("statements:\n%s\nwant:\n%s", strings.Join(e.Log, "\n"), strings.Join(want, "\n"))
	}
	if s := showChanges(got); s != "[1]: hidden false->true*\n" {
		t.Errorf("changes: %s", s)
	}
}

// TestTrackNames: Track with names compares those columns; one the update
// does not assign comes back unchanged. Two Tracks add up.
func TestTrackNames(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{{int64(1), int64(7), "a"}}, {{int64(1), int64(7), "q"}}}, Affected: 1}
	var got []velox.Change
	hook := velox.Before(func(_ context.Context, m *velox.Mutation) error { return m.Track("author_id") }, velox.OpUpdate)
	db := openFake(velox.Postgres, e, velox.WithHooks[sPost](hook, tracking(&got, []string{"title"}, velox.OpUpdate)))
	if _, err := db.Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if e.Log[1] != `SELECT "id", "author_id", "title" FROM "s_posts" WHERE "id" = $1 ORDER BY "id" FOR UPDATE` {
		t.Errorf("read: %s", e.Log[1])
	}
	if s := showChanges(got); s != "[1]: author_id 7->7 title a->q*\n" {
		t.Errorf("changes: %s", s)
	}
	if ch := got[0].Changed(); len(ch) != 1 || ch[0].Name != "title" {
		t.Errorf("Changed: %+v", ch)
	}
}

// TestTrackCompositeKey: a composite key narrows the write with one AND
// group per row.
func TestTrackCompositeKey(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{{int64(1), int64(2), int64(5)}, {int64(1), int64(3), nil}}, {{int64(1), int64(2), int64(9)}, {int64(1), int64(3), int64(9)}}}, Affected: 2}
	var got []velox.Change
	db := openFake(velox.Postgres, e, velox.WithHooks[wgPair](tracking(&got, nil, velox.OpUpdate)))
	if _, err := db.Update[wgPair]().Where(wgPairs.A.EQ(1)).Set(wgPairs.Price.Set(9)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want := `UPDATE "pairs" SET "price" = $1 WHERE "a" = $2 AND (("a" = $3 AND "b" = $4) OR ("a" = $5 AND "b" = $6)) RETURNING "a", "b", "price"`
	if e.Log[2] != want {
		t.Errorf("update:\n%s\nwant:\n%s", e.Log[2], want)
	}
	if s := showChanges(got); s != "[1 2]: price 5->9*\n[1 3]: price <nil>->9*\n" {
		t.Errorf("changes: %s", s)
	}
}

// TestTrackChunks: keys past the parameter limit split the write into
// several statements, in one transaction; the MySQL re-read splits too.
func TestTrackChunks(t *testing.T) {
	var rows, back [][]any
	for i := range 5 {
		rows = append(rows, []any{int64(i + 1), "a"})
		back = append(back, []any{int64(i + 1), "q"})
	}
	e := &fakeconn.Exec{Results: [][][]any{rows, back[:3], back[3:]}, Affected: 2}
	var got []velox.Change
	db := openFake(velox.MySQL, e, engine.WithMaxParams(68), velox.WithHooks[sPost](tracking(&got, nil, velox.OpUpdate)))
	n, err := db.Update[sPost]().Where(sPosts.AuthorID.EQ(7)).Set(sPosts.Title.Set("q")).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var updates, reads int
	for _, s := range e.Log {
		switch {
		case strings.HasPrefix(s, "UPDATE"):
			updates++
		case strings.HasPrefix(s, "SELECT"):
			reads++
		}
	}
	if updates < 2 || reads < 3 || e.Log[0] != "BEGIN" || e.Log[len(e.Log)-1] != "COMMIT" {
		t.Errorf("statements: %q", e.Log)
	}
	if n != int64(2*updates) || len(got) != 5 {
		t.Errorf("affected %d, %d changes; want %d, 5", n, len(got), 2*updates)
	}
}

// TestTrackInCallerTx: inside db.Tx a tracked write opens no transaction or
// savepoint of its own.
func TestTrackInCallerTx(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{{int64(1), "a"}}, {{int64(1), "q"}}}, Affected: 1}
	var got []velox.Change
	db := openFake(velox.Postgres, e, velox.WithHooks[sPost](tracking(&got, nil, velox.OpUpdate)))
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).Exec(ctx)
		return err
	})
	if err != nil || len(e.Log) != 4 || e.Log[0] != "BEGIN" || e.Log[3] != "COMMIT" || len(got) != 1 {
		t.Errorf("%v; statements %q; changes %v", err, e.Log, got)
	}
}

// TestTrackNoRow: when no row matches, a tracked write sends nothing after
// the read, affects 0 rows and yields no Change.
func TestTrackNoRow(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{}}, Affected: 1}
	got := []velox.Change{{}}
	db := openFake(velox.Postgres, e, velox.WithHooks[sPost](tracking(&got, nil, velox.OpUpdate, velox.OpDelete)))
	n, err := db.Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).Exec(ctx)
	if err != nil || n != 0 || len(got) != 0 || len(e.Log) != 3 {
		t.Errorf("%d, %v; statements %q; changes %v", n, err, e.Log, got)
	}
}

// TestTrackDryRun: under SQL(ctx) Track reads nothing and the statement
// renders as it would untracked; Changes yields none.
func TestTrackDryRun(t *testing.T) {
	e := &fakeconn.Exec{}
	got := []velox.Change{{}}
	db := openFake(velox.Postgres, e, velox.WithHooks[sPost](tracking(&got, nil, velox.OpUpdate, velox.OpDelete)))
	q, _, err := db.Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).SQL(ctx)
	if err != nil || q != `UPDATE "s_posts" SET "title" = $1 WHERE "id" = $2` || len(e.Log) != 0 || len(got) != 0 {
		t.Errorf("%q, %v; sent %q; changes %v", q, err, e.Log, got)
	}
	q, _, err = db.Delete[sPost]().Where(sPosts.ID.EQ(1)).SQL(ctx)
	if err != nil || q != `DELETE FROM "s_posts" WHERE "id" = $1` || len(e.Log) != 0 {
		t.Errorf("%q, %v; sent %q", q, err, e.Log)
	}
}

// TestTrackMisuse: Track on an insert, after next, or with a name the
// entity has no column of fails the write; on an insert and with a wrong
// name before anything is sent.
func TestTrackMisuse(t *testing.T) {
	late := velox.On(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			err := next.Mutate(ctx, m)
			_ = m.Track()
			return err
		})
	}, velox.OpUpdate)
	update := func(db *velox.DB) error {
		_, err := db.Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).Exec(ctx)
		return err
	}
	cases := []struct {
		name string
		hook velox.Hook
		run  func(db *velox.DB) error
		want string
		sent bool
	}{
		{"insert", velox.Before(func(_ context.Context, m *velox.Mutation) error { _ = m.Track(); return nil }, velox.OpInsert),
			func(db *velox.DB) error { return db.Insert(&sPost{ID: 1, Title: "a"}).Exec(ctx) }, "Track is for updates and deletes", false},
		{"name", velox.Before(func(_ context.Context, m *velox.Mutation) error { _ = m.Track("nope"); return nil }, velox.OpUpdate),
			update, `Track("nope"): no column "nope"`, false},
		{"after next", late, update, "a Track after next comes too late", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &fakeconn.Exec{Affected: 1}
			err := c.run(openFake(velox.Postgres, e, velox.WithHooks[sPost](c.hook)))
			if err == nil || !strings.Contains(err.Error(), c.want) || (len(e.Log) > 0) != c.sent {
				t.Errorf("%v; sent %q", err, e.Log)
			}
		})
	}
}

// TestTrackSkippedNext: a write an inner hook skips is not read, and
// Changes yields none.
func TestTrackSkippedNext(t *testing.T) {
	e := &fakeconn.Exec{}
	got := []velox.Change{{}}
	skip := func(velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(context.Context, *velox.Mutation) error { return nil })
	}
	db := openFake(velox.Postgres, e, velox.WithHooks[sPost](tracking(&got, nil, velox.OpUpdate), skip))
	if _, err := db.Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).Exec(ctx); err != nil || len(e.Log) != 0 || len(got) != 0 {
		t.Errorf("%v; sent %q; changes %v", err, e.Log, got)
	}
}

// TestTrackRewriteReturning: a Rewrite hook may not change the RETURNING of
// a tracked update, which velox reads by position.
func TestTrackRewriteReturning(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{{int64(1), "a"}}}, Affected: 1}
	var got []velox.Change
	db := openFake(velox.Postgres, e, velox.WithHooks[sPost](tracking(&got, nil, velox.OpUpdate)),
		velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			if st.Op == velox.OpUpdate {
				st.Returning = st.Returning[:1]
			}
			return nil
		}))
	_, err := db.Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "changed the statement's Returning") || e.Log[len(e.Log)-1] != "ROLLBACK" {
		t.Errorf("%v; sent %q", err, e.Log)
	}
}

// TestTrackUpdateOne: UpdateQuery.One runs the hooks, Track included, inside
// its transaction.
func TestTrackUpdateOne(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{{int64(1), "a"}}, {{int64(1), "q"}}, {{int64(1), int64(7), "q"}}}, Affected: 1}
	var got []velox.Change
	db := openFake(velox.Postgres, e, velox.WithHooks[sPost](tracking(&got, nil, velox.OpUpdate)))
	p, err := db.Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).One(ctx)
	if err != nil || p.Title != "q" {
		t.Fatalf("One: %+v, %v", p, err)
	}
	want := []string{
		"BEGIN",
		`SELECT "id", "title" FROM "s_posts" WHERE "id" = $1 ORDER BY "id" FOR UPDATE`,
		`UPDATE "s_posts" SET "title" = $1 WHERE "id" = $2 AND "id" IN ($3) RETURNING "id", "title"`,
		`SELECT "id", "author_id", "title" FROM "s_posts" WHERE "id" = $1 LIMIT $2`,
		"COMMIT",
	}
	if !slices.Equal(e.Log, want) || showChanges(got) != "[1]: title a->q*\n" {
		t.Errorf("statements:\n%s\nchanges: %s", strings.Join(e.Log, "\n"), showChanges(got))
	}
}

// TestInsertChanges: an insert's Changes, with no Track, yield each row as
// written, every column with no old value.
func TestInsertChanges(t *testing.T) {
	e := &fakeconn.Exec{Affected: 2}
	var got []velox.Change
	hook := velox.On(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			err := next.Mutate(ctx, m)
			got = slices.Collect(m.Changes())
			return err
		})
	}, velox.OpInsert)
	db := openFake(velox.Postgres, e, velox.WithHooks[wgPair](hook))
	if err := db.InsertAll([]wgPair{{A: 1, B: 2}, {A: 1, B: 3}}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if s := showChanges(got); s != "[1 2]: a <nil>->1* b <nil>->2* price <nil>->250*\n[1 3]: a <nil>->1* b <nil>->3* price <nil>->250*\n" {
		t.Errorf("changes: %s", s)
	}
}
