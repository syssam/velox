package apitest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

// TestExplicitSetWinsOverUpdateDefault records what velox did before
// KeepDefaults: an update that assigns an UpdateDefault column writes the
// value it assigns, and the UpdateDefault does not fire. So Set(col) of the
// current value already kept a column; KeepDefaults writes nothing to it.
func TestExplicitSetWinsOverUpdateDefault(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	if _, err := db.Update[testmodel.Invoice]().Set(I.Total.Set(1), I.UpdatedAt.Set(t0)).Where(I.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "invoices" SET "total" = $1, "updated_at" = $2 WHERE "id" = $3`; e.SQL != want || e.Args[1] != t0 {
		t.Errorf("\n got %s %v\nwant %s", e.SQL, e.Args, want)
	}
}

// TestKeepDefaults: the named columns' UpdateDefault does not fire on an
// update, a One or a Save, and nothing is written to them; a column the
// update assigns is still written. A column without an UpdateDefault fails
// the statement, naming it, before anything is sent.
func TestKeepDefaults(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	if _, err := db.Update[testmodel.Invoice]().Set(I.Total.Set(1)).KeepDefaults(I.UpdatedAt).Where(I.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "invoices" SET "total" = $1 WHERE "id" = $2`; e.SQL != want {
		t.Errorf("Exec:\n got %s\nwant %s", e.SQL, want)
	}
	q, _, err := db.Update[testmodel.Invoice]().Set(I.Total.Set(1)).KeepDefaults(I.UpdatedAt).Where(I.ID.EQ(1)).SQL(ctx)
	if want := `UPDATE "invoices" SET "total" = $1 WHERE "id" = $2`; err != nil || q != want {
		t.Errorf("SQL: %s, %v", q, err)
	}
	if _, err := db.Update[testmodel.Invoice]().Set(I.Total.Set(1), I.UpdatedAt.Set(t0)).KeepDefaults(I.UpdatedAt).Where(I.ID.EQ(1)).Exec(ctx); err != nil || !strings.Contains(e.SQL, `"updated_at" = $2`) {
		t.Errorf("an assigned column is written: %s, %v", e.SQL, err)
	}

	calls := e.Calls
	_, err = db.Update[testmodel.Invoice]().Set(I.Total.Set(1)).KeepDefaults(I.Note).Where(I.ID.EQ(1)).Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "Note") || !strings.Contains(err.Error(), "UpdateDefault") || e.Calls != calls {
		t.Errorf("a column without an UpdateDefault: %v, %d calls", err, e.Calls-calls)
	}
	if _, _, err := db.Update[testmodel.Invoice]().Set(I.Total.Set(1)).KeepDefaults(I.Note).Where(I.ID.EQ(1)).SQL(ctx); err == nil {
		t.Error("SQL of a column without an UpdateDefault: no error")
	}

	if _, err := db.Update[testmodel.Invoice]().Set(I.Total.Set(1)).KeepDefaults(nil).Where(I.ID.EQ(1)).Exec(ctx); err == nil || !strings.Contains(err.Error(), "nil column") || e.Calls != calls {
		t.Errorf("a nil column: %v", err)
	}

	inv := testmodel.Invoice{ID: 3, Number: "X", UpdatedAt: t0}
	if err := db.Save(&inv, I.Number).KeepDefaults(I.UpdatedAt).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "invoices" SET "number" = $1 WHERE "id" = $2`; e.SQL != want || !inv.UpdatedAt.Equal(t0) {
		t.Errorf("Save:\n got %s, UpdatedAt %v\nwant %s", e.SQL, inv.UpdatedAt, want)
	}
	if err := db.Save(&inv, I.Number).KeepDefaults(I.Total).Exec(ctx); err == nil || !strings.Contains(err.Error(), "Total") {
		t.Errorf("Save of a column without an UpdateDefault: %v", err)
	}
}

// TestKeepDefaultsOnUpdateOne: One by key sends the UPDATE without the
// column.
func TestKeepDefaultsOnUpdateOne(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1, Rows: [][]any{invoiceRow(1)}}
	db := openFake(velox.Postgres, e)
	if _, err := db.Update[testmodel.Invoice]().Set(I.Total.Set(1)).KeepDefaults(I.UpdatedAt).Where(I.ID.EQ(1)).One(ctx); err != nil {
		t.Fatal(err)
	}
	updates := 0
	for _, q := range e.Log {
		if strings.HasPrefix(q, "UPDATE") {
			updates++
			if strings.Contains(q, "updated_at") {
				t.Errorf("One wrote updated_at: %s", q)
			}
		}
	}
	if updates != 1 {
		t.Errorf("%d UPDATEs in %v", updates, e.Log)
	}
}

// TestKeepDefaultsUntyped: TableUpdate.KeepDefaults takes column indexes.
func TestKeepDefaultsUntyped(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	tbl := untyped.TableOf[testmodel.Invoice]()
	if _, err := untyped.Update(db, tbl).Set(col(I.Total), 1.0).KeepDefaults(col(I.UpdatedAt)).Where(velox.Predicate(I.ID.EQ(1))).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "invoices" SET "total" = $1 WHERE "id" = $2`; e.SQL != want {
		t.Errorf("\n got %s\nwant %s", e.SQL, want)
	}
}

// TestSetPtrOnUpdate: a nil pointer's setter is skipped, a non-nil one
// sets the value, on a Null kind too; SetNull still writes NULL. An update
// whose every setter is skipped has no Set.
func TestSetPtrOnUpdate(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	var none *float64
	total, number := 2.0, "N"
	if _, err := db.Update[testmodel.Invoice]().Set(I.Total.SetPtr(none), I.Number.SetPtr(&number), I.PaidAt.SetPtr(nil)).KeepDefaults(I.UpdatedAt).Where(I.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "invoices" SET "number" = $1 WHERE "id" = $2`; e.SQL != want || e.Args[0] != "N" {
		t.Errorf("\n got %s %v\nwant %s", e.SQL, e.Args, want)
	}
	if _, err := db.Update[testmodel.Invoice]().Set(I.Total.SetPtr(&total), I.PaidAt.SetNull()).KeepDefaults(I.UpdatedAt).Where(I.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "invoices" SET "total" = $1, "paid_at" = $2 WHERE "id" = $3`; e.SQL != want || e.Args[0] != 2.0 || e.Args[1] != nil {
		t.Errorf("\n got %s %v\nwant %s", e.SQL, e.Args, want)
	}
	calls := e.Calls
	if _, err := db.Update[testmodel.Invoice]().Set(I.Total.SetPtr(nil)).Where(I.ID.EQ(1)).Exec(ctx); err == nil || !strings.Contains(err.Error(), "Update has no Set") || e.Calls != calls {
		t.Errorf("every setter skipped: %v, %d calls", err, e.Calls-calls)
	}
	e.Results = [][][]any{{{int64(1)}}, {invoiceRow(1)}}
	logged := len(e.Log)
	got, err := db.Update[testmodel.Invoice]().Set(I.Total.SetPtr(nil)).Where(I.ID.EQ(1)).One(ctx)
	if err != nil || got.ID != 1 {
		t.Errorf("One with every setter skipped: %+v, %v", got, err)
	}
	for _, q := range e.Log[logged:] {
		if strings.HasPrefix(q, "UPDATE") {
			t.Errorf("One with every setter skipped sent %s", q)
		}
	}
	e.Results = nil
	tbl := untyped.TableOf[testmodel.Invoice]()
	set := []velox.Setter[testmodel.Invoice]{I.Total.SetPtr(nil), I.Number.SetPtr(&number)}
	if _, err := untyped.Update(db, tbl).Assign(untyped.Assignments(set)...).KeepDefaults(col(I.UpdatedAt)).Where(velox.Predicate(I.ID.EQ(1))).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "invoices" SET "number" = $1 WHERE "id" = $2`; e.SQL != want {
		t.Errorf("untyped:\n got %s\nwant %s", e.SQL, want)
	}
}

// TestSetPtrOnInsert: a nil pointer's setter leaves the field as it is; a
// non-nil one stores its value over the field.
func TestSetPtrOnInsert(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}}
	db := openFake(velox.Postgres, e)
	number := "SET"
	inv := testmodel.Invoice{OrgID: 1, Number: "KEPT", Total: 3}
	if err := db.Insert(&inv).Set(I.Total.SetPtr(nil), I.Note.SetPtr(nil)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if inv.Number != "KEPT" || inv.Total != 3 || inv.Note != "" {
		t.Errorf("a skipped setter changed the entity: %+v", inv)
	}
	inv = testmodel.Invoice{OrgID: 1, Number: "KEPT"}
	if err := db.Insert(&inv).Set(I.Number.SetPtr(&number)).Exec(ctx); err != nil || inv.Number != "SET" {
		t.Errorf("%v, Number %q", err, inv.Number)
	}
}

// TestSetPtrLeavesTheDatabaseDefault: a nil SetPtr on an insert leaves a
// zero field whose column has a declared database default to the database,
// whose value is read back, and the field's validators do not run on it
// (ADR 0093); a non-nil one is validated.
func TestSetPtrLeavesTheDatabaseDefault(t *testing.T) {
	db, _ := enumDB(t)
	a := dEnum{Name: "a"}
	if err := db.Insert(&a).Set(dEnums.Status.SetPtr(nil)).Exec(ctx); err != nil || a.Status != "new" {
		t.Fatalf("%v, status %q", err, a.Status)
	}
	bogus := "bogus"
	if err := db.Insert(&dEnum{Name: "b"}).Set(dEnums.Status.SetPtr(&bogus)).Exec(ctx); !velox.IsValidationError(err) {
		t.Errorf("a value is validated: %v", err)
	}
}

// TestSetPtrOnUpsert: a column of the update branch whose setter is skipped
// is not updated; with none left, the upsert keeps the existing row.
func TestSetPtrOnUpsert(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}}
	db := openFake(velox.Postgres, e)
	inv := testmodel.Invoice{OrgID: 1, Number: "N", Total: 3}
	if err := db.Insert(&inv).Set(I.Note.SetPtr(nil)).OnConflict(I.OrgID, I.Number).Update(I.Total, I.Note).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `ON CONFLICT ("org_id", "number") DO UPDATE SET "total" = EXCLUDED."total" RETURNING "id"`; !strings.HasSuffix(e.SQL, want) {
		t.Errorf("\n got %s\nwant suffix %s", e.SQL, want)
	}
	note := "n"
	if err := db.Insert(&inv).Set(I.Note.SetPtr(nil), I.Note.SetPtr(&note)).OnConflict(I.OrgID, I.Number).Update(I.Note).Exec(ctx); err != nil || !strings.Contains(e.SQL, `"note" = EXCLUDED."note"`) {
		t.Errorf("a column another setter sets is updated: %s, %v", e.SQL, err)
	}
	q, _, err := db.Insert(&inv).Set(I.Note.SetPtr(nil)).OnConflict(I.OrgID, I.Number).Update(I.Note).SQL(ctx)
	if err != nil || !strings.Contains(q, "DO NOTHING") {
		t.Errorf("no column left: %s, %v", q, err)
	}
}

// TestErrorHelpers: IsConstraintError, IsValidationError and IsNotSingular
// find velox's errors through wrapping, and are false for nil, another
// error and a typed nil.
func TestErrorHelpers(t *testing.T) {
	ce := &velox.ConstraintError{Entity: "testmodel.Invoice", Kind: velox.ConstraintUnique, Err: errors.New("dup")}
	ve := &velox.ValidationError{Entity: "testmodel.Invoice", Field: "Note", Err: errors.New("empty")}
	ns := fmt.Errorf("velox: testmodel.Invoice: %w", velox.ErrNotSingular)
	var nilCE *velox.ConstraintError
	var nilVE *velox.ValidationError
	for _, c := range []struct {
		name                    string
		err                     error
		constraint, valid, sing bool
	}{
		{"nil", nil, false, false, false},
		{"other", errors.New("x"), false, false, false},
		{"constraint", fmt.Errorf("w: %w", ce), true, false, false},
		{"validation", fmt.Errorf("w: %w", ve), false, true, false},
		{"not singular", fmt.Errorf("w: %w", ns), false, false, true},
		{"not found", velox.ErrNotFound, false, false, false},
		{"typed nil constraint", fmt.Errorf("w: %w", nilCE), false, false, false},
		{"typed nil validation", fmt.Errorf("w: %w", nilVE), false, false, false},
		{"joined", errors.Join(errors.New("a"), ce), true, false, false},
	} {
		if got := velox.IsConstraintError(c.err); got != c.constraint {
			t.Errorf("%s: IsConstraintError = %v", c.name, got)
		}
		if got := velox.IsValidationError(c.err); got != c.valid {
			t.Errorf("%s: IsValidationError = %v", c.name, got)
		}
		if got := velox.IsNotSingular(c.err); got != c.sing {
			t.Errorf("%s: IsNotSingular = %v", c.name, got)
		}
	}
}

// TestErrorHelpersOnVeloxErrors: the helpers are true for what velox
// returns: a validator's refusal and a One that found two rows.
func TestErrorHelpersOnVeloxErrors(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	tctx := testmodel.Org.With(context.Background(), int64(7))
	bad := "no at sign"
	if err := db.Insert(&fixture.DAuthor{Name: "mallory", Email: &bad}).Exec(tctx); !velox.IsValidationError(err) {
		t.Errorf("validator: %v", err)
	}
	e.Rows = [][]any{invoiceRow(1), invoiceRow(2)}
	if _, err := db.Query[testmodel.Invoice]().One(ctx); !velox.IsNotSingular(err) {
		t.Errorf("One: %v", err)
	}
}
