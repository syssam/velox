package softdelete_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/schema/mixin/softdelete"
)

// noCol lists the mixin's hooks but has no deleted_at column.
type noCol struct {
	ID   int64
	Name string
}

// textCol lists the mixin's hooks, and its deleted_at is text, not a time.
type textCol struct {
	ID        int64
	DeletedAt string
}

var (
	noColMeta   velox.MetaOnce[noCol]
	textColMeta velox.MetaOnce[textCol]
)

func (*noCol) VeloxMeta() *velox.Meta[noCol] {
	return noColMeta.Load(func() velox.Descriptor[noCol] {
		return velox.Descriptor[noCol]{
			Table:      "plain",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *noCol, i int) any { return [...]any{&x.ID, &x.Name}[i] },
			Schema:     fixture.Fns{Hooks: []func() []velox.Hook{softdelete.Mixin{}.Hooks}},
		}
	})
}

func (*textCol) VeloxMeta() *velox.Meta[textCol] {
	return textColMeta.Load(func() velox.Descriptor[textCol] {
		return velox.Descriptor[textCol]{
			Table:      "plain",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "DeletedAt", Name: "deleted_at"}},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *textCol, i int) any { return [...]any{&x.ID, &x.DeletedAt}[i] },
			Schema:     fixture.Fns{Hooks: []func() []velox.Hook{softdelete.Mixin{}.Hooks}},
		}
	})
}

// TestHookNeedsATimeColumn: the mixin's hook, bound to an entity without a
// deleted_at column or with one that holds no time, fails the delete and
// changes nothing.
func TestHookNeedsATimeColumn(t *testing.T) {
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "misuse.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(`CREATE TABLE "plain" ("id" INTEGER PRIMARY KEY, "name" TEXT NOT NULL DEFAULT '', "deleted_at" TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO "plain" ("id") VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, raw)
	ctx := context.Background()

	if _, err := db.Delete[noCol]().All().Exec(ctx); err == nil || !strings.Contains(err.Error(), "has no column deleted_at") {
		t.Errorf("delete without deleted_at: %v; want an error naming the column", err)
	}
	if _, err := db.Delete[textCol]().All().Exec(ctx); err == nil {
		t.Error("delete with a text deleted_at succeeded")
	}
	var n int
	if err := raw.QueryRow(`SELECT count(*) FROM "plain" WHERE "deleted_at" = ''`).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d live rows (%v), want 1", n, err)
	}
}
