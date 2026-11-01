package conn

import (
	"strings"
	"testing"
	"time"
)

// TestSQLiteTimeRefusesOtherTypes: a value that is no time is an error naming it.
func TestSQLiteTimeRefusesOtherTypes(t *testing.T) {
	var dst time.Time
	err := sqliteTime{dst: &dst}.Scan(42)
	if err == nil || !strings.Contains(err.Error(), "int") {
		t.Errorf("Scan(42) = %v", err)
	}
}

// TestSQLiteRowsWithoutColumns: rows that cannot name their columns say so.
func TestSQLiteRowsWithoutColumns(t *testing.T) {
	type bare struct{ Rows }
	_, err := sqliteRows{Rows: bare{}}.Columns()
	if err == nil || !strings.Contains(err.Error(), "do not name their columns") {
		t.Errorf("Columns() = %v", err)
	}
}

// TestSQLiteTimeTakesATime: a driver that parsed the column itself gives a time.
func TestSQLiteTimeTakesATime(t *testing.T) {
	want := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	var dst time.Time
	if err := (sqliteTime{dst: &dst}).Scan(want); err != nil || !dst.Equal(want) {
		t.Errorf("Scan = %v, %v; want %v", dst, err, want)
	}
}
