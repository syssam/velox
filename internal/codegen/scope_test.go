package codegen

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// The schemas of the scope tests. Each one's org_id is scoped by key "org"
// unless its name says otherwise.
func orgID() *field.NumberBuilder[int64] { return field.Int64("org_id").Immutable() }

type ScNote struct{ schema.Schema }

func (ScNote) Fields() []schema.Field {
	return []schema.Field{orgID().Annotations(schema.Scoped("org")), field.String("title")}
}

type ScTask struct{ schema.Schema }

func (ScTask) Fields() []schema.Field {
	return []schema.Field{orgID().Annotations(schema.Scoped("org"))}
}

// ScHome is scoped by a string, under another key.
type ScHome struct{ schema.Schema }

func (ScHome) Fields() []schema.Field {
	return []schema.Field{field.String("region").Immutable().Annotations(schema.Scoped("region"))}
}

// ScGuest is scoped by a UUID, whose type needs an import.
type ScGuest struct{ schema.Schema }

func (ScGuest) Fields() []schema.Field {
	return []schema.Field{field.UUID("site", UUIDValue{}).Immutable().Annotations(schema.Scoped("site"))}
}

// ScLine's scoped column is also the foreign key of its edge.
type ScLine struct{ schema.Schema }

func (ScLine) Fields() []schema.Field {
	return []schema.Field{orgID().Annotations(schema.Scoped("org"))}
}

func (ScLine) Edges() []schema.Edge {
	return []schema.Edge{edge.To("owner", ScOwner.Type).Unique().Required().Immutable().Field("org_id")}
}

type ScOwner struct{ schema.Schema }

func TestLowerScopeResolves(t *testing.T) {
	o := mustLower(t, Config{}, ScNote{}, ScTask{})
	want := &OutScope{Key: "org", KeyVar: "OrgKey", Var: "scNoteScope", Field: "OrgID", Handle: "ScNotes.OrgID.EQ"}
	if got := o.entity("ScNote").Scope; got == nil || *got != *want {
		t.Errorf("ScNote.Scope = %+v, want %+v", got, want)
	}
	if got := o.entity("ScTask").Scope; got == nil || got.Var != "scTaskScope" || got.KeyVar != "OrgKey" {
		t.Errorf("ScTask.Scope = %+v", got)
	}
	if len(o.ScopeKeys) != 1 || o.ScopeKeys[0].Name != "org" || o.ScopeKeys[0].Var != "OrgKey" || o.ScopeKeys[0].GoType != "int64" {
		t.Errorf("ScopeKeys = %+v, want the one key org", o.ScopeKeys)
	}
	if got := mustLower(t, Config{}, ScOwner{}).entity("ScOwner").Scope; got != nil {
		t.Errorf("an entity without the annotation has Scope %+v", got)
	}
}

func TestLowerScopeStringAndImported(t *testing.T) {
	o := mustLower(t, Config{}, ScNote{}, ScHome{}, ScGuest{})
	var names []string
	types := map[string]*OutScopeKey{}
	for _, k := range o.ScopeKeys {
		names = append(names, k.Var)
		types[k.Var] = k
	}
	if !slices.Equal(names, []string{"OrgKey", "RegionKey", "SiteKey"}) {
		t.Fatalf("keys %v, want sorted OrgKey RegionKey SiteKey", names)
	}
	if types["RegionKey"].GoType != "string" || len(types["RegionKey"].Imports) != 0 {
		t.Errorf("RegionKey = %+v", types["RegionKey"])
	}
	if g := types["SiteKey"]; g.GoType != "codegen.UUIDValue" || !slices.Contains(g.Imports, "github.com/syssam/velox/internal/codegen") {
		t.Errorf("SiteKey = %+v, want the UUID type with its import", g)
	}
}

func TestLowerScopeForeignKeyField(t *testing.T) {
	o := mustLower(t, Config{}, ScLine{}, ScOwner{})
	if s := o.entity("ScLine").Scope; s == nil || s.Field != "OrgID" {
		t.Errorf("a scoped foreign-key field: Scope = %+v", s)
	}
}

type scMutable struct{ schema.Schema }

func (scMutable) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id").Annotations(schema.Scoped("org"))}
}

type scOptional struct{ schema.Schema }

func (scOptional) Fields() []schema.Field {
	return []schema.Field{field.String("org_id").Immutable().Optional().Annotations(schema.Scoped("org"))}
}

type scNillable struct{ schema.Schema }

func (scNillable) Fields() []schema.Field {
	return []schema.Field{orgID().Optional().Nillable().Annotations(schema.Scoped("org"))}
}

type scJSON struct{ schema.Schema }

func (scJSON) Fields() []schema.Field {
	return []schema.Field{field.JSON("org_id", map[string]int{}).Immutable().Annotations(schema.Scoped("org"))}
}

type scEnum struct{ schema.Schema }

func (scEnum) Fields() []schema.Field {
	return []schema.Field{field.Enum("org_id").Values("a", "b").Immutable().Annotations(schema.Scoped("org"))}
}

type scTwo struct{ schema.Schema }

func (scTwo) Fields() []schema.Field {
	return []schema.Field{
		orgID().Annotations(schema.Scoped("org")),
		field.String("region").Immutable().Annotations(schema.Scoped("region")),
	}
}

type scStringOrg struct{ schema.Schema }

func (scStringOrg) Fields() []schema.Field {
	return []schema.Field{field.String("org").Immutable().Annotations(schema.Scoped("org"))}
}

type scBadKey struct{ schema.Schema }

func (scBadKey) Fields() []schema.Field {
	return []schema.Field{orgID().Annotations(schema.Scoped("not a key"))}
}

func TestBuildScopedRefused(t *testing.T) {
	for _, c := range []struct {
		ss   []schema.Interface
		want string
	}{
		{[]schema.Interface{scMutable{}}, `schema scMutable: field "org_id": a Scoped field must be Immutable, so that an update cannot move a row to another scope`},
		{[]schema.Interface{scOptional{}}, `schema scOptional: field "org_id": a Scoped field cannot be Optional or Nillable: a missing value would mean every scope`},
		{[]schema.Interface{scNillable{}}, `schema scNillable: field "org_id": a Scoped field cannot be Optional or Nillable: a missing value would mean every scope`},
		{[]schema.Interface{scJSON{}}, `schema scJSON: field "org_id": a Scoped field must be an integer, string or UUID, not json`},
		{[]schema.Interface{scEnum{}}, `schema scEnum: field "org_id": a Scoped field must be an integer, string or UUID, not enum`},
		{[]schema.Interface{scTwo{}}, `schema scTwo: field "region": an entity has one Scoped field, and "org_id" is already`},
		{[]schema.Interface{ScNote{}, scStringOrg{}}, `schema scStringOrg: field "org": key "org" is int64 in ScNote and string here: a key has one type`},
		{[]schema.Interface{scBadKey{}}, `schema scBadKey: field "org_id": the Scoped key "not a key" has no Go name: use letters, digits and underscores`},
	} {
		_, err := build(t, c.ss...)
		if err == nil || !strings.Contains(plain(err), c.want) {
			t.Errorf("%T:\n got  %v\nwant %s", c.ss[len(c.ss)-1], err, c.want)
		}
	}
	mustBuild(t, ScNote{}, ScTask{}, ScHome{}, ScLine{}, ScOwner{})
}

// Two keys that make one Go identifier, or a key's identifier that another
// generated name already has, are refused: the generated package would not
// compile. plugin.GoName drops empty words, so org_id and org__id are both
// OrgIDKey.
type ScKeyA struct{ schema.Schema }

func (ScKeyA) Fields() []schema.Field {
	return []schema.Field{orgID().Annotations(schema.Scoped("org_id"))}
}

type ScKeyB struct{ schema.Schema }

func (ScKeyB) Fields() []schema.Field {
	return []schema.Field{orgID().Annotations(schema.Scoped("org__id"))}
}

// ScOrg has an enum whose type, ScOrgKey, is the variable of the key sc_org.
type ScOrg struct{ schema.Schema }

func (ScOrg) Fields() []schema.Field {
	return []schema.Field{field.Enum("key").Values("a", "b")}
}

type ScByOrg struct{ schema.Schema }

func (ScByOrg) Fields() []schema.Field {
	return []schema.Field{orgID().Annotations(schema.Scoped("sc_org"))}
}

func TestLowerScopeNameCollisions(t *testing.T) {
	for _, c := range []struct {
		ss   []schema.Interface
		want string
	}{
		{[]schema.Interface{ScKeyA{}, ScKeyB{}}, `scope key "org__id" variable OrgIDKey collides with scope key "org_id" variable OrgIDKey`},
		{[]schema.Interface{ScOrg{}, ScByOrg{}}, `scope key "sc_org" variable ScOrgKey collides with enum type ScOrgKey`},
	} {
		_, err := lower(t, Config{}, c.ss...)
		if err == nil || !strings.Contains(plain(err), c.want) {
			t.Errorf("%T:\n got  %v\nwant %s", c.ss[0], err, c.want)
		}
	}
	// The order of the schemas does not change what is reported.
	_, a := lower(t, Config{}, ScKeyA{}, ScKeyB{})
	_, b := lower(t, Config{}, ScKeyB{}, ScKeyA{})
	if a == nil || b == nil || plain(a) != plain(b) {
		t.Errorf("order changes the report:\n%v\n%v", a, b)
	}
}
