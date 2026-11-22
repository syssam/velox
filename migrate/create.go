package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/syssam/velox"
)

// Create makes schema's tables and enum types in db, a database of
// schema's dialect, for a test or a development database. It plans from an
// empty schema, as the first file of migrate diff would, and runs the
// statements: on PostgreSQL and SQLite in one transaction, on MySQL one
// after another, where a statement that fails leaves the ones before it.
// It writes no history: a database Create made is not one
// that Up manages. A production database keeps versioned migrations,
// written by velox-gen migrate diff and run by Up.
//
// Create reads the tables and enum types schema names first. When db holds
// none of them, it makes them all. When db holds them as schema says, as
// Drift finds, it does nothing, so a program may call it on each start.
// Otherwise it changes nothing and refuses, with an *Error of Code
// "create" that lists what differs: Create makes a new database, and never
// alters one.
//
// Like Up, Create uses one connection of db and closes it after. On SQLite
// db must be a file, or a shared-cache database in a pool of two or more
// connections: each connection to :memory: is a database of its own, and
// Create refuses one with Code "session". A nil db or schema is Code "nil";
// a schema of no dialect migrate knows is Code "dialect".
func Create(ctx context.Context, db *sql.DB, schema *Schema) error {
	switch {
	case db == nil:
		return &Error{Code: "nil", Reason: "Create needs a database"}
	case schema == nil:
		return &Error{Code: "nil", Reason: "Create needs a schema"}
	case !knownDialect(schema.Dialect):
		return &Error{Code: "dialect", Reason: fmt.Sprintf("Create was given a schema of the unknown dialect %q", schema.Dialect.String())}
	}
	plan, err := NewPlan(&Schema{Dialect: schema.Dialect}, schema, PlanOptions{})
	if err != nil {
		return err
	}
	s, err := open(ctx, db, Config{Dialect: schema.Dialect}, defaultLockWait)
	if err != nil {
		return err
	}
	defer s.close()
	if err := s.lasting(ctx, "Create"); err != nil {
		return err
	}
	if done, err := s.created(ctx, schema); done || err != nil {
		return err
	}
	if err := s.strict(ctx); err != nil {
		return err
	}
	defer s.unstrict(ctx)
	for _, sc := range plan.Scripts {
		if err := s.runScript(ctx, sc); err != nil {
			return err
		}
	}
	return nil
}

// created reports whether the session's database holds schema already, and
// refuses one that holds part of it, or holds it otherwise.
func (s *session) created(ctx context.Context, schema *Schema) (bool, error) {
	tables, enums := refsOf(schema)
	held, notes, err := inspectConn(ctx, s.conn, s.d, tables, enums)
	if err != nil {
		return false, err
	}
	if len(held.Tables) == 0 && len(held.Enums) == 0 {
		return false, nil
	}
	r, err := drift(held, notes, schema)
	if err != nil {
		return false, err
	}
	if len(r.Changes) == 0 {
		return true, nil
	}
	lines := make([]string, 0, len(r.Changes))
	for _, c := range r.Changes {
		lines = append(lines, c.String())
	}
	return false, &Error{Code: "create", Reason: "the database holds part of the schema, or holds it otherwise, and Create makes a new database only; drop it, or manage it with migrate diff and Up. What differs:\n  " + strings.Join(lines, "\n  ")}
}

// runScript runs sc's statements on the session: in a transaction, but on
// MySQL, whose DDL commits itself, and for a PostgreSQL script that runs
// outside one.
func (s *session) runScript(ctx context.Context, sc Script) error {
	tx := s.d != velox.MySQL && sc.Kind == ScriptMain && !sc.Directives.NoTransaction
	if tx {
		if err := s.begin(ctx); err != nil {
			return wrap(ctx, "create", err)
		}
	}
	for _, st := range sc.Statements {
		if _, err := s.conn.ExecContext(ctx, st.SQL); err != nil {
			if tx {
				s.rollback(ctx)
			}
			return wrap(ctx, "create", fmt.Errorf("%s: %w", st.SQL, err))
		}
	}
	if tx {
		if _, err := s.conn.ExecContext(ctx, "COMMIT"); err != nil {
			s.rollback(ctx)
			return wrap(ctx, "create", err)
		}
	}
	return nil
}
