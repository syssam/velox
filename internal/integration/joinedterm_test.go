package integration

import (
	"fmt"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestJoinedTermGroupsByTheParent: Rel.Joined groups a child's rows by its
// parent's column in one statement (ADR 0143), the parent read through its
// policy as the join reads it: for ann, ticket 5's parent (bob's 3) is
// hidden, so Join leaves the ticket out and LeftJoin groups it under NULL
// with ticket 1, which has no parent. The tickets are
// TestJoinReadsTheTargetThroughItsPolicy's.
func TestJoinedTermGroupsByTheParent(t *testing.T) {
	inner := map[string]string{"ann": "map[1:1]", "admin": "map[1:1 3:2]", "anyone": "map[1:1 3:2 6:2]"}
	left := map[string]string{"ann": "map[0:2 1:1]", "admin": "map[0:2 1:1 3:2]", "anyone": "map[0:3 1:1 3:2 6:2]"}
	type row struct {
		ID *int64
		N  int64
	}
	eachCaller(t, func(t *testing.T, tg target, c who) {
		parent := velox.Joined(Tk.Parent, Tk.ID)
		count := func(q velox.Query[testmodel.Ticket]) string {
			t.Helper()
			rows, err := velox.Scan[row](c.ctx, q.Select(parent, velox.CountAll[testmodel.Ticket]().As("n")).GroupBy(parent).Limit(10))
			if err != nil {
				t.Fatal(err)
			}
			m := map[int64]int64{}
			for _, r := range rows {
				var id int64
				if r.ID != nil {
					id = *r.ID
				}
				m[id] = r.N
			}
			return fmt.Sprint(m)
		}
		if got := count(c.on(tg).Query[testmodel.Ticket]().Join(Tk.Parent)); got != inner[c.name] {
			t.Errorf("Join: %s, want %s", got, inner[c.name])
		}
		if got := count(c.on(tg).Query[testmodel.Ticket]().LeftJoin(Tk.Parent)); got != left[c.name] {
			t.Errorf("LeftJoin: %s, want %s", got, left[c.name])
		}
	})
}
