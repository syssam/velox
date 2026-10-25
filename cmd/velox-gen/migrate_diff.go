package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/syssam/velox2/internal/gen"
	"github.com/syssam/velox2/migrate"
)

// allowFlag is -allow: the destructive changes allowed, as ParseAllow reads
// them. Given twice, the lists add up.
type allowFlag []migrate.Allow

func (f *allowFlag) String() string { return "" }

func (f *allowFlag) Set(s string) error {
	as, err := migrate.ParseAllow(s)
	if err != nil {
		return errors.New(strings.TrimPrefix(err.Error(), "migrate: "))
	}
	*f = append(*f, as...)
	return nil
}

// timeoutFlag is -lock-timeout: a duration of 0 or more, and whether it
// was given.
type timeoutFlag struct {
	d   time.Duration
	set bool
}

func (f *timeoutFlag) String() string {
	if f == nil || !f.set {
		return ""
	}
	return f.d.String()
}

func (f *timeoutFlag) Set(s string) error {
	d, err := time.ParseDuration(s)
	switch {
	case err != nil:
		return errors.New("not a duration, as 5s")
	case d < 0:
		return errors.New("the duration is negative")
	}
	f.d, f.set = d, true
	return nil
}

// A rename is one -rename-table or -rename-column, as given.
type rename struct {
	raw      string // the flag and its value, "-rename-column users.nme=x", for messages
	table    migrate.TableRef
	column   string // "" for a table rename
	to       string // the new column name, or the new table's name
	toSchema string // a table rename's new schema
}

// renameFlag is -rename-table or -rename-column, repeatable.
type renameFlag struct {
	column bool
	list   *[]rename
}

func (f renameFlag) String() string { return "" }

func (f renameFlag) Set(s string) error {
	from, to, ok := strings.Cut(s, "=")
	r := rename{raw: "-rename-table " + s}
	if f.column {
		r.raw = "-rename-column " + s
	}
	if !ok || strings.Contains(to, "=") {
		if f.column {
			return errors.New("write table.old=new")
		}
		return errors.New("write old=new")
	}
	if f.column {
		i := strings.LastIndexByte(from, '.')
		if i < 0 || i == len(from)-1 || !simpleName(to) {
			return errors.New("write table.old=new, the table optionally schema.table")
		}
		r.column, r.to, from = from[i+1:], to, from[:i]
	} else {
		ref, ok := tableRef(to)
		if !ok {
			return errors.New("write old=new, each optionally schema.table")
		}
		r.toSchema, r.to = ref.Schema, ref.Name
	}
	ref, ok := tableRef(from)
	if !ok {
		return errors.New("the table is not name or schema.name")
	}
	r.table = ref
	if !f.column && r.toSchema == "" {
		r.toSchema = ref.Schema
	}
	*f.list = append(*f.list, r)
	return nil
}

// simpleName reports whether s is a name with no dot.
func simpleName(s string) bool { return s != "" && !strings.Contains(s, ".") }

// tableRef reads "name" or "schema.name".
func tableRef(s string) (migrate.TableRef, bool) {
	schema, name, qualified := strings.Cut(s, ".")
	if !qualified {
		return migrate.TableRef{Name: s}, simpleName(s)
	}
	return migrate.TableRef{Schema: schema, Name: name}, simpleName(schema) && simpleName(name)
}

// runMigrateDiff is velox-gen migrate diff: it plans the schema package
// against the directory's snapshot and writes the files Dir.Add makes.
func runMigrateDiff(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	c := newMigrateCmd("diff", stderr)
	var dialect dialectFlag
	c.fs.Var(&dialect, "dialect", "the dialect of a new directory: postgres, mysql or sqlite (default: velox.json's)")
	name := c.fs.String("name", "", "the files' `name` after their version (default: from the first change, as create_users)")
	var allow allowFlag
	c.fs.Var(&allow, "allow", "allow the destructive changes `code[=target],...`, as drop-column=users.name,drop-table")
	var renames []rename
	c.fs.Var(renameFlag{list: &renames}, "rename-table", "rename the table `old=new`, each optionally schema.name; repeat for several")
	c.fs.Var(renameFlag{column: true, list: &renames}, "rename-column", "rename the column `table.old=new`; repeat for several")
	var lockTimeout timeoutFlag
	c.fs.Var(&lockTimeout, "lock-timeout", "the lock timeout the files carry; 0 writes none, so velox.json's applies (default: velox.json's)")
	check := c.fs.Bool("check", false, "write nothing; exit 3 when the schema differs from schema.json or velox.sum is out of date")
	dryRun := c.fs.Bool("dry-run", false, "print the files instead of writing them")
	patterns, code, done := parseFlags(c.fs, args)
	if done {
		return code
	}
	if len(patterns) > 1 {
		return tooMany("migrate diff", c.fs, stderr)
	}

	// The directory, or a new one of -dialect.
	abs := c.abs(dir)
	var d *migrate.Dir
	state, err := dirState(abs)
	if err != nil {
		return c.fail(1, err)
	}
	if state != migrationDir {
		if dialect.d == 0 {
			how := "does not exist"
			if state == otherDir {
				how = "is not a migration directory yet"
			}
			_, _ = fmt.Fprintf(stderr, "velox-gen: migrate diff: %s %s; name its dialect with -dialect to start it\n", filepath.Clean(*c.dir), how)
			return 2
		}
		if d, err = migrate.NewDir(migrate.Config{Dialect: dialect.d}); err != nil {
			return c.fail(1, err)
		}
	} else {
		staleCode := 1
		if *check {
			staleCode = 3
		}
		var ok bool
		if d, code, ok = c.readDir(dir, staleCode); !ok {
			return code
		}
		if dialect.d != 0 && dialect.d != d.Config.Dialect {
			_, _ = fmt.Fprintf(stderr, "velox-gen: migrate diff: -dialect %s disagrees with %s, which says %s\n", dialect.d, c.path("velox.json"), d.Config.Dialect)
			return 2
		}
	}
	opts, err := renameOptions(d.Snapshot, renames)
	if err != nil {
		return c.fail(1, err)
	}

	// The schema package, as the directory's dialect sees it.
	pattern, err := schemaPackage(dir, patterns, env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", err)
		return 1
	}
	to, err := func() (*migrate.Schema, error) {
		m, err := gen.Load(dir, pattern, env)
		if err != nil {
			return nil, err
		}
		g, err := gen.Build(m)
		if err != nil {
			return nil, err
		}
		return gen.Tables(g, d.Config.Dialect)
	}()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", relErrors(dir, err))
		return 1
	}

	if *check {
		changes, err := migrate.Diff(d.Snapshot, to, opts)
		if err != nil {
			return c.fail(1, relMigrate(dir, err))
		}
		if len(changes) == 0 {
			return 0
		}
		var b strings.Builder
		fmt.Fprintf(&b, "velox-gen: migrate diff: %s is out of date:\n", c.path("schema.json"))
		for _, ch := range changes {
			fmt.Fprintf(&b, "  %s\n", ch)
		}
		b.WriteString("velox-gen: run velox-gen migrate diff\n")
		_, _ = io.WriteString(stderr, b.String())
		return 3
	}

	timeout := d.Config.LockTimeout
	if lockTimeout.set {
		timeout = lockTimeout.d
	}
	plan, err := migrate.NewPlan(d.Snapshot, to, migrate.PlanOptions{Diff: opts, Allow: allow, LockTimeout: timeout})
	if err != nil {
		return c.fail(1, relMigrate(dir, err))
	}
	if len(plan.Changes) == 0 {
		_, _ = fmt.Fprintln(stdout, "no changes")
		return 0
	}
	files, err := d.Add(*name, plan, to)
	if err != nil {
		return c.fail(1, err)
	}
	paths := slices.Sorted(maps.Keys(files))
	if *dryRun {
		for _, p := range paths {
			_, _ = fmt.Fprintf(stdout, "== %s\n%s", c.path(p), files[p])
		}
		return 0
	}
	remove, err := makeDirs(abs)
	if err != nil {
		return c.fail(1, err)
	}
	interrupted, err := writeAllHeld(abs, files, nil)
	if err != nil {
		return c.stopped(interrupted, c.fail(1, undoDirs(err, remove)))
	}
	for _, p := range paths {
		_, _ = fmt.Fprintln(stdout, c.path(p))
	}
	return c.stopped(interrupted, 0)
}

// renameOptions is Diff's options for the renames, each checked against the
// snapshot: a rename naming nothing in it is an error that says where it
// looked.
func renameOptions(snap *migrate.Schema, renames []rename) (migrate.DiffOptions, error) {
	var opts migrate.DiffOptions
	for _, r := range renames {
		t, ok := snap.Table(r.table)
		if !ok {
			return opts, fmt.Errorf("%s: schema.json has no table %s", r.raw, r.table)
		}
		if r.column == "" {
			if opts.RenameTables == nil {
				opts.RenameTables = map[migrate.TableRef]migrate.TableRef{}
			}
			opts.RenameTables[r.table] = migrate.TableRef{Schema: r.toSchema, Name: r.to}
			continue
		}
		if !slices.ContainsFunc(t.Columns, func(col migrate.Column) bool { return col.Name == r.column }) {
			return opts, fmt.Errorf("%s: table %s in schema.json has no column %s", r.raw, r.table, r.column)
		}
		if opts.RenameColumns == nil {
			opts.RenameColumns = map[migrate.ColumnRef]string{}
		}
		opts.RenameColumns[migrate.ColumnRef{Table: r.table, Column: r.column}] = r.to
	}
	return opts, nil
}

// relMigrate shows the schema positions of a planning error relative to
// dir: an *Error's Pos, and each finding's of a *BlockedError.
func relMigrate(dir string, err error) error {
	var be *migrate.BlockedError
	if errors.As(err, &be) {
		c := &migrate.BlockedError{Findings: slices.Clone(be.Findings)}
		for i := range c.Findings {
			if c.Findings[i].Pos != "" {
				c.Findings[i].Pos = relPos(dir, c.Findings[i].Pos)
			}
		}
		return c
	}
	var me *migrate.Error
	if errors.As(err, &me) && me.Pos != "" {
		e := *me
		e.Pos = relPos(dir, me.Pos)
		return &e
	}
	return err
}
