// Package schema declares the policy example's entities: a Doc, which its
// owner may change and anyone may read once shared, and its Comments, which
// are visible exactly when their Doc is. The policies, in doc_policy.go and
// comment_policy.go, name columns and edges by their schema names; the
// schema does not import gen.
package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Doc is a document with an owner.
type Doc struct{ schema.Schema }

// Fields returns the columns after the ID.
func (Doc) Fields() []schema.Field {
	return []schema.Field{
		field.String("owner"),
		field.String("title"),
		field.Bool("shared"),
	}
}

// Edges returns Comments, the remarks on a Doc.
func (Doc) Edges() []schema.Edge { return []schema.Edge{edge.To("comments", Comment.Type)} }

// Comment is a remark on a Doc.
type Comment struct{ schema.Schema }

// Fields returns the columns after the ID.
func (Comment) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("doc_id"),
		field.String("body"),
	}
}

// Edges returns Doc, the inverse of Doc.Comments, which DocID holds the key of.
func (Comment) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("doc", Doc.Type).Ref("comments").Unique().Required().Field("doc_id"),
	}
}
