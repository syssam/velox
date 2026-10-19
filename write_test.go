package velox_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestUpdateSetsUpdateDefaultColumns(t *testing.T) {
	e := &velox.FakeExec{Affected: 2}
	db := velox.OpenFake(velox.Postgres, e)
	n, err := db.Update[testmodel.Invoice]().Set(I.Total.Add(5), I.Note.SetNull()).Where(I.OrgID.EQ(1)).Exec(ctx)
	if err != nil || n != 2 {
		t.Fatalf("%d, %v", n, err)
	}
	want := `UPDATE "invoices" SET "total" = "total" + $1, "note" = $2, "updated_at" = $3 WHERE "org_id" = $4`
	if e.SQL != want {
		t.Fatalf("\n got %s\nwant %s", e.SQL, want)
	}
	if e.Args[0] != 5.0 || e.Args[1] != nil || e.Args[3] != int64(1) {
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
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(ctx); err == nil || !strings.Contains(err.Error(), "Update has no Set") || e.Calls != 0 {
		t.Errorf("%v, calls %d", err, e.Calls)
	}
}

func TestAllMarksTheStatement(t *testing.T) {
	var all []bool
	spy := func(_ context.Context, st *velox.Statement) error { all = append(all, st.AllRows); return nil }
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithRewrite(spy))
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
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.MySQL, e)
	n, err := db.Delete[testmodel.Invoice]().Where(I.ID.EQ(4)).Exec(ctx)
	if err != nil || n != 1 || e.SQL != "DELETE FROM `invoices` WHERE `id` = ?" {
		t.Errorf("%d %v %s", n, err, e.SQL)
	}
	if text, _ := sqlOK(t)(db.Delete[testmodel.Invoice]().Where(I.ID.EQ(4)).SQL(ctx)); text != e.SQL {
		t.Errorf("SQL(ctx) %s", text)
	}
}

func TestSave(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 3, Number: "X"}
	if err := db.Save(ctx, &inv, I.Number); err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "invoices" SET "number" = $1, "updated_at" = $2 WHERE "id" = $3`; e.SQL != want {
		t.Errorf("\n got %s\nwant %s", e.SQL, want)
	}
	if inv.UpdatedAt.IsZero() || e.Args[0] != "X" || e.Args[2] != int64(3) || e.Args[1] != inv.UpdatedAt {
		t.Errorf("args %v, UpdatedAt %v", e.Args, inv.UpdatedAt)
	}
	e.Affected = 0
	if err := db.Save(ctx, &inv, I.Number); !errors.Is(err, velox.ErrNotFound) {
		t.Errorf("no row: %v", err)
	}
	if err := db.Save(ctx, &inv); err == nil || !strings.Contains(err.Error(), "Save names no columns") {
		t.Errorf("no columns: %v", err)
	}
}

func TestSaveStoresUpdateDefaultOnlyAfterSuccess(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 3, Number: "X", UpdatedAt: t0}

	e.Err = errors.New("boom")
	if err := db.Save(ctx, &inv, I.Number); err == nil {
		t.Fatal("want the driver's error")
	}
	if !inv.UpdatedAt.Equal(t0) {
		t.Errorf("a failed Save changed UpdatedAt to %v", inv.UpdatedAt)
	}

	e.Err, e.Affected = nil, 0
	if err := db.Save(ctx, &inv, I.Number); !errors.Is(err, velox.ErrNotFound) {
		t.Fatalf("no row: %v", err)
	}
	if !inv.UpdatedAt.Equal(t0) {
		t.Errorf("a Save that found no row changed UpdatedAt to %v", inv.UpdatedAt)
	}

	e.Affected = 1
	if err := db.Save(ctx, &inv, I.Number); err != nil {
		t.Fatal(err)
	}
	if inv.UpdatedAt.Equal(t0) || e.Args[1] != inv.UpdatedAt {
		t.Errorf("UpdatedAt %v, sent %v", inv.UpdatedAt, e.Args[1])
	}
}

func TestAColumnAssignedTwiceIsRefused(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	inv := testmodel.Invoice{ID: 3}
	want := "velox: testmodel.Invoice: column number is assigned twice"
	if err := db.Save(ctx, &inv, I.Number, I.Number); err == nil || err.Error() != want || e.Calls != 0 {
		t.Errorf("Save: %v, calls %d", err, e.Calls)
	}
	u := db.Update[testmodel.Invoice]().Set(I.Total.Add(1), I.Total.Set(2)).Where(I.ID.EQ(1))
	want = "velox: testmodel.Invoice: column total is assigned twice"
	if _, err := u.Exec(ctx); err == nil || err.Error() != want || e.Calls != 0 {
		t.Errorf("Exec: %v, calls %d", err, e.Calls)
	}
	if text, _, err := u.SQL(ctx); text != "" || err == nil || err.Error() != want {
		t.Errorf("SQL(ctx): %q %v", text, err)
	}
	// Set called twice is the same thing.
	u = db.Update[testmodel.Invoice]().Set(I.Total.Add(1)).Set(I.Total.Set(2))
	if _, err := u.Exec(ctx); err == nil || err.Error() != want {
		t.Errorf("two Set calls: %v", err)
	}
}

func TestSaveRefusesNil(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	if err := db.Save[testmodel.Invoice](ctx, nil, I.Number); err == nil || err.Error() != "velox: testmodel.Invoice: Save of a nil pointer" {
		t.Errorf("nil pointer: %v", err)
	}
	inv := testmodel.Invoice{ID: 3}
	if err := db.Save(ctx, &inv, I.Number, nil); err == nil || err.Error() != "velox: testmodel.Invoice: Save names a nil column" {
		t.Errorf("nil column: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("calls %d", e.Calls)
	}
}
