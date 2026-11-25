package apitest

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox"
)

type valueKey struct{}

type otherKey struct{}

// TestDBValue: WithValue attaches a value that Value reads back, nil for the zero DB, the later
// of two for one key; every DB derived from the DB has it: WithoutPolicy's
// and Record's.
func TestDBValue(t *testing.T) {
	db := velox.Open(velox.SQLite, nil, velox.WithValue(valueKey{}, 1), velox.WithValue(valueKey{}, 2))
	if got := velox.Value(db, valueKey{}); got != 2 {
		t.Errorf("Value = %v, want 2, the later", got)
	}
	if got := velox.Value(db, otherKey{}); got != nil {
		t.Errorf("Value of a key never given = %v, want nil", got)
	}
	if got := velox.Value(db.WithoutPolicy("test"), valueKey{}); got != 2 {
		t.Errorf("WithoutPolicy's Value = %v, want 2", got)
	}
	sink := velox.RecordFunc(func(context.Context, *velox.DB, []velox.Change) error { return nil })
	if got := velox.Value(db.Record(sink), valueKey{}); got != 2 {
		t.Errorf("Record's Value = %v, want 2", got)
	}
	if got := velox.Value(velox.Open(velox.SQLite, nil), valueKey{}); got != nil {
		t.Errorf("Value of a DB with no values = %v, want nil", got)
	}
	if got := velox.Value(&velox.DB{}, valueKey{}); got != nil {
		t.Errorf("Value of the zero DB = %v, want nil", got)
	}
}

// TestDBValueTx: a transaction's DB has the values of the DB it began on.
func TestDBValueTx(t *testing.T) {
	db, err := velox.Connect("sqlite", ":memory:", velox.WithValue(valueKey{}, "v"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	err = db.Tx(context.Background(), func(_ context.Context, tx *velox.DB) error {
		if got := velox.Value(tx, valueKey{}); got != "v" {
			t.Errorf("the transaction's Value = %v, want v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestWithValueRefusesBadKeys: a nil key and a key that is not comparable
// panic, naming the problem.
func TestWithValueRefusesBadKeys(t *testing.T) {
	if got := panicText(func() { velox.WithValue(nil, 1) }); !strings.Contains(got, "nil key") {
		t.Errorf("WithValue(nil) panicked with %q", got)
	}
	if got := panicText(func() { velox.WithValue([]int{1}, 1) }); !strings.Contains(got, "not comparable") {
		t.Errorf("WithValue([]int) panicked with %q", got)
	}
}
