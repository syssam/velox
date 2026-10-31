// Package index declares the indexes of a schema:
//
//	index.Fields("org_id", "name").Unique()
//
// The package imports only the descriptors, the schema package and the
// standard library.
package index

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/internal/desc"
)

// Builder builds an index.
type Builder struct{ d *desc.Index }

// Fields starts an index over the named fields. velox-gen refuses a JSON
// field, which no index can cover, and an index that repeats another of the
// schema or its mixins: the same columns in order, the same Unique and the
// same Where.
func Fields(fields ...string) *Builder { return newBuilder(desc.Caller()).Fields(fields...) }

// Edges starts an index over the foreign keys of the named edges.
func Edges(edges ...string) *Builder { return newBuilder(desc.Caller()).Edges(edges...) }

// newBuilder starts an index declared at pc.
func newBuilder(pc uintptr) *Builder { return &Builder{d: &desc.Index{PC: pc}} }

// Fields adds the named fields to the index.
func (b *Builder) Fields(fields ...string) *Builder {
	b.d.Fields = append(b.d.Fields, fields...)
	return b
}

// Edges adds the foreign keys of the named edges to the index.
func (b *Builder) Edges(edges ...string) *Builder {
	b.d.Edges = append(b.d.Edges, edges...)
	return b
}

// Unique makes the index unique.
func (b *Builder) Unique() *Builder { b.d.Unique = true; return b }

// StorageKey sets the index name. Without one, velox-gen derives ent's name:
// the entity's name in lower case and the columns, as user_name; a partial
// index that would share it with another gets a hash of Where. No two
// indexes of one database schema may share a name.
func (b *Builder) StorageKey(name string) *Builder { b.d.StorageKey = name; return b }

// Where sets the predicate of a partial index, in SQL. PostgreSQL and SQLite
// have partial indexes; MySQL does not.
func (b *Builder) Where(sql string) *Builder { b.d.Where = sql; return b }

// Annotations attaches annotations to the index.
func (b *Builder) Annotations(as ...schema.Annotation) *Builder {
	b.d.Annotations = append(b.d.Annotations, as...)
	return b
}

// Descriptor returns the index's descriptor.
func (b *Builder) Descriptor() *desc.Index { return b.d }
