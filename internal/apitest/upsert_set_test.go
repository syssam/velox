package apitest

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestUpsertUpdateSetSQL: UpdateSet's setters render in the update branch
// after Update's copies, on every dialect: Set a value, Add the existing
// row's value plus the argument, by its table, and SetExpr an expression
// whose handles are the existing row's and whose Excluded is the inserted
// row's. The assigned columns are read back with RETURNING.
func TestUpsertUpdateSetSQL(t *testing.T) {
	for _, c := range []struct {
		d    velox.Dialect
		want string
	}{
		{velox.Postgres, `ON CONFLICT ("org_id", "number") DO UPDATE SET "paid" = EXCLUDED."paid", "total" = "invoices"."total" + $9, "note" = $10, "updated_at" = ("invoices"."updated_at") RETURNING "id", "total", "note", "updated_at"`},
		{velox.SQLite, `ON CONFLICT ("org_id", "number") DO UPDATE SET "paid" = EXCLUDED."paid", "total" = "invoices"."total" + ?, "note" = ?, "updated_at" = ("invoices"."updated_at") RETURNING "id", "total", "note", "updated_at"`},
		{velox.MySQL, "AS new ON DUPLICATE KEY UPDATE `paid` = new.`paid`, `total` = `invoices`.`total` + ?, `note` = ?, `updated_at` = (`invoices`.`updated_at`)"},
	} {
		db := openFake(c.d, &fakeconn.Exec{})
		inv := testmodel.Invoice{OrgID: 1, Number: "N", Total: 3, Data: []byte("d")}
		got, args, err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).Update(I.Paid).
			UpdateSet(I.Total.Add(2), I.Note.Set("again"), velox.SetExpr(I.UpdatedAt, "?", I.UpdatedAt)).SQL(ctx)
		if err != nil || !strings.HasSuffix(got, c.want) {
			t.Errorf("%v:\n got %s, %v\nwant suffix %s", c.d, got, err, c.want)
			continue
		}
		if n := len(args); args[n-2] != 2.0 || args[n-1] != "again" {
			t.Errorf("%v: args %v", c.d, args)
		}
	}
}

// TestUpsertExcludedSQL: Excluded names the inserted row's column, in
// UpdateSet's expression and in UpdateWhere's condition; anywhere else it
// fails the statement.
func TestUpsertExcludedSQL(t *testing.T) {
	for _, c := range []struct {
		d    velox.Dialect
		want string
	}{
		{velox.Postgres, `DO UPDATE SET "total" = ("invoices"."total" + EXCLUDED."total") WHERE ("invoices"."total" < EXCLUDED."total") RETURNING "id", "total"`},
		{velox.SQLite, `DO UPDATE SET "total" = ("invoices"."total" + EXCLUDED."total") WHERE ("invoices"."total" < EXCLUDED."total") RETURNING "id", "total"`},
	} {
		db := openFake(c.d, &fakeconn.Exec{})
		inv := testmodel.Invoice{OrgID: 1, Number: "N", Total: 3, Data: []byte("d")}
		got, _, err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).
			UpdateSet(velox.SetExpr(I.Total, "? + ?", I.Total, velox.Excluded(I.Total))).
			UpdateWhere(velox.Expr[testmodel.Invoice]("? < ?", I.Total, velox.Excluded(I.Total))).SQL(ctx)
		if err != nil || !strings.HasSuffix(got, c.want) {
			t.Errorf("%v:\n got %s, %v\nwant suffix %s", c.d, got, err, c.want)
		}
	}
	db := openFake(velox.MySQL, &fakeconn.Exec{})
	inv := testmodel.Invoice{OrgID: 1, Number: "N", Total: 3}
	got, _, err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).
		UpdateSet(velox.SetExpr(I.Total, "? + ?", I.Total, velox.Excluded(I.Total))).SQL(ctx)
	if want := "UPDATE `total` = (`invoices`.`total` + new.`total`)"; err != nil || !strings.HasSuffix(got, want) {
		t.Errorf("MySQL:\n got %s, %v", got, err)
	}

	pg := openFake(velox.Postgres, &fakeconn.Exec{})
	_, _, err = pg.Query[testmodel.Invoice]().Where(velox.Expr[testmodel.Invoice]("? > 0", velox.Excluded(I.Total))).Limit(1).SQL(ctx)
	if err == nil || !strings.Contains(err.Error(), "Excluded names the row an upsert proposes") {
		t.Errorf("Excluded in a query: %v", err)
	}
	_, _, err = pg.Update[testmodel.Invoice]().Set(velox.SetExpr(I.Total, "?", velox.Excluded(I.Total))).All().SQL(ctx)
	if err == nil || !strings.Contains(err.Error(), "Excluded names the row an upsert proposes") {
		t.Errorf("Excluded in an update: %v", err)
	}
}

// TestUpsertExcludedIsSent: a column that Excluded names is written by the
// INSERT, never left to its database default, whose value EXCLUDED would
// then hold.
func TestUpsertExcludedIsSent(t *testing.T) {
	db, _ := defDB(t)
	dScore := velox.NumberCol[dDef, int64]{I: 3}
	got, _, err := db.Insert(&dDef{ID: 1, Name: "a"}).OnConflict(dDefs.ID).
		UpdateSet(velox.SetExpr(dScore, "? + ?", dScore, velox.Excluded(dScore))).SQL(ctx)
	if want := `INSERT INTO "ddefs" ("id", "name", "score") VALUES (?, ?, ?) ON CONFLICT ("id") DO UPDATE SET "score" = ("ddefs"."score" + EXCLUDED."score") RETURNING "status", "note", "score"`; err != nil || got != want {
		t.Errorf("\n got %s, %v\nwant %s", got, err, want)
	}
}

// TestUpsertConflictTargetSQL: OnConflictWhere is the conflict target's
// condition, unqualified, on PostgreSQL and SQLite; OnConstraint names a
// constraint on PostgreSQL. MySQL refuses both, and UpdateWhere, and SQLite
// refuses OnConstraint, before anything is sent.
func TestUpsertConflictTargetSQL(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
		db := openFake(d, &fakeconn.Exec{})
		inv := testmodel.Invoice{OrgID: 1, Number: "N", Data: []byte("d")}
		got, _, err := db.Insert(&inv).OnConflict(I.Number).OnConflictWhere(I.PaidAt.IsNull()).DoNothing().SQL(ctx)
		if want := `ON CONFLICT ("number") WHERE "paid_at" IS NULL DO NOTHING RETURNING "id"`; err != nil || !strings.HasSuffix(got, want) {
			t.Errorf("%v:\n got %s, %v\nwant suffix %s", d, got, err, want)
		}
	}
	pg := openFake(velox.Postgres, &fakeconn.Exec{})
	inv := testmodel.Invoice{OrgID: 1, Number: "N", Data: []byte("d")}
	got, _, err := pg.Insert(&inv).OnConflict(I.OrgID, I.Number).OnConstraint("invoices_org_number_uq").Update(I.Total).SQL(ctx)
	if want := `ON CONFLICT ON CONSTRAINT "invoices_org_number_uq" DO UPDATE SET "total" = EXCLUDED."total" RETURNING "id"`; err != nil || !strings.HasSuffix(got, want) {
		t.Errorf("OnConstraint:\n got %s, %v", got, err)
	}

	sq := openFake(velox.SQLite, &fakeconn.Exec{})
	if _, _, err := sq.Insert(&inv).OnConflict(I.OrgID, I.Number).OnConstraint("c").DoNothing().SQL(ctx); err == nil || !strings.Contains(err.Error(), "OnConstraint is PostgreSQL's") {
		t.Errorf("SQLite OnConstraint: %v", err)
	}
	e := &fakeconn.Exec{}
	my := openFake(velox.MySQL, e)
	for name, q := range map[string]velox.InsertQuery[testmodel.Invoice]{
		"UpdateWhere":     my.Insert(&inv).OnConflict(I.OrgID, I.Number).Update(I.Total).UpdateWhere(I.Total.GT(1)),
		"OnConflictWhere": my.Insert(&inv).OnConflict(I.OrgID, I.Number).OnConflictWhere(I.PaidAt.IsNull()).Update(I.Total),
		"OnConstraint":    my.Insert(&inv).OnConflict(I.OrgID, I.Number).OnConstraint("c").Update(I.Total),
	} {
		if err := q.Exec(ctx); err == nil || !strings.Contains(err.Error(), "MySQL's ON DUPLICATE KEY UPDATE") || !strings.Contains(err.Error(), name) {
			t.Errorf("MySQL %s: %v", name, err)
		}
	}
	if e.Calls != 0 {
		t.Errorf("MySQL sent %d statements", e.Calls)
	}
}

// TestUpsertUpdateSetRefusals: UpdateSet refuses what cannot be built
// before anything is sent.
func TestUpsertUpdateSetRefusals(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	inv := testmodel.Invoice{OrgID: 1, Number: "N"}
	on := func() velox.InsertQuery[testmodel.Invoice] { return db.Insert(&inv).OnConflict(I.OrgID, I.Number) }
	for _, c := range []struct {
		name string
		q    velox.InsertQuery[testmodel.Invoice]
		want string
	}{
		{"twice", on().Update(I.Total).UpdateSet(I.Total.Add(1)), "assigns total twice"},
		{"twice in Set", on().UpdateSet(I.Total.Add(1), I.Total.Set(2)), "assigns total twice"},
		{"immutable", on().UpdateSet(I.CreatedAt.Set(t0)), "immutable"},
		{"no branch", on().UpdateWhere(I.Total.GT(1)), "UpdateWhere conditions the update branch"},
		{"zero target", on().OnConflictWhere(velox.Pred[testmodel.Invoice]{}).DoNothing(), "zero Predicate"},
		{"edge", on().UpdateSet(I.Items.Add(1)), "UpdateSet takes column setters"},
	} {
		if err := c.q.Exec(ctx); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(c.want)) {
			t.Errorf("%s: %v; want %q", c.name, err, c.want)
		}
	}
	if e.Calls != 0 {
		t.Errorf("sent %d statements", e.Calls)
	}
}

// TestUpsertUpdateSetSkipsAndDoNothing: a skipped SetPtr assigns nothing;
// with nothing left, the upsert keeps the existing row through the no-op
// assignment, as Set's skipped setters do. DoNothing drops the setters, and
// UpdateAll leaves out the columns UpdateSet assigns.
func TestUpsertUpdateSetSkipsAndDoNothing(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	inv := testmodel.Invoice{OrgID: 1, Number: "N", Data: []byte("d")}
	var note *string
	got, _, err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).UpdateSet(I.Note.SetPtr(note)).SQL(ctx)
	if want := `DO UPDATE SET "org_id" = "invoices"."org_id" RETURNING "id"`; err != nil || !strings.HasSuffix(got, want) {
		t.Errorf("skipped:\n got %s, %v", got, err)
	}
	got, _, err = db.Insert(&inv).OnConflict(I.OrgID, I.Number).UpdateSet(I.Total.Add(1)).DoNothing().SQL(ctx)
	if err != nil || !strings.HasSuffix(got, `DO NOTHING RETURNING "id"`) {
		t.Errorf("DoNothing:\n got %s, %v", got, err)
	}
	got, _, err = db.Insert(&inv).OnConflict(I.OrgID, I.Number).UpdateAll().UpdateSet(I.Total.Add(1)).SQL(ctx)
	if err != nil || strings.Contains(got, `EXCLUDED."total"`) || !strings.Contains(got, `"total" = "invoices"."total" + $`) || !strings.Contains(got, `"note" = EXCLUDED."note"`) {
		t.Errorf("UpdateAll:\n got %s, %v", got, err)
	}
}

// TestUpsertUpdateSetReadsBack: on SQLite the update branch's Add reaches
// the stored row, and the entity gets the stored value back.
func TestUpsertUpdateSetReadsBack(t *testing.T) {
	db, raw := defDB(t)
	exec(t, raw, "INSERT INTO ddefs (id, name, score) VALUES (1, 'a', 10)")
	dScore := velox.NumberCol[dDef, int64]{I: 3}
	x := dDef{ID: 1, Name: "a", Score: 5}
	if err := db.Insert(&x).OnConflict(dDefs.ID).UpdateSet(dScore.Add(5)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var score int64
	if err := raw.QueryRow("SELECT score FROM ddefs WHERE id = 1").Scan(&score); err != nil || score != 15 || x.Score != 15 {
		t.Errorf("stored %d, entity %d, %v; want 15", score, x.Score, err)
	}
}
