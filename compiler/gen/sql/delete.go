package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genDelete generates delete builders as a standalone file.
// Returns (*jen.File, error) for interface consistency.
func genDelete(h gen.GeneratorHelper, t *gen.Type) (*jen.File, error) { //nolint:unparam // error kept for interface consistency
	f := h.NewFile(h.Pkg())
	genDeleteInto(h, f, t)
	return f, nil
}

// genDeleteInto appends delete builders to an existing *jen.File.
// Generated types:
//   - {Entity}Delete      — multi-entity delete
//   - {Entity}DeleteOne   — single-entity delete
func genDeleteInto(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	entityPkg := h.EntityPkgPath(t)
	mutName := t.MutationName()
	deleteName := t.DeleteName()
	deleteOneName := t.DeleteOneName()
	recv := t.DeleteReceiver()

	// Concrete return types for chaining methods — no interface indirection.
	var deleterIface jen.Code    // nil → chainReturnType falls back to *BuilderName
	var deleteOnerIface jen.Code // nil → chainReturnType falls back to *BuilderName

	// ----- DeleteName (multi-entity) -----
	f.Commentf("%s is the builder for deleting a %s entity.", deleteName, t.Name)
	f.Type().Id(deleteName).StructFunc(func(group *jen.Group) {
		group.Id("config").Qual(runtimePkg, "Config")
		if h.FeatureEnabled(gen.FeatureSchemaConfig.Name) {
			group.Id("schemaConfig").Qual(h.InternalPkg(), "SchemaConfig")
		}
		group.Id("hooks").Index().Qual(runtimePkg, "Hook")
		if t.NumPolicy() > 0 {
			group.Id("policy").Qual(h.VeloxPkg(), "Policy")
		}
		group.Id("mutation").Op("*").Qual(entityPkg, mutName)
	})

	// --- Constructor ---
	f.Commentf("New%s creates a new %s builder.", deleteName, deleteName)
	f.Func().Id("New" + deleteName).ParamsFunc(func(pg *jen.Group) {
		pg.Id("c").Qual(runtimePkg, "Config")
		pg.Id("mutation").Op("*").Qual(entityPkg, mutName)
		pg.Id("hooks").Index().Qual(runtimePkg, "Hook")
		if t.NumPolicy() > 0 {
			pg.Id("policy").Qual(h.VeloxPkg(), "Policy")
		}
	}).Op("*").Id(deleteName).BlockFunc(func(grp *jen.Group) {
		d := jen.Dict{
			jen.Id("config"):   jen.Id("c"),
			jen.Id("mutation"): jen.Id("mutation"),
			jen.Id("hooks"):    jen.Id("hooks"),
		}
		if t.NumPolicy() > 0 {
			d[jen.Id("policy")] = jen.Id("policy")
		}
		grp.Return(jen.Op("&").Id(deleteName).Values(d))
	})

	// Where
	delRetType := chainReturnType(deleteName, deleterIface)
	f.Commentf("Where appends a list predicates to the %s builder.", deleteName)
	f.Func().Params(jen.Id(recv).Op("*").Id(deleteName)).Id("Where").Params(
		jen.Id("ps").Op("...").Add(h.PredicateType(t)),
	).Add(delRetType).Block(
		jen.Id(recv).Dot("mutation").Dot("Where").Call(jen.Id("ps").Op("...")),
		jen.Return(jen.Id(recv)),
	)

	// sqlExec (named method — Ent pattern)
	f.Commentf("sqlExec executes the SQL delete for %s after hooks have run.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(deleteName)).Id("sqlExec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Int(), jen.Error()).BlockFunc(func(grp *jen.Group) {
		genConvertPredicates(grp, recv, h.SQLPkg())
		baseDict := jen.Dict{
			jen.Id("Driver"):     jen.Id(recv).Dot("config").Dot("Driver"),
			jen.Id("Table"):      jen.Qual(entityPkg, "Table"),
			jen.Id("IDColumn"):   jen.Qual(entityPkg, "FieldID"),
			jen.Id("IDType"):     jen.Id(idFieldTypeVar(t)),
			jen.Id("FieldTypes"): jen.Id(fieldTypesVar(t)),
			jen.Id("Predicates"): jen.Id("ps"),
		}
		if h.FeatureEnabled(gen.FeatureSchemaConfig.Name) {
			baseDict[jen.Id("Schema")] = jen.Id(recv).Dot("schemaConfig").Dot(t.Name)
		}
		grp.Id("base").Op(":=").Op("&").Qual(runtimePkg, "DeleterBase").Values(baseDict)
		grp.List(jen.Id("affected"), jen.Id("err")).Op(":=").Qual(runtimePkg, "DeleteNodes").Call(
			jen.Id("ctx"), jen.Id("base"),
		)
		grp.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Lit(0), jen.Id("err")),
		)
		grp.Return(jen.Id("affected"), jen.Nil())
	})

	// Exec
	f.Commentf("Exec executes the deletion query and returns how many vertices were deleted.")
	f.Func().Params(jen.Id(recv).Op("*").Id(deleteName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Int(), jen.Error()).BlockFunc(func(grp *jen.Group) {
		// Explicit privacy check — runs before hooks since privacy no longer rides on Hooks[0].
		if t.NumPolicy() > 0 {
			grp.If(jen.Id(recv).Dot("policy").Op("!=").Nil()).Block(
				jen.If(jen.Id("err").Op(":=").Id(recv).Dot("policy").Dot("EvalMutation").Call(
					jen.Id("ctx"), jen.Id(recv).Dot("mutation"),
				), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Lit(0), jen.Id("err")),
				),
			)
		}
		// Collect hooks: client-level (from Use) + schema-level (from codegen init).
		if t.NumHooks() > 0 {
			grp.Id("hooks").Op(":=").Id("append").Call(jen.Id(recv).Dot("hooks"), jen.Qual(entityPkg, "Hooks").Index(jen.Op(":")).Op("..."))
		} else {
			grp.Id("hooks").Op(":=").Id(recv).Dot("hooks")
		}
		mutationType := jen.Qual(entityPkg, mutName)
		grp.Return(jen.Qual(h.VeloxPkg(), "WithHooks").Types(
			jen.Int(),
			mutationType,
			jen.Op("*").Add(mutationType),
		).Call(
			jen.Id("ctx"), jen.Id(recv).Dot("sqlExec"), jen.Id(recv).Dot("mutation"), jen.Id("hooks"),
		))
	})

	// ExecX
	f.Comment("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(deleteName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Int().Block(
		jen.List(jen.Id("n"), jen.Id("err")).Op(":=").Id(recv).Dot("Exec").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("n")),
	)

	// Mutation method
	f.Commentf("Mutation returns the %s.", mutName)
	f.Func().Params(jen.Id(recv).Op("*").Id(deleteName)).Id("Mutation").Params().Op("*").Qual(entityPkg, mutName).Block(
		jen.Return(jen.Id(recv).Dot("mutation")),
	)

	// ----- DeleteOneName -----
	f.Commentf("%s is the builder for deleting a single %s entity.", deleteOneName, t.Name)
	f.Type().Id(deleteOneName).Struct(
		jen.Id(recv[1:]).Op("*").Id(deleteName),
	)

	// Constructor for DeleteOne
	f.Commentf("New%s creates a new %s builder.", deleteOneName, deleteOneName)
	f.Func().Id("New" + deleteOneName).Params(
		jen.Id("inner").Op("*").Id(deleteName),
	).Op("*").Id(deleteOneName).Block(
		jen.Return(jen.Op("&").Id(deleteOneName).Values(jen.Dict{
			jen.Id(recv[1:]): jen.Id("inner"),
		})),
	)

	// Exec for DeleteOne (delegates to Delete.Exec)
	f.Commentf("Exec executes the deletion query.")
	f.Func().Params(jen.Id(recv).Op("*").Id(deleteOneName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.List(jen.Id("n"), jen.Id("err")).Op(":=").Id(recv).Dot(recv[1:]).Dot("Exec").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.If(jen.Id("n").Op("==").Lit(0)).Block(
			jen.Return(jen.Qual(h.VeloxPkg(), "NewNotFoundError").Call(jen.Lit(t.Name))),
		),
		jen.Return(jen.Nil()),
	)

	// ExecX for DeleteOne
	f.Comment("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(deleteOneName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id(recv).Dot("Exec").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)

	// Where for DeleteOne
	delOneRetType := chainReturnType(deleteOneName, deleteOnerIface)
	f.Commentf("Where appends a list predicates to the %s builder.", deleteOneName)
	f.Func().Params(jen.Id(recv).Op("*").Id(deleteOneName)).Id("Where").Params(
		jen.Id("ps").Op("...").Add(h.PredicateType(t)),
	).Add(delOneRetType).Block(
		jen.Id(recv).Dot(recv[1:]).Dot("mutation").Dot("Where").Call(jen.Id("ps").Op("...")),
		jen.Return(jen.Id(recv)),
	)

	// Mutation method for DeleteOne
	f.Commentf("Mutation returns the %s.", mutName)
	f.Func().Params(jen.Id(recv).Op("*").Id(deleteOneName)).Id("Mutation").Params().Op("*").Qual(entityPkg, mutName).Block(
		jen.Return(jen.Id(recv).Dot(recv[1:]).Dot("mutation")),
	)
}
