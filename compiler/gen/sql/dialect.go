// Package sql provides SQL dialect code generation for the Jennifer generator.
//
// This package implements the gen.DialectGenerator interface for SQL databases
// including PostgreSQL, MySQL, and SQLite.
package sql

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

func init() {
	gen.RegisterDefaultGenerator(generateBase)
}

func generateBase(g *gen.Graph) error {
	if g.Config == nil || g.Target == "" {
		return gen.NewConfigError("Target", nil, "missing target directory in config", nil)
	}
	generator := gen.NewJenniferGenerator(g, g.Target)
	if g.Package != "" {
		generator.WithPackage(filepath.Base(g.Package))
	}
	dialect := NewDialect(generator)
	generator.WithDialect(dialect)
	ctx := g.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return generator.Generate(ctx)
}

// Generate is a convenience function to generate SQL-dialect code using the Jennifer generator.
// For standard usage, prefer Graph.Gen() which applies hooks automatically.
// This function calls generateBase directly without re-applying hooks,
// since Graph.Gen() already wraps the generator with the hook chain.
func Generate(g *gen.Graph) error {
	return generateBase(g)
}

// Dialect implements gen.DialectGenerator for SQL databases.
type Dialect struct {
	helper   gen.GeneratorHelper
	enumReg  *entityPkgEnumRegistry
	enumOnce sync.Once
}

// NewDialect creates a new SQL dialect generator.
func NewDialect(helper gen.GeneratorHelper) *Dialect {
	return &Dialect{helper: helper}
}

// getEnumRegistry returns the enum name collision registry. Thread-safe via sync.Once.
func (d *Dialect) getEnumRegistry() *entityPkgEnumRegistry {
	d.enumOnce.Do(func() {
		d.enumReg = buildEntityPkgEnumRegistry(d.helper.Graph().Nodes)
	})
	return d.enumReg
}

// Name returns the dialect name.
func (d *Dialect) Name() string { return "sql" }

// WithHelper returns a new Dialect using the given helper.
func (d *Dialect) WithHelper(h gen.GeneratorHelper) gen.MinimalDialect {
	reg := d.getEnumRegistry()
	return &Dialect{helper: h, enumReg: reg}
}

// GenEntityClient generates an entity client for entity sub-packages.
func (d *Dialect) GenEntityClient(h gen.GeneratorHelper, t *gen.Type) (*jen.File, error) {
	return genEntityClient(h, t), nil
}

// GenEntityRuntime generates per-entity runtime.go with init() for defaults/validators/hooks.
func (d *Dialect) GenEntityRuntime(h gen.GeneratorHelper, t *gen.Type) (*jen.File, error) {
	return genEntityRuntime(h, t), nil
}

// GenEntityPkg generates a typed entity struct file for the shared entity/ package.
func (d *Dialect) GenEntityPkg(h gen.GeneratorHelper, t *gen.Type) (*jen.File, error) {
	return genEntityPkgFileWithRegistry(h, t, d.helper.Graph().Nodes, d.getEnumRegistry()), nil
}

// GenCreate generates the create builder file ({entity}/create.go).
func (d *Dialect) GenCreate(h gen.GeneratorHelper, t *gen.Type) (*jen.File, error) {
	return genCreate(h, t)
}

// GenUpdate generates the update builder file ({entity}/update.go).
func (d *Dialect) GenUpdate(h gen.GeneratorHelper, t *gen.Type) (*jen.File, error) {
	return genUpdate(h, t)
}

// GenDelete generates the delete builder file ({entity}/delete.go).
func (d *Dialect) GenDelete(h gen.GeneratorHelper, t *gen.Type) (*jen.File, error) {
	return genDelete(h, t)
}

// GenQueryPkg generates a query builder file for the shared query/ package.
func (d *Dialect) GenQueryPkg(h gen.GeneratorHelper, t *gen.Type, entityPkgPath string) (*jen.File, error) {
	return genQueryPkg(h, t, d.helper.Graph().Nodes, entityPkgPath), nil
}

// GenQueryHelpers generates shared helper functions for the query/ package.
func (d *Dialect) GenQueryHelpers(h gen.GeneratorHelper) (*jen.File, error) {
	return genQueryHelpers(h), nil
}

// GenEntityHooks generates entity/hooks.go with HookStore and InterceptorStore structs.
func (d *Dialect) GenEntityHooks(h gen.GeneratorHelper) (*jen.File, error) {
	return genEntityHooks(h), nil
}

// GenMutation generates the mutation type file ({entity}_mutation.go).
func (d *Dialect) GenMutation(t *gen.Type) (*jen.File, error) {
	return genMutation(d.helper, t), nil
}

// GenPredicate generates the where predicates file ({entity}/where.go).
func (d *Dialect) GenPredicate(t *gen.Type) (*jen.File, error) {
	return genPredicate(d.helper, t), nil
}

// GenPackage generates the entity package constants file ({entity}/{entity}.go).
func (d *Dialect) GenPackage(t *gen.Type) (*jen.File, error) {
	return genPackage(d.helper, t, d.getEnumRegistry()), nil
}

// GenFilter generates the filter file ({entity}_filter.go) for privacy filtering.
func (d *Dialect) GenFilter(t *gen.Type) (*jen.File, error) {
	return genFilter(d.helper, t), nil
}

// GenClient generates the client file (client.go).
func (d *Dialect) GenClient() (*jen.File, error) { return genClient(d.helper), nil }

// GenVelox generates velox.go (base types).
func (d *Dialect) GenVelox() (*jen.File, error) { return genVelox(d.helper), nil }

// GenErrors generates the errors file (errors.go).
func (d *Dialect) GenErrors() (*jen.File, error) { return genErrors(d.helper), nil }

// GenTx generates transaction file (tx.go).
func (d *Dialect) GenTx() (*jen.File, error) { return genTx(d.helper), nil }

// GenPredicatePackage generates the predicate package file (predicate/predicate.go).
func (d *Dialect) GenPredicatePackage() (*jen.File, error) {
	return genPredicatePackage(d.helper), nil
}

// GenRuntime generates runtime file (runtime.go).
func (d *Dialect) GenRuntime() (*jen.File, error) {
	return genRuntimeCombined(d.helper, d.helper.Graph().Nodes), nil
}

// SupportsFeature reports if the feature is supported by SQL dialect.
func (d *Dialect) SupportsFeature(feature string) bool {
	switch feature {
	case "upsert", "lock", "modifier", "hook":
		return true
	default:
		return false
	}
}

// GenFeature generates feature-specific code for the given feature name.
func (d *Dialect) GenFeature(feature string) (*jen.File, error) {
	switch feature {
	case "hook":
		return genHook(d.helper), nil
	default:
		return nil, nil
	}
}

// GenMigrate generates the migrate package files.
func (d *Dialect) GenMigrate() (gen.MigrateFiles, error) { return genMigrate(d.helper), nil }

// GenSchemaConfig generates the internal/schemaconfig.go file.
func (d *Dialect) GenSchemaConfig() (*jen.File, error) {
	return genSchemaConfig(d.helper, d.helper.Graph()), nil
}

// GenIntercept generates the intercept/intercept.go file.
func (d *Dialect) GenIntercept() (*jen.File, error) { return genIntercept(d.helper), nil }

// GenPrivacy generates the privacy/privacy.go file.
func (d *Dialect) GenPrivacy() (*jen.File, error) { return genPrivacy(d.helper), nil }

// GenSnapshot generates the internal/schema.go file.
func (d *Dialect) GenSnapshot() (*jen.File, error) { return genSnapshot(d.helper), nil }

// GenVersionedMigration generates the migrate/migrate.go file.
func (d *Dialect) GenVersionedMigration() (*jen.File, error) {
	return genVersionedMigration(d.helper), nil
}

// GenGlobalID generates the internal/globalid.go file.
func (d *Dialect) GenGlobalID() (*jen.File, error) { return genGlobalID(d.helper), nil }

// GenEntQL generates the querylanguage.go file.
func (d *Dialect) GenEntQL() (*jen.File, error) { return genEntQL(d.helper), nil }

// GenTypes generates the types.go file with shared type aliases.
func (d *Dialect) GenTypes() (*jen.File, error) { return genTypes(d.helper), nil }

// Compile-time interface checks.
var (
	_ gen.DialectGenerator       = (*Dialect)(nil)
	_ gen.TypesGenerator         = (*Dialect)(nil)
	_ gen.EntityPackageDialect   = (*Dialect)(nil)
	_ gen.PrivacyFilterGenerator = (*Dialect)(nil)
)
