package apitest

import (
	"context"
	"errors"
	"testing"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// passAround is an Around hook that only runs the statement, so that the
// DB runs every statement through its hooked path.
func passAround(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
	return next(ctx)
}

// TestTableQueryDriverError: each way of running an untyped query returns
// the driver's error, on the plain path and through an Around hook.
func TestTableQueryDriverError(t *testing.T) {
	boom := errors.New("boom")
	for name, opts := range map[string][]velox.Option{"plain": nil, "hooked": {velox.WithAround(passAround)}} {
		db := openFake(velox.Postgres, &fakeconn.Exec{Err: boom}, opts...)
		q := db.QueryTable(itTable).Limit(5)
		each := db.QueryTable(itTable)
		runs := map[string]func() error{
			"All":   func() error { _, err := q.All(ctx); return err },
			"One":   func() error { _, err := q.One(ctx); return err },
			"First": func() error { _, err := q.First(ctx); return err },
			"Count": func() error { _, err := q.Count(ctx); return err },
			"Exists": func() error {
				_, err := q.Exists(ctx)
				return err
			},
			"LoadEach": func() error {
				_, err := each.LoadEach(ctx, invTable, int(I.Items.I), 0, []unsafe.Pointer{unsafe.Pointer(&testmodel.Invoice{ID: 1})})
				return err
			},
			"CountEach": func() error {
				_, err := each.CountEach(ctx, invTable, int(I.Items.I), []unsafe.Pointer{unsafe.Pointer(&testmodel.Invoice{ID: 1})})
				return err
			},
			"InsertTable": func() error {
				x := testmodel.Item{InvoiceID: 1, SKU: "s"}
				return db.InsertTable(itTable, unsafe.Pointer(&x)).Exec(ctx)
			},
			"InsertRows": func() error {
				rows := itTable.NewRows(2)
				for i := range 2 {
					(*testmodel.Item)(rows.At(i)).SKU = "s"
				}
				return db.InsertRows(itTable, rows).Exec(ctx)
			},
		}
		for run, f := range runs {
			if err := f(); !errors.Is(err, boom) {
				t.Errorf("%s %s: %v, want the driver's error", name, run, err)
			}
		}
	}
}

// TestTableQueryRefusals: a query of the zero Table, or one a builder
// failed, fails each way of running it before anything is sent; a join or
// an After the query cannot take fails it.
func TestTableQueryRefusals(t *testing.T) {
	exec := &fakeconn.Exec{}
	db := openFake(velox.Postgres, exec)
	inv := []unsafe.Pointer{unsafe.Pointer(&testmodel.Invoice{ID: 1})}
	runAll := func(name string, q velox.TableQuery) {
		t.Helper()
		for run, f := range map[string]func() error{
			"All":       func() error { _, err := q.All(ctx); return err },
			"One":       func() error { _, err := q.One(ctx); return err },
			"First":     func() error { _, err := q.First(ctx); return err },
			"Count":     func() error { _, err := q.Count(ctx); return err },
			"Exists":    func() error { _, err := q.Exists(ctx); return err },
			"SQL":       func() error { _, _, err := q.SQL(ctx); return err },
			"LoadEach":  func() error { _, err := q.LoadEach(ctx, invTable, int(I.Items.I), 0, inv); return err },
			"CountEach": func() error { _, err := q.CountEach(ctx, invTable, int(I.Items.I), inv); return err },
		} {
			if err := f(); err == nil {
				t.Errorf("%s: %s: no error", name, run)
			}
		}
	}
	zero := db.QueryTable(velox.Table{}).Limit(1)
	runAll("the zero Table", zero)
	runAll("the zero Table, after After", zero.After(unsafe.Pointer(&testmodel.Item{})))
	runAll("the zero Table, after Join", zero.Join(int(It.Invoice.I), invTable))
	q := db.QueryTable(itTable).Limit(1)
	runAll("a column out of range", q.Columns(99))
	runAll("After of nil", q.After(nil))
	runAll("Join of a to-many relation", db.QueryTable(invTable).Limit(1).Join(int(I.Items.I), itTable))
	runAll("Join twice", q.Join(int(It.Invoice.I), invTable).LeftJoin(int(It.Invoice.I), invTable))
	runAll("Join of the zero Table", q.Join(int(It.Invoice.I), velox.Table{}))
	runAll("Join of no relation", q.Join(9, invTable))
	runAll("Join of another target", q.Join(int(It.Invoice.I), tkTable))
	// A locking read loads no relations, and a relation load needs the
	// owner's key selected: the reads of rows refuse them.
	for name, q := range map[string]velox.TableQuery{
		"a locking read with a load": velox.Untyped(db.Query[testmodel.Invoice]().With(I.Items).ForUpdate().Limit(1)),
		"a load without the key":     velox.Untyped(db.Query[testmodel.Invoice]().With(I.Items).Columns(I.Number).Limit(1)),
	} {
		for run, f := range map[string]func() error{
			"All":   func() error { _, err := q.All(ctx); return err },
			"One":   func() error { _, err := q.One(ctx); return err },
			"First": func() error { _, err := q.First(ctx); return err },
			"SQL":   func() error { _, _, err := q.SQL(ctx); return err },
		} {
			if err := f(); err == nil {
				t.Errorf("%s: %s: no error", name, run)
			}
		}
	}
	if exec.Calls != 0 {
		t.Errorf("sent %d statements: %s", exec.Calls, exec.SQL)
	}
}

// TestTableQueryBuilders: Columns, Selects, Ordered, Writable and ForShare
// do what Query's do, to the SQL.
func TestTableQueryBuilders(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	q := db.QueryTable(itTable).Limit(1)
	if !q.Selects(col(It.SKU)) || q.Ordered() {
		t.Error("a new query selects every column and has no order")
	}
	nq := q.Columns(col(It.ID), col(It.SKU))
	if !nq.Selects(col(It.SKU)) || nq.Selects(col(It.Qty)) {
		t.Error("Selects of a narrowed query")
	}
	if !nq.Columns().Selects(col(It.Qty)) {
		t.Error("Columns() selects every column again")
	}
	same := func(name string, typed velox.Query[testmodel.Item], untyped velox.TableQuery) {
		t.Helper()
		ws, wa, werr := typed.SQL(ctx)
		gs, ga, gerr := untyped.SQL(ctx)
		if werr != nil || gerr != nil || ws != gs || len(wa) != len(ga) {
			t.Errorf("%s: %q %v %v, want %q %v %v", name, gs, ga, gerr, ws, wa, werr)
		}
	}
	same("Offset MaxRows", db.Query[testmodel.Item]().Limit(1).Offset(2).MaxRows(3), q.Offset(2).MaxRows(3))
	// A locking read runs only inside a transaction.
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		tq, q := tx.Query[testmodel.Item]().Limit(1), tx.QueryTable(itTable).Limit(1)
		same("ForShare", tq.ForShare(), q.ForShare())
		same("ForUpdate Writable", tq.ForUpdate().Writable(), q.ForUpdate().Writable())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestTableQueryLoads: an untyped query of a typed one with a relation
// load loads it, through All, One, First and SQL.
func TestTableQueryLoads(t *testing.T) {
	db, _ := eachDB(t)
	tq := db.Query[testmodel.Invoice]().With(I.Items).OrderBy(I.ID.Asc()).Limit(1)
	want, err := tq.First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantItems, _ := want.Edges.Items.Get()
	if len(wantItems) == 0 {
		t.Fatal("the first invoice has no items: the case is not covered")
	}
	q := velox.Untyped(tq)
	check := func(name string, p unsafe.Pointer, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		items, ok := (*testmodel.Invoice)(p).Edges.Items.Get()
		if !ok || len(items) != len(wantItems) {
			t.Errorf("%s: %d items %v, want %d", name, len(items), ok, len(wantItems))
		}
	}
	p, err := q.First(ctx)
	check("First", p, err)
	p, err = q.Where(invTable.Pred(col(I.ID), velox.PredEQ, want.ID)).One(ctx)
	check("One", p, err)
	rows, err := q.All(ctx)
	if err != nil || rows.Len() != 1 {
		t.Fatalf("All: %d %v", rows.Len(), err)
	}
	check("All", rows.At(0), nil)
	if s, _, err := q.SQL(ctx); err != nil || s == "" {
		t.Errorf("SQL: %q %v", s, err)
	}
	if _, err := q.Where(invTable.Pred(col(I.ID), velox.PredEQ, int64(-1))).First(ctx); !velox.IsNotFound(err) {
		t.Errorf("First of none: %v", err)
	}
}

// TestTableOfAndFill: Of is Rel.Of, untyped; Fill and Filled refuse what
// is not the relation's target, and the zero Table.
func TestTableOfAndFill(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	owners := []*testmodel.Invoice{{ID: 1}, {ID: 2}}
	ws, wa, err := db.Query[testmodel.Item]().Where(I.Items.Of(owners...)).Limit(5).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gs, ga, err := db.QueryTable(itTable).Where(invTable.Of(int(I.Items.I), itTable, unsafe.Pointer(owners[0]), unsafe.Pointer(owners[1]))).Limit(5).SQL(ctx)
	if err != nil || gs != ws || len(ga) != len(wa) {
		t.Errorf("Of: %q %v %v, want %q %v", gs, ga, err, ws, wa)
	}
	for name, p := range map[string]velox.Predicate{
		"Of of the zero Table":  velox.Table{}.Of(0, itTable),
		"Of to the zero Table":  invTable.Of(int(I.Items.I), velox.Table{}),
		"Has of the zero Table": velox.Table{}.Has(0, itTable),
		"Has of a bad condition": invTable.Has(int(I.Items.I), itTable,
			itTable.Pred(col(It.SKU), velox.PredEQ, 3)),
	} {
		if _, _, err := db.QueryTable(itTable).Where(p).Limit(1).SQL(ctx); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	it, inv := new(testmodel.Item), new(testmodel.Invoice)
	edge := int(It.Invoice.I)
	for name, err := range map[string]error{
		"Fill of the zero Table": velox.Table{}.Fill(unsafe.Pointer(it), edge, invTable, unsafe.Pointer(inv)),
		"Fill to the zero Table": itTable.Fill(unsafe.Pointer(it), edge, velox.Table{}, unsafe.Pointer(inv)),
		"Fill of another target": itTable.Fill(unsafe.Pointer(it), edge, tkTable, unsafe.Pointer(inv)),
		"Fill of no relation":    itTable.Fill(unsafe.Pointer(it), 9, invTable, unsafe.Pointer(inv)),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	for name, ok := range map[string]bool{
		"Filled of the zero Table": func() bool { _, ok := velox.Table{}.Filled(unsafe.Pointer(it), edge, invTable); return ok }(),
		"Filled of no relation":    func() bool { _, ok := itTable.Filled(unsafe.Pointer(it), 9, invTable); return ok }(),
	} {
		if ok {
			t.Errorf("%s: holds a row", name)
		}
	}
}

// TestTableWriteEdges: the untyped writes' refusals and empty cases, and an
// insert of an entity without hooks, which takes the plain path, renders
// what the typed insert renders.
func TestTableWriteEdges(t *testing.T) {
	exec := &fakeconn.Exec{}
	db := openFake(velox.Postgres, exec)
	if r := (velox.Table{}).NewRows(2); r.Len() != 0 {
		t.Errorf("NewRows of the zero Table: %d rows", r.Len())
	}
	if err := (velox.Table{}).SetField(unsafe.Pointer(new(testmodel.Item)), 0, int64(1)); err == nil {
		t.Error("SetField of the zero Table: no error")
	}
	tk := testmodel.Ticket{Owner: "ann", Title: "t", InvoiceID: 3}
	if err := tkTable.SetField(unsafe.Pointer(&tk), col(Tk.Title), nil); err == nil {
		t.Error("SetField nil of a NOT NULL column: no error")
	}
	if err := tkTable.SetField(unsafe.Pointer(&tk), col(Tk.ParentID), int32(4)); err != nil || tk.ParentID == nil || *tk.ParentID != 4 {
		t.Errorf("SetField of a nullable column: %v %v", tk.ParentID, err)
	}
	if err := tkTable.SetField(unsafe.Pointer(&tk), col(Tk.ParentID), nil); err != nil || tk.ParentID != nil {
		t.Errorf("SetField nil of a nullable column: %v %v", tk.ParentID, err)
	}
	if err := tkTable.SetField(unsafe.Pointer(&tk), col(Tk.ParentID), "x"); err == nil {
		t.Error("SetField of a value of another kind: no error")
	}

	// No rows: nothing runs.
	if err := db.InsertRows(itTable, itTable.NewRows(0)).Exec(ctx); err != nil {
		t.Errorf("InsertRows of none: %v", err)
	}
	if s, _, err := db.InsertRows(itTable, velox.TableRows{}).SQL(ctx); err != nil || s != "" {
		t.Errorf("SQL of InsertRows of none: %q %v", s, err)
	}
	// Every key set, or none.
	rows := itTable.NewRows(2)
	(*testmodel.Item)(rows.At(0)).ID = 5
	for i := range 2 {
		(*testmodel.Item)(rows.At(i)).SKU, (*testmodel.Item)(rows.At(i)).InvoiceID = "s", 1
	}
	if err := db.InsertRows(itTable, rows).Exec(ctx); err == nil {
		t.Error("InsertRows with one key set: no error")
	}
	if exec.Calls != 0 {
		t.Errorf("sent %d statements: %s", exec.Calls, exec.SQL)
	}

	// Item has no hooks: the plain path renders the typed insert's SQL, with
	// an upsert built in either order.
	x := testmodel.Item{InvoiceID: 1, SKU: "s", Qty: 2}
	ws, wa, err := db.Insert(&x).OnConflict(It.SKU).Update(It.Qty).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gs, ga, err := db.InsertTable(itTable, unsafe.Pointer(&x)).Update(col(It.Qty)).OnConflict(col(It.SKU)).SQL(ctx)
	if err != nil || gs != ws || len(ga) != len(wa) {
		t.Errorf("upsert: %q %v %v, want %q %v", gs, ga, err, ws, wa)
	}
	exec.Rows = [][]any{{int64(9)}}
	if err := db.InsertTable(itTable, unsafe.Pointer(&x)).Exec(ctx); err != nil || x.ID != 9 {
		t.Errorf("InsertTable: id %d, %v", x.ID, err)
	}

	// A delete's refusals, before anything is sent.
	exec.Calls = 0
	bad := itTable.Pred(col(It.SKU), velox.PredEQ, 3)
	other := tkTable.Pred(col(Tk.Title), velox.PredEQ, "t")
	for name, f := range map[string]func() error{
		"Delete of the zero Table, SQL": func() error { _, _, err := db.DeleteTable(velox.Table{}).All().SQL(ctx); return err },
		"Delete of the zero Table":      func() error { _, err := db.DeleteTable(velox.Table{}).All().Exec(ctx); return err },
		"Delete of a bad condition, SQL": func() error {
			_, _, err := db.DeleteTable(itTable).Where(bad).SQL(ctx)
			return err
		},
		"Delete of a bad condition": func() error { _, err := db.DeleteTable(itTable).Where(bad).Exec(ctx); return err },
		"Delete of another entity's condition": func() error {
			_, err := db.DeleteTable(itTable).Where(other).Exec(ctx)
			return err
		},
		"Update of another entity's condition": func() error {
			_, err := db.UpdateTable(itTable).Set(col(It.Qty), int64(1)).Where(other).Exec(ctx)
			return err
		},
		"Update of the zero Table": func() error {
			_, err := db.UpdateTable(velox.Table{}).All().Exec(ctx)
			return err
		},
		"Update with no Set": func() error { _, err := db.UpdateTable(itTable).All().Exec(ctx); return err },
	} {
		if err := f(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if exec.Calls != 0 {
		t.Errorf("sent %d statements: %s", exec.Calls, exec.SQL)
	}
}

// TestTableJoinRows: a LeftJoin whose target row is all NULL holds no row,
// one that matched holds it; two joins render as the typed query's; a row
// the driver cannot scan fails the read, joined or not.
func TestTableJoinRows(t *testing.T) {
	exec := &fakeconn.Exec{}
	db := openFake(velox.Postgres, exec)
	invCols := len(invTable.Info().Columns)
	row := func(item []any, inv []any) []any {
		if inv == nil {
			inv = make([]any, invCols)
		}
		return append(item, inv...)
	}
	full := make([]any, invCols)
	zero := invTable.New()
	for i := range full {
		full[i] = invTable.Value(zero, i) // a value of each column's type
	}
	full[col(I.ID)], full[col(I.Number)] = int64(3), "n3"
	exec.Rows = [][]any{row([]any{int64(1), int64(3), "a", int64(1)}, full), row([]any{int64(2), int64(4), "b", int64(1)}, nil)}
	q := db.QueryTable(itTable).LeftJoin(int(It.Invoice.I), invTable).Limit(5)
	rows, err := q.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := itTable.Filled(rows.At(0), int(It.Invoice.I), invTable); !ok || (*testmodel.Invoice)(p).Number != "n3" {
		t.Errorf("the matched row: %v %v", p, ok)
	}
	if p, ok := itTable.Filled(rows.At(1), int(It.Invoice.I), invTable); ok || p != nil {
		t.Errorf("the unmatched row holds %v", p)
	}
	exec.Rows = [][]any{row([]any{"x", int64(3), "a", int64(1)}, full)}
	if _, err := q.All(ctx); err == nil {
		t.Error("a joined row that does not scan: no error")
	}
	if _, err := db.QueryTable(itTable).Limit(5).All(ctx); err == nil {
		t.Error("a row that does not scan: no error")
	}
	exec.Rows = nil

	ws, wa, err := db.Query[testmodel.Ticket]().Join(Tk.Invoice).LeftJoin(Tk.Parent).Limit(1).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gs, ga, err := db.QueryTable(tkTable).Join(int(Tk.Invoice.I), invTable).LeftJoin(int(Tk.Parent.I), tkTable).Limit(1).SQL(ctx)
	if err != nil || gs != ws || len(ga) != len(wa) {
		t.Errorf("two joins: %q %v %v, want %q %v", gs, ga, err, ws, wa)
	}
}

// TestTableQueryLoadFails: a relation load that fails fails All, One and
// First, and a condition of another entity fails Count and Exists before
// anything runs.
func TestTableQueryLoadFails(t *testing.T) {
	boom := errors.New("boom")
	db, _ := eachDB(t, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		if st.Entity.Table == "items" {
			return boom
		}
		return next(ctx)
	}))
	q := velox.Untyped(db.Query[testmodel.Invoice]().With(I.Items).OrderBy(I.ID.Asc()).Limit(1))
	for run, f := range map[string]func() error{
		"All":   func() error { _, err := q.All(ctx); return err },
		"First": func() error { _, err := q.First(ctx); return err },
		"One": func() error {
			inv, err := db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).First(ctx)
			if err != nil {
				return err
			}
			_, err = q.Where(invTable.Pred(col(I.ID), velox.PredEQ, inv.ID)).One(ctx)
			return err
		},
	} {
		if err := f(); !errors.Is(err, boom) {
			t.Errorf("%s: %v, want the load's error", run, err)
		}
	}
	other := tkTable.Pred(col(Tk.Title), velox.PredEQ, "t")
	for run, f := range map[string]func() error{
		"Count":  func() error { _, err := db.QueryTable(invTable).Where(other).Count(ctx); return err },
		"Exists": func() error { _, err := db.QueryTable(invTable).Where(other).Exists(ctx); return err },
	} {
		if err := f(); err == nil {
			t.Errorf("%s of another entity's condition: no error", run)
		}
	}
}

// TestTableInsertPolicy: an untyped insert of a row the Insert policy
// refuses fails with ErrDenied and sends nothing, one row or many.
func TestTableInsertPolicy(t *testing.T) {
	exec := &fakeconn.Exec{}
	db := openFake(velox.Postgres, exec)
	in := annIn7() // ann may create only her own tickets
	bob := testmodel.Ticket{Owner: "bob", Title: "t", InvoiceID: 3}
	rows := tkTable.NewRows(2)
	for i := range 2 {
		*(*testmodel.Ticket)(rows.At(i)) = testmodel.Ticket{Owner: "ann", Title: "t", InvoiceID: 3}
	}
	(*testmodel.Ticket)(rows.At(1)).Owner = "bob"
	for name, f := range map[string]func() error{
		"InsertTable": func() error { return db.InsertTable(tkTable, unsafe.Pointer(&bob)).Exec(in) },
		"InsertRows":  func() error { return db.InsertRows(tkTable, rows).Exec(in) },
	} {
		if err := f(); !errors.Is(err, velox.ErrDenied) {
			t.Errorf("%s of bob's ticket: %v, want ErrDenied", name, err)
		}
	}
	if exec.Calls != 0 {
		t.Errorf("sent %d statements: %s", exec.Calls, exec.SQL)
	}
}

// TestTableTypedConditions: a typed condition converted to a Predicate,
// the zero one and Allow included, renders in an untyped query as in the
// typed one.
func TestTableTypedConditions(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	for name, p := range map[string]velox.Pred[testmodel.Item]{
		"the zero condition": {},
		"Allow":              velox.And[testmodel.Item](),
		"Not Allow":          velox.Not(velox.And[testmodel.Item]()),
		"EQ":                 It.SKU.EQ("a"),
	} {
		ws, wa, werr := db.Query[testmodel.Item]().Where(p).Limit(1).SQL(ctx)
		gs, ga, gerr := db.QueryTable(itTable).Where(velox.Predicate(p)).Limit(1).SQL(ctx)
		if gs != ws || len(ga) != len(wa) || (gerr == nil) != (werr == nil) {
			t.Errorf("%s: %q %v %v, want %q %v %v", name, gs, ga, gerr, ws, wa, werr)
		}
	}
}
