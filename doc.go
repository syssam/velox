// Package velox is an ORM for Go 1.27 and later. Entities are described in a
// schema package, in the DSL of the schema packages (schema, schema/field,
// schema/edge, ...). velox-gen generates from it, into a single gen package,
// the entity structs, their typed column handles and their metadata (Desc),
// which velox reads on an entity's first use.
package velox

//go:generate go run ./internal/handlegen/gen

// GeneratedCodeIsVersion3 is referenced by every file velox-gen writes from a
// schema package, so that generated code and this library fail to compile
// together when they disagree. It is replaced by GeneratedCodeIsVersion4 when
// generated code must change. (Version 3: Desc literals and switch-based Addr
// and RelAddr, generated from a schema package.)
const GeneratedCodeIsVersion3 = true

// Lazy is the constraint of the type parameter that every method of a
// generic velox type carries, and that callers never write. A generic method
// is compiled only where it is called; an ordinary method of a generic type is
// compiled into every package that uses the type.
type Lazy[T any] interface{ *T }
