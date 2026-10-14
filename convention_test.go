package velox_test

import (
	"context"
	"errors"
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
	err = db.Save(ctx, &inv, I.Number) // FakeExec reports 0 rows affected
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
