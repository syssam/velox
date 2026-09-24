package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genRuntimeCombined generates the root runtime.go file with Version/Sum constants.
// Schema descriptor init() is generated per-entity in {entity}/runtime.go by genEntityRuntime().
func genRuntimeCombined(h gen.GeneratorHelper, _ []*gen.Type) *jen.File {
	f := h.NewFile(h.Pkg())

	graph := h.Graph()

	// Module provenance constants (Version/Checksum).
	module := graph.ModuleInfo()
	genModuleProvenance(f, module.Version, module.Sum)

	return f
}

// genModuleProvenance emits the provenance constants for the velox module that
// generated the assets, into the root package's runtime.go.
//
// The go.sum checksum constant is named Checksum, NOT Sum: the root package
// already declares the aggregate-function helper `func Sum(field string)
// AggregateFunc` (genAggregateFunctions in velox.go). A `const Sum` here would
// collide with it ("Sum redeclared in this block"). Ent sidesteps this by
// emitting Version/Sum into a separate `runtime` sub-package; velox emits into
// the root package, so the checksum const is renamed instead.
//
// The constants are only emitted when present. ModuleInfo().Sum is non-empty
// only when velox is consumed as a real versioned module dependency (a go.sum
// entry exists); it is empty when velox is pulled via a `replace` directive,
// which is why every in-repo example/golden has no const block and never
// exercised the collision.
func genModuleProvenance(f *jen.File, version, sum string) {
	if version == "" && sum == "" {
		return
	}
	f.Const().DefsFunc(func(g *jen.Group) {
		if version != "" {
			g.Id("Version").Op("=").Lit(version).Comment("// Version of velox codegen.")
		}
		if sum != "" {
			g.Id("Checksum").Op("=").Lit(sum).Comment("// go.sum checksum of the velox module used for codegen.")
		}
	})
}

// genEntityRuntime generates the per-entity runtime.go file with init() for
// defaults, validators, hooks, interceptors, policies, type info registration,
// and mutator/client registration.
func genEntityRuntime(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	graph := h.Graph()
	schemaPkg := graph.Schema
	if schemaPkg == "" {
		schemaPkg = graph.Package + "/schema"
	}

	f := h.NewFile(h.Pkg())

	f.Comment("The init function reads schema descriptors with runtime code")
	f.Comment("(default values, validators, hooks and policies) and stitches it")
	f.Comment("to the package variables.")
	f.Func().Id("init").Params().BlockFunc(func(grp *jen.Group) {
		genRuntimeEntityInit(h, grp, t, schemaPkg)
		genEntityRuntimeRegistration(h, grp, t)
	})

	return f
}

// genEntityRuntimeRegistration generates a single RegisterEntity call that
// consolidates RegisterMutator and RegisterColumns into one call per entity.
func genEntityRuntimeRegistration(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type) {
	leafPkg := h.LeafPkgPath(t)

	clientName := t.ClientName()
	mutName := t.MutationName()

	grp.Qual(runtimePkg, "RegisterEntity").Call(
		jen.Qual(runtimePkg, "EntityRegistration").Values(jen.Dict{
			jen.Id("Name"):        jen.Lit(t.Name),
			jen.Id("Table"):       jen.Qual(leafPkg, "Table"),
			jen.Id("ValidColumn"): jen.Qual(leafPkg, "ValidColumn"),
			jen.Id("Mutator"): jen.Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("cfg").Qual(runtimePkg, "Config"),
				jen.Id("m").Any(),
			).Params(jen.Any(), jen.Error()).Block(
				jen.Return(
					jen.Id("New"+clientName).Call(jen.Id("cfg")).Dot("mutate").Call(
						jen.Id("ctx"),
						jen.Id("m").Assert(jen.Op("*").Id(mutName)),
					),
				),
			),
		}),
	)

	// An edge schema with defaults registers them, so an M2M edge declared
	// Through() this entity fills them on the join row (Ent runs the join
	// entity's defaults() there; the adding entity cannot import this one).
	if t.IsEdgeSchema() && t.HasDefault() {
		createArgs := []jen.Code{
			jen.Qual(runtimePkg, "Config").Values(),
			jen.Id("New"+mutName).Call(jen.Qual(runtimePkg, "Config").Values(), jen.Qual(runtimePkg, "OpCreate")),
			jen.Nil(),
		}
		if t.NumPolicy() > 0 {
			createArgs = append(createArgs, jen.Nil())
		}
		grp.Qual(runtimePkg, "RegisterEdgeSchemaDefaults").Call(
			jen.Qual(leafPkg, "Table"),
			jen.Func().Params().Index().Op("*").Qual(h.SQLGraphPkg(), "FieldSpec").BlockFunc(func(fn *jen.Group) {
				fn.Id("c").Op(":=").Id("New" + t.CreateName()).Call(createArgs...)
				// A DefaultFunc left nil fails here just as it would on the
				// join entity's own create; the fields set so far still apply.
				fn.Id("_").Op("=").Id("c").Dot("defaults").Call()
				fn.List(jen.Id("_"), jen.Id("spec")).Op(":=").Id("c").Dot("createSpec").Call()
				if t.HasOneFieldID() && t.ID.Default {
					fn.If(jen.Id("spec").Dot("ID").Dot("Value").Op("!=").Nil()).Block(
						jen.Return(jen.Append(jen.Id("spec").Dot("Fields"), jen.Id("spec").Dot("ID"))),
					)
				}
				fn.Return(jen.Id("spec").Dot("Fields"))
			}),
		)
	}

	// Register a NodeResolver so that root client.Noder/Noders can resolve this
	// entity by global ID. The resolver pulls Config from the context (injected
	// by the generated Noder) and constructs a fresh entity client to call Get.
	idType := h.IDType(t)
	grp.Qual(runtimePkg, "RegisterNodeResolver").Call(
		jen.Qual(leafPkg, "Table"),
		jen.Qual(runtimePkg, "NodeResolver").Values(jen.Dict{
			jen.Id("Type"): jen.Lit(t.Name),
			jen.Id("Resolve"): jen.Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("id").Any(),
			).Params(jen.Any(), jen.Error()).Block(
				jen.Id("cfg").Op(":=").Qual(runtimePkg, "ConfigFromContext").Call(jen.Id("ctx")),
				jen.If(jen.Id("cfg").Dot("Driver").Op("==").Nil()).Block(
					jen.Return(jen.Nil(), jen.Qual("errors", "New").Call(
						jen.Lit("velox: NodeResolver requires Config in context — call client.Noder so cfg is propagated"),
					)),
				),
				jen.List(jen.Id("typedID"), jen.Id("ok")).Op(":=").Id("id").Assert(idType),
				// Wrap the sentinel so Noder/Noders can skip this resolver and
				// try the next one. Schemas that mix ID types make a mismatch
				// the normal case for every resolver but one.
				jen.If(jen.Op("!").Id("ok")).Block(
					jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(
						jen.Lit("%w: unexpected id type %T"),
						jen.Qual(runtimePkg, "ErrNodeIDTypeMismatch"),
						jen.Id("id"),
					)),
				),
				jen.Return(jen.Id("New"+clientName).Call(jen.Id("cfg")).Dot("Get").Call(jen.Id("ctx"), jen.Id("typedID"))),
			),
		}),
	)
}
