// Package mixin holds the mixins a schema lists in its Mixin method, and the
// Schema to embed in a mixin of your own.
//
// A mixin carries fields, edges, indexes and annotations, and it may declare
// behaviour: generic Policy, Hooks and Interceptors methods, which velox-gen
// binds for each entity that lists it (see package schema).
package mixin

import (
	"maps"
	"time"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// Schema is embedded by every mixin. Its methods declare nothing; a mixin
// overrides the ones it needs. It declares no behaviour methods, so a mixin
// that embeds it has only those it declares.
type Schema struct{}

// Fields returns no fields.
func (Schema) Fields() []schema.Field { return nil }

// Edges returns no edges.
func (Schema) Edges() []schema.Edge { return nil }

// Indexes returns no indexes.
func (Schema) Indexes() []schema.Index { return nil }

// Annotations returns no annotations.
func (Schema) Annotations() []schema.Annotation { return nil }

// nowDefault is the database default of created_at and updated_at, which
// withNow copies. SQLite compares a time's text, so its default
// writes the form velox binds a time in, with its zone and nine fractional
// digits, of which SQLite knows three: CURRENT_TIMESTAMP's form sorts below a
// parameter of the same instant.
var nowDefault = map[string]string{dialect.Postgres: "now()", dialect.MySQL: "CURRENT_TIMESTAMP(6)",
	dialect.SQLite: "(strftime('%Y-%m-%d %H:%M:%f000000+00:00','now'))"}

// withNow gives the field f nowDefault as its database default. velox's own
// mixins set it on the descriptor, as a schema's sqlschema.DefaultExprs
// would, without an annotation in the model.
func withNow(f schema.Field) schema.Field {
	f.Descriptor().SchemaDefault = maps.Clone(nowDefault)
	return f
}

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
	return []schema.Field{withNow(field.Time("created_at").Default(time.Now).Immutable())}
}

// UpdateTime adds updated_at, set to the current time when the row is created
// and again on every update.
type UpdateTime struct{ Schema }

// Fields returns updated_at.
func (UpdateTime) Fields() []schema.Field {
	return []schema.Field{withNow(field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now))}
}
