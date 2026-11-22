package sqlschema_test

import (
	"maps"
	"testing"

	"github.com/syssam/velox/dialect/sqlschema"
)

// Several index annotations on one index combine: a later non-zero field wins,
// maps join, and neither side changes.
func TestIndexAnnotationMerge(t *testing.T) {
	a := sqlschema.IndexAnnotation{
		Type: "gin", Types: map[string]string{"postgres": "gin"}, Where: "a",
		Desc: true, DescColumns: map[string]bool{"a": true}, OpClass: "c1",
		OpClassColumns: map[string]string{"a": "o1"},
	}
	b := sqlschema.IndexAnnotation{
		Type: "hash", Types: map[string]string{"mysql": "btree"}, Where: "b",
		DescColumns: map[string]bool{"b": true}, OpClass: "c2", OpClassColumns: map[string]string{"b": "o2"},
	}
	got, ok := a.Merge(&b).(sqlschema.IndexAnnotation)
	if !ok {
		t.Fatal("Merge did not return an IndexAnnotation")
	}
	if got.Type != "hash" || got.Where != "b" || !got.Desc || got.OpClass != "c2" {
		t.Errorf("scalars %+v", got)
	}
	if !maps.Equal(got.Types, map[string]string{"postgres": "gin", "mysql": "btree"}) ||
		!maps.Equal(got.DescColumns, map[string]bool{"a": true, "b": true}) ||
		!maps.Equal(got.OpClassColumns, map[string]string{"a": "o1", "b": "o2"}) {
		t.Errorf("collections %+v", got)
	}
	if a.Type != "gin" || len(a.DescColumns) != 1 || len(a.OpClassColumns) != 1 {
		t.Errorf("a changed: %+v", a)
	}
	// A value, a nil pointer and another annotation leave a as it is or merge.
	if v, _ := a.Merge(b).(sqlschema.IndexAnnotation); v.Type != "hash" {
		t.Errorf("value: %+v", v)
	}
	var none *sqlschema.IndexAnnotation
	if v, _ := a.Merge(none).(sqlschema.IndexAnnotation); v.Type != "gin" {
		t.Errorf("nil pointer: %+v", v)
	}
	if v, _ := a.Merge(sqlschema.Annotation{}).(sqlschema.IndexAnnotation); v.Type != "gin" {
		t.Errorf("other annotation: %+v", v)
	}
	if a.Name() != sqlschema.IndexAnnotationName {
		t.Errorf("name %q", a.Name())
	}
}

// Annotation combines the same way, over every field it holds.
func TestAnnotationMerge(t *testing.T) {
	a := sqlschema.Annotation{
		Table: "t", Schema: "s", Collation: "c", Checks: map[string]string{"a": "1"},
		Default: "1", DefaultExpr: "e", DefaultExprs: map[string]string{"postgres": "p"}, NonUniqueFK: true,
	}
	b := sqlschema.Annotation{
		Table: "t2", Schema: "s2", Collation: "c2", Checks: map[string]string{"b": "2"},
		Default: "2", DefaultExpr: "e2", DefaultExprs: map[string]string{"mysql": "m"},
	}
	got, ok := a.Merge(&b).(sqlschema.Annotation)
	if !ok {
		t.Fatal("Merge did not return an Annotation")
	}
	if got.Table != "t2" || got.Schema != "s2" || got.Collation != "c2" || got.Default != "2" ||
		got.DefaultExpr != "e2" || !got.NonUniqueFK {
		t.Errorf("merged %+v", got)
	}
	if !maps.Equal(got.Checks, map[string]string{"a": "1", "b": "2"}) ||
		!maps.Equal(got.DefaultExprs, map[string]string{"postgres": "p", "mysql": "m"}) {
		t.Errorf("maps %+v", got)
	}
	if a.Table != "t" || len(a.Checks) != 1 {
		t.Errorf("a changed: %+v", a)
	}
	var none *sqlschema.Annotation
	if v, _ := a.Merge(none).(sqlschema.Annotation); v.Table != "t" {
		t.Errorf("nil pointer: %+v", v)
	}
	if v, _ := a.Merge(sqlschema.IndexAnnotation{}).(sqlschema.Annotation); v.Table != "t" {
		t.Errorf("other annotation: %+v", v)
	}
	if v, _ := a.Merge(b).(sqlschema.Annotation); v.Table != "t2" {
		t.Errorf("value: %+v", v)
	}
	if a.Name() != sqlschema.AnnotationName {
		t.Errorf("name %q", a.Name())
	}
}

// Each constructor sets its one field, as the struct literal does.
func TestConstructors(t *testing.T) {
	m := map[string]string{"postgres": "x"}
	idx := map[string]sqlschema.IndexAnnotation{
		"IndexType":     *sqlschema.IndexType("GIN"),
		"IndexTypes":    *sqlschema.IndexTypes(m),
		"IndexWhere":    *sqlschema.IndexWhere("p"),
		"Desc":          *sqlschema.Desc(),
		"DescColumns":   *sqlschema.DescColumns("a", "b"),
		"OpClass":       *sqlschema.OpClass("o"),
		"OpClassColumn": *sqlschema.OpClassColumn("a", "o"),
	}
	if idx["IndexType"].Type != "GIN" || idx["IndexTypes"].Types["postgres"] != "x" || idx["IndexWhere"].Where != "p" ||
		!idx["Desc"].Desc || len(idx["DescColumns"].DescColumns) != 2 || idx["OpClass"].OpClass != "o" ||
		idx["OpClassColumn"].OpClassColumns["a"] != "o" {
		t.Errorf("index constructors: %+v", idx)
	}
	m["postgres"] = "changed"
	if idx["IndexTypes"].Types["postgres"] != "x" {
		t.Error("IndexTypes keeps the caller's map")
	}
	ann := map[string]sqlschema.Annotation{
		"Table": sqlschema.Table("t"), "SchemaTable": sqlschema.SchemaTable("s", "t"), "Schema": sqlschema.Schema("s"),
		"Collation": sqlschema.Collation("c"), "Checks": sqlschema.Checks(map[string]string{"a": "1"}),
		"NonUniqueFK": sqlschema.NonUniqueFK(),
		"Default":     sqlschema.Default("1"), "DefaultExpr": sqlschema.DefaultExpr("now()"),
		"DefaultExprs": sqlschema.DefaultExprs(map[string]string{"mysql": "m"}),
	}
	if ann["Table"].Table != "t" || ann["SchemaTable"].Schema != "s" || ann["SchemaTable"].Table != "t" || ann["Schema"].Schema != "s" ||
		ann["Collation"].Collation != "c" || ann["Checks"].Checks["a"] != "1" || !ann["NonUniqueFK"].NonUniqueFK ||
		ann["Default"].Default != "1" || ann["DefaultExpr"].DefaultExpr != "now()" || ann["DefaultExprs"].DefaultExprs["mysql"] != "m" {
		t.Errorf("annotation constructors: %+v", ann)
	}
}
