//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type InOrg struct{ mixin.Schema }

func (InOrg) Fields() []schema.Field { return []schema.Field{field.Int64("org_id").Immutable()} }

type Project struct{ schema.Schema }

func (Project) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

func (Project) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.Int8("priority")}
}

func (Project) Edges() []schema.Edge { return []schema.Edge{edge.To("issues", Issue.Type)} }

type Issue struct{ schema.Schema }

func (Issue) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

func (Issue) Fields() []schema.Field {
	return []schema.Field{field.Int64("project_id"), field.Int64("author_id"), field.String("title")}
}

func (Issue) Edges() []schema.Edge {
	return []schema.Edge{edge.From("project", Project.Type).Ref("issues").Unique().Required().Field("project_id")}
}

type Label struct{ schema.Schema }

func (Label) Fields() []schema.Field { return []schema.Field{field.String("text")} }
