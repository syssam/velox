package apitest

import (
	"context"
	"reflect"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

// TestTableWriteParity: an untyped insert, update and delete sends the
// statements, and the arguments, the typed write it stands for sends,
// through the same pipeline: Ticket's scope, its policy and its hooks (which
// fill CreatedBy and OrgID on insert), on every dialect.
func TestTableWriteParity(t *testing.T) {
	ctx := annIn7()
	newTicket := func() testmodel.Ticket { return testmodel.Ticket{Owner: "ann", Title: "t", InvoiceID: 3} }
	type pair struct {
		name           string
		typed, untyped func(db *velox.DB) (sql func() (string, []any, error), exec func() error)
	}
	pairs := []pair{
		{"insert",
			func(db *velox.DB) (func() (string, []any, error), func() error) {
				x := newTicket()
				return func() (string, []any, error) { x := newTicket(); return db.Insert(&x).SQL(ctx) },
					func() error { return db.Insert(&x).Exec(ctx) }
			},
			func(db *velox.DB) (func() (string, []any, error), func() error) {
				x := newTicket()
				return func() (string, []any, error) {
						x := newTicket()
						return untyped.Insert(db, tkTable, &x).SQL(ctx)
					},
					func() error { return untyped.Insert(db, tkTable, &x).Exec(ctx) }
			}},
		{"insert rows, on conflict",
			func(db *velox.DB) (func() (string, []any, error), func() error) {
				xs := func() []testmodel.Ticket { return []testmodel.Ticket{newTicket(), newTicket()} }
				q := func() velox.InsertQuery[testmodel.Ticket] {
					return db.InsertAll(xs()).OnConflict(Tk.ID).Update(Tk.Title)
				}
				return func() (string, []any, error) { return q().SQL(ctx) }, func() error { return q().Exec(ctx) }
			},
			func(db *velox.DB) (func() (string, []any, error), func() error) {
				q := func() untyped.TableInsert {
					rows := tkTable.NewRows(2)
					for i := range rows.Len() {
						*rows.At(i).(*testmodel.Ticket) = newTicket()
					}
					return untyped.InsertRows(db, tkTable, rows).OnConflict(col(Tk.ID)).Update(col(Tk.Title))
				}
				return func() (string, []any, error) { return q().SQL(ctx) }, func() error { return q().Exec(ctx) }
			}},
		{"update",
			func(db *velox.DB) (func() (string, []any, error), func() error) {
				q := func() velox.UpdateQuery[testmodel.Ticket] {
					return db.Update[testmodel.Ticket]().Set(Tk.Title.Set("u"), Tk.ParentID.SetNull(), Tk.Done.Set(true)).Where(Tk.ID.EQ(4))
				}
				return func() (string, []any, error) { return q().SQL(ctx) }, func() error { _, err := q().Exec(ctx); return err }
			},
			func(db *velox.DB) (func() (string, []any, error), func() error) {
				q := func() untyped.TableUpdate {
					return untyped.Update(db, tkTable).Set(col(Tk.Title), "u").SetNull(col(Tk.ParentID)).
						Assign(untyped.Assignments([]velox.Setter[testmodel.Ticket]{Tk.Done.Set(true)})...).
						Where(tkTable.Pred(col(Tk.ID), untyped.PredEQ, int64(4)))
				}
				return func() (string, []any, error) { return q().SQL(ctx) }, func() error { _, err := q().Exec(ctx); return err }
			}},
		{"delete",
			func(db *velox.DB) (func() (string, []any, error), func() error) {
				q := func() velox.DeleteQuery[testmodel.Ticket] {
					return db.Delete[testmodel.Ticket]().Where(Tk.Done.EQ(true), Tk.Invoice.Has())
				}
				return func() (string, []any, error) { return q().SQL(ctx) }, func() error { _, err := q().Exec(ctx); return err }
			},
			func(db *velox.DB) (func() (string, []any, error), func() error) {
				q := func() untyped.TableDelete {
					return untyped.Delete(db, tkTable).Where(tkTable.Pred(col(Tk.Done), untyped.PredEQ, true), tkTable.Has(int(Tk.Invoice.I), invTable))
				}
				return func() (string, []any, error) { return q().SQL(ctx) }, func() error { _, err := q().Exec(ctx); return err }
			}},
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		for _, p := range pairs {
			t.Run(d.String()+"/"+p.name, func(t *testing.T) {
				e := &fakeconn.Exec{}
				db := openFake(d, e)
				tsql, texec := p.typed(db)
				usql, uexec := p.untyped(db)
				wantSQL, wantArgs, werr := tsql()
				gotSQL, gotArgs, gerr := usql()
				if (werr == nil) != (gerr == nil) || werr != nil && werr.Error() != gerr.Error() {
					t.Fatalf("SQL errors: typed %v, untyped %v", werr, gerr)
				}
				if gotSQL == "" && werr == nil {
					t.Error("no SQL")
				}
				if gotSQL != wantSQL || (len(gotArgs) > 0 || len(wantArgs) > 0) && !reflect.DeepEqual(gotArgs, wantArgs) {
					t.Errorf("SQL:\n typed   %s %#v\n untyped %s %#v", wantSQL, wantArgs, gotSQL, gotArgs)
				}
				run := func(f func() error) ([]string, [][]any, error) {
					e.Log, e.ArgLog = nil, nil
					err := f()
					return e.Log, e.ArgLog, err
				}
				wl, wa, werr := run(texec)
				gl, ga, gerr := run(uexec)
				if (werr == nil) != (gerr == nil) || werr != nil && werr.Error() != gerr.Error() {
					t.Fatalf("Exec errors: typed %v, untyped %v", werr, gerr)
				}
				if len(gl) == 0 && werr == nil {
					t.Error("nothing sent")
				}
				if !reflect.DeepEqual(gl, wl) || !reflect.DeepEqual(ga, wa) {
					t.Errorf("Exec sent:\n typed   %q %#v\n untyped %q %#v", wl, wa, gl, ga)
				}
			})
		}
	}
}

// TestTableWriteRefuses: the untyped writes refuse what the typed ones
// refuse, and a value of another type.
func TestTableWriteRefuses(t *testing.T) {
	db := openPolicied(velox.Postgres, &fakeconn.Exec{})
	ctx := annIn7()
	for name, f := range map[string]func() error{
		"Set of another type":  func() error { _, err := untyped.Update(db, tkTable).Set(col(Tk.Title), 1).All().Exec(ctx); return err },
		"SetNull not nullable": func() error { _, err := untyped.Update(db, tkTable).SetNull(col(Tk.Title)).All().Exec(ctx); return err },
		"Set out of range":     func() error { _, err := untyped.Update(db, tkTable).Set(99, "x").All().Exec(ctx); return err },
		"no Set":               func() error { _, err := untyped.Update(db, tkTable).All().Exec(ctx); return err },
		"update with no Where": func() error { _, err := untyped.Update(db, tkTable).Set(col(Tk.Title), "x").Exec(ctx); return err },
		"update of an invalid row": func() error {
			_, err := untyped.Update(db, tkTable).Set(col(Tk.Title), "x").Where(tkTable.Pred(col(Tk.ID), untyped.PredEQ, 1)).Exec(ctx)
			return err
		},
		"delete with no Where": func() error { _, err := untyped.Delete(db, tkTable).Exec(ctx); return err },
		"delete of an invalid row": func() error {
			_, err := untyped.Delete(db, tkTable).Where(tkTable.Pred(col(Tk.ID), untyped.PredEQ, 1)).Exec(ctx)
			return err
		},
		"insert of nil": func() error { return untyped.Insert(db, tkTable, nil).Exec(ctx) },
		"insert of a zero Table": func() error {
			x := testmodel.Ticket{}
			return untyped.Insert(db, untyped.Table{}, &x).Exec(ctx)
		},
		"insert the policy denies": func() error {
			x := testmodel.Ticket{Owner: "bob", Title: "t"}
			return untyped.Insert(db, tkTable, &x).Exec(ctx)
		},
		"update of a zero Table": func() error {
			_, err := untyped.Update(db, untyped.Table{}).Set(0, int64(1)).All().Exec(ctx)
			return err
		},
		"delete of a zero Table": func() error { _, err := untyped.Delete(db, untyped.Table{}).All().Exec(ctx); return err },
	} {
		if err := f(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if s, _, err := untyped.InsertRows(db, tkTable, untyped.Rows{}).SQL(ctx); s != "" || err != nil {
		t.Errorf("InsertRows of nothing: %q %v", s, err)
	}
}

// TestTableWriteRows: on SQLite, the untyped writes write: an insert reads
// its key back and runs Invoice's hook, which fills OrgID from the scope; a
// write to a row the scope hides affects 0 rows, as the typed one does; and
// the row helpers set fields and validate.
func TestTableWriteRows(t *testing.T) {
	db, _ := eachPolicied(t)
	in7 := testmodel.Org.With(context.Background(), int64(7))
	in8 := testmodel.Org.With(context.Background(), int64(8))
	x := testmodel.Invoice{Number: "n7", Data: []byte{}}
	if err := untyped.Insert(db, invTable, &x).Exec(in7); err != nil {
		t.Fatal(err)
	}
	if x.ID == 0 || x.OrgID != 7 {
		t.Fatalf("inserted %+v", x)
	}
	id := invTable.Pred(col(I.ID), untyped.PredEQ, x.ID)
	for _, c := range []struct {
		name  string
		ctx   context.Context
		want  int
		typed func(ctx context.Context) (int, error)
		untyp func(ctx context.Context) (int, error)
	}{
		{"update, hidden", in8, 0,
			func(ctx context.Context) (int, error) {
				return db.Update[testmodel.Invoice]().Set(I.Note.Set("x")).Where(I.ID.EQ(x.ID)).Exec(ctx)
			},
			func(ctx context.Context) (int, error) {
				return untyped.Update(db, invTable).Set(col(I.Note), "x").Where(id).Exec(ctx)
			}},
		{"delete, hidden", in8, 0,
			func(ctx context.Context) (int, error) {
				return db.Delete[testmodel.Invoice]().Where(I.ID.EQ(x.ID)).Exec(ctx)
			},
			func(ctx context.Context) (int, error) { return untyped.Delete(db, invTable).Where(id).Exec(ctx) }},
		{"update, seen", in7, 1,
			func(ctx context.Context) (int, error) {
				return db.Update[testmodel.Invoice]().Set(I.Note.Set("x")).Where(I.ID.EQ(x.ID)).Exec(ctx)
			},
			func(ctx context.Context) (int, error) {
				return untyped.Update(db, invTable).Set(col(I.Note), "y").Where(id).Exec(ctx)
			}},
	} {
		tn, terr := c.typed(c.ctx)
		un, uerr := c.untyp(c.ctx)
		if terr != nil || uerr != nil || tn != c.want || un != c.want {
			t.Errorf("%s: typed %d %v, untyped %d %v, want %d", c.name, tn, terr, un, uerr, c.want)
		}
	}
	got, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(x.ID)).One(in7)
	if err != nil || got.Note != "y" {
		t.Errorf("after the updates: %+v %v", got, err)
	}
	if n, err := untyped.Delete(db, invTable).Where(id).Exec(in7); n != 1 || err != nil {
		t.Errorf("delete, seen: %d %v", n, err)
	}
	// InsertRows of rows NewRows made, their fields set by SetField.
	rows := invTable.NewRows(2)
	for i := range rows.Len() {
		p := rows.At(i)
		for c, v := range map[int]any{col(I.Number): "r", col(I.Total): 2.5, col(I.Data): []byte{}, col(I.PaidAt): nil} {
			if err := invTable.SetField(p, c, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := untyped.InsertRows(db, invTable, rows).Exec(in7); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Query[testmodel.Invoice]().Where(I.Number.EQ("r"), I.OrgID.EQ(7)).Count(in7); n != 2 || err != nil {
		t.Errorf("InsertRows: %d rows, %v", n, err)
	}
	if invTable.SetField(rows.At(0), col(I.Number), nil) == nil || invTable.SetField(rows.At(0), col(I.Number), 1) == nil ||
		invTable.SetField(rows.At(0), 99, "x") == nil {
		t.Error("SetField of a wrong value: no error")
	}
	if err := invTable.SetField(rows.At(0), col(I.PaidAt), x.CreatedAt); err != nil || rows.At(0).(*testmodel.Invoice).PaidAt == nil {
		t.Errorf("SetField of a nullable field: %v", err)
	}
	if invTable.NewRows(0).Len() != 0 || (untyped.Table{}).NewRows(2).Len() != 0 {
		t.Error("NewRows of none")
	}
	// Validate and ValidateSet, untyped, are the typed ones'.
	tx := testmodel.Ticket{}
	if (tkTable.Validate(&tx, col(Tk.Title)) == nil) != (velox.Validate(&tx, Tk.Title) == nil) {
		t.Error("Validate differs")
	}
	a, err := tkTable.Assign(col(Tk.Title), "")
	if err != nil || (tkTable.ValidateSet(a) == nil) != (velox.ValidateSet[testmodel.Ticket](Tk.Title.Set("")) == nil) {
		t.Errorf("ValidateSet differs: %v", err)
	}
	if (untyped.Table{}).Validate(nil) == nil || (untyped.Table{}).ValidateSet() == nil {
		t.Error("the zero Table validates")
	}
}
