package apitest

import (
	"context"
	"errors"
	"testing"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
)

// recEngine is db's engine DB, for the Recording it holds.
func recEngine(db *velox.DB) *engine.DB { return (*engine.DB)(unsafe.Pointer(db)) }

// recInto reports whether h records into r: a Change sent to its sink lands
// in r.
func recInto(t *testing.T, h *velox.DB, r *velox.Recorder) bool {
	t.Helper()
	rc := recEngine(h).Recording()
	if rc == nil {
		return false
	}
	before := r.Len()
	if err := rc.Sink(context.Background(), engine.ChangeRow{}); err != nil {
		t.Fatal(err)
	}
	return r.Len() == before+1
}

func TestRecordHandleOnly(t *testing.T) {
	ctx := context.Background()
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	if recEngine(db).Recording() != nil {
		t.Fatal("a DB records before Record")
	}
	if err := db.Insert(&fixture.Dnote{Owner: "x"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	plain := e.SQL

	var rec velox.Recorder
	rdb := db.Record(&rec)
	if recEngine(db).Recording() != nil {
		t.Error("Record changed the handle it was called on")
	}
	if !recInto(t, rdb, &rec) {
		t.Error("the handle Record returns does not record into its sink")
	}
	if err := rdb.Insert(&fixture.Dnote{Owner: "x"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if e.SQL != plain {
		t.Errorf("a recording handle sent\n%s\nwant\n%s", e.SQL, plain)
	}
}

func TestRecordPropagates(t *testing.T) {
	ctx := context.Background()
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	var a, b velox.Recorder
	rdb := db.Record(&a)

	err := rdb.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if !recInto(t, tx, &a) {
			t.Error("a transaction does not record")
		}
		return tx.Tx(ctx, func(_ context.Context, sp *velox.DB) error {
			if !recInto(t, sp, &a) {
				t.Error("a savepoint does not record")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !recInto(t, rdb.WithoutPolicy("r"), &a) {
		t.Error("WithoutPolicy of a recording handle does not record")
	}
	if recInto(t, db.WithoutPolicy("r2"), &a) {
		t.Error("WithoutPolicy of a plain handle records")
	}
	both := db.Record(&a).Record(&b)
	if recInto(t, both, &a) {
		t.Error("the replaced sink still receives")
	}
	if !recInto(t, both, &b) {
		t.Error("the replacing sink does not receive")
	}
	// A Recorder's mark and truncate are set, a RecordFunc's are not.
	if rc := recEngine(rdb).Recording(); rc.Mark == nil || rc.Truncate == nil {
		t.Error("a Recorder sink has no Mark or Truncate")
	}
	fn := db.Record(velox.RecordFunc(func(context.Context, velox.Change) error { return nil }), velox.RecordIgnore("updated_at"))
	if rc := recEngine(fn).Recording(); rc.Mark != nil || rc.Truncate != nil || len(rc.Ignore) != 1 || rc.Ignore[0] != "updated_at" {
		t.Errorf("a RecordFunc's recording is %+v", rc)
	}
}

func TestRecordOuterHandleInTxIsRefused(t *testing.T) {
	ctx := context.Background()
	e := &fakeconn.Exec{Affected: 1}
	var rec velox.Recorder
	rdb := openFake(velox.Postgres, e).Record(&rec)
	var err error
	if txErr := rdb.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
		err = rdb.Insert(&fixture.Dnote{Owner: "x"}).Exec(ctx) // rdb, not the transaction's
		return nil
	}); txErr != nil {
		t.Fatal(txErr)
	}
	if !errors.Is(err, velox.ErrOutsideTx) {
		t.Errorf("got %v, want ErrOutsideTx", err)
	}
	if rec.Len() != 0 {
		t.Errorf("%d Changes recorded", rec.Len())
	}
}

// rhook is an entity whose hook keeps the DB its write runs on.
type rhook struct {
	ID    int64
	Owner string
}

var rhookDB *velox.DB

func (*rhook) Hooks() []velox.Hook {
	return []velox.Hook{func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			rhookDB = m.DB()
			return next.Mutate(ctx, m)
		})
	}}
}

var rhookMeta velox.MetaOnce[rhook]

func (*rhook) VeloxMeta() *velox.Meta[rhook] {
	return rhookMeta.Load(func() velox.Descriptor[rhook] {
		return velox.Descriptor[rhook]{
			Schema:     (*rhook)(nil),
			Table:      "rhooks",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Owner", Name: "owner"}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *rhook, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Owner
			},
		}
	})
}

func TestRecordHookDBRecords(t *testing.T) {
	ctx := context.Background()
	e := &fakeconn.Exec{Affected: 1, Rows: [][]any{{int64(1)}}}
	var rec velox.Recorder
	rdb := openFake(velox.Postgres, e).Record(&rec)

	rhookDB = nil
	if err := rdb.Insert(&rhook{Owner: "x"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if rhookDB == nil || !recInto(t, rhookDB, &rec) {
		t.Error("m.DB() of a write on a recording handle does not record")
	}

	rhookDB = nil
	err := rdb.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return tx.Insert(&rhook{Owner: "y"}).Exec(ctx)
	})
	if err != nil {
		t.Fatal(err)
	}
	if rhookDB == nil || !recInto(t, rhookDB, &rec) {
		t.Error("m.DB() of a write in a recording transaction does not record")
	}
}
