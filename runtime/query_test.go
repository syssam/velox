package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/schema/field"
)

// mockDriver is a minimal dialect.Driver used for query-capture tests.
type mockDriver struct {
	dialectName string
	queryFn     func(ctx context.Context, query string, args, v any) error
}

func (m *mockDriver) Query(ctx context.Context, query string, args, v any) error {
	if m.queryFn != nil {
		return m.queryFn(ctx, query, args, v)
	}
	return nil
}

func (m *mockDriver) Exec(_ context.Context, _ string, _, _ any) error { return nil }
func (m *mockDriver) Tx(_ context.Context) (dialect.Tx, error)         { return nil, nil }
func (m *mockDriver) Close() error                                     { return nil }
func (m *mockDriver) Dialect() string                                  { return m.dialectName }

func TestQueryContext_Clone(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		var c *QueryContext
		assert.Nil(t, c.Clone())
	})

	t.Run("with_fields", func(t *testing.T) {
		limit := 10
		c := &QueryContext{
			Type:   "User",
			Fields: []string{"name", "email"},
			Limit:  &limit,
		}
		clone := c.Clone()
		require.NotNil(t, clone)
		assert.Equal(t, "User", clone.Type)
		assert.Equal(t, []string{"name", "email"}, clone.Fields)
		require.NotNil(t, clone.Limit)
		assert.Equal(t, 10, *clone.Limit)

		// Mutating clone should not affect original.
		clone.Fields = append(clone.Fields, "age")
		assert.Len(t, c.Fields, 2)
		assert.Len(t, clone.Fields, 3)
	})
}

func TestQueryContext_AppendFieldOnce(t *testing.T) {
	c := &QueryContext{Type: "User"}
	c.AppendFieldOnce("name")
	c.AppendFieldOnce("email")
	c.AppendFieldOnce("name") // duplicate
	assert.Equal(t, []string{"name", "email"}, c.Fields)
}

func TestQueryGroupBy_SetsDialect(t *testing.T) {
	sentinel := fmt.Errorf("query captured")
	var capturedQuery string
	drv := &mockDriver{
		dialectName: dialect.Postgres,
		queryFn: func(_ context.Context, query string, _ any, _ any) error {
			capturedQuery = query
			return sentinel
		},
	}

	base := newTestQuery(drv, "users", []string{"id", "name"}, "id", nil, "User")
	var result []map[string]any
	err := QueryGroupBy(context.Background(), base, []string{"name"}, nil, &result)
	require.ErrorIs(t, err, sentinel)

	// Postgres dialect uses double-quoted identifiers.
	assert.True(t, strings.Contains(capturedQuery, `"name"`),
		"expected double-quoted identifier in query, got: %s", capturedQuery)
}

func TestQueryScan_SingleColumn(t *testing.T) {
	sentinel := fmt.Errorf("stop after capture")
	var capturedQuery string
	drv := &mockDriver{
		dialectName: dialect.SQLite,
		queryFn: func(_ context.Context, query string, _ any, _ any) error {
			capturedQuery = query
			return sentinel
		},
	}

	base := newTestQuery(drv, "users", []string{"id", "name"}, "id", nil, "User")
	base.Ctx.Fields = []string{"name"}
	var result []string
	err := QueryScan(context.Background(), base, &result)
	require.ErrorIs(t, err, sentinel)

	assert.True(t, strings.Contains(capturedQuery, "name"),
		"expected 'name' in query, got: %s", capturedQuery)
	assert.False(t, strings.Contains(capturedQuery, "id"),
		"should not contain 'id' when only 'name' selected, got: %s", capturedQuery)
}

func TestQueryScan_AllColumns(t *testing.T) {
	sentinel := fmt.Errorf("stop after capture")
	var capturedQuery string
	drv := &mockDriver{
		dialectName: dialect.SQLite,
		queryFn: func(_ context.Context, query string, _ any, _ any) error {
			capturedQuery = query
			return sentinel
		},
	}

	// No Fields set → all Columns should be selected.
	base := newTestQuery(drv, "users", []string{"id", "name", "age"}, "id", nil, "User")
	var result []map[string]any
	err := QueryScan(context.Background(), base, &result)
	require.ErrorIs(t, err, sentinel)

	assert.True(t, strings.Contains(capturedQuery, "id"), "expected 'id' in query: %s", capturedQuery)
	assert.True(t, strings.Contains(capturedQuery, "name"), "expected 'name' in query: %s", capturedQuery)
	assert.True(t, strings.Contains(capturedQuery, "age"), "expected 'age' in query: %s", capturedQuery)
}

func TestQueryScan_AppliesPredicatesOrderLimitOffset(t *testing.T) {
	sentinel := fmt.Errorf("stop after capture")
	var capturedQuery string
	drv := &mockDriver{
		dialectName: dialect.SQLite,
		queryFn: func(_ context.Context, query string, _ any, _ any) error {
			capturedQuery = query
			return sentinel
		},
	}

	base := newTestQuery(drv, "users", []string{"id", "name"}, "id", nil, "User")
	base.Where(func(s *sql.Selector) {
		s.Where(sql.EQ("active", true))
	})
	base.AddOrder(func(s *sql.Selector) {
		s.OrderBy("name")
	})
	base.SetLimit(10)
	base.SetOffset(5)

	var result []string
	err := QueryScan(context.Background(), base, &result)
	require.ErrorIs(t, err, sentinel)

	assert.True(t, strings.Contains(capturedQuery, "WHERE"), "expected WHERE: %s", capturedQuery)
	assert.True(t, strings.Contains(capturedQuery, "ORDER BY"), "expected ORDER BY: %s", capturedQuery)
	assert.True(t, strings.Contains(capturedQuery, "LIMIT"), "expected LIMIT: %s", capturedQuery)
	assert.True(t, strings.Contains(capturedQuery, "OFFSET"), "expected OFFSET: %s", capturedQuery)
}

// QueryScalarSlice, QueryScalar, GroupByScalarSlice, GroupByScalar removed.
// Scalar access is now handled by the Selector type (see selector_test.go).

func TestQueryScan_Distinct(t *testing.T) {
	sentinel := fmt.Errorf("stop after capture")
	var capturedQuery string
	drv := &mockDriver{
		dialectName: dialect.Postgres,
		queryFn: func(_ context.Context, query string, _ any, _ any) error {
			capturedQuery = query
			return sentinel
		},
	}

	base := newTestQuery(drv, "users", []string{"id", "name"}, "id", nil, "User")
	base.SetUnique(true)
	base.Ctx.Fields = []string{"name"}

	var names []string
	err := QueryScan(context.Background(), base, &names)
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, capturedQuery, "DISTINCT")
}

func TestQueryGroupBy_WithLimitOffset(t *testing.T) {
	sentinel := fmt.Errorf("stop after capture")
	var capturedQuery string
	drv := &mockDriver{
		dialectName: dialect.SQLite,
		queryFn: func(_ context.Context, query string, _ any, _ any) error {
			capturedQuery = query
			return sentinel
		},
	}

	base := newTestQuery(drv, "users", []string{"id", "name"}, "id", nil, "User")
	limit := 10
	offset := 5
	base.Ctx.Limit = &limit
	base.Ctx.Offset = &offset

	var results []struct{ Name string }
	err := QueryGroupBy(context.Background(), base, []string{"name"}, nil, &results)
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, capturedQuery, "LIMIT")
	assert.Contains(t, capturedQuery, "OFFSET")
}

func TestQueryScan_WithPath(t *testing.T) {
	sentinel := fmt.Errorf("stop after capture")
	var capturedQuery string
	drv := &mockDriver{
		dialectName: dialect.SQLite,
		queryFn: func(_ context.Context, query string, _ any, _ any) error {
			capturedQuery = query
			return sentinel
		},
	}

	base := newTestQuery(drv, "users", []string{"id", "name"}, "id", nil, "User")
	base.Ctx.Fields = []string{"name"}
	base.Path = func(ctx context.Context) (*sql.Selector, error) {
		return sql.Select().From(sql.Table("sub_query")), nil
	}

	var names []string
	err := QueryScan(context.Background(), base, &names)
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, capturedQuery, "sub_query")
}

// TestQueryGroupBy_ModifiersRunLastAndUniqueApplies pins QueryGroupBy to the
// same ordering as QuerySelect: modifiers see (and may override) the finished
// SELECT list, and Unique renders DISTINCT.
func TestQueryGroupBy_ModifiersRunLastAndUniqueApplies(t *testing.T) {
	sentinel := fmt.Errorf("stop after capture")
	var capturedQuery string
	drv := &mockDriver{
		dialectName: dialect.SQLite,
		queryFn: func(_ context.Context, query string, _ any, _ any) error {
			capturedQuery = query
			return sentinel
		},
	}

	base := newTestQuery(drv, "users", []string{"id", "name"}, "id", nil, "User")
	unique := true
	base.Ctx.Unique = &unique
	var seen []string
	base.Modifiers = append(base.Modifiers, func(s *sql.Selector) {
		seen = append(seen, s.SelectedColumns()...)
		s.AppendSelect("MAX(`users`.`age`)")
	})
	count := func(s *sql.Selector) string { return sql.Count("*") }

	var results []struct{ Name string }
	err := QueryGroupBy(context.Background(), base, []string{"name"}, []AggregateFunc{count}, &results)
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, []string{"name", "COUNT(*)"}, seen, "modifier must run after group columns and aggregates are selected")
	assert.Equal(t, "SELECT DISTINCT `name`, COUNT(*), MAX(`users`.`age`) FROM `users` GROUP BY `name`", capturedQuery)
}

func TestQueryGroupBy_WithPath(t *testing.T) {
	sentinel := fmt.Errorf("stop after capture")
	var capturedQuery string
	drv := &mockDriver{
		dialectName: dialect.SQLite,
		queryFn: func(_ context.Context, query string, _ any, _ any) error {
			capturedQuery = query
			return sentinel
		},
	}

	base := newTestQuery(drv, "users", []string{"id"}, "id", nil, "User")
	base.Path = func(ctx context.Context) (*sql.Selector, error) {
		return sql.Select().From(sql.Table("sub_query")), nil
	}

	var results []struct{ Name string }
	err := QueryGroupBy(context.Background(), base, []string{"name"}, nil, &results)
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, capturedQuery, "sub_query")
}

// =============================================================================
// Parity tests: top-level functions vs testQuery methods
// =============================================================================

func TestBuildQueryFrom_WithPath(t *testing.T) {
	drv := &mockDriver{dialectName: dialect.SQLite}
	base := newTestQuery(drv, "users", []string{"id"}, "id", nil, "User")
	base.Path = func(_ context.Context) (*sql.Selector, error) {
		return sql.Select("id").From(sql.Table("parent_query")), nil
	}

	sel, err := BuildQueryFrom(context.Background(), base)
	require.NoError(t, err)
	query, _ := sel.Query()
	assert.Contains(t, query, "parent_query")
}

func TestBuildQueryFrom_PathError(t *testing.T) {
	drv := &mockDriver{dialectName: dialect.SQLite}
	base := newTestQuery(drv, "users", []string{"id"}, "id", nil, "User")
	base.Path = func(_ context.Context) (*sql.Selector, error) {
		return nil, fmt.Errorf("path error")
	}

	_, err := BuildQueryFrom(context.Background(), base)
	assert.EqualError(t, err, "path error")
}

func TestBuildSelectorFrom_WithFields(t *testing.T) {
	drv := &mockDriver{dialectName: dialect.SQLite}
	base := newTestQuery(drv, "users", []string{"id", "name", "email"}, "id", []string{"org_id"}, "User")
	base.Ctx.Fields = []string{"name", "email"}
	base.WithFKs = true

	sel, err := BuildSelectorFrom(context.Background(), base)
	require.NoError(t, err)
	query, _ := sel.Query()

	assert.Contains(t, query, "`id`")
	assert.Contains(t, query, "`name`")
	assert.Contains(t, query, "`email`")
	assert.Contains(t, query, "`org_id`")
}

// TestBuildSelectorFrom_M2OEdge_AmbiguousColumn is a regression test for the case
// where a M2O (FromEdgeOwner) edge traversal produces a JOIN where the FK column
// on the source table shares the same name as the PK on the target table. Without
// table qualification, the outer SELECT would be ambiguous (e.g. "terms_id" appearing
// in both "terms" and the subquery "t1"). The fix is in BuildSelectorFrom: columns
// must be qualified via selector.C() before being passed to selector.Select().
func TestBuildSelectorFrom_M2OEdge_AmbiguousColumn(t *testing.T) {
	// Simulate: sales_order.terms_id → terms.terms_id (FK name = target PK name).
	drv := &mockDriver{dialectName: dialect.Postgres}
	base := newTestQuery(drv, "terms", []string{"terms_id", "name"}, "terms_id", nil, "Terms")
	// The path is what SetPath injects for a M2O (FromEdgeOwner) edge:
	// Neighbors produces SELECT * FROM terms JOIN (SELECT terms_id FROM sales_order WHERE id = ?) AS t1
	//                        ON terms.terms_id = t1.terms_id
	base.Path = func(_ context.Context) (*sql.Selector, error) {
		b := sql.Dialect(dialect.Postgres)
		targetT := b.Table("terms")
		subq := b.Select(targetT.C("terms_id")).
			From(b.Table("sales_order")).
			Where(sql.EQ("id", 42))
		return b.Select().
			From(targetT).
			Join(subq).
			On(targetT.C("terms_id"), subq.C("terms_id")), nil
	}

	sel, err := BuildSelectorFrom(context.Background(), base)
	require.NoError(t, err)
	query, _ := sel.Query()

	// All selected columns must be table-qualified to avoid "ambiguous column reference".
	assert.Contains(t, query, `"terms"."terms_id"`, "terms_id must be qualified to avoid ambiguity")
	assert.Contains(t, query, `"terms"."name"`, "name must be qualified")
	assert.NotContains(t, query, `SELECT "terms_id",`, "unqualified terms_id in SELECT is ambiguous")
}

// TestBuildSelectorFrom_ModifierOrdering pins that modifiers run AFTER the
// default entity-column projection, matching Ent's sqlgraph.query.selector
// ordering. This is load-bearing for aggregate queries: a Modify() callback
// that does `sel.Select("SUM(age)")` must replace the default column list,
// otherwise PostgreSQL rejects the emitted SQL with SQLSTATE 42803 (column
// must appear in GROUP BY or be used in an aggregate). Similarly, a Modify()
// callback that does `sel.AppendSelect(expr)` must add to the defaults rather
// than producing a SELECT with only the extra expression.
func TestBuildSelectorFrom_ModifierOrdering(t *testing.T) {
	t.Run("Select replaces defaults", func(t *testing.T) {
		drv := &mockDriver{dialectName: dialect.Postgres}
		base := newTestQuery(drv, "users", []string{"id", "name", "age"}, "id", nil, "User")
		base.AddModifier(func(s *sql.Selector) {
			s.Select("SUM(age)")
			s.GroupBy("id")
		})

		sel, err := BuildSelectorFrom(context.Background(), base)
		require.NoError(t, err)
		query, _ := sel.Query()

		assert.Contains(t, query, "SUM(age)", "modifier's aggregate expr must survive")
		assert.NotContains(t, query, `"name"`, "default columns must not be selected when modifier replaced selection")
		assert.NotContains(t, query, `"age",`, "default 'age' column must not appear as a separate SELECT item")
		assert.Contains(t, query, "GROUP BY", "modifier's GROUP BY must survive")
	})

	t.Run("AppendSelect adds to defaults", func(t *testing.T) {
		drv := &mockDriver{dialectName: dialect.Postgres}
		base := newTestQuery(drv, "users", []string{"id", "name"}, "id", nil, "User")
		base.AddModifier(func(s *sql.Selector) {
			s.AppendSelect("LOWER(name) AS lname")
		})

		sel, err := BuildSelectorFrom(context.Background(), base)
		require.NoError(t, err)
		query, _ := sel.Query()

		assert.Contains(t, query, `"id"`, "default 'id' column must survive AppendSelect")
		assert.Contains(t, query, `"name"`, "default 'name' column must survive AppendSelect")
		assert.Contains(t, query, "LOWER(name) AS lname", "modifier's appended expr must be present")
	})

	t.Run("Where in modifier still runs", func(t *testing.T) {
		drv := &mockDriver{dialectName: dialect.Postgres}
		base := newTestQuery(drv, "users", []string{"id", "name"}, "id", nil, "User")
		base.AddModifier(func(s *sql.Selector) {
			s.Where(sql.Like(s.C("name"), "A%"))
		})

		sel, err := BuildSelectorFrom(context.Background(), base)
		require.NoError(t, err)
		query, _ := sel.Query()

		assert.Contains(t, query, `"id"`, "defaults still present")
		assert.Contains(t, query, "LIKE", "modifier's predicate applied")
	})
}

func TestMakeQuerySpec_NoPredicates(t *testing.T) {
	base := newTestQuery(nil, "users", []string{"id"}, "id", nil, "User")
	spec := MakeQuerySpec(base, field.TypeInt)
	assert.Nil(t, spec.Predicate)
	assert.Nil(t, spec.Order)
	assert.Nil(t, spec.Modifiers)
	assert.Equal(t, 0, spec.Limit)
	assert.Equal(t, 0, spec.Offset)
}

func TestMakeQuerySpec_WithFields(t *testing.T) {
	base := newTestQuery(nil, "users", []string{"id", "name", "email"}, "id", []string{"org_id"}, "User")
	base.Ctx.Fields = []string{"name", "email"}
	base.WithFKs = true

	spec := MakeQuerySpec(base, field.TypeInt)
	assert.Contains(t, spec.Node.Columns, "id")
	assert.Contains(t, spec.Node.Columns, "name")
	assert.Contains(t, spec.Node.Columns, "email")
	assert.Contains(t, spec.Node.Columns, "org_id")
}

func TestMakeQuerySpec_IDInFields(t *testing.T) {
	base := newTestQuery(nil, "users", []string{"id", "name"}, "id", nil, "User")
	base.Ctx.Fields = []string{"id", "name"}

	spec := MakeQuerySpec(base, field.TypeInt)
	idCount := 0
	for _, c := range spec.Node.Columns {
		if c == "id" {
			idCount++
		}
	}
	assert.Equal(t, 1, idCount)
}

func TestQueryGroupBy_AcceptsQueryReader(t *testing.T) {
	sentinel := fmt.Errorf("query captured")
	var capturedQuery string
	drv := &mockDriver{
		dialectName: dialect.Postgres,
		queryFn: func(_ context.Context, query string, _ any, _ any) error {
			capturedQuery = query
			return sentinel
		},
	}

	base := newTestQuery(drv, "users", []string{"id", "name"}, "id", nil, "User")
	var result []map[string]any
	// Call with QueryReader interface explicitly.
	var qr QueryReader = base
	err := QueryGroupBy(context.Background(), qr, []string{"name"}, nil, &result)
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, capturedQuery, `"name"`)
}

func TestQuerySelect_AcceptsQueryReader(t *testing.T) {
	sentinel := fmt.Errorf("stop after capture")
	var capturedQuery string
	drv := &mockDriver{
		dialectName: dialect.SQLite,
		queryFn: func(_ context.Context, query string, _ any, _ any) error {
			capturedQuery = query
			return sentinel
		},
	}

	base := newTestQuery(drv, "users", []string{"id", "name"}, "id", nil, "User")
	base.Ctx.Fields = []string{"name"}
	var result []string
	var qr QueryReader = base
	err := QuerySelect(context.Background(), qr, nil, &result)
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, capturedQuery, "name")
}

func TestResolvePathFrom(t *testing.T) {
	t.Run("nil_path", func(t *testing.T) {
		base := newTestQuery(nil, "users", nil, "id", nil, "User")
		sel, err := resolvePathFrom(context.Background(), base)
		assert.NoError(t, err)
		assert.Nil(t, sel)
	})

	t.Run("with_path", func(t *testing.T) {
		base := newTestQuery(nil, "users", nil, "id", nil, "User")
		base.Path = func(_ context.Context) (*sql.Selector, error) {
			return sql.Select("id").From(sql.Table("sub")), nil
		}
		sel, err := resolvePathFrom(context.Background(), base)
		assert.NoError(t, err)
		require.NotNil(t, sel)
		q, _ := sel.Query()
		assert.Contains(t, q, "sub")
	})

	t.Run("path_error", func(t *testing.T) {
		base := newTestQuery(nil, "users", nil, "id", nil, "User")
		base.Path = func(_ context.Context) (*sql.Selector, error) {
			return nil, fmt.Errorf("boom")
		}
		_, err := resolvePathFrom(context.Background(), base)
		assert.EqualError(t, err, "boom")
	})
}

func TestExtractID_UUID(t *testing.T) {
	want := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	ns := &sql.NullString{String: want.String(), Valid: true}
	got, err := ExtractID(ns, field.TypeUUID)
	require.NoError(t, err)
	assert.IsType(t, uuid.UUID{}, got)
	assert.Equal(t, want, got)
}

func TestExtractID_UUID_Invalid(t *testing.T) {
	ns := &sql.NullString{String: "not-a-uuid", Valid: true}
	_, err := ExtractID(ns, field.TypeUUID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid UUID")
}

func TestExtractID_String(t *testing.T) {
	ns := &sql.NullString{String: "hello", Valid: true}
	got, err := ExtractID(ns, field.TypeString)
	require.NoError(t, err)
	assert.Equal(t, "hello", got)
}

func TestExtractID_Int(t *testing.T) {
	ni := &sql.NullInt64{Int64: 42, Valid: true}
	got, err := ExtractID(ni, field.TypeInt)
	require.NoError(t, err)
	assert.Equal(t, 42, got) // must be int, not int64 — generated assertion is id.(int)
}

func TestExtractID_Int64(t *testing.T) {
	ni := &sql.NullInt64{Int64: 42, Valid: true}
	got, err := ExtractID(ni, field.TypeInt64)
	require.NoError(t, err)
	assert.Equal(t, int64(42), got) // must be int64 — generated assertion is id.(int64)
}

func TestExtractID_Int8(t *testing.T) {
	ni := &sql.NullInt64{Int64: 42, Valid: true}
	got, err := ExtractID(ni, field.TypeInt8)
	require.NoError(t, err)
	assert.Equal(t, int8(42), got)
}

func TestExtractID_Int16(t *testing.T) {
	ni := &sql.NullInt64{Int64: 42, Valid: true}
	got, err := ExtractID(ni, field.TypeInt16)
	require.NoError(t, err)
	assert.Equal(t, int16(42), got)
}

func TestExtractID_Int32(t *testing.T) {
	ni := &sql.NullInt64{Int64: 42, Valid: true}
	got, err := ExtractID(ni, field.TypeInt32)
	require.NoError(t, err)
	assert.Equal(t, int32(42), got)
}

func TestExtractID_Uint(t *testing.T) {
	ni := &sql.NullInt64{Int64: 42, Valid: true}
	got, err := ExtractID(ni, field.TypeUint)
	require.NoError(t, err)
	assert.Equal(t, uint(42), got)
}

func TestExtractID_Uint8(t *testing.T) {
	ni := &sql.NullInt64{Int64: 42, Valid: true}
	got, err := ExtractID(ni, field.TypeUint8)
	require.NoError(t, err)
	assert.Equal(t, uint8(42), got)
}

func TestExtractID_Uint16(t *testing.T) {
	ni := &sql.NullInt64{Int64: 42, Valid: true}
	got, err := ExtractID(ni, field.TypeUint16)
	require.NoError(t, err)
	assert.Equal(t, uint16(42), got)
}

func TestExtractID_Uint32(t *testing.T) {
	ni := &sql.NullInt64{Int64: 42, Valid: true}
	got, err := ExtractID(ni, field.TypeUint32)
	require.NoError(t, err)
	assert.Equal(t, uint32(42), got)
}

func TestExtractID_Uint64(t *testing.T) {
	ni := &sql.NullInt64{Int64: 42, Valid: true}
	got, err := ExtractID(ni, field.TypeUint64)
	require.NoError(t, err)
	assert.Equal(t, uint64(42), got)
}

func TestExtractID_WrongScanType(t *testing.T) {
	// passing a NullString for an int field must error, not panic
	ns := &sql.NullString{String: "oops", Valid: true}
	_, err := ExtractID(ns, field.TypeInt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "*sql.NullString")
}

// =============================================================================
// ScanWithInterceptors tests
// =============================================================================

// mockInterceptor records calls and delegates to next.
type mockInterceptor struct {
	called bool
	order  *[]string
	name   string
}

func (m *mockInterceptor) Intercept(next Querier) Querier {
	return QuerierFunc(func(ctx context.Context, q Query) (Value, error) {
		m.called = true
		if m.order != nil {
			*m.order = append(*m.order, m.name)
		}
		return next.Query(ctx, q)
	})
}

func TestScanWithInterceptors_NoInterceptors(t *testing.T) {
	called := false
	sqlFn := func(_ context.Context, _ any) error {
		called = true
		return nil
	}
	var v []string
	err := ScanWithInterceptors(context.Background(), nil, nil, sqlFn, &v)
	require.NoError(t, err)
	assert.True(t, called, "sqlFn should be called directly")
}

func TestScanWithInterceptors_WithInterceptors(t *testing.T) {
	var order []string
	i1 := &mockInterceptor{order: &order, name: "i1"}
	i2 := &mockInterceptor{order: &order, name: "i2"}

	sqlFn := func(_ context.Context, _ any) error {
		order = append(order, "sqlFn")
		return nil
	}

	inters := []Interceptor{i1, i2}
	var v []string
	err := ScanWithInterceptors(context.Background(), nil, inters, sqlFn, &v)
	require.NoError(t, err)
	// Interceptors wrap in reverse order, so i1 runs first, then i2, then sqlFn.
	assert.Equal(t, []string{"i1", "i2", "sqlFn"}, order)
}

func TestScanWithInterceptors_ErrorPropagates(t *testing.T) {
	want := fmt.Errorf("scan failed")
	sqlFn := func(_ context.Context, _ any) error {
		return want
	}
	var v []string
	err := ScanWithInterceptors(context.Background(), nil, nil, sqlFn, &v)
	assert.ErrorIs(t, err, want)
}

func TestScanWithInterceptors_ErrorFromInterceptorChain(t *testing.T) {
	want := fmt.Errorf("interceptor blocked")
	sqlFn := func(_ context.Context, _ any) error {
		t.Fatal("sqlFn should not be called when interceptor errors")
		return nil
	}

	inters := []Interceptor{
		InterceptFunc(func(next Querier) Querier {
			return QuerierFunc(func(_ context.Context, _ Query) (Value, error) {
				return nil, want
			})
		}),
	}
	var v []string
	err := ScanWithInterceptors(context.Background(), nil, inters, sqlFn, &v)
	assert.ErrorIs(t, err, want)
}

// =============================================================================
// RunTraversers tests
// =============================================================================

// mockTraverser implements both Interceptor and Traverser.
type mockTraverser struct {
	traverseCalled bool
	traverseErr    error
}

func (m *mockTraverser) Intercept(next Querier) Querier { return next }
func (m *mockTraverser) Traverse(_ context.Context, _ Query) error {
	m.traverseCalled = true
	return m.traverseErr
}

func TestRunTraversers_Empty(t *testing.T) {
	err := RunTraversers(context.Background(), nil, nil)
	assert.NoError(t, err)
}

func TestRunTraversers_NilInterceptor(t *testing.T) {
	inters := []Interceptor{nil}
	err := RunTraversers(context.Background(), nil, inters)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uninitialized interceptor")
}

func TestRunTraversers_NonTraverser(t *testing.T) {
	// A plain interceptor that does NOT implement Traverser — skipped silently.
	inter := InterceptFunc(func(next Querier) Querier { return next })
	err := RunTraversers(context.Background(), nil, []Interceptor{inter})
	assert.NoError(t, err)
}

func TestRunTraversers_TraverserCalled(t *testing.T) {
	trv := &mockTraverser{}
	err := RunTraversers(context.Background(), nil, []Interceptor{trv})
	require.NoError(t, err)
	assert.True(t, trv.traverseCalled)
}

func TestRunTraversers_TraverserError(t *testing.T) {
	want := fmt.Errorf("traverse failed")
	trv := &mockTraverser{traverseErr: want}
	err := RunTraversers(context.Background(), nil, []Interceptor{trv})
	assert.ErrorIs(t, err, want)
}

func TestRunTraversers_MixedInterceptors(t *testing.T) {
	// Mix of traverser and non-traverser — only traversers get called.
	trv := &mockTraverser{}
	plain := InterceptFunc(func(next Querier) Querier { return next })
	err := RunTraversers(context.Background(), nil, []Interceptor{plain, trv})
	require.NoError(t, err)
	assert.True(t, trv.traverseCalled)
}

func TestCloneSlice_EmptyReturnsNil(t *testing.T) {
	assert.Nil(t, CloneSlice([]int(nil)))
	assert.Nil(t, CloneSlice([]int{}))
}

func TestCloneSlice_PopulatedDeepCopy(t *testing.T) {
	src := []int{1, 2, 3}
	got := CloneSlice(src)
	require.Equal(t, src, got)
	got[0] = 99
	assert.Equal(t, 1, src[0], "mutating the clone must not affect the source")
	got = append(got, 4)
	assert.Len(t, src, 3, "appending to the clone must not affect the source")
	_ = got
}
