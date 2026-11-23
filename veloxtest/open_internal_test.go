package veloxtest

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/benchschema"
	"github.com/syssam/velox/migrate"
)

// TestInMemory: the DSNs of a database in memory, and the parameters a
// copy's file keeps of them.
func TestInMemory(t *testing.T) {
	for _, c := range []struct {
		dsn    string
		memory bool
		params string
	}{
		{":memory:", true, ""},
		{"file::memory:", true, ""},
		{"file::memory:?cache=shared", true, ""},
		{"file:ent?mode=memory&cache=shared&_fk=1", true, "?_fk=1"},
		{"file:x?MODE=memory&_pragma=foreign_keys(1)&_txlock=immediate", true, "?_pragma=foreign_keys(1)&_txlock=immediate"},
		{"/x?vfs=memdb", true, ""},
		{"file:ent?cache=shared", false, ""},
		{"/tmp/a.db", false, ""},
		{"memory.db?mode=rwc", false, "?mode=rwc"},
	} {
		if got := inMemory(c.dsn); got != c.memory {
			t.Errorf("inMemory(%q) = %t", c.dsn, got)
		}
		if c.memory {
			if got := fileParams(c.dsn); got != c.params {
				t.Errorf("fileParams(%q) = %q; want %q", c.dsn, got, c.params)
			}
		}
	}
}

// TestOpenCopyFails: a schema Create refuses fails every Open of it, each
// with Create's error.
func TestOpenCopyFails(t *testing.T) {
	bad := benchschema.Tables(2, false)
	bad.Tables[1].Columns[1].Type = "no such(type"
	for range 2 {
		r := &fatal{TB: t}
		func() {
			defer func() { _ = recover() }()
			openCopy(r, "sqlite", "", "bad", bad, nil)
		}()
		if r.msg == "" {
			t.Fatal("Open of a bad schema did not fail")
		}
	}
}

// fatal records a Fatalf, and stops the goroutine as testing does.
type fatal struct {
	testing.TB
	msg string
}

func (f *fatal) Fatalf(format string, args ...any) {
	f.msg = fmt.Sprintf(format, args...)
	panic(f.msg)
}

// BenchmarkOpenSQLite is Open of bench/scale's 500 wide tables
// (benchschema.Tables) on SQLite: an empty dsn, enttest's dsn in memory, and
// a file named, which Open makes the tables in with migrate.Create. The
// first two copy one image; the first iteration makes it. Run it with
// -benchtime=20x.
func BenchmarkOpenSQLite(b *testing.B) {
	s := benchschema.Tables(500, true)
	for _, c := range []struct{ name, dsn string }{
		{"empty", ""},
		{"enttest", "file:ent?mode=memory&cache=shared&_fk=1"},
		{"file", "file"},
	} {
		b.Run(c.name, func(b *testing.B) {
			tables := func(velox.Dialect) (*migrate.Schema, string, error) { return s, "bench", nil }
			for b.Loop() {
				dsn := c.dsn
				if dsn == "file" {
					dsn = filepath.Join(b.TempDir(), "velox.db")
				}
				db := open(b, "sqlite", dsn, tables, nil)
				var n int
				if err := velox.Pool(db).QueryRowContext(context.Background(), `SELECT count(*) FROM e499s`).Scan(&n); err != nil {
					b.Fatal(err)
				}
				_ = db.Close()
			}
		})
	}
}
