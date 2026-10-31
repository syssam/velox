//go:build ignore

package schema

import (
	"time"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// User is the central entity of the port fixture.
type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.Int("age").Positive(),
		field.String("name").Default("unknown"),
		// ent writes Default(true) on a bool that is not Nillable; velox
		// needs Nillable with it, as an insert cannot tell false from unset.
		field.Bool("active").Nillable().Default(true),
		field.Enum("role").Values("admin", "user"),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Strings("tags").Optional(),
	}
}

func (User) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("pets", Pet.Type),
		edge.To("groups", Group.Type),
		edge.To("card", Card.Type).Unique(),
	}
}

func (User) Indexes() []schema.Index {
	return []schema.Index{index.Fields("name").Unique()}
}
