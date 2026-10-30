package apitest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/fakeconn"
)

// panicText runs f and returns what it panicked with, as text; "" when it
// did not panic.
func panicText(f func()) (msg string) {
	defer func() {
		if p := recover(); p != nil {
			msg = fmt.Sprint(p)
		}
	}()
	f()
	return ""
}

// TestOpenRefusesAnUnknownDialect: Open and OpenShards panic on a Dialect
// that is not one of velox's, before opening anything.
func TestOpenRefusesAnUnknownDialect(t *testing.T) {
	const want = "velox: Open: unknown Dialect(0); use velox.Postgres, velox.MySQL or velox.SQLite"
	if got := panicText(func() { velox.Open(0, new(sql.DB)) }); got != want {
		t.Errorf("Open(0) panicked with %q, want %q", got, want)
	}
	if got := panicText(func() { velox.Open(4, new(sql.DB)) }); !strings.Contains(got, "Dialect(4)") {
		t.Errorf("Open(4) panicked with %q, want it to name Dialect(4)", got)
	}
	shards := map[string]velox.Shard{"a": {Primary: fakeConn(&fakeconn.Exec{})}}
	shardFor := velox.WithShardFor(func(context.Context, any) (string, error) { return "a", nil })
	const wantShards = "velox: OpenShards: unknown Dialect(0); use velox.Postgres, velox.MySQL or velox.SQLite"
	if got := panicText(func() { velox.OpenShards(0, shards, shardFor) }); got != wantShards {
		t.Errorf("OpenShards(0) panicked with %q, want %q", got, wantShards)
	}
}
