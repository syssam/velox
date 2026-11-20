package engine

import (
	"context"
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

// TestPendingTruncatedBySavepoint: in a transaction velox opened for a
// recorded write, the Changes emitted in a savepoint that rolls back, or
// whose RELEASE fails, are dropped; the others reach the sink after COMMIT.
func TestPendingTruncatedBySavepoint(t *testing.T) {
	ctx := context.Background()
	marks := make([]int, 5)
	row := func(i int) ChangeRow { return ChangeRow{Op: OpDelete, Old: unsafe.Pointer(&marks[i])} }
	var got []int
	var log []string
	e := &fakeconn.Exec{}
	rec := &Recording{Sink: func(_ context.Context, r ChangeRow) error {
		got = append(got, int((uintptr(r.Old)-uintptr(unsafe.Pointer(&marks[0])))/unsafe.Sizeof(marks[0])))
		log = append(log, e.Log[len(e.Log)-1])
		return nil
	}}
	db := (&DB{D: Postgres, Ex: e, Cfg: newConfig(nil)}).Record(rec)
	undo := errors.New("undo")
	err := ownTx(ctx, db, func(ctx context.Context, tx *DB) error {
		if err := emit(ctx, tx, []ChangeRow{row(0)}); err != nil {
			return err
		}
		if err := tx.Tx(ctx, func(ctx context.Context, sp *DB) error {
			_ = emit(ctx, sp, []ChangeRow{row(1)})
			return undo
		}); !errors.Is(err, undo) {
			t.Errorf("a savepoint that fails: %v", err)
		}
		if err := tx.Tx(ctx, func(ctx context.Context, sp *DB) error {
			_ = emit(ctx, sp, []ChangeRow{row(2)})
			e.Errs = []error{errors.New("release refused")} // the RELEASE
			return nil
		}); err == nil {
			t.Error("a savepoint whose RELEASE fails succeeded")
		}
		if err := tx.Tx(ctx, func(ctx context.Context, sp *DB) error {
			return emit(ctx, sp, []ChangeRow{row(3)})
		}); err != nil {
			return err
		}
		return emit(ctx, tx, []ChangeRow{row(4)})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []int{0, 3, 4}) {
		t.Errorf("the sink got rows %v, want 0, 3 and 4", got)
	}
	if !slices.Equal(log, []string{"COMMIT", "COMMIT", "COMMIT"}) {
		t.Errorf("the sink ran after %q, want after COMMIT", log)
	}
}
