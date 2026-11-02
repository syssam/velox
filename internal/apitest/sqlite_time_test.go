package apitest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"testing"
	"time"

	"github.com/syssam/velox"
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
	defer func() { _ = raw.Close() }()
	if _, err := raw.Exec("CREATE TABLE s (id integer PRIMARY KEY, at timestamp NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	expired := now.Add(-time.Minute).In(time.FixedZone("HK", 8*3600))
	ctx := context.Background()
	const insert = "INSERT INTO s (id, at) VALUES (?, ?)"

	// A raw UPDATE counts what a comparison matches. The sharded DB's primary is
	// made apart from Open's, so it writes a row too.
	db := velox.Open(velox.SQLite, raw)
	shards := velox.OpenShards(velox.SQLite, map[string]velox.Shard{"a": {Primary: velox.SQLConn(raw)}},
		velox.WithShardFor(func(context.Context) (string, error) { return "a", nil }))
	for i, write := range []func() error{
		func() error { _, err := db.Exec(ctx, "test", insert, 1, expired); return err },
		func() error { _, err := db.Exec(ctx, "test", insert, 2, &expired); return err },
		func() error { _, err := db.Exec(ctx, "test", insert, 3, valuerTime{expired}); return err },
		func() error {
			return db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Exec(ctx, "test", insert, 4, expired)
				return err
			})
		},
		func() error {
			_, err := shards.Exec(velox.WithShard(ctx, "a"), "test", insert, 5, expired)
			return err
		},
	} {
		if err := write(); err != nil {
			t.Fatalf("write %d: %v", i+1, err)
		}
	}
	matched := func(q string) int64 {
		t.Helper()
		n, err := db.Exec(ctx, "test", q, now.UTC())
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := matched("UPDATE s SET id = id WHERE at > ?"); got != 0 {
		t.Errorf("%d rows that expired a minute ago are after now", got)
	}
	if got := matched("UPDATE s SET id = id WHERE at < ?"); got != 5 {
		t.Errorf("%d rows are before now, want 5", got)
	}
	var back time.Time
	if err := raw.QueryRow("SELECT at FROM s WHERE id = 1").Scan(&back); err != nil {
		t.Fatal(err)
	}
	if !back.Equal(expired) {
		t.Errorf("read back %v, wrote %v", back, expired)
	}
}
