package apitest

import (
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// input is a velox.Input of invoices: its setters, or its error.
type input struct {
	set []velox.Setter[testmodel.Invoice]
	err error
}

func (in input) Setters() ([]velox.Setter[testmodel.Invoice], error) { return in.set, in.err }

// TestSetInputWritesItsSetters: SetInput adds the input's setters, so its
// SQL is Set's.
func TestSetInputWritesItsSetters(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	I := testmodel.Invoices
	in := input{set: []velox.Setter[testmodel.Invoice]{I.Number.Set("n"), I.Note.Set("x")}}

	upd := db.Update[testmodel.Invoice]().Where(I.ID.EQ(1))
	want, wargs, err := upd.Set(in.set...).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, gargs, err := upd.SetInput(in).SQL(ctx)
	if err != nil || got != want || len(gargs) != len(wargs) {
		t.Errorf("Update SetInput: %q %v %v, want %q %v", got, gargs, err, want, wargs)
	}

	want, wargs, err = db.Insert(&testmodel.Invoice{ID: 1}).Set(in.set...).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, gargs, err = db.Insert(&testmodel.Invoice{ID: 1}).SetInput(in).SQL(ctx)
	if err != nil || got != want || len(gargs) != len(wargs) {
		t.Errorf("Insert SetInput: %q %v %v, want %q %v", got, gargs, err, want, wargs)
	}
}

// TestSetInputKeepsItsError: an input whose Setters fails sends nothing,
// and Exec, One and SQL return its error unchanged, the first of two.
func TestSetInputKeepsItsError(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	I := testmodel.Invoices
	bad, worse := errors.New("bad input"), errors.New("worse input")
	upd := db.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).SetInput(input{err: bad}).SetInput(input{err: worse}).Set(I.Note.Set("x"))
	ins := db.Insert(&testmodel.Invoice{}).SetInput(input{err: bad}).SetInput(input{err: worse})
	for name, run := range map[string]func() error{
		"Update.Exec": func() error { _, err := upd.Exec(ctx); return err },
		"Update.One":  func() error { _, err := upd.One(ctx); return err },
		"Update.SQL":  func() error { _, _, err := upd.SQL(ctx); return err },
		"Insert.Exec": func() error { return ins.Exec(ctx) },
		"Insert.SQL":  func() error { _, _, err := ins.SQL(ctx); return err },
	} {
		if err := run(); err != bad { //nolint:errorlint // unchanged, not wrapped
			t.Errorf("%s: %v, want the input's error as it is", name, err)
		}
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}
