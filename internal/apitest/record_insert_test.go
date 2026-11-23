package apitest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
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
// Change per row, in the order given, whose new row is the row as stored;
// they send the statements a handle that does not record sends, but read
// back every column: RETURNING every column on PostgreSQL and SQLite, and
// on MySQL one read of every column by key in place of the read of the
// filled columns. The Change holds its own row: the caller's later changes
// to its entity do not reach it.
func TestRecordInsert(t *testing.T) {
	const all = `"id", "name", "status", "score", "note", "tag"`
	allMy := strings.ReplaceAll(all, `"`, "`")
	row := func(id int64, name string, score int64) []any { return []any{id, name, "new", score, "n", nil} }
	for _, c := range []struct {
		d        velox.Dialect
		one      func() *fakeconn.Exec // Insert's driver
		many     func() *fakeconn.Exec // InsertAll's
		oneLog   []string
		manyLog  []string
		rone     func() *fakeconn.Exec // a recorded Insert's driver
		rmany    func() *fakeconn.Exec // a recorded InsertAll's
		roneLog  []string
		rmanyLog []string
	}{{
		d:      velox.Postgres,
		one:    func() *fakeconn.Exec { return &fakeconn.Exec{Rows: [][]any{{int64(1), "new", int64(7), "n"}}} },
		oneLog: []string{`INSERT INTO "ddefs" ("name") VALUES ($1) RETURNING "id", "status", "score", "note"`},
		many: func() *fakeconn.Exec {
			return &fakeconn.Exec{Rows: [][]any{
				{int64(1), "new", int64(7), "n"}, {int64(2), "new", int64(5), "n"},
				{int64(3), "new", int64(6), "n"}, {int64(4), "new", int64(7), "n"},
			}}
		},
		manyLog: []string{
			`INSERT INTO "ddefs" ("name", "score") VALUES ($1, DEFAULT), ($2, $3), ($4, $5), ($6, DEFAULT) RETURNING "id", "status", "score", "note"`,
		},
		rone:    func() *fakeconn.Exec { return &fakeconn.Exec{Rows: [][]any{row(1, "a", 7)}} },
		roneLog: []string{`INSERT INTO "ddefs" ("name") VALUES ($1) RETURNING ` + all},
		rmany: func() *fakeconn.Exec {
			return &fakeconn.Exec{Rows: [][]any{row(1, "c", 7), row(2, "d", 5), row(3, "e", 6), row(4, "f", 7)}}
		},
		rmanyLog: []string{
			`INSERT INTO "ddefs" ("name", "score") VALUES ($1, DEFAULT), ($2, $3), ($4, $5), ($6, DEFAULT) RETURNING ` + all,
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
		rone:    func() *fakeconn.Exec { return &fakeconn.Exec{Rows: [][]any{row(1, "a", 7)}} },
		roneLog: []string{`INSERT INTO "ddefs" ("name") VALUES (?) RETURNING ` + all},
		rmany: func() *fakeconn.Exec {
			return &fakeconn.Exec{Results: [][][]any{{row(1, "c", 7)}, {row(2, "d", 5), row(3, "e", 6)}, {row(4, "f", 7)}}}
		},
		rmanyLog: []string{
			"BEGIN",
			`INSERT INTO "ddefs" ("name") VALUES (?) RETURNING ` + all,
			`INSERT INTO "ddefs" ("name", "score") VALUES (?, ?), (?, ?) RETURNING ` + all,
			`INSERT INTO "ddefs" ("name") VALUES (?) RETURNING ` + all,
			"COMMIT",
		},
	}, {
		d: velox.MySQL,
		one: func() *fakeconn.Exec {
			return &fakeconn.Exec{Affected: 1, LastID: 1, HasLastID: true, Results: [][][]any{{{"new", int64(7), "n"}}}}
		},
		oneLog: []string{
			"INSERT INTO `ddefs` (`name`) VALUES (?)",
			"SELECT `status`, `score`, `note` FROM `ddefs` WHERE `id` = ? LIMIT 2",
		},
		many: func() *fakeconn.Exec {
			return &fakeconn.Exec{AffectedBy: func(sql string) int64 { return int64(strings.Count(sql, "(?")) }, LastID: 1, HasLastID: true,
				Results: [][][]any{{
					{int64(1), "new", int64(7), "n"}, {int64(2), "new", int64(5), "n"},
					{int64(3), "new", int64(6), "n"}, {int64(4), "new", int64(7), "n"},
				}}}
		},
		manyLog: []string{
			"SELECT @@auto_increment_increment",
			"INSERT INTO `ddefs` (`name`, `score`) VALUES (?, DEFAULT), (?, ?), (?, ?), (?, DEFAULT)",
			"SELECT `id`, `status`, `score`, `note` FROM `ddefs` WHERE `id` IN (?, ?, ?, ?)",
		},
		rone: func() *fakeconn.Exec {
			return &fakeconn.Exec{Affected: 1, LastID: 1, HasLastID: true, Rows: [][]any{row(1, "a", 7)}}
		},
		roneLog: []string{
			"INSERT INTO `ddefs` (`name`) VALUES (?)",
			"SELECT " + allMy + " FROM `ddefs` WHERE `id` IN (?)",
		},
		rmany: func() *fakeconn.Exec {
			return &fakeconn.Exec{AffectedBy: func(sql string) int64 { return int64(strings.Count(sql, "(?")) }, LastID: 1, HasLastID: true,
				Rows: [][]any{row(1, "c", 7), row(2, "d", 5), row(3, "e", 6), row(4, "f", 7)}}
		},
		rmanyLog: []string{
			"SELECT @@auto_increment_increment",
			"INSERT INTO `ddefs` (`name`, `score`) VALUES (?, DEFAULT), (?, ?), (?, ?), (?, DEFAULT)",
			"SELECT " + allMy + " FROM `ddefs` WHERE `id` IN (?, ?, ?, ?)",
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
			one, many := c.rone(), c.rmany()
			if err := openFake(c.d, one).Record(&rec).Insert(&a).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			// Recorded, in a transaction of its own: the sink runs before COMMIT.
			wantLog(t, one, append(append([]string{"BEGIN"}, c.roneLog...), "COMMIT"))
			if a.ID != 1 || a.Status != "new" || a.Score != 7 || a.Note == nil || *a.Note != "n" {
				t.Errorf("Insert: the entity read back %+v", a)
			}
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
			if c.rmanyLog[0] == "BEGIN" {
				wantLog(t, many, c.rmanyLog)
			} else {
				wantLog(t, many, append(append([]string{"BEGIN"}, c.rmanyLog...), "COMMIT"))
			}
			cs = slices.Collect(rec.All())
			if len(cs) != 4 {
				t.Fatalf("InsertAll: %d Changes, want 4", len(cs))
			}
			for i, ch := range cs {
				if ch.Op != velox.OpInsert || ch.Key != xs[i].ID || dDefs.Name.New(ch) != xs[i].Name || dDefStatus.New(ch) != "new" {
					t.Errorf("InsertAll change %d: %+v, key %v, name %q, status %q", i, ch, ch.Key, dDefs.Name.New(ch), dDefStatus.New(ch))
				}
			}
			// The caller's slice changed after InsertAll does not reach the
			// Changes.
			names := make([]string, len(xs))
			for i := range xs {
				names[i] = xs[i].Name
				xs[i].Name, xs[i].Status, *xs[i].Note = "changed later", "changed later", "changed later"
			}
			for i, ch := range cs {
				if _, cur, _ := velox.RowsOf[dDef](ch); dDefs.Name.New(ch) != names[i] || dDefStatus.New(ch) != "new" || cur == nil || *cur.Note != "n" {
					t.Errorf("InsertAll change %d after the caller changed its row: name %q, status %q, row %+v", i, dDefs.Name.New(ch), dDefStatus.New(ch), cur)
				}
			}
		})
	}
}

// recDefs are four rows whose middle two set score and the others leave it
// to the database: one statement with DEFAULT cells on PostgreSQL and
// MySQL, three on SQLite (ADR 0093).
func recDefs() []dDef {
	return []dDef{{Name: "c"}, {Name: "d", Score: 5}, {Name: "e", Score: 6}, {Name: "f"}}
}

// TestRecordInsertInTx: inside a transaction a recorded insert and its sink
// run under a savepoint; the sink runs before it is released.
func TestRecordInsertInTx(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1), "a", "new", int64(7), "n", nil}}}
	var at []int
	sink := velox.RecordFunc(func(context.Context, *velox.DB, []velox.Change) error {
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
		`INSERT INTO "ddefs" ("name") VALUES ($1) RETURNING "id", "name", "status", "score", "note", "tag"`,
		"RELEASE SAVEPOINT velox_1",
		"COMMIT",
	})
	if !slices.Equal(at, []int{3}) {
		t.Errorf("the sink ran after %v statements, want once after the INSERT (3)", at)
	}
}

// TestRecordInsertOutsideTx: a recorded single-statement insert outside a
// transaction runs in one of velox's own; the sink runs after the INSERT,
// before COMMIT, and its error rolls the insert back.
func TestRecordInsertOutsideTx(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1), "a", "new", int64(7), "n", nil}}}
	errBoom := errors.New("boom")
	var at []int
	sink := velox.RecordFunc(func(context.Context, *velox.DB, []velox.Change) error {
		at = append(at, len(e.Log))
		return errBoom
	})
	err := openFake(velox.Postgres, e).Record(sink).Insert(&dDef{Name: "a"}).Exec(ctx)
	if err != errBoom {
		t.Fatalf("got %v, want the sink's error", err)
	}
	wantLog(t, e, []string{"BEGIN", `INSERT INTO "ddefs" ("name") VALUES ($1) RETURNING "id", "name", "status", "score", "note", "tag"`, "ROLLBACK"})
	if !slices.Equal(at, []int{2}) {
		t.Errorf("the sink ran after %v statements, want once after the INSERT", at)
	}
}

// tkInsertMy is ticketInsertCols quoted for MySQL.
var tkInsertMy = strings.ReplaceAll(ticketInsertCols, `"`, "`")

// TestRecordUpsert: an upsert of one row that conflicts and one that does
// not reads the conflicting rows first, locked by the conflict columns. On
// PostgreSQL and SQLite the INSERT returns every column of the rows it
// touched; on MySQL a read by the conflict columns follows it, checked
// against the count MySQL reports. The first row is an update with its
// real old values and its new row as stored (owner, which the update
// branch leaves alone, keeps the stored "bob"); the second is an insert.
// The Changes follow the rows' order, whatever order the database gives.
func TestRecordUpsert(t *testing.T) {
	ins := `INSERT INTO "tickets" (` + ticketInsertCols + `) VALUES `
	for _, c := range []struct {
		d       velox.Dialect
		results [][][]any
		want    []string
	}{{velox.Postgres, [][][]any{
		{recTicket(1, "bob", "a", false)},
		{recTicket(1, "bob", "a", true), recTicket(2, "ann", "b", true)},
	}, []string{
		"BEGIN",
		`SELECT ` + ticketCols + ` FROM "tickets" WHERE ("org_id", "title") IN (($1, $2), ($3, $4)) FOR UPDATE`,
		ins + `($1, $2, $3, $4, $5, $6), ($7, $8, $9, $10, $11, $12) ON CONFLICT ("org_id", "title") DO UPDATE SET "done" = EXCLUDED."done" RETURNING ` + ticketCols,
		"COMMIT",
	}}, {velox.SQLite, [][][]any{
		{recTicket(1, "bob", "a", false)},
		{recTicket(1, "bob", "a", true), recTicket(2, "ann", "b", true)},
	}, []string{
		"BEGIN",
		`SELECT ` + ticketCols + ` FROM "tickets" WHERE (("org_id" = ? AND "title" = ?) OR ("org_id" = ? AND "title" = ?))`,
		ins + `(?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?) ON CONFLICT ("org_id", "title") DO UPDATE SET "done" = EXCLUDED."done" RETURNING ` + ticketCols,
		"COMMIT",
	}}, {velox.MySQL, [][][]any{
		{recTicket(1, "bob", "a", false)},
		{recTicket(2, "ann", "b", true), recTicket(1, "bob", "a", true)},
	}, []string{
		"BEGIN",
		"SELECT " + ticketColsMy + " FROM `tickets` WHERE ((`org_id` = ? AND `title` = ?) OR (`org_id` = ? AND `title` = ?)) FOR UPDATE",
		"INSERT INTO `tickets` (" + tkInsertMy + ") VALUES (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?) AS new ON DUPLICATE KEY UPDATE `done` = new.`done`",
		"SELECT " + ticketColsMy + " FROM `tickets` WHERE ((`org_id` = ? AND `title` = ?) OR (`org_id` = ? AND `title` = ?))",
		"COMMIT",
	}}} {
		t.Run(c.d.String(), func(t *testing.T) {
			e := &fakeconn.Exec{Affected: 3, Results: c.results} // MySQL: 1 inserted + 2 updated
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
			if c.d != velox.MySQL && (xs[0].ID != 1 || xs[1].ID != 2) {
				t.Errorf("the entities' keys %d, %d: want what RETURNING gave, as without recording", xs[0].ID, xs[1].ID)
			}
		})
	}
}

// TestRecordUpsertDoNothing: a row DoNothing kept is not returned, and not
// reported; the new row is. A single Insert that DoNothing keeps reports
// nothing.
func TestRecordUpsertDoNothing(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{recTicket(1, "bob", "a", false)},
		{recTicket(2, "ann", "b", true)},
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
		`INSERT INTO "tickets" (` + ticketInsertCols + `) VALUES ($1, $2, $3, $4, $5, $6), ($7, $8, $9, $10, $11, $12) ON CONFLICT ("org_id", "title") DO NOTHING RETURNING ` + ticketCols,
		"COMMIT",
	})
	cs := slices.Collect(rec.All())
	if len(cs) != 1 || cs[0].Op != velox.OpInsert || cs[0].Key != int64(2) || Tk.Title.New(cs[0]) != "b" {
		t.Fatalf("Changes %+v, want the insert of b", cs)
	}

	e = &fakeconn.Exec{Results: [][][]any{{recTicket(1, "bob", "a", false)}, {}}}
	rec.Reset()
	tk := testmodel.Ticket{OrgID: 7, Owner: "ann", Title: "a", Done: true}
	if err := openFake(velox.SQLite, e).Record(&rec).Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).DoNothing().Exec(as("ann", 7)); err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		`SELECT ` + ticketCols + ` FROM "tickets" WHERE ("org_id" = ? AND "title" = ?)`,
		`INSERT INTO "tickets" (` + ticketInsertCols + `) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT ("org_id", "title") DO NOTHING RETURNING ` + ticketCols,
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

// dDefCols are dDef's columns.
const dDefCols = `"id", "name", "status", "score", "note", "tag"`

// TestRecordUpsertOneColumnInTx: a conflict target of one column reads by
// col IN (…); inside a transaction the read, the INSERT and the sink run
// under a savepoint. The new row is the one returned: status, which the
// update branch leaves alone, keeps "done", and the entity gets the
// columns it would get without recording.
func TestRecordUpsertOneColumnInTx(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(1), "a", "done", int64(3), "x", nil}},
		{{int64(1), "b", "done", int64(3), "x", nil}},
	}}
	var rec velox.Recorder
	d := dDef{ID: 1, Name: "b"}
	err := openFake(velox.Postgres, e).Record(&rec).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return tx.Insert(&d).OnConflict(dDefs.ID).Update(dDefs.Name).Exec(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		"SAVEPOINT velox_1",
		`SELECT ` + dDefCols + ` FROM "ddefs" WHERE "id" IN ($1) FOR UPDATE`,
		`INSERT INTO "ddefs" ("id", "name") VALUES ($1, $2) ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name" RETURNING ` + dDefCols,
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
	if d.Status != "done" || d.Score != 3 || d.Note == nil || *d.Note != "x" {
		t.Errorf("entity %+v: want the columns RETURNING would fill without recording", d)
	}
}

// TestRecordUpsertNullConflict: a row with a NULL in a conflict column
// never conflicts; on PostgreSQL and SQLite the INSERT returns it, and it
// is an insert with the key the database gave, with no locked read when no
// row can conflict.
func TestRecordUpsertNullConflict(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
		t.Run(d.String(), func(t *testing.T) {
			e := &fakeconn.Exec{Results: [][][]any{{{int64(5), "a", "new", int64(7), nil, nil}}}}
			var rec velox.Recorder
			x := dDef{Name: "a"}
			if err := openFake(d, e).Record(&rec).Insert(&x).OnConflict(dDefNote).Update(dDefs.Name).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			want := `INSERT INTO "ddefs" ("name", "note") VALUES ($1, NULL) ON CONFLICT ("note") DO UPDATE SET "name" = EXCLUDED."name" RETURNING ` + dDefCols
			if d == velox.SQLite {
				want = strings.Replace(want, "$1", "?", 1)
			}
			wantLog(t, e, []string{"BEGIN", want, "COMMIT"})
			cs := slices.Collect(rec.All())
			if len(cs) != 1 || cs[0].Op != velox.OpInsert || cs[0].Key != int64(5) || x.ID != 5 {
				t.Errorf("Changes %+v, entity key %d; want an insert of key 5", cs, x.ID)
			}
		})
	}
}

// TestRecordUpsertDoNothingGenerated: a DoNothing InsertAll whose rows
// leave the generated key to the database, which is not in the conflict
// target, reports exactly the rows the INSERT returns: those inserted.
func TestRecordUpsertDoNothingGenerated(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{{int64(1), "a", "new", int64(7), "n", nil}},
		{{int64(2), "b", "new", int64(7), "n", nil}, {int64(3), "c", "new", int64(7), "n", nil}},
	}}
	var rec velox.Recorder
	xs := []dDef{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	if err := openFake(velox.Postgres, e).Record(&rec).InsertAll(xs).OnConflict(dDefs.Name).DoNothing().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		`SELECT ` + dDefCols + ` FROM "ddefs" WHERE "name" IN ($1, $2, $3, $4) FOR UPDATE`,
		`INSERT INTO "ddefs" ("name") VALUES ($1), ($2), ($3) ON CONFLICT ("name") DO NOTHING RETURNING ` + dDefCols,
		"COMMIT",
	})
	cs := slices.Collect(rec.All())
	if len(cs) != 2 || cs[0].Op != velox.OpInsert || cs[0].Key != int64(2) || cs[1].Op != velox.OpInsert || cs[1].Key != int64(3) {
		t.Errorf("Changes %+v, want the inserts of b and c", cs)
	}
}

// TestRecordUpsertMySQLRefusesUnfindable: MySQL returns no keys from an
// upsert, so a recorded upsert of a row that cannot conflict and whose key
// only the database knows is refused before anything is sent.
func TestRecordUpsertMySQLRefusesUnfindable(t *testing.T) {
	e := &fakeconn.Exec{}
	rdb := openFake(velox.MySQL, e).Record(&velox.Recorder{})
	err := rdb.Insert(&dDef{Name: "a"}).OnConflict(dDefNote).Update(dDefs.Name).Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "conflict column note is NULL") || len(e.Log) != 0 {
		t.Errorf("NULL: %v, sent %q", err, e.Log)
	}
	err = rdb.InsertAll([]dDef{{Name: "a"}, {Name: "b"}}).OnConflict(dDefs.ID).DoNothing().Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "conflict column id is the generated key, left zero") || len(e.Log) != 0 {
		t.Errorf("generated key: %v, sent %q", err, e.Log)
	}
}

// TestRecordUpsertMySQLCount: on MySQL a count that the rows read back do
// not account for (an inserted row 1, an updated one 2) fails the write,
// which rolls back, and records nothing.
func TestRecordUpsertMySQLCount(t *testing.T) {
	e := &fakeconn.Exec{Affected: 4, Results: [][][]any{
		{recTicket(1, "bob", "a", false)},
		{recTicket(1, "bob", "a", true), recTicket(2, "ann", "b", true)},
	}}
	var rec velox.Recorder
	xs := []testmodel.Ticket{{OrgID: 7, Owner: "ann", Title: "a", Done: true}, {OrgID: 7, Owner: "ann", Title: "b", Done: true}}
	err := openFake(velox.MySQL, e).Record(&rec).InsertAll(xs).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7))
	if err == nil || !strings.Contains(err.Error(), "affected 4 rows by MySQL's count, and the 2 rows it finds by the conflict columns account for 3") {
		t.Fatalf("got %v, want the count's refusal", err)
	}
	if last := e.Log[len(e.Log)-1]; last != "ROLLBACK" || rec.Len() != 0 {
		t.Errorf("last statement %q, %d Changes; want ROLLBACK and none", last, rec.Len())
	}
}

// TestRecordUpsertDoNothingMySQL: on MySQL a DoNothing upsert reads the
// conflicting rows locked, sends its INSERT, and reads the rows back; the
// kept row (counted 1 under clientFoundRows) is not reported, the new one
// is.
func TestRecordUpsertDoNothingMySQL(t *testing.T) {
	e := &fakeconn.Exec{Affected: 2, Results: [][][]any{
		{recTicket(1, "bob", "a", false)},
		{recTicket(1, "bob", "a", false), recTicket(2, "ann", "b", true)},
	}}
	var rec velox.Recorder
	xs := []testmodel.Ticket{{OrgID: 7, Owner: "ann", Title: "a", Done: true}, {OrgID: 7, Owner: "ann", Title: "b", Done: true}}
	if err := openFake(velox.MySQL, e).Record(&rec).InsertAll(xs).OnConflict(Tk.OrgID, Tk.Title).DoNothing().Exec(as("ann", 7)); err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		"SELECT " + ticketColsMy + " FROM `tickets` WHERE ((`org_id` = ? AND `title` = ?) OR (`org_id` = ? AND `title` = ?)) FOR UPDATE",
		"INSERT INTO `tickets` (" + tkInsertMy + ") VALUES (?, ?, ?, ?, ?, ?), (?, ?, ?, ?, ?, ?) AS new ON DUPLICATE KEY UPDATE `org_id` = `tickets`.`org_id`",
		"SELECT " + ticketColsMy + " FROM `tickets` WHERE ((`org_id` = ? AND `title` = ?) OR (`org_id` = ? AND `title` = ?))",
		"COMMIT",
	})
	cs := slices.Collect(rec.All())
	if len(cs) != 1 || cs[0].Op != velox.OpInsert || cs[0].Key != int64(2) {
		t.Errorf("Changes %+v, want the insert of b", cs)
	}
}

// TestRecordUpsertInOwnTx: an upsert InsertAll that WithMaxParams splits
// into two statements runs in velox's own transaction, each statement with
// its locked read, and the sink runs once, before the COMMIT, with every
// Change in the order of the rows.
func TestRecordUpsertInOwnTx(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{},
		{{int64(1), "a", "new", int64(7), "n", nil}},
		{{int64(2), "old", "new", int64(1), "n", nil}},
		{{int64(2), "b", "new", int64(5), "n", nil}},
	}}
	var at []int
	var got []velox.Change
	sink := velox.RecordFunc(func(_ context.Context, _ *velox.DB, cs []velox.Change) error {
		at = append(at, len(e.Log))
		got = append(got, cs...)
		return nil
	})
	xs := []dDef{{ID: 1, Name: "a"}, {ID: 2, Name: "b", Score: 5}}
	if err := openFake(velox.Postgres, e, engine.WithMaxParams(3)).Record(sink).InsertAll(xs).OnConflict(dDefs.ID).Update(dDefs.Name, dDefs.Score).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ins := `INSERT INTO "ddefs" ("id", "name", "score") VALUES ($1, $2, $3) ON CONFLICT ("id") DO UPDATE SET "name" = EXCLUDED."name", "score" = EXCLUDED."score" RETURNING ` + dDefCols
	wantLog(t, e, []string{
		"BEGIN",
		`SELECT ` + dDefCols + ` FROM "ddefs" WHERE "id" IN ($1) FOR UPDATE`,
		ins,
		`SELECT ` + dDefCols + ` FROM "ddefs" WHERE "id" IN ($1) FOR UPDATE`,
		ins,
		"COMMIT",
	})
	if !slices.Equal(at, []int{5}) {
		t.Errorf("the sink ran after %v statements, want once before COMMIT (5)", at)
	}
	if len(got) != 2 || got[0].Op != velox.OpInsert || got[0].Key != int64(1) || got[1].Op != velox.OpUpdate || got[1].Key != int64(2) ||
		dDefs.Name.Old(got[1]) != "old" || dDefs.Score.New(got[1]) != 5 {
		t.Errorf("Changes %+v", got)
	}
}

// TestRecordRewriteDropsReturning: a recorded insert or upsert reports the
// rows its RETURNING reads, so a Rewrite hook that drops it is refused
// before the INSERT is sent: a plain Insert opens its transaction and rolls
// it back, and an upsert,
// whose locked read went first, rolls back. Without recording the same
// hook is allowed, as before.
func TestRecordRewriteDropsReturning(t *testing.T) {
	drop := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		if st.Op == velox.OpInsert {
			st.Returning = nil
		}
		return nil
	})
	const refused = "a Rewrite hook changed the RETURNING of a recorded write"

	e := &fakeconn.Exec{Affected: 1}
	var rec velox.Recorder
	rdb := openFake(velox.Postgres, e, drop).Record(&rec)
	err := rdb.Insert(&dDef{Name: "a"}).Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), refused) || !slices.Equal(e.Log, []string{"BEGIN", "ROLLBACK"}) {
		t.Errorf("Insert: %v, sent %q", err, e.Log)
	}
	e.Log, e.ArgLog = nil, nil

	tk := testmodel.Ticket{OrgID: 7, Owner: "ann", Title: "a", Done: true}
	err = rdb.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7))
	if err == nil || !strings.Contains(err.Error(), refused) {
		t.Fatalf("upsert: %v", err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		`SELECT ` + ticketCols + ` FROM "tickets" WHERE ("org_id", "title") IN (($1, $2)) FOR UPDATE`,
		"ROLLBACK",
	})
	if rec.Len() != 0 {
		t.Errorf("%d Changes", rec.Len())
	}

	e = &fakeconn.Exec{Affected: 1}
	if err := openFake(velox.Postgres, e, drop).Insert(&dDef{Name: "a"}).Exec(ctx); err != nil || len(e.Log) != 1 || strings.Contains(e.Log[0], "RETURNING") {
		t.Errorf("without recording: %v, sent %q", err, e.Log)
	}
}

// TestRecordInsertStoredRow: a recorded insert's new row is the row as the
// database stored it, which may differ from the Go value sent (a decimal
// rounded to its scale, a time to the microsecond): on PostgreSQL and
// SQLite the INSERT returns every column, on MySQL one read by key follows
// it, in the write's transaction. The caller's entity gets only the key
// and the columns the database filled, as without recording.
func TestRecordInsertStoredRow(t *testing.T) {
	L := testmodel.Ledgers
	for _, c := range []struct {
		d    velox.Dialect
		e    func() *fakeconn.Exec
		log  []string
		many []string
	}{{
		d:    velox.Postgres,
		e:    func() *fakeconn.Exec { return &fakeconn.Exec{Rows: [][]any{{int64(1), "1.56", nil}}} },
		log:  []string{"BEGIN", `INSERT INTO "ledgers" ("amount") VALUES ($1) RETURNING "id", "amount", "fee"`, "COMMIT"},
		many: []string{"BEGIN", `INSERT INTO "ledgers" ("amount") VALUES ($1) RETURNING "id", "amount", "fee"`, "COMMIT"},
	}, {
		d:    velox.SQLite,
		e:    func() *fakeconn.Exec { return &fakeconn.Exec{Rows: [][]any{{int64(1), "1.56", nil}}} },
		log:  []string{"BEGIN", `INSERT INTO "ledgers" ("amount") VALUES (?) RETURNING "id", "amount", "fee"`, "COMMIT"},
		many: []string{"BEGIN", `INSERT INTO "ledgers" ("amount") VALUES (?) RETURNING "id", "amount", "fee"`, "COMMIT"},
	}, {
		d: velox.MySQL,
		e: func() *fakeconn.Exec {
			return &fakeconn.Exec{Affected: 1, LastID: 1, HasLastID: true, Rows: [][]any{{int64(1), "1.56", nil}}}
		},
		log:  []string{"BEGIN", "INSERT INTO `ledgers` (`amount`) VALUES (?)", "SELECT `id`, `amount`, `fee` FROM `ledgers` WHERE `id` IN (?)", "COMMIT"},
		many: []string{"BEGIN", "INSERT INTO `ledgers` (`amount`) VALUES (?)", "SELECT `id`, `amount`, `fee` FROM `ledgers` WHERE `id` IN (?)", "COMMIT"},
	}} {
		t.Run(c.d.String(), func(t *testing.T) {
			var rec velox.Recorder
			e := c.e()
			x := testmodel.Ledger{Amount: "1.555"}
			if err := openFake(c.d, e).Record(&rec).Insert(&x).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			wantLog(t, e, c.log)
			cs := slices.Collect(rec.All())
			if len(cs) != 1 || L.Amount.New(cs[0]) != "1.56" || cs[0].Key != int64(1) {
				t.Fatalf("Insert: Changes %+v, amount %q", cs, L.Amount.New(cs[0]))
			}
			if x.ID != 1 || x.Amount != "1.555" {
				t.Errorf("Insert: the caller's entity %+v", x)
			}

			rec.Reset()
			e = c.e()
			xs := []testmodel.Ledger{{Amount: "1.555"}}
			if err := openFake(c.d, e).Record(&rec).InsertAll(xs).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			wantLog(t, e, c.many)
			cs = slices.Collect(rec.All())
			if len(cs) != 1 || L.Amount.New(cs[0]) != "1.56" || cs[0].Key != int64(1) {
				t.Fatalf("InsertAll: Changes %+v", cs)
			}
			if xs[0].ID != 1 || xs[0].Amount != "1.555" {
				t.Errorf("InsertAll: the caller's entity %+v", xs[0])
			}
		})
	}
}

// TestRecordUpsertEntityOwnsValues: the columns a recorded upsert reads back
// into the caller's entity are its own: a later change through a pointer
// field does not reach the Change's new row.
func TestRecordUpsertEntityOwnsValues(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{}, {{int64(1), "a", "new", int64(7), "n", nil}}}}
	var rec velox.Recorder
	a := dDef{Name: "a"}
	if err := openFake(velox.Postgres, e).Record(&rec).Insert(&a).OnConflict(dDefs.Name).Update(dDefs.Name).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if a.ID != 1 || a.Note == nil || *a.Note != "n" {
		t.Fatalf("read back %+v", a)
	}
	*a.Note = "changed later"
	cs := slices.Collect(rec.All())
	if len(cs) != 1 || dDefNote.New(cs[0]) != "n" {
		t.Errorf("the Change's note %q, want n", dDefNote.New(cs[0]))
	}
}
