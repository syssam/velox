package main

import (
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"

	"github.com/syssam/velox2/internal/codegen"
	"github.com/syssam/velox2/migrate"
)

// runMigrateBaseline is velox-gen migrate baseline: it reads the schema
// package's tables from the database and writes a new directory whose
// 0001_baseline.sql builds them.
func runMigrateBaseline(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	c := newDBCmd("baseline", stderr)
	c.whose = "-dialect"
	var dialect dialectFlag
	c.fs.Var(&dialect, "dialect", "the dialect of the database and the new directory: postgres, mysql or sqlite")
	patterns, code, done := parseFlags(c.fs, args)
	if done {
		return code
	}
	if len(patterns) > 1 {
		return tooMany("migrate baseline", c.fs, stderr)
	}
	if dialect.d == 0 {
		_, _ = fmt.Fprintln(stderr, "velox-gen: migrate baseline needs -dialect postgres, mysql or sqlite")
		c.fs.Usage()
		return 2
	}
	dsn, src, ok := c.dsn(env)
	if !ok {
		return 2
	}
	if err := dsnForm(dialect.d, dsn, src, c.whose); err != nil {
		return c.fail(2, err)
	}

	// Baseline starts a directory; it never adds to one.
	abs := c.abs(dir)
	state, err := dirState(abs)
	if err != nil {
		return c.fail(1, err)
	}
	if state == migrationDir {
		return c.fail(1, fmt.Errorf("%s is a migration directory already; baseline starts a new one", filepath.Clean(*c.dir)))
	}

	// The schema package, as -dialect sees it.
	pattern, err := schemaPackage(dir, patterns, env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", err)
		return 1
	}
	want, err := func() (*migrate.Schema, error) {
		m, err := codegen.Load(dir, pattern, env)
		if err != nil {
			return nil, err
		}
		g, err := codegen.Build(m)
		if err != nil {
			return nil, err
		}
		return codegen.Tables(g, dialect.d)
	}()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", relErrors(dir, err))
		return 1
	}

	// The database's form of those tables.
	db, code, ok := c.open(dialect.d, dsn, src, true)
	if !ok {
		return code
	}
	defer func() { _ = db.Close() }()
	ctx, stop := signalContext()
	defer stop()
	snap, warnings, err := migrate.Baseline(ctx, db, want)
	if err != nil {
		for _, e := range unjoin(err) {
			c.fail(1, e)
		}
		return 1
	}

	// The new directory.
	d, err := migrate.NewDir(migrate.Config{Dialect: dialect.d})
	if err != nil {
		return c.fail(1, err)
	}
	plan, err := migrate.NewPlan(d.Snapshot, snap, migrate.PlanOptions{LockTimeout: d.Config.LockTimeout})
	if err != nil {
		return c.fail(1, relMigrate(dir, err))
	}
	files, err := d.Add("baseline", plan, snap)
	if err != nil {
		return c.fail(1, err)
	}
	interrupted, err := writeDirHeld(abs, files)
	if err != nil {
		return c.stopped(interrupted, c.fail(1, err))
	}
	for _, p := range slices.Sorted(maps.Keys(files)) {
		_, _ = fmt.Fprintln(stdout, c.path(p))
	}
	for _, w := range warnings {
		_, _ = fmt.Fprintf(stderr, "velox-gen: migrate baseline: warning: %s: %s\n", w.Object, w.Message)
	}
	// The directory is new, so its last version is its number of scripts.
	_, _ = fmt.Fprintf(stderr, "velox-gen: migrate baseline: run velox-gen migrate apply -baseline %d once on each existing database, then migrate drift there\n", len(plan.Scripts))
	return c.stopped(interrupted, 0)
}
