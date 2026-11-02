package engine

import (
	"reflect"
	"strings"
	"testing"
)

// TestAfterNullableUniqueIndex: NULLs do not collide, so a unique index of a
// nullable column leaves its NULL rows tied, and After does not take it for a
// total order.
func TestAfterNullableUniqueIndex(t *testing.T) {
	in := &EntityInfo{
		Name: "velox.thing", Table: "things", Tenant: -1, PrimaryKey: []int{0},
		Columns: []ColumnInfo{{Name: "id", Type: reflect.TypeFor[int64]()}, {Name: "slug", Type: reflect.TypeFor[*string](), Nullable: true}},
		Indexes: []IndexInfo{{Columns: []int{1}, Unique: true}},
	}
	st := &Statement{Op: OpSelect, Entity: in, OrderBy: []OrderTerm{{Column: 1}}}
	err := AddAfter(st, in, nil)
	if err == nil || !strings.Contains(err.Error(), "After needs an OrderBy that includes the primary key or a unique index") {
		t.Errorf("%v", err)
	}
}
