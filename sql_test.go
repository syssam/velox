package velox_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// sqlOK returns SQL(ctx)'s text and args, failing t on its error:
// sqlOK(t)(q.SQL(ctx)).
func sqlOK(t *testing.T) func(string, []any, error) (string, []any) {
	return func(text string, args []any, err error) (string, []any) {
		t.Helper()
		if err != nil {
			t.Fatalf("SQL(ctx): %v", err)
		}
		return text, args
	}
}

// annIn7 acts as ann under tenant 7: Ticket's tenant scope, policy and hooks
// all apply.
func annIn7() context.Context {
	return testmodel.AsUser(velox.WithTenant(context.Background(), int64(7)), "ann")
}

// sameAsSent fails t unless SQL(ctx)'s text and args are what e was sent.
func sameAsSent(t *testing.T, what string, e *velox.FakeExec, text string, args []any) {
	t.Helper()
	if text != e.SQL || !reflect.DeepEqual(args, e.Args) {
		t.Errorf("%s: SQL(ctx)\n %s %#v\nsent\n %s %#v", what, text, args, e.SQL, e.Args)
	}
}

// TestSQLIsWhatIsSent (Review Focus 5): for a scoped read, update, delete and
// insert, SQL(ctx) returns exactly what execution then sends, on the path with
// no hooks and on the hooked one.
func TestSQLIsWhatIsSent(t *testing.T) {
	ctx := annIn7()
	extra := func(_ context.Context, st *velox.Statement) error {
		if st.Op == velox.OpSelect {
			st.Where = append(st.Where, velox.ColumnEQ(int(Tk.Title.I), "T"))
		}
		return nil
	}
	for _, c := range []struct {
		name string
		opts []velox.Option
	}{
		{"plain", nil},
		{"hooked", []velox.Option{velox.WithRewrite(extra), velox.WithMaxRows(3)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := &velox.FakeExec{Affected: 1}
			db := velox.OpenFake(velox.Postgres, e, c.opts...)

			q := db.Query[testmodel.Ticket]().Where(Tk.Done.EQ(false)).OrderBy(Tk.ID.Asc()).Limit(5)
			text, args, err := q.SQL(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := q.All(ctx); err != nil {
				t.Fatal(err)
			}
			sameAsSent(t, "read", e, text, args)

			u := db.Update[testmodel.Ticket]().Set(Tk.Title.Set("new")).Where(Tk.ID.EQ(1))
			if text, args, err = u.SQL(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := u.Exec(ctx); err != nil {
				t.Fatal(err)
			}
			sameAsSent(t, "update", e, text, args)

			d := db.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(1))
			if text, args, err = d.SQL(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := d.Exec(ctx); err != nil {
				t.Fatal(err)
			}
			sameAsSent(t, "delete", e, text, args)

			tk := testmodel.Ticket{Owner: "ann", Title: "T-1", InvoiceID: 1}
			ins := db.Insert(&tk)
			if text, args, err = ins.SQL(ctx); err != nil {
				t.Fatal(err)
			}
			if tk.OrgID != 7 || tk.CreatedBy != "ann" {
				t.Errorf("SQL(ctx) did not prepare the row as Exec does: %+v", tk)
			}
			e.Rows = [][]any{{int64(9)}}
			if err := ins.Exec(ctx); err != nil {
				t.Fatal(err)
			}
			sameAsSent(t, "insert", e, text, args)

			tks := []testmodel.Ticket{{Owner: "ann", Title: "T-2", InvoiceID: 1}, {Owner: "ann", Title: "T-3", InvoiceID: 1}}
			all := db.InsertAll(tks).OnConflict(Tk.OrgID, Tk.Title).DoNothing()
			if text, args, err = all.SQL(ctx); err != nil {
				t.Fatal(err)
			}
			if err := all.Exec(ctx); err != nil {
				t.Fatal(err)
			}
			sameAsSent(t, "insert all", e, text, args)
		})
	}
}

// TestSQLNeverSends: SQL(ctx) runs no statement and no Around hook.
func TestSQLNeverSends(t *testing.T) {
	ctx := annIn7()
	arounds := 0
	count := func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		arounds++
		return next(ctx)
	}
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(count))
	tk := testmodel.Ticket{Owner: "ann", Title: "T-1", InvoiceID: 1}
	_, _, err1 := db.Query[testmodel.Ticket]().Limit(1).SQL(ctx)
	_, _, err2 := db.Update[testmodel.Ticket]().Set(Tk.Done.Set(true)).All().SQL(ctx)
	_, _, err3 := db.Delete[testmodel.Ticket]().All().SQL(ctx)
	_, _, err4 := db.Insert(&tk).SQL(ctx)
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		t.Error(err)
	}
	if e.Calls != 0 || len(e.Log) != 0 || arounds != 0 {
		t.Errorf("SQL(ctx) sent %d statements %v, and called Around %d times", e.Calls, e.Log, arounds)
	}
}

// TestSQLReportsTheRefusal: SQL(ctx) returns the error the statement would
// fail with, and no text.
func TestSQLReportsTheRefusal(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	text, args, err := db.Query[testmodel.Ticket]().SQL(annIn7())
	if !errors.Is(err, velox.ErrUnbounded) || text != "" || args != nil {
		t.Errorf("unbounded: %q %v %v", text, args, err)
	}
	text, _, err = db.Query[testmodel.Ticket]().Limit(1).SQL(testmodel.AsUser(context.Background(), "ann"))
	if !errors.Is(err, velox.ErrNoTenant) || text != "" {
		t.Errorf("no tenant: %q %v", text, err)
	}
	if _, _, err = db.Update[testmodel.Ticket]().Set(Tk.Title.Set("")).Where(Tk.ID.EQ(1)).SQL(annIn7()); err == nil || err.Error() != "velox: testmodel.Ticket: before update: a ticket needs a title" {
		t.Errorf("BeforeUpdate: %v", err)
	}
	tk := testmodel.Ticket{Owner: "bob", Title: "T-1", InvoiceID: 1}
	if _, _, err = db.Insert(&tk).SQL(annIn7()); !errors.Is(err, velox.ErrDenied) {
		t.Errorf("insert policy: %v", err)
	}
	if _, _, err = db.Insert[testmodel.Ticket](nil).SQL(annIn7()); err == nil {
		t.Error("insert of nil: no error")
	}
}

// TestSQLHooksSeeDryRun: a Rewrite hook sees DryRun under SQL(ctx), and not
// when the statement is sent.
func TestSQLHooksSeeDryRun(t *testing.T) {
	var seen []bool
	see := func(_ context.Context, st *velox.Statement) error {
		seen = append(seen, st.DryRun)
		return nil
	}
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithRewrite(see))
	q := db.Query[testmodel.Ticket]().Limit(1)
	if _, _, err := q.SQL(annIn7()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.All(annIn7()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seen, []bool{true, false}) {
		t.Errorf("DryRun seen %v, want [true false]", seen)
	}
}

// TestSQLOfASplitInsertAll: an InsertAll that needs several statements prints
// the first, as Exec sends it: in a transaction.
func TestSQLOfASplitInsertAll(t *testing.T) {
	ctx := annIn7()
	var inTx []bool
	see := func(_ context.Context, st *velox.Statement) error {
		inTx = append(inTx, st.InTx)
		return nil
	}
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.SQLite, e, velox.WithRewrite(see))
	const per = 32766 / 7 // SQLite's parameters, over the columns but the generated key
	tks := make([]testmodel.Ticket, per+1)
	for i := range tks {
		tks[i] = testmodel.Ticket{Owner: "ann", Title: fmt.Sprintf("T-%d", i), InvoiceID: 1}
	}
	text, args, err := db.InsertAll(tks).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != per*7 {
		t.Errorf("%d args, want the first statement's %d", len(args), per*7)
	}
	if err := db.InsertAll(tks).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.Log) < 2 || e.Log[0] != "BEGIN" || e.Log[1] != text {
		t.Errorf("the first statement sent is not SQL(ctx)'s")
	}
	if len(inTx) == 0 || !inTx[0] {
		t.Errorf("under SQL(ctx), InTx %v; Exec sends the statement in a transaction", inTx)
	}

	text, args, err = db.InsertAll([]testmodel.Ticket{}).SQL(ctx)
	if text != "" || args != nil || err != nil {
		t.Errorf("empty InsertAll: %q %v %v", text, args, err)
	}
}
