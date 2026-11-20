package gogen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"

	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// leftOut are the fields of t's entity that the create input does not set and
// that an insert would store as their zero value though the schema requires
// them: not Optional, not nullable, with no Default, and neither the key nor
// a field the schema leaves out of the create input explicitly, which the
// application or a hook sets, as a tenancy mixin's hook sets its column. A
// resolver sets them on the x it inserts.
func leftOut(t *sdl.Type) []*plugin.Field {
	e := t.Entity
	set := map[int]bool{e.PrimaryKey[0]: true}
	for i, fd := range e.Fields {
		if sdl.SkipsCreate(fd) {
			set[i] = true
		}
	}
	for _, fi := range t.Create {
		switch {
		case fi.Kind == sdl.InputValue:
			set[fi.Field] = true
		case fi.Kind == sdl.InputEdgeID && e.Edges[fi.Edge].OwnsKey:
			for i, fd := range e.Fields {
				if fd.Name == e.Edges[fi.Edge].Field {
					set[i] = true
				}
			}
		}
	}
	var out []*plugin.Field
	for i, fd := range e.Fields {
		if !set[i] && !fd.Optional && !fd.Nullable && !fd.HasDefault {
			out = append(out, fd)
		}
	}
	return out
}

// ownedEdgeOf is the edge of e whose key is e's own field named name; nil
// when the field is no edge's key.
func ownedEdgeOf(e *plugin.Entity, name string) *plugin.Edge {
	for _, ed := range e.Edges {
		if ed.OwnsKey && ed.Field == name {
			return ed
		}
	}
	return nil
}

// fieldOf is e's field named name; nil when e has none.
func fieldOf(e *plugin.Entity, name string) *plugin.Field {
	for _, fd := range e.Fields {
		if fd.Name == name {
			return fd
		}
	}
	return nil
}

// fkOf is the foreign-key field ed.Field of on, the entity that holds it
// (e for an edge that owns its key, else the target). Its error names e, ed
// and on.
func fkOf(e *plugin.Entity, ed *plugin.Edge, on *plugin.Entity) (*plugin.Field, error) {
	fk := fieldOf(on, ed.Field)
	if fk == nil {
		return nil, fmt.Errorf("schema %s, edge %s: its foreign key %s is no field of %s", e.Name, ed.Name, ed.Field, on.Name)
	}
	return fk, nil
}

// inputType is the Go type of the input field fi: its GoType for a value,
// the key's Go type of the edge's target for an ID, a slice of it for a list
// of IDs and a bool for a clear; a pointer when it is not Required, but for
// a list or a bool.
func (r *renderer) inputType(f *file, t *sdl.Type, fi sdl.InputField) (string, error) {
	var typ string
	switch fi.Kind {
	case sdl.InputValue:
		fd := t.Entity.Fields[fi.Field]
		var err error
		if typ, err = r.qualify(f, t.Entity, fd, fi.GoType, fi.GoPkg); err != nil {
			return "", err
		}
	case sdl.InputEdgeID, sdl.InputEdgeIDs, sdl.InputAddIDs, sdl.InputRemoveIDs:
		k, err := r.idKeyOf(f, t.Entity, r.types[fi.Target])
		if err != nil {
			return "", err
		}
		if fi.Kind != sdl.InputEdgeID {
			return "[]" + k.typ, nil
		}
		typ = k.typ
	case sdl.InputClear, sdl.InputClearEdge:
		return "bool", nil
	default:
		return "", fmt.Errorf("velox-graphql writes no mutation-input field of kind %d, which is a bug: schema %s, field %s", fi.Kind, t.Entity.Name, fi.Name)
	}
	if !fi.Required {
		typ = "*" + typ
	}
	return typ, nil
}

// idKey is the Go type of the IDs of a type: its key field's own, which
// gqlgen binds the IDs to.
type idKey struct {
	typ string // the key's Go type, as the file names it
	fd  *plugin.Field
}

// idKeyOf is the idKey of tt, a type with an ID, the target of an edge of e.
func (r *renderer) idKeyOf(f *file, e *plugin.Entity, tt *sdl.Type) (idKey, error) {
	if tt == nil || !tt.HasID {
		return idKey{}, fmt.Errorf("schema %s: an input takes IDs of a type that has none, which is a bug", e.Name)
	}
	tk := tt.Entity.Fields[tt.Entity.PrimaryKey[0]]
	typ, err := r.qualify(f, tt.Entity, tk, tt.KeyType, tt.KeyPkg)
	if err != nil {
		return idKey{}, err
	}
	return idKey{typ: typ, fd: tk}, nil
}

// sameGoType reports whether the fields a and b hold the same Go type; a
// field whose model gives none holds velox's own, as any other may.
func sameGoType(a, b *plugin.Field) bool {
	return a.ElemType == "" || b.ElemType == "" || a.ElemType == b.ElemType && a.PkgPath == b.PkgPath
}

// valueConv is the Go conversion of the input field fi's value to the Go
// type of fd, its field ("idspkg.Code"): "" when the input holds fd's own
// Go type.
func (r *renderer) valueConv(f *file, e *plugin.Entity, fd *plugin.Field, fi sdl.InputField) (string, error) {
	if fd.ElemType == "" || fd.ElemType == fi.GoType && (fd.PkgPath == fi.GoPkg || fd.PkgPath == "") {
		return "", nil
	}
	var base string
	switch fd.Type {
	case "string", "enum":
		base = "string"
	case "bytes":
		base = "[]byte"
	case "uuid":
		base = "[16]byte"
	case "time":
		// converts refuses any other Go type when the model gives its kind,
		// and this when it does not (a model written by hand).
		if err := r.converts(e, fd, "time.Time"); err != nil {
			return "", err
		}
		return "", fmt.Errorf("schema %s, field %s: velox-graphql converts a time field only from time.Time, and its Go type is %s; give the field time.Time", e.Name, fd.Name, fd.ElemType)
	default:
		base = fd.Type
	}
	if err := r.converts(e, fd, base); err != nil {
		return "", err
	}
	return r.qualify(f, e, fd, fd.ElemType, fd.PkgPath)
}

// goType is the Go type expression typ of the field fd, as the file names
// it: each package it names imported, or unqualified when it is the
// generated package. typ may be composite, as a JSON field's []ids.Tag or
// map[string]x.T.
func (r *renderer) goType(f *file, e *plugin.Entity, fd *plugin.Field, typ string) (string, error) {
	fset := token.NewFileSet()
	x, err := parser.ParseExprFrom(fset, "", typ, 0)
	if err != nil {
		return "", fmt.Errorf("schema %s, field %s: velox-graphql cannot read its Go type %s: %w", e.Name, fd.Name, typ, err)
	}
	type edit struct {
		from, to int
		text     string
	}
	var edits []edit
	ast.Inspect(x, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || err != nil {
			return err == nil
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		pkg := ""
		if fd.PkgPath != "" && path.Base(fd.PkgPath) == id.Name {
			pkg = fd.PkgPath
		}
		var q string
		if q, err = r.qualify(f, e, fd, id.Name+"."+sel.Sel.Name, pkg); err != nil {
			return false
		}
		edits = append(edits, edit{fset.Position(sel.Pos()).Offset, fset.Position(sel.End()).Offset, q})
		return false
	})
	if err != nil {
		return "", err
	}
	for _, edit := range slices.Backward(edits) {
		typ = typ[:edit.from] + edit.text + typ[edit.to:]
	}
	return typ, nil
}
