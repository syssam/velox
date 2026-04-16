package schema

import (
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Membership is the edge schema that joins User and Group. It carries the
// role and joined_at fields that are specific to the relationship — data
// that doesn't logically belong on either User or Group alone.
//
// This is the classic Django-style "M2M with intermediate model."
type Membership struct {
	velox.Schema
}

func (Membership) Fields() []velox.Field {
	return []velox.Field{
		field.Enum("role").
			Values("owner", "admin", "member").
			Default("member"),
		field.Time("joined_at").
			Default(time.Now).
			Immutable(),
		// The user_id and group_id columns are declared implicitly via
		// Field() on the edges below — velox materializes FK columns
		// without requiring redundant field.Int() declarations.
		field.Int("user_id").Immutable(),
		field.Int("group_id").Immutable(),
	}
}

// Edges of Membership. Both sides must be Unique + Required and bound to a
// field via Field() — this is how velox (like Ent) tells the codegen which
// columns on the join table hold the two foreign keys.
func (Membership) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("user", User.Type).
			Unique().
			Required().
			Immutable().
			Field("user_id"),
		edge.To("group", Group.Type).
			Unique().
			Required().
			Immutable().
			Field("group_id"),
	}
}
