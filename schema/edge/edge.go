// Package edge declares the relations between schemas. An edge is declared
// with To on the schema that owns it, and with From on the schema that holds
// the inverse. Both name the target schema by its Type method expression:
//
//	edge.To("posts", Post.Type)
//	edge.From("author", User.Type).Ref("posts").Unique()
//
// The package imports only the descriptors, the schema package and the
// standard library.
package edge

import (
	"errors"
	"reflect"

	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/internal/desc"
)

// typ returns the name and the package path of the schema type that t, a
// method expression such as Post.Type, names.
func typ(t any) (name, pkg string, ok bool) {
	rt := reflect.TypeOf(t)
	if rt == nil || rt.Kind() != reflect.Func || rt.NumIn() != 1 {
		return "", "", false
	}
	in := rt.In(0)
	if in.Kind() != reflect.Struct || in.Name() == "" || in.PkgPath() == "" {
		return "", "", false
	}
	return in.Name(), in.PkgPath(), true
}

// newEdge returns the descriptor of an edge to the schema t names, declared
// at pc. A t that is not a schema's Type method expression is a misuse, in
// the descriptor's Errs.
func newEdge(name string, t any, inverse bool, pc uintptr) *desc.Edge {
	d := &desc.Edge{Name: name, Inverse: inverse, PC: pc}
	typeName, pkg, ok := typ(t)
	if !ok {
		d.Errs = append(d.Errs, errors.New("the type must be a schema's Type method expression, such as Post.Type"))
		return d
	}
	d.Type, d.TypePkg = typeName, pkg
	return d
}

// StorageOption sets where an edge is stored: the table, the columns and the
// constraint symbols.
type StorageOption func(*desc.Edge)

// Table sets the join table of a many-to-many edge. It is refused on any other
// edge, whose foreign key is in the table of the schema that holds it, as in
// ent.
func Table(name string) StorageOption { return func(d *desc.Edge) { d.Table = name } }

// Column sets the foreign-key column of an edge that has one.
func Column(name string) StorageOption { return func(d *desc.Edge) { d.Columns = []string{name} } }

// Columns sets the two columns of a many-to-many join table, as ent reads
// them: to is the column that references the owner of the edge declared with
// To, and from the one that references that edge's target. That holds also
// when the option is given on the inverse edge, declared with From.
func Columns(to, from string) StorageOption {
	return func(d *desc.Edge) { d.Columns = []string{to, from} }
}

// Symbol sets the name of the foreign-key constraint.
func Symbol(s string) StorageOption { return func(d *desc.Edge) { d.Symbols = []string{s} } }

// Symbols sets the names of the two foreign-key constraints of a many-to-many
// join table, in the order of Columns.
func Symbols(to, from string) StorageOption {
	return func(d *desc.Edge) { d.Symbols = []string{to, from} }
}

// assocBuilder builds an edge declared with To.
type assocBuilder struct{ d *desc.Edge }

// To declares an edge named name from the schema that lists it to the schema t
// names, where t is that schema's Type method expression: Post.Type.
func To(name string, t any) *assocBuilder {
	return &assocBuilder{d: newEdge(name, t, false, desc.Caller())}
}

// Unique makes the edge point to at most one row.
func (b *assocBuilder) Unique() *assocBuilder { b.d.Unique = true; return b }

// Required makes the edge's foreign key NOT NULL.
func (b *assocBuilder) Required() *assocBuilder { b.d.Required = true; return b }

// Immutable refuses changes to the edge after create.
func (b *assocBuilder) Immutable() *assocBuilder { b.d.Immutable = true; return b }

// Field binds the edge to the foreign-key field of that name.
func (b *assocBuilder) Field(name string) *assocBuilder { b.d.Field = name; return b }

// StorageKey sets where the edge is stored.
func (b *assocBuilder) StorageKey(opts ...StorageOption) *assocBuilder {
	for _, o := range opts {
		o(b.d)
	}
	return b
}

// Comment sets the edge's documentation.
func (b *assocBuilder) Comment(s string) *assocBuilder { b.d.Comment = s; return b }

// Annotations attaches annotations to the edge.
func (b *assocBuilder) Annotations(as ...schema.Annotation) *assocBuilder {
	b.d.Annotations = append(b.d.Annotations, as...)
	return b
}

// From declares the inverse of the edge inline, under name, on the target
// schema. The options that follow configure the inverse, and Descriptor still
// returns the whole edge, with the inverse in its Ref.
func (b *assocBuilder) From(name string) *inverseBuilder {
	b.d.Ref = &desc.Edge{Name: name, Type: b.d.Type, TypePkg: b.d.TypePkg, Inverse: true, RefName: b.d.Name, PC: b.d.PC}
	return &inverseBuilder{d: b.d.Ref, root: b.d}
}

// Descriptor returns the edge's descriptor.
func (b *assocBuilder) Descriptor() *desc.Edge { return b.d }

// inverseBuilder builds an edge declared with From, or the inverse of an edge
// declared with To(...).From(...).
type inverseBuilder struct {
	d    *desc.Edge // what the options change
	root *desc.Edge // what Descriptor returns; d itself unless the inverse is inline
}

// From declares the edge named name on the schema t names as the inverse of
// the edge that schema declares with To. Ref names that edge.
func From(name string, t any) *inverseBuilder {
	d := newEdge(name, t, true, desc.Caller())
	return &inverseBuilder{d: d, root: d}
}

// Ref names the edge the inverse mirrors, as declared with To on the target
// schema.
func (b *inverseBuilder) Ref(name string) *inverseBuilder { b.d.RefName = name; return b }

// Unique makes the edge point to at most one row.
func (b *inverseBuilder) Unique() *inverseBuilder { b.d.Unique = true; return b }

// Required makes the edge's foreign key NOT NULL.
func (b *inverseBuilder) Required() *inverseBuilder { b.d.Required = true; return b }

// Immutable refuses changes to the edge after create.
func (b *inverseBuilder) Immutable() *inverseBuilder { b.d.Immutable = true; return b }

// Field binds the edge to the foreign-key field of that name.
func (b *inverseBuilder) Field(name string) *inverseBuilder { b.d.Field = name; return b }

// StorageKey sets where the edge is stored.
func (b *inverseBuilder) StorageKey(opts ...StorageOption) *inverseBuilder {
	for _, o := range opts {
		o(b.d)
	}
	return b
}

// Comment sets the edge's documentation.
func (b *inverseBuilder) Comment(s string) *inverseBuilder { b.d.Comment = s; return b }

// Annotations attaches annotations to the edge.
func (b *inverseBuilder) Annotations(as ...schema.Annotation) *inverseBuilder {
	b.d.Annotations = append(b.d.Annotations, as...)
	return b
}

// Descriptor returns the edge's descriptor: for an inverse declared inline, the
// edge declared with To, whose Ref is the inverse.
func (b *inverseBuilder) Descriptor() *desc.Edge { return b.root }
