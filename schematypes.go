package velox

import "github.com/syssam/velox/schema"

// The types a schema is written with, as ent's root package has them and the old
// velox's did, so that a schema file imports one package for them:
//
//	type User struct{ velox.Schema }
//
//	func (User) Fields() []velox.Field { return []velox.Field{field.String("name")} }
//
// They are aliases of package schema's, so a schema that says schema.Schema,
// schema.Field and the like is the same schema.
type (
	// Schema is embedded by a schema, as schema.Schema is.
	Schema = schema.Schema
	// Field is a field of a schema, built by package field.
	Field = schema.Field
	// Edge is an edge of a schema, built by package edge.
	Edge = schema.Edge
	// Index is an index of a schema, built by package index.
	Index = schema.Index
	// Mixin contributes fields, edges, indexes and annotations to the schemas
	// that list it.
	Mixin = schema.Mixin
	// Annotation is extra data attached to a schema, field, edge or index.
	Annotation = schema.Annotation
)
