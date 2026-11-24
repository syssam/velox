package apitest

import (
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestValuesAllocationsPerRow: Values and IDs make no allocation per row for
// a value that needs none (an int64): reading 100 rows costs no more
// allocations than reading 1, the values' one backing array included.
func TestValuesAllocationsPerRow(t *testing.T) {
	rows := func(n int) [][]any {
		out := make([][]any, n)
		for i := range out {
			out[i] = []any{int64(i + 1)}
		}
		return out
	}
	allocs := func(n int, read func(db *velox.DB) error) float64 {
		db := openFake(velox.Postgres, &fakeconn.Exec{Rows: rows(n)})
		run := func() {
			if err := read(db); err != nil {
				t.Fatal(err)
			}
		}
		run() // fill the render cache
		return testing.AllocsPerRun(100, run)
	}
	for name, read := range map[string]func(db *velox.DB) error{
		"Values": func(db *velox.DB) error {
			_, err := db.Query[testmodel.Invoice]().Limit(100).Values(ctx, I.OrgID)
			return err
		},
		"IDs": func(db *velox.DB) error {
			_, err := db.Query[testmodel.Invoice]().Limit(100).IDs[int64](ctx)
			return err
		},
	} {
		one, hundred := allocs(1, read), allocs(100, read)
		t.Logf("%s: %.0f allocations for 1 row, %.0f for 100", name, one, hundred)
		if hundred > one {
			t.Errorf("%s makes %.0f allocations for 100 rows and %.0f for 1: it allocates per row", name, hundred, one)
		}
	}
}
