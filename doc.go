// Package velox is an ORM for Go 1.27 and later. Entities are plain structs
// owned by the packages that use them; velox-gen writes a small file of typed
// column handles beside each one.
package velox

//go:generate go run ./internal/handlegen/gen

// GeneratedCodeIsVersion2 is referenced by every file velox-gen writes, so
// that generated code and this library fail to compile together when they
// disagree. It is replaced by GeneratedCodeIsVersion3 when generated code must
// change. (Version 2: relations in Shape and Rel handles.)
const GeneratedCodeIsVersion2 = true

// Lazy is the constraint of the type parameter that every method of a
// generic velox type carries, and that callers never write. A generic method
// is compiled only where it is called; an ordinary method of a generic type is
// compiled into every package that uses the type.
type Lazy[T any] interface{ *T }
