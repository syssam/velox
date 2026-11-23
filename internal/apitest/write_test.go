package apitest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

func TestUpdateSetsUpdateDefaultColumns(t *testing.T) {
	e := &fakeconn.Exec{Affected: 2}
	db := openFake(velox.Postgres, e)
	n, err := db.Update[testmodel.Invoice]().Set(I.Total.Add(5), I.Note.Set("")).Where(I.OrgID.EQ(1)).Exec(ctx)
	if err != nil || n != 2 {
		t.Fatalf("%d, %v", n, err)
	}
	want := `UPDATE "invoices" SET "total" = "total" + $1, "note" = $2, "updated_at" = $3 WHERE "org_id" = $4`
	if e.SQL != want {
		t.Fatalf("\n got %s\nwant %s", e.SQL, want)
	}
	if e.Args[0] != 5.0 || e.Args[1] != "" || e.Args[3] != int64(1) {
		t.Errorf("args %v", e.Args)
	}
	if at, ok := e.Args[2].(time.Time); !ok || time.Since(at) > time.Minute {
		t.Errorf("updated_at %v", e.Args[2])
	}
	if _, err := db.Update[testmodel.Invoice]().Set(I.UpdatedAt.Set(t0)).Where(I.ID.EQ(1)).Exec(ctx); err != nil || e.SQL != `UPDATE "invoices" SET "updated_at" = $1 WHERE "id" = $2` {
		t.Errorf("an explicit UpdatedAt must not be set twice: %s %v", e.SQL, err)
	}
}

func TestUpdateNeedsSet(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	if _, err := db.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(ctx); err == nil || !strings.Contains(err.Error(), "Update has no Set") || e.Calls != 0 {
		t.Errorf("%v, calls %d", err, e.Calls)
	}
}

func TestAllMarksTheStatement(t *testing.T) {
	var all []bool
	spy := func(_ context.Context, st *velox.Statement) error { all = append(all, st.AllRows); return nil }
	db := openFake(velox.Postgres, &fakeconn.Exec{}, velox.WithRewrite(spy))
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).All().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Delete[testmodel.Invoice]().All().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Delete[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(all, []bool{true, true, false}) {
		t.Errorf("AllRows %v", all)
	}
}

func TestDelete(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.MySQL, e)
	n, err := db.Delete[testmodel.Invoice]().Where(I.ID.EQ(4)).Exec(ctx)
	if err != nil || n != 1 || e.SQL != "DELETE FROM `invoices` WHERE `id` = ?" {
		t.Errorf("%d %v %s", n, err, e.SQL)
	}
	if text, _ := sqlOK(t)(db.Delete[testmodel.Invoice]().Where(I.ID.EQ(4)).SQL(ctx)); text != e.SQL {
		t.Errorf("SQL(ctx) %s", text)
	}
}

func TestSave(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 3, Number: "X"}
	if err := db.Save(&inv, I.Number).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "invoices" SET "number" = $1, "updated_at" = $2 WHERE "id" = $3`; e.SQL != want {
		t.Errorf("\n got %s\nwant %s", e.SQL, want)
	}
	if inv.UpdatedAt.IsZero() || e.Args[0] != "X" || e.Args[2] != int64(3) || e.Args[1] != inv.UpdatedAt {
		t.Errorf("args %v, UpdatedAt %v", e.Args, inv.UpdatedAt)
	}
	e.Affected = 0
	if err := db.Save(&inv, I.Number).Exec(ctx); !errors.Is(err, velox.ErrNotFound) {
		t.Errorf("no row: %v", err)
	}
}

func TestSaveStoresUpdateDefaultOnlyAfterSuccess(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 3, Number: "X", UpdatedAt: t0}

	e.Err = errors.New("boom")
	if err := db.Save(&inv, I.Number).Exec(ctx); err == nil {
		t.Fatal("want the driver's error")
	}
	if !inv.UpdatedAt.Equal(t0) {
		t.Errorf("a failed Save changed UpdatedAt to %v", inv.UpdatedAt)
	}

	e.Err, e.Affected = nil, 0
	if err := db.Save(&inv, I.Number).Exec(ctx); !errors.Is(err, velox.ErrNotFound) {
		t.Fatalf("no row: %v", err)
	}
	if !inv.UpdatedAt.Equal(t0) {
		t.Errorf("a Save that found no row changed UpdatedAt to %v", inv.UpdatedAt)
	}

	e.Affected = 1
	if err := db.Save(&inv, I.Number).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if inv.UpdatedAt.Equal(t0) || e.Args[1] != inv.UpdatedAt {
		t.Errorf("UpdatedAt %v, sent %v", inv.UpdatedAt, e.Args[1])
	}
}

// TestALaterSetterWins: a column assigned twice keeps the last assignment,
// as ent's mutation keeps one value per field (ADR 0140), so the statement
// assigns it once, at the later setter's place.
func TestALaterSetterWins(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 3, Number: "N"}
	if err := db.Save(&inv, I.Number, I.Number).Exec(ctx); err != nil || strings.Count(e.SQL, "number") != 1 {
		t.Errorf("Save naming number twice: %v %s", err, e.SQL)
	}
	for name, tc := range map[string]struct {
		u    velox.UpdateQuery[testmodel.Invoice]
		want string
		args []any
	}{
		"one Set":       {db.Update[testmodel.Invoice]().Set(I.Total.Add(1), I.Total.Set(2)).Where(I.ID.EQ(1)), `SET "total" = $1, "updated_at" = $2 WHERE`, []any{int64(2)}},
		"two Set calls": {db.Update[testmodel.Invoice]().Set(I.Total.Add(1)).Set(I.Total.Set(2)).Where(I.ID.EQ(1)), `SET "total" = $1, "updated_at" = $2 WHERE`, []any{int64(2)}},
		"Set then Add":  {db.Update[testmodel.Invoice]().Set(I.Total.Set(2)).Set(I.Total.Add(5)).Where(I.ID.EQ(1)), `SET "total" = "total" + $1, "updated_at" = $2 WHERE`, []any{int64(5)}},
	} {
		text, args, err := tc.u.SQL(ctx)
		if err != nil {
			t.Errorf("%s: %q %v", name, text, err)
			continue
		}
		if !strings.Contains(text, tc.want) || len(args) == 0 || fmt.Sprint(args[0]) != fmt.Sprint(tc.args[0]) {
			t.Errorf("%s: %q %v, want %q with %v", name, text, args, tc.want, tc.args)
		}
		if _, err := tc.u.Exec(ctx); err != nil {
			t.Errorf("%s Exec: %v", name, err)
		}
	}
	// An insert keeps the last setter too: setters write the row it sends.
	ins := testmodel.Invoice{OrgID: 1}
	if _, args, err := db.Insert(&ins).Set(I.Number.Set("a")).Set(I.Number.Set("b")).SQL(ctx); err != nil || !slices.Contains(args, any("b")) || slices.Contains(args, any("a")) {
		t.Errorf("insert setting number twice: %v %v", args, err)
	}
	// A builder is a value: one built from another by a later setter leaves
	// the first as it was.
	base := db.Update[testmodel.Invoice]().Set(I.Total.Set(2), I.Note.Set("a")).Where(I.ID.EQ(1))
	_ = base.Set(I.Total.Set(9))
	if _, args, err := base.SQL(ctx); err != nil || fmt.Sprint(args[0]) != "2" || fmt.Sprint(args[1]) != "a" {
		t.Errorf("base after a derived Set: %v %v", args, err)
	}
}

func TestSaveRefusesNil(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	if err := db.Save[testmodel.Invoice](nil, I.Number).Exec(ctx); err == nil || err.Error() != "velox: testmodel.Invoice: Save of a nil pointer" {
		t.Errorf("nil pointer: %v", err)
	}
	inv := testmodel.Invoice{ID: 3}
	if err := db.Save(&inv, I.Number, nil).Exec(ctx); err == nil || err.Error() != "velox: testmodel.Invoice: Save names a nil column" {
		t.Errorf("nil column: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("calls %d", e.Calls)
	}
}

// TestSaveSQL: SQL(ctx) renders the statement Exec sends, with Where's
// conditions after the key, sends nothing, leaves *x as it was and does not
// fail with ErrNotFound for a row it did not look for.
func TestSaveSQL(t *testing.T) {
	e := &fakeconn.Exec{Affected: 0}
	db := openFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 3, Number: "X"}
	q := db.Save(&inv, I.Number).Where(I.Total.EQ(5), I.Paid.EQ(false))
	text, args, err := q.SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const want = `UPDATE "invoices" SET "number" = $1, "updated_at" = $2 WHERE "id" = $3 AND "total" = $4 AND NOT "paid"`
	if text != want {
		t.Errorf("\n got %s\nwant %s", text, want)
	}
	if len(args) != 4 || args[0] != "X" || args[2] != int64(3) || args[3] != 5.0 {
		t.Errorf("args %v", args)
	}
	if e.Calls != 0 {
		t.Errorf("SQL(ctx) sent %d statements", e.Calls)
	}
	if !inv.UpdatedAt.IsZero() {
		t.Errorf("SQL(ctx) changed *x: UpdatedAt %v", inv.UpdatedAt)
	}
	// Exec sends the same statement, and finds no row.
	if err := q.Exec(ctx); !errors.Is(err, velox.ErrNotFound) {
		t.Errorf("Exec: %v", err)
	}
	if e.SQL != want {
		t.Errorf("Exec sent %s", e.SQL)
	}
	// The builder is a value: Where on it leaves it as it was.
	q2 := q.Where(I.Note.EQ("n"))
	if text2, _, err := q.SQL(ctx); err != nil || text2 != want {
		t.Errorf("q after Where: %s %v", text2, err)
	}
	if text2, _, err := q2.SQL(ctx); err != nil || !strings.HasSuffix(text2, ` AND "note" = $5`) {
		t.Errorf("q2: %s %v", text2, err)
	}
	// Errors are the same from SQL(ctx) as from Exec, and nothing is sent.
	calls := e.Calls
	for name, q := range map[string]velox.SaveQuery[testmodel.Invoice]{
		"nil pointer":     db.Save[testmodel.Invoice](nil, I.Number),
		"nil column":      db.Save(&inv, I.Number, nil),
		"zero predicate":  db.Save(&inv, I.Number).Where(velox.Pred[testmodel.Invoice]{}),
		"zero after good": db.Save(&inv, I.Number).Where(I.Total.EQ(1), velox.Pred[testmodel.Invoice]{}),
	} {
		_, _, serr := q.SQL(ctx)
		eerr := q.Exec(ctx)
		if serr == nil || eerr == nil || serr.Error() != eerr.Error() {
			t.Errorf("%s: SQL(ctx) %v, Exec %v", name, serr, eerr)
		}
	}
	if e.Calls != calls {
		t.Errorf("%d statements sent", e.Calls-calls)
	}
}

// TestSaveWhereNeedsAMatch: a condition that does not hold is ErrNotFound,
// and *x is as it was.
func TestSaveWhereNeedsAMatch(t *testing.T) {
	e := &fakeconn.Exec{Affected: 0}
	db := openFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 3, Number: "X", UpdatedAt: t0}
	err := db.Save(&inv, I.Number).Where(I.Total.EQ(1)).Exec(ctx)
	if !errors.Is(err, velox.ErrNotFound) || !strings.Contains(err.Error(), "testmodel.Invoice") {
		t.Errorf("got %v", err)
	}
	if !inv.UpdatedAt.Equal(t0) {
		t.Errorf("UpdatedAt %v", inv.UpdatedAt)
	}
}

// TestSaveWhereOnTheKeyKeepsTheKey: a condition on the key column is added to
// the key's, never put in its place, so Save writes x's row or none. *x is
// read when SQL or Exec runs, not when Save is called.
func TestSaveWhereOnTheKeyKeepsTheKey(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 3, Number: "X"}
	q := db.Save(&inv, I.Number).Where(I.ID.EQ(4))
	inv.Number = "Y"
	text, args, err := q.SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const want = `UPDATE "invoices" SET "number" = $1, "updated_at" = $2 WHERE "id" = $3 AND "id" = $4`
	if text != want || args[0] != "Y" || args[2] != int64(3) || args[3] != int64(4) {
		t.Errorf("%s %v", text, args)
	}
}
