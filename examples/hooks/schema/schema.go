// Package schema declares the hooks example's entities: a Task, and the Audit
// rows that its update hook writes. Task's hooks, in hooks.go, are typed with
// the generated gen.Task and its handles; velox-gen binds them through
// gen/runtime.
package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// CreatedBy is a mixin: it adds a created_by column to each entity that lists
// it. A mixin carries fields, edges, indexes and annotations, never
// behaviour; what fills the column is a hook, auth.CreatedBy, that each entity
// lists in its own Hooks.
type CreatedBy struct{ mixin.Schema }

// Fields returns created_by.
func (CreatedBy) Fields() []schema.Field {
	return []schema.Field{field.String("created_by")}
}

// Task is a to-do item. The hooks derive Slug from Title.
type Task struct{ schema.Schema }

// Mixin adds created_by, before the fields below.
func (Task) Mixin() []schema.Mixin { return []schema.Mixin{CreatedBy{}} }

// Fields returns the columns after the ID and the mixin's.
func (Task) Fields() []schema.Field {
	return []schema.Field{
		field.String("title"),
		field.String("slug"),
		field.Bool("done"),
	}
}

// Audit is a row of the audit trail: who changed what. Task's update hook
// inserts one on the write's DB: inside db.Tx it commits or rolls back with
// the update.
type Audit struct{ schema.Schema }

// Fields returns the columns after the ID.
func (Audit) Fields() []schema.Field {
	return []schema.Field{
		field.String("actor"),
		field.String("message"),
	}
}
