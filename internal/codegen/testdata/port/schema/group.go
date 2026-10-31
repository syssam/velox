//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Group is stored in the table "teams".
type Group struct{ schema.Schema }

func (Group) Fields() []schema.Field {
	return []schema.Field{field.String("name")}
}

func (Group) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", User.Type).Ref("groups")}
}

func (Group) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("teams")}
}
