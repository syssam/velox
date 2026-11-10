// Package schema declares the graphql example's entities: a User writes
// Posts, each Post belongs to an organization and carries Tags,
// the GraphQL type Label.
// The graphql.* annotations shape the GraphQL schema velox-graphql writes
// beside the entities. go tool velox-gen -plugin velox-graphql reads it and
// writes the gen package.
package veloxschema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// User writes posts. It is not scoped to an organization: users are shared, their posts
// are not.
type User struct{ schema.Schema }

// Fields returns the columns after the ID. PasswordHash is Sensitive, so it
// is in no GraphQL type, where input or order, and Skip leaves it out of the
// inputs too: a client sends a password, which the createUser and updateUser
// resolvers hash, and never a hash it chose. Email is for admins only: the
// application's directive @admin guards the field and, since velox-graphql
// copies it onto them, its where predicates, by which a client could
// otherwise find an email a prefix at a time.
func (User) Fields() []schema.Field {
	return []schema.Field{
		field.String("name").Comment("The name the user shows.").Annotations(graphql.OrderField("NAME"), graphql.WhereOps()),
		field.String("email").Comment("The address the user signs in with; admins only.").Annotations(graphql.WhereOps(), graphql.Directives(graphql.Directive{Name: "admin"})),
		field.Enum("role").Values("admin", "member").Default("member").Comment("What the user may do.").Annotations(graphql.WhereOps()),
		field.Time("joined_at").Comment("When the user joined.").Annotations(graphql.OrderField("JOINED_AT")),
		field.String("password_hash").Sensitive().Annotations(graphql.Skip(graphql.SkipMutationCreateInput, graphql.SkipMutationUpdateInput)),
	}
}

// Edges returns Posts, the inverse of Post.Author. WhereEdge gives the where
// input hasPosts and hasPostsWith.
func (User) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("posts", Post.Type).Comment("The posts the user wrote.").Annotations(graphql.WhereEdge()),
	}
}

// Annotations names the root query field.
func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Comment("A user, who writes posts."), graphql.QueryField("users"), graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
}

// Post belongs to an organization, and to its author.
type Post struct{ schema.Schema }

// Mixin adds org_id: InOrg confines every statement on Post to the context's
// organization and fills it on insert.
func (Post) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

// Fields returns the columns after the ID and the mixin's. AuthorID is the key of the Author edge, which stands for it in GraphQL. Views is Optional: CreatePostInput may leave it out, and it is
// then 0.
func (Post) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("author_id").Annotations(graphql.WhereOps(graphql.OpsEquality)),
		field.String("title").NotEmpty().Comment("The post's title, never empty.").Annotations(graphql.OrderField("TITLE"), graphql.WhereOps()),
		field.Enum("status").Values("draft", "published").Default("draft").Comment("Whether the post is a draft or published.").Annotations(graphql.WhereOps()),
		field.Int64("views").Optional().Default(0).Comment("How many times the post was read.").Annotations(graphql.OrderField("VIEWS")),
		field.Time("published_at").Optional().Nillable().Comment("When the post was published; null for a draft.").Annotations(graphql.WhereOps()),
	}
}

// Edges returns Author, and Tags, a many-to-many edge through the join table
// post_tags. Neither input has authorID: the createPost resolver sets the
// author to the viewer, so a client cannot post as another user, and no
// update re-assigns a post to another author.
func (Post) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("author", User.Type).Ref("posts").Unique().Required().Field("author_id").Comment("The user who wrote the post.").Annotations(graphql.Skip(graphql.SkipMutationCreateInput, graphql.SkipMutationUpdateInput)),
		edge.To("tags", Tag.Type).Comment("The labels on the post.").Annotations(graphql.WhereEdge()),
	}
}

// Annotations names the root query field. MultiOrder makes orderBy a list.
func (Post) Annotations() []schema.Annotation {
	return []schema.Annotation{
		schema.Comment("A post, in an organization, by its author."),
		graphql.QueryField("posts"),
		graphql.MultiOrder(),
		graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate),
	}
}

// Tag labels posts. Its GraphQL type is Label: graphql.Type renames it, and
// gen's Go for the type takes the GraphQL names (Label, LabelConnection,
// LabelWhereInput…), with Label an alias of the entity Tag.
type Tag struct{ schema.Schema }

// Fields returns the columns after the ID. Rank is an int8, so its where
// predicates take an Int that gqlgen's Int8 must hold: a client's 300 is
// refused as its input, with BAD_USER_INPUT.
func (Tag) Fields() []schema.Field {
	return []schema.Field{
		field.String("name").Unique().Comment("The label's name, unique.").Annotations(graphql.OrderField("NAME"), graphql.WhereOps()),
		field.Int8("rank").Comment("Where the label sorts among labels.").Annotations(graphql.WhereOps()),
	}
}

// Edges returns Posts, the inverse of Post.Tags.
func (Tag) Edges() []schema.Edge {
	return []schema.Edge{edge.From("posts", Post.Type).Ref("tags").Comment("The posts with the label.")}
}

// Annotations names the GraphQL type and the root query field.
func (Tag) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Comment("A label on posts."), graphql.Type("Label"), graphql.QueryField("labels"), graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate)}
}
