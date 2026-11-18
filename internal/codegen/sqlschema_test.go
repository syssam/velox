package codegen

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/load"
)

// ssForms uses every spelling of the index and default annotations that velox
// holds: a type per dialect, Desc for all keys, OpClass for all and for one,
// several annotations on one index, and each form of a column default.
type ssForms struct{ schema.Schema }

func (ssForms) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("ss_forms")}
}
func (ssForms) Fields() []schema.Field {
	return []schema.Field{
		field.String("a"), field.String("b"), field.JSON("attrs", map[string]string{}),
	}
}
func (ssForms) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("attrs").Annotations(sqlschema.IndexTypes(map[string]string{"postgres": "GIN"})).StorageKey("ix_types"),
		index.Fields("a", "b").Annotations(sqlschema.Desc()).StorageKey("ix_desc_all"),
		index.Fields("a", "b").Annotations(sqlschema.OpClass("text_pattern_ops"), sqlschema.OpClassColumn("b", "varchar_pattern_ops")).StorageKey("ix_class_all"),
		index.Fields("b", "a").Annotations(sqlschema.DescColumns("b"), sqlschema.DescColumns("b")).StorageKey("ix_desc_cols"),
		index.Fields("a").Annotations(sqlschema.IndexWhere("a <> ''")).StorageKey("ix_where"),
	}
}

// ssDefaults has a default for each dialect and one expression for all.
type ssDefaults struct{ schema.Schema }

func (ssDefaults) Fields() []schema.Field {
	return []schema.Field{
		field.Int("n").Annotations(sqlschema.DefaultExprs(map[string]string{"postgres": "7", "sqlite3": "8", "mysql": "9"})),
		field.String("s").Annotations(sqlschema.DefaultExpr("lower('X')")),
	}
}

func TestSQLSchemaForms(t *testing.T) {
	tb := table(t, tables(t, velox.Postgres, ssForms{}), "ss_forms")
	type keys struct {
		desc []string
		ops  map[string]string
	}
	got := map[string]keys{}
	where := ""
	for _, ix := range tb.Indexes {
		got[ix.Name] = keys{ix.Desc, ix.OpClass}
		if ix.Name == "ix_where" {
			where = ix.Where
		}
	}
	want := map[string]keys{
		"ix_types":     {nil, nil},
		"ix_desc_all":  {[]string{"a", "b"}, nil},
		"ix_class_all": {nil, map[string]string{"a": "text_pattern_ops", "b": "varchar_pattern_ops"}},
		"ix_desc_cols": {[]string{"b"}, nil},
	}
	for name, w := range want {
		g, ok := got[name]
		if !ok || !slices.Equal(g.desc, w.desc) || !maps.Equal(g.ops, w.ops) {
			t.Errorf("index %s: Desc %v, OpClass %v (present %t), want %v, %v", name, g.desc, g.ops, ok, w.desc, w.ops)
		}
	}
	if where != "a <> ''" {
		t.Errorf("Where %q", where)
	}
	for ix, m := range map[string]string{"ix_types": "gin"} {
		for _, x := range tb.Indexes {
			if x.Name == ix && x.Method != m {
				t.Errorf("%s: Method %q, want %q", ix, x.Method, m)
			}
		}
	}
	for d, want := range map[velox.Dialect]string{velox.Postgres: "7", velox.SQLite: "8", velox.MySQL: "9"} {
		col := map[string]string{}
		for _, c := range table(t, tables(t, d, ssDefaults{}), "ss_defaults").Columns {
			col[c.Name] = c.Default
		}
		if col["n"] != want {
			t.Errorf("%v: n default %q, want %q", d, col["n"], want)
		}
	}
}

// ssRefused holds what the old velox's annotations can say and velox does not:
// each is refused with the schema's index or field named, and a default of two
// kinds or beside SchemaDefault is refused.
type ssRefused struct{ schema.Schema }

func (ssRefused) Fields() []schema.Field {
	return []schema.Field{
		field.String("a"),
		field.Int("n").Annotations(sqlschema.Default("1"), sqlschema.DefaultExpr("2")),
		field.Int("m").SchemaDefault(map[string]string{field.Postgres: "1"}).Annotations(sqlschema.Default("2")),
		field.Int("k").Annotations(sqlschema.DefaultExprs(map[string]string{"oracle": "1"})),
		field.Int("j").Annotations(sqlschema.DefaultExprs(map[string]string{"mysql": " "})),
		field.String("id").Annotations(sqlschema.Desc()),
		field.String("t").Annotations(sqlschema.Table("x")),
		field.String("sz").Annotations(sqlschema.Size(10)),
	}
}
func (ssRefused) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("peers", ssRefused.Type).Annotations(sqlschema.OnDelete(sqlschema.Cascade)),
		edge.To("others", ssRefused.Type).Annotations(sqlschema.Default("1")),
	}
}
func (ssRefused) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("a").Annotations(sqlschema.StorageParams("fillfactor=90")),
		index.Fields("a").Annotations(sqlschema.Prefix(10)),
		index.Fields("a").Annotations(sqlschema.IncludeColumns("n")),
		index.Fields("a").Annotations(sqlschema.IndexTypes(map[string]string{"mysql": "BTREE"})),
		index.Fields("a").Annotations(sqlschema.IndexType("gin"), sqlschema.IndexTypes(map[string]string{"postgres": "hash"})),
		index.Fields("a").Where("a <> ''").Annotations(sqlschema.IndexWhere("a <> 'x'")),
		index.Fields("a").Annotations(sqlschema.Default("1")),
	}
}

func TestSQLSchemaRefusals(t *testing.T) {
	b, err := load.Marshal("github.com/syssam/velox/internal/codegen", ssRefused{})
	if err != nil {
		t.Fatal(err)
	}
	var m loadjson.Model
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprintf("%+v", m.Errors)
	for _, w := range []string{
		"sqlschema.StorageParams is not held by velox",
		"sqlschema.Prefix and PrefixColumn are not held by velox",
		"sqlschema.IncludeColumns is not held by velox",
		`sqlschema.IndexTypes names the "mysql" dialect`,
		`sqlschema.IndexType "gin" and IndexTypes postgres "hash" disagree`,
		`the index has Where("a <> ''") and sqlschema.IndexWhere("a <> 'x'")`,
		"sqlschema.Default and DefaultExpr are both set",
		"the field has SchemaDefault and a sqlschema default",
		`sqlschema.DefaultExprs names the dialect "oracle"`,
		"sqlschema.DefaultExprs: the mysql default is empty",
		"a sqlschema.IndexAnnotation (IndexType, Desc, OpClass, IndexWhere and the like) is an index's: put it on index.Fields(...).Annotations, not on a field",
		"a sqlschema.Annotation is a schema's or a field's: put it there, not on an index",
		"sqlschema Table, Schema, Collation and Checks are a schema's: put them in the schema's Annotations, not on a field",
		"sqlschema Default, DefaultExpr and DefaultExprs are a field's: put them on the field, not on an edge",
		"sqlschema.Size is not held by velox: use field.MaxLen",
		"sqlschema.OnDelete is not held by velox: use edge.OnDelete",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("%q not in\n%s", w, got)
		}
	}
}

// Merge combines two annotations of a name, a later non-zero field winning.
func TestSQLSchemaMerge(t *testing.T) {
	a := sqlschema.IndexType("gin")
	m, _ := a.Merge(sqlschema.DescColumns("x")).(sqlschema.IndexAnnotation)
	m, _ = m.Merge(sqlschema.IndexType("hash")).(sqlschema.IndexAnnotation)
	if m.Type != "hash" || !m.DescColumns["x"] || a.Type != "gin" || a.DescColumns != nil {
		t.Errorf("merged %+v, a %+v", m, a)
	}
	f := sqlschema.Default("1")
	g, _ := f.Merge(sqlschema.DefaultExprs(map[string]string{"mysql": "2"})).(sqlschema.Annotation)
	if g.Default != "1" || g.DefaultExprs["mysql"] != "2" {
		t.Errorf("merged %+v", g)
	}
}

// ssOwner states its table, collation and checks as the old velox did, with
// a struct literal, a constructor and Checks.
type ssOwner struct{ schema.Schema }

func (ssOwner) Annotations() []schema.Annotation {
	return []schema.Annotation{
		sqlschema.Annotation{Table: "owners_tbl"},
		sqlschema.Collation("utf8mb4_bin"),
		sqlschema.Checks(map[string]string{"owners_a_ok": "a <> ''", "owners_b_ok": "b <> ''"}),
	}
}
func (ssOwner) Fields() []schema.Field {
	return []schema.Field{field.String("a"), field.String("b")}
}

func TestSQLSchemaSchemaLevel(t *testing.T) {
	g := mustBuild(t, ssOwner{})
	s, err := Tables(g, velox.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	tb := table(t, s, "owners_tbl")
	var names []string
	for _, c := range tb.Checks {
		names = append(names, c.Name)
	}
	if !slices.Equal(names, []string{"owners_a_ok", "owners_b_ok"}) {
		t.Errorf("checks %v", names)
	}
}

// ssBadSchema, ssNoTable and ssUnnamedCheck set what a schema cannot take from
// the old velox's Annotation; the loader reports the first of a list.
type ssBadSchema struct{ schema.Schema }

func (ssBadSchema) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Table("t"), sqlschema.Default("1")}
}
func (ssBadSchema) Fields() []schema.Field { return []schema.Field{field.String("a")} }

type ssNoTable struct{ schema.Schema }

func (ssNoTable) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Schema("tenant")}
}
func (ssNoTable) Fields() []schema.Field { return []schema.Field{field.String("a")} }

type ssUnnamedCheck struct{ schema.Schema }

func (ssUnnamedCheck) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Check("a > 0")}
}
func (ssUnnamedCheck) Fields() []schema.Field { return []schema.Field{field.String("a")} }

func TestSQLSchemaSchemaLevelRefusals(t *testing.T) {
	for s, want := range map[schema.Interface]string{
		ssBadSchema{}:    "sqlschema Default, DefaultExpr and DefaultExprs are a field's: put them on the field, not on a schema",
		ssNoTable{}:      "sqlschema.Schema without a Table",
		ssUnnamedCheck{}: "sqlschema.Check is the old velox's unnamed constraint",
	} {
		b, err := load.Marshal("github.com/syssam/velox/internal/codegen", s)
		if err != nil {
			t.Fatal(err)
		}
		var m loadjson.Model
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%+v", m.Errors); !strings.Contains(got, want) {
			t.Errorf("%T: %q not in\n%s", s, want, got)
		}
	}
}
