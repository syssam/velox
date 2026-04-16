package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	velsql "github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/schema/field"

	_ "modernc.org/sqlite"
)

// testEntity is a minimal entity struct used by runtime tests that need
// a concrete entity type but don't rely on generated code. It implements
// the Scannable interface so scan helpers (ScanAll/ScanFirst) can target it.
type testEntity struct {
	ID   int
	Name string
	Age  int
}

// ScanValues returns the destinations for a row scan based on column names.
func (*testEntity) ScanValues(columns []string) ([]any, error) {
	values := make([]any, len(columns))
	for i, col := range columns {
		switch col {
		case "id", "age":
			values[i] = new(sql.NullInt64)
		case "name":
			values[i] = new(sql.NullString)
		default:
			return nil, fmt.Errorf("unexpected column %q", col)
		}
	}
	return values, nil
}

// AssignValues copies scanned values into the entity fields.
func (e *testEntity) AssignValues(columns []string, values []any) error {
	for i, col := range columns {
		switch col {
		case "id":
			if v, ok := values[i].(*sql.NullInt64); ok {
				e.ID = int(v.Int64)
			}
		case "name":
			if v, ok := values[i].(*sql.NullString); ok {
				e.Name = v.String
			}
		case "age":
			if v, ok := values[i].(*sql.NullInt64); ok {
				e.Age = int(v.Int64)
			}
		default:
			return fmt.Errorf("unexpected column %q", col)
		}
	}
	return nil
}

// testMeta holds test metadata for testEntity. Provides the glue required
// by QueryBase/edge loaders (RegisteredTypeInfo + ScanConfig).
type testMeta struct {
	Table       string
	Columns     []string
	IDColumn    string
	IDFieldType field.Type
	FieldTypes  map[string]field.Type
	ScanValues  func(columns []string) ([]any, error)
	New         func() *testEntity
	Assign      func(entity *testEntity, columns []string, values []any) error
	GetID       func(entity *testEntity) any
	SetDriver   func(entity *testEntity, driver dialect.Driver)
	LoadEdges   func(ctx context.Context, nodes []*testEntity, edges []EdgeLoad, driver dialect.Driver) error
}

// RegisteredInfo returns a RegisteredTypeInfo for query/edge operations.
func (m *testMeta) RegisteredInfo() *RegisteredTypeInfo {
	info := &RegisteredTypeInfo{
		Table:      m.Table,
		Columns:    m.Columns,
		IDColumn:   m.IDColumn,
		ScanValues: m.ScanValues,
		New:        func() any { return m.New() },
		Assign:     func(e any, cols []string, vals []any) error { return m.Assign(e.(*testEntity), cols, vals) },
	}
	if m.GetID != nil {
		info.GetID = func(e any) any { return m.GetID(e.(*testEntity)) }
	}
	return info
}

// ScanConfig returns a ScanConfig for test query operations.
func (m *testMeta) ScanConfig() *ScanConfig {
	sc := &ScanConfig{
		Table:       m.Table,
		Columns:     m.Columns,
		IDColumn:    m.IDColumn,
		IDFieldType: m.IDFieldType,
		ScanValues:  m.ScanValues,
		New:         func() any { return m.New() },
		Assign:      func(e any, cols []string, vals []any) error { return m.Assign(e.(*testEntity), cols, vals) },
	}
	if m.GetID != nil {
		sc.GetID = func(e any) any { return m.GetID(e.(*testEntity)) }
	}
	if m.SetDriver != nil {
		sc.SetDriver = func(e any, drv dialect.Driver) { m.SetDriver(e.(*testEntity), drv) }
	}
	return sc
}

// testTypeInfo returns a testMeta for testEntity.
func testTypeInfo() *testMeta {
	return &testMeta{
		Table:       "users",
		Columns:     []string{"id", "name", "age"},
		IDColumn:    "id",
		IDFieldType: field.TypeInt,
		New:         func() *testEntity { return &testEntity{} },
		ScanValues: func(columns []string) ([]any, error) {
			values := make([]any, len(columns))
			for i, col := range columns {
				switch col {
				case "id", "age":
					values[i] = new(sql.NullInt64)
				case "name":
					values[i] = new(sql.NullString)
				default:
					return nil, fmt.Errorf("unexpected column %q", col)
				}
			}
			return values, nil
		},
		GetID: func(entity *testEntity) any { return entity.ID },
		Assign: func(entity *testEntity, columns []string, values []any) error {
			for i, col := range columns {
				switch col {
				case "id":
					switch v := values[i].(type) {
					case *sql.NullInt64:
						entity.ID = int(v.Int64)
					case int64:
						entity.ID = int(v)
					case int:
						entity.ID = v
					default:
						return fmt.Errorf("unexpected type %T for column %q", v, col)
					}
				case "name":
					switch v := values[i].(type) {
					case *sql.NullString:
						entity.Name = v.String
					case string:
						entity.Name = v
					default:
						return fmt.Errorf("unexpected type %T for column %q", v, col)
					}
				case "age":
					switch v := values[i].(type) {
					case *sql.NullInt64:
						entity.Age = int(v.Int64)
					case int64:
						entity.Age = int(v)
					case int:
						entity.Age = v
					default:
						return fmt.Errorf("unexpected type %T for column %q", v, col)
					}
				default:
					return fmt.Errorf("unexpected column %q", col)
				}
			}
			return nil
		},
	}
}

// failDriver wraps a real driver but fails Query/Exec after a configurable number of calls.
type failDriver struct {
	dialect.Driver
	failAfter int // fail on the Nth call (0 = fail immediately)
	calls     int
	err       error
}

func (d *failDriver) Query(ctx context.Context, query string, args, v any) error {
	d.calls++
	if d.calls > d.failAfter {
		return d.err
	}
	return d.Driver.Query(ctx, query, args, v)
}

func (d *failDriver) Exec(ctx context.Context, query string, args, v any) error {
	d.calls++
	if d.calls > d.failAfter {
		return d.err
	}
	return d.Driver.Exec(ctx, query, args, v)
}

// seedUsers inserts multiple users directly via SQL INSERT.
// Returns created entities with their auto-assigned IDs.
func seedUsers(ctx context.Context, t *testing.T, drv dialect.Driver, _ *testMeta, users []struct {
	Name string
	Age  int
}) []*testEntity {
	t.Helper()
	result := make([]*testEntity, len(users))
	for i, u := range users {
		var res sql.Result
		err := drv.Exec(ctx, "INSERT INTO users (name, age) VALUES (?, ?)", []any{u.Name, u.Age}, &res)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		result[i] = &testEntity{ID: int(id), Name: u.Name, Age: u.Age}
	}
	return result
}

// captureDriver wraps a real driver and records the last query string.
type captureDriver struct {
	dialect.Driver
	lastQuery string
}

func (d *captureDriver) Query(ctx context.Context, query string, args, v any) error {
	d.lastQuery = query
	return d.Driver.Query(ctx, query, args, v)
}

// newTestDB creates an in-memory SQLite database with a users table.
func newTestDB(tb testing.TB) dialect.Driver {
	tb.Helper()
	db, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	require.NoError(tb, err)
	tb.Cleanup(func() { db.Close() })

	_, err = db.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		age INTEGER NOT NULL DEFAULT 0
	)`)
	require.NoError(tb, err)

	return velsql.OpenDB(dialect.SQLite, db)
}
