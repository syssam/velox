package apitest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
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
	if err := rc.Sink(context.Background(), nil, []engine.ChangeRow{{}}); err != nil {
		t.Fatal(err)
	}
	return r.Len() == before+1
}

// ignoresOne reports whether m names one column of one entity, entity.
func ignoresOne(m map[*engine.EntityInfo][]int, entity string) bool {
	for in, cols := range m {
		return len(m) == 1 && in.Name == entity && len(cols) == 1
	}
	return false
}

// TestRecordHandleOnly: Record returns a new handle; the one it was called
// on records nothing and sends what it sent before. The recording handle's
// insert returns every column, the row as stored.
func TestRecordHandleOnly(t *testing.T) {
	ctx := context.Background()
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	if recEngine(db).Recording() != nil {
		t.Fatal("a DB records before Record")
	}
	const plain = `INSERT INTO "dnotes" ("owner") VALUES ($1) RETURNING "id"`
	if err := db.Insert(&fixture.Dnote{Owner: "x"}).Exec(ctx); err != nil || e.SQL != plain {
		t.Fatalf("a plain handle sent %s, %v; want %s", e.SQL, err, plain)
	}

	var rec velox.Recorder
	rdb := db.Record(&rec)
	if recEngine(db).Recording() != nil {
		t.Error("Record changed the handle it was called on")
	}
	if !recInto(t, rdb, &rec) {
		t.Error("the handle Record returns does not record into its sink")
	}
	e.Rows = [][]any{{int64(1), "x"}}
	if err := rdb.Insert(&fixture.Dnote{Owner: "x"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `INSERT INTO "dnotes" ("owner") VALUES ($1) RETURNING "id", "owner"`; e.SQL != want {
		t.Errorf("a recording handle sent\n%s\nwant\n%s", e.SQL, want)
	}
	e.Log = nil
	rec.Reset() // of recInto's Change
	if _, err := db.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Set(2)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := []string{`UPDATE "invoices" SET "total" = $1, "updated_at" = $2 WHERE "id" = $3`}; !slices.Equal(e.Log, want) {
		t.Errorf("the handle Record was called on sent %q, want %q", e.Log, want)
	}
	if rec.Len() != 0 {
		t.Errorf("%d Changes from writes on the plain handle", rec.Len())
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
	// A Recorder's Keep and Drop are set, a RecordFunc's are not.
	if rc := recEngine(rdb).Recording(); rc.Keep == nil || rc.Drop == nil {
		t.Error("a Recorder sink has no Keep or Drop")
	}
	fn := db.Record(velox.RecordFunc(func(context.Context, *velox.DB, []velox.Change) error { return nil }), velox.RecordIgnore(I.Note))
	if rc := recEngine(fn).Recording(); rc.Keep != nil || rc.Drop != nil || !ignoresOne(rc.Ignore, "testmodel.Invoice") {
		t.Errorf("a RecordFunc's recording is %+v", rc)
	}
}

func TestRecordOuterHandleInTxIsRefused(t *testing.T) {
	ctx := context.Background()
	e := &fakeconn.Exec{Affected: 1}
	var rec velox.Recorder
	db := openFake(velox.Postgres, e)
	rdb := db.Record(&rec)
	var err error
	if txErr := db.Tx(ctx, func(ctx context.Context, _ *velox.DB) error {
		// rdb records, the DB that began the transaction does not: rdb is
		// not that DB, so it does not join its transaction.
		err = rdb.Insert(&fixture.Dnote{Owner: "x"}).Exec(ctx)
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

// rhook is an entity for a hook given with WithHooks.
type rhook struct {
	ID    int64
	Owner string
}

// keepDB is a hook that stores in *db the DB its write runs on.
func keepDB(db **velox.DB) velox.Hook {
	return func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			*db = m.DB()
			return next.Mutate(ctx, m)
		})
	}
}

var rhookMeta velox.MetaOnce[rhook]

func (*rhook) VeloxMeta() *velox.Meta[rhook] {
	return rhookMeta.Load(func() velox.Descriptor[rhook] {
		return velox.Descriptor[rhook]{
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
	e := &fakeconn.Exec{Affected: 1, Rows: [][]any{{int64(1), "x"}}}
	var rec velox.Recorder
	var rhookDB *velox.DB
	rdb := openFake(velox.Postgres, e, velox.WithHooks[rhook](keepDB(&rhookDB))).Record(&rec)

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

// rlog is the entity a hook on rhook writes through Mutation.DB.
type rlog struct {
	ID    int64
	Owner string
}

var rlogMeta velox.MetaOnce[rlog]

func (*rlog) VeloxMeta() *velox.Meta[rlog] {
	return rlogMeta.Load(func() velox.Descriptor[rlog] {
		return velox.Descriptor[rlog]{
			Table:      "rlogs",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Owner", Name: "owner"}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *rlog, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.Owner
			},
		}
	})
}

// TestRecordHookWriteOwnSinkCall: a hook runs before its write's frame
// opens, so its write through m.DB() is a recorded write of its own, with
// its own sink call, in the order the hook makes them, outside a
// transaction and in one (ADR 0094 rule 2).
func TestRecordHookWriteOwnSinkCall(t *testing.T) {
	for _, c := range []struct {
		after, inTx bool
	}{{false, false}, {true, false}, {false, true}, {true, true}} {
		after := c.after
		logWrite := func(next velox.Mutator) velox.Mutator {
			return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
				if after {
					if err := next.Mutate(ctx, m); err != nil {
						return err
					}
				}
				if err := m.DB().Insert(&rlog{Owner: "log"}).Exec(ctx); err != nil {
					return err
				}
				if after {
					return nil
				}
				return next.Mutate(ctx, m)
			})
		}
		e := &fakeconn.Exec{Affected: 1, Rows: [][]any{{int64(1), "x"}}}
		var calls [][]string
		sink := velox.RecordFunc(func(_ context.Context, _ *velox.DB, cs []velox.Change) error {
			var names []string
			for _, c := range cs {
				names = append(names, c.Entity)
			}
			calls = append(calls, names)
			return nil
		})
		rdb := openFake(velox.Postgres, e, velox.WithHooks[rhook](logWrite)).Record(sink)
		var err error
		if c.inTx {
			err = rdb.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				return tx.Insert(&rhook{Owner: "x"}).Exec(ctx)
			})
		} else {
			err = rdb.Insert(&rhook{Owner: "x"}).Exec(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
		want := [][]string{{"rlog"}, {"rhook"}}
		if after {
			want = [][]string{{"rhook"}, {"rlog"}}
		}
		if !slices.EqualFunc(calls, want, slices.Equal) {
			t.Errorf("%+v: sink calls %v, want %v; sent %q", c, calls, want, e.Log)
		}
	}
}
