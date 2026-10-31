package engine_test

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestOneRowUsesTheCache: the budget above assumes the shape is cached.
func TestOneRowUsesTheCache(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}})
	for range 3 {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := cacheSize(db); n != 1 {
		t.Errorf("cache holds %d shapes after one shape ran three times", n)
	}
}

// TestRewriteCanReplaceTheEntity: a rewrite hook may point the statement at
// its own EntityInfo (a per-tenant table, say). The SQL sent must name that
// entity's table, even when the garbage collector reuses the memory of an
// EntityInfo an earlier statement used.
func TestRewriteCanReplaceTheEntity(t *testing.T) {
	e := &fakeconn.Exec{}
	tables := [2]string{"invoices_a", "invoices_b"}
	var chose string
	call := 0
	perTenant := func(_ context.Context, st *velox.Statement) error {
		info := *st.Entity
		info.Table = tables[call%2]
		chose = info.Table
		call++
		st.Entity = &info
		return nil
	}
	db := openFake(velox.Postgres, e, velox.WithRewrite(perTenant))
	q := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1)
	wrong := 0
	for i := range 3000 {
		if i%7 == 0 { // an odd period, so a reused address comes back with the other table
			runtime.GC()
		}
		if _, err := q.All(ctx); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(e.SQL, `FROM "`+chose+`"`) {
			if wrong == 0 {
				t.Errorf("call %d: the hook chose %s, the SQL sent was %s", i, chose, e.SQL)
			}
			wrong++
		}
	}
	if wrong > 0 {
		t.Errorf("%d of 3000 queries named the wrong table", wrong)
	}

	// The statement whose entity a hook replaced is rendered without the
	// cache: running it again does not grow the cache.
	before := cacheSize(db)
	for range 3 {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if after := cacheSize(db); after != before {
		t.Errorf("cache grew from %d to %d shapes for statements whose entity a hook replaced", before, after)
	}
}
