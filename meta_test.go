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
	s.Column(&x.Total).SQLType("numeric(18,2)")
	s.Column(&x.Code).Name("order_code").Default("none")
	s.Unique(&x.TenantID, &x.Code)
	s.Tenant(&x.TenantID)
}

var (
	orderCols = []string{"CreatedAt", "UpdatedAt", "ID", "TenantID", "Code", "Total", "ShipAt"}
	orderRels = []string{"Lines", "Owner"}
)

func infoOf[E any](cols, rels []string) (*EntityInfo, error) {
	x := new(E)
	rs := make([]Relation, len(rels))
	for i, n := range rels {
		rs[i] = Relation{name: n}
	}
	return buildInfo(reflect.TypeFor[E](), unsafe.Pointer(x), any(x), cols, rs)
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
	if got := GoType(info.Columns[6]); got != reflect.TypeFor[*time.Time]() {
		t.Errorf("GoType(ship_at) = %v", got)
	}
	if got := GoType(ColumnInfo{}); got != nil {
		t.Errorf("GoType of a column without a type = %v, want nil", got)
	}
	total := info.Columns[5]
	if total.SQLType != "numeric(18,2)" || total.HasDefault {
		t.Errorf("total: %+v", total)
	}
	if code := info.Columns[4]; !code.HasDefault || code.Default != "none" {
		t.Errorf("code: %+v", code)
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

type dupName struct {
	ID int64
	A  string
	B  string
}

func (x *dupName) Schema(s *Schema) { s.Column(&x.B).Name("a") }

type withID struct{ ID int64 }

type shadowed struct {
	withID
	ID int64
}

func (*shadowed) Schema(*Schema) {}

type emptyKeys struct {
	ID int64
	A  string
}

func (x *emptyKeys) Schema(s *Schema) {
	s.PrimaryKey()
	s.Unique()
	s.Index(&x.A, &x.A)
}

func TestDuplicateColumnNames(t *testing.T) {
	_, err := infoOf[dupName]([]string{"ID", "A", "B"}, nil)
	if err == nil || !strings.Contains(err.Error(), `column name "a" is also the name of A`) {
		t.Errorf("renamed column: %v", err)
	}
	_, err = infoOf[shadowed]([]string{"ID", "ID"}, nil)
	if err == nil || !strings.Contains(err.Error(), `column name "id" is also the name of ID`) {
		t.Errorf("shadowed field: %v", err)
	}
}

func TestEmptyKeysAndIndexes(t *testing.T) {
	_, err := infoOf[emptyKeys]([]string{"ID", "A"}, nil)
	for _, part := range []string{"the primary key names no fields", "a unique index names no fields", "an index names the field twice"} {
		if err == nil || !strings.Contains(err.Error(), part) {
			t.Errorf("%v does not mention %q", err, part)
		}
	}
}

func TestNotAColumnNamesTheField(t *testing.T) {
	_, err := infoOf[badSchema]([]string{"ID", "Name"}, nil)
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("%v is not a *SchemaError", err)
	}
	if se.Field != "hidden" {
		t.Fatalf("field %q, want hidden: %v", se.Field, err)
	}
}

type checked struct{ ID int64 }

func (*checked) Schema(*Schema) {}

var checkedMeta MetaOnce[checked]

func (*checked) VeloxMeta() *Meta[checked] {
	return checkedMeta.Get(func() Shape[checked] {
		return Shape[checked]{Columns: []string{"ID"}, Addr: func(x *checked, i int) any { return &x.ID }}
	})
}

type checkedStale struct{ ID int64 }

func (*checkedStale) Schema(*Schema) {}

var checkedStaleMeta MetaOnce[checkedStale]

func (*checkedStale) VeloxMeta() *Meta[checkedStale] {
	return checkedStaleMeta.Get(func() Shape[checkedStale] { return Shape[checkedStale]{} })
}

func TestCheckEntity(t *testing.T) {
	if err := CheckEntity[checked](); err != nil {
		t.Errorf("valid entity: %v", err)
	}
	if err := CheckEntity[checkedStale](); !errors.Is(err, ErrStale) {
		t.Errorf("stale entity: %v", err)
	}
}

type qualified struct{ ID int64 }

func (x *qualified) Schema(s *Schema) { s.TableIn("billing", "invoices") }

func TestTableIn(t *testing.T) {
	info, err := infoOf[qualified]([]string{"ID"}, nil)
	if err != nil || info.Schema != "billing" || info.Table != "invoices" {
		t.Fatalf("%+v, %v", info, err)
	}
}

type badDefaults struct {
	ID    int64
	Paid  bool
	Count int
	Name  string
	Opt   *bool
	Cnt   *int64
	Gen   int64
}

func (x *badDefaults) Schema(s *Schema) {
	s.Column(&x.Paid).Default(true)
	s.Column(&x.Count).Default(3)
	s.Column(&x.Name).Default("n")
	s.Column(&x.Opt).Default(new(bool))
	s.Column(&x.Cnt).Default(new(int64))
	s.Column(&x.Gen).DefaultFunc(func() int64 { return 5 })
}

func TestDefaultOnBoolOrNumericFieldIsASchemaError(t *testing.T) {
	_, err := infoOf[badDefaults]([]string{"ID", "Paid", "Count", "Name", "Opt", "Cnt", "Gen"}, nil)
	if err == nil {
		t.Fatal("no error")
	}
	var fields []string
	for _, e := range err.(interface{ Unwrap() []error }).Unwrap() {
		var se *SchemaError
		if !errors.As(e, &se) || !strings.Contains(se.Reason, "Default on bool/numeric field "+se.Field+" would overwrite an explicit false or 0 on insert; make the field a pointer") {
			t.Errorf("%v", e)
			continue
		}
		fields = append(fields, se.Field)
	}
	if want := []string{"Paid", "Count"}; !reflect.DeepEqual(fields, want) {
		t.Errorf("errors on %v, want %v (string, pointer and DefaultFunc stay legal)", fields, want)
	}
}

type outsider struct{ N int64 }

type reachesOut struct {
	ID int64
}

func (x *reachesOut) Schema(s *Schema) {
	o := new(outsider) // another object: its field is not inside the entity
	s.Column(&o.N)
}

func TestPointerOutsideTheEntityIsASchemaError(t *testing.T) {
	_, err := infoOf[reachesOut]([]string{"ID"}, nil)
	var se *SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "does not point into the entity") {
		t.Fatalf("got %v, want a *SchemaError that says the pointer does not point into the entity", err)
	}
}

type pkFields struct {
	ID int64
	A  string
	B  string
}

type keyedA struct{ pkFields }
type keyedAB struct{ pkFields }
type keyedBA struct{ pkFields }

func (x *keyedA) Schema(s *Schema)  { s.PrimaryKey(&x.A) }
func (x *keyedAB) Schema(s *Schema) { s.PrimaryKey(&x.A, &x.B) }
func (x *keyedBA) Schema(s *Schema) { s.PrimaryKey(&x.B, &x.A) }

func TestExplicitPrimaryKey(t *testing.T) {
	cols := []string{"ID", "A", "B"}
	for name, c := range map[string]struct {
		info func() (*EntityInfo, error)
		want []int
	}{
		"one field replaces the ID default": {func() (*EntityInfo, error) { return infoOf[keyedA](cols, nil) }, []int{1}},
		"composite":                         {func() (*EntityInfo, error) { return infoOf[keyedAB](cols, nil) }, []int{1, 2}},
		"composite keeps the given order":   {func() (*EntityInfo, error) { return infoOf[keyedBA](cols, nil) }, []int{2, 1}},
	} {
		info, err := c.info()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(info.PrimaryKey, c.want) {
			t.Errorf("%s: primary key %v, want %v", name, info.PrimaryKey, c.want)
		}
	}
}

type indexed struct {
	ID int64
	A  string
	B  string
}

func (x *indexed) Schema(s *Schema) {
	s.Index(&x.A)
	s.Index(&x.B, &x.A)
	s.Unique(&x.A, &x.B)
}

func TestIndexIsNotUnique(t *testing.T) {
	info, err := infoOf[indexed]([]string{"ID", "A", "B"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []IndexInfo{
		{Columns: []int{1}},
		{Columns: []int{2, 1}},
		{Columns: []int{1, 2}, Unique: true},
	}
	if !reflect.DeepEqual(info.Indexes, want) {
		t.Errorf("indexes %+v, want %+v", info.Indexes, want)
	}
}

type stamped struct {
	ID int64
	N  int64
	At string
}

var onUpdateCalls int

func (x *stamped) Schema(s *Schema) {
	s.Column(&x.N).OnUpdate(func() int64 { onUpdateCalls++; return 42 })
}

func TestOnUpdateReturnsTheGivenFunctionsValue(t *testing.T) {
	info, err := infoOf[stamped]([]string{"ID", "N", "At"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Columns[0].OnUpdate != nil || info.Columns[2].OnUpdate != nil {
		t.Error("OnUpdate set on a column that did not declare one")
	}
	f := info.Columns[1].OnUpdate
	if f == nil {
		t.Fatal("OnUpdate is nil on the column that declared one")
	}
	onUpdateCalls = 0
	if got := f(); got != int64(42) || onUpdateCalls != 1 {
		t.Errorf("OnUpdate() = %#v after %d calls, want int64(42) after 1", got, onUpdateCalls)
	}
}

type zeroSize struct {
	ID   int64
	A, B struct{} // two fields at one address: a pointer could not tell them apart
	Name string
}

func (x *zeroSize) Schema(s *Schema) { s.Column(&x.B).Name("b") }

func TestZeroSizeFieldIsRefused(t *testing.T) {
	_, err := infoOf[zeroSize]([]string{"ID", "A", "B", "Name"}, nil)
	var se *SchemaError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "field A has the zero-size type struct {}, which holds no value; tag it velox:\"-\"") {
		t.Errorf("%v", err)
	}
}

func TestColumnTypeIsTheFieldsType(t *testing.T) {
	info, err := infoOf[order](orderCols, orderRels)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range info.Columns {
		f, ok := reflect.TypeFor[order]().FieldByName(c.Field)
		if !ok {
			t.Fatalf("order has no field %s", c.Field)
		}
		if _, isType := c.Type.(reflect.Type); !isType {
			t.Errorf("%s: Type is a %T, not a reflect.Type", c.Field, c.Type)
		}
		if GoType(c) != f.Type {
			t.Errorf("%s: GoType %v, want %v", c.Field, GoType(c), f.Type)
		}
	}
}
