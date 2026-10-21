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
	"fmt"
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

// Action is what the database does to the rows that hold a foreign key when the
// row it references is deleted.
type Action string

// The actions OnDelete takes. An edge that sets none has the dialect's default,
// NO ACTION.
const (
	Cascade  Action = "CASCADE"
	SetNull  Action = "SET NULL"
	Restrict Action = "RESTRICT"
)

// setOnDelete records a on d, or the misuse.
func setOnDelete(d *desc.Edge, a Action) {
	switch a {
	case Cascade, SetNull, Restrict:
		d.OnDelete = string(a)
	default:
		d.Errs = append(d.Errs, fmt.Errorf("OnDelete(%q) is not edge.Cascade, edge.SetNull or edge.Restrict", string(a)))
	}
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

// AssocBuilder builds an edge declared with To. Its From declares the inverse
// inline, and returns an InverseBuilder.
type AssocBuilder struct{ d *desc.Edge }

// To declares an edge named name from the schema that lists it to the schema t
// names, where t is that schema's Type method expression: Post.Type.
func To(name string, t any) *AssocBuilder {
	return &AssocBuilder{d: newEdge(name, t, false, desc.Caller())}
}

// Unique makes the edge point to at most one row.
func (b *AssocBuilder) Unique() *AssocBuilder { b.d.Unique = true; return b }

// Required makes the edge's foreign key NOT NULL.
func (b *AssocBuilder) Required() *AssocBuilder { b.d.Required = true; return b }

// Immutable refuses changes to the edge after create.
func (b *AssocBuilder) Immutable() *AssocBuilder { b.d.Immutable = true; return b }

// Field binds the edge to the foreign-key field of that name.
func (b *AssocBuilder) Field(name string) *AssocBuilder { b.d.Field = name; return b }

// StorageKey sets where the edge is stored.
func (b *AssocBuilder) StorageKey(opts ...StorageOption) *AssocBuilder {
	for _, o := range opts {
		o(b.d)
	}
	return b
}

// OnDelete sets what the database does to the rows holding the edge's foreign
// key when the row they reference is deleted. SetNull needs a NULL-able key:
// the edge not Required, and its field Nillable. A many-to-many edge has no
// foreign key to act on. Both edges of a relation must agree.
func (b *AssocBuilder) OnDelete(a Action) *AssocBuilder { setOnDelete(b.d, a); return b }

// Comment sets the edge's documentation.
func (b *AssocBuilder) Comment(s string) *AssocBuilder { b.d.Comment = s; return b }

// Annotations attaches annotations to the edge.
func (b *AssocBuilder) Annotations(as ...schema.Annotation) *AssocBuilder {
	b.d.Annotations = append(b.d.Annotations, as...)
	return b
}

// From declares the inverse of the edge inline, under name, on the target
// schema. The options that follow configure the inverse, and Descriptor still
// returns the whole edge, with the inverse in its Ref.
func (b *AssocBuilder) From(name string) *InverseBuilder {
	b.d.Ref = &desc.Edge{Name: name, Type: b.d.Type, TypePkg: b.d.TypePkg, Inverse: true, RefName: b.d.Name, PC: b.d.PC}
	return &InverseBuilder{d: b.d.Ref, root: b.d}
}

// Descriptor returns the edge's descriptor.
func (b *AssocBuilder) Descriptor() *desc.Edge { return b.d }

// InverseBuilder builds an edge declared with From, or the inverse of an edge
// declared with To(...).From(...).
type InverseBuilder struct {
	d    *desc.Edge // what the options change
	root *desc.Edge // what Descriptor returns; d itself unless the inverse is inline
}

// From declares the edge named name on the schema t names as the inverse of
// the edge that schema declares with To. Ref names that edge.
func From(name string, t any) *InverseBuilder {
	d := newEdge(name, t, true, desc.Caller())
	return &InverseBuilder{d: d, root: d}
}

// Ref names the edge the inverse mirrors, as declared with To on the target
// schema.
func (b *InverseBuilder) Ref(name string) *InverseBuilder { b.d.RefName = name; return b }

// Unique makes the edge point to at most one row.
func (b *InverseBuilder) Unique() *InverseBuilder { b.d.Unique = true; return b }

// Required makes the edge's foreign key NOT NULL.
func (b *InverseBuilder) Required() *InverseBuilder { b.d.Required = true; return b }

// Immutable refuses changes to the edge after create.
func (b *InverseBuilder) Immutable() *InverseBuilder { b.d.Immutable = true; return b }

// Field binds the edge to the foreign-key field of that name.
func (b *InverseBuilder) Field(name string) *InverseBuilder { b.d.Field = name; return b }

// StorageKey sets where the edge is stored.
func (b *InverseBuilder) StorageKey(opts ...StorageOption) *InverseBuilder {
	for _, o := range opts {
		o(b.d)
	}
	return b
}

// OnDelete sets what the database does to the rows holding the edge's foreign
// key when the row they reference is deleted. On an inverse declared inline
// with To(...).From(...), it sets the inverse's side. See AssocBuilder.OnDelete.
func (b *InverseBuilder) OnDelete(a Action) *InverseBuilder { setOnDelete(b.d, a); return b }

// Comment sets the edge's documentation.
func (b *InverseBuilder) Comment(s string) *InverseBuilder { b.d.Comment = s; return b }

// Annotations attaches annotations to the edge.
func (b *InverseBuilder) Annotations(as ...schema.Annotation) *InverseBuilder {
	b.d.Annotations = append(b.d.Annotations, as...)
	return b
}

// Descriptor returns the edge's descriptor: for an inverse declared inline, the
// edge declared with To, whose Ref is the inverse.
func (b *InverseBuilder) Descriptor() *desc.Edge { return b.root }
