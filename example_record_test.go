package velox_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// auditRow is what an application's audit log keeps for one changed row.
type auditRow struct {
	Entity   string
	Key      any
	Op       velox.Op
	Old, New []byte // the rows as JSON; null for an insert's old row and a delete's new row
	Changed  []string
}

// A recording handle reports every row its writes change, bulk updates and
// deletes included, with the row before and after the write. Here the
// changes go to a Recorder, and from it to an audit batch, as for any
// entity; I.Total.Old and I.Total.New read one column typed. RecordIgnore
// leaves updated_at, which every update sets, out of Diff.
func ExampleDB_Record() {
	db := exampleDB()
	ctx := testmodel.Org.With(context.Background(), 1)
	for _, total := range []float64{10, 20} {
		if err := db.Insert(&testmodel.Invoice{Number: fmt.Sprint(total), Total: total, Data: []byte{}}).Exec(ctx); err != nil {
			panic(err)
		}
	}

	var rec velox.Recorder
	rdb := db.Record(&rec, velox.RecordIgnore("updated_at"))
	I := testmodel.Invoices
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.Total.LT(100)).Set(I.Total.Add(5)).Exec(ctx); err != nil {
		panic(err)
	}
	if _, err := rdb.Delete[testmodel.Invoice]().Where(I.Total.GT(20)).Exec(ctx); err != nil {
		panic(err)
	}
	if err := rdb.Insert(&testmodel.Invoice{Number: "30", Total: 30, Data: []byte{}}).Exec(ctx); err != nil {
		panic(err)
	}

	var batch []auditRow
	for c := range rec.All() {
		a := auditRow{Entity: c.Entity, Key: c.Key, Op: c.Op}
		var err error
		if a.Old, err = json.Marshal(c.OldRow()); err != nil {
			panic(err)
		}
		if a.New, err = json.Marshal(c.NewRow()); err != nil {
			panic(err)
		}
		for _, f := range c.Diff() {
			a.Changed = append(a.Changed, f.Name)
		}
		batch = append(batch, a)

		switch c.Op {
		case velox.OpUpdate:
			fmt.Println(c.Entity, c.Key, c.Op, strings.Join(a.Changed, ","), I.Total.Old(c), "->", I.Total.New(c))
		case velox.OpDelete:
			fmt.Println(c.Entity, c.Key, c.Op, "new row", string(a.New))
		case velox.OpInsert:
			fmt.Println(c.Entity, c.Key, c.Op, "old row", string(a.Old))
		}
	}
	fmt.Println(len(batch), "audit rows")
	// Output:
	// Invoice 1 update total 10 -> 15
	// Invoice 2 update total 20 -> 25
	// Invoice 2 delete new row null
	// Invoice 3 insert old row null
	// 4 audit rows
}
