package codegen

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// imGin has a GIN index over a JSON field, a hash index, and a btree one
// that says so.
type imGin struct{ schema.Schema }

func (imGin) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table("im_gins")} }
func (imGin) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.JSON("attrs", map[string]string{}), field.Strings("tags")}
}
func (imGin) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("attrs").Annotations(sqlschema.IndexType("GIN")).StorageKey("ix_attrs_gin"),
		index.Fields("tags").Annotations(sqlschema.IndexType("gin")).StorageKey("ix_tags_gin"),
		index.Fields("name").Annotations(sqlschema.IndexType("hash")).StorageKey("ix_name_hash"),
		index.Fields("name").Annotations(sqlschema.IndexType("btree")).StorageKey("ix_name_btree"),
	}
}

// Rule 58: a method PostgreSQL has not. Rule 59: unique and not btree. Rule
// 42 still holds for a btree index over JSON.
type imBad struct{ schema.Schema }

func (imBad) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.JSON("attrs", map[string]string{})}
}
func (imBad) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("name").Annotations(sqlschema.IndexType("rtree")),
		index.Fields("name").Unique().Annotations(sqlschema.IndexType("hash")).StorageKey("ix_name_uhash"),
		index.Fields("name").Unique().Annotations(sqlschema.IndexType("btree")).StorageKey("ix_name_ubtree"),
		index.Fields("attrs").Annotations(sqlschema.IndexType("btree")).StorageKey("ix_attrs_btree"),
	}
}

func TestIndexMethodRules(t *testing.T) {
	_, err := build(t, imBad{})
	want := `schema imBad: index 3: field "attrs" is JSON, which no index can cover but a GIN one; give the index sqlschema.IndexType("GIN")
schema imBad: index 0: has sqlschema.IndexType "rtree"; PostgreSQL's are btree, hash, gin, gist, spgist, brin
schema imBad: index 1: is Unique with sqlschema.IndexType "hash", and only btree can enforce uniqueness`
	if got := plain(err); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// A method is part of an index's identity: the same columns with another
// method are another index (rule 43), and btree is the method none names.
func TestIndexMethodTables(t *testing.T) {
	tb := table(t, tables(t, velox.Postgres, imGin{}), "im_gins")
	got := map[string]string{}
	for _, ix := range tb.Indexes {
		got[ix.Name] = ix.Method
	}
	want := map[string]string{
		"ix_attrs_gin": "gin", "ix_tags_gin": "gin", "ix_name_hash": "hash", "ix_name_btree": "",
	}
	for name, m := range want {
		if g, ok := got[name]; !ok || g != m {
			t.Errorf("index %s: method %q (present %t), want %q", name, g, ok, m)
		}
	}
}

// MySQL and SQLite have no choice of index method.
func TestIndexMethodRefusedOutsidePostgres(t *testing.T) {
	g := mustBuild(t, imGin{})
	for _, d := range []velox.Dialect{velox.MySQL, velox.SQLite} {
		_, err := Tables(g, d)
		if err == nil {
			t.Errorf("%v: no error", d)
			continue
		}
		for _, w := range []string{
			`index 0: the index type "gin" is PostgreSQL's; ` + d.String() + ` has no choice of index type`,
			`index 2: the index type "hash" is PostgreSQL's`,
		} {
			if got := plain(err); !strings.Contains(got, w) {
				t.Errorf("%v: %q not in\n%s", d, w, got)
			}
		}
		if got := plain(err); strings.Contains(got, `"btree"`) {
			t.Errorf("%v: an explicit btree is no method:\n%s", d, got)
		}
	}
}

// Two indexes of the same columns, one btree and one GIN, are not a repeat.
type imBoth struct{ schema.Schema }

func (imBoth) Fields() []schema.Field {
	return []schema.Field{field.JSON("attrs", map[string]string{}), field.String("name")}
}
func (imBoth) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("name").StorageKey("ix_both_btree"),
		index.Fields("name").Annotations(sqlschema.IndexType("hash")).StorageKey("ix_both_hash"),
	}
}

func TestIndexMethodIsNotARepeat(t *testing.T) {
	if _, err := build(t, imBoth{}); err != nil {
		t.Fatal(err)
	}
}
