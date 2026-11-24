package apitest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestValuesUnderPolicyAndFilter: Values and IDs read one column with the
// query's conditions, order and limit, the Read policy's scope and a
// WithFilter filter, and scan it into the handle's value type.
func TestValuesUnderPolicyAndFilter(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{"a"}, {"b"}}}
	db := openPolicied(velox.Postgres, e, velox.WithFilter(func(_ context.Context, _ *velox.DB, fe velox.FilterEntity, _ velox.Op) (velox.Predicate, error) {
		if strings.HasSuffix(fe.Name(), "Ticket") {
			return velox.FieldEQ("done", false), nil
		}
		return velox.Allow(), nil
	}))
	q := db.Query[testmodel.Ticket]().Where(Tk.InvoiceID.EQ(9)).OrderBy(Tk.Title.Asc()).Limit(5).Offset(2)
	titles, err := q.Values(asUser("ann"), Tk.Title)
	if err != nil || !slices.Equal(titles, []string{"a", "b"}) {
		t.Fatalf("%v, %v", titles, err)
	}
	wantSent(t, e, `SELECT "title" FROM "tickets" WHERE "invoice_id" = $1 AND NOT "done" AND "org_id" = $2 AND "owner" = $3 ORDER BY "title" LIMIT 5 OFFSET $4`, int64(9), int64(1), "ann", 2)

	e.Rows = [][]any{{int64(4)}, {int64(7)}}
	ids, err := db.Query[testmodel.Ticket]().AllowUnbounded().IDs(asUser("ann"))
	if err != nil || !slices.Equal(ids, []int64{4, 7}) {
		t.Fatalf("IDs: %v, %v", ids, err)
	}
	if !strings.HasPrefix(e.SQL, `SELECT "id" FROM "tickets" WHERE NOT "done" AND "org_id" = $1 AND "owner" = $2`) {
		t.Errorf("IDs sent %s", e.SQL)
	}

	// WithoutPolicy lifts the policy, not the filter.
	if _, err := db.WithoutPolicy("test").Query[testmodel.Ticket]().Limit(1).IDs(ctx); err != nil || !strings.Contains(e.SQL, `WHERE NOT "done" LIMIT`) {
		t.Errorf("WithoutPolicy: %s, %v", e.SQL, err)
	}
}

// TestValuesNullable: a nullable column's handle reads pointers, nil for NULL.
func TestValuesNullable(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(3)}, {nil}}}
	db := openFake(velox.Postgres, e)
	ps, err := db.Query[testmodel.Ticket]().Limit(2).Values(ctx, Tk.ParentID)
	if err != nil || len(ps) != 2 || ps[0] == nil || *ps[0] != 3 || ps[1] != nil {
		t.Fatalf("%v, %v", ps, err)
	}
}

// TestValuesRefusals: no Limit, a wrong key type, a join and a projection
// fail before anything is sent.
func TestValuesRefusals(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Ticket]().Values(ctx, Tk.Title); !errors.Is(err, velox.ErrUnbounded) {
		t.Errorf("Values without Limit: %v", err)
	}
	if _, err := db.Query[testmodel.Ticket]().IDs(ctx); !errors.Is(err, velox.ErrUnbounded) {
		t.Errorf("IDs without Limit: %v", err)
	}
	if _, err := db.Query[testmodel.Ticket]().Join(Tk.Invoice).Limit(1).Values(ctx, Tk.Title); err == nil || !strings.Contains(err.Error(), "cannot join") {
		t.Errorf("a join: %v", err)
	}
	if _, err := db.Query[testmodel.Ticket]().Select(Tk.Title).Limit(1).Values(ctx, Tk.Title); err == nil || !strings.Contains(err.Error(), "projection") {
		t.Errorf("a projection: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// TestValuesOnSQLite reads real rows, in order, past a With that it ignores.
func TestValuesOnSQLite(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	numbers, err := db.Query[testmodel.Invoice]().OrderBy(I.Number.Desc()).Limit(2).Values(ctx, I.Number)
	if err != nil || !slices.Equal(numbers, []string{"c", "b"}) {
		t.Errorf("Values: %v, %v", numbers, err)
	}
	ids, err := db.Query[testmodel.Invoice]().Where(I.ID.GT(1)).OrderBy(I.ID.Asc()).AllowUnbounded().IDs(ctx)
	if err != nil || !slices.Equal(ids, []int64{2, 3}) {
		t.Errorf("IDs: %v, %v", ids, err)
	}
	none, err := db.Query[testmodel.Invoice]().Where(I.ID.GT(9)).Limit(1).IDs(ctx)
	if err != nil || len(none) != 0 {
		t.Errorf("no rows: %v, %v", none, err)
	}
}
