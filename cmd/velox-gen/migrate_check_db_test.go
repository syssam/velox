package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox"
)

// TestMigrateDiffCheckScratchDB: diff -check -scratch-db applies a PostgreSQL
// or MySQL directory's files to the empty database it names and compares the
// result with schema.json; a last file gone, the sum rewritten, fails it, and
// no message shows the DSN.
func TestMigrateDiffCheckScratchDB(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			first := testDSN(t, d)
			dir := tempModule(t, map[string]string{"veloxschema/user.go": userSchema(`field.String("name")`)})
			setSchema := func(fields ...string) {
				writeFile(t, filepath.Join(dir, "veloxschema", "user.go"), userSchema(fields...))
			}
			if code, _, errs := runCLI(t, dir, "migrate", "diff", "-dialect", d.String()); code != 0 {
				t.Fatalf("diff: %s", errs)
			}
			setSchema(`field.String("name")`, `field.String("email").Nillable()`)
			if code, _, errs := runCLI(t, dir, "migrate", "diff"); code != 0 {
				t.Fatalf("diff: %s", errs)
			}
			code, out, errs := runCLI(t, dir, "migrate", "diff", "-check", "-scratch-db", first)
			want(t, "check", code, out, errs, 0, "", "")

			mig := filepath.Join(dir, "migrations")
			matches, err := filepath.Glob(filepath.Join(mig, "0002_*.sql"))
			if err != nil || len(matches) != 1 {
				t.Fatalf("0002: %v %v", matches, err)
			}
			if err := os.Remove(matches[0]); err != nil {
				t.Fatal(err)
			}
			if code, _, errs := runCLI(t, dir, "migrate", "hash"); code != 0 {
				t.Fatalf("hash: %s", errs)
			}
			second := testDSN(t, d)
			code, out, errs = runCLI(t, dir, "migrate", "diff", "-check", "-scratch-db", second)
			if code != 1 || out != "" || !strings.Contains(errs, "the files do not make migrations/schema.json: applied to -scratch-db") ||
				!strings.Contains(errs, "add-column users.email") || strings.Contains(errs, second) {
				t.Errorf("check without the last file: %d %q %q", code, out, errs)
			}
			// The scratch database must be empty: first holds the files.
			code, _, errs = runCLI(t, dir, "migrate", "diff", "-check", "-scratch-db", first)
			if code != 1 || !strings.Contains(errs, "empty database") || strings.Contains(errs, first) {
				t.Errorf("a used scratch database: %d %q", code, errs)
			}
		})
	}
}

// TestMigrateDriftPendingScratchDB: drift on a PostgreSQL or MySQL database
// with a file pending needs -scratch-db, and then compares with what the
// files up to the last applied version make there.
func TestMigrateDriftPendingScratchDB(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			dsn := testDSN(t, d)
			dir := t.TempDir()
			handDir(t, dir, d, map[string]string{
				"0001_a.sql": "CREATE TABLE a (id integer NOT NULL PRIMARY KEY);\n",
			})
			o := &opsDB{t: t, dir: dir, dsn: dsn}
			o.ok("apply")
			db, err := openDB(d, dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			o.db = db
			o.files(nil, map[string]string{"0002_b.sql": "CREATE TABLE b (id integer NOT NULL PRIMARY KEY);\n"})
			code, out, errs := o.run("drift")
			if code != 1 || out != "" || !strings.Contains(errs, "0002_b.sql is pending; to compare it with what the files up to 0001_a.sql make, give an empty "+d.String()+" database with -scratch-db") {
				t.Errorf("no scratch: %d %q %q", code, out, errs)
			}
			o.exec("ALTER TABLE a ADD COLUMN x integer")
			scratch := testDSN(t, d)
			code, out, errs = o.run("drift", "-scratch-db", scratch)
			if code != 3 || out != "change: drop-column a.x\n" || !strings.Contains(errs, "compared with what the files up to 0001_a.sql make") || strings.Contains(errs, scratch) {
				t.Errorf("drift: %d %q %q", code, out, errs)
			}
		})
	}
}
