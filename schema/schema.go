package schema

import "github.com/syssam/velox2/schema/internal/desc"

// Annotation is extra data attached to a schema, field, edge or index. Name
// identifies it, and velox-gen passes annotations it does not know to the
// generators that do.
type Annotation = desc.Annotation

// Merger is implemented by an annotation that combines with another of the
// same name on one element, such as one from a mixin and one from the schema.
type Merger interface{ Merge(Annotation) Annotation }

// Field is implemented by the builders of the field package.
type Field interface{ Descriptor() *desc.Field }

// Edge is implemented by the builders of the edge package.
type Edge interface{ Descriptor() *desc.Edge }

// Index is implemented by the builders of the index package.
type Index interface{ Descriptor() *desc.Index }

// Mixin contributes fields, edges, indexes and annotations to the schemas that
// list it. A mixin carries no behaviour.
type Mixin interface {
	Fields() []Field
	Edges() []Edge
	Indexes() []Index
	Annotations() []Annotation
}

// Interface is implemented by every schema: a struct that embeds Schema.
// velox-gen finds schemas by that embedding.
type Interface interface {
	// Type is never called. Edges name a schema by its method expression:
	// edge.To("posts", Post.Type).
	Type()
	Fields() []Field
	Edges() []Edge
	Indexes() []Index
	Mixin() []Mixin
	Annotations() []Annotation
}

// Schema is embedded by every schema. Its methods declare nothing; a schema
// overrides the ones it needs.
type Schema struct{}

// Type is never called. Edges name a schema by its method expression:
// edge.To("posts", Post.Type).
func (Schema) Type() {}

// Fields returns no fields.
func (Schema) Fields() []Field { return nil }

// Edges returns no edges.
func (Schema) Edges() []Edge { return nil }

// Indexes returns no indexes.
func (Schema) Indexes() []Index { return nil }

// Mixin returns no mixins.
func (Schema) Mixin() []Mixin { return nil }

// Annotations returns no annotations.
func (Schema) Annotations() []Annotation { return nil }
