package engine

import (
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	_ "modernc.org/sqlite"
)

func TestSQLiteKind(t *testing.T) {
	for _, c := range []struct {
		code int
		msg  string
		kind ConstraintKind
		name string
	}{
		{787, "constraint failed: FOREIGN KEY constraint failed (787)", ConstraintForeignKey, ""},
		{2067, "constraint failed: UNIQUE constraint failed: p.code (2067)", ConstraintUnique, "p.code"},
		{1299, "constraint failed: NOT NULL constraint failed: p.code (1299)", ConstraintNotNull, "code"},
	} {
		if kind, name := SqliteKind(c.code, c.msg); kind != c.kind || name != c.name {
			t.Errorf("sqliteKind(%d, %q) = %v, %q", c.code, c.msg, kind, name)
		}
	}
	if got := (&ConstraintError{Entity: "e", Kind: ConstraintForeignKey, Err: io.EOF}).Error(); got != "velox: e: foreign key constraint (unnamed) violated: EOF" {
		t.Error(got)
	}
}

// TestIsNilTarget: is with a nil target matches as errors.Is does, a nil err
// only.
func TestIsNilTarget(t *testing.T) {
	for _, err := range []error{nil, errors.New("x"), (*pgconn.PgError)(nil), fmt.Errorf("x: %w", io.EOF)} {
		if got, want := Is(err, nil), errors.Is(err, nil); got != want {
			t.Errorf("is(%#v, nil) = %v, errors.Is says %v", err, got, want)
		}
	}
}

// TestPgxTypesMirrored pins what the core knows of pgx without importing it, so
// that a pgx upgrade that changes one of them fails here: a sentinel's text, the
// fields read from a PgError, and the type names.
func TestPgxTypesMirrored(t *testing.T) {
	if got := pgconn.ErrConnClosed.Error(); got != PgConnClosed {
		t.Errorf("pgconn.ErrConnClosed is %q, pgConnClosed %q", got, PgConnClosed)
	}
	if got := pgx.ErrTxClosed.Error(); got != pgTxClosed {
		t.Errorf("pgx.ErrTxClosed is %q, pgTxClosed %q", got, pgTxClosed)
	}
	if !DriverSentinel(fmt.Errorf("x: %w", pgconn.ErrConnClosed), PgConnClosed) || !Ended(fmt.Errorf("x: %w", pgx.ErrTxClosed)) {
		t.Error("pgx's sentinels are not recognized through a wrap")
	}
	if got := reflect.TypeFor[pgconn.PgError]().PkgPath(); got != PgconnPkg {
		t.Errorf("PgError is in %q, pgconnPkg is %q", got, PgconnPkg)
	}
	pg, ok := PgErrorOf(fmt.Errorf("x: %w", &pgconn.PgError{Code: "23502", ConstraintName: "c", ColumnName: "k"}))
	if want := (pgErr{Code: "23502", ConstraintName: "c", ColumnName: "k"}); !ok || pg != want {
		t.Errorf("pgError read %+v, %v; want %+v", pg, ok, want)
	}
	if _, ok := reflect.TypeFor[pgx.Rows]().MethodByName("FieldDescriptions"); !ok {
		t.Error("pgx.Rows has no FieldDescriptions")
	}
	if f, ok := reflect.TypeFor[pgconn.FieldDescription]().FieldByName("Name"); !ok || f.Type.Kind() != reflect.String {
		t.Error("pgconn.FieldDescription has no string Name")
	}
}

// pgxRowsNames is a fake with pgx.Rows's way of naming columns.
type pgxRowsNames struct{ Rows }

func (pgxRowsNames) FieldDescriptions() []pgconn.FieldDescription {
	return []pgconn.FieldDescription{{Name: "id"}, {Name: "number"}}
}

// TestResultColumnsOfPgxRows: rows that name their columns as pgx's do are read
// without a pgx import in the core.
func TestResultColumnsOfPgxRows(t *testing.T) {
	got, err := resultColumns(pgxRowsNames{})
	if err != nil || !slices.Equal(got, []string{"id", "number"}) {
		t.Errorf("columns %v, error %v", got, err)
	}
}
