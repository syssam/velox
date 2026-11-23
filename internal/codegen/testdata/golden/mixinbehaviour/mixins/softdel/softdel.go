//go:build ignore

// Package softdel is a mixin in a package of its own: it hides rows whose
// deleted_at is set.
package softdel

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// Mixin adds deleted_at and hides the rows that have it.
type Mixin struct{ mixin.Schema }

func (Mixin) Fields() []schema.Field {
	return []schema.Field{field.Time("deleted_at").Optional().Nillable()}
}

func (Mixin) Interceptors() []velox.Interceptor {
	return []velox.Interceptor{{Filter: func(context.Context, *velox.DB) (velox.Predicate, error) {
		return velox.FieldIsNull("deleted_at"), nil
	}}}
}
