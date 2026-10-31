//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Pet belongs to one User.
type Pet struct{ schema.Schema }

func (Pet) Fields() []schema.Field {
	return []schema.Field{field.String("name")}
}

func (Pet) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owner", User.Type).Ref("pets").Unique()}
}
