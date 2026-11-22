package codegen

import (
	"strings"
	"testing"

	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// nuCustomer is a veloxe application's Customer: its to-one edge to its own
// schema is many-to-one in intent, which sqlschema.NonUniqueFK says.
type nuCustomer struct{ schema.Schema }

func (nuCustomer) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Table("nu_customers")}
}
func (nuCustomer) Fields() []schema.Field {
	return []schema.Field{field.Int64("merged_into_id").Optional().Nillable()}
}
func (nuCustomer) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("merged_into", nuCustomer.Type).Field("merged_into_id").Unique().Annotations(sqlschema.NonUniqueFK()),
	}
}

// nuAllocation's one-to-one pair to its own schema keeps a key many rows may
// hold, NonUniqueFK being on its inverse edge.
type nuAllocation struct{ schema.Schema }

func (nuAllocation) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Table("nu_allocations")}
}
func (nuAllocation) Fields() []schema.Field {
	return []schema.Field{field.Int64("reversed_by_id").Optional().Nillable()}
}
func (nuAllocation) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("reversed_by", nuAllocation.Type).Field("reversed_by_id").Unique(),
		edge.From("reversal_of", nuAllocation.Type).Ref("reversed_by").Unique().Annotations(sqlschema.NonUniqueFK()),
	}
}

// nuPlain is nuCustomer without NonUniqueFK: a bidirectional one-to-one
// edge, whose key is unique.
type nuPlain struct{ schema.Schema }

func (nuPlain) Fields() []schema.Field {
	return []schema.Field{field.Int64("spouse_id").Optional().Nillable()}
}
func (nuPlain) Edges() []schema.Edge {
	return []schema.Edge{edge.To("spouse", nuPlain.Type).Field("spouse_id").Unique()}
}

// sqlschema.NonUniqueFK keeps a to-one edge's key from being unique, as in
// veloxe; on an edge to its own schema with no inverse it is many-to-one, so
// nothing re-points a partner (decision 0090).
func TestNonUniqueFK(t *testing.T) {
	g := mustBuild(t, nuCustomer{}, nuAllocation{}, nuPlain{})
	c := g.Entity("nuCustomer")
	if ed := c.Edge("merged_into"); ed.Rel.Kind != "M2O" || ed.Bidi || c.Field("merged_into_id").Unique {
		t.Errorf("merged_into: kind %s, bidi %v, key unique %v; want M2O, false, false", ed.Rel.Kind, ed.Bidi, c.Field("merged_into_id").Unique)
	}
	a := g.Entity("nuAllocation")
	if ed := a.Edge("reversed_by"); ed.Rel.Kind != "O2O" || a.Field("reversed_by_id").Unique {
		t.Errorf("reversed_by: kind %s, key unique %v; want O2O, false", ed.Rel.Kind, a.Field("reversed_by_id").Unique)
	}
	p := g.Entity("nuPlain")
	if ed := p.Edge("spouse"); ed.Rel.Kind != "O2O" || !ed.Bidi || !p.Field("spouse_id").Unique {
		t.Errorf("spouse: kind %s, bidi %v, key unique %v; want O2O, true, true", ed.Rel.Kind, ed.Bidi, p.Field("spouse_id").Unique)
	}
	for _, d := range allDialects {
		s := tables(t, d, nuCustomer{}, nuAllocation{})
		for tb, col := range map[string]string{"nu_customers": "merged_into_id", "nu_allocations": "reversed_by_id"} {
			for _, ix := range table(t, s, tb).Indexes {
				if ix.Unique && len(ix.Columns) == 1 && ix.Columns[0] == col {
					t.Errorf("%v: %s has the unique index %s on %s", d, tb, ix.Name, col)
				}
			}
		}
	}
}

// zdFields has a NOT NULL column of each type, with no Default.
type zdFields struct{ schema.Schema }

func (zdFields) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Table("zd_fields")}
}
func (zdFields) Fields() []schema.Field {
	return []schema.Field{
		field.String("s"), field.String("os").Optional(), field.Bool("b"), field.Int("i"), field.Float("f"),
		field.Uint8("u8").Optional(),
		field.String("ns").Optional().Nillable(),
		field.Enum("e").Values("a"), field.Time("t"), field.JSON("j", []string{}), field.Bytes("by"),
		field.String("fn").DefaultFunc(func() string { return "x" }),
		field.Time("ot").Optional(),
	}
}

// A string, bool or number column that is NOT NULL and has no Default gets
// its zero as its DEFAULT, as veloxe's FeatureAutoDefault gave it; others
// get none.
func TestZeroColumnDefaults(t *testing.T) {
	want := map[string][3]string{ // PostgreSQL, MySQL, SQLite
		"s":  {"''", "''", "''"},
		"os": {"''", "''", "''"},
		"b":  {"false", "0", "0"},
		"i":  {"0", "0", "0"},
		"f":  {"0", "0", "0"},
		"u8": {"0", "0", "0"},
		"ns": {"", "", ""},
		"e":  {"", "", ""},
		"t":  {"", "", ""},
		"j":  {"", "", ""},
		"by": {"", "", ""},
		"fn": {"", "", ""},
		"ot": {"", "", ""},
	}
	for i, d := range allDialects {
		tb := table(t, tables(t, d, zdFields{}), "zd_fields")
		for name, w := range want {
			if c := column(t, tb, name); c.Default != w[i] {
				t.Errorf("%v: %s default %q, want %q", d, name, c.Default, w[i])
			}
		}
	}
}

// ojFields has the Optional JSON fields veloxe zero-filled: a slice, a map,
// a struct and a pointer, none Nillable, beside a required one.
type ojFields struct{ schema.Schema }

func (ojFields) Fields() []schema.Field {
	return []schema.Field{
		field.Strings("tags").Optional(),
		field.JSON("attrs", map[string]any{}).Optional(),
		field.JSON("shape", JSONShape{}).Optional(),
		field.JSON("ptr", &JSONShape{}).Optional(),
		field.JSON("req", []string{}),
		field.JSON("given", []string{}).Optional().Default([]string{"x"}),
		field.JSON("changes", []struct {
			Field string `json:"field"`
			Old   any    `json:"old"`
		}{}).Optional(),
	}
}

// An Optional JSON column is NULL-able; unset, a slice or a map is written
// empty, [] or {}, and a pointer as a pointer to its element's zero, by a
// DefaultFunc in its descriptor, as veloxe wrote them. A struct needs none:
// its zero value is written. An unnamed struct is written inline.
func TestOptionalJSONZeroFill(t *testing.T) {
	o := mustLower(t, Config{Package: "gen", SchemaPath: lowerSchemaPath}, ojFields{})
	e := o.entity("ojFields")
	for name, want := range map[string]string{
		"tags":    "func() any { return []string{} }",
		"attrs":   "func() any { return map[string]any{} }",
		"shape":   "",
		"ptr":     "func() any { return new(codegen.JSONShape) }",
		"req":     "",
		"given":   "",
		"changes": "func() any { return []struct {",
	} {
		got := e.field(name).ZeroFillCode
		if name == "changes" {
			got = got[:min(len(got), len(want))]
		}
		if got != want {
			t.Errorf("%s: zero fill %q, want %q", name, got, want)
		}
	}
	for _, d := range allDialects {
		tb := table(t, tables(t, d, ojFields{}), "oj_fields")
		for name, null := range map[string]bool{"tags": true, "attrs": true, "shape": true, "ptr": true, "changes": true, "req": false} {
			if c := column(t, tb, name); c.Nullable != null {
				t.Errorf("%v: %s nullable %v, want %v", d, name, c.Nullable, null)
			}
		}
	}
	g := mustBuild(t, ojFields{})
	files, err := Generate(g, Config{Package: "gen", SchemaPath: lowerSchemaPath})
	if err != nil {
		t.Fatal(err)
	}
	var src string
	for _, f := range files {
		src += string(f.Data)
	}
	if !strings.Contains(src, "Changes []struct {") {
		t.Errorf("the unnamed struct is not written inline:\n%s", src)
	}
}
