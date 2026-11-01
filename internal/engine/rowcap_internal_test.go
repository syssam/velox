package engine

import (
	"math"
	"testing"
)

// TestRowCapAppliesToSelectsOnly: an insert's RETURNING and a COUNT are not
// capped, whatever the DB's cap.
func TestRowCapAppliesToSelectsOnly(t *testing.T) {
	invInfo := &EntityInfo{Name: "velox.inv", Tenant: -1}
	itemInfo := &EntityInfo{Name: "velox.item", Tenant: -1, PrimaryKey: []int{0}}
	tagInfo := &EntityInfo{Name: "velox.tag", Tenant: -1, PrimaryKey: []int{0}}
	bookTagInfo := &EntityInfo{Name: "velox.bookTag", Tenant: -1, PrimaryKey: []int{0, 1}}
	db := &DB{D: Postgres, Cfg: &config{MaxRows: 2}}
	for _, c := range []struct {
		name string
		st   Statement
		want int
	}{
		{"insert", Statement{Op: OpInsert, Entity: invInfo}, 0},
		{"insert returning", Statement{Op: OpInsert, Entity: invInfo, Returning: []int{0}}, 0},
		{"count", Statement{Op: OpSelect, Entity: invInfo, Count: true}, 0},
		{"select", Statement{Op: OpSelect, Entity: invInfo}, 2},
		{"select with MaxRows", Statement{Op: OpSelect, Entity: invInfo, MaxRows: 7}, 7},
		// A per-owner page is bounded by its limit times its owners; a load
		// keyed by the target's one-column key, by its keys.
		{"each, window", Statement{Op: OpSelect, Entity: itemInfo, Each: &EachClause{Keys: []any{1, 2, 3}, Column: 1, Limit: 4}}, 12},
		{"each, keyed by the target key", Statement{Op: OpSelect, Entity: itemInfo, Each: &EachClause{Keys: []any{1, 2, 3}, Column: 0, Limit: 4}}, 3},
		{"each, keyed by the target key, no limit", Statement{Op: OpSelect, Entity: itemInfo, Each: &EachClause{Keys: []any{1, 2, 3}, Column: 0}}, 3},
		{"each, no limit", Statement{Op: OpSelect, Entity: itemInfo, Each: &EachClause{Keys: []any{1, 2, 3}, Column: 1}}, 2},
		{"each, through", Statement{Op: OpSelect, Entity: tagInfo, Each: &EachClause{Keys: []any{1, 2}, Join: bookTagInfo, JoinTarget: 1, Limit: 3}}, 6},
		{"each, through, no limit", Statement{Op: OpSelect, Entity: tagInfo, Each: &EachClause{Keys: []any{1, 2}, Join: bookTagInfo, JoinTarget: 1}}, 2},
		{"each with MaxRows", Statement{Op: OpSelect, Entity: itemInfo, MaxRows: 7, Each: &EachClause{Keys: []any{1, 2, 3}, Column: 1, Limit: 4}}, 7},
		{"each, an overflowing limit", Statement{Op: OpSelect, Entity: itemInfo, Each: &EachClause{Keys: []any{1, 2, 3}, Column: 1, Limit: math.MaxInt}}, math.MaxInt},
		// A grouped count returns a row per owner at most.
		{"count each", Statement{Op: OpSelect, Entity: itemInfo, Count: true, Each: &EachClause{Keys: []any{1, 2, 3}, Column: 1}}, 3},
		{"count each, through", Statement{Op: OpSelect, Entity: tagInfo, Count: true, Each: &EachClause{Keys: []any{1, 2}, Join: bookTagInfo, JoinTarget: 1}}, 2},
	} {
		if got := db.rowCap(&c.st); got != c.want {
			t.Errorf("%s: rowCap = %d, want %d", c.name, got, c.want)
		}
	}
}
