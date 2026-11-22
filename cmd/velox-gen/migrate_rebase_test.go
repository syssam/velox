package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateRebase runs migrations-operations.md's "Two branches took one
// version": each branch wrote 0002 from 0001; after the merge, rebase moves
// the second to 0003 with its hand-written statement, schema.json and
// velox.sum follow, and diff -check passes. A schema package with a change
// no file makes is refused, and nothing is written.
func TestMigrateRebase(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"veloxschema/user.go": userSchema(`field.String("name")`)})
	mig := filepath.Join(dir, "migrations")
	setSchema := func(fields ...string) {
		writeFile(t, filepath.Join(dir, "veloxschema", "user.go"), userSchema(fields...))
	}
	run := func(args ...string) string {
		t.Helper()
		code, out, errs := runCLI(t, dir, args...)
		if code != 0 {
			t.Fatalf("%v: %d\n%s\n%s", args, code, out, errs)
		}
		return out
	}
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(mig, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	run("migrate", "diff", "-dialect", "sqlite")
	base := map[string]string{"schema.json": read("schema.json"), "velox.sum": read("velox.sum")}

	// The main branch adds email.
	setSchema(`field.String("name")`, `field.String("email").Nillable()`)
	run("migrate", "diff")
	main := map[string]string{"0002_add_users_email.sql": read("0002_add_users_email.sql"), "schema.json": read("schema.json"), "velox.sum": read("velox.sum")}

	// This branch, from 0001, adds bio, with a statement by hand.
	if err := os.Remove(filepath.Join(mig, "0002_add_users_email.sql")); err != nil {
		t.Fatal(err)
	}
	for name, data := range base {
		writeFile(t, filepath.Join(mig, name), data)
	}
	setSchema(`field.String("name")`, `field.String("bio").Nillable()`)
	run("migrate", "diff")
	mine := read("0002_add_users_bio.sql") + "UPDATE `users` SET `bio` = 'none';\n"
	writeFile(t, filepath.Join(mig, "0002_add_users_bio.sql"), mine)

	// The merge: main's files, both 0002s, and both fields; velox.sum is left
	// in conflict.
	for name, data := range main {
		writeFile(t, filepath.Join(mig, name), data)
	}
	writeFile(t, filepath.Join(mig, "velox.sum"), "<<<<<<< HEAD\n=======\n>>>>>>> mine\n")
	setSchema(`field.String("name")`, `field.String("email").Nillable()`, `field.String("bio").Nillable()`, `field.String("extra").Nillable()`)
	code, out, errs := runCLI(t, dir, "migrate", "rebase", "0002_add_users_bio.sql")
	if code != 1 || out != "" || !strings.Contains(errs, "after the rebase, the files do not make the schema package's tables, so nothing is written") || !strings.Contains(errs, "users.extra") {
		t.Errorf("a change no file makes: %d %q %q", code, out, errs)
	}
	if read("0002_add_users_bio.sql") != mine || read("schema.json") != main["schema.json"] {
		t.Error("a refused rebase wrote")
	}

	setSchema(`field.String("name")`, `field.String("email").Nillable()`, `field.String("bio").Nillable()`)
	out = run("migrate", "rebase", "migrations/0002_add_users_bio.sql")
	if want := "migrations/0002_add_users_bio.sql -> migrations/0003_add_users_bio.sql\nmigrations/schema.json\nmigrations/velox.sum\n"; out != want {
		t.Errorf("rebase printed %q, want %q", out, want)
	}
	if exists(filepath.Join(mig, "0002_add_users_bio.sql")) || read("0003_add_users_bio.sql") != mine || read("0002_add_users_email.sql") != main["0002_add_users_email.sql"] {
		t.Error("the files were not moved as they are")
	}
	run("migrate", "diff", "-check")
	if out := run("migrate", "diff"); out != "no changes\n" {
		t.Errorf("diff after the rebase: %q", out)
	}

	code, out, errs = runCLI(t, dir, "migrate", "rebase")
	if code != 2 || out != "" || !strings.Contains(errs, "needs the migration files to move") {
		t.Errorf("no file: %d %q %q", code, out, errs)
	}
	code, _, errs = runCLI(t, dir, "migrate", "rebase", "0009_nope.sql")
	if code != 1 || !strings.Contains(errs, "0009_nope.sql is not a migration file of the directory") {
		t.Errorf("a missing file: %d %q", code, errs)
	}
}
