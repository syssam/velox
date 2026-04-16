// Package schema defines the Category entity for the tree example.
package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

// Category is a node in a hierarchical category tree. Each category may have
// one parent and any number of children. Root categories have a nil parent.
type Category struct {
	velox.Schema
}

// Mixin of Category.
func (Category) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of Category.
func (Category) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			NotEmpty().
			MaxLen(120),
		field.String("slug").
			Unique().
			NotEmpty().
			MaxLen(160),
	}
}

// Edges of Category.
//
// A single edge.To self-referencing declaration covers both sides of the
// parent/children relationship via the inline .From() shortcut. Unique() on
// the inverse side makes each category have at most one parent, producing an
// O2M "parent has many children / child has one parent" tree.
func (Category) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("children", Category.Type).
			From("parent").
			Unique(),
	}
}

// Indexes of Category.
func (Category) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("slug").Unique(),
	}
}
