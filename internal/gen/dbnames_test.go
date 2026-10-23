package gen

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2/internal/ddl"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/index"
)

func TestDerivedNames(t *testing.T) {
	long := strings.Repeat("t", 60)
	for got, want := range map[string]string{
		pkName("users"):            "users_pkey",
		seqName("users", "id"):     "users_id_seq",
		fkName("pets", "owner_id"): "pets_owner_id_fkey",
		checkName("users", "role"): "users_role_check",
		pkName(long):               ddl.Cap(long + "_pkey"),
		seqName(long, "id"):        ddl.Cap(long + "_id_seq"),
	} {
		if got != want || len(got) > ddl.MaxName {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

type dnOrderStatus string

func (dnOrderStatus) Values() []string { return []string{"open", "closed"} }

type dnOrder struct{ schema.Schema }

func (dnOrder) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table("orders")} }
func (dnOrder) Fields() []schema.Field {
	return []schema.Field{
		field.Enum("plain").Values("a"),
		field.EnumOf[dnOrderStatus]("status"),
		field.Enum("typed").GoType(dnOrderStatus("")),
		field.Enum("named").Values("a").SchemaType(map[string]string{field.Postgres: "mood"}),
		field.Enum("text").Values("a").SchemaType(map[string]string{field.Postgres: "varchar(9)"}),
		field.Enum(strings.Repeat("e", 60)).Values("a"),
	}
}

func TestEnumType(t *testing.T) {
	e := mustBuild(t, dnOrder{}).Entity("dnOrder")
	for _, c := range []struct {
		field, name string
		native      bool
	}{
		{"plain", "orders_plain", true},
		{"status", "dn_order_status", true},
		{"typed", "dn_order_status", true},
		{"named", "mood", true},
		{"text", "", false},
		{strings.Repeat("e", 60), ddl.Cap("orders_" + strings.Repeat("e", 60)), true},
	} {
		name, native := enumType(e, e.Field(c.field))
		if name != c.name || native != c.native {
			t.Errorf("%s: %q %v, want %q %v", c.field, name, native, c.name, c.native)
		}
	}
}

// dnOwner's pets are an O2M relation with an inverse, named on either edge;
// its groups a many-to-many one with a join table.
type dnOwner struct{ schema.Schema }

func (dnOwner) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("pets", dnPet.Type),
		edge.To("groups", dnGroup.Type).StorageKey(edge.Symbols("m_owner", "m_group")),
		edge.To("toys", dnToy.Type).OnDelete(edge.Cascade),
	}
}

type dnPet struct{ schema.Schema }

func (dnPet) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", dnOwner.Type).Ref("pets").Unique().StorageKey(edge.Symbol("pet_owner"))}
}
func (dnPet) Indexes() []schema.Index { return []schema.Index{index.Edges("owner").Where("x")} }

type dnGroup struct{ schema.Schema }

func (dnGroup) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owners", dnOwner.Type).Ref("groups")}
}

type dnToy struct{ schema.Schema }

func (dnToy) Fields() []schema.Field  { return []schema.Field{field.String("name")} }
func (dnToy) Indexes() []schema.Index { return []schema.Index{index.Fields("dn_owner_toys", "name")} }

func TestSymbols(t *testing.T) {
	g := mustBuild(t, dnOwner{}, dnPet{}, dnGroup{}, dnToy{})
	owner, pet, group := g.Entity("dnOwner"), g.Entity("dnPet"), g.Entity("dnGroup")
	for _, c := range []struct {
		e    *Entity
		edge string
		want []string
	}{
		{owner, "pets", []string{"pet_owner"}},
		{pet, "owner", []string{"pet_owner"}},
		{group, "owners", []string{"m_owner", "m_group"}},
		{owner, "toys", nil},
	} {
		if got := symbols(g, c.e, c.e.Edge(c.edge)); !slices.Equal(got, c.want) || (got == nil) != (c.want == nil) {
			t.Errorf("%s.%s: %q, want %q", c.e.Name, c.edge, got, c.want)
		}
	}
}

func TestJoinTablesAndForeignKeys(t *testing.T) {
	g := mustBuild(t, dnOwner{}, dnPet{}, dnGroup{}, dnToy{})
	jts := joinTables(g)
	if len(jts) != 1 {
		t.Fatalf("join tables %+v", jts)
	}
	jt := jts[0]
	if jt.Table != "dn_owner_groups" || jt.Columns != [2]string{"dn_owner_id", "dn_group_id"} || jt.Owner.Name != "dnOwner" ||
		jt.Target.Name != "dnGroup" || jt.Edge.Name != "groups" || !slices.Equal(jt.Symbols, []string{"m_owner", "m_group"}) {
		t.Errorf("join table %+v", jt)
	}
	var got []string
	for _, fk := range foreignKeys(g) {
		got = append(got, fmt.Sprintf("%s.%s %s -> %s %q by %s.%s", fk.Table, fk.Column, fk.Name, fk.Ref.Name, fk.OnDelete, fk.Owner.Name, fk.Edge.Name))
	}
	want := []string{
		`dn_owner_groups.dn_group_id m_group -> dnGroup "CASCADE" by dnOwner.groups`,
		`dn_owner_groups.dn_owner_id m_owner -> dnOwner "CASCADE" by dnOwner.groups`,
		`dn_pets.dn_owner_pets pet_owner -> dnOwner "" by dnPet.owner`,
		`dn_toys.dn_owner_toys dn_toys_dn_owner_toys_fkey -> dnOwner "CASCADE" by dnOwner.toys`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestFKIndexed(t *testing.T) {
	pk := []string{"id"}
	ixs := [][]string{{"a", "b"}}
	for column, want := range map[string]bool{"id": true, "a": true, "b": false, "c": false} {
		if got := fkIndexed(pk, ixs, column); got != want {
			t.Errorf("%s: %v, want %v", column, got, want)
		}
	}
}

// objects lists every name the graph puts in a database, in its space.
func TestObjects(t *testing.T) {
	g := mustBuild(t, dnOwner{}, dnPet{}, dnGroup{}, dnToy{}, dnOrder{})
	spaces := map[nameSpace][]string{}
	for _, o := range objects(g, nil) {
		spaces[o.space] = append(spaces[o.space], o.name)
	}
	for _, c := range []struct {
		space nameSpace
		has   []string
		lacks []string
	}{
		{spaceRelations, []string{
			"orders", "dn_owners", "dn_owner_groups", "velox_new_orders", "velox_new_dn_owner_groups", "orders_pkey",
			"orders_id_seq", "dn_owner_groups_pkey", "dn_owner_groups_dn_group_id", "dn_pets_dn_owner_pets",
			"dn_pets_dn_owner_pets_fd0c5087", "dn_toys_dn_owner_toys_name",
		}, []string{"dn_owner_groups_id_seq", "dn_owner_groups_dn_owner_id", "dn_toys_dn_owner_toys", "orders_plain"}},
		{spaceTypes, []string{"orders", "dn_owner_groups", "orders_plain", "dn_order_status", "mood", "orders_plain_velox_old", "mood_velox_old"},
			[]string{"orders_text", "orders_pkey"}},
		{spaceConstraints, []string{
			"m_owner", "m_group", "pet_owner", "dn_toys_dn_owner_toys_fkey", "orders_plain_check", "orders_text_check",
			"orders_id_not_null", "orders_plain_not_null", "dn_owner_groups_dn_owner_id_not_null", "dn_toys_name_not_null",
		}, []string{"dn_pets_dn_owner_pets_not_null"}},
	} {
		for _, n := range c.has {
			if !slices.Contains(spaces[c.space], n) {
				t.Errorf("space %d lacks %s", c.space, n)
			}
		}
		for _, n := range c.lacks {
			if slices.Contains(spaces[c.space], n) {
				t.Errorf("space %d has %s", c.space, n)
			}
		}
	}
	// The shared enum type and its helper are listed once.
	if n := strings.Count(strings.Join(spaces[spaceTypes], " ")+" ", "dn_order_status_velox_old "); n != 1 {
		t.Errorf("dn_order_status_velox_old is listed %d times", n)
	}
	// A refused index is left out.
	pet := g.Entity("dnPet")
	for _, o := range objects(g, map[*Index]bool{pet.Indexes[0]: true}) {
		if o.name == pet.Indexes[0].Name {
			t.Errorf("the refused index %s is listed", o.name)
		}
	}
}
