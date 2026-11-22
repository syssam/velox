package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/genrun"
	"github.com/syssam/velox/migrate"
)

// userSchema is a schema package of User with the fields given, as Go
// expressions.
func userSchema(fields ...string) string {
	return `package veloxschema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field { return []schema.Field{` + strings.Join(fields, ", ") + `} }
`
}

// want checks a run's exit code, standard output and standard error.
func want(t *testing.T, name string, code int, out, errs string, wantCode int, wantOut, wantErrs string) {
	t.Helper()
	if code != wantCode || out != wantOut || errs != wantErrs {
		t.Errorf("%s: exit %d, want %d\nstdout:\n%s\nwant:\n%s\nstderr:\n%s\nwant:\n%s", name, code, wantCode, out, wantOut, errs, wantErrs)
	}
}

// dirFiles is the files of dir by name, with their content.
func dirFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range ents {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(data)
	}
	return out
}

// exists reports whether the file at path is there.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestMigrateDiff(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	// Serial: "all or nothing" replaces genrun.WriteTemp and
	// genrun.RenameFile, which the parallel tests' runs use too.
	dir := tempModuleSerial(t, map[string]string{"veloxschema/user.go": userSchema(`field.String("name")`)})
	mig := filepath.Join(dir, "migrations")
	setSchema := func(fields ...string) {
		writeFile(t, filepath.Join(dir, "veloxschema", "user.go"), userSchema(fields...))
	}

	t.Run("first", func(t *testing.T) {
		code, out, errs := runCLI(t, dir, "migrate", "diff", "-dialect", "sqlite")
		want(t, "diff", code, out, errs, 0, "migrations/0001_create_users.sql\nmigrations/schema.json\nmigrations/velox.json\nmigrations/velox.sum\n", "")
		for _, f := range []string{"0001_create_users.sql", "schema.json", "velox.json", "velox.sum"} {
			if !exists(filepath.Join(mig, f)) {
				t.Errorf("%s not written", f)
			}
		}
		code, out, errs = runCLI(t, dir, "migrate", "diff")
		want(t, "again", code, out, errs, 0, "no changes\n", "")
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-check")
		want(t, "check", code, out, errs, 0, "", "")

		// -check applies the files to a temporary SQLite file: a hand edit,
		// hashed, that schema.json does not know fails it.
		file := filepath.Join(mig, "0001_create_users.sql")
		orig, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, file, "CREATE TABLE `users` (`id` integer NOT NULL PRIMARY KEY AUTOINCREMENT);\n")
		if code, _, errs := runCLI(t, dir, "migrate", "hash"); code != 0 {
			t.Fatalf("hash: %s", errs)
		}
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-check")
		if code != 1 || out != "" || !strings.HasPrefix(errs, "velox-gen: migrate diff: the files do not make migrations/schema.json: applied to a temporary SQLite file, the database differs from migrations/schema.json after applying the directory:\n") ||
			!strings.Contains(errs, "add-column users.name") {
			t.Errorf("check of an edited file: %d %q %q", code, out, errs)
		}
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-check", "-replay=false")
		want(t, "check -replay=false", code, out, errs, 0, "", "")
		writeFile(t, file, string(orig))
		if code, _, errs := runCLI(t, dir, "migrate", "hash"); code != 0 {
			t.Fatalf("hash: %s", errs)
		}
	})

	t.Run("check replays a postgres directory only on -scratch-db", func(t *testing.T) {
		code, _, errs := runCLI(t, dir, "migrate", "diff", "-dir", "pg", "-dialect", "postgres")
		if code != 0 {
			t.Fatalf("diff: %s", errs)
		}
		code, out, errs := runCLI(t, dir, "migrate", "diff", "-check", "-dir", "pg")
		want(t, "no scratch", code, out, errs, 2, "", "velox-gen: migrate diff: -check applies the files to an empty database and compares it with schema.json, and the files of this postgres directory need a postgres one: pass -scratch-db <dsn>, or -replay=false to compare the schema package with schema.json alone\n")
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-check", "-dir", "pg", "-replay=false")
		want(t, "-replay=false", code, out, errs, 0, "", "")
		for _, args := range [][]string{{"-scratch-db", "x"}, {"-replay=false"}, {"-check", "-replay=false", "-scratch-db", "x"}} {
			code, out, errs := runCLI(t, dir, append([]string{"migrate", "diff", "-dir", "pg"}, args...)...)
			if code != 2 || out != "" || !strings.Contains(errs, "Usage:") {
				t.Errorf("%v: %d %q %q", args, code, out, errs)
			}
		}
		if err := os.RemoveAll(filepath.Join(dir, "pg")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("existing empty directory", func(t *testing.T) {
		writeFile(t, filepath.Join(dir, "empty", ".gitkeep"), "")
		writeFile(t, filepath.Join(dir, "empty", "embed.go"), "package empty\n")
		code, out, errs := runCLI(t, dir, "migrate", "hash", "-dir", "empty")
		want(t, "hash", code, out, errs, 1, "", "velox-gen: migrate hash: empty: not a migration directory (no velox.json); run velox-gen migrate diff first\n")
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-dir", "empty")
		want(t, "no dialect", code, out, errs, 2, "", "velox-gen: migrate diff: empty is not a migration directory yet; name its dialect with -dialect to start it\n")
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-dir", "empty", "-dialect", "sqlite")
		want(t, "diff", code, out, errs, 0, "empty/0001_create_users.sql\nempty/schema.json\nempty/velox.json\nempty/velox.sum\n", "")
		code, out, errs = runCLI(t, dir, "migrate", "hash", "-dir", "empty")
		want(t, "hash after", code, out, errs, 0, "", "")
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-dir", "empty")
		want(t, "diff after", code, out, errs, 0, "no changes\n", "")
		for _, f := range []string{".gitkeep", "embed.go"} {
			if !exists(filepath.Join(dir, "empty", f)) {
				t.Errorf("%s is gone", f)
			}
		}
	})

	t.Run("add", func(t *testing.T) {
		setSchema(`field.String("name")`, `field.String("email").Nillable()`)
		code, out, errs := runCLI(t, dir, "migrate", "diff", "-check", "./veloxschema")
		want(t, "check", code, out, errs, 3, "", "velox-gen: migrate diff: migrations/schema.json is out of date:\n  add-column users.email\nvelox-gen: run velox-gen migrate diff\n")
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-dry-run")
		if code != 0 || errs != "" || !strings.HasPrefix(out, "== migrations/0002_add_users_email.sql\n-- Generated by ") ||
			!strings.Contains(out, "\n== migrations/schema.json\n{") || !strings.Contains(out, "\n== migrations/velox.sum\nh1:") {
			t.Errorf("dry-run: %d %q\n%s", code, errs, out)
		}
		if exists(filepath.Join(mig, "0002_add_users_email.sql")) {
			t.Error("dry-run wrote the file")
		}
		_, again, _ := runCLI(t, dir, "migrate", "diff", "-dry-run")
		if again != out {
			t.Errorf("dry-run twice:\n%s\n%s", out, again)
		}
		code, out, errs = runCLI(t, dir, "migrate", "diff")
		want(t, "diff", code, out, errs, 0, "migrations/0002_add_users_email.sql\nmigrations/schema.json\nmigrations/velox.sum\n", "")

		// The last file deleted and the sum rewritten, schema.json still says
		// what it made: -check fails.
		last := filepath.Join(mig, "0002_add_users_email.sql")
		data, err := os.ReadFile(last)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(last); err != nil {
			t.Fatal(err)
		}
		if code, _, errs := runCLI(t, dir, "migrate", "hash"); code != 0 {
			t.Fatalf("hash: %s", errs)
		}
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-check")
		if code != 1 || out != "" || !strings.Contains(errs, "the files do not make migrations/schema.json") || !strings.Contains(errs, "add-column users.email") {
			t.Errorf("check without the last file: %d %q %q", code, out, errs)
		}
		writeFile(t, last, string(data))
		if code, _, errs := runCLI(t, dir, "migrate", "hash"); code != 0 {
			t.Fatalf("hash: %s", errs)
		}
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-check")
		want(t, "check restored", code, out, errs, 0, "", "")
	})

	t.Run("drop", func(t *testing.T) {
		setSchema(`field.String("full_name")`, `field.String("email").Nillable()`)
		code, out, errs := runCLI(t, dir, "migrate", "diff")
		if code != 1 || out != "" || !strings.HasSuffix(errs, "may be a rename: if so, pass -rename-column users.name=full_name\n") {
			t.Errorf("rename hint: %d %q %q", code, out, errs)
		}
		setSchema(`field.String("email").Nillable()`)
		code, out, errs = runCLI(t, dir, "migrate", "diff")
		want(t, "blocked", code, out, errs, 1, "", "velox-gen: migrate diff: 1 destructive change refused:\n"+
			"  users.name: drop-column: drops column users.name and its data; allow with -allow drop-column=users.name\n")
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-allow", "drop-column=users.name")
		want(t, "allowed", code, out, errs, 0, "migrations/0003_drop_users_name.sql\nmigrations/schema.json\nmigrations/velox.sum\n", "")
		data, err := os.ReadFile(filepath.Join(mig, "0003_drop_users_name.sql"))
		if err != nil || !strings.Contains(string(data), "\n-- velox:allow drop-column users.name\n") {
			t.Errorf("%v\n%s", err, data)
		}
	})

	t.Run("all or nothing", func(t *testing.T) {
		setSchema(`field.String("email").Nillable()`, `field.String("bio").Nillable()`)
		before := dirFiles(t, mig)
		injected := errors.New("injected failure")
		for _, c := range []struct {
			name string
			fail func()
		}{
			{"write", func() {
				n := 0
				genrun.WriteTemp = func(dir, pattern string, data []byte) (string, error) {
					if n++; n == 2 {
						return "", injected
					}
					return genrun.WriteTempFile(dir, pattern, data)
				}
			}},
			{"rename", func() {
				genrun.RenameFile = func(from, to string) error {
					if filepath.Base(to) == "velox.sum" && strings.Contains(from, ".tmp-") { // the swap, not the undo
						return injected
					}
					return os.Rename(from, to)
				}
			}},
		} {
			c.fail()
			code, out, errs := runCLI(t, dir, "migrate", "diff")
			genrun.WriteTemp, genrun.RenameFile = genrun.WriteTempFile, os.Rename
			want(t, c.name, code, out, errs, 1, "", "velox-gen: migrate diff: injected failure\n")
			if after := dirFiles(t, mig); !reflect.DeepEqual(after, before) {
				t.Errorf("%s: the directory changed:\n%v\nwas\n%v", c.name, slices.Sorted(maps.Keys(after)), slices.Sorted(maps.Keys(before)))
			}
		}
		genrun.RenameFile = func(string, string) error { return injected }
		code, out, errs := runCLI(t, dir, "migrate", "diff", "-dir", "new/a/b", "-dialect", "sqlite")
		genrun.RenameFile = os.Rename
		want(t, "new directory", code, out, errs, 1, "", "velox-gen: migrate diff: injected failure\n")
		if exists(filepath.Join(dir, "new")) {
			t.Error("a failed diff left the directories it made")
		}
		code, out, errs = runCLI(t, dir, "migrate", "diff")
		want(t, "diff", code, out, errs, 0, "migrations/0004_add_users_bio.sql\nmigrations/schema.json\nmigrations/velox.sum\n", "")
	})

	t.Run("misuse", func(t *testing.T) {
		code, out, errs := runCLI(t, dir, "migrate", "diff", "-dialect", "postgres")
		want(t, "dialect", code, out, errs, 2, "", "velox-gen: migrate diff: -dialect postgres disagrees with migrations/velox.json, which says sqlite\n")
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-allow", "nope")
		if code != 2 || out != "" || !strings.HasPrefix(errs, `invalid value "nope" for flag -allow: nope: "nope" is not a destructive code;`) || !strings.Contains(errs, "Flags of migrate diff:") {
			t.Errorf("-allow: %d %q %q", code, out, errs)
		}
		for _, args := range [][]string{
			{"-rename-table", "x"}, {"-rename-table", "a=b=c"}, {"-rename-column", "users=x"}, {"-rename-column", "users.=x"},
			{"-dialect", "oracle"}, {"./a", "./b"}, {"-lock-timeout", "-5s"},
		} {
			code, out, errs := runCLI(t, dir, append([]string{"migrate", "diff"}, args...)...)
			if code != 2 || out != "" || !strings.Contains(errs, "Usage:") {
				t.Errorf("%v: %d %q %q", args, code, out, errs)
			}
		}
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-rename-column", "users.nme=x")
		want(t, "rename column", code, out, errs, 1, "", "velox-gen: migrate diff: -rename-column users.nme=x: table users in schema.json has no column nme\n")
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-rename-column", "people.nme=x")
		want(t, "rename column table", code, out, errs, 1, "", "velox-gen: migrate diff: -rename-column people.nme=x: schema.json has no table people\n")
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-rename-table", "x=y")
		want(t, "rename table", code, out, errs, 1, "", "velox-gen: migrate diff: -rename-table x=y: schema.json has no table x\n")
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-dir", "other")
		want(t, "missing", code, out, errs, 2, "", "velox-gen: migrate diff: other does not exist; name its dialect with -dialect to start it\n")
	})

	t.Run("stale", func(t *testing.T) {
		writeFile(t, filepath.Join(mig, "0001_create_users.sql"), "-- edited\n")
		code, out, errs := runCLI(t, dir, "migrate", "diff")
		want(t, "diff", code, out, errs, 1, "", "velox-gen: migrate diff: migrations/velox.sum is out of date; run velox-gen migrate hash if the edits are intended\n")
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-check")
		want(t, "check", code, out, errs, 3, "", "velox-gen: migrate diff: migrations/velox.sum is out of date; run velox-gen migrate hash if the edits are intended\n")
	})

	t.Run("schema errors", func(t *testing.T) {
		setSchema(`field.String("name")`, `field.String("name")`)
		code, out, errs := runCLI(t, dir, "migrate", "diff", "-dir", "fresh", "-dialect", "postgres")
		if code != 1 || out != "" || !strings.HasPrefix(errs, "velox-gen: veloxschema/user.go:") || exists(filepath.Join(dir, "fresh")) {
			t.Errorf("%d %q %q", code, out, errs)
		}
	})
}

func TestMigrateHashAndLint(t *testing.T) {
	dir := t.TempDir()
	put := func(name, src string) { writeFile(t, filepath.Join(dir, name), src) }
	hash := func() {
		sum, err := migrate.Sum(os.DirFS(dir))
		if err != nil {
			t.Fatal(err)
		}
		put("velox.sum", string(sum))
	}
	put("velox.json", `{"version": 1, "dialect": "sqlite"}`+"\n")
	snap, err := json.Marshal(&migrate.Schema{Dialect: velox.SQLite})
	if err != nil {
		t.Fatal(err)
	}
	put("schema.json", string(snap)+"\n")
	put("0001_create_users.sql", "CREATE TABLE users (id integer);\n")
	hash()
	run := func(args ...string) (int, string, string) {
		return runCLI(t, dir, append([]string{"migrate"}, append(args, "-dir", ".")...)...)
	}
	stale := "velox.sum is out of date; run velox-gen migrate hash if the edits are intended\n"

	code, out, errs := run("lint")
	want(t, "lint", code, out, errs, 0, "", "")
	code, out, errs = run("hash", "-check")
	want(t, "hash -check", code, out, errs, 0, "", "")

	put("0001_create_users.sql", "CREATE TABLE users (id integer PRIMARY KEY);\n")
	code, out, errs = run("hash", "-check")
	want(t, "edited hash -check", code, out, errs, 3, "", "velox-gen: migrate hash: "+stale)
	code, out, errs = run("lint")
	want(t, "edited lint", code, out, errs, 1, "", "velox-gen: migrate lint: "+stale)
	before := dirFiles(t, dir)
	genrun.RenameFile = func(string, string) error { return errors.New("injected failure") }
	code, out, errs = run("hash")
	genrun.RenameFile = os.Rename
	want(t, "hash failing", code, out, errs, 1, "", "velox-gen: migrate hash: injected failure\n")
	if after := dirFiles(t, dir); !reflect.DeepEqual(after, before) {
		t.Errorf("a failed hash changed the directory:\n%v", slices.Sorted(maps.Keys(after)))
	}
	code, out, errs = run("hash")
	want(t, "hash", code, out, errs, 0, "velox.sum\n", "")
	code, out, errs = run("hash")
	want(t, "hash again", code, out, errs, 0, "", "")
	// Root reads a file whatever its mode, so there the file is not unreadable.
	if os.Geteuid() != 0 {
		if err := os.Chmod(filepath.Join(dir, "velox.sum"), 0); err != nil {
			t.Fatal(err)
		}
		code, out, errs = run("hash")
		if err := os.Chmod(filepath.Join(dir, "velox.sum"), 0o644); err != nil {
			t.Fatal(err)
		}
		if code != 1 || out != "" || !strings.Contains(errs, "permission denied") {
			t.Errorf("unreadable velox.sum: %d %q %q; want exit 1", code, out, errs)
		}
	}
	sum, err := os.ReadFile(filepath.Join(dir, "velox.sum"))
	if err != nil {
		t.Fatal(err)
	}
	crlf := "\xEF\xBB\xBF" + strings.ReplaceAll(string(sum), "\n", "\r\n")
	put("velox.sum", crlf)
	code, out, errs = run("hash")
	want(t, "hash BOM and CRLF", code, out, errs, 0, "", "")
	if got, _ := os.ReadFile(filepath.Join(dir, "velox.sum")); string(got) != crlf {
		t.Errorf("hash rewrote a velox.sum that matches but for its BOM and line endings")
	}
	code, out, errs = run("hash", "-check")
	want(t, "hashed hash -check", code, out, errs, 0, "", "")
	code, out, errs = run("lint")
	want(t, "hashed lint", code, out, errs, 0, "", "")

	put("0002_x.sql", "DROP TABLE users;\n")
	put("0003_y.sql", "BEGIN;\nDELETE FROM users;\n")
	hash()
	code, out, errs = run("lint")
	want(t, "blocked", code, out, errs, 1, "", "0002_x.sql:1: drop-table: drops table users and its rows\n"+
		"0003_y.sql:1: file: BEGIN in a file run in a transaction; the runner begins and commits the file's transaction itself\n"+
		"0003_y.sql:2: delete-rows: deletes every row of users\n")
	_, again, errsAgain := run("lint")
	if again != out || errsAgain != errs {
		t.Errorf("lint twice: %q %q", again, errsAgain)
	}
	code, out, errs = run("lint", "-since", "3")
	want(t, "since", code, out, errs, 1, "", "0003_y.sql:1: file: BEGIN in a file run in a transaction; the runner begins and commits the file's transaction itself\n"+
		"0003_y.sql:2: delete-rows: deletes every row of users\n")
	put("0003_y.sql", "-- velox:allow delete-rows\nDELETE FROM users;\n")
	hash()
	code, out, errs = run("lint", "-since", "3")
	want(t, "since allowed", code, out, errs, 0, "", "")
	put("0002_x.sql", "-- velox:allow drop-table users\nDROP TABLE users;\n")
	hash()
	code, out, errs = run("lint")
	want(t, "allowed", code, out, errs, 0, "", "")

	// The paths are the directory's, as -dir names it.
	code, out, errs = runCLI(t, filepath.Dir(dir), "migrate", "hash", "-check", "-dir", filepath.Base(dir))
	want(t, "-dir", code, out, errs, 0, "", "")
	put("0004_z.sql", "TRUNCATE users;\n")
	hash()
	code, out, errs = runCLI(t, filepath.Dir(dir), "migrate", "lint", "-dir", filepath.Base(dir))
	want(t, "-dir lint", code, out, errs, 1, "", filepath.Join(filepath.Base(dir), "0004_z.sql")+":1: delete-rows: deletes every row of users\n")
	put("0005_a.sql", "SELECT 1;\n")
	put("0005_b.sql", "SELECT 2;\n")
	hash()
	code, out, errs = runCLI(t, filepath.Dir(dir), "migrate", "lint", "-dir", filepath.Base(dir))
	base := filepath.Base(dir)
	want(t, "-dir names", code, out, errs, 1, "", "velox-gen: migrate lint: "+filepath.Join(base, "0005_a.sql")+" and "+filepath.Join(base, "0005_b.sql")+" have one version\n")
	for _, sub := range []string{"lint", "hash"} {
		code, out, errs = runCLI(t, dir, "migrate", sub, "-dir", "nowhere")
		want(t, sub+" missing", code, out, errs, 1, "", "velox-gen: migrate "+sub+": nowhere does not exist\n")
	}
	code, out, errs = run("lint", "extra")
	if code != 2 || !strings.Contains(errs, "Usage:") {
		t.Errorf("extra: %d %q %q", code, out, errs)
	}
}

func TestMigrateUsage(t *testing.T) {
	dir := t.TempDir()
	usage, err := os.ReadFile(filepath.Join("testdata", "usage.golden"))
	if err != nil {
		t.Fatal(err)
	}
	code, out, errs := runCLI(t, dir, "migrate")
	want(t, "migrate", code, out, errs, 2, "", "velox-gen: migrate needs a subcommand: diff, apply, status, drift, baseline, lint, hash, resolve, rebase or sql\n"+string(usage))
	code, out, errs = runCLI(t, dir, "migrate", "nope")
	want(t, "nope", code, out, errs, 2, "", "velox-gen: migrate: unknown subcommand \"nope\"\n"+string(usage))
	code, out, errs = runCLI(t, dir, "migrate", "-h")
	want(t, "-h", code, out, errs, 0, "", string(usage))
	for _, sub := range []string{"diff", "apply", "status", "drift", "baseline", "lint", "hash", "resolve", "rebase"} {
		code, _, errs := runCLI(t, dir, "migrate", sub, "-h")
		if code != 0 || !strings.HasPrefix(errs, string(usage)) || !strings.Contains(errs, "Flags of migrate "+sub+":\n") || !strings.Contains(errs, "  -dir string\n") {
			t.Errorf("%s -h: %d\n%s", sub, code, errs)
		}
	}
	code, _, errs = runCLI(t, dir, "migrate", "sql", "-h")
	_, flags, _ := strings.Cut(errs, "Flags of migrate sql:\n")
	if code != 0 || !strings.HasPrefix(errs, string(usage)) || !strings.Contains(flags, "  -dialect ") || strings.Contains(flags, "-dir") {
		t.Errorf("sql -h: %d\n%s", code, errs)
	}
}

// noVeloxDB is the process's environment without VELOX_DB.
func noVeloxDB() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "VELOX_DB=") })
}

// writeMigrations adds a file to the migration directory dir for each of
// steps, each planned from the one before, as migrate diff writes them; a
// new directory is of dialect d.
func writeMigrations(t *testing.T, dir string, d velox.Dialect, steps ...*migrate.Schema) {
	t.Helper()
	for _, to := range steps {
		md, err := migrate.ReadDir(os.DirFS(dir))
		if !exists(filepath.Join(dir, "velox.json")) {
			md, err = migrate.NewDir(migrate.Config{Dialect: d})
		}
		if err != nil {
			t.Fatal(err)
		}
		plan, err := migrate.NewPlan(md.Snapshot, to, migrate.PlanOptions{LockTimeout: md.Config.LockTimeout})
		if err != nil {
			t.Fatal(err)
		}
		files, err := md.Add("", plan, to)
		if err != nil {
			t.Fatal(err)
		}
		for p, data := range files {
			writeFile(t, filepath.Join(dir, p), string(data))
		}
	}
}

// handDir writes a migration directory of dialect d into dir with the
// hand-written files given, by path, an empty schema.json and velox.sum.
func handDir(t *testing.T, dir string, d velox.Dialect, files map[string]string) {
	t.Helper()
	handDirOf(t, dir, &migrate.Schema{Dialect: d}, files)
}

// handDirOf is handDir with snap as schema.json.
func handDirOf(t *testing.T, dir string, snap *migrate.Schema, files map[string]string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "velox.json"), `{"version": 1, "dialect": "`+snap.Dialect.String()+`"}`+"\n")
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "schema.json"), string(data)+"\n")
	for p, src := range files {
		writeFile(t, filepath.Join(dir, p), src)
	}
	sum, err := migrate.Sum(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "velox.sum"), string(sum))
}

// databaseCount counts the databases testDSN made.
var databaseCount atomic.Int64

// testDSN is the DSN of a new, empty database of dialect d, dropped when t
// ends: on PostgreSQL one beside VELOX_TEST_POSTGRES's, on MySQL one made
// through VELOX_TEST_MYSQL_ADMIN. It skips t when the variable is unset.
func testDSN(t *testing.T, d velox.Dialect) string {
	t.Helper()
	v, driver, q := "VELOX_TEST_POSTGRES", "pgx", `"`
	if d == velox.MySQL {
		v, driver, q = "VELOX_TEST_MYSQL_ADMIN", "mysql", "`"
	}
	base := os.Getenv(v)
	if base == "" {
		t.Skip("needs " + v)
	}
	admin, err := sql.Open(driver, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	// The count tells apart two parallel tests that read the same clock.
	name := fmt.Sprintf("velox_gen_%d_%d_%d", os.Getpid(), time.Now().UnixNano(), databaseCount.Add(1))
	if _, err := admin.Exec("CREATE DATABASE " + q + name + q); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		drop := "DROP DATABASE IF EXISTS " + q + name + q
		if d == velox.Postgres {
			drop += " WITH (FORCE)"
		}
		if _, err := admin.Exec(drop); err != nil {
			t.Errorf("%s: %v", drop, err)
		}
	})
	if d == velox.MySQL {
		cfg, err := mysql.ParseDSN(base)
		if err != nil {
			t.Fatal(err)
		}
		cfg.DBName = name
		return cfg.FormatDSN()
	}
	rest, query, _ := strings.Cut(base, "?")
	if i := strings.LastIndex(rest, "/"); i > len("postgres://") {
		rest = rest[:i]
	}
	if query != "" {
		return rest + "/" + name + "?" + query
	}
	return rest + "/" + name
}

// The schemas of the SQLite tests: users, then users with email, then
// posts beside them.
var (
	usersSchema = &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{{
		Name:       "users",
		Columns:    []migrate.Column{{Name: "id", Type: "integer", Identity: true}, {Name: "name", Type: "text"}},
		PrimaryKey: []string{"id"},
	}}}
	usersEmailSchema = &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{{
		Name:       "users",
		Columns:    []migrate.Column{{Name: "id", Type: "integer", Identity: true}, {Name: "name", Type: "text"}, {Name: "email", Type: "text", Nullable: true}},
		PrimaryKey: []string{"id"},
	}}}
	postsSchema = &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{
		{
			Name:       "posts",
			Columns:    []migrate.Column{{Name: "id", Type: "integer", Identity: true}, {Name: "title", Type: "text"}},
			PrimaryKey: []string{"id"},
		},
		usersEmailSchema.Tables[0],
	}}
)

// appliedLine is a line migrate apply prints for a file.
func appliedLine(path string) string {
	return `applied ` + regexp.QuoteMeta(path) + ` in [0-9.]+(ns|µs|ms|s)\n`
}

// matches checks a run against an exit code, a pattern of its whole
// standard output and its standard error.
func matches(t *testing.T, name string, code int, out, errs string, wantCode int, outPattern, wantErrs string) {
	t.Helper()
	if code != wantCode || !regexp.MustCompile(`^`+outPattern+`$`).MatchString(out) || errs != wantErrs {
		t.Errorf("%s: exit %d, want %d\nstdout:\n%s\nwant /%s/\nstderr:\n%s\nwant:\n%s", name, code, wantCode, out, outPattern, errs, wantErrs)
	}
}

const stamp = `[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z`

func TestMigrateApply(t *testing.T) {
	dir := t.TempDir()
	mig := filepath.Join(dir, "migrations")
	writeMigrations(t, mig, velox.SQLite, usersSchema, usersEmailSchema)
	dsn := filepath.Join(dir, "app.db")
	writeFile(t, dsn, "") // status opens only a database that is there
	m := func(sub string, args ...string) (int, string, string) {
		return runCLI(t, dir, append([]string{"migrate", sub, "-db", dsn}, args...)...)
	}
	first, second := filepath.Join("migrations", "0001_create_users.sql"), filepath.Join("migrations", "0002_add_users_email.sql")

	code, out, errs := m("status")
	want(t, "status", code, out, errs, 3, "pending 1 "+first+"\npending 2 "+second+"\n", "")

	code, out, errs = m("apply", "-dry-run")
	files := dirFiles(t, mig)
	want(t, "dry-run", code, out, errs, 0, "== "+first+"\n"+files["0001_create_users.sql"]+"== "+second+"\n"+files["0002_add_users_email.sql"], "")
	code, out, errs = m("apply", "-dry-run", "-to", "1")
	want(t, "dry-run -to", code, out, errs, 0, "== "+first+"\n"+files["0001_create_users.sql"], "")
	code, _, _ = m("status")
	if code != 3 {
		t.Errorf("dry-run applied: status exit %d", code)
	}

	code, out, errs = m("apply", "-to", "1")
	matches(t, "apply -to 1", code, out, errs, 0, appliedLine(first), "")
	code, out, errs = m("status")
	matches(t, "status after 1", code, out, errs, 3, "applied 1 "+regexp.QuoteMeta(first)+" "+stamp+"\npending 2 "+regexp.QuoteMeta(second)+"\n", "")
	code, out, errs = m("apply")
	matches(t, "apply", code, out, errs, 0, appliedLine(second), "")
	code, out, errs = m("apply")
	want(t, "apply again", code, out, errs, 0, "", "")
	code, out, errs = m("status")
	matches(t, "status after", code, out, errs, 0, "applied 1 "+regexp.QuoteMeta(first)+" "+stamp+"\napplied 2 "+regexp.QuoteMeta(second)+" "+stamp+"\n", "")

	code, out, errs = m("status", "-json")
	if code != 0 || errs != "" || !strings.Contains(out, "\n  \"ahead\": [],\n  \"dirty\": null\n}") {
		t.Fatalf("status -json: %d %q\n%s", code, errs, out)
	}
	type applied struct {
		Version    uint64    `json:"version"`
		Name       string    `json:"name"`
		Sum        string    `json:"sum"`
		AppliedAt  time.Time `json:"applied_at"`
		DurationMS int64     `json:"duration_ms"`
		Dirty      bool      `json:"dirty"`
		Statements int       `json:"statements"`
		Error      string    `json:"error"`
	}
	var st struct {
		Applied []applied `json:"applied"`
		Pending []struct {
			Version uint64 `json:"version"`
			Path    string `json:"path"`
		} `json:"pending"`
		Ahead []applied `json:"ahead"`
		Dirty *applied  `json:"dirty"`
	}
	dec := json.NewDecoder(strings.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&st); err != nil {
		t.Fatal(err)
	}
	if len(st.Applied) != 2 || st.Applied[0].Version != 1 || st.Applied[0].Name != "create_users" || st.Applied[1].Name != "add_users_email" ||
		!strings.HasPrefix(st.Applied[0].Sum, "h1:") || st.Applied[0].AppliedAt.IsZero() || st.Applied[0].AppliedAt.Location() != time.UTC ||
		st.Applied[0].Dirty || st.Applied[0].Statements != 1 || len(st.Pending) != 0 || st.Ahead == nil || len(st.Ahead) != 0 || st.Dirty != nil {
		t.Errorf("status -json: %+v", st)
	}
	_, again, _ := m("status", "-json")
	if again != out {
		t.Errorf("status -json twice:\n%s\n%s", out, again)
	}

	writeMigrations(t, mig, velox.SQLite, postsSchema)
	code, out, errs = m("status", "-json")
	if code != 3 || errs != "" || !strings.Contains(out, "\"pending\": [\n    {\n      \"version\": 3,\n      \"path\": \"0003_create_posts.sql\"\n    }\n  ],") {
		t.Errorf("status -json pending: %d %q\n%s", code, errs, out)
	}
}

// TestMigrateApplyBaseline: apply -baseline records the files up to its
// version and applies the rest, once; its misuses are exit 2.
func TestMigrateApplyBaseline(t *testing.T) {
	dir := t.TempDir()
	mig := filepath.Join(dir, "migrations")
	writeMigrations(t, mig, velox.SQLite, usersSchema, usersEmailSchema)
	dsn := filepath.Join(dir, "app.db")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE users (id integer PRIMARY KEY AUTOINCREMENT, name text NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	m := func(args ...string) (int, string, string) {
		return runCLI(t, dir, append([]string{"migrate", "apply", "-db", dsn}, args...)...)
	}
	first, second := filepath.Join("migrations", "0001_create_users.sql"), filepath.Join("migrations", "0002_add_users_email.sql")

	for _, c := range []struct {
		args []string
		text string
	}{
		{[]string{"-baseline", "1", "-resume"}, "-baseline and -resume cannot be combined"},
		{[]string{"-baseline", "1", "-dry-run"}, "-baseline and -dry-run cannot be combined"},
		{[]string{"-to", "1", "-baseline", "2"}, "-to 1 is below -baseline 2"},
	} {
		code, out, errs := m(c.args...)
		if code != 2 || out != "" || !strings.HasPrefix(errs, "velox-gen: migrate apply: "+c.text+"\n") {
			t.Errorf("%v: exit %d\nstdout:\n%s\nstderr:\n%s", c.args, code, out, errs)
		}
	}
	code, out, errs := m("-baseline", "x")
	if code != 2 || out != "" || !strings.HasPrefix(errs, `invalid value "x" for flag -baseline: not a version`) {
		t.Errorf("-baseline x: exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}

	missing := filepath.Join(dir, "typo.db")
	code, out, errs = runCLI(t, dir, "migrate", "apply", "-db", missing, "-baseline", "1")
	want(t, "apply -baseline 1 on a missing file", code, out, errs, 1, "", "velox-gen: migrate apply: "+missing+": no such database\n")
	if _, err := os.Stat(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("apply -baseline 1 on a missing file made it: %v", err)
	}

	empty := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs = runCLI(t, dir, "migrate", "apply", "-db", empty, "-baseline", "1")
	want(t, "apply -baseline 1 on an empty database", code, out, errs, 1, "",
		"velox-gen: migrate apply: the database has no tables besides the history: there is nothing to adopt; apply without a baseline\n")

	code, out, errs = m("-baseline", "1")
	matches(t, "apply -baseline 1", code, out, errs, 0, "baselined "+regexp.QuoteMeta(first)+"\n"+appliedLine(second), "")
	code, out, errs = m("-baseline", "1")
	want(t, "apply -baseline 1 again", code, out, errs, 1, "",
		"velox-gen: migrate apply: the database already has a migration history (2 rows): a baseline adopts a database once; apply without one\n")
	code, out, errs = runCLI(t, dir, "migrate", "status", "-db", dsn)
	matches(t, "status", code, out, errs, 0, "applied 1 "+regexp.QuoteMeta(first)+" "+stamp+"\napplied 2 "+regexp.QuoteMeta(second)+" "+stamp+"\n", "")
}

func TestMigrateDrift(t *testing.T) {
	dir := t.TempDir()
	mig := filepath.Join(dir, "migrations")
	writeMigrations(t, mig, velox.SQLite, usersSchema, usersEmailSchema)
	dsn := filepath.Join(dir, "app.db")
	m := func(sub string, args ...string) (int, string, string) {
		return runCLI(t, dir, append([]string{"migrate", sub, "-db", dsn}, args...)...)
	}
	if code, _, errs := m("apply"); code != 0 {
		t.Fatalf("apply: %d %s", code, errs)
	}
	code, out, errs := m("drift")
	want(t, "drift", code, out, errs, 0, "", "")
	code, out, errs = m("drift", "-json")
	want(t, "drift -json", code, out, errs, 0, "{\n  \"changes\": [],\n  \"warnings\": []\n}\n", "")

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("ALTER TABLE users ADD COLUMN extra text"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	code, out, errs = m("drift")
	want(t, "drift extra", code, out, errs, 3, "change: drop-column users.extra\n", "")
	code, out, errs = m("drift", "-json")
	want(t, "drift extra -json", code, out, errs, 3, `{
  "changes": [
    {
      "kind": "drop-column",
      "target": "users",
      "text": "drop-column users.extra"
    }
  ],
  "warnings": []
}
`, "")
	_, again, _ := m("drift", "-json")
	if again != out {
		t.Errorf("drift -json twice:\n%s\n%s", out, again)
	}

	// With a file pending, drift compares with what the files up to the last
	// applied version make, on a temporary SQLite file.
	writeMigrations(t, mig, velox.SQLite, postsSchema)
	code, out, errs = m("drift")
	note := "velox-gen: note: " + filepath.Join("migrations", "0003_create_posts.sql") + " is pending; compared with what the files up to " +
		filepath.Join("migrations", "0002_add_users_email.sql") + " make\n"
	want(t, "pending", code, out, errs, 3, "change: drop-column users.extra\n", note)
	db, err = sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("ALTER TABLE users DROP COLUMN extra"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	code, out, errs = m("drift")
	want(t, "pending, no drift", code, out, errs, 0, "", note)
}

func TestMigrateAhead(t *testing.T) {
	dir := t.TempDir()
	three, two := filepath.Join(dir, "three"), filepath.Join(dir, "two")
	writeMigrations(t, three, velox.SQLite, usersSchema, usersEmailSchema, postsSchema)
	writeMigrations(t, two, velox.SQLite, usersSchema, usersEmailSchema)
	dsn := filepath.Join(dir, "app.db")
	m := func(d, sub string, args ...string) (int, string, string) {
		return runCLI(t, dir, append([]string{"migrate", sub, "-dir", d, "-db", dsn}, args...)...)
	}
	if code, _, errs := m("three", "apply"); code != 0 {
		t.Fatalf("apply: %d %s", code, errs)
	}
	code, out, errs := m("two", "apply")
	want(t, "apply", code, out, errs, 0, "the database is ahead of the directory (version 3); nothing applied\n", "")
	code, out, errs = m("two", "apply", "-dry-run")
	want(t, "apply -dry-run", code, out, errs, 0, "the database is ahead of the directory (version 3); nothing applied\n", "")
	code, out, errs = m("two", "status")
	matches(t, "status", code, out, errs, 0, "applied 1 two/0001_create_users.sql "+stamp+"\napplied 2 two/0002_add_users_email.sql "+stamp+"\nahead 3 create_posts\n", "")
	code, out, errs = m("two", "drift")
	want(t, "drift", code, out, errs, 1, "", "velox-gen: migrate drift: the database is not at the directory's last version: version 3 is applied above the directory's last file\n")
}

// TestMigrateVersionZero: 0 is a version to -to and to resolve's -version.
func TestMigrateVersionZero(t *testing.T) {
	dir := t.TempDir()
	mig := filepath.Join(dir, "migrations")
	writeMigrations(t, mig, velox.SQLite, usersSchema)
	first := filepath.Join(mig, "0001_create_users.sql")
	if err := os.WriteFile(filepath.Join(mig, "0000_start.sql"), []byte("CREATE TABLE start (x integer);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runCLI(t, dir, "migrate", "hash"); code != 0 {
		t.Fatal(errs)
	}
	dsn := filepath.Join(dir, "app.db")
	m := func(args ...string) (int, string, string) {
		return runCLI(t, dir, append(append([]string{"migrate"}, args...), "-db", dsn)...)
	}
	code, out, errs := m("apply", "-to", "0", "-dry-run")
	if code != 0 || !strings.Contains(out, "0000_start.sql") || strings.Contains(out, filepath.Base(first)) {
		t.Errorf("apply -to 0 -dry-run: %d %q %q", code, out, errs)
	}
	code, out, errs = m("apply", "-to", "0")
	if code != 0 || !strings.Contains(out, "0000_start.sql") || strings.Contains(out, filepath.Base(first)) {
		t.Errorf("apply -to 0: %d %q %q", code, out, errs)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("UPDATE velox_migrations SET dirty = true WHERE version = 0"); err != nil {
		t.Fatal(err)
	}
	code, out, errs = m("resolve", "-version", "0", "-undone")
	want(t, "resolve -version 0", code, out, errs, 0, "resolved version 0 as undone\n", "")
}

// TestMigrateApplyRefusesRebuildLoss: apply stops a rebuild that would lose a
// trigger, saying what to do, and the directive it names, once hashed, lets it
// through.
func TestMigrateApplyRefusesRebuildLoss(t *testing.T) {
	dir := t.TempDir()
	mig := filepath.Join(dir, "migrations")
	writeMigrations(t, mig, velox.SQLite, usersSchema)
	dsn := filepath.Join(dir, "app.db")
	m := func(args ...string) (int, string, string) {
		return runCLI(t, dir, append(append([]string{"migrate"}, args...), "-db", dsn)...)
	}
	if code, _, errs := m("apply"); code != 0 {
		t.Fatal(errs)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("CREATE TRIGGER users_ai AFTER INSERT ON users BEGIN SELECT 1; END"); err != nil {
		t.Fatal(err)
	}
	var create string
	if err := db.QueryRow("SELECT sql FROM sqlite_schema WHERE name = 'users'").Scan(&create); err != nil {
		t.Fatal(err)
	}
	rebuild := strings.Replace(create, `"users"`, `"velox_new_users"`, 1) + ";\nINSERT INTO \"velox_new_users\" SELECT * FROM \"users\";\nDROP TABLE \"users\";\nALTER TABLE \"velox_new_users\" RENAME TO \"users\";\n"
	file := filepath.Join(mig, "0002_rebuild.sql")
	write := func(head string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(head+rebuild), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, _, errs := runCLI(t, dir, "migrate", "hash"); code != 0 {
			t.Fatal(errs)
		}
	}
	write("")
	// The dry run refuses as apply does, before it.
	code, out, errs := m("apply", "-dry-run")
	if code != 1 || out != "" || !strings.Contains(errs, "users.users_ai") {
		t.Errorf("apply -dry-run: exit %d\n%s\n%s", code, out, errs)
	}
	code, _, errs = m("apply")
	if code != 1 {
		t.Errorf("apply: exit %d", code)
	}
	for _, want := range []string{filepath.Join("migrations", "0002_rebuild.sql"), "users.users_ai", "-- velox:allow unmodeled-loss users.users_ai", "migrate hash"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr does not say %q:\n%s", want, errs)
		}
	}
	write("-- velox:allow unmodeled-loss users.users_ai\n")
	if code, out, errs := m("apply", "-dry-run"); code != 0 || !strings.HasPrefix(out, "== "+filepath.Join("migrations", "0002_rebuild.sql")+"\n") {
		t.Errorf("apply -dry-run with the directive: %d %s %s", code, out, errs)
	}
	if code, _, errs := m("apply"); code != 0 {
		t.Errorf("apply with the directive: %d %s", code, errs)
	}
}

func TestMigrateResolve(t *testing.T) {
	dir := t.TempDir()
	mig := filepath.Join(dir, "migrations")
	writeMigrations(t, mig, velox.SQLite, usersSchema)
	dsn := filepath.Join(dir, "app.db")
	env := noVeloxDB()
	m := func(args ...string) (int, string, string) {
		return veloxEnv(t, dir, env, append([]string{"migrate"}, args...)...)
	}
	first := filepath.Join("migrations", "0001_create_users.sql")

	for _, sub := range []string{"apply", "status", "drift", "resolve"} {
		args := []string{sub}
		if sub == "resolve" {
			args = append(args, "-version", "1", "-applied")
		}
		code, out, errs := m(args...)
		want(t, sub+" without a database", code, out, errs, 2, "", "velox-gen: migrate "+sub+" needs -db or $VELOX_DB\n")
	}
	env = append(env, "VELOX_DB="+dsn)
	code, out, errs := m("apply")
	matches(t, "apply from $VELOX_DB", code, out, errs, 0, appliedLine(first), "")

	code, out, errs = m("resolve", "-version", "1", "-applied")
	want(t, "clean", code, out, errs, 1, "", "velox-gen: migrate resolve: version 1 is not dirty\n")
	code, out, errs = m("resolve", "-version", "2", "-undone")
	want(t, "absent", code, out, errs, 1, "", "velox-gen: migrate resolve: version 2 is not in the history table velox_migrations\n")
	for _, args := range [][]string{{"-version", "1"}, {"-applied"}, {"-version", "1", "-applied", "-undone"}, {"-version", "1", "-undone", "extra"}} {
		code, out, errs := m(append([]string{"resolve"}, args...)...)
		if code != 2 || out != "" || !strings.Contains(errs, "Flags of migrate resolve:") {
			t.Errorf("resolve %v: %d %q %q", args, code, out, errs)
		}
	}

	// A version made dirty by hand.
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("UPDATE velox_migrations SET dirty = true, statements = 0, error = 'boom' WHERE version = 1"); err != nil {
		t.Fatal(err)
	}
	code, out, errs = m("status")
	want(t, "dirty status", code, out, errs, 1, "dirty 1 "+first+": 0 statements done: boom\n",
		"velox-gen: migrate status: "+first+": version 1 is dirty: boom\n  fix the cause and run migrate apply -resume, or undo its applied statements and run migrate resolve -version 1 -undone\n")
	code, out, errs = m("status", "-json")
	if code != 1 || !strings.Contains(out, "\"dirty\": {\n    \"version\": 1,") || !strings.Contains(out, "\"error\": \"boom\"") {
		t.Errorf("dirty status -json: %d %q\n%s", code, errs, out)
	}
	code, out, errs = m("apply")
	want(t, "dirty apply", code, out, errs, 1, "",
		"velox-gen: migrate apply: "+first+": version 1 is dirty: boom\n  fix the cause and run migrate apply -resume, or undo its applied statements and run migrate resolve -version 1 -undone\n")
	code, out, errs = m("drift")
	want(t, "dirty drift", code, out, errs, 1, "", "velox-gen: migrate drift: the database is not at the directory's last version: "+first+" is dirty\n")
	code, out, errs = m("resolve", "-version", "1", "-applied")
	want(t, "resolve -applied", code, out, errs, 0, "resolved version 1 as applied\n", "")
	code, out, errs = m("status")
	matches(t, "status after", code, out, errs, 0, "applied 1 "+regexp.QuoteMeta(first)+" "+stamp+"\n", "")
}

func TestMigrateDirtyMySQL(t *testing.T) {
	dsn := testDSN(t, velox.MySQL)
	dir := t.TempDir()
	handDir(t, dir, velox.MySQL, map[string]string{
		"0001_a.sql": "CREATE TABLE a (id int);\n",
		"0002_b.sql": "CREATE TABLE b (id int);\nCREATE TABLE a (id int);\n",
	})
	m := func(args ...string) (int, string, string) {
		return runCLI(t, dir, append([]string{"migrate", args[0], "-dir", ".", "-db", dsn}, args[1:]...)...)
	}
	code, out, errs := m("apply")
	if code != 1 || !regexp.MustCompile(`^`+appliedLine("0001_a.sql")+`$`).MatchString(out) ||
		!strings.HasPrefix(errs, "velox-gen: migrate apply: 0002_b.sql: statement 2 of 2: Error 1050 (42S01): Table 'a' already exists\n") ||
		!strings.Contains(errs, "\n  version 2 is dirty; fix the cause and run migrate apply -resume, or undo statement 1 and run migrate resolve -version 2 -undone\n") ||
		!strings.Contains(errs, "counts 1 of 2 statements") || !strings.Contains(errs, "2. CREATE TABLE a (id int)") ||
		!strings.HasSuffix(errs, "run migrate resolve -version 2 -statements K, then migrate apply -resume\n") {
		t.Errorf("apply: %d\n%s\n%s", code, out, errs)
	}
	code, out, errs = m("status")
	if code != 1 || !regexp.MustCompile(`^applied 1 0001_a.sql `+stamp+"\ndirty 2 0002_b.sql: 1 statement done: Error 1050 \\(42S01\\): Table 'a' already exists\n$").MatchString(out) ||
		!strings.HasPrefix(errs, "velox-gen: migrate status: 0002_b.sql: version 2 is dirty: Error 1050") {
		t.Errorf("status: %d\n%s\n%s", code, out, errs)
	}
	code, out, errs = m("resolve", "-version", "2", "-undone")
	want(t, "resolve", code, out, errs, 0, "resolved version 2 as undone\n", "")
	code, out, errs = m("status")
	matches(t, "status after", code, out, errs, 3, "applied 1 0001_a.sql "+stamp+"\npending 2 0002_b.sql\n", "")
}

// TestMigrateCancel: an apply cancelled while a statement runs, as Ctrl-C
// cancels it, leaves its version dirty with the reason and exits 1.
func TestMigrateCancel(t *testing.T) {
	for _, c := range []struct {
		d     velox.Dialect
		files map[string]string
	}{
		{velox.Postgres, map[string]string{"0001_slow.sql": "-- velox:no-transaction\nCREATE TABLE a (id int);\nSELECT pg_sleep(30);\nCREATE TABLE b (id int);\n"}},
		{velox.MySQL, map[string]string{"0001_slow.sql": "CREATE TABLE a (id int);\nSELECT SLEEP(30);\nCREATE TABLE b (id int);\n"}},
	} {
		t.Run(c.d.String(), func(t *testing.T) {
			dsn := testDSN(t, c.d)
			dir := t.TempDir()
			handDir(t, dir, c.d, c.files)
			signalContext = func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				time.AfterFunc(2*time.Second, cancel)
				return ctx, cancel
			}
			t.Cleanup(func() { signalContext = notifyContext })
			// On PostgreSQL the SELECT runs in a transaction with its count, which a
			// cancel rolls back; on MySQL it may finish on the server, and the way out
			// leads with checking it.
			way := "\n  version 1 is dirty; fix the cause and run migrate apply -resume, or undo statement 1 and run migrate resolve -version 1 -undone\n"
			if c.d == velox.MySQL {
				way = "the run was cancelled during statement 2; check whether it ran before resuming: context canceled\n" +
					"  version 1 is dirty, and the run stopped with statement 2 under way: it may have run on the server after the history's count of 1"
			}
			start := time.Now()
			code, out, errs := runCLI(t, dir, "migrate", "apply", "-dir", ".", "-db", dsn)
			if code != 1 || out != "" || time.Since(start) > 20*time.Second ||
				!strings.HasPrefix(errs, "velox-gen: migrate apply: 0001_slow.sql: statement 2 of 3: ") ||
				!strings.Contains(errs, way) {
				t.Errorf("apply: %d %v\n%s\n%s", code, time.Since(start), out, errs)
			}
			signalContext = notifyContext
			code, out, _ = runCLI(t, dir, "migrate", "status", "-dir", ".", "-db", dsn)
			if code != 1 || !strings.HasPrefix(out, "dirty 1 0001_slow.sql: 1 statement done: ") {
				t.Errorf("status: %d\n%s", code, out)
			}
		})
	}

	// A file in a transaction is rolled back: no history row, nothing made.
	t.Run("transaction", func(t *testing.T) {
		dsn := testDSN(t, velox.Postgres)
		dir := t.TempDir()
		handDir(t, dir, velox.Postgres, map[string]string{"0001_slow.sql": "CREATE TABLE a (id int);\nSELECT pg_sleep(30);\n"})
		signalContext = func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(2*time.Second, cancel)
			return ctx, cancel
		}
		t.Cleanup(func() { signalContext = notifyContext })
		start := time.Now()
		code, out, errs := runCLI(t, dir, "migrate", "apply", "-dir", ".", "-db", dsn)
		if code != 1 || out != "" || time.Since(start) > 20*time.Second || !strings.HasPrefix(errs, "velox-gen: migrate apply: ") {
			t.Errorf("apply: %d %v\n%s\n%s", code, time.Since(start), out, errs)
		}
		signalContext = notifyContext
		code, out, errs = runCLI(t, dir, "migrate", "status", "-dir", ".", "-db", dsn)
		want(t, "status", code, out, errs, 3, "pending 1 0001_slow.sql\n", "")
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		var rows, tables int
		if err := db.QueryRow("SELECT count(*) FROM velox_migrations").Scan(&rows); err != nil || rows != 0 {
			t.Errorf("history rows: %d %v", rows, err)
		}
		if err := db.QueryRow("SELECT count(*) FROM pg_tables WHERE tablename = 'a'").Scan(&tables); err != nil || tables != 0 {
			t.Errorf("table a: %d %v", tables, err)
		}
	})
}

// TestMigrateDSNHidden: no message shows the DSN's password, though the
// driver's error would.
func TestMigrateDSNHidden(t *testing.T) {
	for _, c := range []struct {
		d   velox.Dialect
		dsn string
	}{
		{velox.Postgres, "postgres://velox:s3cret%2Fpw@127.0.0.1:1/velox?connect_timeout=2"},
		{velox.Postgres, "host=127.0.0.1 port=1 user=velox password=s3cretpw dbname=velox connect_timeout=2"},
		{velox.Postgres, "host=127.0.0.1 port=1 user=velox password='s3cret pw' dbname=velox connect_timeout=2"},
		{velox.MySQL, "velox:s3cretpw@tcp(127.0.0.1:1)/velox?timeout=2s"},
	} {
		dir := t.TempDir()
		handDir(t, dir, c.d, map[string]string{"0001_a.sql": "SELECT 1;\n"})
		for _, args := range [][]string{{"status"}, {"apply"}, {"drift"}, {"resolve", "-version", "1", "-undone"}} {
			for _, env := range [][]string{nil, {"VELOX_DB=" + c.dsn}} {
				full := append([]string{"migrate"}, args...)
				full = append(full, "-dir", ".")
				if env == nil {
					full = append(full, "-db", c.dsn)
				}
				code, out, errs := veloxEnv(t, dir, append(noVeloxDB(), env...), full...)
				if code != 1 || strings.Contains(out+errs, "s3cret") || !strings.HasPrefix(errs, "velox-gen: migrate "+args[0]+": ") {
					t.Errorf("%s %v: %d\n%s\n%s", c.dsn, full, code, out, errs)
				}
			}
		}
	}
}

// TestRedact: an error that echoes the DSN, in any of its spellings, or
// its password where a DSN holds it, shows neither; the rest of the message
// stays whole.
func TestRedact(t *testing.T) {
	for _, c := range []struct {
		d       velox.Dialect
		dsn, pw string
	}{
		{velox.Postgres, "postgres://velox:s3cret%2Fpw@db.example:5432/velox?sslmode=disable", "s3cret/pw"},
		{velox.Postgres, "postgres://velox:s3cret@pw@db.example:notaport/velox", "s3cret@pw"},
		{velox.Postgres, "host=db.example user=velox password=s3cretpw dbname=velox", "s3cretpw"},
		{velox.Postgres, "host=db.example user=velox password='s3cret pw' dbname=velox", "s3cret pw"},
		{velox.MySQL, "velox:s3cret@pw@tcp(db.example:3306)/velox?parseTime=true", "s3cret@pw"},
		{velox.MySQL, "velox:s3cretpw@tcp(db.example:3306)/velox?nope=%zz", "s3cretpw"},
		{velox.SQLite, "file:/data/app.db?_pragma=key('s3cret')", ""},
	} {
		s := secrets(c.d, c.dsn)
		for _, text := range []string{
			"cannot connect to " + c.dsn + ": refused",
			"bad DSN " + strings.ReplaceAll(c.dsn, "%2F", "/"),
			"as velox:" + c.pw + "@db.example",
			"as velox:" + url.QueryEscape(c.pw) + "@db.example",
			"user=velox password=" + c.pw + " dbname=velox",
			"user=velox password='" + c.pw + "' dbname=velox",
			"uid=velox;pwd=" + c.pw + ";",
		} {
			if got := s.redact(text); strings.Contains(got, "s3cret") {
				t.Errorf("%s: %q shows it", c.dsn, got)
			}
		}
	}

	// A common password leaves the message's words alone.
	s := secrets(velox.Postgres, "postgres://velox:velox@db.example/velox")
	msg := "version 1 is dirty: velox.json, velox_migrations; run migrate resolve -version 1 -undone"
	if got := s.redact(msg); got != msg {
		t.Errorf("redacted what holds no secret: %q", got)
	}
	got := s.redact("dial postgres://velox:velox@db.example/velox, as velox:velox@db.example, password=velox: velox_migrations in velox.json")
	if want := "dial xxxxx, as velox:xxxxx@db.example, password=xxxxx: velox_migrations in velox.json"; got != want {
		t.Errorf("redact: %q, want %q", got, want)
	}
	// pgconn's echo of what it read from a DSN.
	got = s.redact("failed to connect to `user=velox database=velox`: refused; cannot parse `postgres://velox:xxxxxx@db.example/velox`: x")
	if want := "failed to connect to `xxxxx`: refused; cannot parse `xxxxx`: x"; got != want {
		t.Errorf("redact: %q, want %q", got, want)
	}
	// A SQLite path is named; a DSN with parameters is not.
	if got := secrets(velox.SQLite, "/data/app.db").redact("/data/app.db: no such database"); got != "/data/app.db: no such database" {
		t.Errorf("sqlite path: %q", got)
	}
}

// TestRedactJoined: a DSN or password holding a file's name is hidden
// after the name is joined to -dir.
func TestRedactJoined(t *testing.T) {
	for _, c := range []struct {
		d          velox.Dialect
		dsn, extra string
	}{
		{velox.SQLite, "file:/data/velox.sum?_pragma=key('s3cret')", ""},
		{velox.Postgres, "postgres://velox:s3cret.velox.json@db.example/velox", "; as velox:s3cret.velox.json@db.example"},
	} {
		cmd := newDBCmd("status", io.Discard)
		*cmd.dir = "migrations"
		if code, ok := func() (int, bool) { _, code, ok := cmd.open(c.d, c.dsn, "-db", false); return code, ok }(); !ok {
			t.Fatalf("%s: open %d", c.dsn, code)
		}
		got := cmd.text("open " + c.dsn + ": velox.json is missing" + c.extra)
		if strings.Contains(got, "s3cret") || !strings.Contains(got, filepath.Join("migrations", "velox.json")+" is missing") {
			t.Errorf("%s: %q", c.dsn, got)
		}
	}
}

// TestDirFileIsMigrates: the names the CLI joins to -dir are the names
// migrate reads as migration files.
func TestDirFileIsMigrates(t *testing.T) {
	for _, name := range []string{
		"0001_a.sql", "0002_add_users_email.sql", "12345_x_9.sql", "0001__a.sql",
		"001_a.sql", "0001_A.sql", "0001-a.sql", "0001_a.SQL", "0001_.sql", "0001_a-b.sql", "0001a.sql", "a_0001_a.sql",
	} {
		_, err := migrate.ParseFile(velox.SQLite, name, []byte("SELECT 1;\n"))
		if joined := dirFile.FindString(name) == name; joined != (err == nil) {
			t.Errorf("%s: dirFile matches it %v, migrate reads it %v (%v)", name, joined, err == nil, err)
		}
	}
}

// TestMigrateDSNForm: a -db not of the dialect's form is exit 2, before it
// is used, and the message shows nothing of it.
func TestMigrateDSNForm(t *testing.T) {
	pg := "-db is not a PostgreSQL connection string (velox.json's dialect is postgres)"
	my := "-db is not a MySQL DSN (velox.json's dialect is mysql)"
	for _, c := range []struct {
		d        velox.Dialect
		dsn, msg string
	}{
		{velox.Postgres, "postgress://appuser:hunter22@dbhost.example:5432/appdb", pg},
		{velox.Postgres, "appuser:hunter22@tcp(dbhost.example:3306)/appdb", pg},
		{velox.Postgres, "mysql://appuser:hunter22@dbhost.example/appdb", pg},
		{velox.Postgres, "host=dbhost.example port=notaport user=appuser password=hunter22 dbname=appdb", pg},
		{velox.Postgres, "postgres://appuser:hunter22@dbhost.example/appdb?sslmode=verify-full&sslrootcert=/nonexistent/root.crt",
			"-db names a file that cannot be read: open /nonexistent/root.crt: no such file or directory"},
		{velox.MySQL, "postgres://appuser:hunter22@dbhost.example:5432/appdb", my + "; MySQL DSNs look like user:pass@tcp(host:3306)/db"},
		{velox.MySQL, "mysql://appuser:hunter22@tcp(dbhost.example:3306)/appdb", my + "; MySQL DSNs look like user:pass@tcp(host:3306)/db"},
		{velox.MySQL, "appuser:hunter22@tcp(dbhost.example:3306)/appdb?nope=%zz", my},
		{velox.SQLite, "postgres://appuser:hunter22@dbhost.example/appdb", "-db is not a SQLite path or URI (velox.json's dialect is sqlite)"},
	} {
		dir := t.TempDir()
		handDir(t, dir, c.d, map[string]string{"0001_a.sql": "SELECT 1;\n"})
		for _, args := range [][]string{{"status"}, {"apply"}, {"drift"}, {"resolve", "-version", "1", "-undone"}} {
			full := append(append([]string{"migrate"}, args...), "-dir", ".", "-db", c.dsn)
			code, out, errs := veloxEnv(t, dir, noVeloxDB(), full...)
			want(t, c.dsn+" "+args[0], code, out, errs, 2, "", "velox-gen: migrate "+args[0]+": "+c.msg+"\n")
			for _, part := range []string{"appuser", "hunter22", "dbhost", "appdb"} {
				if strings.Contains(out+errs, part) {
					t.Errorf("%s %s: shows %s", c.dsn, args[0], part)
				}
			}
		}
		code, out, errs := veloxEnv(t, dir, append(noVeloxDB(), "VELOX_DB="+c.dsn), "migrate", "status", "-dir", ".")
		want(t, c.dsn+" $VELOX_DB", code, out, errs, 2, "", "velox-gen: migrate status: $VELOX_DB"+strings.TrimPrefix(c.msg, "-db")+"\n")
	}

	// A common password leaves the message whole.
	dir := t.TempDir()
	handDir(t, dir, velox.Postgres, map[string]string{"0001_a.sql": "SELECT 1;\n"})
	code, out, errs := runCLI(t, dir, "migrate", "status", "-dir", ".", "-db", "postgres://velox:velox@127.0.0.1:1/velox?connect_timeout=2")
	if code != 1 || out != "" || !strings.HasPrefix(errs, "velox-gen: migrate status: ") || strings.Contains(errs, "xxxxx_") ||
		strings.Contains(errs, "velox:velox") || strings.Contains(errs, "user=velox") {
		t.Errorf("velox:velox: %d %q", code, errs)
	}
}

// TestMigrateSQLiteMissing: status and drift do not make a SQLite database
// that is not there; apply -dry-run shows every file pending and makes
// nothing; apply makes it.
func TestMigrateSQLiteMissing(t *testing.T) {
	dir := t.TempDir()
	writeMigrations(t, filepath.Join(dir, "migrations"), velox.SQLite, usersSchema)
	for _, dsn := range []string{filepath.Join(dir, "app.db"), "file:" + filepath.Join(dir, "app.db") + "?_pragma=foreign_keys(1)"} {
		code, out, errs := runCLI(t, dir, "migrate", "apply", "-dry-run", "-db", dsn)
		if code != 0 || errs != "" || !strings.HasPrefix(out, "== migrations/0001_create_users.sql\n") {
			t.Errorf("apply -dry-run: %d %q\n%s", code, errs, out)
		}
		if exists(filepath.Join(dir, "app.db")) {
			t.Fatalf("apply -dry-run %s made the database", dsn)
		}
		for _, args := range [][]string{{"status"}, {"drift"}} {
			sub := args[0]
			code, out, errs := runCLI(t, dir, append(append([]string{"migrate"}, args...), "-db", dsn)...)
			want(t, sub, code, out, errs, 1, "", "velox-gen: migrate "+sub+": "+filepath.Join(dir, "app.db")+": no such database\n")
			if exists(filepath.Join(dir, "app.db")) {
				t.Fatalf("%s %s made the database", sub, dsn)
			}
		}
	}
	dsn := "file:" + filepath.Join(dir, "app.db") + "?_pragma=foreign_keys(1)"
	if code, _, errs := runCLI(t, dir, "migrate", "apply", "-db", dsn); code != 0 {
		t.Fatalf("apply: %d %s", code, errs)
	}
	code, out, errs := runCLI(t, dir, "migrate", "status", "-db", dsn)
	matches(t, "status", code, out, errs, 0, "applied 1 "+regexp.QuoteMeta(filepath.Join("migrations", "0001_create_users.sql"))+" "+stamp+"\n", "")
}

// TestMigrateSQLiteMemory: a SQLite database in memory, which ends with its
// connection, is refused by every subcommand that opens one.
func TestMigrateSQLiteMemory(t *testing.T) {
	dir := t.TempDir()
	writeMigrations(t, filepath.Join(dir, "migrations"), velox.SQLite, usersSchema)
	for _, dsn := range []string{":memory:", "file::memory:?cache=shared", "file:app.db?mode=memory"} {
		for _, args := range [][]string{{"status"}, {"apply"}, {"apply", "-dry-run"}, {"drift"}, {"resolve", "-version", "1", "-undone"}} {
			code, out, errs := runCLI(t, dir, append(append([]string{"migrate"}, args...), "-db", dsn)...)
			want(t, dsn+" "+strings.Join(args, " "), code, out, errs, 2, "", "velox-gen: migrate "+args[0]+": -db is a SQLite database in memory, which ends with its connection; name a file\n")
		}
	}
}

// TestMigrateDryRunLockWait: apply -dry-run waits for a writer as long as
// -lock-wait says.
func TestMigrateDryRunLockWait(t *testing.T) {
	dir := t.TempDir()
	writeMigrations(t, filepath.Join(dir, "migrations"), velox.SQLite, usersSchema)
	path := filepath.Join(dir, "app.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "CREATE TABLE x (id integer)"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	release := time.AfterFunc(2*time.Second, func() { _, _ = conn.ExecContext(ctx, "ROLLBACK") })
	defer release.Stop()
	start := time.Now()
	code, _, errs := runCLI(t, dir, "migrate", "apply", "-dry-run", "-lock-wait", "100ms", "-db", path)
	if code != 1 || time.Since(start) > 1500*time.Millisecond {
		t.Errorf("exit %d after %s: %s; want 1 within the wait", code, time.Since(start), errs)
	}
}

// TestMigrateDriftWarning: a difference drift only warns about is printed
// and is exit 0.
func TestMigrateDriftWarning(t *testing.T) {
	dir := t.TempDir()
	snap := &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{{
		Name:       "t",
		Columns:    []migrate.Column{{Name: "id", Type: "integer", Identity: true}, {Name: "at", Type: "text", Default: "CURRENT_TIMESTAMP"}},
		PrimaryKey: []string{"id"},
	}}}
	handDirOf(t, dir, snap, map[string]string{
		"0001_t.sql": "CREATE TABLE t (id integer NOT NULL PRIMARY KEY AUTOINCREMENT, at text NOT NULL DEFAULT (datetime('now')));\n",
	})
	dsn := filepath.Join(dir, "app.db")
	if code, _, errs := runCLI(t, dir, "migrate", "apply", "-dir", ".", "-db", dsn); code != 0 {
		t.Fatalf("apply: %d %s", code, errs)
	}
	code, out, errs := runCLI(t, dir, "migrate", "drift", "-dir", ".", "-db", dsn)
	want(t, "drift", code, out, errs, 0, "warning: t.at: the default is (datetime('now')) in the database and CURRENT_TIMESTAMP in the snapshot\n", "")
	code, out, errs = runCLI(t, dir, "migrate", "drift", "-dir", ".", "-db", dsn, "-json")
	want(t, "drift -json", code, out, errs, 0, `{
  "changes": [],
  "warnings": [
    {
      "object": "t.at",
      "message": "the default is (datetime('now')) in the database and CURRENT_TIMESTAMP in the snapshot"
    }
  ]
}
`, "")
}
