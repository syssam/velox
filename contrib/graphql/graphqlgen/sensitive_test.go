package graphqlgen

import (
	"testing"

	"github.com/syssam/velox/contrib/graphql"
)

// field.Sensitive() marks a value that must be unreadable through the
// graph but still settable.
//
// Before 2026-08-14 velox consulted the flag in exactly one place
// (where_input.go), so a field marked Sensitive was still emitted into
// the GraphQL output type and was queryable by any client. That is the
// leak that matters most: it needs no log access and no struct
// marshaling to reach — just a query.
//
// The split pinned here matches ent-contrib/entgql, which checks
// Sensitive() on the type/order/where paths and NOT on the
// mutation-input path:
//
//	surface       sensitive field
//	------------------------------
//	output type   excluded
//	OrderBy       excluded   (row order is an oracle on the value)
//	WhereInput    excluded   (where_input.go::skipFieldInWhereInput)
//	CreateInput   KEPT       (a password must be settable)
//	UpdateInput   KEPT
func TestSkipSensitiveField_ReadSurfacesOnly(t *testing.T) {
	tests := []struct {
		name string
		skip graphql.SkipMode
		want bool
	}{
		{"excluded from output type", graphql.SkipType, true},
		{"excluded from order by", graphql.SkipOrderField, true},
		{"kept on create input", graphql.SkipMutationCreateInput, false},
		{"kept on update input", graphql.SkipMutationUpdateInput, false},
		{"kept on enum type", graphql.SkipEnumField, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := skipSensitiveRead(true, tc.skip); got != tc.want {
				t.Errorf("skipSensitiveRead(true, %v) = %v, want %v", tc.skip, got, tc.want)
			}
			if got := skipSensitiveRead(false, tc.skip); got {
				t.Errorf("skipSensitiveRead(false, %v) = true, want false", tc.skip)
			}
		})
	}
}
