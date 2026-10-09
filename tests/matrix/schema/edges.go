package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Shape 14: every edge kind.

// Author owns articles (O2M), edits articles (O2M to an optional key) and
// holds one passport (O2O).
type Author struct{ velox.Schema }

// Fields of the Author.
func (Author) Fields() []velox.Field {
	return []velox.Field{field.String("name").NotEmpty()}
}

// Edges of the Author.
func (Author) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("articles", Article.Type),
		edge.To("edited", Article.Type),
		edge.To("passport", Passport.Type).Unique(),
	}
}

// Article is the M2O side, both keys bound with Field().
type Article struct{ velox.Schema }

// Fields of the Article.
func (Article) Fields() []velox.Field {
	return []velox.Field{
		field.String("title").NotEmpty(),
		field.Int("words").Default(0),
		field.String("section").Default("news"),
		field.Int("author_id"),
		field.Int("editor_id").Optional().Nillable(),
	}
}

// Edges of the Article.
func (Article) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("author", Author.Type).Ref("articles").Unique().Field("author_id"),
		edge.From("editor", Author.Type).Ref("edited").Unique().Field("editor_id"),
		edge.To("topics", Topic.Type),
	}
}

// Passport is the O2O side holding the key.
type Passport struct{ velox.Schema }

// Fields of the Passport.
func (Passport) Fields() []velox.Field {
	return []velox.Field{field.String("number")}
}

// Edges of the Passport.
func (Passport) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("owner", Author.Type).Ref("passport").Unique().Required(),
	}
}

// Topic is the inverse side of the Article.topics M2M edge.
type Topic struct{ velox.Schema }

// Fields of the Topic.
func (Topic) Fields() []velox.Field {
	return []velox.Field{field.String("name").Unique()}
}

// Edges of the Topic.
func (Topic) Edges() []velox.Edge {
	return []velox.Edge{edge.From("articles", Article.Type).Ref("topics")}
}

// Node is a self-referencing tree.
type Node struct{ velox.Schema }

// Fields of the Node.
func (Node) Fields() []velox.Field {
	return []velox.Field{field.String("name")}
}

// Edges of the Node.
func (Node) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("children", Node.Type).From("parent").Unique(),
	}
}
