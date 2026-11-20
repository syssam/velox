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
// changes go to a Recorder, read once the writes have committed, and from it
// to an audit batch, as for any entity; I.Total.Old and I.Total.New read one
// column typed. updated_at, which an UpdateDefault fills on every update, is
// left out of Diff without being named; RecordIgnore leaves out more.
func ExampleDB_Record() {
	db := exampleDB()
	ctx := testmodel.Org.With(context.Background(), 1)
	for _, total := range []float64{10, 20} {
		if err := db.Insert(&testmodel.Invoice{Number: fmt.Sprint(total), Total: total, Data: []byte{}}).Exec(ctx); err != nil {
			panic(err)
		}
	}

	var rec velox.Recorder
	I := testmodel.Invoices
	rdb := db.Record(&rec, velox.RecordIgnore(I.Note))
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.Total.LT(100)).Set(I.Total.Add(5), I.Note.Set("bumped")).Exec(ctx); err != nil {
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

// A sink set up once, at startup, writes its audit rows in the write's own
// transaction: tx is a handle on it that does not record, so the audit rows
// commit or roll back with the write, and are not audited themselves. The
// sink gets all of a write's Changes in one call, here the two rows of one
// bulk update. An application writes its audit entity with tx.InsertAll;
// this example, whose model has none, writes raw SQL through tx.
func ExampleRecordFunc() {
	db := exampleDB()
	if _, err := velox.Pool(db).Exec(`CREATE TABLE "audit_log" ("entity" text, "key" integer, "op" text)`); err != nil {
		panic(err)
	}
	ctx := testmodel.Org.With(context.Background(), 1)
	for _, total := range []float64{10, 20} {
		if err := db.Insert(&testmodel.Invoice{Number: fmt.Sprint(total), Total: total, Data: []byte{}}).Exec(ctx); err != nil {
			panic(err)
		}
	}

	audit := velox.RecordFunc(func(ctx context.Context, tx *velox.DB, changes []velox.Change) error {
		fmt.Println("sink:", len(changes), "changes")
		for _, c := range changes {
			if _, err := tx.Exec(ctx, "audit", `INSERT INTO "audit_log" VALUES (?, ?, ?)`, c.Entity, c.Key, c.Op.String()); err != nil {
				return err
			}
		}
		return nil
	})
	rdb := db.Record(audit) // once, at startup

	I := testmodel.Invoices
	if _, err := rdb.Update[testmodel.Invoice]().Where(I.Total.LT(100)).Set(I.Total.Add(5)).Exec(ctx); err != nil {
		panic(err)
	}
	// A transaction that rolls back takes its audit rows with it.
	_ = rdb.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if _, err := tx.Delete[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(ctx); err != nil {
			return err
		}
		return fmt.Errorf("changed my mind")
	})

	for row, err := range db.Rows(ctx, "read the audit log", `SELECT "entity", "key", "op" FROM "audit_log"`) {
		if err != nil {
			panic(err)
		}
		var entity, op string
		var key int64
		if err := row.Scan(&entity, &key, &op); err != nil {
			panic(err)
		}
		fmt.Println(entity, key, op)
	}
	// Output:
	// sink: 2 changes
	// sink: 1 changes
	// Invoice 1 update
	// Invoice 2 update
}
