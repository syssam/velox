package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/syssam/velox/internal/genrun"
	"github.com/syssam/velox/migrate"
)

// runMigrateRebase is velox-gen migrate rebase: it moves the migration files
// named, a branch's, to the versions after the directory's other files, with
// migrate.Rebase, checks that the files then make the schema package's
// tables, and writes them with schema.json and velox.sum.
func runMigrateRebase(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	c := newMigrateCmd("rebase", stderr)
	scratch := c.fs.String("scratch-db", "", "an empty database's `dsn`, which the files are applied to, to compare with the schema package (default on SQLite: a temporary file)")
	replay := c.fs.Bool("replay", true, "apply the files to a scratch database and compare the result with the schema package before writing; -replay=false writes unchecked")
	rest, code, done := parseFlags(c.fs, args)
	if done {
		return code
	}
	var files, patterns []string
	for _, a := range rest {
		if strings.HasSuffix(a, ".sql") {
			files = append(files, filepath.Base(a))
		} else {
			patterns = append(patterns, a)
		}
	}
	switch {
	case len(files) == 0:
		_, _ = fmt.Fprintln(stderr, "velox-gen: migrate rebase needs the migration files to move, as 0002_mine.sql")
		c.fs.Usage()
		return 2
	case len(patterns) > 1:
		return tooMany("migrate rebase", c.fs, stderr)
	case *scratch != "" && !*replay:
		_, _ = fmt.Fprintln(stderr, "velox-gen: migrate rebase: -scratch-db and -replay=false cannot be combined")
		c.fs.Usage()
		return 2
	}
	if err := c.exists(dir); err != nil {
		return c.fail(1, err)
	}
	abs := c.abs(dir)
	raw, err := os.ReadFile(filepath.Join(abs, "velox.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return c.fail(1, fmt.Errorf("%s: not a migration directory (no velox.json)", filepath.Clean(*c.dir)))
	}
	if err != nil {
		return c.fail(1, err)
	}
	var cfg struct{ Dialect string }
	var dialect dialectFlag
	if err := json.Unmarshal(raw, &cfg); err != nil || dialect.Set(cfg.Dialect) != nil {
		return c.fail(1, errors.New("velox.json: no dialect postgres, mysql or sqlite"))
	}

	pattern, err := genrun.SchemaPackage(dir, patterns, env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", err)
		return 1
	}
	to, err := schemaTables(dir, pattern, env, dialect.d)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", genrun.RelErrors(dir, err))
		return 1
	}
	out, moved, err := migrate.Rebase(os.DirFS(abs), files, to)
	if err != nil {
		return c.fail(1, c.inDir(err))
	}
	if *replay {
		result := fstest.MapFS{}
		for p, data := range out {
			result[p] = &fstest.MapFile{Data: data}
		}
		if code := c.replayed(result, dialect.d, *scratch, "rebase", "after the rebase, the files do not make the schema package's tables, so nothing is written; run migrate diff for a change no file makes yet"); code != 0 {
			return code
		}
	}

	write := map[string][]byte{"schema.json": out["schema.json"], "velox.sum": out["velox.sum"]}
	for _, np := range moved {
		write[np] = out[np]
	}
	olds := slices.Sorted(maps.Keys(moved))
	var remove []string
	for _, old := range olds {
		if moved[old] != old { // a file that is already last stays
			remove = append(remove, old)
		}
	}
	interrupted, err := genrun.WriteAllHeld(abs, write, remove)
	if err != nil {
		return c.stopped(interrupted, c.fail(1, err))
	}
	for _, old := range olds {
		_, _ = fmt.Fprintf(stdout, "%s -> %s\n", c.path(old), c.path(moved[old]))
	}
	_, _ = fmt.Fprintln(stdout, c.path("schema.json"))
	_, _ = fmt.Fprintln(stdout, c.path("velox.sum"))
	return c.stopped(interrupted, 0)
}
