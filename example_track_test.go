package velox_test

import (
	"context"
	"fmt"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// An audit hook asks for the rows' old values with Track before next, and
// records what changed from Changes after it: for a bulk update, one Change
// per row. Here it tracks the total alone; with no names it would track
// every column the update assigns.
func ExampleMutation_Track() {
	audit := velox.On(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			if err := m.Track("total"); err != nil {
				return err
			}
			if err := next.Mutate(ctx, m); err != nil {
				return err
			}
			for c := range m.Changes() {
				for _, f := range c.Changed() {
					fmt.Printf("invoice %v: %s %v -> %v\n", c.Key[0], f.Name, f.Old, f.New)
				}
			}
			return nil
		})
	}, velox.OpUpdate, velox.OpDelete)
	db, err := velox.Connect("sqlite", "file::memory:", velox.WithHooks[testmodel.Invoice](audit))
	if err != nil {
		panic(err)
	}
	if _, err := velox.Pool(db).Exec(exampleDDL); err != nil {
		panic(err)
	}
	ctx := testmodel.Org.With(context.Background(), 1)
	for _, total := range []float64{10, 20} {
		if err := db.Insert(&testmodel.Invoice{Number: fmt.Sprint(total), Total: total, Data: []byte{}}).Exec(ctx); err != nil {
			panic(err)
		}
	}
	I := testmodel.Invoices
	if _, err := db.Update[testmodel.Invoice]().Where(I.Total.LT(100)).Set(I.Total.Add(5)).Exec(ctx); err != nil {
		panic(err)
	}
	// Output:
	// invoice 1: total 10 -> 15
	// invoice 2: total 20 -> 25
}
