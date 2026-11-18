// Package veloxschema declares the tracker's entities. go tool velox-gen reads it and
// writes the veloxgen package beside it. The InOrg mixin (inorg.go) confines the
// entities that list it to the caller's Org; the policies (policy.go) decide
// what a member may do within it.
package veloxschema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

// Org is an organization, the scope of the rest. It is not itself scoped, as
// signing up and logging in find it before the context names one.
type Org struct{ velox.Schema }

// Fields returns the columns after the ID.
func (Org) Fields() []velox.Field {
	return []velox.Field{
		field.String("slug").NotEmpty().MaxLen(40).Unique(),
		field.String("name").NotEmpty().MaxRuneLen(100), // in characters: a name may be Chinese
	}
}

// Annotations holds the slug to lower case in the database too, whichever code writes it.
func (Org) Annotations() []velox.Annotation {
	return []velox.Annotation{schema.Check("orgs_slug_lower", "slug = lower(slug)")}
}

// User is a member of one Org. An email is unique within the Org.
type User struct{ velox.Schema }

// Mixin adds org_id: InOrg confines every statement on User to the
// context's Org, and fails without one.
func (User) Mixin() []velox.Mixin { return []velox.Mixin{InOrg{}} }

// Fields returns the columns after the ID and the mixin's.
func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("email").NotEmpty().MaxLen(200),
		field.String("name").NotEmpty().MaxLen(100),
		field.String("password_hash").Sensitive(),
		field.Enum("role").Values("admin", "member"),
	}
}

// Indexes makes an email unique within its Org.
func (User) Indexes() []velox.Index {
	return []velox.Index{index.Fields("org_id", "email").Unique()}
}

// Session is a login. Only a hash of its token is stored, so a copy of the table
// cannot be used to log in. It names its Org because a request names no Org
// until its Session is found.
type Session struct{ velox.Schema }

// Fields returns the columns after the ID.
func (Session) Fields() []velox.Field {
	return []velox.Field{
		field.String("token_hash").Unique().Sensitive(),
		field.Int64("org_id"),
		field.Int64("user_id"),
		field.Time("expires_at"),
	}
}

// Project groups Issues. Only an admin can change one (policy.go).
type Project struct{ velox.Schema }

// Mixin adds created_at and updated_at, and org_id, which InOrg confines.
func (Project) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}, InOrg{}} }

// Fields returns the columns after the ID and the mixins'.
func (Project) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty().MaxLen(100),
	}
}

// Edges returns Issues.
func (Project) Edges() []velox.Edge { return []velox.Edge{edge.To("issues", Issue.Type)} }

// Indexes makes a name unique within its Org.
func (Project) Indexes() []velox.Index {
	return []velox.Index{index.Fields("org_id", "name").Unique()}
}

// Issue is the unit of work. Its author, its assignee and an admin can change it
// (policy.go).
type Issue struct{ velox.Schema }

// Mixin adds created_at and updated_at, and org_id, which InOrg confines.
func (Issue) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}, InOrg{}} }

// Fields returns the columns after the ID and the mixins'.
func (Issue) Fields() []velox.Field {
	return []velox.Field{
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
func (Issue) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("project", Project.Type).Ref("issues").Unique().Required().Field("project_id"),
		edge.To("author", User.Type).Unique().Required().Field("author_id"),
		edge.To("assignee", User.Type).Unique().Field("assignee_id"),
		edge.To("comments", Comment.Type),
		edge.To("labels", Label.Type),
	}
}

// Indexes serves a project's issues, by status, in the order they page in.
func (Issue) Indexes() []velox.Index {
	return []velox.Index{index.Fields("project_id", "status", "id")}
}

// Comment is a remark on an Issue. Its author and an admin can change it.
type Comment struct{ velox.Schema }

// Mixin adds created_at and updated_at, and org_id, which InOrg confines.
func (Comment) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}, InOrg{}} }

// Fields returns the columns after the ID and the mixins'.
func (Comment) Fields() []velox.Field {
	return []velox.Field{
		field.Int64("issue_id"),
		field.Int64("author_id"),
		field.Text("body").NotEmpty(),
	}
}

// Edges returns the Issue, the inverse of Issue.Comments, whose deletion deletes
// its Comments, and the author.
func (Comment) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("issue", Issue.Type).Ref("comments").Unique().Required().Field("issue_id").OnDelete(edge.Cascade),
		edge.To("author", User.Type).Unique().Required().Field("author_id"),
	}
}

// Label tags Issues. A name is unique within its Org.
type Label struct{ velox.Schema }

// Mixin adds org_id, which InOrg confines.
func (Label) Mixin() []velox.Mixin { return []velox.Mixin{InOrg{}} }

// Fields returns the columns after the ID and the mixin's.
func (Label) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty().MaxLen(50),
	}
}

// Edges returns Issues, the inverse of Issue.Labels.
func (Label) Edges() []velox.Edge {
	return []velox.Edge{edge.From("issues", Issue.Type).Ref("labels")}
}

// Indexes makes a name unique within its Org.
func (Label) Indexes() []velox.Index {
	return []velox.Index{index.Fields("org_id", "name").Unique()}
}
