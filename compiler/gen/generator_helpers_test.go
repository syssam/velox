package gen

// Tests for the JenniferGenerator helper methods (generate.go, helper_entity.go, option.go).

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/schema/field"
)

// =============================================================================
// generate.go — JenniferGenerator helpers
// =============================================================================

func TestJenniferGenerator_WithPackage(t *testing.T) {
	g := &JenniferGenerator{pkg: "original", generatedEnums: make(map[string]bool)}
	g.WithPackage("newpkg")
	assert.Equal(t, "newpkg", g.pkg)

	// Empty string → no change.
	g.WithPackage("")
	assert.Equal(t, "newpkg", g.pkg)
}

func TestJenniferGenerator_WithWorkers(t *testing.T) {
	g := &JenniferGenerator{workers: 4, generatedEnums: make(map[string]bool)}
	g.WithWorkers(8)
	assert.Equal(t, 8, g.workers)
	g.WithWorkers(0)
	assert.Equal(t, 8, g.workers)
}

func TestJenniferGenerator_VeloxPkg(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	assert.Equal(t, "github.com/syssam/velox", g.VeloxPkg())
}

func TestJenniferGenerator_SQLPkg(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	assert.Equal(t, "github.com/syssam/velox/dialect/sql", g.SQLPkg())
}

func TestJenniferGenerator_SQLGraphPkg(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	assert.Equal(t, "github.com/syssam/velox/dialect/sql/sqlgraph", g.SQLGraphPkg())
}

func TestJenniferGenerator_FieldPkg(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	assert.Equal(t, "github.com/syssam/velox/schema/field", g.FieldPkg())
}

func TestJenniferGenerator_LeafPkgPath(t *testing.T) {
	graph := &Graph{Config: &Config{Package: "example.com/app/ent"}}
	graph.Package = "example.com/app/ent"
	g := &JenniferGenerator{
		graph:          graph,
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	typ := &Type{Name: "User"}
	assert.Equal(t, "example.com/app/ent/user", g.LeafPkgPath(typ))
}

func TestJenniferGenerator_LeafPkgPath_FallbackPkg(t *testing.T) {
	// When graph.Config is nil or Package is empty, falls back to g.pkg.
	graph := &Graph{}
	g := &JenniferGenerator{
		graph:          graph,
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	typ := &Type{Name: "Post"}
	assert.Equal(t, "ent/post", g.LeafPkgPath(typ))
}

func TestJenniferGenerator_SharedEntityPkg(t *testing.T) {
	graph := &Graph{Config: &Config{Package: "example.com/app/ent"}}
	graph.Package = "example.com/app/ent"
	g := &JenniferGenerator{graph: graph, pkg: "ent", generatedEnums: make(map[string]bool)}
	assert.Equal(t, "example.com/app/ent/entity", g.SharedEntityPkg())

	// Without a graph package it falls back to the generator's own pkg.
	g = &JenniferGenerator{graph: &Graph{}, pkg: "ent", generatedEnums: make(map[string]bool)}
	assert.Equal(t, "ent/entity", g.SharedEntityPkg())
}

func TestJenniferGenerator_PredicateType(t *testing.T) {
	graph := &Graph{Config: &Config{Package: "example.com/app/ent"}}
	graph.Package = "example.com/app/ent"
	g := &JenniferGenerator{
		graph:          graph,
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	assert.Equal(t, "predicate.User", render(g.PredicateType(&Type{Name: "User"})))
}

func TestJenniferGenerator_EdgePredicateType(t *testing.T) {
	graph := &Graph{Config: &Config{Package: "example.com/app/ent"}}
	graph.Package = "example.com/app/ent"
	g := &JenniferGenerator{
		graph:          graph,
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	// The edge's target type, not the edge name.
	assert.Equal(t, "predicate.Post", render(g.EdgePredicateType(&Edge{Name: "posts", Type: &Type{Name: "Post"}})))
}

func TestJenniferGenerator_FeatureEnabled(t *testing.T) {
	graph, err := NewGraph(&Config{Package: "entc/gen", Storage: drivers["sql"]}, T1, T2)
	require.NoError(t, err)
	g := &JenniferGenerator{
		graph:          graph,
		generatedEnums: make(map[string]bool),
	}
	// Non-existent feature → false (with slog warning, no panic).
	assert.False(t, g.FeatureEnabled("nonexistent_feature"))
	// Known feature name.
	assert.False(t, g.FeatureEnabled(FeatureSnapshot.Name))
}

func TestJenniferGenerator_InternalPkg(t *testing.T) {
	graph := &Graph{Config: &Config{Package: "example.com/app/ent"}}
	g := &JenniferGenerator{
		graph:          graph,
		generatedEnums: make(map[string]bool),
	}
	assert.Equal(t, "example.com/app/ent/internal", g.InternalPkg())
}

func TestJenniferGenerator_RootPkg_NoEntityPackageDialect(t *testing.T) {
	graph := &Graph{Config: &Config{Package: "example.com/app/ent"}}
	graph.Package = "example.com/app/ent"
	// Use a minimal dialect that doesn't implement EntityPackageDialect.
	g := &JenniferGenerator{
		graph:          graph,
		generatedEnums: make(map[string]bool),
		dialect:        nil, // no dialect
	}
	assert.Equal(t, "", g.RootPkg())
}

func TestJenniferGenerator_Pkg(t *testing.T) {
	g := &JenniferGenerator{pkg: "ent", generatedEnums: make(map[string]bool)}
	assert.Equal(t, "ent", g.Pkg())
}

func TestJenniferGenerator_Graph(t *testing.T) {
	graph, err := NewGraph(&Config{Package: "entc/gen", Storage: drivers["sql"]}, T1, T2)
	require.NoError(t, err)
	g := &JenniferGenerator{
		graph:          graph,
		generatedEnums: make(map[string]bool),
	}
	assert.Equal(t, graph, g.Graph())
}

func TestJenniferGenerator_MarkEnumGenerated(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	// First call → not already generated.
	assert.False(t, g.MarkEnumGenerated("StatusEnum"))
	// Second call → already generated.
	assert.True(t, g.MarkEnumGenerated("StatusEnum"))
	// Different enum → not yet generated.
	assert.False(t, g.MarkEnumGenerated("RoleEnum"))
}

func TestJenniferGenerator_ZeroValue(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	assert.Equal(t, "0", render(g.ZeroValue(nil)))
	// Nillable fields are pointers: nil, whatever the base type.
	assert.Equal(t, "nil", render(g.ZeroValue(&Field{Nillable: true, Type: &field.TypeInfo{Type: field.TypeString}})))
	assert.Equal(t, `""`, render(g.ZeroValue(&Field{Type: &field.TypeInfo{Type: field.TypeString}})))
}

func TestJenniferGenerator_AnnotationExists(t *testing.T) {
	cfg := &Config{}
	cfg.Annotations = Annotations{"foo": "bar"}
	g := &JenniferGenerator{
		graph:          &Graph{Config: cfg},
		generatedEnums: make(map[string]bool),
	}
	assert.True(t, g.AnnotationExists("foo"))
	assert.False(t, g.AnnotationExists("bar"))

	// Nil annotations.
	g2 := &JenniferGenerator{
		graph:          &Graph{Config: &Config{}},
		generatedEnums: make(map[string]bool),
	}
	assert.False(t, g2.AnnotationExists("anything"))
}

func TestJenniferGenerator_NewFile(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	code := g.NewFile("ent").GoString()
	assert.True(t, strings.HasPrefix(code, "// Code generated by velox. DO NOT EDIT.\n"), code)
	assert.Contains(t, code, "package ent\n")
}

// render renders a single jen.Code value outside a file.
func render(c jen.Code) string { return jen.Add(c).GoString() }

// =============================================================================
// helper_entity.go
// =============================================================================

func TestEntityPkgHelper_Pkg(t *testing.T) {
	base := &JenniferGenerator{
		graph:          &Graph{Config: &Config{Package: "example.com/app/ent"}},
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	h := newEntityPkgHelper(base, "user", "example.com/app/ent")
	assert.Equal(t, "user", h.(*entityPkgHelper).Pkg())
}

func TestEntityPkgHelper_RootPkg(t *testing.T) {
	base := &JenniferGenerator{
		graph:          &Graph{Config: &Config{Package: "example.com/app/ent"}},
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	h := newEntityPkgHelper(base, "user", "example.com/app/ent")
	assert.Equal(t, "example.com/app/ent", h.(*entityPkgHelper).RootPkg())
}

func TestEntityPkgHelper_LeafPkgPath_Self(t *testing.T) {
	base := &JenniferGenerator{
		graph:          &Graph{Config: &Config{Package: "example.com/app/ent"}},
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	h := newEntityPkgHelper(base, "user", "example.com/app/ent")
	// Self-reference → empty (no self-import).
	assert.Equal(t, "", h.(*entityPkgHelper).LeafPkgPath(&Type{Name: "User"}))
}

func TestEntityPkgHelper_LeafPkgPath_Other(t *testing.T) {
	base := &JenniferGenerator{
		graph:          &Graph{Config: &Config{Package: "example.com/app/ent"}},
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	h := newEntityPkgHelper(base, "user", "example.com/app/ent")
	// Other entity → delegates to base.
	path := h.(*entityPkgHelper).LeafPkgPath(&Type{Name: "Post"})
	assert.Contains(t, path, "post")
}

func TestEntityPkgHelper_GoType_Enum(t *testing.T) {
	base := &JenniferGenerator{
		graph:          &Graph{Config: &Config{Package: "example.com/app/ent"}},
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	h := newEntityPkgHelper(base, "user", "example.com/app/ent")
	f := &Field{
		Name: "status",
		Type: &field.TypeInfo{Type: field.TypeEnum},
	}
	// The helper renders inside the entity's own package, so its enum is local.
	assert.Equal(t, "Status", render(h.(*entityPkgHelper).GoType(f)))

	// Nillable enum → pointer.
	f2 := &Field{
		Name:     "status",
		Nillable: true,
		Type:     &field.TypeInfo{Type: field.TypeEnum},
	}
	assert.Equal(t, "*Status", render(h.(*entityPkgHelper).GoType(f2)))
}

func TestEntityPkgHelper_BaseType_Enum(t *testing.T) {
	base := &JenniferGenerator{
		graph:          &Graph{Config: &Config{Package: "example.com/app/ent"}},
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	h := newEntityPkgHelper(base, "user", "example.com/app/ent")
	f := &Field{
		Name: "status",
		Type: &field.TypeInfo{Type: field.TypeEnum},
	}
	assert.Equal(t, "Status", render(h.(*entityPkgHelper).BaseType(f)))
}

// =============================================================================
// option.go — WithGenerator
// =============================================================================

func TestWithGenerator(t *testing.T) {
	g := GenerateFunc(func(_ *Graph) error { return nil })
	opt := WithGenerator(g)
	c := &Config{}
	require.NoError(t, opt(c))
	assert.NotNil(t, c.Generator)
}

func TestWithGenerator_Nil(t *testing.T) {
	opt := WithGenerator(nil)
	c := &Config{}
	err := opt(c)
	assert.Error(t, err)
}

// =============================================================================
// generate.go — osFS.WriteFile, osFS.Glob
// =============================================================================

func TestWriteFileResult(t *testing.T) {
	g := &JenniferGenerator{outDir: t.TempDir(), generatedEnums: make(map[string]bool)}
	err := g.writeFileResult(context.Background(), nil, nil, "", "skip.go")
	assert.NoError(t, err)
	err2 := g.writeFileResult(context.Background(), nil, fmt.Errorf("gen failed"), "sub", "fail.go")
	assert.ErrorContains(t, err2, "gen failed")
}

// =============================================================================
// generate.go — JenniferGenerator.EdgeRel
// =============================================================================

func TestJenniferGenerator_EdgeRel(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	tests := []struct {
		rel  Rel
		want string
	}{
		{O2O, "O2O"},
		{O2M, "O2M"},
		{M2O, "M2O"},
		{M2M, "M2M"},
	}
	for _, tt := range tests {
		e := &Edge{Rel: Relation{Type: tt.rel}}
		assert.Equal(t, tt.want, g.EdgeRelType(e))
	}
}

func TestJenniferGenerator_EdgeRel_Default(t *testing.T) {
	g := &JenniferGenerator{generatedEnums: make(map[string]bool)}
	// Unknown relation type → defaults to "O2M".
	e := &Edge{Rel: Relation{Type: Rel(99)}}
	assert.Equal(t, "O2M", g.EdgeRelType(e))
}
