package gen_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"

	"example.com/tmp/gen"
)

// Post declares Policy and User declares Hooks, but the generated metadata was made without binding
// it: velox refuses the entity at its first use instead of ignoring the method.
func TestUnboundMethodsAreRefused(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	db := velox.Open(velox.SQLite, raw)
	_, err = db.Query[gen.Post]().Limit(1).All(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not bind it; run go tool velox-gen") {
		t.Fatalf("got %v", err)
	}
	if err := velox.CheckEntity[gen.Post](); err == nil {
		t.Error("CheckEntity accepted an entity with an unbound Policy")
	}
	// User declares Hooks, which is unbound as well.
	_, err = db.Query[gen.User]().Limit(1).All(context.Background())
	if err == nil || !strings.Contains(err.Error(), "has a Hooks method") || !strings.Contains(err.Error(), "does not bind it; run go tool velox-gen") {
		t.Fatalf("got %v", err)
	}
}
