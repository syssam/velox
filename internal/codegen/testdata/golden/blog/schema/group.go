//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Group struct{ schema.Schema }

func (Group) Fields() []schema.Field {
	return []schema.Field{
		field.String("name"),
		field.String("token").DefaultFunc(func() string { return "tok" }),
		field.EnumOf[Visibility]("visibility"),
	}
}

func (Group) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", User.Type).Ref("groups")}
}
