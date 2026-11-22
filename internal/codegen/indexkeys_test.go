package codegen

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// ikDocs has an index with a column sorted descending, one with an operator
// class, and one over a field and an edge's key with both.
type ikDocs struct{ schema.Schema }

func (ikDocs) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Table("ik_docs")}
}
func (ikDocs) Fields() []schema.Field {
	return []schema.Field{field.String("title"), field.Time("created_at"), field.JSON("attrs", map[string]string{})}
}
func (ikDocs) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("title", "created_at").Annotations(sqlschema.DescColumns("created_at")).StorageKey("ix_recent"),
		index.Fields("title").Annotations(sqlschema.OpClassColumn("title", "text_pattern_ops")).StorageKey("ix_title_prefix"),
		index.Fields("attrs").Annotations(sqlschema.IndexType("gin")).Annotations(sqlschema.OpClassColumn("attrs", "jsonb_path_ops")).StorageKey("ix_attrs_path"),
		index.Fields("created_at", "title").Annotations(sqlschema.DescColumns("created_at", "title")).Annotations(sqlschema.DescColumns("title")).Annotations(sqlschema.OpClassColumn("title", "text_pattern_ops")).StorageKey("ix_both"),
	}
}

// Rule 60: a Desc or an OpClass on a name the index does not cover. Rule 61:
// Desc on a method with no order. Rule 62: a class that is no name, or two.
type ikBad struct{ schema.Schema }

func (ikBad) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.String("other"), field.JSON("attrs", map[string]string{})}
}
func (ikBad) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("name").Annotations(sqlschema.DescColumns("other")).StorageKey("ix_desc_elsewhere"),
		index.Fields("name").Annotations(sqlschema.OpClassColumn("other", "text_ops")).StorageKey("ix_class_elsewhere"),
		index.Fields("attrs").Annotations(sqlschema.IndexType("gin")).Annotations(sqlschema.DescColumns("attrs")).StorageKey("ix_gin_desc"),
		index.Fields("name").Annotations(sqlschema.OpClassColumn("name", "x); DROP TABLE t")).StorageKey("ix_class_name"),
		index.Fields("name").Annotations(sqlschema.OpClassColumn("name", "")).StorageKey("ix_class_empty"),
	}
}

func TestIndexKeysRules(t *testing.T) {
	_, err := build(t, ikBad{})
	got := plain(err)
	for _, want := range []string{
		`index 0: has sqlschema.Desc on "other", which it does not cover`,
		`index 1: has sqlschema.OpClass on "other", which it does not cover`,
		`index 2: has sqlschema.Desc with IndexType "gin", and only btree has an order`,
		`index 3: has sqlschema.OpClass "x); DROP TABLE t" for "name", which is not an operator class name`,
		`index 4: has sqlschema.OpClass "" for "name", which is not an operator class name`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q not in\n%s", want, got)
		}
	}
}

// Desc and OpClass reach the migrate index by column, Desc in the index's
// column order whatever the order of the calls and a name given twice.
func TestIndexKeysTables(t *testing.T) {
	tb := table(t, tables(t, velox.Postgres, ikDocs{}), "ik_docs")
	type keys struct {
		desc []string
		ops  map[string]string
	}
	got := map[string]keys{}
	for _, ix := range tb.Indexes {
		got[strings.TrimPrefix(ix.Name, tb.Name+"_")] = keys{ix.Desc, ix.OpClass}
	}
	want := map[string]keys{
		"ix_recent":       {[]string{"created_at"}, nil},
		"ix_title_prefix": {nil, map[string]string{"title": "text_pattern_ops"}},
		"ix_attrs_path":   {nil, map[string]string{"attrs": "jsonb_path_ops"}},
		"ix_both":         {[]string{"created_at", "title"}, map[string]string{"title": "text_pattern_ops"}},
	}
	for name, w := range want {
		g, ok := got[name]
		if !ok || !slices.Equal(g.desc, w.desc) || !maps.Equal(g.ops, w.ops) {
			t.Errorf("index %s: Desc %v, OpClass %v (present %t), want %v, %v", name, g.desc, g.ops, ok, w.desc, w.ops)
		}
	}
}

// MySQL and SQLite have neither.
func TestIndexKeysRefusedOutsidePostgres(t *testing.T) {
	g := mustBuild(t, ikDocs{})
	for _, d := range []velox.Dialect{velox.MySQL, velox.SQLite} {
		_, err := Tables(g, d)
		if err == nil {
			t.Errorf("%v: no error", d)
			continue
		}
		for _, w := range []string{
			`index 0: sqlschema.Desc is PostgreSQL's for now; ` + d.String(),
			`index 1: sqlschema.OpClass is PostgreSQL's; ` + d.String() + ` has no operator classes`,
		} {
			if got := plain(err); !strings.Contains(got, w) {
				t.Errorf("%v: %q not in\n%s", d, w, got)
			}
		}
	}
}

// The same columns with another direction or class are another index, not a
// repeat (rule 43); the same keys twice are one.
type ikRepeat struct{ schema.Schema }

func (ikRepeat) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.Time("at")}
}
func (ikRepeat) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("name", "at").StorageKey("ix_r_asc"),
		index.Fields("name", "at").Annotations(sqlschema.DescColumns("at")).StorageKey("ix_r_desc"),
		index.Fields("name", "at").Annotations(sqlschema.OpClassColumn("name", "text_pattern_ops")).StorageKey("ix_r_class"),
	}
}

type ikTwice struct{ schema.Schema }

func (ikTwice) Fields() []schema.Field { return []schema.Field{field.String("name")} }
func (ikTwice) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("name").Annotations(sqlschema.OpClassColumn("name", "text_pattern_ops")).StorageKey("ix_t_one"),
		index.Fields("name").Annotations(sqlschema.OpClassColumn("name", "text_pattern_ops")).StorageKey("ix_t_two"),
	}
}

func TestIndexKeysAreNotARepeat(t *testing.T) {
	if _, err := build(t, ikRepeat{}); err != nil {
		t.Fatal(err)
	}
	_, err := build(t, ikTwice{})
	if got := plain(err); !strings.Contains(got, "repeats index 0") {
		t.Errorf("the same keys twice: %q", got)
	}
}
