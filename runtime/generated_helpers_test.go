package runtime

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/dialect/sql/sqlgraph"
	"github.com/syssam/velox/schema/field"
)

// Tests for runtime helpers that only generated code calls. Their behavior is
// covered end to end by tests/integration, which is a different package, so
// without these the runtime package's own coverage does not see them.

func TestResolveNode(t *testing.T) {
	notFound := func(context.Context, any) (any, error) { return nil, NewNotFoundError("x") }
	mismatch := func(context.Context, any) (any, error) {
		return nil, fmt.Errorf("x: %w", ErrNodeIDTypeMismatch)
	}
	found := func(v string) func(context.Context, any) (any, error) {
		return func(context.Context, any) (any, error) { return v, nil }
	}
	register := func(t *testing.T, resolvers map[string]NodeResolver) {
		t.Helper()
		saved := NodeResolvers()
		nodeMu.Lock()
		nodeRegistry = resolvers
		nodeMu.Unlock()
		t.Cleanup(func() {
			nodeMu.Lock()
			nodeRegistry = saved
			nodeMu.Unlock()
		})
	}
	ctx := context.Background()

	t.Run("one match; not-found and id-type mismatch are skipped", func(t *testing.T) {
		register(t, map[string]NodeResolver{
			"a": {Type: "A", Resolve: notFound},
			"b": {Type: "B", Resolve: mismatch},
			"c": {Type: "C", Resolve: found("c-node")},
		})
		node, ok, err := ResolveNode(ctx, 1)
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "c-node", node)
	})

	t.Run("no match", func(t *testing.T) {
		register(t, map[string]NodeResolver{"a": {Type: "A", Resolve: notFound}})
		node, ok, err := ResolveNode(ctx, 1)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, node)
	})

	t.Run("two matches are ambiguous and name both types", func(t *testing.T) {
		register(t, map[string]NodeResolver{
			"users": {Type: "User", Resolve: found("u")},
			"posts": {Type: "Post", Resolve: found("p")},
		})
		node, ok, err := ResolveNode(ctx, 7)
		require.ErrorIs(t, err, ErrAmbiguousNodeID)
		assert.False(t, ok)
		assert.Nil(t, node)
		// Resolvers are probed in table order, so the message is stable.
		assert.Contains(t, err.Error(), "id 7 is a Post and a User")
	})

	t.Run("other errors abort", func(t *testing.T) {
		boom := errors.New("connection reset")
		register(t, map[string]NodeResolver{
			"a": {Type: "A", Resolve: func(context.Context, any) (any, error) { return nil, boom }},
			"b": {Type: "B", Resolve: found("b")},
		})
		_, ok, err := ResolveNode(ctx, 1)
		require.ErrorIs(t, err, boom)
		assert.False(t, ok)
	})
}

func TestIsNodeIDTypeMismatch(t *testing.T) {
	assert.True(t, IsNodeIDTypeMismatch(fmt.Errorf("user: %w", ErrNodeIDTypeMismatch)))
	assert.False(t, IsNodeIDTypeMismatch(NewNotFoundError("user")))
	assert.False(t, IsNodeIDTypeMismatch(nil))
}

func TestEdgeSchemaDefaults(t *testing.T) {
	const table = "test_edge_schema_defaults"
	t.Cleanup(func() {
		edgeDefaultsMu.Lock()
		delete(edgeDefaults, table)
		edgeDefaultsMu.Unlock()
	})

	assert.Nil(t, EdgeSchemaDefaults(table), "unregistered table has no defaults")

	calls := 0
	RegisterEdgeSchemaDefaults(table, func() []*sqlgraph.FieldSpec {
		calls++
		return []*sqlgraph.FieldSpec{{Column: "joined_at", Type: field.TypeTime, Value: calls}}
	})
	first := EdgeSchemaDefaults(table)
	second := EdgeSchemaDefaults(table)
	require.Len(t, first, 1)
	assert.Equal(t, "joined_at", first[0].Column)
	// Built per call: a time.Now default must not be frozen at registration.
	assert.Equal(t, 1, first[0].Value)
	assert.Equal(t, 2, second[0].Value)
}

// uniqueViolation looks like a Postgres unique violation to sqlgraph.
type uniqueViolation struct{}

func (uniqueViolation) Error() string    { return "duplicate key value violates unique constraint" }
func (uniqueViolation) SQLState() string { return "23505" }

func TestMayWrapConstraintError(t *testing.T) {
	wrapped := MayWrapConstraintError(uniqueViolation{})
	assert.True(t, velox.IsConstraintError(wrapped))
	assert.ErrorIs(t, wrapped, uniqueViolation{}, "the driver error stays in the chain")

	other := errors.New("connection reset")
	assert.Same(t, other, MayWrapConstraintError(other), "non-constraint errors pass through unchanged")
}

func TestIDScanValues(t *testing.T) {
	for _, ft := range []field.Type{field.TypeString, field.TypeUUID} {
		v := IDScanValues(ft)
		require.Len(t, v, 1)
		assert.IsType(t, new(stdsql.NullString), v[0], ft.String())
	}
	for _, ft := range []field.Type{field.TypeInt, field.TypeInt64, field.TypeUint64} {
		v := IDScanValues(ft)
		require.Len(t, v, 1)
		assert.IsType(t, new(stdsql.NullInt64), v[0], ft.String())
	}
	// Fresh values per call: rows must not share scan targets.
	assert.NotSame(t, IDScanValues(field.TypeInt)[0], IDScanValues(field.TypeInt)[0])
}

func TestNewLoadConfig(t *testing.T) {
	assert.Equal(t, &LoadConfig{}, NewLoadConfig())

	c := NewLoadConfig(
		func(c *LoadConfig) { c.Fields = append(c.Fields, "name") },
		func(c *LoadConfig) { c.Fields = append(c.Fields, "email") },
	)
	assert.Equal(t, []string{"name", "email"}, c.Fields, "options apply in order")
}

func TestSelector_AggregateFns(t *testing.T) {
	s := NewSelector("User", nil, nil)
	assert.Empty(t, s.Fns())
	count := func(*sql.Selector) string { return sql.Count("*") }
	sum := func(*sql.Selector) string { return sql.Sum("age") }
	s.AppendFns(count, sum)
	s.AppendFns(func(*sql.Selector) string { return sql.Max("age") })
	assert.Len(t, s.Fns(), 3)
}

func TestSelector_NumericX(t *testing.T) {
	ctx := context.Background()
	scanInts := func(vals ...int) *Selector {
		return newTestSelector(t, func(_ context.Context, v any) error {
			*v.(*[]int) = vals
			return nil
		})
	}
	scanFloats := func(vals ...float64) *Selector {
		return newTestSelector(t, func(_ context.Context, v any) error {
			*v.(*[]float64) = vals
			return nil
		})
	}

	assert.Equal(t, []int{1, 2}, scanInts(1, 2).IntsX(ctx))
	assert.Equal(t, 5, scanInts(5).IntX(ctx))
	assert.Equal(t, []float64{1.5}, scanFloats(1.5).Float64sX(ctx))
	assert.InDelta(t, 2.5, scanFloats(2.5).Float64X(ctx), 0)

	_, err := scanInts().Int(ctx)
	assert.True(t, IsNotFound(err))
	_, err = scanInts(1, 2).Int(ctx)
	assert.True(t, IsNotSingular(err))
	_, err = scanFloats().Float64(ctx)
	assert.True(t, IsNotFound(err))
	_, err = scanFloats(1, 2).Float64(ctx)
	assert.True(t, IsNotSingular(err))

	assert.Panics(t, func() { scanInts().IntX(ctx) })
	assert.Panics(t, func() { scanFloats().Float64X(ctx) })
	boom := newTestSelector(t, func(context.Context, any) error { return errors.New("boom") })
	assert.Panics(t, func() { boom.IntsX(ctx) })
	assert.Panics(t, func() { boom.Float64sX(ctx) })
}

// deleteDriver records the DELETE it is given and reports rows affected.
type deleteDriver struct {
	mockDriver
	query    string
	args     []any
	affected int64
	err      error
}

func (d *deleteDriver) Exec(_ context.Context, query string, args, v any) error {
	if d.err != nil {
		return d.err
	}
	d.query = query
	d.args, _ = args.([]any)
	*v.(*stdsql.Result) = driverResult(d.affected)
	return nil
}

type driverResult int64

func (r driverResult) LastInsertId() (int64, error) { return 0, nil }
func (r driverResult) RowsAffected() (int64, error) { return int64(r), nil }

func TestDeleteNodes(t *testing.T) {
	ctx := context.Background()

	t.Run("predicates and schema", func(t *testing.T) {
		drv := &deleteDriver{mockDriver: mockDriver{dialectName: dialect.Postgres}, affected: 3}
		n, err := DeleteNodes(ctx, &DeleterBase{
			Driver:   drv,
			Table:    "users",
			IDColumn: "id",
			IDType:   field.TypeInt,
			Schema:   "app",
			Predicates: []func(*sql.Selector){
				func(s *sql.Selector) { s.Where(sql.EQ(s.C("name"), "a")) },
				func(s *sql.Selector) { s.Where(sql.GT(s.C("age"), 30)) },
			},
		})
		require.NoError(t, err)
		assert.Equal(t, 3, n)
		assert.Equal(t, `DELETE FROM "app"."users" WHERE "app"."users"."name" = $1 AND "app"."users"."age" > $2`, drv.query)
		assert.Equal(t, []any{"a", 30}, drv.args)
	})

	t.Run("no predicates deletes the table", func(t *testing.T) {
		drv := &deleteDriver{mockDriver: mockDriver{dialectName: dialect.SQLite}}
		_, err := DeleteNodes(ctx, &DeleterBase{Driver: drv, Table: "users", IDColumn: "id", IDType: field.TypeInt})
		require.NoError(t, err)
		assert.Equal(t, "DELETE FROM `users`", drv.query)
	})

	t.Run("constraint errors are wrapped", func(t *testing.T) {
		drv := &deleteDriver{mockDriver: mockDriver{dialectName: dialect.Postgres}, err: uniqueViolation{}}
		_, err := DeleteNodes(ctx, &DeleterBase{Driver: drv, Table: "users", IDColumn: "id", IDType: field.TypeInt})
		assert.True(t, velox.IsConstraintError(err))
	})
}
