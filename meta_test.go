package velox

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"
)

type stamps struct {
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (t *stamps) Mixin(s *Schema) { s.Column(&t.UpdatedAt).OnUpdate(time.Now) }

type other struct{ ID int64 }

func (*other) Schema(*Schema) {}

type order struct {
	stamps
	ID       int64
	TenantID int64
	Code     string
	Total    float64
	ShipAt   *time.Time
	Lines    Many[other]
	Owner    One[other]
	Display  string `velox:"-"`
	cache    string
}

func (x *order) Schema(s *Schema) {
	s.Table("orders")
	s.Column(&x.Total).SQLType("numeric(18,2)").Default(1.5)
	s.Column(&x.Code).Name("order_code")
	s.Unique(&x.TenantID, &x.Code)
	s.Tenant(&x.TenantID)
}

var (
	orderCols = []string{"CreatedAt", "UpdatedAt", "ID", "TenantID", "Code", "Total", "ShipAt"}
	orderRels = []string{"Lines", "Owner"}
)

func infoOf[E any](cols, rels []string) (*EntityInfo, error) {
	x := new(E)
	return buildInfo(reflect.TypeFor[E](), unsafe.Pointer(x), any(x), cols, rels)
}

func TestEntityInfo(t *testing.T) {
	_ = order{cache: "never a column"}
	info, err := infoOf[order](orderCols, orderRels)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "velox.order" || info.Table != "orders" {
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
	total := info.Columns[5]
	if total.SQLType != "numeric(18,2)" || !total.HasDefault || total.Default != 1.5 {
		t.Errorf("total: %+v", total)
	}
	if info.Columns[1].OnUpdate == nil {
		t.Error("the mixin's OnUpdate on updated_at was not applied")
	}
	if !reflect.DeepEqual(info.PrimaryKey, []int{2}) || info.Tenant != 3 {
		t.Errorf("primary key %v tenant %d", info.PrimaryKey, info.Tenant)
	}
	if want := []IndexInfo{{Columns: []int{3, 4}, Unique: true}}; !reflect.DeepEqual(info.Indexes, want) {
		t.Errorf("indexes %+v", info.Indexes)
	}
	if i, ok := info.Column("order_code"); !ok || i != 4 {
		t.Errorf("Column(order_code) = %d, %v", i, ok)
	}
}

func TestStaleColumns(t *testing.T) {
	_, err := infoOf[order](orderCols[:6], orderRels)
	if !errors.Is(err, ErrStale) {
		t.Fatalf("got %v, want ErrStale", err)
	}
	want := "velox: generated code is out of date: velox.order: columns #6 is ShipAt in the struct and (none) in the generated code; run go tool velox-gen"
	if err.Error() != want {
		t.Errorf("message\n got %s\nwant %s", err, want)
	}
}

func TestStaleRelations(t *testing.T) {
	if _, err := infoOf[order](orderCols, []string{"Lines"}); !errors.Is(err, ErrStale) {
		t.Fatalf("got %v, want ErrStale", err)
	}
}

type badSchema struct {
	ID     int64
	Name   string
	hidden int
}

func (x *badSchema) Schema(s *Schema) {
	s.Column(&x.hidden).Name("h")
	s.Tenant(x.ID)
	s.Tenant(&x.ID)
	s.Tenant(&x.ID)
}

func TestSchemaErrors(t *testing.T) {
	_, err := infoOf[badSchema]([]string{"ID", "Name"}, nil)
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("got %v, want a *SchemaError", err)
	}
	for _, part := range []string{"is not a column", "expected a pointer to a field", "Tenant is declared twice"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("%q does not mention %q", err, part)
		}
	}
}

type noKey struct{ Name string }

func (*noKey) Schema(*Schema) {}

func TestNoPrimaryKey(t *testing.T) {
	_, err := infoOf[noKey]([]string{"Name"}, nil)
	if err == nil || !strings.Contains(err.Error(), "no primary key") {
		t.Fatalf("got %v", err)
	}
}

type lineItem struct{ ID int64 }

func (*lineItem) Schema(*Schema) {}

func TestTableDefaultsToSnakeCase(t *testing.T) {
	info, err := infoOf[lineItem]([]string{"ID"}, nil)
	if err != nil || info.Table != "line_item" {
		t.Fatalf("table %v, err %v", info, err)
	}
}

type embedsPointer struct {
	*stamps
	ID int64
}

func (*embedsPointer) Schema(*Schema) {}

func TestEmbeddedPointerIsRefused(t *testing.T) {
	_, err := infoOf[embedsPointer]([]string{"ID"}, nil)
	if err == nil || !strings.Contains(err.Error(), "embedded pointer") {
		t.Fatalf("got %v", err)
	}
}

func TestMetaOnceBuildsOnce(t *testing.T) {
	var o MetaOnce[other]
	calls := 0
	shape := func() Shape[other] {
		calls++
		return Shape[other]{Columns: []string{"ID"}, Addr: func(x *other, i int) any { return &x.ID }}
	}
	a, b := o.Get(shape), o.Get(shape)
	if calls != 1 || a != b || a.err != nil || a.info.Table != "other" {
		t.Fatalf("calls %d, same %v, meta %+v", calls, a == b, a)
	}
}

func TestRelationHolders(t *testing.T) {
	if _, loaded := (Many[other]{}).Get(); loaded {
		t.Error("a zero Many reports loaded")
	}
	if v, loaded := (One[other]{}).Get(); loaded || v != nil {
		t.Error("a zero One reports loaded")
	}
	if !isRelation(reflect.TypeFor[Many[other]]()) || isRelation(reflect.TypeFor[time.Time]()) {
		t.Error("isRelation")
	}
}
