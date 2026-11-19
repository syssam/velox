//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Group struct{ schema.Schema }

// Group's open is ent's field.Bool("open").Default(true): a group is open
// unless the insert says false with a setter.
func (Group) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.Bool("open").Default(true)}
}

func (Group) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", User.Type).Ref("groups")}
}
