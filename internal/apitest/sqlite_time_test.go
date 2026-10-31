package apitest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"testing"
	"time"

	"github.com/syssam/velox2"
	_ "modernc.org/sqlite"
)

type valuerTime struct{ t time.Time }

func (v valuerTime) Value() (driver.Value, error) { return v.t, nil }

// TestSQLiteComparesTimesByTheInstant: SQLite compares text, so a time written in
// a zone ahead of UTC must not sort after a later instant given in UTC.
func TestSQLiteComparesTimesByTheInstant(t *testing.T) {
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec("CREATE TABLE s (id integer PRIMARY KEY, at timestamp NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, raw)
	ctx := context.Background()
	now := time.Now()
	expired := now.Add(-time.Minute).In(time.FixedZone("HK", 8*3600))

	count := func(run func(context.Context, *velox.DB) (int64, error)) int64 {
		t.Helper()
		n, err := run(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	insert := func(ctx context.Context, db *velox.DB, id int, at any) (int64, error) {
		return db.Exec(ctx, "test", "INSERT INTO s (id, at) VALUES (?, ?)", id, at)
	}
	count(func(ctx context.Context, db *velox.DB) (int64, error) { return insert(ctx, db, 1, expired) })
	count(func(ctx context.Context, db *velox.DB) (int64, error) { return insert(ctx, db, 2, &expired) })
	count(func(ctx context.Context, db *velox.DB) (int64, error) { return insert(ctx, db, 3, valuerTime{expired}) })
	if err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := insert(ctx, tx, 4, expired)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var n int
	// A raw UPDATE counts what the comparison matches, each way round.
	matched := func(q string, arg any) int64 {
		t.Helper()
		return count(func(ctx context.Context, db *velox.DB) (int64, error) { return db.Exec(ctx, "test", q, arg) })
	}
	if got := matched("UPDATE s SET id = id WHERE at > ?", now.UTC()); got != 0 {
		t.Errorf("%d rows that expired a minute ago are after now", got)
	}
	if got := matched("UPDATE s SET id = id WHERE at < ?", now.UTC()); got != 4 {
		t.Errorf("%d rows are before now, want 4", got)
	}
	if err := raw.QueryRow("SELECT count(*) FROM s WHERE id = 1").Scan(&n); err != nil || n != 1 {
		t.Fatalf("count %d, %v", n, err)
	}
	var back time.Time
	if err := raw.QueryRow("SELECT at FROM s WHERE id = 1").Scan(&back); err != nil {
		t.Fatal(err)
	}
	if !back.Equal(expired) {
		t.Errorf("read back %v, wrote %v", back, expired)
	}
}
