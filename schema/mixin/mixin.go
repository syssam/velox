// Package mixin holds the mixins a schema lists in its Mixin method, and the
// Schema to embed in a mixin of your own.
//
// A mixin carries fields, edges, indexes and annotations, and nothing else.
// A reusable hook is not part of a mixin: it is a generic function, and each
// entity that wants it lists it in its own Hooks.
package mixin

import (
	"time"

	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

// Schema is embedded by every mixin. Its methods declare nothing; a mixin
// overrides the ones it needs. A mixin carries fields, edges, indexes and
// annotations only; a reusable hook is a generic function each entity lists.
type Schema struct{}

// Fields returns no fields.
func (Schema) Fields() []schema.Field { return nil }

// Edges returns no edges.
func (Schema) Edges() []schema.Edge { return nil }

// Indexes returns no indexes.
func (Schema) Indexes() []schema.Index { return nil }

// Annotations returns no annotations.
func (Schema) Annotations() []schema.Annotation { return nil }

// Time adds created_at and updated_at: the first is set once when the row is
// created, the second on every update.
type Time struct{ Schema }

// Fields returns the fields of CreateTime followed by those of UpdateTime.
func (Time) Fields() []schema.Field {
	return append(CreateTime{}.Fields(), UpdateTime{}.Fields()...)
}

// CreateTime adds created_at, set to the current time when the row is created
// and immutable after.
type CreateTime struct{ Schema }

// Fields returns created_at.
func (CreateTime) Fields() []schema.Field {
	return []schema.Field{field.Time("created_at").Default(time.Now).Immutable()}
}

// UpdateTime adds updated_at, set to the current time when the row is created
// and again on every update.
type UpdateTime struct{ Schema }

// Fields returns updated_at.
func (UpdateTime) Fields() []schema.Field {
	return []schema.Field{field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now)}
}
