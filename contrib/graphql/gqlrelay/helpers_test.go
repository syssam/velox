package gqlrelay

import (
	stdsql "database/sql"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite" // registers the "sqlite" driver

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

// The single row comparison is only exact where every NULL row sorts before the
// cursor (PostgreSQL, all keys descending, no NULL cursor value). Anywhere else
// the OR expansion must stay, because a NULL row can sort after the cursor and a
// row comparison would skip it.
func TestCursorsPredicate_RowComparisonOnlyWhereExact(t *testing.T) {
	cursor := &Cursor{ID: 7, Value: "alice"}
	tests := []struct {
		name      string
		dialect   string
		after     *Cursor
		direction OrderDirection
		wantRow   bool
	}{
		{"postgres DESC non-NULL value", "postgres", cursor, OrderDirectionDesc, true},
		{"postgres ASC keeps OR (NULLs sort after)", "postgres", cursor, OrderDirectionAsc, false},
		{"mysql DESC keeps OR (NULLs sort last)", "mysql", cursor, OrderDirectionDesc, false},
		{"sqlite DESC keeps OR (NULLs sort last)", "sqlite", cursor, OrderDirectionDesc, false},
		{"postgres DESC NULL value keeps OR", "postgres", &Cursor{ID: 7}, OrderDirectionDesc, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preds := CursorsPredicate(tt.after, nil, "id", "name", tt.direction)
			require.Len(t, preds, 1)
			s := sql.Dialect(tt.dialect).Select("*").From(sql.Table("users"))
			preds[0](s)
			query, args := s.Query()
			if tt.wantRow {
				assert.Contains(t, query, ") < (", "expected one row comparison: %s", query)
				assert.NotContains(t, query, " OR ", "row comparison must not also carry the OR form: %s", query)
				assert.Equal(t, []any{tt.after.Value, tt.after.ID}, args, "the cursor's own values, in column order")
			} else {
				assert.NotContains(t, query, ") < (", "must keep the OR expansion: %s", query)
			}
		})
	}
}

// The claim behind the row comparison is that, for a non-NULL cursor value, it
// selects exactly the rows after the cursor in PostgreSQL's descending order
// (NULL first, then value descending, then id descending). This runs the SQL
// keysetAfter really emits for PostgreSQL against a table holding NULLs and
// ties, and checks it against a model of that order. SQLite is only the engine:
// it evaluates the same row-value comparison and three-valued logic.
func TestCursorsPredicate_RowComparisonSelectsRowsAfterCursor(t *testing.T) {
	db, err := stdsql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1) // one connection, or each would see its own empty database

	_, err = db.Exec(`CREATE TABLE "t" ("name" TEXT NULL, "id" INTEGER PRIMARY KEY)`)
	require.NoError(t, err)

	type row struct {
		name *string
		id   int64
	}
	str := func(s string) *string { return &s }
	rows := []row{
		{nil, 1}, {nil, 2},
		{str("a"), 3}, {str("a"), 4},
		{str("b"), 5}, {str("b"), 6},
		{str("c"), 7},
	}
	for _, r := range rows {
		_, err := db.Exec(`INSERT INTO "t" ("name", "id") VALUES (?, ?)`, r.name, r.id)
		require.NoError(t, err)
	}

	// before reports whether a sorts ahead of b: NULL first, then name
	// descending, then id descending.
	before := func(a, b row) bool {
		switch {
		case a.name == nil && b.name != nil:
			return true
		case a.name != nil && b.name == nil:
			return false
		case a.name != nil && *a.name != *b.name:
			return *a.name > *b.name
		}
		return a.id > b.id
	}

	for _, cur := range rows {
		if cur.name == nil {
			continue // a NULL cursor value takes the OR expansion; see rowComparable
		}
		var want []int64
		for _, r := range rows {
			if before(cur, r) {
				want = append(want, r.id)
			}
		}
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })

		preds := CursorsPredicate(&Cursor{ID: cur.id, Value: *cur.name}, nil, "id", "name", OrderDirectionDesc)
		require.Len(t, preds, 1)
		s := sql.Dialect("postgres").Select("id").From(sql.Table("t"))
		preds[0](s)
		query, args := s.Query()
		require.Contains(t, query, ") < (", "this test is about the row comparison form: %s", query)
		// PostgreSQL numbers its placeholders $1, $2; SQLite spells that ?1, ?2.
		query = strings.NewReplacer("$1", "?1", "$2", "?2").Replace(query)

		found, err := db.Query(query, args...)
		require.NoError(t, err)
		var got []int64
		for found.Next() {
			var id int64
			require.NoError(t, found.Scan(&id))
			got = append(got, id)
		}
		require.NoError(t, found.Err())
		require.NoError(t, found.Close())
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })

		assert.Equal(t, want, got, "rows after cursor (%s, %d)", *cur.name, cur.id)
	}
}

func TestRowComparable(t *testing.T) {
	desc, asc := []bool{false, false}, []bool{false, true}
	f := []string{"name", "id"}
	assert.True(t, rowComparable(f, desc, []any{"a", int64(1)}, false))
	assert.False(t, rowComparable(f, desc, []any{"a", int64(1)}, true), "NULLs-first-on-ASC dialect")
	assert.False(t, rowComparable(f, asc, []any{"a", int64(1)}, false), "mixed direction")
	assert.False(t, rowComparable(f, desc, []any{nil, int64(1)}, false), "NULL cursor value")
	assert.False(t, rowComparable([]string{"id"}, []bool{false}, []any{int64(1)}, false), "single key")
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

// TestMultiCursorsPredicate_Before_Composite_ASC pins that the `before`
// cursor produces the MIRROR comparison of `after` — the same inversion that
// CursorsPredicate applies (after+ASC → GT, before+ASC → LT). Before this fix
// the before cursor reused the after-direction, so backward multi-order
// pagination selected rows on the WRONG side of the cursor (e.g. ordering
// name ASC with before=cursor(D) returned rows after D, not before it).
func TestMultiCursorsPredicate_Before_Composite_ASC(t *testing.T) {
	before := &Cursor{ID: 5, Value: []any{"Alice"}}
	preds, err := MultiCursorsPredicate(nil, before, &MultiCursorsOptions{
		FieldID:     "id",
		DirectionID: OrderDirectionAsc,
		Fields:      []string{"name"},
		Directions:  []OrderDirection{OrderDirectionAsc},
	})
	require.NoError(t, err)
	require.Len(t, preds, 1)

	s := sql.Select("*").From(sql.Table("users"))
	preds[0](s)
	query, _ := s.Query()
	// before + ASC restricts to rows BEFORE the cursor → "<", never ">".
	assert.Contains(t, query, "<", "before+ASC must use LT (mirror of the after-direction)")
	assert.NotContains(t, query, ">", "before+ASC must NOT use GT — that selects the after-side")
}

// TestMultiCursorsPredicate_Before_Composite_DESC is the DESC mirror: before +
// DESC must use GT (CursorsPredicate after+DESC → LT, before+DESC → GT).
func TestMultiCursorsPredicate_Before_Composite_DESC(t *testing.T) {
	before := &Cursor{ID: 5, Value: []any{"Alice"}}
	preds, err := MultiCursorsPredicate(nil, before, &MultiCursorsOptions{
		FieldID:     "id",
		DirectionID: OrderDirectionDesc,
		Fields:      []string{"name"},
		Directions:  []OrderDirection{OrderDirectionDesc},
	})
	require.NoError(t, err)
	require.Len(t, preds, 1)

	s := sql.Select("*").From(sql.Table("users"))
	preds[0](s)
	query, _ := s.Query()
	assert.Contains(t, query, ">", "before+DESC must use GT (mirror of the after-direction)")
	assert.NotContains(t, query, "<", "before+DESC must NOT use LT — that selects the after-side")
}

// TestMultiCursorsPredicate_Before_IDOnly_ASC pins the same inversion for the
// ID-only (nil Value) before path, which previously also reused the
// after-direction.
func TestMultiCursorsPredicate_Before_IDOnly_ASC(t *testing.T) {
	before := &Cursor{ID: 10}
	preds, err := MultiCursorsPredicate(nil, before, &MultiCursorsOptions{
		FieldID:     "id",
		DirectionID: OrderDirectionAsc,
	})
	require.NoError(t, err)
	require.Len(t, preds, 1)

	s := sql.Select("*").From(sql.Table("users"))
	preds[0](s)
	query, _ := s.Query()
	assert.Contains(t, query, "<", "before+ASC ID-only must use LT (mirror of after)")
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
