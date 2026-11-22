package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// createErr is the *Error of Create's refusal, or fails t.
func createErr(t *testing.T, err error) *migrate.Error {
	t.Helper()
	e, ok := errors.AsType[*migrate.Error](err)
	if !ok {
		t.Fatalf("Create: %v, want an *Error", err)
	}
	return e
}

// TestCreate: on each dialect Create makes the base model's tables, which
// Drift then finds as the model says; a second Create does nothing; a
// database that holds part of the model, or holds it otherwise, is refused
// and left as it was; and no history table is made.
func TestCreate(t *testing.T) {
	t.Parallel()
	for _, d := range []velox.Dialect{velox.SQLite, velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			model := mustTables(d, nil)
			db, _ := freshDB(t, d)
			if err := migrate.Create(ctx, db, model); err != nil {
				t.Fatalf("Create: %v", err)
			}
			noChanges(t, db, model)
			if err := migrate.Create(ctx, db, model); err != nil {
				t.Errorf("a second Create: %v", err)
			}
			held, err := migrate.Inspect(ctx, db, d, []migrate.TableRef{{Name: "velox_migrations"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(held.Tables) != 0 {
				t.Errorf("Create made a history table: %v", held.Tables)
			}

			// A table that differs is refused, naming the change.
			other := mustTables(d, nil)
			table(other, "users").Columns = append(table(other, "users").Columns, migrate.Column{Name: "nick", Type: table(other, "users").Columns[1].Type, Nullable: true})
			e := createErr(t, migrate.Create(ctx, db, other))
			if e.Code != "create" || !strings.Contains(e.Reason, "nick") {
				t.Errorf("Create of a changed model: %v; want Code create naming nick", e)
			}
			noChanges(t, db, model)
		})
	}
}

// TestCreatePartial: a database that holds one of the model's tables is
// refused, and Create makes none of the others.
func TestCreatePartial(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	model := mustTables(velox.SQLite, nil)
	db, _ := freshDB(t, velox.SQLite)
	one := &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{*table(model, "users")}}
	if err := migrate.Create(ctx, db, one); err != nil {
		t.Fatalf("Create of one table: %v", err)
	}
	e := createErr(t, migrate.Create(ctx, db, model))
	if e.Code != "create" || !strings.Contains(e.Reason, "create-table") {
		t.Errorf("Create over part of the model: %v; want Code create listing create-table", e)
	}
	held, err := migrate.Inspect(ctx, db, velox.SQLite, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(held.Tables) != 1 {
		t.Errorf("Create made tables of a refused model: %d tables", len(held.Tables))
	}
}

// TestCreateRefusals: a nil database or schema, an unknown dialect and a
// SQLite database in memory are refused with their codes.
func TestCreateRefusals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	model := mustTables(velox.SQLite, nil)
	db, _ := freshDB(t, velox.SQLite)
	for _, c := range []struct {
		name string
		db   *sql.DB
		s    *migrate.Schema
		code string
	}{
		{"nil db", nil, model, "nil"},
		{"nil schema", db, nil, "nil"},
		{"no dialect", db, &migrate.Schema{}, "dialect"},
	} {
		if e := createErr(t, migrate.Create(ctx, c.db, c.s)); e.Code != c.code {
			t.Errorf("%s: %v; want Code %s", c.name, e, c.code)
		}
	}
	mem := openDB(t, "sqlite", ":memory:")
	mem.SetMaxOpenConns(1)
	if e := createErr(t, migrate.Create(ctx, mem, model)); e.Code != "session" || !strings.Contains(e.Reason, "Create would leave nothing") {
		t.Errorf("in memory: %v; want Code session", e)
	}
}

// TestCreateFails: a statement the database refuses fails Create, naming
// the statement, and the SQLite transaction leaves no table.
func TestCreateFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	model := mustTables(velox.SQLite, nil)
	table(model, "users").Columns[1].Type = "no such(type"
	db, _ := freshDB(t, velox.SQLite)
	err := migrate.Create(ctx, db, model)
	if err == nil || !strings.Contains(err.Error(), "CREATE TABLE") {
		t.Fatalf("Create of a bad type: %v; want the failing statement", err)
	}
	held, err := migrate.Inspect(ctx, db, velox.SQLite, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(held.Tables) != 0 {
		t.Errorf("a failed Create left %d tables", len(held.Tables))
	}
}
