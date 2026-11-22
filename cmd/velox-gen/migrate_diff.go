package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen"
	"github.com/syssam/velox/internal/genrun"
	"github.com/syssam/velox/migrate"
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

// versionsFree refuses a directory behind the database at dsn: a version in its
// history above the directory's last file is one the next file would reuse, as two
// branches do. It reports false, with the exit code, when it refuses.
func (c *migrateCmd) versionsFree(d *migrate.Dir, dir, dsn string) (int, bool) {
	bc := &dbCmd{migrateCmd: c, db: &dsn}
	db, code, ok := bc.open(d.Config.Dialect, dsn, "-db", true)
	if !ok {
		return code, false
	}
	defer func() { _ = db.Close() }()
	ctx, stop := signalContext()
	defer stop()
	rows, err := migrate.History(ctx, db, os.DirFS(c.abs(dir)))
	if err != nil {
		return bc.fail(1, err), false
	}
	var last uint64
	if n := len(d.Files); n > 0 {
		last = d.Files[n-1].Version
	}
	for _, r := range rows {
		if r.Version > last {
			return bc.fail(1, fmt.Errorf("version %d (%s) is in the database's history and not in the directory, and the next file would reuse its number; merge the branch that has it, or, if its file is gone for good, run migrate resolve -version %d -forget", r.Version, r.Name, r.Version)), false
		}
	}
	return 0, true
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
	seen := c.fs.String("db", "", "a database whose history to look at first: refuse if it holds a version above the directory's last, which a new file would reuse")
	scratch := c.fs.String("scratch-db", "", "with -check: an empty database's `dsn`, which the files are applied to, to compare with schema.json (default on SQLite: a temporary file)")
	replay := c.fs.Bool("replay", true, "with -check: apply the files to a scratch database and compare the result with schema.json; -replay=false compares the schema package with schema.json alone")
	patterns, code, done := parseFlags(c.fs, args)
	if done {
		return code
	}
	if len(patterns) > 1 {
		return tooMany("migrate diff", c.fs, stderr)
	}
	for _, f := range []string{"scratch-db", "replay"} {
		if given(c.fs, f) && !*check {
			_, _ = fmt.Fprintf(stderr, "velox-gen: migrate diff: -%s goes with -check\n", f)
			c.fs.Usage()
			return 2
		}
	}
	if *scratch != "" && !*replay {
		_, _ = fmt.Fprintf(stderr, "velox-gen: migrate diff: -scratch-db and -replay=false cannot be combined\n")
		c.fs.Usage()
		return 2
	}

	// The directory, or a new one of -dialect.
	abs := c.abs(dir)
	d, state, code, ok := c.diffDir(dir, abs, dialect.d, *check)
	if !ok {
		return code
	}
	opts, err := renameOptions(d.Snapshot, renames)
	if err != nil {
		return c.fail(1, err)
	}

	if *seen != "" && !*check && state == migrationDir {
		if code, ok := c.versionsFree(d, dir, *seen); !ok {
			return code
		}
	}

	// The schema package, as the directory's dialect sees it.
	pattern, err := genrun.SchemaPackage(dir, patterns, env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", err)
		return 1
	}
	to, err := schemaTables(dir, pattern, env, d.Config.Dialect)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %v\n", genrun.RelErrors(dir, err))
		return 1
	}

	if *check {
		if code := c.checkDiff(dir, d, to, opts); code != 0 || !*replay || state != migrationDir || len(d.Files) == 0 {
			return code
		}
		return c.checkReplay(dir, d, *scratch)
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
	interrupted, err := writeDirHeld(abs, files)
	if err != nil {
		return c.stopped(interrupted, c.fail(1, err))
	}
	for _, p := range paths {
		_, _ = fmt.Fprintln(stdout, c.path(p))
	}
	return c.stopped(interrupted, 0)
}

// diffDir returns the migration directory at abs, or a new one of
// dialect, and dirState's state of abs. On a failure it reports the exit
// code and false; a stale directory is staleCode 3 with check.
func (c *migrateCmd) diffDir(dir, abs string, dialect velox.Dialect, check bool) (*migrate.Dir, int, int, bool) {
	state, err := dirState(abs)
	if err != nil {
		return nil, 0, c.fail(1, err), false
	}
	if state != migrationDir {
		if dialect == 0 {
			how := "does not exist"
			if state == otherDir {
				how = "is not a migration directory yet"
			}
			_, _ = fmt.Fprintf(c.stderr, "velox-gen: migrate diff: %s %s; name its dialect with -dialect to start it\n", filepath.Clean(*c.dir), how)
			return nil, 0, 2, false
		}
		d, err := migrate.NewDir(migrate.Config{Dialect: dialect})
		if err != nil {
			return nil, 0, c.fail(1, err), false
		}
		return d, state, 0, true
	}
	staleCode := 1
	if check {
		staleCode = 3
	}
	d, code, ok := c.readDir(dir, staleCode)
	if !ok {
		return nil, 0, code, false
	}
	if dialect != 0 && dialect != d.Config.Dialect {
		_, _ = fmt.Fprintf(c.stderr, "velox-gen: migrate diff: -dialect %s disagrees with %s, which says %s\n", dialect, c.path("velox.json"), d.Config.Dialect)
		return nil, 0, 2, false
	}
	return d, state, 0, true
}

// schemaTables loads and builds the schema package at pattern and returns
// its tables, as dialect sees them.
func schemaTables(dir, pattern string, env []string, dialect velox.Dialect) (*migrate.Schema, error) {
	m, err := codegen.Load(dir, pattern, env)
	if err != nil {
		return nil, err
	}
	g, err := codegen.Build(m)
	if err != nil {
		return nil, err
	}
	return codegen.Tables(g, dialect)
}

// checkDiff is diff -check: it names each change from d's snapshot to to,
// and returns 3 when there is any, else 0.
func (c *migrateCmd) checkDiff(dir string, d *migrate.Dir, to *migrate.Schema, opts migrate.DiffOptions) int {
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
	_, _ = io.WriteString(c.stderr, b.String())
	return 3
}

// checkReplay is the rest of diff -check: it applies the directory's files to
// an empty database, as migrate.Verify does, and fails unless the result is
// schema.json; see replayed.
func (c *migrateCmd) checkReplay(dir string, d *migrate.Dir, dsn string) int {
	return c.replayed(os.DirFS(c.abs(dir)), d.Config.Dialect, dsn, "-check", "the files do not make schema.json")
}

// replayed applies the files of the directory fsys to an empty database with
// migrate.Verify and returns 0 when they make its schema.json, else 1 after
// it says why, after fails. The database is dsn, which must be empty and is
// left with the files applied, or with no dsn on SQLite a temporary file,
// removed after. A PostgreSQL or MySQL directory with no dsn is exit 2, which
// who, the flag or command that replays, starts: SQLite cannot run its files,
// and a check that ran nothing would pass.
func (c *migrateCmd) replayed(fsys fs.FS, dialect velox.Dialect, dsn, who, fails string) int {
	bc := &dbCmd{migrateCmd: c, db: &dsn}
	where := "-scratch-db"
	if dsn == "" {
		if dialect != velox.SQLite {
			_, _ = fmt.Fprintf(c.stderr, "velox-gen: migrate %s: %s applies the files to an empty database and compares it with schema.json, and the files of this %s directory need a %s one: pass -scratch-db <dsn>, or -replay=false to compare the schema package with schema.json alone\n", c.name, who, dialect, dialect)
			return 2
		}
		tmp, err := os.MkdirTemp("", "velox-check-")
		if err != nil {
			return c.fail(1, err)
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		dsn, where = filepath.Join(tmp, "check.db"), "a temporary SQLite file"
	}
	db, code, ok := bc.open(dialect, dsn, "-scratch-db", false)
	if !ok {
		return code
	}
	defer func() { _ = db.Close() }()
	ctx, stop := signalContext()
	defer stop()
	if err := migrate.Verify(ctx, db, fsys); err != nil {
		if ctx.Err() != nil {
			return bc.fail(1, ctx.Err())
		}
		return bc.fail(1, fmt.Errorf("%s: applied to %s, %s", fails, where, strings.TrimPrefix(err.Error(), "migrate: ")))
	}
	return 0
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
	if be, ok := errors.AsType[*migrate.BlockedError](err); ok {
		c := *be // its rename hints with it
		c.Findings = slices.Clone(be.Findings)
		for i := range c.Findings {
			if c.Findings[i].Pos != "" {
				c.Findings[i].Pos = genrun.RelPos(dir, c.Findings[i].Pos)
			}
		}
		return &c
	}
	if me, ok := errors.AsType[*migrate.Error](err); ok && me.Pos != "" {
		e := *me
		e.Pos = genrun.RelPos(dir, me.Pos)
		return &e
	}
	return err
}
