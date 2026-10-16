//go:build ignore

package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
)

// Category is a tree, stored in the blog database schema.
type Category struct{ schema.Schema }

func (Category) Fields() []schema.Field { return []schema.Field{field.String("title")} }

func (Category) Edges() []schema.Edge {
	return []schema.Edge{edge.To("children", Category.Type).From("parent").Unique()}
}

func (Category) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("blog", "categories")}
}
