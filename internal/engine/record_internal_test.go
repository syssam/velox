package engine

import (
	"strings"
	"testing"
)

// TestTrackedWriteRefuses: a recorded write whose rows cannot be found again
// is refused before anything is sent.
func TestTrackedWriteRefuses(t *testing.T) {
	keyless := &EntityInfo{Name: "velox.log", Columns: []ColumnInfo{{Name: "line"}}}
	keyed := &EntityInfo{Name: "velox.inv", Columns: []ColumnInfo{{Name: "id"}}, PrimaryKey: []int{0}}
	for _, c := range []struct {
		name string
		st   *Statement
		want string
	}{
		{"no key", &Statement{Op: OpDelete, Entity: keyless}, "needs a primary key"},
		{"key by Add", &Statement{Op: OpUpdate, Entity: keyed, Set: []Assignment{{Column: 0, Op: AssignAdd, Value: int64(1)}}}, "by Add or Sub"},
	} {
		err := trackedWrite(t.Context(), nil, &MutationState{}, nil, c.st)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want %q", c.name, err, c.want)
		}
	}
}
