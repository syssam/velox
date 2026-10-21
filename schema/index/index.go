// Package index declares the indexes of a schema:
//
//	index.Fields("org_id", "name").Unique()
//
// The package imports only the descriptors, the schema package and the
// standard library.
package index

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/internal/desc"
)

// Builder builds an index.
type Builder struct{ d *desc.Index }

// Fields starts an index over the named fields.
func Fields(fields ...string) *Builder { return newBuilder(desc.Caller()).Fields(fields...) }

// Edges starts an index over the foreign keys of the named edges.
func Edges(edges ...string) *Builder { return newBuilder(desc.Caller()).Edges(edges...) }

// newBuilder starts an index declared at pos.
func newBuilder(pos string) *Builder { return &Builder{d: &desc.Index{Pos: pos}} }

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

// StorageKey sets the index name.
func (b *Builder) StorageKey(name string) *Builder { b.d.StorageKey = name; return b }

// Where sets the predicate of a partial index, in SQL. Only PostgreSQL has
// partial indexes.
func (b *Builder) Where(sql string) *Builder { b.d.Where = sql; return b }

// Annotations attaches annotations to the index.
func (b *Builder) Annotations(as ...schema.Annotation) *Builder {
	b.d.Annotations = append(b.d.Annotations, as...)
	return b
}

// Descriptor returns the index's descriptor.
func (b *Builder) Descriptor() *desc.Index { return b.d }
