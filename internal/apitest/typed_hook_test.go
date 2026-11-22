package apitest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestTypedHookInsert: a hook reads and writes an insert's columns through
// the handles, by type: Fill fills a zero column, Assign sets every row,
// and Get and Put read and set one row; a nullable column's GetPtr tells
// NULL apart and PutNull writes it.
func TestTypedHookInsert(t *testing.T) {
	var got []string
	var paid []*time.Time
	hook := velox.Before(func(_ context.Context, m *velox.Mutation) error {
		if err := I.Note.Fill(m, "filled"); err != nil {
			return err
		}
		if err := I.Total.Assign(m, 9.5); err != nil {
			return err
		}
		for r := range m.Rows() {
			got = append(got, I.Number.Get(r))
			paid = append(paid, I.PaidAt.GetPtr(r))
			if err := I.Number.Put(r, "N-"+I.Number.Get(r)); err != nil {
				return err
			}
			if I.Number.Get(r) == "N-b" {
				if err := I.PaidAt.PutNull(r); err != nil {
					return err
				}
			}
		}
		return nil
	}, velox.OpInsert)
	e := &fakeconn.Exec{Affected: 2}
	db := openFake(velox.Postgres, e, velox.WithHooks[testmodel.Invoice](hook))
	rows := []testmodel.Invoice{{OrgID: 1, Number: "a", Note: "kept", PaidAt: &t0}, {OrgID: 1, Number: "b", PaidAt: &t0}}
	if err := db.InsertAll(rows).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "a,b" || paid[0] == nil || !paid[0].Equal(t0) {
		t.Errorf("Get read %q, GetPtr %v", got, paid)
	}
	if rows[0].Number != "N-a" || rows[1].Number != "N-b" || rows[0].Note != "kept" || rows[1].Note != "filled" ||
		rows[0].Total != 9.5 || rows[1].Total != 9.5 || rows[0].PaidAt == nil || rows[1].PaidAt != nil {
		t.Errorf("rows %+v", rows)
	}
}

// TestTypedHookUpdate: a hook reads an update's assignment with Value and
// ValuePtr, and replaces it with Assign and AssignNull.
func TestTypedHookUpdate(t *testing.T) {
	type seen struct {
		total     float64
		ok, unset bool
		paid      *time.Time
		paidOK    bool
	}
	var s seen
	hook := velox.Before(func(_ context.Context, m *velox.Mutation) error {
		s.total, s.ok = I.Total.Value(m)
		_, assigned := I.Number.Value(m)
		s.unset = !assigned
		if err := I.Total.Assign(m, s.total*2); err != nil {
			return err
		}
		if err := I.PaidAt.AssignNull(m); err != nil {
			return err
		}
		s.paid, s.paidOK = I.PaidAt.ValuePtr(m)
		return nil
	}, velox.OpUpdate)
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e, velox.WithHooks[testmodel.Invoice](hook))
	if _, err := db.Update[testmodel.Invoice]().Set(I.Total.Set(2)).Where(I.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if s.total != 2 || !s.ok || !s.unset || s.paid != nil || !s.paidOK {
		t.Errorf("the hook saw %+v", s)
	}
	if want := `UPDATE "invoices" SET "total" = $1, "paid_at" = NULL, "updated_at" = $2 WHERE "id" = $3`; !strings.HasPrefix(e.SQL, `UPDATE "invoices" SET "total" = $1`) || e.Args[0] != 4.0 {
		t.Errorf("\n got %s %v\nwant %s", e.SQL, e.Args, want)
	}
}

// TestTypedHookMisuse: a handle of another entity, Fill on an update and
// Value on an insert are misuses: the write fails, naming them, and nothing
// is sent.
func TestTypedHookMisuse(t *testing.T) {
	for name, c := range map[string]struct {
		op   velox.Op
		f    func(m *velox.Mutation) error
		want string
	}{
		"Assign of another entity": {velox.OpInsert, func(m *velox.Mutation) error {
			_ = testmodel.Items.SKU.Assign(m, "x")
			return nil
		}, "Assign: a column of testmodel.Item, on a mutation of testmodel.Invoice"},
		"Value of another entity": {velox.OpUpdate, func(m *velox.Mutation) error {
			_, _ = testmodel.Items.SKU.Value(m)
			return nil
		}, "Value: a column of testmodel.Item"},
		"Put on a row of another entity": {velox.OpInsert, func(m *velox.Mutation) error {
			for r := range m.Rows() {
				_ = testmodel.Items.SKU.Put(r, "x")
			}
			return nil
		}, "Put: a column of testmodel.Item, on a row of testmodel.Invoice"},
		"Fill on an update": {velox.OpUpdate, func(m *velox.Mutation) error {
			return I.Note.Fill(m, "x")
		}, "Fill(note) on an update"},
		"Value on an insert": {velox.OpInsert, func(m *velox.Mutation) error {
			_, _ = I.Note.Value(m)
			return nil
		}, "Value(note) on an insert"},
		"a forged handle": {velox.OpUpdate, func(m *velox.Mutation) error {
			return velox.StringCol[testmodel.Invoice, string]{I: 42}.Assign(m, "x")
		}, "Assign: no column 42"},
	} {
		t.Run(name, func(t *testing.T) {
			e := &fakeconn.Exec{Affected: 1}
			db := openFake(velox.Postgres, e, velox.WithHooks[testmodel.Invoice](velox.Before(func(_ context.Context, m *velox.Mutation) error { return c.f(m) }, c.op)))
			var err error
			if c.op == velox.OpInsert {
				err = db.Insert(&testmodel.Invoice{OrgID: 1, Number: "a"}).Exec(ctx)
			} else {
				_, err = db.Update[testmodel.Invoice]().Set(I.Total.Set(1)).Where(I.ID.EQ(1)).Exec(ctx)
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%v, want %q", err, c.want)
			}
			if e.Calls != 0 {
				t.Errorf("%d statements sent", e.Calls)
			}
		})
	}
}
