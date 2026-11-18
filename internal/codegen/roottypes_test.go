package codegen

import (
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// rtMixin and rtAuthor are written with the root package's types alone, the way
// the old velox's schemas were: velox.Schema, Field, Edge, Index, Mixin and
// Annotation.
type rtMixin struct{ velox.Schema }

func (rtMixin) Fields() []velox.Field {
	return []velox.Field{field.String("note").Optional().Default("")}
}

type rtAuthor struct{ velox.Schema }

func (rtAuthor) Mixin() []velox.Mixin { return []velox.Mixin{rtMixin{}} }
func (rtAuthor) Annotations() []velox.Annotation {
	return []velox.Annotation{sqlschema.Table("rt_authors")}
}
func (rtAuthor) Fields() []velox.Field { return []velox.Field{field.String("name")} }
func (rtAuthor) Edges() []velox.Edge {
	return []velox.Edge{edge.To("books", rtBook.Type)}
}
func (rtAuthor) Indexes() []velox.Index {
	return []velox.Index{index.Fields("name").Annotations(sqlschema.IndexType("hash"))}
}

type rtBook struct{ velox.Schema }

func (rtBook) Fields() []velox.Field { return []velox.Field{field.String("title")} }
func (rtBook) Edges() []velox.Edge {
	return []velox.Edge{edge.From("author", rtAuthor.Type).Ref("books").Unique()}
}

// A schema written with the root's types builds like one written with
// package schema's: they are the same types.
func TestRootSchemaTypes(t *testing.T) {
	g := mustBuild(t, rtAuthor{}, rtBook{})
	tb := table(t, tables(t, velox.Postgres, rtAuthor{}, rtBook{}), "rt_authors")
	if len(tb.Indexes) == 0 {
		t.Errorf("no index on %s", tb.Name)
	}
	if len(g.Entities) != 2 {
		t.Errorf("%d entities", len(g.Entities))
	}
}
