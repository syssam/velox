package apitest

import (
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestWriteAllocationBudgets pins, on an entity with no hooks, filters or
// recorder, the allocations of a cached update by key, at the numbers
// measured when the budgets were set: a feature that is off costs these
// paths nothing.
func TestWriteAllocationBudgets(t *testing.T) {
	measure := func(name string, allocBudget, byteBudget float64, run func() error) {
		t.Helper()
		if err := run(); err != nil { // fill the render cache
			t.Fatalf("%s: %v", name, err)
		}
		f := func() {
			if err := run(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
		allocs, bytes := testing.AllocsPerRun(200, f), bytesPerRun(f)
		t.Logf("%s makes %.0f allocations, %.0f bytes", name, allocs, bytes)
		if allocs > allocBudget {
			t.Errorf("%s makes %.0f allocations: over the budget of %.0f", name, allocs, allocBudget)
		}
		if byteBudget > 0 && bytes > byteBudget {
			t.Errorf("%s allocates %.0f bytes: over the budget of %.0f", name, bytes, byteBudget)
		}
	}
	upd := &fakeconn.Exec{}
	updates := openFake(velox.SQLite, upd)
	measure("an update by key", 5, 0, func() error {
		_, err := updates.Update[testmodel.Sample]().Set(testmodel.Samples.Name.Set("x")).Where(testmodel.Samples.ID.EQ(1)).Exec(ctx)
		upd.Log, upd.ArgLog = upd.Log[:0], upd.ArgLog[:0] // the fake keeps every statement
		return err
	})
}
