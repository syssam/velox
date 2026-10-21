// Package schema declares the graphql example's entities: a User writes
// Posts, each Post belongs to an organization (the tenant) and carries Tags,
// the GraphQL type Label.
// The graphql.* annotations shape the GraphQL schema velox-graphql writes
// beside the entities. go tool velox-gen -plugin velox-graphql reads it and
// writes the gen package.
package schema

import (
	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
)

// User writes posts. It is not tenant-scoped: users are shared, their posts
// are not.
type User struct{ schema.Schema }

// Fields returns the columns after the ID. PasswordHash is Sensitive, so it
// is in no GraphQL type, input or order. Email is for admins only: the
// application's directive @admin guards the field and, since velox-graphql
// copies it onto them, its where predicates, by which a client could
// otherwise find an email a prefix at a time.
func (User) Fields() []schema.Field {
	return []schema.Field{
		field.String("name").Annotations(graphql.OrderField("NAME"), graphql.WhereOps()),
		field.String("email").Annotations(graphql.WhereOps(), graphql.Directives(graphql.Directive{Name: "admin"})),
		field.Enum("role").Values("admin", "member").Default("member").Annotations(graphql.WhereOps()),
		field.Time("joined_at").Annotations(graphql.OrderField("JOINED_AT")),
		field.String("password_hash").Sensitive(),
	}
}

// Edges returns Posts, the inverse of Post.Author. WhereEdge gives the where
// input hasPosts and hasPostsWith.
func (User) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("posts", Post.Type).Annotations(graphql.WhereEdge()),
	}
}

// Annotations names the root query field.
func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("users")}
}

// Post belongs to an organization, the tenant, and to its author.
type Post struct{ schema.Schema }

// Fields returns the columns after the ID. OrgID is the tenant column: it is
// in no GraphQL type or input, and every statement on Post is scoped to the
// context's tenant. AuthorID is the key of the Author edge, which stands for
// it in GraphQL.
func (Post) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.Int64("author_id").Annotations(graphql.WhereOps(graphql.OpsEquality)),
		field.String("title").Annotations(graphql.OrderField("TITLE"), graphql.WhereOps()),
		field.Enum("status").Values("draft", "published").Default("draft").Annotations(graphql.WhereOps()),
		field.Int64("views").Annotations(graphql.OrderField("VIEWS")),
		field.Time("published_at").Optional().Nillable().Annotations(graphql.WhereOps()),
	}
}

// Edges returns Author, and Tags, a many-to-many edge through the join table
// post_tags.
func (Post) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("author", User.Type).Ref("posts").Unique().Required().Field("author_id"),
		edge.To("tags", Tag.Type).Annotations(graphql.WhereEdge()),
	}
}

// Annotations names the root query field. MultiOrder makes orderBy a list.
func (Post) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.QueryField("posts"), graphql.MultiOrder()}
}

// Tag labels posts. Its GraphQL type is Label: graphql.Type renames it, and
// gen's Go for the type takes the GraphQL names (Label, LabelConnection,
// LabelWhereInput…), with Label an alias of the entity Tag.
type Tag struct{ schema.Schema }

// Fields returns the column after the ID.
func (Tag) Fields() []schema.Field {
	return []schema.Field{
		field.String("name").Annotations(graphql.OrderField("NAME"), graphql.WhereOps()),
	}
}

// Edges returns Posts, the inverse of Post.Tags.
func (Tag) Edges() []schema.Edge {
	return []schema.Edge{edge.From("posts", Post.Type).Ref("tags")}
}

// Annotations names the GraphQL type and the root query field.
func (Tag) Annotations() []schema.Annotation {
	return []schema.Annotation{graphql.Type("Label"), graphql.QueryField("labels")}
}
