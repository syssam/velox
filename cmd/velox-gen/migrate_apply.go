package main

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
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

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// A dbCmd is a migrate subcommand that opens a database: -dir and -db.
type dbCmd struct {
	*migrateCmd
	db     *string // -db, as given
	secret secret  // what of the DSN no message shows
	whose  string  // whose dialect the DSN is of, for messages: velox.json's unless set
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
func (c *dbCmd) open(d velox.Dialect, dsn, src string, existing bool) (*sql.DB, int, bool) {
	if err := dsnForm(d, dsn, src, c.dialectOf()); err != nil {
		return nil, c.fail(2, err), false
	}
	c.secret = secrets(d, dsn).joined(func(s string) string { return dirFile.ReplaceAllStringFunc(s, c.path) })
	if d == velox.SQLite && existing {
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

// dialectOf is whose dialect the DSN is of, as messages name it.
func (c *dbCmd) dialectOf() string { return cmp.Or(c.whose, "velox.json's dialect") }

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

// given reports whether the command line set the flag name: 0 is a version,
// so a version flag's value cannot say.
func given(flags *flag.FlagSet, name string) bool {
	set := false
	flags.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
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
// it checks them as Up would, with migrate.WithDryRun, and prints them
// instead. With -baseline it first records the files up to
// that version, as migrate.WithBaseline does, and prints each.
func runMigrateApply(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	c := newDBCmd("apply", stderr)
	to := c.fs.Uint64("to", 0, "apply the files up to `version` and none above it (default: every file)")
	resume := c.fs.Bool("resume", false, "continue the dirty version, from its first unit not done, before the pending files")
	lockWait := c.fs.Duration("lock-wait", 5*time.Minute, "how long to wait for another runner's migration lock; 0 tries it once")
	dryRun := c.fs.Bool("dry-run", false, "check the files to apply as apply would, changing nothing, and print them")
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
	a := &applyRun{
		c: c, stdout: stdout,
		to: *to, resume: *resume, lockWait: *lockWait, dryRun: *dryRun,
		baseline: baseline, baselineSet: baselineSet,
		toSet: given(c.fs, "to"), // -to 0 is a version: 0000 is a valid file
	}
	if misuse := a.misuse(); misuse != "" {
		_, _ = fmt.Fprintf(stderr, "velox-gen: migrate apply: %s\n", misuse)
		c.fs.Usage()
		return 2
	}
	a.fsys = os.DirFS(c.abs(dir))
	if a.dryRun {
		if code, done := a.unmadeDryRun(dir, env); done {
			return code
		}
	}
	// A dry run makes no database, and a baseline adopts one already there.
	d, db, code, ok := c.connect(dir, env, 1, a.dryRun || a.baselineSet)
	if !ok {
		return code
	}
	defer func() { _ = db.Close() }()
	ctx, stop := signalContext()
	defer stop()

	if a.dryRun {
		r, err := migrate.Up(ctx, db, a.fsys, append(a.options(stderr), migrate.WithDryRun())...)
		if err != nil {
			return c.fail(1, err)
		}
		if r.Ahead {
			st, err := migrate.Status(ctx, db, a.fsys)
			if err != nil {
				return c.fail(1, err)
			}
			return a.ahead(st.Ahead)
		}
		return a.show(applying(d, r.Applied))
	}

	r, err := migrate.Up(ctx, db, a.fsys, a.options(stderr)...)
	if r != nil {
		a.report(d, r)
	}
	if err != nil {
		return c.fail(1, err)
	}
	if r.Ahead {
		st, err := migrate.Status(ctx, db, a.fsys) // the versions, which Up does not report
		if err != nil {
			return c.fail(1, err)
		}
		return a.ahead(st.Ahead)
	}
	return 0
}

// applyRun is one run of migrate apply, with its flags.
type applyRun struct {
	c           *dbCmd
	stdout      io.Writer
	fsys        fs.FS
	to          uint64
	toSet       bool
	resume      bool
	lockWait    time.Duration
	dryRun      bool
	baseline    uint64
	baselineSet bool
}

// misuse returns why the flags cannot be combined with -baseline, or "".
func (a *applyRun) misuse() string {
	if !a.baselineSet {
		return ""
	}
	switch {
	case a.resume:
		return "-baseline and -resume cannot be combined"
	case a.dryRun:
		return "-baseline and -dry-run cannot be combined"
	case a.toSet && a.to < a.baseline:
		return fmt.Sprintf("-to %d is below -baseline %d", a.to, a.baseline)
	}
	return ""
}

// show prints each of files up to -to, and returns the exit code.
func (a *applyRun) show(files []migrate.File) int {
	for _, f := range files {
		if a.toSet && f.Version > a.to {
			break
		}
		data, err := fs.ReadFile(a.fsys, f.Path)
		if err != nil {
			return a.c.fail(1, err)
		}
		_, _ = fmt.Fprintf(a.stdout, "== %s\n%s", a.c.path(f.Path), data)
	}
	return 0
}

// applying is the files of d that rows, a dry run's report, would apply.
func applying(d *migrate.Dir, rows []migrate.Applied) []migrate.File {
	var out []migrate.File
	for _, r := range rows {
		if i := slices.IndexFunc(d.Files, func(f migrate.File) bool { return f.Version == r.Version }); i >= 0 {
			out = append(out, d.Files[i])
		}
	}
	return out
}

// unmadeDryRun handles a dry run on a SQLite file not made yet, and a
// missing DSN or unreadable directory. It reports the exit code, and
// whether the run is done.
func (a *applyRun) unmadeDryRun(dir string, env []string) (int, bool) {
	c := a.c
	// A SQLite file not made yet is an empty database: every file is
	// pending. They are applied to a temporary file, as Up would apply
	// them, and the file named is not made.
	dsn, src, ok := c.dsn(env)
	if !ok {
		return 2, true
	}
	d, code, ok := c.readDir(dir, 1)
	if !ok {
		return code, true
	}
	if d.Config.Dialect == velox.SQLite && dsnForm(d.Config.Dialect, dsn, src, c.dialectOf()) == nil {
		if _, err := os.Stat(sqlitePath(dsn)); errors.Is(err, fs.ErrNotExist) {
			return a.emptyDryRun(d), true
		}
	}
	return 0, false
}

// emptyDryRun is a dry run on a SQLite database not made yet: Up on an
// empty temporary file, removed after, then the files it applied.
func (a *applyRun) emptyDryRun(d *migrate.Dir) int {
	tmp, err := os.MkdirTemp("", "velox-dry-run-")
	if err != nil {
		return a.c.fail(1, err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	db, err := openDB(velox.SQLite, filepath.Join(tmp, "dry-run.db"))
	if err != nil {
		return a.c.fail(1, err)
	}
	defer func() { _ = db.Close() }()
	ctx, stop := signalContext()
	defer stop()
	r, err := migrate.Up(ctx, db, a.fsys, a.options(a.c.stderr)...)
	if err != nil {
		return a.c.fail(1, err)
	}
	return a.show(applying(d, r.Applied))
}

// ahead prints that the database is ahead of the directory, by rows, and
// returns the exit code.
func (a *applyRun) ahead(rows []migrate.Applied) int {
	_, _ = fmt.Fprintf(a.stdout, "the database is ahead of the directory (%s); nothing applied\n", versions(rows))
	return 0
}

// options returns the options of migrate.Up for the flags; its notes go to
// stderr.
func (a *applyRun) options(stderr io.Writer) []migrate.Option {
	opts := []migrate.Option{migrate.WithLockWait(a.lockWait)}
	if a.toSet {
		opts = append(opts, migrate.WithTarget(a.to))
	}
	if a.resume {
		opts = append(opts, migrate.WithResume())
	}
	if a.baselineSet {
		opts = append(opts, migrate.WithBaseline(a.baseline))
	}
	return append(opts, migrate.WithNotes(func(n string) { _, _ = fmt.Fprintf(stderr, "velox-gen: note: %s\n", a.c.text(n)) }))
}

// report prints each file of d that r baselined, then each it applied.
func (a *applyRun) report(d *migrate.Dir, r *migrate.Report) {
	for _, v := range r.Baselined {
		_, _ = fmt.Fprintf(a.stdout, "baselined %s\n", a.c.path(d.PathOf(v, "")))
	}
	for _, ap := range r.Applied {
		_, _ = fmt.Fprintf(a.stdout, "applied %s in %s\n", a.c.path(filePath(d, ap)), ap.Duration.Round(time.Microsecond))
	}
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
	dirty, isDirty := errors.AsType[*migrate.DirtyError](err)
	if err != nil && !isDirty {
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
			s := fmt.Sprintf("applied %d %s %s", a.Version, c.path(filePath(d, a)), a.AppliedAt.UTC().Format(time.RFC3339))
			if a.Error != "" {
				s += ": " + c.text(a.Error) // what a forced resolve recorded
			}
			lines = append(lines, line{a.Version, s})
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
	scratch := c.fs.String("scratch-db", "", "an empty database's `dsn`, which the files up to the last applied version are applied to, to compare with when files are pending (default on SQLite: a temporary file)")
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
	switch dirty, isDirty := errors.AsType[*migrate.DirtyError](err); {
	case isDirty:
		return c.fail(1, errors.New(behind+dirty.Path+" is dirty"))
	case err != nil:
		return c.fail(1, err)
	case len(st.Ahead) > 0:
		verb := " is applied"
		if len(st.Ahead) > 1 {
			verb = " are applied"
		}
		return c.fail(1, errors.New(behind+versions(st.Ahead)+verb+" above the directory's last file"))
	}
	want, code, ok := c.driftWant(ctx, dir, d, st, *scratch)
	if !ok {
		return code
	}
	r, err := migrate.Drift(ctx, db, want)
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

// driftWant is what drift compares the database with: schema.json, at the
// directory's last version; else, with files pending, what the files up to
// the last applied version make on an empty database: dsn, or with no dsn on
// SQLite a temporary file, removed after. A PostgreSQL or MySQL directory
// with files pending and no dsn is refused, naming them. It reports the exit
// code and false when it fails.
func (c *dbCmd) driftWant(ctx context.Context, dir string, d *migrate.Dir, st *migrate.State, dsn string) (*migrate.Schema, int, bool) {
	if len(st.Pending) == 0 {
		return d.Snapshot, 0, true
	}
	paths := make([]string, len(st.Pending))
	for i, f := range st.Pending {
		paths[i] = f.Path
	}
	pending := strings.Join(paths, ", ") + " is pending"
	if len(paths) > 1 {
		pending = strings.Join(paths, ", ") + " are pending"
	}
	if len(st.Applied) == 0 {
		_, _ = fmt.Fprintf(c.stderr, "velox-gen: note: %s, and no version is applied: nothing to compare\n", c.text(pending))
		return &migrate.Schema{Dialect: d.Config.Dialect}, 0, true
	}
	m := st.Applied[len(st.Applied)-1].Version
	j := slices.IndexFunc(d.Files, func(f migrate.File) bool { return f.Version == m })
	if j < 0 {
		return nil, c.fail(1, fmt.Errorf("the database is not at the directory's last version: %s, and the last applied version, %d, has no file in the directory to replay", pending, m)), false
	}
	if dsn == "" && d.Config.Dialect != velox.SQLite {
		return nil, c.fail(1, fmt.Errorf("the database is not at the directory's last version: %s; to compare it with what the files up to %s make, give an empty %s database with -scratch-db", pending, d.Files[j].Path, d.Config.Dialect)), false
	}
	if dsn == "" {
		tmp, err := os.MkdirTemp("", "velox-drift-")
		if err != nil {
			return nil, c.fail(1, err), false
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		dsn = filepath.Join(tmp, "scratch.db")
	}
	hidden := c.secret
	sdb, code, ok := c.open(d.Config.Dialect, dsn, "-scratch-db", false)
	c.secret = hidden.and(c.secret)
	if !ok {
		return nil, code, false
	}
	defer func() { _ = sdb.Close() }()
	want, err := migrate.SchemaAt(ctx, sdb, os.DirFS(c.abs(dir)), m)
	if err != nil {
		return nil, c.fail(1, err), false
	}
	_, _ = fmt.Fprintf(c.stderr, "velox-gen: note: %s; compared with what the files up to %s make\n", c.text(pending), c.text(d.Files[j].Path))
	return want, 0, true
}

// runMigrateResolve is velox-gen migrate resolve: it ends the dirty version
// -version with migrate.Resolve, as applied (-applied) or undone (-undone).
// -applied and -adopt compare the database with the directory first, on
// -scratch-db when files above the version are pending, and record a
// difference only with -force.
func runMigrateResolve(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	c := newDBCmd("resolve", stderr)
	version := c.fs.Uint64("version", 0, "the `version` to resolve")
	c.fs.Bool("applied", false, "the version was finished by hand: mark it applied")
	undone := c.fs.Bool("undone", false, "the version's applied statements were reverted by hand: delete its row")
	adopt := c.fs.Bool("adopt", false, "the directory's file is what this applied version is now: record its checksum, running nothing")
	forget := c.fs.Bool("forget", false, "the file of this clean version is gone or never ran: delete its row, undoing nothing")
	statements := c.fs.Int("statements", 0, "the first `k` statements of the dirty version ran: record that, so that apply -resume goes on from statement k+1")
	force := c.fs.String("force", "", "with -applied or -adopt: record it though the database differs from the directory, or cannot be compared; the `reason` goes in the history")
	scratch := c.fs.String("scratch-db", "", "with -applied or -adopt: an empty database's `dsn`, which the files up to the version are applied to, to compare with when files above it are pending (default on SQLite: a temporary file)")
	rest, code, done := parseFlags(c.fs, args)
	if done {
		return code
	}
	if len(rest) > 0 {
		return c.noArgs()
	}
	ways := 0
	for _, way := range []string{"applied", "undone", "statements", "adopt", "forget"} {
		if given(c.fs, way) {
			ways++
		}
	}
	if !given(c.fs, "version") || ways != 1 {
		_, _ = fmt.Fprintf(stderr, "velox-gen: migrate resolve needs -version and one of -applied, -undone, -statements, -adopt and -forget\n")
		c.fs.Usage()
		return 2
	}
	checked := given(c.fs, "applied") || *adopt
	for _, f := range []string{"force", "scratch-db"} {
		if given(c.fs, f) && !checked {
			_, _ = fmt.Fprintf(stderr, "velox-gen: migrate resolve: -%s goes with -applied or -adopt\n", f)
			c.fs.Usage()
			return 2
		}
	}
	if given(c.fs, "force") && strings.TrimSpace(*force) == "" {
		_, _ = fmt.Fprintf(stderr, "velox-gen: migrate resolve: -force needs a reason\n")
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
	r, how, opts := migrate.ResolveApplied, "applied", []migrate.Option{migrate.WithNotes(func(n string) { _, _ = fmt.Fprintf(stderr, "velox-gen: note: %s\n", c.text(n)) })}
	if *scratch != "" {
		hidden := c.secret
		sdb, code, ok := c.open(dialect.d, *scratch, "-scratch-db", false)
		c.secret = hidden.and(c.secret)
		if !ok {
			return code
		}
		defer func() { _ = sdb.Close() }()
		opts = append(opts, migrate.WithScratch(sdb))
	}
	if given(c.fs, "force") {
		opts = append(opts, migrate.WithForce(*force))
	}
	ctx, stop := signalContext()
	defer stop()
	switch {
	case *undone:
		r, how = migrate.ResolveUndone, "undone"
	case *adopt:
		r, how = migrate.ResolveAdopted, "adopted"
	case *forget:
		r, how = migrate.ResolveForgotten, "forgotten"
	case given(c.fs, "statements"):
		r, how = migrate.ResolveStatements, fmt.Sprintf("%d statements run, to resume", *statements)
		opts = append(opts, migrate.WithStatements(*statements))
	}
	if err := migrate.Resolve(ctx, db, os.DirFS(c.abs(dir)), *version, r, opts...); err != nil {
		return c.fail(1, err)
	}
	_, _ = fmt.Fprintf(stdout, "resolved version %d as %s\n", *version, how)
	return 0
}
