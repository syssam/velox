package gen

import "github.com/dave/jennifer/jen"

// entityPkgHelper wraps a GeneratorHelper to override Pkg() for entity sub-packages.
// Generators use h.Pkg() to determine the package name for jen.NewFile().
// When generating into entity sub-packages, this wrapper returns the entity package
// name (e.g., "user") instead of the root package name (e.g., "velox").
//
// It also overrides GoType for entity struct references: in entity sub-packages,
// enum types are defined locally in entity_model.go.
type entityPkgHelper struct {
	GeneratorHelper
	pkgName string // entity package name (e.g., "user")
	rootPkg string // root package import path (non-empty = entity mode)
}

// newEntityPkgHelper creates a helper that generates into an entity sub-package.
func newEntityPkgHelper(base GeneratorHelper, entityPkgName, rootPkg string) GeneratorHelper {
	return &entityPkgHelper{
		GeneratorHelper: base,
		pkgName:         entityPkgName,
		rootPkg:         rootPkg,
	}
}

// Pkg returns the entity package name instead of the root package name.
func (h *entityPkgHelper) Pkg() string { return h.pkgName }

// RootPkg returns the root package import path when in entity mode (non-empty = entity mode).
func (h *entityPkgHelper) RootPkg() string { return h.rootPkg }

// EntityPkgPath returns empty string when the target entity IS the current package.
// This prevents self-imports: when generating user/create.go, references to
// user.FieldID become just FieldID (local, no import).
func (h *entityPkgHelper) EntityPkgPath(t *Type) string {
	if t.PackageDir() == h.pkgName {
		return ""
	}
	return h.GeneratorHelper.EntityPkgPath(t)
}

// GoType returns the type code for a field. For enum fields defined in the
// current entity package, returns a local reference (jen.Id) instead of a
// qualified reference (jen.Qual) to avoid self-imports.
func (h *entityPkgHelper) GoType(f *Field) jen.Code {
	if f.IsEnum() && !f.HasGoType() {
		enumName := f.SubpackageEnumTypeName()
		if f.Nillable {
			return jen.Op("*").Id(enumName)
		}
		return jen.Id(enumName)
	}
	return h.GeneratorHelper.GoType(f)
}

// BaseType returns the base type for a field, handling enums in the current package.
func (h *entityPkgHelper) BaseType(f *Field) jen.Code {
	if f.IsEnum() && !f.HasGoType() {
		return jen.Id(f.SubpackageEnumTypeName())
	}
	return h.GeneratorHelper.BaseType(f)
}

// EntityPackageDialect is implemented by dialects that support creating
// entity-scoped variants for per-entity package generation.
// All generation methods return (*jen.File, error) to propagate errors
// through the parallel errgroup pipeline.
type EntityPackageDialect interface {
	// WithHelper returns a new dialect instance scoped to an entity sub-package.
	// The returned instance MUST be safe for concurrent use — Generate() calls
	// multiple methods on it in parallel goroutines (e.g., GenMutation, GenCreate).
	WithHelper(h GeneratorHelper) MinimalDialect
	// GenEntityClient generates an entity client for entity sub-packages.
	GenEntityClient(h GeneratorHelper, t *Type) (*jen.File, error)
	// GenEntityRuntime generates per-entity runtime.go with init() for defaults/validators/hooks.
	GenEntityRuntime(h GeneratorHelper, t *Type) (*jen.File, error)
	// GenCreate generates the create builder file ({entity}/create.go).
	GenCreate(h GeneratorHelper, t *Type) (*jen.File, error)
	// GenUpdate generates the update builder file ({entity}/update.go).
	GenUpdate(h GeneratorHelper, t *Type) (*jen.File, error)
	// GenDelete generates the delete builder file ({entity}/delete.go).
	GenDelete(h GeneratorHelper, t *Type) (*jen.File, error)
	// GenEntityPkg generates a typed entity struct file for the shared entity/ package.
	GenEntityPkg(h GeneratorHelper, t *Type) (*jen.File, error)
	// GenQueryPkg generates a query builder for the shared query/ package.
	GenQueryPkg(h GeneratorHelper, t *Type, entityPkgPath string) (*jen.File, error)
	// GenQueryHelpers generates shared helper functions for the query/ package.
	GenQueryHelpers(h GeneratorHelper) (*jen.File, error)
	// GenEntityHooks generates entity/hooks.go with HookStore and InterceptorStore structs.
	GenEntityHooks(h GeneratorHelper) (*jen.File, error)
}
