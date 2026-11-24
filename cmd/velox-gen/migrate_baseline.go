package main

import (
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"

	"github.com/syssam/velox/internal/codegen"
	"github.com/syssam/velox/internal/genrun"
	"github.com/syssam/velox/migrate"
)

// runMigrateBaseline is velox-gen migrate baseline: it reads the schema
// package's tables from the database and writes a new directory whose
// 0001_baseline.sql builds them. With -foreign-keys=false the directory
// holds no foreign key, the database's included, each named in a warning.
func runMigrateBaseline(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	c := newDBCmd("baseline", stderr)
	c.whose = "-dialect"
	var dialect dialectFlag
	c.fs.Var(&dialect, "dialect", "the dialect of the database and the new directory: postgres, mysql or sqlite")
	foreignKeys := c.fs.Bool("foreign-keys", true, "false writes no foreign key, the database's included, and records \"foreign_keys\": false in velox.json, as ent's migrate.WithForeignKeys(false)")
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
	pattern, err := genrun.SchemaPackage(dir, patterns, env)
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
		s, err := codegen.Tables(g, dialect.d)
		if err != nil || *foreignKeys {
			return s, err
		}
		return s.WithoutForeignKeys(), nil
	}()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", genrun.RelErrors(dir, err))
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
	if !*foreignKeys {
		warnings = append(warnings, leftOutKeys(snap)...)
		snap = snap.WithoutForeignKeys()
	}

	// The new directory.
	d, err := migrate.NewDir(migrate.Config{Dialect: dialect.d, NoForeignKeys: !*foreignKeys})
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

// leftOutKeys is a warning for each foreign key of snap, the database's,
// which baseline -foreign-keys=false leaves out of the directory.
func leftOutKeys(snap *migrate.Schema) []migrate.Warning {
	var out []migrate.Warning
	for _, t := range snap.Tables {
		for _, fk := range t.ForeignKeys {
			out = append(out, migrate.Warning{
				Object:  t.Ref().String() + "." + fk.Name,
				Message: "the database has this foreign key and -foreign-keys=false leaves it out of the directory, so migrate drift reports it until it is dropped",
			})
		}
	}
	return out
}
