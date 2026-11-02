package engine

import (
	"context"
	"reflect"
	"testing"
)

// TestCheckRefsCostsNothingUnset pins the common path of an entity with TenantRefs,
// and of one without: a write that sets no reference sends nothing and allocates
// nothing for the check.
func TestCheckRefsCostsNothingUnset(t *testing.T) {
	ctx := context.Background()
	with := &EntityInfo{Name: "x", Tenant: 0, Columns: []ColumnInfo{{Name: "org"}, {Name: "fk"}, {Name: "n"}},
		refs: []tenantRef{{Column: 1, Table: "t", Key: "id", Tenant: "org", self: -1}}}
	without := &EntityInfo{Name: "y", Tenant: 0, Columns: with.Columns}
	db := DB{D: Postgres}
	var nilKey *int64
	for name, st := range map[string]*Statement{
		"an update of another column": {Op: OpUpdate, Entity: with, Tenant: int64(7), Set: []Assignment{{Column: 2, Op: AssignSet, Value: 1}}},
		"an update to NULL":           {Op: OpUpdate, Entity: with, Tenant: int64(7), Set: []Assignment{{Column: 1, Op: AssignSet}}},
		"an update to a nil pointer":  {Op: OpUpdate, Entity: with, Tenant: int64(7), Set: []Assignment{{Column: 1, Op: AssignSet, Value: nilKey}}},
		"an insert without it":        {Op: OpInsert, Entity: with, Tenant: int64(7), Columns: []int{0, 2}, Rows: [][]any{{1, 2}}},
		"an insert of NULL":           {Op: OpInsert, Entity: with, Tenant: int64(7), Columns: []int{0, 1}, Rows: [][]any{{1, nilKey}}},
		"under no tenant":             {Op: OpUpdate, Entity: with, Set: []Assignment{{Column: 1, Op: AssignSet, Value: 1}}},
		"a delete":                    {Op: OpDelete, Entity: with, Tenant: int64(7)},
		"an entity without any":       {Op: OpInsert, Entity: without, Tenant: int64(7), Columns: []int{1}, Rows: [][]any{{1}}},
	} {
		t.Run(name, func(t *testing.T) {
			if n := testing.AllocsPerRun(100, func() {
				if err := db.checkRefs(ctx, st, Target{}); err != nil { // a zero Target: sending would panic
					t.Fatal(err)
				}
			}); n != 0 {
				t.Errorf("%.0f allocations", n)
			}
		})
	}
}

func TestRefValues(t *testing.T) {
	type bs []byte
	one, two := int64(1), int64(2)
	r := &tenantRef{Column: 1, self: -1}
	self := &tenantRef{Column: 1, self: 0}
	ins := func(rows ...[]any) *Statement { return &Statement{Op: OpInsert, Columns: []int{0, 1}, Rows: rows} }
	for name, c := range map[string]struct {
		r    *tenantRef
		st   *Statement
		want []any
	}{
		"not written":       {r, &Statement{Op: OpInsert, Columns: []int{0}, Rows: [][]any{{1}}}, nil},
		"one":               {r, ins([]any{0, &one}), []any{int64(1)}},
		"nil":               {r, ins([]any{0, (*int64)(nil)}), nil},
		"nil bytes":         {r, ins([]any{0, []byte(nil)}), nil},
		"nil named bytes":   {r, ins([]any{0, bs(nil)}), nil},
		"distinct":          {r, ins([]any{0, &one}, []any{0, &two}, []any{0, &one}, []any{0, nil}), []any{int64(1), int64(2)}},
		"distinct bytes":    {r, ins([]any{0, []byte("a")}, []any{0, []byte("a")}, []any{0, bs("b")}), []any{[]byte("a"), bs("b")}},
		"own rows":          {self, ins([]any{int64(1), &two}, []any{int64(2), &one}), []any{}},
		"another's":         {self, ins([]any{int64(1), &two}), []any{int64(2)}},
		"an upsert's own":   {self, &Statement{Op: OpInsert, Columns: []int{0, 1}, Rows: [][]any{{int64(2), &two}}, Conflict: &ConflictClause{DoNothing: true}}, []any{int64(2)}},
		"a generated key":   {self, &Statement{Op: OpInsert, Columns: []int{1}, Rows: [][]any{{&two}}}, []any{int64(2)}},
		"uncomparable":      {r, ins([]any{0, []int{1}}, []any{0, []int{1}}), []any{[]int{1}}},
		"an update":         {r, &Statement{Op: OpUpdate, Set: []Assignment{{Column: 1, Op: AssignSet, Value: &one}}}, []any{int64(1)}},
		"an update's other": {r, &Statement{Op: OpUpdate, Set: []Assignment{{Column: 2, Op: AssignSet, Value: &one}}}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			if got := refValues(c.st, c.r); (len(got) != 0 || len(c.want) != 0) && !reflect.DeepEqual(got, c.want) {
				t.Errorf("%#v, want %#v", got, c.want)
			}
		})
	}
}

func TestBuildTenantRefsRefuses(t *testing.T) {
	cols := []ColumnInfo{{Name: "id", Field: "ID"}, {Name: "fk", Field: "FK"}}
	for name, r := range map[string]TenantRef{
		"out of range": {Column: 2, Table: "t", Key: "id", Tenant: "org", TenantKind: "int64"},
		"negative":     {Column: -1, Table: "t", Key: "id", Tenant: "org", TenantKind: "int64"},
		"no table":     {Column: 1, Key: "id", Tenant: "org", TenantKind: "int64"},
		"no key":       {Column: 1, Table: "t", Tenant: "org", TenantKind: "int64"},
		"no tenant":    {Column: 1, Table: "t", Key: "id", TenantKind: "int64"},
		"no kind":      {Column: 1, Table: "t", Key: "id", Tenant: "org"},
	} {
		if _, err := buildTenantRefs("x", &DescriptorFields{Table: "x", TenantRefs: []TenantRef{r}}, cols); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	refs, err := buildTenantRefs("x", &DescriptorFields{Table: "x", TenantRefs: []TenantRef{
		{Column: 1, Table: "x", Key: "id", Tenant: "org", TenantKind: "int64"}, {Column: 1, Schema: "other", Table: "x", Key: "id", Tenant: "org", TenantKind: "int64"}, {Column: 1, Table: "y", Key: "id", Tenant: "org", TenantKind: "int64"},
	}}, cols)
	if err != nil || refs[0].self != 0 || refs[1].self != -1 || refs[2].self != -1 {
		t.Errorf("%+v, %v", refs, err)
	}
}
