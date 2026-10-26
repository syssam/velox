package velox_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// TestReturnedErrorsNameTheEntityOnce pins the error convention: one
// "velox: <Entity>: " prefix, then the sentinel's phrase.
func TestReturnedErrorsNameTheEntityOnce(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)

	_, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
	want(t, err, velox.ErrNotFound, "velox: testmodel.Invoice: not found")

	e.Rows = [][]any{invoiceRow(1), invoiceRow(2)}
	_, err = db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).One(ctx)
	want(t, err, velox.ErrNotSingular, "velox: testmodel.Invoice: more than one row")

	inv := testmodel.Invoice{ID: 7}
	err = db.Save(&inv, I.Number).Exec(ctx) // FakeExec reports 0 rows affected
	want(t, err, velox.ErrNotFound, "velox: testmodel.Invoice: not found")

	refused := errors.New("refused")
	hooked := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithRewrite(func(context.Context, *velox.Statement) error { return refused }))
	_, err = hooked.Query[testmodel.Invoice]().Limit(1).All(ctx)
	want(t, err, refused, "velox: testmodel.Invoice: rewrite: refused")
}

func want(t *testing.T, err, sentinel error, text string) {
	t.Helper()
	if !errors.Is(err, sentinel) || err.Error() != text {
		t.Errorf("got %v, want %q wrapping %v", err, text, sentinel)
	}
}

// TestRewriteErrorNamesTheEntityOnce: a Rewrite hook's error is put under
// "velox: <Entity>: rewrite: ", without naming the entity twice when the
// hook's own error already starts with it.
func TestRewriteErrorNamesTheEntityOnce(t *testing.T) {
	refused := errors.New("refused")
	for _, c := range []struct {
		name  string
		err   func(*velox.Statement) error
		want  string
		wraps error // the hook's error the result wraps, if any
	}{
		{"its own entity", func(st *velox.Statement) error { return fmt.Errorf("velox: %s: %w", st.Entity.Name, refused) },
			"velox: testmodel.Invoice: rewrite: refused", refused},
		{"another entity", func(*velox.Statement) error { return errors.New("velox: other.X: nope") },
			"velox: testmodel.Invoice: rewrite: velox: other.X: nope", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := &velox.FakeExec{}
			db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { return c.err(st) }))
			_, err := db.Query[testmodel.Invoice]().Unbounded().All(ctx)
			if err == nil || err.Error() != c.want || e.Calls != 0 {
				t.Errorf("%v, calls %d; want %q", err, e.Calls, c.want)
			}
			if c.wraps != nil && !errors.Is(err, c.wraps) {
				t.Errorf("%v does not wrap the hook's error", err)
			}
		})
	}
}
