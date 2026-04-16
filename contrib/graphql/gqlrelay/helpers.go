package gqlrelay

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/99designs/gqlgen/graphql"

	"github.com/syssam/velox/dialect/sql"
)

// MaxPaginationLimit is the maximum number of items that can be requested in a single page.
// This prevents clients from requesting excessive amounts of data.
const MaxPaginationLimit = 1000

// AppendIf appends the predicate fn(*v) to preds if v is non-nil.
// Used by generated WhereInput code for optional pointer fields.
func AppendIf[P ~func(*sql.Selector), T any](preds []P, v *T, fn func(T) P) []P {
	if v != nil {
		return append(preds, fn(*v))
	}
	return preds
}

// AppendSlice appends the predicate fn(vs...) to preds if vs is non-empty.
// Used by generated WhereInput code for In/NotIn operators.
func AppendSlice[P ~func(*sql.Selector), T any](preds []P, vs []T, fn func(...T) P) []P {
	if len(vs) > 0 {
		return append(preds, fn(vs...))
	}
	return preds
}

// AppendBool appends the predicate fn() to preds if val is true.
// Used by generated WhereInput code for niladic operators (IsNil, NotNil).
func AppendBool[P ~func(*sql.Selector)](preds []P, val bool, fn func() P) []P {
	if val {
		return append(preds, fn())
	}
	return preds
}

// Pagination validation errors - generic messages for security.
var (
	ErrInvalidPagination       = errors.New("invalid pagination parameters")
	ErrPaginationLimitExceeded = errors.New("pagination limit exceeded")
)

// DirectionOrderTerm converts an OrderDirection to an sql.OrderTermOption.
// This is used by generated pagination code to apply ordering.
func DirectionOrderTerm(d OrderDirection) sql.OrderTermOption {
	return d.OrderTermOption()
}

// ValidateFirstLast validates first/last pagination parameters.
func ValidateFirstLast(first *int, last *int) error {
	switch {
	case first != nil && last != nil:
		return ErrInvalidPagination
	case first != nil && *first < 0:
		return ErrInvalidPagination
	case last != nil && *last < 0:
		return ErrInvalidPagination
	case first != nil && *first > MaxPaginationLimit:
		return ErrPaginationLimitExceeded
	case last != nil && *last > MaxPaginationLimit:
		return ErrPaginationLimitExceeded
	}
	return nil
}

// CollectedField returns the collected field for the given path in the GraphQL context.
func CollectedField(ctx context.Context, path ...string) *graphql.CollectedField {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil {
		return nil
	}
	field := fc.Field
	oc := graphql.GetOperationContext(ctx)
walk:
	for _, name := range path {
		for _, f := range graphql.CollectFields(oc, field.Selections, nil) {
			if f.Alias == name {
				field = f
				continue walk
			}
		}
		return nil
	}
	return &field
}

// HasCollectedField reports whether the given field path exists in the GraphQL context.
func HasCollectedField(ctx context.Context, path ...string) bool {
	if graphql.GetFieldContext(ctx) == nil {
		return true
	}
	return CollectedField(ctx, path...) != nil
}

// PaginateLimit returns the query limit based on first/last pagination parameters.
func PaginateLimit(first *int, last *int) int {
	var limit int
	if first != nil {
		limit = *first + 1
	} else if last != nil {
		limit = *last + 1
	}
	return limit
}

// CursorsPredicate converts cursors to SQL predicates for cursor-based pagination.
// It uses composite comparison (field, id) to handle non-unique order fields correctly.
// When field is empty, only the ID column is used for cursor comparison (default order).
func CursorsPredicate(after *Cursor, before *Cursor, idField string, field string, direction OrderDirection) []func(*sql.Selector) {
	var predicates []func(*sql.Selector)
	// When no custom order field is set, default to ascending ID order.
	if direction == "" {
		direction = OrderDirectionAsc
	}
	if after != nil {
		if field == "" {
			// ID-only cursor: simple single-column comparison.
			if direction == OrderDirectionAsc {
				predicates = append(predicates, func(s *sql.Selector) {
					s.Where(sql.GT(s.C(idField), after.ID))
				})
			} else {
				predicates = append(predicates, func(s *sql.Selector) {
					s.Where(sql.LT(s.C(idField), after.ID))
				})
			}
		} else if direction == OrderDirectionAsc {
			predicates = append(predicates, func(s *sql.Selector) {
				s.Where(sql.CompositeGT([]string{field, idField}, after.Value, after.ID))
			})
		} else {
			predicates = append(predicates, func(s *sql.Selector) {
				s.Where(sql.CompositeLT([]string{field, idField}, after.Value, after.ID))
			})
		}
	}
	if before != nil {
		if field == "" {
			// ID-only cursor: simple single-column comparison.
			if direction == OrderDirectionAsc {
				predicates = append(predicates, func(s *sql.Selector) {
					s.Where(sql.LT(s.C(idField), before.ID))
				})
			} else {
				predicates = append(predicates, func(s *sql.Selector) {
					s.Where(sql.GT(s.C(idField), before.ID))
				})
			}
		} else if direction == OrderDirectionAsc {
			predicates = append(predicates, func(s *sql.Selector) {
				s.Where(sql.CompositeLT([]string{field, idField}, before.Value, before.ID))
			})
		} else {
			predicates = append(predicates, func(s *sql.Selector) {
				s.Where(sql.CompositeGT([]string{field, idField}, before.Value, before.ID))
			})
		}
	}
	return predicates
}

// MultiCursorsOptions configures multi-column cursor predicate generation.
type MultiCursorsOptions struct {
	FieldID     string           // ID field name.
	DirectionID OrderDirection   // ID field direction.
	Fields      []string         // OrderBy fields used by the cursor.
	Directions  []OrderDirection // OrderBy directions used by the cursor.
}

// MultiCursorsPredicate returns predicates for multi-column cursor-based pagination.
// It generates composite row-value comparisons like:
//
//	(x < x1 OR (x = x1 AND y > y1) OR (x = x1 AND y = y1 AND id > id1))
//
// This is equivalent to Ent's entgql.MultiCursorsPredicate.
func MultiCursorsPredicate(after, before *Cursor, opts *MultiCursorsOptions) ([]func(s *sql.Selector), error) {
	var predicates []func(s *sql.Selector)
	for _, cursor := range []*Cursor{after, before} {
		if cursor == nil {
			continue
		}
		if cursor.Value != nil {
			predicate, err := multiPredicate(cursor, opts)
			if err != nil {
				return nil, err
			}
			predicates = append(predicates, predicate)
		} else {
			if opts.DirectionID == OrderDirectionAsc {
				predicates = append(predicates, func(s *sql.Selector) {
					s.Where(sql.GT(s.C(opts.FieldID), cursor.ID))
				})
			} else {
				predicates = append(predicates, func(s *sql.Selector) {
					s.Where(sql.LT(s.C(opts.FieldID), cursor.ID))
				})
			}
		}
	}
	return predicates, nil
}

func multiPredicate(cursor *Cursor, opts *MultiCursorsOptions) (func(*sql.Selector), error) {
	values, ok := cursor.Value.([]any)
	if !ok {
		return nil, fmt.Errorf("cursor %T is not a slice", cursor.Value)
	}
	if len(values) != len(opts.Fields) {
		return nil, fmt.Errorf("cursor values length %d do not match orderBy fields length %d", len(values), len(opts.Fields))
	}
	if len(opts.Directions) != len(opts.Fields) {
		return nil, fmt.Errorf("orderBy directions length %d do not match orderBy fields length %d", len(opts.Directions), len(opts.Fields))
	}
	// Ensure the row value is unique by adding the ID field if not already present.
	fields := make([]string, len(opts.Fields))
	copy(fields, opts.Fields)
	directions := make([]OrderDirection, len(opts.Directions))
	copy(directions, opts.Directions)
	vals := make([]any, len(values))
	copy(vals, values)

	if !slices.Contains(fields, opts.FieldID) {
		vals = append(vals, cursor.ID)
		fields = append(fields, opts.FieldID)
		directions = append(directions, opts.DirectionID)
	}
	return func(s *sql.Selector) {
		// Given terms: x DESC, y ASC, etc. Generate:
		// (x < x1 OR (x = x1 AND y > y1) OR (x = x1 AND y = y1 AND id > last))
		var or []*sql.Predicate
		for i := range fields {
			var ands []*sql.Predicate
			for j := range i {
				ands = append(ands, sql.EQ(s.C(fields[j]), vals[j]))
			}
			if directions[i] == OrderDirectionAsc {
				ands = append(ands, sql.GT(s.C(fields[i]), vals[i]))
			} else {
				ands = append(ands, sql.LT(s.C(fields[i]), vals[i]))
			}
			or = append(or, sql.And(ands...))
		}
		s.Where(sql.Or(or...))
	}, nil
}

// LimitPerRow returns a query modifier that limits the number of rows returned
// per partition. Used for edge pagination to limit results per parent node
// instead of globally.
//
// This is equivalent to Ent's entgql.LimitPerRow.
func LimitPerRow(partitionBy string, limit int, orderBy ...sql.Querier) func(s *sql.Selector) {
	return func(s *sql.Selector) {
		d := sql.Dialect(s.Dialect())
		s.SetDistinct(false)
		with := d.With("src_query").
			As(s.Clone()).
			With("limited_query").
			As(
				d.Select("*").
					AppendSelectExprAs(
						sql.RowNumber().PartitionBy(partitionBy).OrderExpr(orderBy...),
						"row_number",
					).
					From(d.Table("src_query")),
			)
		t := d.Table("limited_query").As(s.TableName())
		*s = *d.Select(s.UnqualifiedColumns()...).
			From(t).
			Where(sql.LTE(t.C("row_number"), limit)).
			Prefix(with)
	}
}
