package engine

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"
	"unsafe"

	"github.com/syssam/velox/internal/fakeconn"
)

// TestTrackedWriteRefuses: a recorded write whose rows cannot be found again
// is refused before anything is sent.
func TestTrackedWriteRefuses(t *testing.T) {
	keyless := &EntityInfo{Name: "velox.log", Columns: []ColumnInfo{{Name: "line"}}}
	keyed := &EntityInfo{Name: "velox.inv", Columns: []ColumnInfo{{Name: "id"}}, PrimaryKey: []int{0}}
	for _, c := range []struct {
		name string
		st   *Statement
		want string
	}{
		{"no key", &Statement{Op: OpDelete, Entity: keyless}, "needs a primary key"},
		{"key by Add", &Statement{Op: OpUpdate, Entity: keyed, Set: []Assignment{{Column: 0, Op: AssignAdd, Value: int64(1)}}}, "by Add or Sub"},
	} {
		err := trackedWrite(t.Context(), nil, &MutationState{}, nil, c.st)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want %q", c.name, err, c.want)
		}
	}
}

// TestBatchTruncatedBySavepoint: in a transaction velox opened for a
// recorded write, the Changes emitted in a savepoint that rolls back, or
// whose RELEASE fails, are dropped; the others reach the sink in one call,
// before COMMIT, with a handle on the transaction that does not record.
func TestBatchTruncatedBySavepoint(t *testing.T) {
	ctx := context.Background()
	marks := make([]int, 5)
	row := func(i int) ChangeRow { return ChangeRow{Op: OpDelete, Old: unsafe.Pointer(&marks[i])} }
	var got []int
	var calls int
	var log []string
	e := &fakeconn.Exec{}
	rec := &Recording{Sink: func(_ context.Context, tx *DB, rows []ChangeRow) error {
		calls++
		if !tx.InTx || tx.rec != nil || tx.batch != nil {
			t.Errorf("the sink's tx: InTx %v, recording %v, batch %v; want in the transaction, not recording", tx.InTx, tx.rec, tx.batch)
		}
		for _, r := range rows {
			got = append(got, int((uintptr(r.Old)-uintptr(unsafe.Pointer(&marks[0])))/unsafe.Sizeof(marks[0])))
		}
		log = slices.Clone(e.Log)
		return nil
	}}
	db := (&DB{D: Postgres, Ex: e, Cfg: newConfig(nil)}).Record(rec)
	undo := errors.New("undo")
	err := ownTx(ctx, db, func(ctx context.Context, tx *DB) error {
		if err := emit(tx, []ChangeRow{row(0)}); err != nil {
			return err
		}
		if err := tx.Tx(ctx, func(ctx context.Context, sp *DB) error {
			_ = emit(sp, []ChangeRow{row(1)})
			return undo
		}); !errors.Is(err, undo) {
			t.Errorf("a savepoint that fails: %v", err)
		}
		if err := tx.Tx(ctx, func(ctx context.Context, sp *DB) error {
			_ = emit(sp, []ChangeRow{row(2)})
			e.Errs = []error{errors.New("release refused")} // the RELEASE
			return nil
		}); err == nil {
			t.Error("a savepoint whose RELEASE fails succeeded")
		}
		if err := tx.Tx(ctx, func(ctx context.Context, sp *DB) error {
			return emit(sp, []ChangeRow{row(3)})
		}); err != nil {
			return err
		}
		return emit(tx, []ChangeRow{row(4)})
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !slices.Equal(got, []int{0, 3, 4}) {
		t.Errorf("the sink got %d calls, rows %v; want one call with rows 0, 3 and 4", calls, got)
	}
	if slices.Contains(log, "COMMIT") || !slices.Contains(log, "BEGIN") {
		t.Errorf("the sink ran after %q, want inside the transaction, before COMMIT", log)
	}
	if e.Log[len(e.Log)-1] != "COMMIT" {
		t.Errorf("the transaction ended with %q, want COMMIT", e.Log[len(e.Log)-1])
	}
}

// TestEmitOutsideBatch: a recorded write's emit outside the frame of its
// write is an internal error, not a lost Change.
func TestEmitOutsideBatch(t *testing.T) {
	db := (&DB{D: Postgres, Cfg: newConfig(nil)}).Record(&Recording{})
	if err := emit(db, []ChangeRow{{Op: OpDelete}}); err == nil {
		t.Error("emit outside a batch succeeded")
	}
	if err := emit(&DB{}, []ChangeRow{{Op: OpDelete}}); err != nil {
		t.Errorf("emit on a handle that does not record: %v", err)
	}
}

// TestAdoptedTxKeepsNothing: in a transaction the caller began (UseTx),
// whose end velox never sees, a Recorder's rows go through Sink and the
// transaction keeps no id to drop: kept stays empty.
func TestAdoptedTxKeepsNothing(t *testing.T) {
	ctx := context.Background()
	pool := sql.OpenDB(nopConnector{})
	t.Cleanup(func() { _ = pool.Close() })
	var sunk int
	rec := &Recording{
		Sink: func(_ context.Context, _ *DB, rows []ChangeRow) error { sunk += len(rows); return nil },
		Keep: func(ChangeRow) uint64 { t.Error("Keep in an adopted transaction"); return 0 },
		Drop: func([]uint64) { t.Error("Drop in an adopted transaction") },
	}
	db := Open(SQLite, pool, nil)
	tx, err := UseTx(db.Record(rec), &sql.Tx{})
	if err != nil {
		t.Fatal(err)
	}
	b := &batch{}
	tx.batch = b
	if err := emit(tx, []ChangeRow{{Op: OpDelete}, {Op: OpDelete}}); err != nil {
		t.Fatal(err)
	}
	if err := b.flush(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if sunk != 2 || len(tx.txRoot.kept) != 0 {
		t.Errorf("sink got %d rows, kept %d; want 2 and none", sunk, len(tx.txRoot.kept))
	}
}

// decStr, decInt and decStringer are decimals as a driver.Valuer of text,
// a Valuer of an int64, and a fmt.Stringer.
type (
	decStr      string
	decInt      int64
	decStringer struct{ s string }
)

func (d decStr) Value() (driver.Value, error) { return string(d), nil }
func (d decInt) Value() (driver.Value, error) { return int64(d), nil }
func (d decStringer) String() string          { return d.s }

// TestEqualDecimal: a decimal column's values are equal when they are the
// same number, read from a Valuer's text or int64, or a Stringer's text;
// a value that reads as no number falls back to EqualValues.
func TestEqualDecimal(t *testing.T) {
	dec := &ColumnInfo{decimal: [2]uint8{10, 2}}
	plainCol := &ColumnInfo{}
	for _, c := range []struct {
		a, b any
		want bool
	}{
		{decStr("1.50"), decStr("1.5"), true},
		{decStr("1.50"), decStr("1.51"), false},
		{decStr(" 2 "), decStr("2.000"), true},
		{decInt(3), decInt(3), true},
		{decStringer{"0.10"}, decStringer{"0.1"}, true},
		{decStringer{"x"}, decStringer{"x"}, true},
		{decStringer{"x"}, decStringer{"y"}, false},
		{nil, nil, true},
		{decStr("0"), nil, false},
	} {
		if got := equalColumn(dec, c.a, c.b); got != c.want {
			t.Errorf("equalColumn(decimal, %v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
	if equalColumn(plainCol, decStr("1.50"), decStr("1.5")) {
		t.Error("a column that is no decimal compares text by number")
	}
}
