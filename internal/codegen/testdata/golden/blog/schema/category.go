//go:build ignore

package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/index"
)

// Category is a tree, stored in the blog database schema.
type Category struct{ schema.Schema }

func (Category) Fields() []schema.Field {
	return []schema.Field{field.String("title").SchemaType(map[string]string{field.Postgres: "varchar(200)"})}
}

func (Category) Edges() []schema.Edge {
	return []schema.Edge{edge.To("children", Category.Type).From("parent").Unique()}
}

func (Category) Indexes() []schema.Index {
	return []schema.Index{index.Fields("title").Edges("parent")}
}

func (Category) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("blog", "categories")}
}
