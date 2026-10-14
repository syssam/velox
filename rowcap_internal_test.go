package velox

import "testing"

// TestRowCapAppliesToSelectsOnly: an insert's RETURNING and a COUNT are not
// capped, whatever the DB's cap.
func TestRowCapAppliesToSelectsOnly(t *testing.T) {
	db := OpenFake(Postgres, &FakeExec{}, WithMaxRows(1))
	for _, c := range []struct {
		name string
		st   Statement
		want int
	}{
		{"insert", Statement{Op: OpInsert, Entity: invInfo}, 0},
		{"insert returning", Statement{Op: OpInsert, Entity: invInfo, Returning: []int{0}}, 0},
		{"count", Statement{Op: OpSelect, Entity: invInfo, Count: true}, 0},
		{"select", Statement{Op: OpSelect, Entity: invInfo}, 1},
		{"select with MaxRows", Statement{Op: OpSelect, Entity: invInfo, MaxRows: 7}, 7},
	} {
		if got := db.rowCap(&c.st); got != c.want {
			t.Errorf("%s: rowCap = %d, want %d", c.name, got, c.want)
		}
	}
}
