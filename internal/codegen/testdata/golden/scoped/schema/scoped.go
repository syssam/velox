//go:build ignore

package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
	"github.com/syssam/velox/scope"
)

var (
	OrgKey    = scope.NewKey[int64]("org")
	RegionKey = scope.NewKey[string]("region")
)

// InOrg scopes an entity by org: a tenancy mixin over package scope.
type InOrg struct{ mixin.Schema }

func (InOrg) Fields() []schema.Field { return []schema.Field{field.Int64("org_id").Immutable()} }

func (InOrg) Policy[E any]() velox.Policy[E] { return scope.By[E](OrgKey, "org_id").Policy() }

func (InOrg) Hooks[E any]() []velox.Hook[E] {
	return []velox.Hook[E]{scope.By[E](OrgKey, "org_id").Fill()}
}

// InRegion scopes an entity by a string, under another key.
type InRegion struct{ mixin.Schema }

func (InRegion) Fields() []schema.Field { return []schema.Field{field.String("region").Immutable()} }

func (InRegion) Policy[E any]() velox.Policy[E] { return scope.By[E](RegionKey, "region").Policy() }

func (InRegion) Hooks[E any]() []velox.Hook[E] {
	return []velox.Hook[E]{scope.By[E](RegionKey, "region").Fill()}
}

// Note is scoped by org, and declares neither Policy nor Hooks.
type Note struct{ schema.Schema }

func (Note) Mixin() []schema.Mixin  { return []schema.Mixin{InOrg{}} }
func (Note) Fields() []schema.Field { return []schema.Field{field.String("title")} }

// Task is scoped by org, and declares both (the golden Config binds them).
type Task struct{ schema.Schema }

func (Task) Mixin() []schema.Mixin  { return []schema.Mixin{InOrg{}} }
func (Task) Fields() []schema.Field { return []schema.Field{field.String("title")} }

// Memo is scoped by org and declares only a Policy.
type Memo struct{ schema.Schema }

func (Memo) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

// Tag is scoped by org and declares only Hooks.
type Tag struct{ schema.Schema }

func (Tag) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

// Home is scoped by a string, under another key.
type Home struct{ schema.Schema }

func (Home) Mixin() []schema.Mixin { return []schema.Mixin{InRegion{}} }

// Owner has no scope.
type Owner struct{ schema.Schema }

func (Owner) Fields() []schema.Field { return []schema.Field{field.String("name")} }
