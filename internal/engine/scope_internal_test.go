package engine

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

type (
	tenantNum int32
	tenantStr string
)

// TestTenantFor: a context's tenant converts to the tenant column's type when
// the column can hold it. The zero value is no tenant, a value out of the type's
// range is refused, and so is an id of a kind the column never holds.
func TestTenantFor(t *testing.T) {
	in := func(typ reflect.Type) *EntityInfo {
		return &EntityInfo{Name: "e", Tenant: 0, Columns: []ColumnInfo{{Name: "org", Type: typ}}}
	}
	i64, i8, u16 := reflect.TypeFor[int64](), reflect.TypeFor[int8](), reflect.TypeFor[uint16]()
	str, num, named := reflect.TypeFor[string](), reflect.TypeFor[tenantNum](), reflect.TypeFor[tenantStr]()
	const (
		ok = iota
		zero
		mismatch
		misfit
	)
	for _, c := range []struct {
		col  reflect.Type
		id   any
		want any
		kind int
	}{
		{i64, int64(7), int64(7), ok},
		{i64, int(7), int64(7), ok},
		{i64, int8(7), int64(7), ok},
		{i64, int16(7), int64(7), ok},
		{i64, int32(7), int64(7), ok},
		{i64, uint(7), int64(7), ok},
		{i64, uint8(7), int64(7), ok},
		{i64, uint16(7), int64(7), ok},
		{i64, uint32(7), int64(7), ok},
		{i64, uint64(7), int64(7), ok},
		{i8, int(-1), int8(-1), ok},
		{u16, int(7), uint16(7), ok},
		{u16, uint64(7), uint16(7), ok},
		{num, int(7), tenantNum(7), ok},
		{str, "a", "a", ok},
		{named, "a", tenantStr("a"), ok},
		{i64, int64(0), nil, zero},
		{i64, int(0), nil, zero},
		{u16, uint(0), nil, zero},
		{str, "", nil, zero},
		{named, "", nil, zero},
		{i8, int(300), nil, misfit},
		{i64, uint64(math.MaxUint64), nil, misfit},
		{u16, int(-1), nil, misfit},
		{u16, uint(70000), nil, misfit},
		{i64, "a", nil, mismatch},
		{i64, 1.5, nil, mismatch},
		{str, int(1), nil, mismatch},
		{reflect.TypeFor[float64](), int(1), nil, mismatch},
	} {
		got, err := tenantFor(in(c.col), c.id)
		switch c.kind {
		case ok:
			if err != nil || got != c.want {
				t.Errorf("%v from %T(%v): %#v, %v; want %#v", c.col, c.id, c.id, got, err, c.want)
			}
		case zero:
			if !errors.Is(err, ErrNoTenant) {
				t.Errorf("%v from %T(%v): %#v, %v; want ErrNoTenant", c.col, c.id, c.id, got, err)
			}
		default:
			if !errors.Is(err, ErrTenantMismatch) {
				t.Errorf("%v from %T(%v): %#v, %v; want ErrTenantMismatch", c.col, c.id, c.id, got, err)
			}
		}
	}
	_, err := tenantFor(in(i64), "a")
	if want := "velox: e: " + ErrTenantMismatch.Error() + ": the context's tenant is a string, the column org is a int64"; err == nil || err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
	_, err = tenantFor(in(i8), 300)
	if want := "velox: e: " + ErrTenantMismatch.Error() + ": the context's tenant 300 (a int) does not fit the column org, a int8"; err == nil || err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
}

func TestCheckTenantRows(t *testing.T) {
	in := &EntityInfo{Name: "e", Tenant: 1, Columns: []ColumnInfo{{Name: "id"}, {Name: "org"}}}
	for _, c := range []struct {
		name string
		st   Statement
		want string // the end of the error; empty for none
	}{
		{"all in the tenant", Statement{Columns: []int{0, 1}, Rows: [][]any{{1, int64(7)}, {2, int64(7)}}}, ""},
		{"the column is not written", Statement{Columns: []int{0}, Rows: [][]any{{1}}}, "the statement does not write org"},
		{"another tenant's row", Statement{Columns: []int{1}, Rows: [][]any{{int64(7)}, {int64(8)}}}, "a row's org is 8, the tenant is 7"},
	} {
		err := checkTenantRows(in, &c.st, int64(7))
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		if want := "velox: e: " + ErrTenantMismatch.Error() + ": " + c.want; !errors.Is(err, ErrTenantMismatch) || err.Error() != want {
			t.Errorf("%s: got %v, want %s", c.name, err, want)
		}
	}
}

func TestCheckTenantAssignments(t *testing.T) {
	in := &EntityInfo{Name: "e", Tenant: 1, Columns: []ColumnInfo{{Name: "id"}, {Name: "org"}}}
	for _, c := range []struct {
		name string
		set  []Assignment
		want string // the end of the error; empty for none
	}{
		{"another column", []Assignment{{Column: 0, Op: AssignAdd, Value: 1}}, ""},
		{"the tenant itself", []Assignment{{Column: 1, Op: AssignSet, Value: int64(7)}}, ""},
		{"arithmetic", []Assignment{{Column: 1, Op: AssignAdd, Value: int64(1)}}, "the statement does arithmetic on org"},
		{"another tenant", []Assignment{{Column: 1, Op: AssignSet, Value: int64(8)}}, "the statement sets org to 8, the tenant is 7"},
	} {
		err := checkTenantAssignments(in, c.set, int64(7))
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		if want := "velox: e: " + ErrTenantImmutable.Error() + ": " + c.want; !errors.Is(err, ErrTenantImmutable) || err.Error() != want {
			t.Errorf("%s: got %v, want %s", c.name, err, want)
		}
	}
}

func TestColumnNames(t *testing.T) {
	in := &EntityInfo{Columns: []ColumnInfo{{Name: "a"}, {Name: "b"}, {Name: "c"}}}
	for want, cols := range map[string][]int{"": nil, "b": {1}, "c, a": {2, 0}} {
		if got := columnNames(in, cols); got != want {
			t.Errorf("%v: %q, want %q", cols, got, want)
		}
	}
}

// TestInExists: an error from inside an EXISTS names the statement's entity,
// then the targets from the outermost in, once each, and errors.Is still sees
// it. A cycle names the statement's entity already, and passes unchanged.
func TestInExists(t *testing.T) {
	sc := &Scoper{Entity: "a"}
	inner, outer := &EntityInfo{Name: "b"}, &EntityInfo{Name: "c"}
	boom := errors.New("velox: b: boom")
	err := sc.inExists(inner, boom)
	if want := "velox: a: in the EXISTS on b: boom"; err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
	err = sc.inExists(outer, err)
	if want := "velox: a: in the EXISTS on c, then on b: boom"; err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
	if !errors.Is(err, boom) {
		t.Errorf("%v does not wrap the error", err)
	}
	cycle := &cycleError{entity: "a", policy: "b"}
	if got := sc.inExists(outer, cycle); got != error(cycle) {
		t.Errorf("a cycle became %v", got)
	}
	if want := "velox: a: the Read policy of b reaches its own Read through an EXISTS; such a policy cannot be applied"; cycle.Error() != want {
		t.Errorf("got %v, want %s", cycle, want)
	}
}
