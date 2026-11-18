// Package veloxschema declares the hooks example's entities: a Task, and the Audit
// rows that its update writes. Task's hooks, in hooks.go, work by column
// name; the schema does not import veloxgen.
package veloxschema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/hooks/auth"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// CreatedBy adds created_by, and fills it on insert from the context's
// user, in each row that leaves it empty.
type CreatedBy struct{ mixin.Schema }

// Fields returns created_by.
func (CreatedBy) Fields() []velox.Field {
	return []velox.Field{field.String("created_by")}
}

// Hooks fills created_by on insert.
func (CreatedBy) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		if u := auth.UserFromContext(ctx); u != "" {
			return m.Fill("created_by", u)
		}
		return nil
	}, velox.OpInsert)}
}

// Task is a to-do item. The hooks derive Slug from Title.
type Task struct{ velox.Schema }

// Mixin adds created_by, before the fields below.
func (Task) Mixin() []velox.Mixin { return []velox.Mixin{CreatedBy{}} }

// Fields returns the columns after the ID and the mixin's.
func (Task) Fields() []velox.Field {
	return []velox.Field{
		field.String("title"),
		field.String("slug"),
		field.Bool("done"),
	}
}

// Audit is a row of the audit trail: who changed what. main.go's run-time
// hook inserts one when an update renames a task, on the write's DB: inside
// db.Tx it commits or rolls back with the update.
type Audit struct{ velox.Schema }

// Fields returns the columns after the ID.
func (Audit) Fields() []velox.Field {
	return []velox.Field{
		field.String("actor"),
		field.String("message"),
	}
}
