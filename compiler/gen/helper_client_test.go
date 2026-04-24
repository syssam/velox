package gen

import (
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/schema/field"
)

// newTestClientPkgHelper is a test helper that constructs a clientPkgHelper
// backed by a minimal JenniferGenerator.
func newTestClientPkgHelper(entityDir, rootPkg string) GeneratorHelper {
	base := &JenniferGenerator{
		graph:          &Graph{Config: &Config{Package: rootPkg}},
		pkg:            "ent",
		generatedEnums: make(map[string]bool),
	}
	return newClientPkgHelper(base, entityDir, rootPkg)
}

// renderCode renders a jen.Code value to a Go source string via a file variable
// declaration, so the result is inspectable with string assertions.
func renderCode(t *testing.T, code jen.Code) string {
	t.Helper()
	f := jen.NewFile("test")
	f.Var().Id("x").Add(code)
	return f.GoString()
}

// =============================================================================
// TestNewClientPkgHelper_Pkg
// =============================================================================

func TestNewClientPkgHelper_Pkg(t *testing.T) {
	h := newTestClientPkgHelper("user", "example.com/app/ent")
	assert.Equal(t, "userclient", h.(*clientPkgHelper).Pkg())
}

// =============================================================================
// TestNewClientPkgHelper_RootPkg
// =============================================================================

func TestNewClientPkgHelper_RootPkg(t *testing.T) {
	h := newTestClientPkgHelper("user", "example.com/app/ent")
	assert.Equal(t, "example.com/app/ent", h.(*clientPkgHelper).RootPkg())
}

// =============================================================================
// TestNewClientPkgHelper_LeafPkgPath_Qualifies
// =============================================================================

// For entityPkgHelper, LeafPkgPath returns "" for the helper's own entity
// (to avoid self-imports). clientPkgHelper must NOT do this — it lives in a
// different package from the leaf and must always qualify.
func TestNewClientPkgHelper_LeafPkgPath_Qualifies(t *testing.T) {
	h := newTestClientPkgHelper("user", "example.com/app/ent")
	// Even for the "own" entity (User → packageDir "user"), the path must be
	// non-empty so the generated client code can import the leaf.
	path := h.(*clientPkgHelper).LeafPkgPath(&Type{Name: "User"})
	assert.NotEmpty(t, path, "clientPkgHelper must not suppress self-imports")
	assert.Contains(t, path, "user")
}

// =============================================================================
// TestClientPkgHelper_GoType_EnumQualifies
// =============================================================================

func TestClientPkgHelper_GoType_EnumQualifies(t *testing.T) {
	h := newTestClientPkgHelper("user", "example.com/app/ent")
	f := &Field{
		Name: "status",
		Type: &field.TypeInfo{Type: field.TypeEnum},
	}
	code := h.(*clientPkgHelper).GoType(f)
	require.NotNil(t, code)

	src := renderCode(t, code)
	// Must be qualified into the leaf package (e.g., user.Status), not bare Status.
	assert.Contains(t, src, "user.Status", "enum GoType must be qualified as leaf.EnumName")
	assert.NotContains(t, src, "var x Status", "enum GoType must not be an unqualified local reference")
}

// =============================================================================
// TestClientPkgHelper_GoType_NillableEnumQualifies
// =============================================================================

func TestClientPkgHelper_GoType_NillableEnumQualifies(t *testing.T) {
	h := newTestClientPkgHelper("user", "example.com/app/ent")
	f := &Field{
		Name:     "status",
		Nillable: true,
		Type:     &field.TypeInfo{Type: field.TypeEnum},
	}
	code := h.(*clientPkgHelper).GoType(f)
	require.NotNil(t, code)

	src := renderCode(t, code)
	// Nillable enum → *user.Status.
	assert.Contains(t, src, "*user.Status", "nillable enum GoType must be *leaf.EnumName")
}

// =============================================================================
// TestClientPkgHelper_BaseType_EnumQualifies
// =============================================================================

func TestClientPkgHelper_BaseType_EnumQualifies(t *testing.T) {
	h := newTestClientPkgHelper("user", "example.com/app/ent")
	f := &Field{
		Name: "status",
		Type: &field.TypeInfo{Type: field.TypeEnum},
	}
	code := h.(*clientPkgHelper).BaseType(f)
	require.NotNil(t, code)

	src := renderCode(t, code)
	assert.Contains(t, src, "user.Status", "enum BaseType must be qualified as leaf.EnumName")
}

// =============================================================================
// TestClientPkgHelper_GoType_NonEnumDelegates
// =============================================================================

func TestClientPkgHelper_GoType_NonEnumDelegates(t *testing.T) {
	h := newTestClientPkgHelper("user", "example.com/app/ent")
	f := &Field{
		Name: "name",
		Type: &field.TypeInfo{Type: field.TypeString},
	}
	code := h.(*clientPkgHelper).GoType(f)
	require.NotNil(t, code)

	src := renderCode(t, code)
	// String field → plain "string", no package qualifier.
	assert.Contains(t, src, "string", "non-enum GoType must delegate to base (plain string)")
	assert.NotContains(t, src, "user.", "non-enum GoType must not be leaf-qualified")
}
