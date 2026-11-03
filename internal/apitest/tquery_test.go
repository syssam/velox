package apitest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// The column and relation indexes of Ticket and Invoice, as generated code
// names them.
var (
	tkTable  = velox.TableOf[testmodel.Ticket]()
	invTable = velox.TableOf[testmodel.Invoice]()
	itTable  = velox.TableOf[testmodel.Item]()
)

func col[C interface{ ~struct{ I int32 } }](c C) int { return int(struct{ I int32 }(c).I) }

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
		untyped func(db *velox.DB) velox.TableQuery
	}
	pairs := []pair{
		{"conditions",
			func(db *velox.DB) velox.Query[testmodel.Ticket] {
				return db.Query[testmodel.Ticket]().Where(Tk.Done.EQ(false), Tk.Title.Contains("a%b"), Tk.ParentID.IsNull(),
					Tk.ID.In(1, 2), Tk.Owner.HasPrefix("x_"), Tk.Owner.HasSuffix("y"), Tk.InvoiceID.GT(3), Tk.InvoiceID.LTE(9),
					Tk.ID.NotIn(), Tk.Title.NEQ("n"), Tk.ParentID.NotNull(), Tk.ID.GTE(0), Tk.ID.LT(100)).Limit(5)
			},
			func(db *velox.DB) velox.TableQuery {
				return db.QueryTable(tkTable).Where(tkTable.Pred(col(Tk.Done), velox.PredEQ, false), tkTable.Pred(col(Tk.Title), velox.PredContains, "a%b"),
					tkTable.Pred(col(Tk.ParentID), velox.PredIsNull, nil), tkTable.Pred(col(Tk.ID), velox.PredIn, []any{int64(1), int64(2)}),
					tkTable.Pred(col(Tk.Owner), velox.PredHasPrefix, "x_"), tkTable.Pred(col(Tk.Owner), velox.PredHasSuffix, "y"),
					tkTable.Pred(col(Tk.InvoiceID), velox.PredGT, int64(3)), tkTable.Pred(col(Tk.InvoiceID), velox.PredLTE, int64(9)),
					tkTable.Pred(col(Tk.ID), velox.PredNotIn, nil), tkTable.Pred(col(Tk.Title), velox.PredNEQ, "n"),
					tkTable.Pred(col(Tk.ParentID), velox.PredNotNull, nil), tkTable.Pred(col(Tk.ID), velox.PredGTE, int64(0)),
					tkTable.Pred(col(Tk.ID), velox.PredLT, int64(100))).Limit(5)
			}},
		{"relation, order, columns, after",
			func(db *velox.DB) velox.Query[testmodel.Ticket] {
				return db.Query[testmodel.Ticket]().Where(Tk.Children.HasWhere(Tk.Done.EQ(true)), Tk.Invoice.Has()).
					OrderBy(Tk.Title.Desc(), Tk.ID.Asc()).Columns(Tk.ID, Tk.Title).After(&after).Limit(5).Offset(2).MaxRows(7)
			},
			func(db *velox.DB) velox.TableQuery {
				return db.QueryTable(tkTable).Where(tkTable.Has(int(Tk.Children.I), tkTable, tkTable.Pred(col(Tk.Done), velox.PredEQ, true)), tkTable.Has(int(Tk.Invoice.I), invTable)).
					OrderBy(velox.OrderTerm{Column: col(Tk.Title), Desc: true}, velox.OrderTerm{Column: col(Tk.ID)}).Columns(col(Tk.ID), col(Tk.Title)).
					After(unsafe.Pointer(&after)).Limit(5).Offset(2).MaxRows(7)
			}},
		{"join",
			func(db *velox.DB) velox.Query[testmodel.Ticket] {
				return db.Query[testmodel.Ticket]().Join(Tk.Invoice).Where(Tk.Invoice.Is(I.Number.EQ("n"))).
					OrderBy(Tk.Invoice.By(I.Number.Asc()), Tk.ID.Asc()).Unbounded()
			},
			func(db *velox.DB) velox.TableQuery {
				return db.QueryTable(tkTable).Join(int(Tk.Invoice.I), invTable).Where(velox.Predicate(Tk.Invoice.Is(I.Number.EQ("n")))).
					OrderBy(velox.OrderTerm{Column: col(I.Number), Rel: Tk.Invoice.I + 1}, velox.OrderTerm{Column: col(Tk.ID)}).Unbounded()
			}},
		{"lock",
			func(db *velox.DB) velox.Query[testmodel.Ticket] {
				return db.Query[testmodel.Ticket]().Where(Tk.ID.EQ(1)).ForUpdate().Limit(1)
			},
			func(db *velox.DB) velox.TableQuery {
				return db.QueryTable(tkTable).Where(tkTable.Pred(col(Tk.ID), velox.PredEQ, int64(1))).ForUpdate().Limit(1)
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
					// The conversions both ways keep the query.
					if s, a, err := velox.Untyped(p.typed(db)).SQL(ctx); err != nil || s != wantSQL || !reflect.DeepEqual(a, wantArgs) {
						t.Errorf("Untyped: %s %#v %v", s, a, err)
					}
					if s, a, err := velox.Typed[testmodel.Ticket](p.untyped(db)).SQL(ctx); err != nil || s != wantSQL || !reflect.DeepEqual(a, wantArgs) {
						t.Errorf("Typed: %s %#v %v", s, a, err)
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
	uq := db.QueryTable(itTable).OrderBy(velox.OrderTerm{Column: col(It.SKU), Desc: true}).Limit(10)
	want, err := tq.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := uq.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rows := velox.RowsOf[testmodel.Item](got); !reflect.DeepEqual(rows, want) || got.Len() != len(want) {
		t.Errorf("All: got %v, want %v", rows, want)
	}
	for i := range got.Len() {
		if (*testmodel.Item)(got.At(i)).ID != want[i].ID {
			t.Errorf("At(%d)", i)
		}
	}
	// Growing past the first rows allocated keeps every row.
	many, err := db.QueryTable(itTable).Unbounded().All(ctx)
	if n, _ := db.Query[testmodel.Item]().Count(ctx); err != nil || int64(many.Len()) != n {
		t.Errorf("Unbounded: %d rows of %d, %v", many.Len(), n, err)
	}
	// A join fills the relation.
	joined, err := db.QueryTable(itTable).Join(int(It.Invoice.I), invTable).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range velox.RowsOf[testmodel.Item](joined) {
		if inv, ok := it.Invoice.Get(); !ok || inv == nil || inv.ID != it.InvoiceID {
			t.Errorf("join: item %d: %v %v", it.ID, inv, ok)
		}
	}
	p, err := uq.First(ctx)
	if err != nil || (*testmodel.Item)(p).ID != want[0].ID {
		t.Errorf("First: %v %v", p, err)
	}
	if _, err := uq.One(ctx); !errors.Is(err, velox.ErrNotSingular) {
		t.Errorf("One of many: %v", err)
	}
	if _, err := uq.Where(itTable.Pred(col(It.SKU), velox.PredEQ, "nope")).One(ctx); !velox.IsNotFound(err) || err.Error() != "velox: testmodel.Item: velox: not found" && !strings.Contains(err.Error(), "testmodel.Item") {
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
	ptrs := make([]unsafe.Pointer, len(owners))
	for i, o := range owners {
		ptrs[i] = unsafe.Pointer(o)
	}
	wantEach, err := velox.LoadEach(ctx, db.Query[testmodel.Item]().OrderBy(It.SKU.Asc()), I.Items.LimitEach(2), owners)
	if err != nil {
		t.Fatal(err)
	}
	gotEach, err := db.QueryTable(itTable).OrderBy(velox.OrderTerm{Column: col(It.SKU)}).LoadEach(ctx, invTable, int(I.Items.I), 2, ptrs)
	if err != nil {
		t.Fatal(err)
	}
	for i := range wantEach {
		if g := velox.RowsOf[testmodel.Item](gotEach[i]); len(g) != len(wantEach[i]) || len(g) > 0 && !reflect.DeepEqual(g, wantEach[i]) {
			t.Errorf("LoadEach %d: got %v, want %v", i, g, wantEach[i])
		}
	}
	wantN, err := velox.CountEach(ctx, db.Query[testmodel.Item](), I.Items, owners)
	if err != nil {
		t.Fatal(err)
	}
	gotN, err := db.QueryTable(itTable).CountEach(ctx, invTable, int(I.Items.I), ptrs)
	if err != nil || !reflect.DeepEqual(gotN, wantN) {
		t.Errorf("CountEach: got %v %v, want %v", gotN, err, wantN)
	}
}

// TestTablePredRefuses: an untyped condition the column cannot take is an
// error of the statement, never a condition on another value.
func TestTablePredRefuses(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	for name, p := range map[string]velox.Predicate{
		"another type":        tkTable.Pred(col(Tk.ID), velox.PredEQ, 1),
		"a pointer":           tkTable.Pred(col(Tk.ParentID), velox.PredEQ, new(int64)),
		"nil":                 tkTable.Pred(col(Tk.ID), velox.PredEQ, nil),
		"In of another type":  tkTable.Pred(col(Tk.ID), velox.PredIn, []any{int64(1), "2"}),
		"In of no []any":      tkTable.Pred(col(Tk.ID), velox.PredIn, []int64{1}),
		"text on a number":    tkTable.Pred(col(Tk.ID), velox.PredContains, int64(1)),
		"IsNull not nullable": tkTable.Pred(col(Tk.ID), velox.PredIsNull, nil),
		"IsNull with a value": tkTable.Pred(col(Tk.ParentID), velox.PredIsNull, int64(1)),
		"column out of range": tkTable.Pred(99, velox.PredEQ, int64(1)),
		"no operator":         tkTable.Pred(col(Tk.ID), 0, int64(1)),
		"zero Table":          velox.Table{}.Pred(0, velox.PredEQ, int64(1)),
	} {
		if _, _, err := db.QueryTable(tkTable).Where(p).Limit(1).SQL(ctx); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, _, err := db.QueryTable(tkTable).Columns(99).Limit(1).SQL(ctx); err == nil {
		t.Error("Columns out of range: no error")
	}
	if _, _, err := db.QueryTable(velox.Table{}).Limit(1).SQL(ctx); err == nil {
		t.Error("a zero Table's query: no error")
	}
	if _, _, err := velox.Typed[testmodel.Invoice](db.QueryTable(tkTable)).Limit(1).SQL(ctx); err == nil {
		t.Error("Typed of another entity: no error")
	}
	if _, _, err := db.QueryTable(tkTable).After(nil).Limit(1).SQL(ctx); err == nil {
		t.Error("After(nil): no error")
	}
	if _, _, err := db.QueryTable(tkTable).Join(int(Tk.Children.I), tkTable).Limit(1).SQL(ctx); err == nil {
		t.Error("Join of a to-many relation: no error")
	}
	if _, _, err := db.QueryTable(tkTable).Join(int(Tk.Invoice.I), tkTable).Limit(1).SQL(ctx); err == nil {
		t.Error("Join with another target: no error")
	}
}

// TestTableRows covers the row helpers: Field, Value, Ptr, New, Fill and
// Filled.
func TestTableRows(t *testing.T) {
	p := tkTable.New()
	x := (*testmodel.Ticket)(p)
	*tkTable.Field(p, col(Tk.Title)).(*string) = "t"
	if x.Title != "t" || tkTable.Value(p, col(Tk.Title)) != "t" {
		t.Errorf("Field and Value: %v", x)
	}
	if tkTable.Ptr(p).(*testmodel.Ticket) != x {
		t.Error("Ptr")
	}
	inv := invTable.New()
	if err := tkTable.Fill(p, int(Tk.Invoice.I), invTable, inv); err != nil {
		t.Fatal(err)
	}
	if got, ok := tkTable.Filled(p, int(Tk.Invoice.I)); !ok || got != inv {
		t.Errorf("Filled: %v %v", got, ok)
	}
	if v, ok := x.Invoice.Get(); !ok || unsafe.Pointer(v) != inv {
		t.Errorf("Get after Fill: %v %v", v, ok)
	}
	if err := tkTable.Fill(p, int(Tk.Invoice.I), tkTable, inv); err == nil {
		t.Error("Fill with another target: no error")
	}
	if _, ok := tkTable.Filled(p, int(Tk.Parent.I)); ok {
		t.Error("Filled of an empty relation")
	}
	if tkTable.Info().Name != "testmodel.Ticket" || tkTable.Err() != nil || (velox.Table{}).Err() == nil {
		t.Error("Info and Err")
	}
}
