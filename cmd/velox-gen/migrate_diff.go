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
	if err := writeAll(abs, files); err != nil {
		remove()
		return c.fail(1, err)
	}
	for _, p := range paths {
		_, _ = fmt.Fprintln(stdout, c.path(p))
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

// writeTemp and renameFile are how diff writes its files; tests replace them.
var (
	writeTemp  = writeTempFile
	renameFile = os.Rename
	linkFile   = os.Link
)

// writeTempFile writes data to a new file in dir named by pattern, as
// os.CreateTemp names it, with mode 0o644, and returns its path.
func writeTempFile(dir, pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// writeAll writes files into dir, all or nothing: each under a temporary
// name first, then renamed into place, velox.sum last; a file it replaces is
// kept aside first, by a hard link (else a rename), and removed once every
// file is in place. On a
// failure it removes what it wrote and renames the replaced files back; the
// error names what it could not undo.
func writeAll(dir string, files map[string][]byte) error {
	paths := slices.Sorted(maps.Keys(files))
	if i := slices.Index(paths, "velox.sum"); i >= 0 {
		paths = append(append(paths[:i:i], paths[i+1:]...), "velox.sum")
	}
	temps := map[string]string{}
	type swapped struct{ target, aside string } // aside "": the target was new
	var done []swapped
	undo := func(err error) error {
		var left []string
		for _, t := range temps {
			if rerr := os.Remove(t); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
				left = append(left, t)
			}
		}
		for i := len(done) - 1; i >= 0; i-- {
			s := done[i]
			if s.aside == "" {
				if rerr := os.Remove(s.target); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
					left = append(left, s.target)
				}
			} else if rerr := renameFile(s.aside, s.target); rerr != nil {
				left = append(left, s.target+" (what it held is in "+s.aside+")")
			}
		}
		if len(left) > 0 {
			slices.Sort(left)
			return fmt.Errorf("%w; undoing the write failed, which left %s", err, strings.Join(left, ", "))
		}
		return err
	}
	for _, p := range paths {
		t, err := writeTemp(dir, "."+p+".tmp-*", files[p])
		if err != nil {
			return undo(err)
		}
		temps[p] = t
	}
	for _, p := range paths {
		s := swapped{target: filepath.Join(dir, p)}
		linked := false
		if fi, err := os.Lstat(s.target); err == nil {
			aside, err := writeTemp(dir, "."+p+".old-*", nil) // a name of its own
			if err != nil {
				return undo(err)
			}
			// A hard link keeps a regular file at its path until the rename
			// replaces it; a symbolic link, or where links fail, is renamed
			// aside.
			if err := os.Remove(aside); err != nil {
				return undo(err)
			}
			linked = fi.Mode().IsRegular() && linkFile(s.target, aside) == nil
			if !linked {
				if err := renameFile(s.target, aside); err != nil {
					_ = os.Remove(aside)
					return undo(err)
				}
			}
			s.aside = aside
		} else if !errors.Is(err, fs.ErrNotExist) {
			return undo(err)
		}
		if err := renameFile(temps[p], s.target); err != nil {
			if linked {
				_ = os.Remove(s.aside) // the original is at its path still
			} else {
				done = append(done, s)
			}
			return undo(err)
		}
		done = append(done, s)
		delete(temps, p)
	}
	var left []string
	for _, s := range done {
		if s.aside != "" {
			if err := os.Remove(s.aside); err != nil && !errors.Is(err, fs.ErrNotExist) {
				left = append(left, s.aside)
			}
		}
	}
	if len(left) > 0 {
		return fmt.Errorf("the files are written, but removing the old ones failed, which left %s", strings.Join(left, ", "))
	}
	return nil
}

// makeDirs makes dir and the parents it lacks, and returns what removes
// those it made, when they are empty.
func makeDirs(dir string) (remove func(), err error) {
	var made []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil || !errors.Is(err, fs.ErrNotExist) {
			break
		}
		made = append(made, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	remove = func() {
		for _, d := range made { // deepest first
			_ = os.Remove(d)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		remove()
		return nil, err
	}
	return remove, nil
}
