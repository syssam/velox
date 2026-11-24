package apitest

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// keyColumns is the names of in's primary-key columns, in key order.
func keyColumns(in *velox.EntityInfo) []string {
	var out []string
	for _, k := range in.PrimaryKey {
		out = append(out, in.Columns[k].Name)
	}
	return out
}

func TestEntitiesListsThePackage(t *testing.T) {
	infos, err := velox.Entities[testmodel.Invoice]()
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		name, table string
		key         []string
	}
	var got []row
	for _, in := range infos {
		got = append(got, row{in.Name, in.Table, keyColumns(in)})
	}
	id := []string{"id"}
	want := []row{
		{"testmodel.Event", "events", id},
		{"testmodel.Invoice", "invoices", id},
		{"testmodel.Item", "items", id},
		{"testmodel.Ledger", "ledgers", id},
		{"testmodel.Line", "line", id},
		{"testmodel.Paper", "papers", id},
		{"testmodel.PaperTopic", "paper_topics", []string{"paper_id", "topic_id"}},
		{"testmodel.Player", "players", id},
		{"testmodel.Remark", "remarks", id},
		{"testmodel.Roster", "rosters", id},
		{"testmodel.Sample", "samples", id},
		{"testmodel.Team", "teams", id},
		{"testmodel.Ticket", "tickets", id},
		{"testmodel.Topic", "topics", id},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Entities:\n got %v\nwant %v", got, want)
	}

	// Any entity of the package gives the same list.
	again, err := velox.Entities[testmodel.PaperTopic]()
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(infos) {
		t.Fatalf("Entities[PaperTopic] has %d entities, Entities[Invoice] %d", len(again), len(infos))
	}
	for i := range again {
		if again[i].Name != infos[i].Name || again[i].Table != infos[i].Table {
			t.Fatalf("entity %d: %s %s, then %s %s", i, infos[i].Name, infos[i].Table, again[i].Name, again[i].Table)
		}
	}

	// A column's nullability is there.
	inv := infos[slices.IndexFunc(infos, func(in *velox.EntityInfo) bool { return in.Table == "invoices" })]
	c, ok := inv.Column("paid_at")
	if !ok || !inv.Columns[c].Nullable {
		t.Fatalf("invoices.paid_at: found %v, nullable %v", ok, ok && inv.Columns[c].Nullable)
	}
}

func TestEntitiesReturnsCopies(t *testing.T) {
	first, err := velox.Entities[testmodel.Invoice]()
	if err != nil {
		t.Fatal(err)
	}
	inv := first[slices.IndexFunc(first, func(in *velox.EntityInfo) bool { return in.Table == "invoices" })]
	if len(inv.Indexes) == 0 {
		t.Fatal("invoices has no index to change")
	}
	inv.Table = "x"
	inv.Columns[0].Name = "x"
	inv.PrimaryKey[0] = 5
	inv.Indexes[0].Columns[0] = 5
	inv.Indexes[0].Unique = false

	second, err := velox.Entities[testmodel.Invoice]()
	if err != nil {
		t.Fatal(err)
	}
	inv = second[slices.IndexFunc(second, func(in *velox.EntityInfo) bool { return in.Name == "testmodel.Invoice" })]
	if inv.Table != "invoices" || inv.Columns[0].Name != "id" || inv.PrimaryKey[0] != 0 ||
		inv.Indexes[0].Columns[0] == 5 || !inv.Indexes[0].Unique {
		t.Fatalf("a change to a returned value reached velox's metadata: %s %s %v %v", inv.Table, inv.Columns[0].Name, inv.PrimaryKey, inv.Indexes)
	}
}

func TestEntitiesCopiesDefaults(t *testing.T) {
	infos, err := velox.Entities[entsShared]()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("%d entities", len(infos))
	}
	infos[0].Columns[1].Default.([]string)[0] = "changed"

	infos, err = velox.Entities[entsShared]()
	if err != nil {
		t.Fatal(err)
	}
	if got := infos[0].Columns[1].Default.([]string)[0]; got != "a" {
		t.Fatalf("Default is %q after a caller changed a copy", got)
	}
}

func TestEntitiesRefuses(t *testing.T) {
	// A hand-written entity names no table of entities.
	if _, err := velox.Entities[dshared](); err == nil || !strings.Contains(err.Error(), "has no entity table") {
		t.Fatalf("hand-written entity: %v", err)
	}
	// E's own error is returned.
	if _, err := velox.Entities[badDescriptor](); err == nil {
		t.Fatal("an entity whose metadata fails: no error")
	}
	// So is the error of another entity of the package.
	infos, err := velox.Entities[entsGood]()
	if err == nil || infos != nil || !strings.Contains(err.Error(), "entsBroken") {
		t.Fatalf("a package with a broken entity: %v, %d entities", err, len(infos))
	}
}

var entsTable velox.EntityTable

func entsTableOf() *velox.EntityTable {
	return entsTable.Load(func() []velox.EntityLink {
		return []velox.EntityLink{
			velox.LinkEntity[entsGood]("Good", "goods"),
			velox.LinkEntity[entsBroken]("Broken", "brokens"),
		}
	})
}

type entsGood struct{ ID int64 }

var entsGoodMeta velox.MetaOnce[entsGood]

func (*entsGood) VeloxMeta() *velox.Meta[entsGood] {
	return entsGoodMeta.Load(func() velox.Descriptor[entsGood] {
		return velox.Descriptor[entsGood]{Table: "goods", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			Entities: entsTableOf, FieldPtr: func(x *entsGood, _ int) any { return &x.ID }}
	})
}

type entsBroken struct{ ID int64 }

var entsBrokenMeta velox.MetaOnce[entsBroken]

func (*entsBroken) VeloxMeta() *velox.Meta[entsBroken] {
	return entsBrokenMeta.Load(func() velox.Descriptor[entsBroken] {
		return velox.Descriptor[entsBroken]{Table: "brokens", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{3},
			Entities: entsTableOf, FieldPtr: func(x *entsBroken, _ int) any { return &x.ID }}
	})
}

type entsShared struct {
	ID int64
	S  []string
}

var (
	entsSharedMeta  velox.MetaOnce[entsShared]
	entsSharedTable velox.EntityTable
)

func entsSharedTableOf() *velox.EntityTable {
	return entsSharedTable.Load(func() []velox.EntityLink {
		return []velox.EntityLink{velox.LinkEntity[entsShared]("Shared", "shareds")}
	})
}

func (*entsShared) VeloxMeta() *velox.Meta[entsShared] {
	return entsSharedMeta.Load(func() velox.Descriptor[entsShared] {
		enc := func(v any) ([]byte, error) { return nil, nil }
		dec := func([]byte, any) error { return nil }
		return velox.Descriptor[entsShared]{
			Table: "shareds",
			Columns: []velox.ColumnDescriptor{
				{Field: "ID", Name: "id"},
				{Field: "S", Name: "s", Default: []string{"a"}, Encode: enc, Decode: dec},
			},
			PrimaryKey: []int{0},
			Entities:   entsSharedTableOf,
			FieldPtr: func(x *entsShared, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.S
			},
		}
	})
}
