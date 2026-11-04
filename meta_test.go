package velox

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type other struct{ ID int64 }

func TestMetaOnceBuildsOnce(t *testing.T) {
	var o MetaOnce[other]
	calls := 0
	desc := func() Descriptor[other] {
		calls++
		return Descriptor[other]{Table: "other", Columns: []ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			FieldPtr: func(x *other, i int) any { return &x.ID }}
	}
	a, b := o.Load(desc), o.Load(desc)
	if calls != 1 || a != b || a.err != nil || a.info.Table != "other" {
		t.Fatalf("calls %d, same %v, meta %+v", calls, a == b, a)
	}
}

// A build that panics with NotLinkedError, as generated code does before its
// package's init binds the schema, is not kept: the next Load builds again,
// and its Meta is kept. Any other failure is kept.
func TestMetaOnceRetriesNotLinked(t *testing.T) {
	var o MetaOnce[other]
	linked, calls := false, 0
	desc := func() Descriptor[other] {
		calls++
		if !linked {
			panic(NotLinkedError("other: not linked: import the generated package"))
		}
		return Descriptor[other]{Table: "other", Columns: []ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			FieldPtr: func(x *other, i int) any { return &x.ID }}
	}
	for range 2 {
		m := o.Load(desc)
		if m.err == nil || !strings.Contains(m.err.Error(), "other: not linked: import the generated package") || m.info != nil {
			t.Fatalf("before the link: %+v", m)
		}
	}
	linked = true
	a, b := o.Load(desc), o.Load(desc)
	if calls != 3 || a != b || a.err != nil || a.info.Table != "other" {
		t.Fatalf("calls %d, same %v, meta %+v", calls, a == b, a)
	}

	var bad MetaOnce[other]
	n := 0
	fail := func() Descriptor[other] { n++; panic("broken") }
	if c, d := bad.Load(fail), bad.Load(fail); c != d || n != 1 {
		t.Fatalf("a failure that is not NotLinkedError was built %d times", n)
	}
}

// A build whose Policy, Hooks or Interceptors panic with NotLinkedError, as
// one that names an entity not linked yet does, is not kept either.
func TestMetaOnceRetriesNotLinkedBinding(t *testing.T) {
	var o MetaOnce[other]
	linked, calls := false, 0
	desc := func() Descriptor[other] {
		return Descriptor[other]{Table: "other", Columns: []ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			Policy: BindPolicy(func() Policy[other] {
				calls++
				if !linked {
					panic(NotLinkedError("target: not linked: import the generated package"))
				}
				return Policy[other]{}
			}),
			FieldPtr: func(x *other, i int) any { return &x.ID }}
	}
	m := o.Load(desc)
	var se *SchemaError
	if !errors.As(m.err, &se) || !strings.Contains(m.err.Error(), "target: not linked") || m.info != nil {
		t.Fatalf("before the link: %+v", m)
	}
	linked = true
	a, b := o.Load(desc), o.Load(desc)
	if calls != 2 || a != b || a.err != nil {
		t.Fatalf("calls %d, same %v, meta %+v", calls, a == b, a)
	}
}

// Show is the value a pointer points to, or nil.
func TestShow(t *testing.T) {
	n := 5
	if got := Show(&n); got != 5 {
		t.Errorf("Show(&5) = %v", got)
	}
	if got := Show[int](nil); got != nil {
		t.Errorf("Show(nil) = %v", got)
	}
}

func TestColumnTypeIsTheFieldsType(t *testing.T) {
	info, err := loadDescriptor(orderedDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range info.Columns {
		f, ok := reflect.TypeFor[ordered]().FieldByName(c.Field)
		if !ok {
			t.Fatalf("ordered has no field %s", c.Field)
		}
		if _, isType := c.Type.(reflect.Type); !isType {
			t.Errorf("%s: Type is a %T, not a reflect.Type", c.Field, c.Type)
		}
		if GoType(c) != f.Type {
			t.Errorf("%s: GoType %v, want %v", c.Field, GoType(c), f.Type)
		}
	}
}

// TestDescriptorEntityInfo checks what a Descriptor's literals become in the EntityInfo. The names, the table and the keys
// are the Descriptor's own; velox derives none of them.
func TestDescriptorEntityInfo(t *testing.T) {
	info, err := loadDescriptor(orderedDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "velox.ordered" || info.Table != "orders" {
		t.Errorf("name %q table %q", info.Name, info.Table)
	}
	var names []string
	for _, c := range info.Columns {
		names = append(names, c.Name)
	}
	if want := []string{"created_at", "updated_at", "id", "tenant_id", "order_code", "total", "ship_at"}; !reflect.DeepEqual(names, want) {
		t.Errorf("columns %v, want %v", names, want)
	}
	for i, c := range info.Columns {
		if c.Nullable != (c.Field == "ShipAt") {
			t.Errorf("column %d (%s) nullable = %v", i, c.Field, c.Nullable)
		}
	}
	if got := GoType(info.Columns[6]); got != reflect.TypeFor[*time.Time]() {
		t.Errorf("GoType(ship_at) = %v", got)
	}
	if got := GoType(ColumnInfo{}); got != nil {
		t.Errorf("GoType of a column without a type = %v, want nil", got)
	}
	total := info.Columns[5]
	if total.HasDefault {
		t.Errorf("total: %+v", total)
	}
	if code := info.Columns[4]; !code.HasDefault || code.Default != "none" {
		t.Errorf("code: %+v", code)
	}
	if info.Columns[1].UpdateDefault == nil {
		t.Error("the UpdateDefault of updated_at was not applied")
	}
	if !reflect.DeepEqual(info.PrimaryKey, []int{2}) {
		t.Errorf("primary key %v", info.PrimaryKey)
	}
	if want := []IndexInfo{{Columns: []int{3, 4}, Unique: true}}; !reflect.DeepEqual(info.Indexes, want) {
		t.Errorf("indexes %+v", info.Indexes)
	}
	if i, ok := info.Column("order_code"); !ok || i != 4 {
		t.Errorf("Column(order_code) = %d, %v", i, ok)
	}
}
