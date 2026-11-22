package schema

import (
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Line is the to-many side of Sample.Lines. Its table is "line".
type Line struct{ schema.Schema }

// Fields returns the column after the ID.
func (Line) Fields() []schema.Field { return []schema.Field{field.Int64("sample_id")} }

// Edges returns Sample, which SampleID holds the key of.
func (Line) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("sample", Sample.Type).Ref("lines").Unique().Required().Field("sample_id"),
	}
}

// Annotations names the table "line", not "lines".
func (Line) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Table("line")}
}
