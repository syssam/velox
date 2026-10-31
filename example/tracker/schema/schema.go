// Package schema declares the tracker's entities. go tool velox-gen reads it and
// writes the gen package beside it. A schema holds structure only: the policies
// are methods in the gen package.
package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/index"
	"github.com/syssam/velox2/schema/mixin"
)

// Org is an organization: the tenant. It is not itself scoped to a tenant, as
// signing up and logging in find it before there is one.
type Org struct{ schema.Schema }

// Fields returns the columns after the ID.
func (Org) Fields() []schema.Field {
	return []schema.Field{
		field.String("slug").NotEmpty().MaxLen(40).Unique(),
		field.String("name").NotEmpty().MaxRuneLen(100), // in characters: a name may be Chinese
	}
}

// Annotations holds the slug to lower case in the database too, whichever code writes it.
func (Org) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Check("orgs_slug_lower", "slug = lower(slug)")}
}

// User is a member of one Org. An email is unique within the Org.
type User struct{ schema.Schema }

// Fields returns the columns after the ID. Tenant makes OrgID the tenant column:
// every statement on User is scoped to the context's tenant, and fails without
// one.
func (User) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.String("email").NotEmpty().MaxLen(200),
		field.String("name").NotEmpty().MaxLen(100),
		field.String("password_hash").Sensitive(),
		field.Enum("role").Values("admin", "member"),
	}
}

// Indexes makes an email unique within its Org.
func (User) Indexes() []schema.Index {
	return []schema.Index{index.Fields("org_id", "email").Unique()}
}

// Session is a login. Only a hash of its token is stored, so a copy of the table
// cannot be used to log in. It names its Org because a request has no tenant
// until its Session is found.
type Session struct{ schema.Schema }

// Fields returns the columns after the ID.
func (Session) Fields() []schema.Field {
	return []schema.Field{
		field.String("token_hash").Unique(),
		field.Int64("org_id"),
		field.Int64("user_id"),
		field.Time("expires_at"),
	}
}

// Project groups Issues. Only an admin can change one (gen/policy.go).
type Project struct{ schema.Schema }

// Mixin adds created_at and updated_at.
func (Project) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }

// Fields returns the columns after the ID and the mixin's.
func (Project) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.String("name").NotEmpty().MaxLen(100),
	}
}

// Edges returns Issues.
func (Project) Edges() []schema.Edge { return []schema.Edge{edge.To("issues", Issue.Type)} }

// Indexes makes a name unique within its Org.
func (Project) Indexes() []schema.Index {
	return []schema.Index{index.Fields("org_id", "name").Unique()}
}

// Issue is the unit of work. Its author, its assignee and an admin can change it
// (gen/policy.go).
type Issue struct{ schema.Schema }

// Mixin adds created_at and updated_at.
func (Issue) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }

// Fields returns the columns after the ID and the mixin's.
func (Issue) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.Int64("project_id"),
		field.Int64("author_id"),
		field.Int64("assignee_id").Optional().Nillable(),
		field.String("title").NotEmpty().MaxLen(200),
		field.Text("body"),
		field.Enum("status").Values("open", "closed").Default("open"),
	}
}

// Edges returns the Project, the author and the assignee, whose keys are
// columns here, its Comments, and its Labels, through a join table.
func (Issue) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("project", Project.Type).Ref("issues").Unique().Required().Field("project_id"),
		edge.To("author", User.Type).Unique().Required().Field("author_id"),
		edge.To("assignee", User.Type).Unique().Field("assignee_id"),
		edge.To("comments", Comment.Type),
		edge.To("labels", Label.Type),
	}
}

// Indexes serves a project's issues, by status, in the order they page in.
func (Issue) Indexes() []schema.Index {
	return []schema.Index{index.Fields("project_id", "status", "id")}
}

// Comment is a remark on an Issue. Its author and an admin can change it.
type Comment struct{ schema.Schema }

// Mixin adds created_at and updated_at.
func (Comment) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }

// Fields returns the columns after the ID and the mixin's.
func (Comment) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.Int64("issue_id"),
		field.Int64("author_id"),
		field.Text("body").NotEmpty(),
	}
}

// Edges returns the Issue, the inverse of Issue.Comments, whose deletion deletes
// its Comments, and the author.
func (Comment) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("issue", Issue.Type).Ref("comments").Unique().Required().Field("issue_id").OnDelete(edge.Cascade),
		edge.To("author", User.Type).Unique().Required().Field("author_id"),
	}
}

// Label tags Issues. A name is unique within its Org.
type Label struct{ schema.Schema }

// Fields returns the columns after the ID.
func (Label) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.String("name").NotEmpty().MaxLen(50),
	}
}

// Edges returns Issues, the inverse of Issue.Labels.
func (Label) Edges() []schema.Edge {
	return []schema.Edge{edge.From("issues", Issue.Type).Ref("labels")}
}

// Indexes makes a name unique within its Org.
func (Label) Indexes() []schema.Index {
	return []schema.Index{index.Fields("org_id", "name").Unique()}
}
