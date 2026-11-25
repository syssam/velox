package gqlgenx

import (
	"context"
	"slices"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// source is gqlgen's selection source, which the Extension installs with
// each operation's state. The field of a context is gqlgen's field context
// (graphql.GetFieldContext); its key is the *ast.Field with the selections
// gqlgen merged under its response key, so two aliases, or one field merged
// with two different sets of fragments, are two keys; its selection on a
// type is graphql.CollectFields with the type's interfaces, which the
// server's schema gives. Wait does nothing: gqlgen runs every field in a
// goroutine of its own, and a field that waits holds back no other.
type source struct {
	// schema is the server's, nil when unknown: On reads the interfaces of a
	// type from it.
	schema *ast.Schema

	mu sync.Mutex
	// sels are the field occurrences met, per *ast.Field: each the key and
	// the selection of its field.
	sels map[*ast.Field][]*selection
}

// selection is a sub-selection of an operation: gqlgen's selections, and
// what reads them.
type selection struct {
	src  *source
	oc   *graphql.OperationContext
	sels ast.SelectionSet
}

var _ gqlcore.SelectionSource = (*source)(nil)

// Field is the field of gqlgen's field context in ctx. Its Key is nil when
// gqlgen gives the field no *ast.Field, and its Sel nil without an
// operation context or a definition of the field: the caller then reads
// alone, and every column.
func (s *source) Field(ctx context.Context) (gqlcore.Field, bool) {
	fc := graphql.GetFieldContext(ctx)
	if fc == nil {
		return gqlcore.Field{}, false
	}
	return s.field(ctx, fc), true
}

// field is Field of fc, gqlgen's field context in ctx.
func (s *source) field(ctx context.Context, fc *graphql.FieldContext) gqlcore.Field {
	f := gqlcore.Field{Object: fc.Object}
	if fc.Field.Field == nil {
		return f
	}
	f.Name = fc.Field.Name
	var oc *graphql.OperationContext
	if graphql.HasOperationContext(ctx) {
		oc = graphql.GetOperationContext(ctx)
	}
	sel := s.occurrence(fc.Field.Field, fc.Field.Selections, oc)
	f.Key = sel
	if oc != nil && fc.Field.Definition != nil {
		f.Type, f.Sel = fc.Field.Definition.Type.Name(), sel
	}
	return f
}

// Wait does nothing.
func (*source) Wait(context.Context) func() { return noWait }

// noWait is Wait's done function.
func noWait() {}

// occurrence is the one *selection of the field f with the selections sels
// under oc: made on its first call, and the same pointer on every other, so
// that it keys batches and selections. Selections are compared by their
// elements, as gqlgen merges them anew for each object.
func (s *source) occurrence(f *ast.Field, sels ast.SelectionSet, oc *graphql.OperationContext) *selection {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sel := range s.sels[f] {
		if sel.oc == oc && slices.Equal(sel.sels, sels) {
			return sel
		}
	}
	if s.sels == nil {
		s.sels = map[*ast.Field][]*selection{}
	}
	sel := &selection{src: s, oc: oc, sels: sels}
	s.sels[f] = append(s.sels[f], sel)
	return sel
}

// On is the fields sel selects on an object of the type typ, through its
// fragments on typ and on each interface and union typ is a member of, as
// the schema says. Without a schema, that is typ alone, and a fragment on an
// interface or a union fails it.
func (sel *selection) On(typ string) ([]gqlcore.SelectedField, bool) {
	cs, ok := collect(sel.oc, sel.src.schema, sel.sels, typ)
	if !ok {
		return nil, false
	}
	out := make([]gqlcore.SelectedField, len(cs))
	for i, c := range cs {
		out[i].Name = c.Name
		if len(c.Selections) > 0 {
			out[i].Sel = &selection{src: sel.src, oc: sel.oc, sels: c.Selections}
		}
	}
	return out, true
}

// collect is the fields sels selects on an object of the type typ, through
// its fragments on typ and on each interface and union typ is a member of,
// as schema says. Without a schema, that is typ alone, and a fragment on an
// interface or a union fails it.
func collect(oc *graphql.OperationContext, schema *ast.Schema, sels ast.SelectionSet, typ string) ([]graphql.CollectedField, bool) {
	sat := []string{typ}
	if d := schemaType(schema, typ); d != nil {
		for _, i := range schema.GetImplements(d) {
			sat = append(sat, i.Name)
		}
	} else {
		// No schema (the registered source): the interfaces of typ are
		// those the validator's definition of typ lists, found on a field
		// selected on it. A union is not among them, and a fragment on one
		// fails, as one on an interface the definition does not name.
		if d := objectDef(oc, sels, typ, map[string]bool{}); d != nil {
			sat = append(sat, d.Interfaces...)
		}
		if abstractFragment(oc, sels, sat, map[string]bool{}) {
			return nil, false
		}
	}
	return graphql.CollectFields(oc, sels, sat), true
}

// objectDef is the definition of the object type typ that the validator
// gave a field of sels selected on it, through its fragments; nil when no
// field has one.
func objectDef(oc *graphql.OperationContext, sels ast.SelectionSet, typ string, seen map[string]bool) *ast.Definition {
	for _, sel := range sels {
		var sub ast.SelectionSet
		switch sel := sel.(type) {
		case *ast.Field:
			if d := sel.ObjectDefinition; d != nil && d.Name == typ && d.Kind == ast.Object {
				return d
			}
			continue
		case *ast.InlineFragment:
			sub = sel.SelectionSet
		case *ast.FragmentSpread:
			if seen[sel.Name] || oc == nil || oc.Doc == nil {
				continue
			}
			seen[sel.Name] = true
			fd := oc.Doc.Fragments.ForName(sel.Name)
			if fd == nil {
				continue
			}
			sub = fd.SelectionSet
		}
		if d := objectDef(oc, sub, typ, seen); d != nil {
			return d
		}
	}
	return nil
}

// schemaType is typ's definition in schema; nil when schema is nil or has
// none.
func schemaType(schema *ast.Schema, typ string) *ast.Definition {
	if schema == nil {
		return nil
	}
	return schema.Types[typ]
}

// abstractFragment reports whether sels holds, at any depth of fragments, a
// fragment on an interface or a union not in sat, or on a type it cannot
// tell is an object type.
func abstractFragment(oc *graphql.OperationContext, sels ast.SelectionSet, sat []string, seen map[string]bool) bool {
	abstract := func(cond string, d *ast.Definition) bool {
		return cond != "" && !slices.Contains(sat, cond) && (d == nil || d.Kind != ast.Object)
	}
	for _, sel := range sels {
		switch sel := sel.(type) {
		case *ast.InlineFragment:
			// The validator gives an inline fragment its parent's definition;
			// the fields in it have the type condition's.
			var d *ast.Definition
			for _, c := range sel.SelectionSet {
				if f, ok := c.(*ast.Field); ok && f.ObjectDefinition != nil {
					d = f.ObjectDefinition
					break
				}
			}
			if abstract(sel.TypeCondition, d) || abstractFragment(oc, sel.SelectionSet, sat, seen) {
				return true
			}
		case *ast.FragmentSpread:
			if seen[sel.Name] {
				continue
			}
			seen[sel.Name] = true
			d := oc.Doc.Fragments.ForName(sel.Name)
			if d == nil {
				continue
			}
			if abstract(d.TypeCondition, d.Definition) || abstractFragment(oc, d.SelectionSet, sat, seen) {
				return true
			}
		}
	}
	return false
}
