package conn

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// countlessConn is a sqlConn whose statements run but whose results cannot
// report their count.
type countlessConn struct{ sqlConn }

func (countlessConn) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return countlessResult{}, nil
}

type countlessResult struct{}

func (countlessResult) LastInsertId() (int64, error) { return 0, errors.New("no id") }
func (countlessResult) RowsAffected() (int64, error) { return 0, errors.New("no count") }

// TestRowsAffectedErrorIsUnknown: a statement that ran but whose driver could
// not count its rows is not failed: its count is -1.
func TestRowsAffectedErrorIsUnknown(t *testing.T) {
	r, err := SQL{c: countlessConn{}}.Exec(context.Background(), "UPDATE t SET a = 1", nil)
	if err != nil || r.Affected != -1 {
		t.Errorf("affected %d, error %v; want -1 and no error", r.Affected, err)
	}
}

// TestSQL runs a statement of each kind on a real database, with and without
// the column scan, inside and outside a transaction.
func TestSQL(t *testing.T) {
	ctx := context.Background()
	for _, cols := range []bool{false, true} {
		db, err := sql.Open("sqlite", "file::memory:")
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })
		e := NewSQLCols(db, cols)
		if e.ColumnScanning() != cols {
			t.Errorf("ColumnScanning %v, want %v", e.ColumnScanning(), cols)
		}
		if _, err := e.Exec(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY, s TEXT)", nil); err != nil {
			t.Fatal(err)
		}
		res, err := e.Exec(ctx, "INSERT INTO t (s) VALUES (?)", []any{"a"})
		if err != nil || res.Affected != 1 || res.LastID != 1 || !res.HasLastID {
			t.Fatalf("Exec: %+v, %v", res, err)
		}

		tx, ctl, err := e.Begin(ctx, sql.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO t (s) VALUES ('rolled back')", nil); err != nil {
			t.Fatal(err)
		}
		if err := ctl.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if tx, ctl, err = e.Begin(ctx, sql.TxOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO t (s) VALUES ('b')", nil); err != nil {
			t.Fatal(err)
		}
		if err := ctl.Commit(ctx); err != nil {
			t.Fatal(err)
		}

		r, err := e.Query(ctx, "SELECT id, s FROM t ORDER BY id", nil)
		if err != nil {
			t.Fatal(err)
		}
		if RowsColumnScanning(r) != cols {
			t.Errorf("RowsColumnScanning %v, want %v", RowsColumnScanning(r), cols)
		}
		var ids []int64
		var last string
		for r.Next() {
			var id int64
			if err := r.Scan(&id, &last); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := r.Err(); err != nil {
			t.Fatal(err)
		}
		r.Close()
		if len(ids) != 2 || last != "b" {
			t.Errorf("read %v, last %q; want two rows, the last b", ids, last)
		}

		if _, err := e.Query(ctx, "SELECT nope FROM t", nil); err == nil {
			t.Error("Query of a missing column: no error")
		}
		if _, err := e.Exec(ctx, "INSERT INTO nope VALUES (1)", nil); err == nil {
			t.Error("Exec on a missing table: no error")
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := e.Begin(cancelled, sql.TxOptions{}); err == nil {
			t.Error("Begin on a canceled context: no error")
		}
	}
}
