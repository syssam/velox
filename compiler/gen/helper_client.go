package gen

import "github.com/dave/jennifer/jen"

// clientPkgHelper wraps a GeneratorHelper for emission into the client/{entity}/
// sub-package. Unlike entityPkgHelper (scoped to the {entity}/ leaf and avoids
// self-imports), clientPkgHelper sits in a DIFFERENT package from the leaf and
// must qualify every reference to leaf symbols.
//
// Pkg() returns "{entity}client" (e.g., "userclient"), so jen.NewFile emits
// `package userclient`. The Go directory for the emitted file is
// client/{entity}/ — the package name ≠ dir name is intentional (Go allows it).
//
// LeafPkgPath(t) delegates to the base helper (never returns ""), so the
// client code references the leaf via qualified imports like user.Label,
// user.FieldID, etc.
//
// GoType and BaseType qualify enum references with the leaf package path
// (e.g., user.Status), since the enum types live in the leaf after Phase A.
type clientPkgHelper struct {
	GeneratorHelper
	entityDir string // e.g., "user" — drives Pkg() and the leafPath()
	rootPkg   string // root package import path (non-empty = entity mode)
}

// newClientPkgHelper creates a helper that generates into the client/{entity}/
// sub-package declared as `package {entity}client`.
func newClientPkgHelper(base GeneratorHelper, entityDir, rootPkg string) GeneratorHelper {
	return &clientPkgHelper{
		GeneratorHelper: base,
		entityDir:       entityDir,
		rootPkg:         rootPkg,
	}
}

// Pkg returns the client package name, e.g., "userclient".
func (h *clientPkgHelper) Pkg() string { return h.entityDir + "client" }

// RootPkg returns the root package import path (non-empty = entity mode).
func (h *clientPkgHelper) RootPkg() string { return h.rootPkg }

// leafPath returns the full import path of the {entity}/ leaf package that
// this client helper is paired with (e.g., "github.com/foo/ent/user").
func (h *clientPkgHelper) leafPath() string {
	if h.rootPkg != "" {
		return h.rootPkg + "/" + h.entityDir
	}
	return h.entityDir
}

// GoType returns the type code for a field. For enum fields owned by the
// current entity (the one this helper is scoped to), it emits a qualified
// reference into the leaf package.
func (h *clientPkgHelper) GoType(f *Field) jen.Code {
	if f.IsEnum() && !f.HasGoType() {
		enumName := f.SubpackageEnumTypeName()
		if f.Nillable {
			return jen.Op("*").Qual(h.leafPath(), enumName)
		}
		return jen.Qual(h.leafPath(), enumName)
	}
	return h.GeneratorHelper.GoType(f)
}

// BaseType returns the base type for a field, qualifying enums into the leaf.
func (h *clientPkgHelper) BaseType(f *Field) jen.Code {
	if f.IsEnum() && !f.HasGoType() {
		return jen.Qual(h.leafPath(), f.SubpackageEnumTypeName())
	}
	return h.GeneratorHelper.BaseType(f)
}
