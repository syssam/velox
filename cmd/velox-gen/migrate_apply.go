package main

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	velox2 "github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// A dbCmd is a migrate subcommand that opens a database: -dir and -db.
type dbCmd struct {
	*migrateCmd
	db     *string // -db, as given
	secret secret  // what of the DSN no message shows
}

// newDBCmd is the subcommand name with its -dir and -db flags.
func newDBCmd(name string, stderr io.Writer) *dbCmd {
	c := &dbCmd{migrateCmd: newMigrateCmd(name, stderr)}
	c.db = c.fs.String("db", "", "the database's `dsn`, as its driver reads it (default: $VELOX_DB)")
	return c
}

// dsn is -db, else $VELOX_DB, and the one it is ("-db" or "$VELOX_DB");
// with neither it prints the usage error and reports false.
func (c *dbCmd) dsn(env []string) (dsn, src string, ok bool) {
	dsn, src = *c.db, "-db"
	if dsn == "" {
		dsn, src = lookupEnv(env, "VELOX_DB"), "$VELOX_DB"
	}
	if dsn == "" {
		_, _ = fmt.Fprintf(c.stderr, "velox-gen: migrate %s needs -db or $VELOX_DB\n", c.name)
		return "", "", false
	}
	return dsn, src, true
}

// open opens dsn, from src, with the driver of d. A DSN not of the driver's
// form is exit 2, before it is used. With existing, a SQLite file that is
// not there is exit 1, and the file is opened without creating it. It
// reports false, with the exit code, when it fails.
func (c *dbCmd) open(d velox2.Dialect, dsn, src string, existing bool) (*sql.DB, int, bool) {
	if err := dsnForm(d, dsn, src); err != nil {
		return nil, c.fail(2, err), false
	}
	c.secret = secrets(d, dsn).joined(func(s string) string { return dirFile.ReplaceAllStringFunc(s, c.path) })
	if d == velox2.SQLite && existing {
		if path := sqlitePath(dsn); path != "" {
			if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
				return nil, c.fail(1, fmt.Errorf("%s: no such database", path)), false
			}
			dsn = readWrite(dsn)
		}
	}
	db, err := openDB(d, dsn)
	if err != nil {
		return nil, c.fail(1, err), false
	}
	return db, 0, true
}

// fail prints err as "velox-gen: migrate <name>: <text>", without the
// library's "migrate: ", the DSN hidden and the directory's files joined to
// -dir, and returns code.
func (c *dbCmd) fail(code int, err error) int {
	_, _ = fmt.Fprintf(c.stderr, "velox-gen: migrate %s: %s\n", c.name, c.text(strings.TrimPrefix(err.Error(), "migrate: ")))
	return code
}

// text is s with the directory's files joined to -dir, then the DSN hidden.
func (c *dbCmd) text(s string) string {
	return c.secret.redact(dirFile.ReplaceAllStringFunc(s, c.path))
}

// dirFile is a file of the directory in a message of migrate's, which names
// them as the directory does: a migration file's name as migrate reads it
// (TestDirFileIsMigrates holds the two together), or one of its other files.
var dirFile = regexp.MustCompile(`\b(?:[0-9]{4,}_[a-z0-9_]+\.sql|velox\.json|velox\.sum|schema\.json)\b`)

// connect reads the directory and opens the database: the start of apply,
// status and drift; existing is open's. It reports false, with the exit
// code, when it fails.
func (c *dbCmd) connect(dir string, env []string, staleCode int, existing bool) (*migrate.Dir, *sql.DB, int, bool) {
	dsn, src, ok := c.dsn(env)
	if !ok {
		return nil, nil, 2, false
	}
	d, code, ok := c.readDir(dir, staleCode)
	if !ok {
		return nil, nil, code, false
	}
	db, code, ok := c.open(d.Config.Dialect, dsn, src, existing)
	if !ok {
		return nil, nil, code, false
	}
	return d, db, 0, true
}

// filePath is the path of a's file in d, or the path migrate gives a
// version without one.
func filePath(d *migrate.Dir, a migrate.Applied) string { return d.PathOf(a.Version, a.Name) }

// statements is "1 statement" or "<n> statements".
func statements(n int) string {
	if n == 1 {
		return "1 statement"
	}
	return strconv.Itoa(n) + " statements"
}

// versions is "version 5" or "versions 5, 6".
func versions(rows []migrate.Applied) string {
	vs := make([]string, len(rows))
	for i, r := range rows {
		vs[i] = strconv.FormatUint(r.Version, 10)
	}
	if len(vs) == 1 {
		return "version " + vs[0]
	}
	return "versions " + strings.Join(vs, ", ")
}

// runMigrateApply is velox-gen migrate apply: it applies the directory's
// pending files with migrate.Up and prints each it applied; with -dry-run
// it prints them instead. With -baseline it first records the files up to
// that version, as migrate.WithBaseline does, and prints each.
func runMigrateApply(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	c := newDBCmd("apply", stderr)
	to := c.fs.Uint64("to", 0, "apply the files up to `version` and none above it (default: every file)")
	resume := c.fs.Bool("resume", false, "continue the dirty version, from its first unit not done, before the pending files")
	lockWait := c.fs.Duration("lock-wait", 5*time.Minute, "how long to wait for another runner's migration lock; 0 tries it once")
	dryRun := c.fs.Bool("dry-run", false, "print the files to apply instead of applying them")
	var baseline uint64
	baselineSet := false
	c.fs.Func("baseline", "record the files up to `version` as applied without running them, on a database made before the directory, then apply the rest", func(v string) error {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return errors.New("not a version")
		}
		baseline, baselineSet = n, true
		return nil
	})
	rest, code, done := parseFlags(c.fs, args)
	if done {
		return code
	}
	if len(rest) > 0 {
		return c.noArgs()
	}
	if baselineSet {
		misuse := ""
		switch {
		case *resume:
			misuse = "-baseline and -resume cannot be combined"
		case *dryRun:
			misuse = "-baseline and -dry-run cannot be combined"
		case *to != 0 && *to < baseline:
			misuse = fmt.Sprintf("-to %d is below -baseline %d", *to, baseline)
		}
		if misuse != "" {
			_, _ = fmt.Fprintf(stderr, "velox-gen: migrate apply: %s\n", misuse)
			c.fs.Usage()
			return 2
		}
	}
	fsys := os.DirFS(c.abs(dir))
	show := func(files []migrate.File) int {
		for _, f := range files {
			if *to != 0 && f.Version > *to {
				break
			}
			data, err := fs.ReadFile(fsys, f.Path)
			if err != nil {
				return c.fail(1, err)
			}
			_, _ = fmt.Fprintf(stdout, "== %s\n%s", c.path(f.Path), data)
		}
		return 0
	}
	if *dryRun {
		// A SQLite file not made yet is an empty database: every file is
		// pending, and nothing is opened.
		dsn, src, ok := c.dsn(env)
		if !ok {
			return 2
		}
		d, code, ok := c.readDir(dir, 1)
		if !ok {
			return code
		}
		if d.Config.Dialect == velox2.SQLite && dsnForm(d.Config.Dialect, dsn, src) == nil {
			if _, err := os.Stat(sqlitePath(dsn)); errors.Is(err, fs.ErrNotExist) {
				return show(d.Files)
			}
		}
	}
	d, db, code, ok := c.connect(dir, env, 1, *dryRun) // a dry run makes no database
	if !ok {
		return code
	}
	defer func() { _ = db.Close() }()
	ctx, stop := signalContext()
	defer stop()
	ahead := func(rows []migrate.Applied) int {
		_, _ = fmt.Fprintf(stdout, "the database is ahead of the directory (%s); nothing applied\n", versions(rows))
		return 0
	}

	if *dryRun {
		st, err := migrate.Status(ctx, db, fsys, migrate.WithLockWait(*lockWait))
		if err != nil {
			return c.fail(1, err)
		}
		if len(st.Ahead) > 0 {
			return ahead(st.Ahead)
		}
		return show(st.Pending)
	}

	opts := []migrate.Option{migrate.WithTarget(*to), migrate.WithLockWait(*lockWait)}
	if *resume {
		opts = append(opts, migrate.WithResume())
	}
	if baselineSet {
		opts = append(opts, migrate.WithBaseline(baseline))
	}
	r, err := migrate.Up(ctx, db, fsys, opts...)
	if r != nil {
		for _, v := range r.Baselined {
			_, _ = fmt.Fprintf(stdout, "baselined %s\n", c.path(d.PathOf(v, "")))
		}
		for _, a := range r.Applied {
			_, _ = fmt.Fprintf(stdout, "applied %s in %s\n", c.path(filePath(d, a)), a.Duration.Round(time.Microsecond))
		}
	}
	if err != nil {
		return c.fail(1, err)
	}
	if r.Ahead {
		st, err := migrate.Status(ctx, db, fsys) // the versions, which Up does not report
		if err != nil {
			return c.fail(1, err)
		}
		return ahead(st.Ahead)
	}
	return 0
}

// appliedJSON is a version of the history in status -json.
type appliedJSON struct {
	Version    uint64    `json:"version"`
	Name       string    `json:"name"`
	Sum        string    `json:"sum"`
	AppliedAt  time.Time `json:"applied_at"`
	DurationMS int64     `json:"duration_ms"`
	Dirty      bool      `json:"dirty"`
	Statements int       `json:"statements"`
	Error      string    `json:"error"`
}

func appliedOf(a migrate.Applied) appliedJSON {
	return appliedJSON{a.Version, a.Name, a.Sum, a.AppliedAt.UTC(), a.Duration.Milliseconds(), a.Dirty, a.Statements, a.Error}
}

func appliedList(rows []migrate.Applied) []appliedJSON {
	out := make([]appliedJSON, len(rows))
	for i, a := range rows {
		out[i] = appliedOf(a)
	}
	return out
}

// statusJSON is status -json: a file's path in it is the directory's own.
type statusJSON struct {
	Applied []appliedJSON `json:"applied"`
	Pending []pendingJSON `json:"pending"`
	Ahead   []appliedJSON `json:"ahead"`
	Dirty   *appliedJSON  `json:"dirty"`
}

type pendingJSON struct {
	Version uint64 `json:"version"`
	Path    string `json:"path"`
}

// printJSON prints v indented.
func printJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// runMigrateStatus is velox-gen migrate status: it prints the directory
// read against the database's history, by version. It exits 3 with pending
// files and 1 with a dirty version.
func runMigrateStatus(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	c := newDBCmd("status", stderr)
	asJSON := c.fs.Bool("json", false, "print the state as JSON")
	rest, code, done := parseFlags(c.fs, args)
	if done {
		return code
	}
	if len(rest) > 0 {
		return c.noArgs()
	}
	d, db, code, ok := c.connect(dir, env, 1, true)
	if !ok {
		return code
	}
	defer func() { _ = db.Close() }()
	ctx, stop := signalContext()
	defer stop()
	st, err := migrate.Status(ctx, db, os.DirFS(c.abs(dir)))
	var dirty *migrate.DirtyError
	if err != nil && !errors.As(err, &dirty) {
		return c.fail(1, err)
	}

	if *asJSON {
		j := statusJSON{Applied: appliedList(st.Applied), Pending: []pendingJSON{}, Ahead: appliedList(st.Ahead)}
		for _, f := range st.Pending {
			j.Pending = append(j.Pending, pendingJSON{f.Version, f.Path})
		}
		if st.Dirty != nil {
			a := appliedOf(*st.Dirty)
			a.Error = c.secret.redact(a.Error)
			j.Dirty = &a
		}
		if err := printJSON(stdout, j); err != nil {
			return c.fail(1, err)
		}
	} else {
		type line struct {
			version uint64
			text    string
		}
		var lines []line
		for _, a := range st.Applied {
			lines = append(lines, line{a.Version, fmt.Sprintf("applied %d %s %s", a.Version, c.path(filePath(d, a)), a.AppliedAt.UTC().Format(time.RFC3339))})
		}
		for _, f := range st.Pending {
			lines = append(lines, line{f.Version, fmt.Sprintf("pending %d %s", f.Version, c.path(f.Path))})
		}
		for _, a := range st.Ahead {
			lines = append(lines, line{a.Version, fmt.Sprintf("ahead %d %s", a.Version, a.Name)})
		}
		if a := st.Dirty; a != nil {
			s := fmt.Sprintf("dirty %d %s: %s done", a.Version, c.path(dirty.Path), statements(a.Statements))
			if a.Error != "" {
				s += ": " + c.text(a.Error)
			}
			lines = append(lines, line{a.Version, s})
		}
		slices.SortStableFunc(lines, func(a, b line) int { return cmp.Compare(a.version, b.version) })
		for _, l := range lines {
			_, _ = fmt.Fprintln(stdout, l.text)
		}
	}
	switch {
	case dirty != nil:
		return c.fail(1, dirty)
	case len(st.Pending) > 0:
		return 3
	}
	return 0
}

// changeJSON and warningJSON are drift -json's.
type changeJSON struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Text   string `json:"text"`
}

type warningJSON struct {
	Object  string `json:"object"`
	Message string `json:"message"`
}

type driftJSON struct {
	Changes  []changeJSON  `json:"changes"`
	Warnings []warningJSON `json:"warnings"`
}

// runMigrateDrift is velox-gen migrate drift: on a database at the
// directory's last version, it prints what tells the database's tables from
// schema.json, each change as "change: <change>" and each warning as
// "warning: <warning>". It exits 3 with a change.
func runMigrateDrift(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	c := newDBCmd("drift", stderr)
	asJSON := c.fs.Bool("json", false, "print the changes and warnings as JSON")
	rest, code, done := parseFlags(c.fs, args)
	if done {
		return code
	}
	if len(rest) > 0 {
		return c.noArgs()
	}
	d, db, code, ok := c.connect(dir, env, 1, true)
	if !ok {
		return code
	}
	defer func() { _ = db.Close() }()
	ctx, stop := signalContext()
	defer stop()
	st, err := migrate.Status(ctx, db, os.DirFS(c.abs(dir)))
	behind := "the database is not at the directory's last version: "
	var dirty *migrate.DirtyError
	switch {
	case errors.As(err, &dirty):
		return c.fail(1, errors.New(behind+dirty.Path+" is dirty"))
	case err != nil:
		return c.fail(1, err)
	case len(st.Pending) > 0:
		paths := make([]string, len(st.Pending))
		for i, f := range st.Pending {
			paths[i] = f.Path
		}
		verb := " is pending"
		if len(paths) > 1 {
			verb = " are pending"
		}
		return c.fail(1, errors.New(behind+strings.Join(paths, ", ")+verb))
	case len(st.Ahead) > 0:
		verb := " is applied"
		if len(st.Ahead) > 1 {
			verb = " are applied"
		}
		return c.fail(1, errors.New(behind+versions(st.Ahead)+verb+" above the directory's last file"))
	}
	r, err := migrate.Drift(ctx, db, d.Snapshot)
	if err != nil {
		return c.fail(1, err)
	}
	if *asJSON {
		j := driftJSON{Changes: []changeJSON{}, Warnings: []warningJSON{}}
		for _, ch := range r.Changes {
			j.Changes = append(j.Changes, changeJSON{string(ch.Kind()), ch.Target().String(), fmt.Sprint(ch)})
		}
		for _, w := range r.Warnings {
			j.Warnings = append(j.Warnings, warningJSON{w.Object, w.Message})
		}
		if err := printJSON(stdout, j); err != nil {
			return c.fail(1, err)
		}
	} else {
		for _, ch := range r.Changes {
			_, _ = fmt.Fprintf(stdout, "change: %s\n", ch)
		}
		for _, w := range r.Warnings {
			_, _ = fmt.Fprintf(stdout, "warning: %s\n", w)
		}
	}
	if len(r.Changes) > 0 {
		return 3
	}
	return 0
}

// runMigrateResolve is velox-gen migrate resolve: it ends the dirty version
// -version with migrate.Resolve, as applied (-applied) or undone (-undone).
func runMigrateResolve(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	c := newDBCmd("resolve", stderr)
	version := c.fs.Uint64("version", 0, "the dirty `version`")
	applied := c.fs.Bool("applied", false, "the version was finished by hand: mark it applied")
	undone := c.fs.Bool("undone", false, "the version's applied statements were reverted by hand: delete its row")
	rest, code, done := parseFlags(c.fs, args)
	if done {
		return code
	}
	if len(rest) > 0 {
		return c.noArgs()
	}
	if *version == 0 || *applied == *undone {
		_, _ = fmt.Fprintf(stderr, "velox-gen: migrate resolve needs -version and one of -applied and -undone\n")
		c.fs.Usage()
		return 2
	}
	dsn, src, ok := c.dsn(env)
	if !ok {
		return 2
	}
	if err := c.exists(dir); err != nil {
		return c.fail(1, err)
	}
	// The dialect, to pick the driver; Resolve reads velox.json in full.
	raw, err := os.ReadFile(filepath.Join(c.abs(dir), "velox.json"))
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
	db, code, ok := c.open(dialect.d, dsn, src, true)
	if !ok {
		return code
	}
	defer func() { _ = db.Close() }()
	ctx, stop := signalContext()
	defer stop()
	r, how := migrate.ResolveApplied, "applied"
	if *undone {
		r, how = migrate.ResolveUndone, "undone"
	}
	if err := migrate.Resolve(ctx, db, os.DirFS(c.abs(dir)), *version, r); err != nil {
		return c.fail(1, err)
	}
	_, _ = fmt.Fprintf(stdout, "resolved version %d as %s\n", *version, how)
	return 0
}
