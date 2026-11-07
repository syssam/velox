package apitest

import (
	"fmt"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestTableRefusesMismatches: the untyped entry points refuse a condition,
// an assignment, rows or a column index of another entity or out of range,
// with an error and no statement sent: never a panic, and never a
// statement on the wrong column.
func TestTableRefusesMismatches(t *testing.T) {
	ctx := annIn7()
	exec := &fakeconn.Exec{}
	db := openFake(velox.Postgres, exec)
	run := func(name string, f func() error) {
		t.Helper()
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic: %v", r)
					t.Errorf("%s: %v", name, err)
				}
			}()
			err = f()
		}()
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	sql := func(_ string, _ []any, err error) error { return err }
	other := tkTable.Pred(col(Tk.Title), velox.PredEQ, "t") // a Ticket condition
	run("a Ticket condition on an Invoice query", func() error { return sql(db.QueryTable(invTable).Where(other).Limit(1).SQL(ctx)) })
	run("a Ticket condition in an Invoice update", func() error {
		return sql(db.UpdateTable(invTable).Set(col(I.Number), "n").Where(other).SQL(ctx))
	})
	run("a Ticket condition in an Invoice delete", func() error { return sql(db.DeleteTable(invTable).Where(other).SQL(ctx)) })
	run("a Ticket condition under And", func() error {
		return sql(db.QueryTable(invTable).Where(velox.AndPredicates(other)).Limit(1).SQL(ctx))
	})
	run("a Ticket condition under Not, in an EXISTS on items", func() error {
		return sql(db.QueryTable(invTable).Where(invTable.Has(int(I.Items.I), itTable, velox.NotPredicate(other))).Limit(1).SQL(ctx))
	})
	run("a Ticket Has on an Invoice query", func() error {
		return sql(db.QueryTable(invTable).Where(tkTable.Has(int(Tk.Invoice.I), invTable)).Limit(1).SQL(ctx))
	})
	// An Assignment carries no entity: one of another entity's column is
	// refused when its value is not of this column's Go type.
	run("an assignment of another Go type", func() error {
		return sql(db.UpdateTable(invTable).Assign(velox.Assignment{Column: col(I.Number), Op: velox.AssignSet, Value: 3}).All().SQL(ctx))
	})
	run("an assignment out of range", func() error {
		return sql(db.UpdateTable(invTable).Assign(velox.Assignment{Column: 999, Op: velox.AssignSet, Value: "x"}).All().SQL(ctx))
	})
	run("ValidateSet out of range", func() error {
		return invTable.ValidateSet(velox.Assignment{Column: 999, Op: velox.AssignSet, Value: "x"})
	})
	run("Validate out of range", func() error { return invTable.Validate(invTable.New(), 999) })
	run("Set out of range", func() error { return sql(db.UpdateTable(invTable).Set(999, "x").All().SQL(ctx)) })
	run("SetField out of range", func() error { return invTable.SetField(invTable.New(), 999, "x") })
	run("Columns out of range", func() error { return sql(db.QueryTable(invTable).Columns(999).Limit(1).SQL(ctx)) })
	run("OrderBy out of range", func() error {
		return sql(db.QueryTable(invTable).OrderBy(velox.OrderTerm{Column: 999}).Limit(1).SQL(ctx))
	})
	run("OnConflict out of range", func() error {
		x := testmodel.Invoice{Number: "n"}
		return sql(db.InsertTable(invTable, &x).OnConflict(999).DoNothing().SQL(ctx))
	})
	run("Ticket rows into Invoice", func() error {
		return sql(db.InsertRows(invTable, tkTable.NewRows(2)).SQL(ctx))
	})
	if rs := velox.RowsOf[testmodel.Invoice](tkTable.NewRows(2)); rs != nil {
		t.Errorf("RowsOf Invoice of Ticket rows: %d rows", len(rs))
	}
	if len(exec.Log) != 0 {
		t.Errorf("sent %v", exec.Log)
	}
}
