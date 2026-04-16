package gqlrelay

import (
	"context"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/dialect/sql"
)

func TestValidateFirstLast(t *testing.T) {
	intPtr := func(v int) *int { return &v }

	tests := []struct {
		name    string
		first   *int
		last    *int
		wantErr error
	}{
		{
			name:    "nil/nil OK",
			first:   nil,
			last:    nil,
			wantErr: nil,
		},
		{
			name:    "negative first errors",
			first:   intPtr(-1),
			last:    nil,
			wantErr: ErrInvalidPagination,
		},
		{
			name:    "negative last errors",
			first:   nil,
			last:    intPtr(-1),
			wantErr: ErrInvalidPagination,
		},
		{
			name:    "both set errors",
			first:   intPtr(10),
			last:    intPtr(10),
			wantErr: ErrInvalidPagination,
		},
		{
			name:    "over-limit first errors",
			first:   intPtr(MaxPaginationLimit + 1),
			last:    nil,
			wantErr: ErrPaginationLimitExceeded,
		},
		{
			name:    "over-limit last errors",
			first:   nil,
			last:    intPtr(MaxPaginationLimit + 1),
			wantErr: ErrPaginationLimitExceeded,
		},
		{
			name:    "valid first",
			first:   intPtr(10),
			last:    nil,
			wantErr: nil,
		},
		{
			name:    "valid last",
			first:   nil,
			last:    intPtr(10),
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFirstLast(tt.first, tt.last)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPaginateLimit(t *testing.T) {
	intPtr := func(v int) *int { return &v }

	tests := []struct {
		name  string
		first *int
		last  *int
		want  int
	}{
		{
			name:  "first=10 returns 11",
			first: intPtr(10),
			last:  nil,
			want:  11,
		},
		{
			name:  "last=5 returns 6",
			first: nil,
			last:  intPtr(5),
			want:  6,
		},
		{
			name:  "both nil returns 0",
			first: nil,
			last:  nil,
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PaginateLimit(tt.first, tt.last)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHasCollectedField_NilContext(t *testing.T) {
	// When there is no GraphQL field context, HasCollectedField returns true
	// (assumes field is present when outside GraphQL context).
	ctx := context.Background()
	assert.True(t, HasCollectedField(ctx, "edges"))
	assert.True(t, HasCollectedField(ctx, "edges", "node"))
}

func TestCollectedField_NilContext(t *testing.T) {
	// CollectedField returns nil when there is no GraphQL field context.
	ctx := context.Background()
	assert.Nil(t, CollectedField(ctx, "edges"))
}

func TestCursorsPredicate_AllDirections(t *testing.T) {
	after := &Cursor{ID: 1, Value: "alice"}
	before := &Cursor{ID: 10, Value: "zara"}

	tests := []struct {
		name      string
		after     *Cursor
		before    *Cursor
		direction OrderDirection
		wantLen   int
	}{
		{
			name:      "after only, ASC",
			after:     after,
			before:    nil,
			direction: OrderDirectionAsc,
			wantLen:   1,
		},
		{
			name:      "after only, DESC",
			after:     after,
			before:    nil,
			direction: OrderDirectionDesc,
			wantLen:   1,
		},
		{
			name:      "before only, ASC",
			after:     nil,
			before:    before,
			direction: OrderDirectionAsc,
			wantLen:   1,
		},
		{
			name:      "before only, DESC",
			after:     nil,
			before:    before,
			direction: OrderDirectionDesc,
			wantLen:   1,
		},
		{
			name:      "both cursors, ASC",
			after:     after,
			before:    before,
			direction: OrderDirectionAsc,
			wantLen:   2,
		},
		{
			name:      "both cursors, DESC",
			after:     after,
			before:    before,
			direction: OrderDirectionDesc,
			wantLen:   2,
		},
		{
			name:      "no cursors",
			after:     nil,
			before:    nil,
			direction: OrderDirectionAsc,
			wantLen:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preds := CursorsPredicate(tt.after, tt.before, "id", "name", tt.direction)
			assert.Len(t, preds, tt.wantLen)
			// Verify each predicate is callable (non-nil).
			for _, pred := range preds {
				assert.NotNil(t, pred)
			}
		})
	}
}

// closures actually produce valid SQL when applied to a selector.
func TestCursorsPredicate_ExecutePredicates(t *testing.T) {
	tests := []struct {
		name      string
		after     *Cursor
		before    *Cursor
		direction OrderDirection
		wantSQL   []string
	}{
		{
			name:      "after ASC executes CompositeGT",
			after:     &Cursor{ID: 1, Value: "alice"},
			before:    nil,
			direction: OrderDirectionAsc,
			wantSQL:   []string{"name", "id"},
		},
		{
			name:      "after DESC executes CompositeLT",
			after:     &Cursor{ID: 1, Value: "alice"},
			before:    nil,
			direction: OrderDirectionDesc,
			wantSQL:   []string{"name", "id"},
		},
		{
			name:      "before ASC executes CompositeLT",
			after:     nil,
			before:    &Cursor{ID: 10, Value: "zara"},
			direction: OrderDirectionAsc,
			wantSQL:   []string{"name", "id"},
		},
		{
			name:      "before DESC executes CompositeGT",
			after:     nil,
			before:    &Cursor{ID: 10, Value: "zara"},
			direction: OrderDirectionDesc,
			wantSQL:   []string{"name", "id"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preds := CursorsPredicate(tt.after, tt.before, "id", "name", tt.direction)
			require.Len(t, preds, 1)

			// Apply the predicate to a real selector to cover the closure body.
			s := sql.Dialect("postgres").Select("*").From(sql.Table("users"))
			preds[0](s)
			query, _ := s.Query()
			for _, want := range tt.wantSQL {
				assert.Contains(t, query, want, "predicate SQL should reference %q", want)
			}
		})
	}
}

// TestCursorsPredicate_BothCursors_ExecutePredicates covers both predicate closures
// when after and before are both non-nil.
func TestCursorsPredicate_BothCursors_ExecutePredicates(t *testing.T) {
	after := &Cursor{ID: 1, Value: "alice"}
	before := &Cursor{ID: 10, Value: "zara"}

	for _, dir := range []OrderDirection{OrderDirectionAsc, OrderDirectionDesc} {
		t.Run(string(dir), func(t *testing.T) {
			preds := CursorsPredicate(after, before, "id", "name", dir)
			require.Len(t, preds, 2)
			s := sql.Dialect("postgres").Select("*").From(sql.Table("users"))
			for _, pred := range preds {
				pred(s)
			}
			query, _ := s.Query()
			assert.Contains(t, query, "name")
			assert.Contains(t, query, "id")
		})
	}
}

func TestCollectedField_WithOperationContext(t *testing.T) {
	// Build a minimal OperationContext with field selections
	// so CollectFields can resolve them.
	nodeField := &ast.Field{Alias: "node", Name: "node"}
	edgesField := &ast.Field{
		Alias:        "edges",
		Name:         "edges",
		SelectionSet: ast.SelectionSet{nodeField},
	}

	fc := &graphql.FieldContext{
		Field: graphql.CollectedField{
			Field: &ast.Field{
				Alias: "users",
				Name:  "users",
			},
			Selections: ast.SelectionSet{edgesField},
		},
	}
	oc := &graphql.OperationContext{}
	ctx := graphql.WithOperationContext(context.Background(), oc)
	ctx = graphql.WithFieldContext(ctx, fc)

	// CollectFields on OperationContext with field selections.
	// The selections contain edgesField, so the walk should match "edges".
	result := CollectedField(ctx, "edges")
	assert.NotNil(t, result, "expected non-nil when edges field is in selections")
	assert.Equal(t, "edges", result.Alias)

	// Walk nested path: edges -> node
	nodeResult := CollectedField(ctx, "edges", "node")
	assert.NotNil(t, nodeResult, "expected non-nil for nested node field")
	assert.Equal(t, "node", nodeResult.Alias)

	// Non-existent path returns nil
	missingResult := CollectedField(ctx, "nonexistent")
	assert.Nil(t, missingResult, "expected nil for non-existent field")
}

func TestHasCollectedField_WithFieldContext(t *testing.T) {
	edgesField := &ast.Field{
		Alias: "edges",
		Name:  "edges",
	}
	fc := &graphql.FieldContext{
		Field: graphql.CollectedField{
			Field:      &ast.Field{Alias: "users", Name: "users"},
			Selections: ast.SelectionSet{edgesField},
		},
	}
	oc := &graphql.OperationContext{}
	ctx := graphql.WithOperationContext(context.Background(), oc)
	ctx = graphql.WithFieldContext(ctx, fc)

	assert.True(t, HasCollectedField(ctx, "edges"))
	assert.False(t, HasCollectedField(ctx, "nonexistent"))
}

func TestDirectionOrderTerm(t *testing.T) {
	// DirectionOrderTerm is a thin wrapper around OrderDirection.OrderTermOption.
	ascOpt := DirectionOrderTerm(OrderDirectionAsc)
	assert.NotNil(t, ascOpt)

	descOpt := DirectionOrderTerm(OrderDirectionDesc)
	assert.NotNil(t, descOpt)
}

// TestAppendIf removed: AppendIf was deleted from the package.

// TestAppendSlice removed: AppendSlice was deleted from the package.

// TestAppendBool removed: AppendBool was deleted from the package.

// TestCursorsPredicate_IDOnly covers the ID-only cursor branches (field == "").
func TestCursorsPredicate_IDOnly_ExecutePredicates(t *testing.T) {
	tests := []struct {
		name      string
		after     *Cursor
		before    *Cursor
		direction OrderDirection
		wantOp    string // substring expected in SQL
	}{
		{
			name:      "after ASC ID-only uses GT",
			after:     &Cursor{ID: 5},
			before:    nil,
			direction: OrderDirectionAsc,
			wantOp:    ">",
		},
		{
			name:      "after DESC ID-only uses LT",
			after:     &Cursor{ID: 5},
			before:    nil,
			direction: OrderDirectionDesc,
			wantOp:    "<",
		},
		{
			name:      "before ASC ID-only uses LT",
			after:     nil,
			before:    &Cursor{ID: 5},
			direction: OrderDirectionAsc,
			wantOp:    "<",
		},
		{
			name:      "before DESC ID-only uses GT",
			after:     nil,
			before:    &Cursor{ID: 5},
			direction: OrderDirectionDesc,
			wantOp:    ">",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preds := CursorsPredicate(tt.after, tt.before, "id", "", tt.direction)
			require.Len(t, preds, 1)

			s := sql.Dialect("postgres").Select("*").From(sql.Table("users"))
			preds[0](s)
			query, _ := s.Query()
			assert.Contains(t, query, tt.wantOp, "expected SQL operator %q in %q", tt.wantOp, query)
			assert.Contains(t, query, "id")
		})
	}
}

// TestCursorsPredicate_IDOnly_BothCursors covers both ID-only predicates applied together.
func TestCursorsPredicate_IDOnly_BothCursors(t *testing.T) {
	after := &Cursor{ID: 3}
	before := &Cursor{ID: 20}

	for _, dir := range []OrderDirection{OrderDirectionAsc, OrderDirectionDesc} {
		t.Run(string(dir), func(t *testing.T) {
			preds := CursorsPredicate(after, before, "id", "", dir)
			require.Len(t, preds, 2)
			s := sql.Dialect("postgres").Select("*").From(sql.Table("users"))
			for _, pred := range preds {
				pred(s)
			}
			query, _ := s.Query()
			assert.Contains(t, query, "id")
		})
	}
}

// TestCursorsPredicate_EmptyDirection covers the default direction assignment (direction == "").
func TestCursorsPredicate_EmptyDirection(t *testing.T) {
	after := &Cursor{ID: 1}
	// Empty direction should default to ASC (GT for after cursor, ID-only).
	preds := CursorsPredicate(after, nil, "id", "", "")
	require.Len(t, preds, 1)

	s := sql.Dialect("postgres").Select("*").From(sql.Table("users"))
	preds[0](s)
	query, _ := s.Query()
	// ASC + after => GT
	assert.Contains(t, query, ">")
	assert.Contains(t, query, "id")
}

// =============================================================================
// AppendIf / AppendSlice / AppendBool Tests
// =============================================================================

func TestAppendIf(t *testing.T) {
	type P = func(*sql.Selector)
	fn := func(v int) P {
		return func(s *sql.Selector) {}
	}

	// nil value — no append
	var preds []P
	preds = AppendIf(preds, (*int)(nil), fn)
	assert.Len(t, preds, 0)

	// non-nil — appends
	v := 42
	preds = AppendIf(preds, &v, fn)
	assert.Len(t, preds, 1)
}

func TestAppendSlice(t *testing.T) {
	type P = func(*sql.Selector)
	fn := func(vs ...int) P {
		return func(s *sql.Selector) {}
	}

	// empty slice — no append
	var preds []P
	preds = AppendSlice(preds, []int{}, fn)
	assert.Len(t, preds, 0)

	// non-empty — appends
	preds = AppendSlice(preds, []int{1, 2}, fn)
	assert.Len(t, preds, 1)
}

func TestAppendBool(t *testing.T) {
	type P = func(*sql.Selector)
	fn := func() P {
		return func(s *sql.Selector) {}
	}

	// false — no append
	var preds []P
	preds = AppendBool(preds, false, fn)
	assert.Len(t, preds, 0)

	// true — appends
	preds = AppendBool(preds, true, fn)
	assert.Len(t, preds, 1)
}

// =============================================================================
// MultiCursorsPredicate Tests
// =============================================================================

func TestMultiCursorsPredicate_NilCursors(t *testing.T) {
	preds, err := MultiCursorsPredicate(nil, nil, &MultiCursorsOptions{
		FieldID:     "id",
		DirectionID: OrderDirectionAsc,
	})
	require.NoError(t, err)
	assert.Len(t, preds, 0)
}

func TestMultiCursorsPredicate_IDOnly(t *testing.T) {
	after := &Cursor{ID: 10}
	preds, err := MultiCursorsPredicate(after, nil, &MultiCursorsOptions{
		FieldID:     "id",
		DirectionID: OrderDirectionAsc,
	})
	require.NoError(t, err)
	assert.Len(t, preds, 1)

	// Apply predicate and check SQL
	s := sql.Select("*").From(sql.Table("users"))
	preds[0](s)
	query, _ := s.Query()
	assert.Contains(t, query, ">")
}

func TestMultiCursorsPredicate_IDOnly_Desc(t *testing.T) {
	before := &Cursor{ID: 10}
	preds, err := MultiCursorsPredicate(nil, before, &MultiCursorsOptions{
		FieldID:     "id",
		DirectionID: OrderDirectionDesc,
	})
	require.NoError(t, err)
	assert.Len(t, preds, 1)
}

func TestMultiCursorsPredicate_MultiField(t *testing.T) {
	after := &Cursor{ID: 5, Value: []any{"Alice", 30}}
	preds, err := MultiCursorsPredicate(after, nil, &MultiCursorsOptions{
		FieldID:     "id",
		DirectionID: OrderDirectionAsc,
		Fields:      []string{"name", "age"},
		Directions:  []OrderDirection{OrderDirectionAsc, OrderDirectionDesc},
	})
	require.NoError(t, err)
	assert.Len(t, preds, 1)

	// The composite predicate generates OR of ANDs
	s := sql.Select("*").From(sql.Table("users"))
	preds[0](s)
	query, _ := s.Query()
	assert.Contains(t, query, "OR")
}

func TestMultiCursorsPredicate_InvalidCursorType(t *testing.T) {
	after := &Cursor{ID: 5, Value: "not-a-slice"}
	_, err := MultiCursorsPredicate(after, nil, &MultiCursorsOptions{
		FieldID:     "id",
		DirectionID: OrderDirectionAsc,
		Fields:      []string{"name"},
		Directions:  []OrderDirection{OrderDirectionAsc},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a slice")
}

func TestMultiCursorsPredicate_MismatchedLengths(t *testing.T) {
	after := &Cursor{ID: 5, Value: []any{"Alice"}}
	_, err := MultiCursorsPredicate(after, nil, &MultiCursorsOptions{
		FieldID:     "id",
		DirectionID: OrderDirectionAsc,
		Fields:      []string{"name", "age"}, // 2 fields but 1 value
		Directions:  []OrderDirection{OrderDirectionAsc, OrderDirectionDesc},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "do not match")
}

// =============================================================================
// LimitPerRow Tests
// =============================================================================

func TestLimitPerRow(t *testing.T) {
	s := sql.Select("*").From(sql.Table("posts"))
	fn := LimitPerRow("user_id", 5)
	fn(s)
	query, _ := s.Query()
	// Should contain the CTE with ROW_NUMBER
	assert.Contains(t, query, "row_number")
	assert.Contains(t, query, "src_query")
	assert.Contains(t, query, "limited_query")
}
