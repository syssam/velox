// Package veloxtest opens a database for a test with the schema's tables
// made, as ent's enttest does:
//
//	var schemas = []schema.Interface{veloxschema.User{}, veloxschema.Post{}}
//
//	func TestUsers(t *testing.T) {
//		db := veloxtest.Open(t, "sqlite", "", schemas)
//		...
//	}
//
// The tables are the ones velox-gen plans for the schema package, built in
// the test's process from the schema values given, and made by
// migrate.Create. It is for tests and development databases. A production
// database keeps versioned migrations, written by velox-gen migrate diff and
// run by migrate.Up.
package veloxtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/load"
)

// Schema returns the tables velox-gen plans for schemas on dialect d: what
// velox-gen migrate diff would write as a new directory's schema.json. The
// schemas are every schema of one package, as velox-gen discovers them; a
// relation to a schema left out is an error. Each error of the model is
// one of the errors joined in the result, as velox-gen reports them.
//
// It reads the schema values in the process, so a test needs no go
// command, and the generated package is not involved.
func Schema(d velox.Dialect, schemas ...schema.Interface) (*migrate.Schema, error) {
	if len(schemas) == 0 {
		return nil, errors.New("veloxtest: no schemas")
	}
	pkg := ""
	for _, s := range schemas {
		if s == nil {
			return nil, errors.New("veloxtest: a schema is nil")
		}
		t := reflect.TypeOf(s)
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		switch {
		case pkg == "":
			pkg = t.PkgPath()
		case t.PkgPath() != pkg:
			return nil, fmt.Errorf("veloxtest: schema %s is of package %s and the others of %s; velox-gen reads one package", t.Name(), t.PkgPath(), pkg)
		}
	}
	b, err := load.Marshal(pkg, schemas...)
	if err != nil {
		return nil, fmt.Errorf("veloxtest: %w", err)
	}
	var m loadjson.Model
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("veloxtest: %w", err)
	}
	if len(m.Errors) > 0 {
		errs := make([]error, len(m.Errors))
		for i, e := range m.Errors {
			errs[i] = &codegen.Error{Pos: e.Pos, Schema: e.Schema, Where: e.Where, Reason: e.Reason}
		}
		return nil, errors.Join(errs...)
	}
	g, err := codegen.Build(&m)
	if err != nil {
		return nil, err
	}
	return codegen.Tables(g, d)
}

// built holds the tables Open built, by dialect and schema values, so that
// the tests of one package build them once.
var built sync.Map // string → *migrate.Schema

// cached is Schema, built once per dialect and schema values, and the key
// it is kept by.
func cached(d velox.Dialect, schemas []schema.Interface) (*migrate.Schema, string, error) {
	key := fmt.Sprintf("%d %#v", d, schemas)
	if s, ok := built.Load(key); ok {
		return s.(*migrate.Schema), key, nil
	}
	s, err := Schema(d, schemas...)
	if err != nil {
		return nil, "", err
	}
	built.Store(key, s)
	return s, key, nil
}

// Open opens a database with velox.Connect(driver, dsn, opts...), makes the
// tables of schemas in it with migrate.Create, and closes it when t ends.
// It fails t on an error. The application imports the driver, as for
// Connect. The DB counts statements (velox.WithStatementCounting) for
// NoNPlusOne and Budget, before opts' Around hooks.
//
// On SQLite ("sqlite" or "sqlite3") an empty dsn, or one of a database in
// memory, as enttest's "file:ent?mode=memory&cache=shared&_fk=1", is a
// database of the call's own: a file in t.TempDir() that is a copy of the
// tables Create made once for the test binary, opened with the dsn's other
// parameters, and with journal_mode = MEMORY and synchronous = OFF, which
// a database the test throws away does without. Two calls with one such dsn
// never share a database. A dsn naming a file is opened as it is.
//
// On PostgreSQL and MySQL the dsn names a database, and each test should
// have its own. On every dialect, for a dsn opened as it is, Create makes
// the tables in an empty database, accepts one that holds them as the
// schema says, rows and all, and refuses any other.
func Open(t testing.TB, driver, dsn string, schemas []schema.Interface, opts ...velox.Option) *velox.DB {
	t.Helper()
	return open(t, driver, dsn, func(d velox.Dialect) (*migrate.Schema, string, error) { return cached(d, schemas) }, opts)
}

// open is Open of the tables that tables gives for a dialect, with the key
// they are kept by.
func open(t testing.TB, driver, dsn string, tables func(velox.Dialect) (*migrate.Schema, string, error), opts []velox.Option) *velox.DB {
	t.Helper()
	opts = append([]velox.Option{velox.WithStatementCounting()}, opts...)
	if isSQLite(driver) && (dsn == "" || inMemory(dsn)) {
		s, key, err := tables(velox.SQLite)
		if err != nil {
			t.Fatalf("veloxtest: the schema's tables: %v", err)
		}
		return openCopy(t, driver, dsn, key, s, opts)
	}
	db, err := velox.Connect(driver, dsn, opts...)
	if err != nil {
		t.Fatalf("veloxtest: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, _, err := tables(db.Dialect())
	if err != nil {
		t.Fatalf("veloxtest: the schema's tables: %v", err)
	}
	if err := migrate.Create(t.Context(), velox.Pool(db), s); err != nil {
		t.Fatalf("veloxtest: %v", err)
	}
	return db
}

// isSQLite reports whether driver is a SQLite driver's usual name.
func isSQLite(driver string) bool { return driver == "sqlite" || driver == "sqlite3" }

// inMemory reports whether a SQLite dsn names a database in memory:
// ":memory:", "file::memory:…", or a URI of mode=memory or vfs=memdb.
func inMemory(dsn string) bool {
	if dsn == ":memory:" || strings.HasPrefix(dsn, "file::memory:") {
		return true
	}
	_, query, _ := strings.Cut(dsn, "?")
	for p := range strings.SplitSeq(strings.ToLower(query), "&") {
		if p == "mode=memory" || p == "vfs=memdb" {
			return true
		}
	}
	return false
}

// fileParams is the query of a dsn in memory, as "?…", without what makes
// it one (mode, cache, vfs); "" when nothing is left.
func fileParams(dsn string) string {
	_, query, _ := strings.Cut(dsn, "?")
	var kept []string
	for p := range strings.SplitSeq(query, "&") {
		name, _, _ := strings.Cut(strings.ToLower(p), "=")
		if p != "" && name != "mode" && name != "cache" && name != "vfs" {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return "?" + strings.Join(kept, "&")
}

// An image is a SQLite database file holding a schema's tables, made once.
type image struct {
	once sync.Once
	data []byte
	err  error
}

// images holds the images openCopy made, by driver and schema key.
var images sync.Map // string → *image

// openCopy opens, for t alone, a file in t.TempDir() that is a copy of the
// image of s, the tables kept by key, for driver.
func openCopy(t testing.TB, driver, dsn, key string, s *migrate.Schema, opts []velox.Option) *velox.DB {
	t.Helper()
	v, _ := images.LoadOrStore(driver+"\x00"+key, new(image))
	img := v.(*image)
	img.once.Do(func() { img.data, img.err = makeImage(context.WithoutCancel(t.Context()), driver, s) })
	if img.err != nil {
		t.Fatalf("veloxtest: making the schema's tables: %v", img.err)
	}
	path := filepath.Join(t.TempDir(), "velox.db")
	if err := os.WriteFile(path, img.data, 0o600); err != nil {
		t.Fatalf("veloxtest: %v", err)
	}
	db, err := velox.Connect(driver, path+fileParams(dsn), opts...)
	if err != nil {
		t.Fatalf("veloxtest: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := throwaway(t.Context(), velox.Pool(db)); err != nil {
		t.Fatalf("veloxtest: %v", err)
	}
	return db
}

// throwaway sets db, a SQLite pool of one connection, for a database no
// one keeps: its journal in memory, and no syncs.
func throwaway(ctx context.Context, db *sql.DB) error {
	for _, q := range []string{"PRAGMA journal_mode = MEMORY", "PRAGMA synchronous = OFF"} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}

// makeImage makes s's tables with migrate.Create in a new file of driver's,
// and returns the file.
func makeImage(ctx context.Context, driver string, s *migrate.Schema) ([]byte, error) {
	dir, err := os.MkdirTemp("", "veloxtest-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "velox.db")
	db, err := velox.Connect(driver, path)
	if err != nil {
		return nil, err
	}
	err = throwaway(ctx, velox.Pool(db))
	if err == nil {
		err = migrate.Create(ctx, velox.Pool(db), s)
	}
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// Budget returns ctx with a velox.StatementCounter, and fails t when the
// test ends if the statements run with it went over b, naming each read
// repeated. The DB must count statements, as Open's does.
func Budget(t testing.TB, ctx context.Context, b velox.StatementBudget) context.Context {
	t.Helper()
	ctx, n := velox.CountStatements(ctx)
	t.Cleanup(func() {
		if err := n.Check(b); err != nil {
			t.Error(err)
		}
	})
	return ctx
}

// NoNPlusOne is Budget with no read of one shape run twice: a loop that
// reads a relation once per row, rather than with With, fails the test.
//
//	ctx := veloxtest.NoNPlusOne(t, t.Context())
//	posts, err := listPosts(ctx, db) // fails t if it reads each author alone
//
// velox's GraphQL resolvers batch the reads of a relation per request, so a
// query through them passes.
func NoNPlusOne(t testing.TB, ctx context.Context) context.Context {
	t.Helper()
	return Budget(t, ctx, velox.StatementBudget{Repeats: 1})
}
