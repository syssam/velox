package integration_test

import (
	"testing"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// TestTestschemaCoversGeneratorShapes fails when testschema stops containing
// a schema shape the generator emits distinct code for. Bugs shipped for
// exactly this reason: the eager-load panic on a NULL key lived in the
// optional hidden-key to-one edge, which testschema did not have; the edge-
// schema defaults registry had no reader in the root module until an edge
// schema was added; the one-to-one and self-referencing paths were covered
// only by examples. Every shape here must stay reachable from the root
// module's integration suite. Add a shape when the generator grows a branch.
func TestTestschemaCoversGeneratorShapes(t *testing.T) {
	cfg, err := gen.NewConfig(
		gen.WithTarget("."), // the generator's own target: tests/integration
		gen.WithPackage("github.com/syssam/velox/tests/integration"),
		gen.WithFeatures(gen.FeaturePrivacy),
	)
	if err != nil {
		t.Fatal(err)
	}
	g, err := compiler.LoadGraph("../../testschema", cfg)
	if err != nil {
		t.Fatal(err)
	}

	hidden := func(e *gen.Edge) bool { return e.Field() == nil || !e.Field().UserDefined }
	edgeShapes := map[string]func(*gen.Type, *gen.Edge) bool{
		"one-to-many edge":                         func(_ *gen.Type, e *gen.Edge) bool { return e.O2M() && !e.IsInverse() },
		"required to-one edge with a hidden key":   func(_ *gen.Type, e *gen.Edge) bool { return e.M2O() && !e.Optional && hidden(e) },
		"optional to-one edge with a hidden key":   func(_ *gen.Type, e *gen.Edge) bool { return e.M2O() && e.Optional && hidden(e) },
		"to-one edge bound to a field (.Field())":  func(_ *gen.Type, e *gen.Edge) bool { return e.Unique && !hidden(e) },
		"many-to-many edge":                        func(_ *gen.Type, e *gen.Edge) bool { return e.M2M() && e.Through == nil },
		"many-to-many edge through an edge schema": func(_ *gen.Type, e *gen.Edge) bool { return e.M2M() && e.Through != nil },
		"one-to-one edge, key-owning side":         func(_ *gen.Type, e *gen.Edge) bool { return e.O2O() && e.OwnFK() },
		"one-to-one edge, other side":              func(_ *gen.Type, e *gen.Edge) bool { return e.O2O() && !e.OwnFK() },
		"self-referencing edge":                    func(t *gen.Type, e *gen.Edge) bool { return e.Type == t },
		"edge to an entity with a privacy policy":  func(_ *gen.Type, e *gen.Edge) bool { return e.Type.NumPolicy() > 0 },
		"edge between entities with different ID types": func(t *gen.Type, e *gen.Edge) bool {
			return t.ID != nil && e.Type.ID != nil && t.ID.Type.Type != e.Type.ID.Type.Type
		},
	}
	fieldShapes := map[string]func(*gen.Field) bool{
		"Nillable field":                   func(f *gen.Field) bool { return f.Nillable },
		"Optional, non-Nillable field":     func(f *gen.Field) bool { return f.Optional && !f.Nillable },
		"Immutable field":                  func(f *gen.Field) bool { return f.Immutable },
		"enum field":                       func(f *gen.Field) bool { return f.IsEnum() },
		"time field":                       func(f *gen.Field) bool { return f.IsTime() },
		"JSON slice field (AppendXxx)":     func(f *gen.Field) bool { return f.SupportsMutationAppend() },
		"field with a Go-function default": func(f *gen.Field) bool { return f.Default && f.DefaultFunc() },
		"field with a validator":           func(f *gen.Field) bool { return f.Validators > 0 },
	}
	typeShapes := map[string]func(*gen.Type) bool{
		"UUID-keyed entity":            func(t *gen.Type) bool { return t.ID != nil && t.ID.Type.Type == field.TypeUUID },
		"int-keyed entity":             func(t *gen.Type) bool { return t.ID != nil && t.ID.Type.Type.Integer() },
		"entity with a privacy policy": func(t *gen.Type) bool { return t.NumPolicy() > 0 },
		"entity with schema hooks":     func(t *gen.Type) bool { return t.NumHooks() > 0 },
		"edge schema":                  func(t *gen.Type) bool { return t.IsEdgeSchema() },
	}

	found := map[string]bool{}
	for _, t := range g.Nodes {
		for name, has := range typeShapes {
			if has(t) {
				found[name] = true
			}
		}
		for _, f := range t.Fields {
			for name, has := range fieldShapes {
				if has(f) {
					found[name] = true
				}
			}
		}
		for _, e := range t.Edges {
			for name, has := range edgeShapes {
				if has(t, e) {
					found[name] = true
				}
			}
		}
	}
	for _, shapes := range []any{edgeShapes, fieldShapes, typeShapes} {
		var names []string
		switch m := shapes.(type) {
		case map[string]func(*gen.Type, *gen.Edge) bool:
			for n := range m {
				names = append(names, n)
			}
		case map[string]func(*gen.Field) bool:
			for n := range m {
				names = append(names, n)
			}
		case map[string]func(*gen.Type) bool:
			for n := range m {
				names = append(names, n)
			}
		}
		for _, n := range names {
			if !found[n] {
				t.Errorf("testschema has no %s — add one so the integration suite exercises that generator path", n)
			}
		}
	}
}
