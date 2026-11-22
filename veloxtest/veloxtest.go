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
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
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

// cached is Schema, built once per dialect and schema values.
func cached(d velox.Dialect, schemas []schema.Interface) (*migrate.Schema, error) {
	key := fmt.Sprintf("%d %#v", d, schemas)
	if s, ok := built.Load(key); ok {
		return s.(*migrate.Schema), nil
	}
	s, err := Schema(d, schemas...)
	if err != nil {
		return nil, err
	}
	built.Store(key, s)
	return s, nil
}

// Open opens a database with velox.Connect(driver, dsn, opts...), makes the
// tables of schemas in it with migrate.Create, and closes it when t ends.
// It fails t on an error. The application imports the driver, as for
// Connect.
//
// On SQLite ("sqlite" or "sqlite3") an empty dsn is a new file in
// t.TempDir(), so each call has a database of its own. Create cannot use
// :memory:, which is a database per connection. On PostgreSQL and MySQL the
// dsn names a database, and each test should have its own: Create makes the
// tables in an empty database, accepts one that holds them as the schema
// says, rows and all, and refuses any other.
func Open(t testing.TB, driver, dsn string, schemas []schema.Interface, opts ...velox.Option) *velox.DB {
	t.Helper()
	if dsn == "" && (driver == "sqlite" || driver == "sqlite3") {
		dsn = filepath.Join(t.TempDir(), "velox.db")
	}
	db, err := velox.Connect(driver, dsn, opts...)
	if err != nil {
		t.Fatalf("veloxtest: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := cached(db.Dialect(), schemas)
	if err != nil {
		t.Fatalf("veloxtest: the schema's tables: %v", err)
	}
	if err := migrate.Create(t.Context(), velox.Pool(db), s); err != nil {
		t.Fatalf("veloxtest: %v", err)
	}
	return db
}
