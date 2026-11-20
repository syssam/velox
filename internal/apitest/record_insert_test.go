package apitest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// dDefStatus and dDefNote are dDef's handles for status and note, which dDefs
// leaves out.
var (
	dDefStatus = velox.StringCol[dDef, string]{I: 2}
	dDefNote   = velox.NullStringCol[dDef, string]{I: 4}
)

// ticketColsMy is ticketCols quoted for MySQL.
var ticketColsMy = strings.ReplaceAll(ticketCols, `"`, "`")

// recTicket is a row of tickets, every column, as the locked read and the
// read-back return it.
func recTicket(id int64, owner, title string, done bool) []any {
	return []any{id, "seed", int64(7), owner, title, done, int64(0), nil}
}

// TestRecordInsert: Insert and InsertAll on a recording handle report one
// Change per row, in the order given, whose new row holds what the database
// filled; they send exactly the statements a handle that does not record
// sends. The Change holds a copy: the caller's later changes to its entity
// do not reach it.
func TestRecordInsert(t *testing.T) {
	for _, c := range []struct {
		d       velox.Dialect
		one     func() *fakeconn.Exec // Insert's driver
		many    func() *fakeconn.Exec // InsertAll's
		oneLog  []string
		manyLog []string
	}{{
		d:      velox.Postgres,
		one:    func() *fakeconn.Exec { return &fakeconn.Exec{Rows: [][]any{{int64(1), "new", int64(7), "n"}}} },
		oneLog: []string{`INSERT INTO "ddefs" ("name") VALUES ($1) RETURNING "id", "status", "score", "note"`},
		many: func() *fakeconn.Exec {
			return &fakeconn.Exec{Results: [][][]any{
				{{int64(1), "new", int64(7), "n"}},
				{{int64(2), "new", "n"}, {int64(3), "new", "n"}},
				{{int64(4), "new", int64(7), "n"}},
			}}
		},
		manyLog: []string{
			"BEGIN",
			`INSERT INTO "ddefs" ("name") VALUES ($1) RETURNING "id", "status", "score", "note"`,
			`INSERT INTO "ddefs" ("name", "score") VALUES ($1, $2), ($3, $4) RETURNING "id", "status", "note"`,
			`INSERT INTO "ddefs" ("name") VALUES ($1) RETURNING "id", "status", "score", "note"`,
			"COMMIT",
		},
	}, {
		d:      velox.SQLite,
		one:    func() *fakeconn.Exec { return &fakeconn.Exec{Rows: [][]any{{int64(1), "new", int64(7), "n"}}} },
		oneLog: []string{`INSERT INTO "ddefs" ("name") VALUES (?) RETURNING "id", "status", "score", "note"`},
		many: func() *fakeconn.Exec {
			return &fakeconn.Exec{Results: [][][]any{
				{{int64(1), "new", int64(7), "n"}},
				{{int64(2), "new", "n"}, {int64(3), "new", "n"}},
				{{int64(4), "new", int64(7), "n"}},
			}}
		},
		manyLog: []string{
			"BEGIN",
			`INSERT INTO "ddefs" ("name") VALUES (?) RETURNING "id", "status", "score", "note"`,
			`INSERT INTO "ddefs" ("name", "score") VALUES (?, ?), (?, ?) RETURNING "id", "status", "note"`,
			`INSERT INTO "ddefs" ("name") VALUES (?) RETURNING "id", "status", "score", "note"`,
			"COMMIT",
		},
	}, {
		d: velox.MySQL,
		one: func() *fakeconn.Exec {
			return &fakeconn.Exec{Affected: 1, LastID: 1, HasLastID: true, Results: [][][]any{{{"new", int64(7), "n"}}}}
		},
		oneLog: []string{
			"INSERT INTO `ddefs` (`name`) VALUES (?)",
			"SELECT `status`, `score`, `note` FROM `ddefs` WHERE `id` = ? LIMIT ?",
		},
		many: func() *fakeconn.Exec {
			return &fakeconn.Exec{AffectedBy: func(sql string) int64 { return int64(strings.Count(sql, "(?")) }, LastID: 1, HasLastID: true,
				Results: [][][]any{
					{{"new", int64(7), "n"}},
					{{int64(1), "new", "n"}, {int64(2), "new", "n"}},
					{{"new", int64(7), "n"}},
				}}
		},
		manyLog: []string{
			"BEGIN",
			"INSERT INTO `ddefs` (`name`) VALUES (?)",
			"SELECT `status`, `score`, `note` FROM `ddefs` WHERE `id` = ? LIMIT ?",
			"INSERT INTO `ddefs` (`name`, `score`) VALUES (?, ?), (?, ?)",
			"SELECT `id`, `status`, `note` FROM `ddefs` WHERE `id` IN (?, ?)",
			"INSERT INTO `ddefs` (`name`) VALUES (?)",
			"SELECT `status`, `score`, `note` FROM `ddefs` WHERE `id` = ? LIMIT ?",
			"COMMIT",
		},
	}} {
		t.Run(c.d.String(), func(t *testing.T) {
			// Today's statements: a handle that does not record.
			plain, plainMany := c.one(), c.many()
			if err := openFake(c.d, plain).Insert(&dDef{Name: "a"}).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			wantLog(t, plain, c.oneLog)
			if err := openFake(c.d, plainMany).InsertAll(recDefs()).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			wantLog(t, plainMany, c.manyLog)

			var rec velox.Recorder
			a := dDef{Name: "a"}
			one, many := c.one(), c.many()
			if err := openFake(c.d, one).Record(&rec).Insert(&a).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			wantLog(t, one, c.oneLog)
			cs := slices.Collect(rec.All())
			if len(cs) != 1 {
				t.Fatalf("Insert: %d Changes, want 1", len(cs))
			}
			ch := cs[0]
			a.Status, a.Name, *a.Note = "changed later", "changed later", "changed later"
			if ch.Op != velox.OpInsert || ch.Key != int64(1) || ch.OldRow() != nil || ch.Diff() != nil {
				t.Errorf("Insert: %+v", ch)
			}
			if dDefStatus.New(ch) != "new" || dDefs.Score.New(ch) != 7 || dDefNote.New(ch) != "n" || dDefs.Name.New(ch) != "a" || dDefStatus.Old(ch) != "" {
				t.Errorf("Insert: status %q, score %d, note %q, name %q", dDefStatus.New(ch), dDefs.Score.New(ch), dDefNote.New(ch), dDefs.Name.New(ch))
			}
			if _, cur, ok := velox.RowsOf[dDef](ch); !ok || cur == &a || cur.Status != "new" || *cur.Note != "n" {
				t.Errorf("Insert: RowsOf %+v, %v; want a copy", cur, ok)
			}

			rec.Reset()
			xs := recDefs()
			if err := openFake(c.d, many).Record(&rec).InsertAll(xs).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			wantLog(t, many, c.manyLog)
			cs = slices.Collect(rec.All())
			if len(cs) != 4 {
				t.Fatalf("InsertAll: %d Changes, want 4", len(cs))
			}
			for i, ch := range cs {
				if ch.Op != velox.OpInsert || ch.Key != xs[i].ID || dDefs.Name.New(ch) != xs[i].Name || dDefStatus.New(ch) != "new" {
					t.Errorf("InsertAll change %d: %+v, key %v, name %q, status %q", i, ch, ch.Key, dDefs.Name.New(ch), dDefStatus.New(ch))
				}
			}
		})
	}
}

// recDefs are four rows that InsertAll splits into three statements
// (ADR 0093): the middle two set score, the others leave it to the database.
func recDefs() []dDef {
	return []dDef{{Name: "c"}, {Name: "d", Score: 5}, {Name: "e", Score: 6}, {Name: "f"}}
}

// TestRecordInsertInTx: inside a transaction a recorded insert and its sink
// run under a savepoint; the sink runs before it is released.
func TestRecordInsertInTx(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1), "new", int64(7), "n"}}}
	var at []int
	sink := velox.RecordFunc(func(context.Context, velox.Change) error {
		at = append(at, len(e.Log))
		return nil
	})
	err := openFake(velox.Postgres, e).Record(sink).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return tx.Insert(&dDef{Name: "a"}).Exec(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		"SAVEPOINT velox_1",
		`INSERT INTO "ddefs" ("name") VALUES ($1) RETURNING "id", "status", "score", "note"`,
		"RELEASE SAVEPOINT velox_1",
		"COMMIT",
	})
	if !slices.Equal(at, []int{3}) {
		t.Errorf("the sink ran after %v statements, want once after the INSERT (3)", at)
	}
}

// TestRecordInsertOutsideTx: a recorded single-statement insert outside a
// transaction sends no BEGIN; the sink runs after the INSERT, which has
// committed, and its error undoes nothing.
func TestRecordInsertOutsideTx(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1), "new", int64(7), "n"}}}
	errBoom := errors.New("boom")
	var at []int
	sink := velox.RecordFunc(func(context.Context, velox.Change) error {
		at = append(at, len(e.Log))
		return errBoom
	})
	err := openFake(velox.Postgres, e).Record(sink).Insert(&dDef{Name: "a"}).Exec(ctx)
	if err != errBoom {
		t.Fatalf("got %v, want the sink's error", err)
	}
	wantLog(t, e, []string{`INSERT INTO "ddefs" ("name") VALUES ($1) RETURNING "id", "status", "score", "note"`})
	if !slices.Equal(at, []int{1}) {
		t.Errorf("the sink ran after %v statements, want once after the INSERT", at)
	}
}

// TestRecordUpsert: an upsert of one row that conflicts and one that does
// not reads the conflicting rows first, locked by the conflict columns, and
// after the INSERT reads the rows as stored: the first is an update with
// its real old values, and its new row is the stored one (owner, which the
// update branch leaves alone, keeps the stored "bob"); the second is an
// insert.
func TestRecordUpsert(t *testing.T) {
	ins := `INSERT INTO "tickets" (` + ticketInsertCols + `) VALUES `
	for _, c := range []struct {
		d    velox.Dialect
		want []string
	}{{velox.Postgres, []string{
		"BEGIN",
		`SELECT ` + ticketCols + ` FROM "tickets" WHERE ("org_id", "title") IN (($1, $2), ($3, $4)) FOR UPDATE`,
		ins + `($1, $2, $3, $4, $5, $6), ($7, $8, $9, $10, $11, $12) ON CONFLICT ("org_id", "title") DO UPDATE SET "done" = EXCLUDED."done" RETURNING "id"`,
		`SELECT ` + ticketCols + ` FROM "tickets" WHERE ("org_id", "title") IN (($1, $2), ($3, $4))`,
		"COMMIT",
	}}, {velox.SQLite, []string{
		"BEGIN",
		`SELECT ` + ticketCols + ` FROM "tickets" WHERE (("org_id" = ? AND "title" = ?) OR ("org_id" = ? AND "title" = ?))`,
		ins + `(?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?) ON CONFLICT ("org_id", "title") DO UPDATE SET "done" = EXCLUDED."done" RETURNING "id"`,
		`SELECT ` + ticketCols + ` FROM "tickets" WHERE (("org_id" = ? AND "title" = ?) OR ("org_id" = ? AND "title" = ?))`,
		"COMMIT",
	}}, {velox.MySQL, []string{
		"BEGIN",
		"SELECT " + ticketColsMy + " FROM `tickets` WHERE ((`org_id` = ? AND `title` = ?) OR (`org_id` = ? AND `title` = ?)) FOR UPDATE",
		"INSERT INTO `tickets` (" + strings.ReplaceAll(ticketInsertCols, `"`, "`") + ") VALUES (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?) AS new ON DUPLICATE KEY UPDATE `done` = new.`done`",
		"SELECT " + ticketColsMy + " FROM `tickets` WHERE ((`org_id` = ? AND `title` = ?) OR (`org_id` = ? AND `title` = ?))",
		"COMMIT",
	}}} {
		t.Run(c.d.String(), func(t *testing.T) {
			e := &fakeconn.Exec{Affected: 3, Results: [][][]any{
				{recTicket(1, "bob", "a", false)},
				{{int64(1)}, {int64(2)}},
				{recTicket(2, "ann", "b", true), recTicket(1, "bob", "a", true)},
			}}
			if c.d == velox.MySQL {
				e.Results = slices.Delete(e.Results, 1, 2) // no RETURNING
			}
			var rec velox.Recorder
			rdb := openFake(c.d, e).Record(&rec)
			xs := []testmodel.Ticket{{OrgID: 7, Owner: "ann", Title: "a", Done: true}, {OrgID: 7, Owner: "ann", Title: "b", Done: true}}
			if err := rdb.InsertAll(xs).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)); err != nil {
				t.Fatal(err)
			}
			wantLog(t, e, c.want)
			cs := slices.Collect(rec.All())
			if len(cs) != 2 {
				t.Fatalf("%d Changes, want 2: %+v", len(cs), cs)
			}
			up, in := cs[0], cs[1]
			if up.Op != velox.OpUpdate || up.Key != int64(1) || Tk.Done.Old(up) || !Tk.Done.New(up) || Tk.Owner.New(up) != "bob" || !up.Changed(Tk.Done) || up.Changed(Tk.Owner) {
				t.Errorf("update: %+v, done %v -> %v, owner %q", up, Tk.Done.Old(up), Tk.Done.New(up), Tk.Owner.New(up))
			}
			if in.Op != velox.OpInsert || in.Key != int64(2) || in.OldRow() != nil || Tk.Title.New(in) != "b" {
				t.Errorf("insert: %+v", in)
			}
		})
	}
}

// TestRecordUpsertDoNothing: a row DoNothing kept is not reported, and the
// new row is; a single Insert that DoNothing keeps reports nothing.
func TestRecordUpsertDoNothing(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1, Results: [][][]any{
		{recTicket(1, "bob", "a", false)},
		{recTicket(1, "bob", "a", false), recTicket(2, "ann", "b", true)},
	}}
	var rec velox.Recorder
	rdb := openFake(velox.Postgres, e).Record(&rec)
	xs := []testmodel.Ticket{{OrgID: 7, Owner: "ann", Title: "a", Done: true}, {OrgID: 7, Owner: "ann", Title: "b", Done: true}}
	if err := rdb.InsertAll(xs).OnConflict(Tk.OrgID, Tk.Title).DoNothing().Exec(as("ann", 7)); err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		`SELECT ` + ticketCols + ` FROM "tickets" WHERE ("org_id", "title") IN (($1, $2), ($3, $4)) FOR UPDATE`,
		`INSERT INTO "tickets" (` + ticketInsertCols + `) VALUES ($1, $2, $3, $4, $5, $6), ($7, $8, $9, $10, $11, $12) ON CONFLICT ("org_id", "title") DO NOTHING`,
		`SELECT ` + ticketCols + ` FROM "tickets" WHERE ("org_id", "title") IN (($1, $2), ($3, $4))`,
		"COMMIT",
	})
	cs := slices.Collect(rec.All())
	if len(cs) != 1 || cs[0].Op != velox.OpInsert || cs[0].Key != int64(2) || Tk.Title.New(cs[0]) != "b" {
		t.Fatalf("Changes %+v, want the insert of b", cs)
	}

	e = &fakeconn.Exec{Affected: 0, Results: [][][]any{
		{recTicket(1, "bob", "a", false)},
		{},
		{recTicket(1, "bob", "a", false)},
	}}
	rec.Reset()
	tk := testmodel.Ticket{OrgID: 7, Owner: "ann", Title: "a", Done: true}
	if err := openFake(velox.SQLite, e).Record(&rec).Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).DoNothing().Exec(as("ann", 7)); err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		`SELECT ` + ticketCols + ` FROM "tickets" WHERE ("org_id" = ? AND "title" = ?)`,
		`INSERT INTO "tickets" (` + ticketInsertCols + `) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT ("org_id", "title") DO NOTHING RETURNING "id"`,
		`SELECT ` + ticketCols + ` FROM "tickets" WHERE ("org_id" = ? AND "title" = ?)`,
		"COMMIT",
	})
	if rec.Len() != 0 {
		t.Errorf("a kept row was reported: %+v", slices.Collect(rec.All()))
	}
}

// TestRecordUpsertUnchanged: an upsert whose update branch writes the
// values the row already holds reports nothing, as an update that changes
// nothing.
func TestRecordUpsertUnchanged(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{recTicket(1, "bob", "a", true)},
		{{int64(1)}},
		{recTicket(1, "bob", "a", true)},
	}}
	var rec velox.Recorder
	tk := testmodel.Ticket{OrgID: 7, Owner: "ann", Title: "a", Done: true}
	if err := openFake(velox.Postgres, e).Record(&rec).Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)); err != nil {
		t.Fatal(err)
	}
	if rec.Len() != 0 {
		t.Errorf("an unchanged row was reported: %+v", slices.Collect(rec.All()))
	}
}

// TestRecordUpsertOneColumnInTx: a conflict target of one column reads by
// col IN (…); inside a transaction the read, the INSERT, the read-back and
// the sink run under a savepoint. The new row is the stored one: status,
// which the update branch leaves alone, keeps "done".
func TestRecordUpsertOneColumnInTx(t *testing.T) {
	const cols = `"id", "name", "status", "score", "note", "tag"`
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(1), "a", "done", int64(3), "x", nil}},
		{{"done", int64(3), "x"}},
		{{int64(1), "b", "done", int64(3), "x", nil}},
	}}
	var rec velox.Recorder
	err := openFake(velox.Postgres, e).Record(&rec).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return tx.Insert(&dDef{ID: 1, Name: "b"}).OnConflict(dDefs.ID).Update(dDefs.Name).Exec(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		"SAVEPOINT velox_1",
		`SELECT ` + cols + ` FROM "ddefs" WHERE "id" IN ($1) FOR UPDATE`,
		`INSERT INTO "ddefs" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name" RETURNING "status", "score", "note"`,
		`SELECT ` + cols + ` FROM "ddefs" WHERE "id" IN ($1)`,
		"RELEASE SAVEPOINT velox_1",
		"COMMIT",
	})
	cs := slices.Collect(rec.All())
	if len(cs) != 1 || cs[0].Op != velox.OpUpdate || cs[0].Key != int64(1) {
		t.Fatalf("Changes %+v", cs)
	}
	want := []velox.FieldChange{{Name: "name", Old: "a", New: "b"}}
	if got := cs[0].Diff(); !slices.Equal(got, want) || dDefStatus.New(cs[0]) != "done" {
		t.Errorf("Diff %+v, status %q", got, dDefStatus.New(cs[0]))
	}
}
