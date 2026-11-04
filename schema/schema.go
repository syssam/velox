package schema

import (
	"fmt"
	"reflect"

	"github.com/syssam/velox/schema/internal/desc"
)

// Annotation is extra data attached to a schema, field, edge or index. Name
// identifies it, and velox-gen passes annotations it does not know to the
// generators that do. Two annotations of one name on one element merge when
// the later implements Merger; otherwise the later replaces the earlier,
// without a message. A third-party annotation should take a name no other
// package uses, such as one that starts with its module path.
type Annotation = desc.Annotation

// Merger is implemented by an annotation that combines with another of the
// same name on one element, such as one from a mixin and one from the schema.
type Merger interface{ Merge(Annotation) Annotation }

// Field is implemented only by the builders of the field package. Descriptor
// is for velox-gen: the descriptor's type is internal to velox, and neither
// its fields nor their values are covered by velox's compatibility promise.
type Field interface{ Descriptor() *desc.Field }

// Edge is implemented only by the builders of the edge package. As with
// [Field], the descriptor's type is internal to velox and outside its
// compatibility promise.
type Edge interface{ Descriptor() *desc.Edge }

// Index is implemented only by the builders of the index package. As with
// [Field], the descriptor's type is internal to velox and outside its
// compatibility promise.
type Index interface{ Descriptor() *desc.Index }

// Mixin contributes fields, edges, indexes and annotations to the schemas that
// list it. A mixin may also declare generic Policy, Hooks and Interceptors
// methods, which velox-gen binds for each entity that lists it (see the
// package documentation).
type Mixin interface {
	Fields() []Field
	Edges() []Edge
	Indexes() []Index
	Annotations() []Annotation
}

// Interface is implemented by every schema. A schema is an exported,
// non-generic struct type of the schema package that implements Interface,
// by value or through a pointer, as ent's rule has it. Embedding Schema, at
// any depth, is the usual way. An unexported struct is never a schema: it can
// be a base that schemas embed.
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

// Schema is embedded by value by a schema, or by an unexported base that the
// schema embeds. Its methods declare nothing; a schema overrides the ones it
// needs, with a value or a pointer receiver.
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

// MixinAt returns the mixin at index i of s's Mixin method, as a *T. It is
// for the code velox-gen writes, which calls the Policy, Hooks or
// Interceptors method of a mixin that is not its type's zero value on the
// value itself. A mixin that is a T is copied; one that is a *T is returned.
// It panics when the mixin is neither, which means the generated code is
// older than the schema: run velox-gen.
func MixinAt[T any](s Interface, i int) *T {
	ms := s.Mixin()
	if i >= 0 && i < len(ms) {
		switch m := any(ms[i]).(type) {
		case T:
			return &m
		case *T:
			if m != nil {
				return m
			}
		}
	}
	var got any = "nothing"
	if i >= 0 && i < len(ms) {
		got = fmt.Sprintf("%T", ms[i])
	}
	panic(fmt.Sprintf("schema: %T: Mixin()[%d] is %v, not %v: the generated code is older than the schema; run velox-gen", s, i, got, reflect.TypeFor[T]()))
}

// FieldDescriptor returns the descriptor of the field name of s, declared
// by s or by one of its mixins. It is for the code velox-gen writes, which
// reads from it a default or a validator it cannot call by name. It panics
// when s has no such field, which means the generated code is older than
// the schema: run velox-gen.
func FieldDescriptor(s Interface, name string) *desc.Field {
	var lists [][]Field
	for _, m := range s.Mixin() {
		if m != nil {
			lists = append(lists, m.Fields())
		}
	}
	lists = append(lists, s.Fields())
	for _, l := range lists {
		for _, f := range l {
			if f == nil {
				continue
			}
			if d := f.Descriptor(); d != nil && d.Name == name {
				return d
			}
		}
	}
	panic(fmt.Sprintf("schema: %T has no field %q: the generated code is older than the schema; run velox-gen", s, name))
}
