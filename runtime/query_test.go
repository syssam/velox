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

func TestQueryBaseImplementsQueryReader(t *testing.T) {
	var _ QueryReader = (*QueryBase)(nil)
}

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

func TestNewQueryBase(t *testing.T) {
	q := NewQueryBase(nil, "users", []string{"id", "name"}, "id", []string{"org_id"}, "User")
	assert.Equal(t, "users", q.Table)
	assert.Equal(t, []string{"id", "name"}, q.Columns)
	assert.Equal(t, "id", q.IDColumn)
	assert.Equal(t, []string{"org_id"}, q.FKColumns)
	assert.Equal(t, "User", q.Ctx.Type)
}

func TestQueryBase_Where(t *testing.T) {
	q := NewQueryBase(nil, "users", nil, "id", nil, "User")
	assert.Empty(t, q.Predicates)
	q.Where(func(s *sql.Selector) {})
	assert.Len(t, q.Predicates, 1)
}

func TestQueryBase_LimitOffset(t *testing.T) {
	q := NewQueryBase(nil, "users", nil, "id", nil, "User")
	assert.Nil(t, q.Ctx.Limit)
	assert.Nil(t, q.Ctx.Offset)

	q.SetLimit(10)
	q.SetOffset(20)
	require.NotNil(t, q.Ctx.Limit)
	require.NotNil(t, q.Ctx.Offset)
	assert.Equal(t, 10, *q.Ctx.Limit)
	assert.Equal(t, 20, *q.Ctx.Offset)
}

func TestQueryBase_Order(t *testing.T) {
	q := NewQueryBase(nil, "users", nil, "id", nil, "User")
	q.AddOrder(func(s *sql.Selector) {})
	assert.Len(t, q.Order, 1)
}

func TestQueryBase_Modifier(t *testing.T) {
	q := NewQueryBase(nil, "users", nil, "id", nil, "User")
	q.AddModifier(func(s *sql.Selector) {})
	assert.Len(t, q.Modifiers, 1)
}

func TestQueryBase_Unique(t *testing.T) {
	q := NewQueryBase(nil, "users", nil, "id", nil, "User")
	assert.Nil(t, q.Ctx.Unique)
	q.SetUnique(true)
	require.NotNil(t, q.Ctx.Unique)
	assert.True(t, *q.Ctx.Unique)
}

func TestQueryBase_WithEdgeLoad(t *testing.T) {
	q := NewQueryBase(nil, "users", nil, "id", nil, "User")
	q.WithEdgeLoad("posts", Limit(5))
	require.Len(t, q.Edges, 1)
	assert.Equal(t, "posts", q.Edges[0].Name)
	assert.Len(t, q.Edges[0].Opts, 1)
}

func TestQueryBase_Clone(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		var q *QueryBase
		assert.Nil(t, q.Clone())
	})

	t.Run("full", func(t *testing.T) {
		q := NewQueryBase(nil, "users", []string{"id", "name"}, "id", nil, "User")
		q.Where(func(s *sql.Selector) {})
		q.AddOrder(func(s *sql.Selector) {})
		q.AddModifier(func(s *sql.Selector) {})
		q.WithEdgeLoad("posts")
		q.SetLimit(10)

		clone := q.Clone()
		require.NotNil(t, clone)
		assert.Equal(t, q.Table, clone.Table)
		assert.Equal(t, q.Columns, clone.Columns)
		assert.Len(t, clone.Predicates, 1)
		assert.Len(t, clone.Order, 1)
		assert.Len(t, clone.Modifiers, 1)
		assert.Len(t, clone.Edges, 1)
		require.NotNil(t, clone.Ctx.Limit)
		assert.Equal(t, 10, *clone.Ctx.Limit)

		// Mutating clone should not affect original.
		clone.Predicates = append(clone.Predicates, func(s *sql.Selector) {})
		assert.Len(t, q.Predicates, 1)
		assert.Len(t, clone.Predicates, 2)
	})
}

// TestQueryBase_Clone_PopulatedDeepCopy guards the deep-copy semantics of
// QueryBase.Clone. Mutating the clone's slices must not affect the original.
// Added in SP-9 before the zero-alloc refactor to prevent silent regression.
func TestQueryBase_Clone_PopulatedDeepCopy(t *testing.T) {
	pred := func(*sql.Selector) {}
	ord := func(*sql.Selector) {}
	mod := func(*sql.Selector) {}
	original := &QueryBase{
		Table:      "users",
		Columns:    []string{"id", "name"},
		IDColumn:   "id",
		Ctx:        &QueryContext{Type: "User"},
		Predicates: []func(*sql.Selector){pred},
		Order:      []func(*sql.Selector){ord},
		Modifiers:  []func(*sql.Selector){mod},
		Edges:      []EdgeLoad{{Name: "posts"}},
		Inters:     []Interceptor{InterceptFunc(func(Querier) Querier { return nil })},
	}

	clone := original.Clone()

	// Sanity: the clone is a different *QueryBase but logically equal.
	if clone == original {
		t.Fatal("Clone returned same pointer")
	}
	if len(clone.Predicates) != 1 || len(clone.Order) != 1 || len(clone.Modifiers) != 1 || len(clone.Edges) != 1 || len(clone.Inters) != 1 {
		t.Fatalf("clone lengths wrong: preds=%d order=%d mods=%d edges=%d inters=%d",
			len(clone.Predicates), len(clone.Order), len(clone.Modifiers), len(clone.Edges), len(clone.Inters))
	}

	// Mutating clone slices must not affect original.
	clone.Predicates = append(clone.Predicates, pred)
	clone.Order = append(clone.Order, ord)
	clone.Modifiers = append(clone.Modifiers, mod)
	clone.Edges = append(clone.Edges, EdgeLoad{Name: "comments"})
	clone.Inters = append(clone.Inters, InterceptFunc(func(Querier) Querier { return nil }))

	if len(original.Predicates) != 1 {
		t.Errorf("original.Predicates was mutated: len=%d", len(original.Predicates))
	}
	if len(original.Order) != 1 {
		t.Errorf("original.Order was mutated: len=%d", len(original.Order))
	}
	if len(original.Modifiers) != 1 {
		t.Errorf("original.Modifiers was mutated: len=%d", len(original.Modifiers))
	}
	if len(original.Edges) != 1 {
		t.Errorf("original.Edges was mutated: len=%d", len(original.Edges))
	}
	if len(original.Inters) != 1 {
		t.Errorf("original.Inters was mutated: len=%d", len(original.Inters))
	}
}

// TestQueryBase_Clone_EmptyAllocInvariant locks in the allocation contract
// for cloning an empty QueryBase. Exactly 2 allocations are expected:
// the *QueryBase itself and the *QueryContext copy. Nothing else.
//
// This is a regression guard, not a failing TDD test. Go 1.22+ optimizes
// `append([]X{}, nil...)` to zero allocations when stored into a field,
// so cloning a QueryBase with no predicates/order/modifiers/edges/inters
// already allocates only the 2 expected objects. If that optimization is
// ever defeated — by a Go compiler regression, or by a velox refactor
// that introduces non-nil empty slices or extra indirection — this test
// fails loud.
//
// Uses `==` not `<=` so both directions are caught: any drift from the
// contract (even favorable) is a signal worth investigating.
func TestQueryBase_Clone_EmptyAllocInvariant(t *testing.T) {
	base := &QueryBase{
		Table:    "users",
		IDColumn: "id",
		Ctx:      &QueryContext{Type: "User"},
	}
	allocs := testing.AllocsPerRun(100, func() {
		_ = base.Clone()
	})
	const want = 2.0 // *QueryBase + *QueryContext
	switch {
	case allocs > want:
		t.Errorf("QueryBase.Clone empty-alloc regression: got %v allocs, want %v", allocs, want)
	case allocs < want:
		t.Errorf("QueryBase.Clone empty-alloc favorable drift: got %v allocs, want %v — update the want constant and docstring if this is intentional", allocs, want)
	}
}

func TestQueryBase_QuerySpec(t *testing.T) {
	q := NewQueryBase(nil, "users", []string{"id", "name", "email"}, "id", []string{"org_id"}, "User")
	q.SetLimit(10)
	q.SetOffset(5)
	q.SetUnique(true)
	q.Where(func(s *sql.Selector) {})
	q.AddOrder(func(s *sql.Selector) {})
	q.AddModifier(func(s *sql.Selector) {})

	spec := q.QuerySpec(field.TypeInt)
	assert.Equal(t, "users", spec.Node.Table)
	assert.Equal(t, "id", spec.Node.ID.Column)
	assert.Equal(t, field.TypeInt, spec.Node.ID.Type)
	assert.Equal(t, 10, spec.Limit)
	assert.Equal(t, 5, spec.Offset)
	assert.True(t, spec.Unique)
	assert.NotNil(t, spec.Predicate)
	assert.NotNil(t, spec.Order)
	assert.Len(t, spec.Modifiers, 1)
}

func TestQueryBase_QuerySpec_WithFields(t *testing.T) {
	q := NewQueryBase(nil, "users", []string{"id", "name", "email"}, "id", []string{"org_id"}, "User")
	q.Ctx.Fields = []string{"name", "email"}
	q.WithFKs = true

	spec := q.QuerySpec(field.TypeInt)
	// Should include id + name + email + org_id (FK).
	assert.Contains(t, spec.Node.Columns, "id")
	assert.Contains(t, spec.Node.Columns, "name")
	assert.Contains(t, spec.Node.Columns, "email")
	assert.Contains(t, spec.Node.Columns, "org_id")
}

func TestQueryBase_QuerySpec_IDInFields(t *testing.T) {
	q := NewQueryBase(nil, "users", []string{"id", "name"}, "id", nil, "User")
	q.Ctx.Fields = []string{"id", "name"}

	spec := q.QuerySpec(field.TypeInt)
	// ID should appear only once.
	idCount := 0
	for _, c := range spec.Node.Columns {
		if c == "id" {
			idCount++
		}
	}
	assert.Equal(t, 1, idCount)
}

func TestQueryBase_QuerySpec_NoPredicates(t *testing.T) {
	q := NewQueryBase(nil, "users", []string{"id"}, "id", nil, "User")
	spec := q.QuerySpec(field.TypeInt)
	assert.Nil(t, spec.Predicate)
	assert.Nil(t, spec.Order)
	assert.Nil(t, spec.Modifiers)
	assert.Equal(t, 0, spec.Limit)
	assert.Equal(t, 0, spec.Offset)
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

	base := NewQueryBase(drv, "users", []string{"id", "name"}, "id", nil, "User")
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

	base := NewQueryBase(drv, "users", []string{"id", "name"}, "id", nil, "User")
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
	base := NewQueryBase(drv, "users", []string{"id", "name", "age"}, "id", nil, "User")
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

	base := NewQueryBase(drv, "users", []string{"id", "name"}, "id", nil, "User")
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

func TestQueryBase_WithNamedEdgeLoad(t *testing.T) {
	drv := &mockDriver{dialectName: dialect.SQLite}
	base := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")

	base.WithNamedEdgeLoad("my_posts", "posts", Limit(5))
	require.Len(t, base.Edges, 1)
	assert.Equal(t, "posts", base.Edges[0].Name)
	assert.Equal(t, "my_posts", base.Edges[0].Label)
}

func TestQueryBase_ForUpdate(t *testing.T) {
	drv := &mockDriver{dialectName: dialect.Postgres}
	base := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")

	base.ForUpdate()
	require.NotNil(t, base.Ctx.Unique)
	assert.False(t, *base.Ctx.Unique)
	assert.Len(t, base.Modifiers, 1)
}

func TestQueryBase_ForShare(t *testing.T) {
	drv := &mockDriver{dialectName: dialect.Postgres}
	base := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")

	base.ForShare()
	require.NotNil(t, base.Ctx.Unique)
	assert.False(t, *base.Ctx.Unique)
	assert.Len(t, base.Modifiers, 1)
}

func TestQueryBase_ForNoKeyUpdate(t *testing.T) {
	drv := &mockDriver{dialectName: dialect.Postgres}
	base := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")

	base.ForNoKeyUpdate()
	require.NotNil(t, base.Ctx.Unique)
	assert.False(t, *base.Ctx.Unique)
	assert.Len(t, base.Modifiers, 1)
}

func TestQueryBase_ForKeyShare(t *testing.T) {
	drv := &mockDriver{dialectName: dialect.Postgres}
	base := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")

	base.ForKeyShare()
	require.NotNil(t, base.Ctx.Unique)
	assert.False(t, *base.Ctx.Unique)
	assert.Len(t, base.Modifiers, 1)
}

func TestQueryBase_BuildQuery(t *testing.T) {
	t.Run("simple", func(t *testing.T) {
		drv := &mockDriver{dialectName: dialect.SQLite}
		base := NewQueryBase(drv, "users", []string{"id", "name"}, "id", nil, "User")
		base.Where(func(s *sql.Selector) {
			s.Where(sql.EQ(s.C("name"), "Alice"))
		})
		limit := 10
		offset := 5
		base.Ctx.Limit = &limit
		base.Ctx.Offset = &offset
		base.AddOrder(func(s *sql.Selector) { s.OrderBy(sql.Asc(s.C("name"))) })

		selector, err := base.BuildQuery(context.Background())
		require.NoError(t, err)
		require.NotNil(t, selector)

		query, _ := selector.Query()
		assert.Contains(t, query, "users")
		assert.Contains(t, query, "LIMIT")
		assert.Contains(t, query, "OFFSET")
	})

	t.Run("with_path", func(t *testing.T) {
		drv := &mockDriver{dialectName: dialect.SQLite}
		base := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")
		base.Path = func(ctx context.Context) (*sql.Selector, error) {
			return sql.Select("id").From(sql.Table("parent_query")), nil
		}

		selector, err := base.BuildQuery(context.Background())
		require.NoError(t, err)
		query, _ := selector.Query()
		assert.Contains(t, query, "parent_query")
	})

	t.Run("path_error", func(t *testing.T) {
		drv := &mockDriver{dialectName: dialect.SQLite}
		base := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")
		base.Path = func(ctx context.Context) (*sql.Selector, error) {
			return nil, fmt.Errorf("path error")
		}

		_, err := base.BuildQuery(context.Background())
		assert.EqualError(t, err, "path error")
	})
}

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

	base := NewQueryBase(drv, "users", []string{"id", "name"}, "id", nil, "User")
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

	base := NewQueryBase(drv, "users", []string{"id", "name"}, "id", nil, "User")
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

	base := NewQueryBase(drv, "users", []string{"id", "name"}, "id", nil, "User")
	base.Ctx.Fields = []string{"name"}
	base.Path = func(ctx context.Context) (*sql.Selector, error) {
		return sql.Select().From(sql.Table("sub_query")), nil
	}

	var names []string
	err := QueryScan(context.Background(), base, &names)
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, capturedQuery, "sub_query")
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

	base := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")
	base.Path = func(ctx context.Context) (*sql.Selector, error) {
		return sql.Select().From(sql.Table("sub_query")), nil
	}

	var results []struct{ Name string }
	err := QueryGroupBy(context.Background(), base, []string{"name"}, nil, &results)
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, capturedQuery, "sub_query")
}

// =============================================================================
// Parity tests: top-level functions vs QueryBase methods
// =============================================================================

func TestBuildQueryFrom_ParityWithMethod(t *testing.T) {
	drv := &mockDriver{dialectName: dialect.SQLite}
	base := NewQueryBase(drv, "users", []string{"id", "name"}, "id", nil, "User")
	base.Where(func(s *sql.Selector) {
		s.Where(sql.EQ(s.C("name"), "Alice"))
	})
	base.SetLimit(10)
	base.SetOffset(5)
	base.AddOrder(func(s *sql.Selector) { s.OrderBy(sql.Asc(s.C("name"))) })
	base.AddModifier(func(s *sql.Selector) {
		// no-op modifier for parity check
	})

	ctx := context.Background()

	methodSel, err := base.BuildQuery(ctx)
	require.NoError(t, err)
	methodSQL, methodArgs := methodSel.Query()

	funcSel, err := BuildQueryFrom(ctx, base)
	require.NoError(t, err)
	funcSQL, funcArgs := funcSel.Query()

	assert.Equal(t, methodSQL, funcSQL, "SQL mismatch between method and function")
	assert.Equal(t, methodArgs, funcArgs, "args mismatch between method and function")
}

func TestBuildQueryFrom_WithPath(t *testing.T) {
	drv := &mockDriver{dialectName: dialect.SQLite}
	base := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")
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
	base := NewQueryBase(drv, "users", []string{"id"}, "id", nil, "User")
	base.Path = func(_ context.Context) (*sql.Selector, error) {
		return nil, fmt.Errorf("path error")
	}

	_, err := BuildQueryFrom(context.Background(), base)
	assert.EqualError(t, err, "path error")
}

func TestBuildSelectorFrom_ParityWithMethod(t *testing.T) {
	drv := &mockDriver{dialectName: dialect.Postgres}
	base := NewQueryBase(drv, "users", []string{"id", "name", "email"}, "id", []string{"org_id"}, "User")
	base.Where(func(s *sql.Selector) {
		s.Where(sql.EQ(s.C("name"), "Bob"))
	})
	base.SetLimit(20)
	base.SetUnique(true)
	base.WithFKs = true

	ctx := context.Background()

	methodSel, err := base.BuildSelector(ctx)
	require.NoError(t, err)
	methodSQL, methodArgs := methodSel.Query()

	funcSel, err := BuildSelectorFrom(ctx, base)
	require.NoError(t, err)
	funcSQL, funcArgs := funcSel.Query()

	assert.Equal(t, methodSQL, funcSQL, "SQL mismatch between method and function")
	assert.Equal(t, methodArgs, funcArgs, "args mismatch between method and function")
}

func TestBuildSelectorFrom_WithFields(t *testing.T) {
	drv := &mockDriver{dialectName: dialect.SQLite}
	base := NewQueryBase(drv, "users", []string{"id", "name", "email"}, "id", []string{"org_id"}, "User")
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

func TestMakeQuerySpec_ParityWithMethod(t *testing.T) {
	base := NewQueryBase(nil, "users", []string{"id", "name", "email"}, "id", []string{"org_id"}, "User")
	base.SetLimit(10)
	base.SetOffset(5)
	base.SetUnique(true)
	base.Where(func(s *sql.Selector) {})
	base.AddOrder(func(s *sql.Selector) {})
	base.AddModifier(func(s *sql.Selector) {})
	base.WithFKs = true

	methodSpec := base.QuerySpec(field.TypeInt)
	funcSpec := MakeQuerySpec(base, field.TypeInt)

	assert.Equal(t, methodSpec.Node.Table, funcSpec.Node.Table)
	assert.Equal(t, methodSpec.Node.ID.Column, funcSpec.Node.ID.Column)
	assert.Equal(t, methodSpec.Node.ID.Type, funcSpec.Node.ID.Type)
	assert.Equal(t, methodSpec.Node.Columns, funcSpec.Node.Columns)
	assert.Equal(t, methodSpec.Limit, funcSpec.Limit)
	assert.Equal(t, methodSpec.Offset, funcSpec.Offset)
	assert.Equal(t, methodSpec.Unique, funcSpec.Unique)
	assert.Equal(t, methodSpec.Predicate != nil, funcSpec.Predicate != nil)
	assert.Equal(t, methodSpec.Order != nil, funcSpec.Order != nil)
	assert.Equal(t, len(methodSpec.Modifiers), len(funcSpec.Modifiers))
}

func TestMakeQuerySpec_NoPredicates(t *testing.T) {
	base := NewQueryBase(nil, "users", []string{"id"}, "id", nil, "User")
	spec := MakeQuerySpec(base, field.TypeInt)
	assert.Nil(t, spec.Predicate)
	assert.Nil(t, spec.Order)
	assert.Nil(t, spec.Modifiers)
	assert.Equal(t, 0, spec.Limit)
	assert.Equal(t, 0, spec.Offset)
}

func TestMakeQuerySpec_WithFields(t *testing.T) {
	base := NewQueryBase(nil, "users", []string{"id", "name", "email"}, "id", []string{"org_id"}, "User")
	base.Ctx.Fields = []string{"name", "email"}
	base.WithFKs = true

	spec := MakeQuerySpec(base, field.TypeInt)
	assert.Contains(t, spec.Node.Columns, "id")
	assert.Contains(t, spec.Node.Columns, "name")
	assert.Contains(t, spec.Node.Columns, "email")
	assert.Contains(t, spec.Node.Columns, "org_id")
}

func TestMakeQuerySpec_IDInFields(t *testing.T) {
	base := NewQueryBase(nil, "users", []string{"id", "name"}, "id", nil, "User")
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

	base := NewQueryBase(drv, "users", []string{"id", "name"}, "id", nil, "User")
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

	base := NewQueryBase(drv, "users", []string{"id", "name"}, "id", nil, "User")
	base.Ctx.Fields = []string{"name"}
	var result []string
	var qr QueryReader = base
	err := QuerySelect(context.Background(), qr, nil, &result)
	require.ErrorIs(t, err, sentinel)
	assert.Contains(t, capturedQuery, "name")
}

func TestResolvePathFrom(t *testing.T) {
	t.Run("nil_path", func(t *testing.T) {
		base := NewQueryBase(nil, "users", nil, "id", nil, "User")
		sel, err := resolvePathFrom(context.Background(), base)
		assert.NoError(t, err)
		assert.Nil(t, sel)
	})

	t.Run("with_path", func(t *testing.T) {
		base := NewQueryBase(nil, "users", nil, "id", nil, "User")
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
		base := NewQueryBase(nil, "users", nil, "id", nil, "User")
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
	assert.Equal(t, 42, got)
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
