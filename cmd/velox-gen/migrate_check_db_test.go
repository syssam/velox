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
