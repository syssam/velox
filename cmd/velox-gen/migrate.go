package main

import (
	"cmp"
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

	velox2 "github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// defaultMigrations is the migration directory when -dir is not given.
const defaultMigrations = "./migrations"

// runMigrate is velox-gen migrate: diff, apply, status, drift, lint, hash and
// resolve.
func runMigrate(args []string, dir string, env []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "velox-gen: migrate needs a subcommand: diff, apply, status, drift, lint, hash or resolve")
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "diff":
		return runMigrateDiff(args[1:], dir, env, stdout, stderr)
	case "apply":
		return runMigrateApply(args[1:], dir, env, stdout, stderr)
	case "status":
		return runMigrateStatus(args[1:], dir, env, stdout, stderr)
	case "drift":
		return runMigrateDrift(args[1:], dir, env, stdout, stderr)
	case "resolve":
		return runMigrateResolve(args[1:], dir, env, stdout, stderr)
	case "lint":
		return runMigrateLint(args[1:], dir, stderr)
	case "hash":
		return runMigrateHash(args[1:], dir, stdout, stderr)
	case "-h", "-help", "--help":
		printUsage(stderr)
		return 0
	}
	_, _ = fmt.Fprintf(stderr, "velox-gen: migrate: unknown subcommand %q\n", args[0])
	printUsage(stderr)
	return 2
}

// migrateCmd is one migrate subcommand: its name, its flag set with -dir,
// and how it reports.
type migrateCmd struct {
	name   string // "diff"
	fs     *flag.FlagSet
	dir    *string // -dir, as given
	stderr io.Writer
}

// newMigrateCmd is the subcommand name with its -dir flag.
func newMigrateCmd(name string, stderr io.Writer) *migrateCmd {
	fs := newFlagSet("migrate "+name, stderr)
	return &migrateCmd{
		name:   name,
		fs:     fs,
		dir:    fs.String("dir", defaultMigrations, "the migration directory"),
		stderr: stderr,
	}
}

// fail prints err as "velox-gen: migrate <name>: <text>", without the
// library's "migrate: ", and returns code.
func (c *migrateCmd) fail(code int, err error) int {
	_, _ = fmt.Fprintf(c.stderr, "velox-gen: migrate %s: %s\n", c.name, strings.TrimPrefix(err.Error(), "migrate: "))
	return code
}

// noArgs is the usage error of a subcommand given arguments it does not
// take.
func (c *migrateCmd) noArgs() int {
	_, _ = fmt.Fprintf(c.stderr, "velox-gen: migrate %s takes no arguments\n", c.name)
	c.fs.Usage()
	return 2
}

// path is a file of the directory as the user sees it: joined to -dir.
func (c *migrateCmd) path(name string) string { return filepath.Join(*c.dir, name) }

// abs is the directory on disk: -dir, relative to dir.
func (c *migrateCmd) abs(dir string) string {
	if filepath.IsAbs(*c.dir) {
		return *c.dir
	}
	return filepath.Join(dir, *c.dir)
}

// inDir shows the paths of err, which name files of the directory, joined
// to -dir: a *SumError's, and the Pos of an *Error that ReadDir or LintFile
// gives.
func (c *migrateCmd) inDir(err error) error {
	var se *migrate.SumError
	if errors.As(err, &se) {
		return &migrate.SumError{Path: c.path(se.Path)}
	}
	var me *migrate.Error
	if errors.As(err, &me) && (me.Pos != "" || me.Code == "directory") {
		e := *me
		if e.Pos != "" {
			e.Pos = c.path(me.Pos)
		}
		if e.Code == "directory" {
			e.Reason = migrationFile.ReplaceAllStringFunc(e.Reason, c.path)
		}
		return &e
	}
	return err
}

// migrationFile is a migration file's name in an error's text.
var migrationFile = regexp.MustCompile(`\b[0-9]{4,}_[a-z0-9_]+\.sql\b`)

// The states of a -dir.
const (
	noDir        = iota // nothing is there
	otherDir            // a directory without the files velox.sum covers, such as one holding only .gitkeep: a new one
	migrationDir        // a directory with velox.json, velox.sum, schema.json or a *.sql file
)

// dirState is the state of the directory at abs.
func dirState(abs string) (int, error) {
	ents, err := os.ReadDir(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return noDir, nil
	case err != nil:
		return 0, err
	}
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() && (n == "velox.json" || n == "velox.sum" || n == "schema.json" || strings.HasSuffix(n, ".sql")) {
			return migrationDir, nil
		}
	}
	return otherDir, nil
}

// exists refuses a -dir that is not there.
func (c *migrateCmd) exists(dir string) error {
	if _, err := os.Stat(c.abs(dir)); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s does not exist", filepath.Clean(*c.dir))
	}
	return nil
}

// readDir reads the directory at -dir. A stale velox.sum is staleCode.
func (c *migrateCmd) readDir(dir string, staleCode int) (*migrate.Dir, int, bool) {
	if err := c.exists(dir); err != nil {
		return nil, c.fail(1, err), false
	}
	d, err := migrate.ReadDir(os.DirFS(c.abs(dir)))
	if err != nil {
		code := 1
		if se := (*migrate.SumError)(nil); errors.As(err, &se) {
			code = staleCode
		}
		return nil, c.fail(code, c.inDir(err)), false
	}
	return d, 0, true
}

// dialectFlag is -dialect: postgres, mysql or sqlite.
type dialectFlag struct{ d velox2.Dialect }

func (f *dialectFlag) String() string {
	if f == nil || f.d == 0 {
		return ""
	}
	return f.d.String()
}

func (f *dialectFlag) Set(s string) error {
	for _, d := range [...]velox2.Dialect{velox2.Postgres, velox2.MySQL, velox2.SQLite} {
		if d.String() == s {
			f.d = d
			return nil
		}
	}
	return fmt.Errorf("%q is not postgres, mysql or sqlite", s)
}

// runMigrateHash is velox-gen migrate hash: it rewrites velox.sum when it
// does not match the files and prints its path; with -check it writes
// nothing and exits 3 when the sum is stale.
func runMigrateHash(args []string, dir string, stdout, stderr io.Writer) int {
	c := newMigrateCmd("hash", stderr)
	check := c.fs.Bool("check", false, "write nothing; exit 3 when velox.sum is out of date")
	rest, code, done := parseFlags(c.fs, args)
	if done {
		return code
	}
	if len(rest) > 0 {
		return c.noArgs()
	}
	if err := c.exists(dir); err != nil {
		return c.fail(1, err)
	}
	abs := c.abs(dir)
	if _, err := os.Stat(filepath.Join(abs, "velox.json")); errors.Is(err, fs.ErrNotExist) {
		return c.fail(1, fmt.Errorf("%s: not a migration directory (no velox.json); run velox-gen migrate diff first", filepath.Clean(*c.dir)))
	}
	sum, err := migrate.Sum(os.DirFS(abs))
	if err != nil {
		return c.fail(1, err)
	}
	// ReadDir checks velox.sum first, read as migrate reads every file; its
	// other errors are the directory's, which hash does not hide.
	_, err = migrate.ReadDir(os.DirFS(abs))
	switch {
	case err == nil:
		return 0
	case !errors.As(err, new(*migrate.SumError)):
		return c.fail(1, err)
	}
	if *check {
		return c.fail(3, &migrate.SumError{Path: c.path("velox.sum")})
	}
	if err := writeAll(abs, map[string][]byte{"velox.sum": sum}); err != nil {
		return c.fail(1, err)
	}
	_, _ = fmt.Fprintln(stdout, c.path("velox.sum"))
	return 0
}

// runMigrateLint is velox-gen migrate lint: it reads the directory and
// checks each file from -since with migrate.LintFile, printing each error
// and each destructive finding the file does not allow as
// "<pos>: <code>: <message>".
func runMigrateLint(args []string, dir string, stderr io.Writer) int {
	c := newMigrateCmd("lint", stderr)
	since := c.fs.Uint64("since", 0, "check the files from `version` on (default: every file)")
	rest, code, done := parseFlags(c.fs, args)
	if done {
		return code
	}
	if len(rest) > 0 {
		return c.noArgs()
	}
	d, code, ok := c.readDir(dir, 1)
	if !ok {
		return code
	}
	bad := false
	for _, f := range d.Files {
		if f.Version < *since {
			continue
		}
		findings, err := migrate.LintFile(d.Config.Dialect, f)
		var lines []string // by statement: errors and findings in file order
		for _, e := range unjoin(err) {
			if me := (*migrate.Error)(nil); errors.As(e, &me) {
				lines = append(lines, c.path(me.Pos)+": "+me.Code+": "+me.Reason)
			} else {
				lines = append(lines, strings.TrimPrefix(e.Error(), "migrate: "))
			}
		}
		for _, fd := range migrate.Blocked(findings, f.Directives.Allow) {
			lines = append(lines, c.path(fd.Pos)+": "+fd.Code+": "+fd.Message)
		}
		sortByLine(lines)
		for _, l := range lines {
			_, _ = fmt.Fprintln(stderr, l)
			bad = true
		}
	}
	if bad {
		return 1
	}
	return 0
}

// unjoin is the errors errors.Join joined into err, or err alone.
func unjoin(err error) []error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}

// sortByLine orders "<path>:<line>: …" lines of one file by line, keeping
// the order of lines on one line.
func sortByLine(lines []string) {
	line := func(s string) int {
		pos, _, _ := strings.Cut(s, ": ")
		n, _ := strconv.Atoi(pos[strings.LastIndexByte(pos, ':')+1:])
		return n
	}
	slices.SortStableFunc(lines, func(a, b string) int { return cmp.Compare(line(a), line(b)) })
}
