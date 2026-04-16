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

	// Version/Sum constants
	module := graph.ModuleInfo()
	if module.Version != "" || module.Sum != "" {
		f.Const().DefsFunc(func(g *jen.Group) {
			if module.Version != "" {
				g.Id("Version").Op("=").Lit(module.Version).Comment("// Version of velox codegen.")
			}
			if module.Sum != "" {
				g.Id("Sum").Op("=").Lit(module.Sum).Comment("// Sum of velox codegen.")
			}
		})
	}

	return f
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
// consolidates RegisterTypeInfo, RegisterColumns, RegisterMutator, and
// RegisterEntityClient into one call per entity.
func genEntityRuntimeRegistration(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type) {
	entityPkg := h.SharedEntityPkg()
	entityType := func() *jen.Statement { return jen.Qual(entityPkg, t.Name) }

	clientName := t.ClientName()
	mutName := t.MutationName()

	grp.Qual(runtimePkg, "RegisterEntity").Call(
		jen.Qual(runtimePkg, "EntityRegistration").Values(jen.Dict{
			jen.Id("Name"):  jen.Lit(t.Name),
			jen.Id("Table"): jen.Id("Table"),
			jen.Id("TypeInfo"): jen.Op("&").Qual(runtimePkg, "RegisteredTypeInfo").Values(jen.Dict{
				jen.Id("Table"):       jen.Id("Table"),
				jen.Id("Columns"):     jen.Id("Columns"),
				jen.Id("IDColumn"):    jen.Id("FieldID"),
				jen.Id("IDFieldType"): jen.Qual(schemaPkg(), t.ID.Type.ConstName()),
				jen.Id("ScanValues"): jen.Func().Params(
					jen.Id("columns").Index().String(),
				).Params(
					jen.Index().Any(), jen.Error(),
				).Block(
					jen.Return(jen.Parens(jen.Op("&").Add(entityType()).Values()).Dot("ScanValues").Call(jen.Id("columns"))),
				),
				jen.Id("New"): jen.Func().Params().Any().Block(
					jen.Return(jen.Op("&").Add(entityType()).Values()),
				),
				jen.Id("Assign"): jen.Func().Params(
					jen.Id("_e").Any(),
					jen.Id("columns").Index().String(),
					jen.Id("values").Index().Any(),
				).Error().Block(
					jen.Return(jen.Id("_e").Assert(jen.Op("*").Add(entityType())).Dot("AssignValues").Call(jen.Id("columns"), jen.Id("values"))),
				),
				jen.Id("GetID"): jen.Func().Params(
					jen.Id("_e").Any(),
				).Any().Block(
					jen.Return(jen.Id("_e").Assert(jen.Op("*").Add(entityType())).Dot("ID")),
				),
			}),
			jen.Id("ValidColumn"): jen.Id("ValidColumn"),
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
			jen.Id("Client"): jen.Func().Params(
				jen.Id("cfg").Qual(runtimePkg, "Config"),
			).Any().Block(
				jen.Return(jen.Id("New" + clientName).Call(jen.Id("cfg"))),
			),
		}),
	)

	// Register a NodeResolver so that root client.Noder/Noders can resolve this
	// entity by global ID. The resolver pulls Config from the context (injected
	// by the generated Noder) and constructs a fresh entity client to call Get.
	idType := h.IDType(t)
	grp.Qual(runtimePkg, "RegisterNodeResolver").Call(
		jen.Id("Table"),
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
				jen.If(jen.Op("!").Id("ok")).Block(
					jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(
						jen.Lit("velox: NodeResolver: unexpected id type %T"),
						jen.Id("id"),
					)),
				),
				jen.Return(jen.Id("New"+clientName).Call(jen.Id("cfg")).Dot("Get").Call(jen.Id("ctx"), jen.Id("typedID"))),
			),
		}),
	)
}
