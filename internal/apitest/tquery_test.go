package apitest

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

// The column and relation indexes of Ticket and Invoice, as generated code
// names them.
var (
	tkTable  = untyped.TableOf[testmodel.Ticket]()
	invTable = untyped.TableOf[testmodel.Invoice]()
	itTable  = untyped.TableOf[testmodel.Item]()
)

// col is a column handle's index.
func col(c any) int { return int(reflect.ValueOf(c).FieldByName("I").Int()) }

// TestTableQueryParity: an untyped query sends the statement, and the
// arguments, the typed query it stands for sends, through the same pipeline:
// Ticket's scope, its policy and its hooks, and a Rewrite hook, on every
// dialect, on the plain path and the hooked one.
func TestTableQueryParity(t *testing.T) {
	ctx := annIn7()
	after := testmodel.Ticket{ID: 4, Title: "t"}
	type pair struct {
		name    string
		typed   func(db *velox.DB) velox.Query[testmodel.Ticket]
		untyped func(db *velox.DB) untyped.TableQuery
	}
	pairs := []pair{
		{"conditions",
			func(db *velox.DB) velox.Query[testmodel.Ticket] {
				return db.Query[testmodel.Ticket]().Where(Tk.Done.EQ(false), Tk.Title.Contains("a%b"), Tk.ParentID.IsNull(),
					Tk.ID.In(1, 2), Tk.Owner.HasPrefix("x_"), Tk.Owner.HasSuffix("y"), Tk.InvoiceID.GT(3), Tk.InvoiceID.LTE(9),
					Tk.ID.NotIn(), Tk.Title.NEQ("n"), Tk.ParentID.NotNull(), Tk.ID.GTE(0), Tk.ID.LT(100),
					Tk.Title.EqualFold("Ab"), Tk.Title.ContainsFold("a%b")).Limit(5)
			},
			func(db *velox.DB) untyped.TableQuery {
				return untyped.Query(db, tkTable).Where(tkTable.Pred(col(Tk.Done), untyped.PredEQ, false), tkTable.Pred(col(Tk.Title), untyped.PredContains, "a%b"),
					tkTable.Pred(col(Tk.ParentID), untyped.PredIsNull, nil), tkTable.Pred(col(Tk.ID), untyped.PredIn, []any{int64(1), int64(2)}),
					tkTable.Pred(col(Tk.Owner), untyped.PredHasPrefix, "x_"), tkTable.Pred(col(Tk.Owner), untyped.PredHasSuffix, "y"),
					tkTable.Pred(col(Tk.InvoiceID), untyped.PredGT, int64(3)), tkTable.Pred(col(Tk.InvoiceID), untyped.PredLTE, int64(9)),
					tkTable.Pred(col(Tk.ID), untyped.PredNotIn, nil), tkTable.Pred(col(Tk.Title), untyped.PredNEQ, "n"),
					tkTable.Pred(col(Tk.ParentID), untyped.PredNotNull, nil), tkTable.Pred(col(Tk.ID), untyped.PredGTE, int64(0)),
					tkTable.Pred(col(Tk.ID), untyped.PredLT, int64(100)),
					tkTable.Pred(col(Tk.Title), untyped.PredEqualFold, "Ab"), tkTable.Pred(col(Tk.Title), untyped.PredContainsFold, "a%b")).Limit(5)
			}},
		{"relation, order, columns, after",
			func(db *velox.DB) velox.Query[testmodel.Ticket] {
				return db.Query[testmodel.Ticket]().Where(Tk.Children.HasWith(Tk.Done.EQ(true)), Tk.Invoice.Has()).
					OrderBy(Tk.Title.Desc(), Tk.ID.Asc()).Columns(Tk.ID, Tk.Title).After(&after).Limit(5).Offset(2).MaxRows(7)
			},
			func(db *velox.DB) untyped.TableQuery {
				return untyped.Query(db, tkTable).Where(tkTable.Has(int(Tk.Children.I), tkTable, tkTable.Pred(col(Tk.Done), untyped.PredEQ, true)), tkTable.Has(int(Tk.Invoice.I), invTable)).
					OrderBy(velox.OrderTerm{Column: col(Tk.Title), Desc: true}, velox.OrderTerm{Column: col(Tk.ID)}).Columns(col(Tk.ID), col(Tk.Title)).
					After(&after).Limit(5).Offset(2).MaxRows(7)
			}},
		{"join",
			func(db *velox.DB) velox.Query[testmodel.Ticket] {
				return db.Query[testmodel.Ticket]().Join(Tk.Invoice).Where(Tk.Invoice.Is(I.Number.EQ("n"))).
					OrderBy(Tk.Invoice.By(I.Number.Asc()), Tk.ID.Asc()).AllowUnbounded()
			},
			func(db *velox.DB) untyped.TableQuery {
				return untyped.Query(db, tkTable).Join(int(Tk.Invoice.I), invTable).Where(velox.Predicate(Tk.Invoice.Is(I.Number.EQ("n")))).
					OrderBy(velox.OrderTerm{Column: col(I.Number), Rel: Tk.Invoice.I + 1}, velox.OrderTerm{Column: col(Tk.ID)}).AllowUnbounded()
			}},
		{"lock",
			func(db *velox.DB) velox.Query[testmodel.Ticket] {
				return db.Query[testmodel.Ticket]().Where(Tk.ID.EQ(1)).ForUpdate().Limit(1)
			},
			func(db *velox.DB) untyped.TableQuery {
				return untyped.Query(db, tkTable).Where(tkTable.Pred(col(Tk.ID), untyped.PredEQ, int64(1))).ForUpdate().Limit(1)
			}},
	}
	extra := func(_ context.Context, st *velox.Statement) error {
		if st.Op == velox.OpSelect && st.Entity.Name == "testmodel.Ticket" {
			st.Where = append(st.Where, velox.ColumnEQ(col(Tk.Title), "T"))
		}
		return nil
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		for _, hooked := range []bool{false, true} {
			var opts []velox.Option
			if hooked {
				opts = []velox.Option{velox.WithRewrite(extra), velox.WithMaxRows(50)}
			}
			for _, p := range pairs {
				name := d.String() + "/" + p.name
				if hooked {
					name += "/hooked"
				}
				t.Run(name, func(t *testing.T) {
					e := &fakeconn.Exec{}
					db := openFake(d, e, opts...)
					wantSQL, wantArgs, werr := p.typed(db).SQL(ctx)
					gotSQL, gotArgs, gerr := p.untyped(db).SQL(ctx)
					if (werr == nil) != (gerr == nil) || werr != nil && werr.Error() != gerr.Error() {
						t.Fatalf("SQL errors: typed %v, untyped %v", werr, gerr)
					}
					if gotSQL != wantSQL || !reflect.DeepEqual(gotArgs, wantArgs) {
						t.Errorf("SQL:\n typed   %s %#v\n untyped %s %#v", wantSQL, wantArgs, gotSQL, gotArgs)
					}
					if werr != nil {
						return
					}
					run := func(f func() error) ([]string, [][]any) {
						t.Helper()
						e.Log, e.ArgLog = nil, nil
						if err := f(); err != nil {
							t.Fatal(err)
						}
						return e.Log, e.ArgLog
					}
					sent := func(what string, typed, untyped func() error) {
						t.Helper()
						wl, wa := run(typed)
						gl, ga := run(untyped)
						if !reflect.DeepEqual(gl, wl) || !reflect.DeepEqual(ga, wa) {
							t.Errorf("%s sent:\n typed   %q %#v\n untyped %q %#v", what, wl, wa, gl, ga)
						}
					}
					if !strings.Contains(p.name, "lock") {
						sent("All", func() error { _, err := p.typed(db).All(ctx); return err },
							func() error { _, err := p.untyped(db).All(ctx); return err })
						sent("Count", func() error { _, err := p.typed(db).Count(ctx); return err },
							func() error { _, err := p.untyped(db).Count(ctx); return err })
						sent("Exists", func() error { _, err := p.typed(db).Exists(ctx); return err },
							func() error { _, err := p.untyped(db).Exists(ctx); return err })
					}
					// FromQuery keeps the query.
					if s, a, err := untyped.FromQuery(p.typed(db)).SQL(ctx); err != nil || s != wantSQL || !reflect.DeepEqual(a, wantArgs) {
						t.Errorf("FromQuery: %s %#v %v", s, a, err)
					}
				})
			}
		}
	}
}

// TestTableQueryRows: on SQLite, an untyped query reads the rows the typed
// one reads, with its joins and relation loads, and First, One, Count,
// Exists, LoadEach and CountEach agree.
func TestTableQueryRows(t *testing.T) {
	db, _ := eachDB(t)
	tq := db.Query[testmodel.Item]().OrderBy(It.SKU.Desc()).Limit(10)
	uq := untyped.Query(db, itTable).OrderBy(velox.OrderTerm{Column: col(It.SKU), Desc: true}).Limit(10)
	want, err := tq.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := uq.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rows := untyped.RowsOf[testmodel.Item](got); !reflect.DeepEqual(rows, want) || got.Len() != len(want) {
		t.Errorf("All: got %v, want %v", rows, want)
	}
	for i := range got.Len() {
		if got.At(i).(*testmodel.Item).ID != want[i].ID {
			t.Errorf("At(%d)", i)
		}
	}
	// Growing past the first rows allocated keeps every row.
	many, err := untyped.Query(db, itTable).AllowUnbounded().All(ctx)
	if n, _ := db.Query[testmodel.Item]().Count(ctx); err != nil || int64(many.Len()) != n {
		t.Errorf("Unbounded: %d rows of %d, %v", many.Len(), n, err)
	}
	// A join fills the relation.
	joined, err := untyped.Query(db, itTable).Join(int(It.Invoice.I), invTable).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range untyped.RowsOf[testmodel.Item](joined) {
		if inv, ok := it.Edges.Invoice.Get(); !ok || inv == nil || inv.ID != it.InvoiceID {
			t.Errorf("join: item %d: %v %v", it.ID, inv, ok)
		}
	}
	// Filled of a to-many relation holds no row, loaded or not.
	invs, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(ctx)
	if err != nil || len(invs) == 0 {
		t.Fatalf("invoices: %v %v", invs, err)
	}
	loaded := 0
	for i := range invs {
		items, _ := invs[i].Edges.Items.Get()
		loaded += len(items)
		for _, target := range []untyped.Table{itTable, invTable} {
			if r, ok := invTable.Filled(&invs[i], int(I.Items.I), target); ok || r != nil {
				t.Errorf("Filled of the loaded to-many Items: %v %v", r, ok)
			}
		}
	}
	if loaded == 0 {
		t.Error("no invoice has items: the case is not covered")
	}
	if r, ok := invTable.Filled(&testmodel.Invoice{}, int(I.Items.I), itTable); ok || r != nil {
		t.Errorf("Filled of an empty to-many Items: %v %v", r, ok)
	}
	p, err := uq.First(ctx)
	if err != nil || p.(*testmodel.Item).ID != want[0].ID {
		t.Errorf("First: %v %v", p, err)
	}
	if _, err := uq.One(ctx); !errors.Is(err, velox.ErrNotSingular) {
		t.Errorf("One of many: %v", err)
	}
	if _, err := uq.Where(itTable.Pred(col(It.SKU), untyped.PredEQ, "nope")).One(ctx); !velox.IsNotFound(err) || err.Error() != "velox: testmodel.Item: velox: not found" && !strings.Contains(err.Error(), "testmodel.Item") {
		t.Errorf("One of none: %v", err)
	}
	if n, err := uq.Count(ctx); err != nil || n != int64(len(want)) {
		t.Errorf("Count: %d %v", n, err)
	}
	if ok, err := uq.Exists(ctx); err != nil || !ok {
		t.Errorf("Exists: %v %v", ok, err)
	}
	// LoadEach and CountEach, untyped, read what the typed ones read.
	owners := eachInvoices(t, db, 2, 1, 3, 1)
	ptrs := make([]any, len(owners))
	for i, o := range owners {
		ptrs[i] = o
	}
	wantEach, err := velox.LoadEach(ctx, db.Query[testmodel.Item]().OrderBy(It.SKU.Asc()), I.Items.LimitEach(2), owners)
	if err != nil {
		t.Fatal(err)
	}
	gotEach, err := untyped.Query(db, itTable).OrderBy(velox.OrderTerm{Column: col(It.SKU)}).LoadEach(ctx, invTable, int(I.Items.I), 2, ptrs)
	if err != nil {
		t.Fatal(err)
	}
	for i := range wantEach {
		if g := untyped.RowsOf[testmodel.Item](gotEach[i]); len(g) != len(wantEach[i]) || len(g) > 0 && !reflect.DeepEqual(g, wantEach[i]) {
			t.Errorf("LoadEach %d: got %v, want %v", i, g, wantEach[i])
		}
	}
	wantN, err := velox.CountEach(ctx, db.Query[testmodel.Item](), I.Items, owners)
	if err != nil {
		t.Fatal(err)
	}
	gotN, err := untyped.Query(db, itTable).CountEach(ctx, invTable, int(I.Items.I), ptrs)
	if err != nil || !reflect.DeepEqual(gotN, wantN) {
		t.Errorf("CountEach: got %v %v, want %v", gotN, err, wantN)
	}
}

// TestTablePredRefuses: an untyped condition the column cannot take is an
// error of the statement, never a condition on another value.
func TestTablePredRefuses(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	for name, p := range map[string]velox.Predicate{
		"another type":         tkTable.Pred(col(Tk.ID), untyped.PredEQ, 1),
		"a pointer":            tkTable.Pred(col(Tk.ParentID), untyped.PredEQ, new(int64)),
		"nil":                  tkTable.Pred(col(Tk.ID), untyped.PredEQ, nil),
		"In of another type":   tkTable.Pred(col(Tk.ID), untyped.PredIn, []any{int64(1), "2"}),
		"In of no []any":       tkTable.Pred(col(Tk.ID), untyped.PredIn, []int64{1}),
		"text on a number":     tkTable.Pred(col(Tk.ID), untyped.PredContains, int64(1)),
		"fold on a number":     tkTable.Pred(col(Tk.ID), untyped.PredContainsFold, int64(1)),
		"fold of another type": tkTable.Pred(col(Tk.Title), untyped.PredEqualFold, 1),
		"IsNull not nullable":  tkTable.Pred(col(Tk.ID), untyped.PredIsNull, nil),
		"IsNull with a value":  tkTable.Pred(col(Tk.ParentID), untyped.PredIsNull, int64(1)),
		"column out of range":  tkTable.Pred(99, untyped.PredEQ, int64(1)),
		"no operator":          tkTable.Pred(col(Tk.ID), 0, int64(1)),
		"zero Table":           untyped.Table{}.Pred(0, untyped.PredEQ, int64(1)),
	} {
		if _, _, err := untyped.Query(db, tkTable).Where(p).Limit(1).SQL(ctx); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, _, err := untyped.Query(db, tkTable).Columns(99).Limit(1).SQL(ctx); err == nil {
		t.Error("Columns out of range: no error")
	}
	if _, _, err := untyped.Query(db, untyped.Table{}).Limit(1).SQL(ctx); err == nil {
		t.Error("a zero Table's query: no error")
	}
	if _, _, err := untyped.Query(db, tkTable).After(nil).Limit(1).SQL(ctx); err == nil {
		t.Error("After(nil): no error")
	}
	if _, _, err := untyped.Query(db, tkTable).Join(int(Tk.Children.I), tkTable).Limit(1).SQL(ctx); err == nil {
		t.Error("Join of a to-many relation: no error")
	}
	if _, _, err := untyped.Query(db, tkTable).Join(int(Tk.Invoice.I), tkTable).Limit(1).SQL(ctx); err == nil {
		t.Error("Join with another target: no error")
	}
}

// TestTableRows covers the row helpers: Field, Value, New, Fill and Filled.
// Each refuses a row of another entity, a non-pointer and a nil *E.
func TestTableRows(t *testing.T) {
	p := tkTable.New()
	x := p.(*testmodel.Ticket)
	db, ctx := openFake(velox.Postgres, &fakeconn.Exec{}), annIn7()
	*tkTable.Field(p, col(Tk.Title)).(*string) = "t"
	if x.Title != "t" || tkTable.Value(p, col(Tk.Title)) != "t" {
		t.Errorf("Field and Value: %v", x)
	}
	for name, bad := range map[string]any{
		"another entity": &testmodel.Invoice{},
		"a value":        testmodel.Ticket{},
		"a nil *E":       (*testmodel.Ticket)(nil),
		"nil":            nil,
	} {
		if tkTable.Field(bad, col(Tk.Title)) != nil || tkTable.Value(bad, col(Tk.Title)) != nil {
			t.Errorf("Field or Value of %s: not nil", name)
		}
		if err := tkTable.SetField(bad, col(Tk.Title), "t"); err == nil {
			t.Errorf("SetField of %s: no error", name)
		}
		if err := tkTable.Validate(bad); err == nil {
			t.Errorf("Validate of %s: no error", name)
		}
		if err := untyped.Insert(db, tkTable, bad).Exec(ctx); err == nil {
			t.Errorf("InsertTable of %s: no error", name)
		}
		if _, _, err := untyped.Query(db, tkTable).After(bad).Limit(1).SQL(ctx); err == nil {
			t.Errorf("After of %s: no error", name)
		}
		if _, _, err := untyped.Query(db, tkTable).Where(tkTable.Of(int(Tk.Parent.I), tkTable, bad)).Limit(1).SQL(ctx); err == nil {
			t.Errorf("Of of %s: no error", name)
		}
	}
	if tkTable.Field(p, -1) != nil || tkTable.Value(p, 99) != nil {
		t.Error("Field or Value of no column: not nil")
	}
	inv := invTable.New()
	if err := tkTable.Fill(p, int(Tk.Invoice.I), invTable, inv); err != nil {
		t.Fatal(err)
	}
	if got, ok := tkTable.Filled(p, int(Tk.Invoice.I), invTable); !ok || got != inv {
		t.Errorf("Filled: %v %v", got, ok)
	}
	if v, ok := x.Edges.Invoice.Get(); !ok || any(v) != inv {
		t.Errorf("Get after Fill: %v %v", v, ok)
	}
	if err := tkTable.Fill(p, int(Tk.Invoice.I), tkTable, inv); err == nil {
		t.Error("Fill with another target: no error")
	}
	if _, ok := tkTable.Filled(p, int(Tk.Parent.I), tkTable); ok {
		t.Error("Filled of an empty relation")
	}
	if tkTable.Info().Name != "testmodel.Ticket" || tkTable.Err() != nil || (untyped.Table{}).Err() == nil {
		t.Error("Info and Err")
	}
}

// TestPredicateLogic: AndPredicates, OrPredicates and NotPredicate render as
// And, Or and Not of the same conditions, an Allow among them included.
func TestPredicateLogic(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	ctx := annIn7()
	done, title := tkTable.Pred(col(Tk.Done), untyped.PredEQ, true), tkTable.Pred(col(Tk.Title), untyped.PredEQ, "t")
	allow := velox.Predicate(velox.And[testmodel.Ticket]())
	for name, c := range map[string]struct {
		typed   velox.Pred[testmodel.Ticket]
		untyped velox.Predicate
	}{
		"and":          {velox.And(Tk.Done.EQ(true), Tk.Title.EQ("t")), untyped.And(done, title)},
		"and, allow":   {velox.And(Tk.Done.EQ(true), velox.And[testmodel.Ticket]()), untyped.And(done, allow)},
		"and of none":  {velox.And[testmodel.Ticket](), untyped.And()},
		"or":           {velox.Or(Tk.Done.EQ(true), Tk.Title.EQ("t")), untyped.Or(done, title)},
		"or, allow":    {velox.Or(Tk.Done.EQ(true), velox.And[testmodel.Ticket]()), untyped.Or(done, allow)},
		"or of none":   {velox.Or[testmodel.Ticket](), untyped.Or()},
		"not":          {velox.Not(Tk.Done.EQ(true)), untyped.Not(done)},
		"not of allow": {velox.Not(velox.And[testmodel.Ticket]()), untyped.Not(allow)},
	} {
		want, wargs, werr := db.Query[testmodel.Ticket]().Where(c.typed).Limit(1).SQL(ctx)
		got, gargs, gerr := untyped.Query(db, tkTable).Where(c.untyped).Limit(1).SQL(ctx)
		if got != want || len(gargs) != len(wargs) || len(wargs) > 0 && !reflect.DeepEqual(gargs, wargs) || (gerr == nil) != (werr == nil) {
			t.Errorf("%s:\n got %s %v %v\nwant %s %v %v", name, got, gargs, gerr, want, wargs, werr)
		}
	}
}

// TestTableConvert: Convert turns a value of the column's kind into the
// column's Go type, and refuses another kind.
func TestTableConvert(t *testing.T) {
	type (
		label string
		flag  bool
	)
	for name, c := range map[string]struct {
		col  int
		v    any
		want any
	}{
		"its own type":      {col(Tk.Title), "t", "t"},
		"a string type":     {col(Tk.Title), label("t"), "t"},
		"a bool type":       {col(Tk.Done), flag(true), true},
		"another int size":  {col(Tk.ID), int32(3), int64(3)},
		"an unsigned int":   {col(Tk.ID), uint8(3), int64(3)},
		"a nullable column": {col(Tk.ParentID), int16(2), int64(2)},
	} {
		got, err := tkTable.Convert(c.col, c.v)
		if err != nil || got != c.want {
			t.Errorf("%s: %#v %v, want %#v", name, got, err, c.want)
		}
	}
	for name, c := range map[string]struct {
		col int
		v   any
	}{
		"a string to a number": {col(Tk.ID), "3"},
		"a number to a string": {col(Tk.Title), int64(3)},
		"a float to a number":  {col(Tk.ID), 3.0},
		"nil":                  {col(Tk.ID), nil},
		"out of range":         {99, int64(1)},
	} {
		if _, err := tkTable.Convert(c.col, c.v); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := (untyped.Table{}).Convert(0, int64(1)); err == nil {
		t.Error("the zero Table: no error")
	}
	// A number the column's type cannot hold is ErrOutOfRange, not a value
	// wrapped round.
	if _, err := tkTable.Convert(col(Tk.ID), uint64(math.MaxUint64)); !errors.Is(err, velox.ErrOutOfRange) {
		t.Errorf("MaxUint64 for an int64: %v, want ErrOutOfRange", err)
	}
	if got, err := tkTable.Convert(col(Tk.ID), uint64(math.MaxInt64)); err != nil || got != int64(math.MaxInt64) {
		t.Errorf("MaxInt64 as a uint64: %v %v", got, err)
	}
}

// TestTableHasRowFilter: Ticket's schema declares a Read policy; Item's
// declares none, and the zero Table has none.
func TestTableHasRowFilter(t *testing.T) {
	if !tkTable.HasRowFilter() {
		t.Error("Ticket: no Read policy")
	}
	if itTable.HasRowFilter() {
		t.Error("Item: a Read policy")
	}
	if (untyped.Table{}).HasRowFilter() {
		t.Error("the zero Table: a Read policy")
	}
}
