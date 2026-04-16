package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genHook generates the hook/hook.go file with hook composition utilities.
// This includes per-entity typed XxxFunc adapters, condition combinators (And, Or, Not),
// operation filters (On, Unless, If), and utility hooks (Reject, FixedError, Chain).
func genHook(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile("hook")

	veloxPkg := h.VeloxPkg()
	graph := h.Graph()

	// Determine the import path for the generated package's types (Value, Hook, etc.).
	// These types live in the root generated package (velox.go / types.go).
	genPkg := graph.Package

	// Per-entity typed XxxFunc adapters
	for _, t := range graph.MutableNodes() {
		funcName := t.Name + "Func"
		mutType := t.MutationName()
		entityPkg := h.EntityPkgPath(t)

		f.Commentf("The %s type is an adapter to allow the use of ordinary", funcName)
		f.Commentf("function as %s mutator.", t.Name)
		f.Type().Id(funcName).Func().Params(
			jen.Qual("context", "Context"),
			jen.Op("*").Qual(entityPkg, mutType),
		).Params(jen.Qual(genPkg, "Value"), jen.Error())

		// Mutate method
		f.Comment("Mutate calls f(ctx, m).")
		f.Func().Params(jen.Id("f").Id(funcName)).Id("Mutate").Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("m").Qual(veloxPkg, "Mutation"),
		).Params(jen.Qual(veloxPkg, "Value"), jen.Error()).Block(
			jen.If(
				jen.List(jen.Id("mv"), jen.Id("ok")).Op(":=").Id("m").Assert(jen.Op("*").Qual(entityPkg, mutType)),
				jen.Id("ok"),
			).Block(
				jen.Return(jen.Id("f").Call(jen.Id("ctx"), jen.Id("mv"))),
			),
			jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(
				jen.Lit("unexpected mutation type %T. expect *"+entityPkg+"."+mutType),
				jen.Id("m"),
			)),
		)
	}

	// Condition type
	f.Comment("Condition is a hook condition function.")
	f.Type().Id("Condition").Func().Params(
		jen.Qual("context", "Context"),
		jen.Qual(veloxPkg, "Mutation"),
	).Bool()

	// And combinator
	f.Comment("And groups conditions with the AND operator.")
	f.Func().Id("And").Params(
		jen.Id("first").Id("Condition"),
		jen.Id("second").Id("Condition"),
		jen.Id("rest").Op("...").Id("Condition"),
	).Id("Condition").Block(
		jen.Return(jen.Func().Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("m").Qual(veloxPkg, "Mutation"),
		).Bool().Block(
			jen.If(jen.Op("!").Id("first").Call(jen.Id("ctx"), jen.Id("m")).Op("||").Op("!").Id("second").Call(jen.Id("ctx"), jen.Id("m"))).Block(
				jen.Return(jen.False()),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("cond")).Op(":=").Range().Id("rest")).Block(
				jen.If(jen.Op("!").Id("cond").Call(jen.Id("ctx"), jen.Id("m"))).Block(
					jen.Return(jen.False()),
				),
			),
			jen.Return(jen.True()),
		)),
	)

	// Or combinator
	f.Comment("Or groups conditions with the OR operator.")
	f.Func().Id("Or").Params(
		jen.Id("first").Id("Condition"),
		jen.Id("second").Id("Condition"),
		jen.Id("rest").Op("...").Id("Condition"),
	).Id("Condition").Block(
		jen.Return(jen.Func().Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("m").Qual(veloxPkg, "Mutation"),
		).Bool().Block(
			jen.If(jen.Id("first").Call(jen.Id("ctx"), jen.Id("m")).Op("||").Id("second").Call(jen.Id("ctx"), jen.Id("m"))).Block(
				jen.Return(jen.True()),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("cond")).Op(":=").Range().Id("rest")).Block(
				jen.If(jen.Id("cond").Call(jen.Id("ctx"), jen.Id("m"))).Block(
					jen.Return(jen.True()),
				),
			),
			jen.Return(jen.False()),
		)),
	)

	// Not combinator
	f.Comment("Not negates a given condition.")
	f.Func().Id("Not").Params(
		jen.Id("cond").Id("Condition"),
	).Id("Condition").Block(
		jen.Return(jen.Func().Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("m").Qual(veloxPkg, "Mutation"),
		).Bool().Block(
			jen.Return(jen.Op("!").Id("cond").Call(jen.Id("ctx"), jen.Id("m"))),
		)),
	)

	// HasOp condition
	f.Comment("HasOp is a condition testing mutation operation.")
	f.Func().Id("HasOp").Params(
		jen.Id("op").Qual(veloxPkg, "Op"),
	).Id("Condition").Block(
		jen.Return(jen.Func().Params(
			jen.Id("_").Qual("context", "Context"),
			jen.Id("m").Qual(veloxPkg, "Mutation"),
		).Bool().Block(
			jen.Return(jen.Id("m").Dot("Op").Call().Dot("Is").Call(jen.Id("op"))),
		)),
	)

	// HasFields condition
	f.Comment("HasFields is a condition validating `.Field` on fields.")
	f.Func().Id("HasFields").Params(
		jen.Id("field").String(),
		jen.Id("fields").Op("...").String(),
	).Id("Condition").Block(
		jen.Return(jen.Func().Params(
			jen.Id("_").Qual("context", "Context"),
			jen.Id("m").Qual(veloxPkg, "Mutation"),
		).Bool().Block(
			jen.If(
				jen.List(jen.Id("_"), jen.Id("exists")).Op(":=").Id("m").Dot("Field").Call(jen.Id("field")),
				jen.Op("!").Id("exists"),
			).Block(jen.Return(jen.False())),
			jen.For(jen.List(jen.Id("_"), jen.Id("f")).Op(":=").Range().Id("fields")).Block(
				jen.If(
					jen.List(jen.Id("_"), jen.Id("exists")).Op(":=").Id("m").Dot("Field").Call(jen.Id("f")),
					jen.Op("!").Id("exists"),
				).Block(jen.Return(jen.False())),
			),
			jen.Return(jen.True()),
		)),
	)

	// HasAddedFields condition
	f.Comment("HasAddedFields is a condition validating `.AddedField` on fields.")
	f.Func().Id("HasAddedFields").Params(
		jen.Id("field").String(),
		jen.Id("fields").Op("...").String(),
	).Id("Condition").Block(
		jen.Return(jen.Func().Params(
			jen.Id("_").Qual("context", "Context"),
			jen.Id("m").Qual(veloxPkg, "Mutation"),
		).Bool().Block(
			jen.If(
				jen.List(jen.Id("_"), jen.Id("exists")).Op(":=").Id("m").Dot("AddedField").Call(jen.Id("field")),
				jen.Op("!").Id("exists"),
			).Block(jen.Return(jen.False())),
			jen.For(jen.List(jen.Id("_"), jen.Id("f")).Op(":=").Range().Id("fields")).Block(
				jen.If(
					jen.List(jen.Id("_"), jen.Id("exists")).Op(":=").Id("m").Dot("AddedField").Call(jen.Id("f")),
					jen.Op("!").Id("exists"),
				).Block(jen.Return(jen.False())),
			),
			jen.Return(jen.True()),
		)),
	)

	// HasClearedFields condition
	f.Comment("HasClearedFields is a condition validating `.FieldCleared` on fields.")
	f.Func().Id("HasClearedFields").Params(
		jen.Id("field").String(),
		jen.Id("fields").Op("...").String(),
	).Id("Condition").Block(
		jen.Return(jen.Func().Params(
			jen.Id("_").Qual("context", "Context"),
			jen.Id("m").Qual(veloxPkg, "Mutation"),
		).Bool().Block(
			jen.If(
				jen.Op("!").Id("m").Dot("FieldCleared").Call(jen.Id("field")),
			).Block(jen.Return(jen.False())),
			jen.For(jen.List(jen.Id("_"), jen.Id("f")).Op(":=").Range().Id("fields")).Block(
				jen.If(
					jen.Op("!").Id("m").Dot("FieldCleared").Call(jen.Id("f")),
				).Block(jen.Return(jen.False())),
			),
			jen.Return(jen.True()),
		)),
	)

	// HasEdge condition — checks if edges were added or removed
	f.Comment("HasEdge is a condition validating `.AddedEdges` OR `.RemovedEdges` on edges.")
	f.Func().Id("HasEdge").Params(
		jen.Id("edge").String(),
		jen.Id("edges").Op("...").String(),
	).Id("Condition").Block(
		jen.Return(jen.Func().Params(
			jen.Id("_").Qual("context", "Context"),
			jen.Id("m").Qual(veloxPkg, "Mutation"),
		).Bool().Block(
			jen.If(jen.Len(jen.Id("m").Dot("AddedIDs").Call(jen.Id("edge"))).Op("==").Lit(0).Op("&&").
				Len(jen.Id("m").Dot("RemovedIDs").Call(jen.Id("edge"))).Op("==").Lit(0)).Block(
				jen.Return(jen.False()),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("e")).Op(":=").Range().Id("edges")).Block(
				jen.If(jen.Len(jen.Id("m").Dot("AddedIDs").Call(jen.Id("e"))).Op("==").Lit(0).Op("&&").
					Len(jen.Id("m").Dot("RemovedIDs").Call(jen.Id("e"))).Op("==").Lit(0)).Block(
					jen.Return(jen.False()),
				),
			),
			jen.Return(jen.True()),
		)),
	)

	// HasClearedEdge condition
	f.Comment("HasClearedEdge is a condition validating `.EdgeCleared` on edges.")
	f.Func().Id("HasClearedEdge").Params(
		jen.Id("edge").String(),
		jen.Id("edges").Op("...").String(),
	).Id("Condition").Block(
		jen.Return(jen.Func().Params(
			jen.Id("_").Qual("context", "Context"),
			jen.Id("m").Qual(veloxPkg, "Mutation"),
		).Bool().Block(
			jen.If(
				jen.Op("!").Id("m").Dot("EdgeCleared").Call(jen.Id("edge")),
			).Block(jen.Return(jen.False())),
			jen.For(jen.List(jen.Id("_"), jen.Id("e")).Op(":=").Range().Id("edges")).Block(
				jen.If(
					jen.Op("!").Id("m").Dot("EdgeCleared").Call(jen.Id("e")),
				).Block(jen.Return(jen.False())),
			),
			jen.Return(jen.True()),
		)),
	)

	// If hook
	f.Comment("If executes the given hook under condition.")
	f.Comment("")
	f.Comment("\thook.If(ComputeAverage, And(HasFields(...), HasAddedFields(...)))")
	f.Func().Id("If").Params(
		jen.Id("hk").Qual(veloxPkg, "Hook"),
		jen.Id("cond").Id("Condition"),
	).Qual(veloxPkg, "Hook").Block(
		jen.Return(jen.Func().Params(
			jen.Id("next").Qual(veloxPkg, "Mutator"),
		).Qual(veloxPkg, "Mutator").Block(
			jen.Return(jen.Qual(veloxPkg, "MutateFunc").Call(
				jen.Func().Params(
					jen.Id("ctx").Qual("context", "Context"),
					jen.Id("m").Qual(veloxPkg, "Mutation"),
				).Params(jen.Qual(veloxPkg, "Value"), jen.Error()).Block(
					jen.If(jen.Id("cond").Call(jen.Id("ctx"), jen.Id("m"))).Block(
						jen.Return(jen.Id("hk").Call(jen.Id("next")).Dot("Mutate").Call(jen.Id("ctx"), jen.Id("m"))),
					),
					jen.Return(jen.Id("next").Dot("Mutate").Call(jen.Id("ctx"), jen.Id("m"))),
				),
			)),
		)),
	)

	// On hook
	f.Comment("On executes the given hook only for the given operation.")
	f.Comment("")
	f.Commentf("\thook.On(Log, velox.Delete|velox.Create)")
	f.Func().Id("On").Params(
		jen.Id("hk").Qual(veloxPkg, "Hook"),
		jen.Id("op").Qual(veloxPkg, "Op"),
	).Qual(veloxPkg, "Hook").Block(
		jen.Return(jen.Id("If").Call(jen.Id("hk"), jen.Id("HasOp").Call(jen.Id("op")))),
	)

	// Unless hook
	f.Comment("Unless skips the given hook only for the given operation.")
	f.Comment("")
	f.Commentf("\thook.Unless(Log, velox.Update|velox.UpdateOne)")
	f.Func().Id("Unless").Params(
		jen.Id("hk").Qual(veloxPkg, "Hook"),
		jen.Id("op").Qual(veloxPkg, "Op"),
	).Qual(veloxPkg, "Hook").Block(
		jen.Return(jen.Id("If").Call(jen.Id("hk"), jen.Id("Not").Call(jen.Id("HasOp").Call(jen.Id("op"))))),
	)

	// FixedError hook
	f.Comment("FixedError is a hook returning a fixed error.")
	f.Func().Id("FixedError").Params(
		jen.Id("err").Error(),
	).Qual(veloxPkg, "Hook").Block(
		jen.Return(jen.Func().Params(
			jen.Qual(veloxPkg, "Mutator"),
		).Qual(veloxPkg, "Mutator").Block(
			jen.Return(jen.Qual(veloxPkg, "MutateFunc").Call(
				jen.Func().Params(
					jen.Qual("context", "Context"),
					jen.Qual(veloxPkg, "Mutation"),
				).Params(jen.Qual(veloxPkg, "Value"), jen.Error()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
			)),
		)),
	)

	// Reject hook
	f.Comment("Reject returns a hook that rejects all operations that match op.")
	f.Comment("")
	f.Comment("\tfunc (T) Hooks() []velox.Hook {")
	f.Comment("\t\treturn []velox.Hook{")
	f.Comment("\t\t\tReject(velox.Delete|velox.Update),")
	f.Comment("\t\t}")
	f.Comment("\t}")
	f.Func().Id("Reject").Params(
		jen.Id("op").Qual(veloxPkg, "Op"),
	).Qual(veloxPkg, "Hook").Block(
		jen.Id("hk").Op(":=").Id("FixedError").Call(
			jen.Qual("fmt", "Errorf").Call(jen.Lit("%s operation is not allowed"), jen.Id("op")),
		),
		jen.Return(jen.Id("On").Call(jen.Id("hk"), jen.Id("op"))),
	)

	// Chain type
	f.Comment("Chain acts as a list of hooks and is effectively immutable.")
	f.Comment("Once created, it will always hold the same set of hooks in the same order.")
	f.Type().Id("Chain").Struct(
		jen.Id("hooks").Index().Qual(veloxPkg, "Hook"),
	)

	// NewChain
	f.Comment("NewChain creates a new chain of hooks.")
	f.Func().Id("NewChain").Params(
		jen.Id("hooks").Op("...").Qual(veloxPkg, "Hook"),
	).Id("Chain").Block(
		jen.Return(jen.Id("Chain").Values(jen.Dict{
			jen.Id("hooks"): jen.Append(jen.Index().Qual(veloxPkg, "Hook").Call(jen.Nil()), jen.Id("hooks").Op("...")),
		})),
	)

	// Chain.Hook
	f.Comment("Hook chains the list of hooks and returns the final hook.")
	f.Func().Params(jen.Id("c").Id("Chain")).Id("Hook").Params().Qual(veloxPkg, "Hook").Block(
		jen.Return(jen.Func().Params(
			jen.Id("mutator").Qual(veloxPkg, "Mutator"),
		).Qual(veloxPkg, "Mutator").Block(
			jen.For(jen.Id("i").Op(":=").Len(jen.Id("c").Dot("hooks")).Op("-").Lit(1), jen.Id("i").Op(">=").Lit(0), jen.Id("i").Op("--")).Block(
				jen.Id("mutator").Op("=").Id("c").Dot("hooks").Index(jen.Id("i")).Call(jen.Id("mutator")),
			),
			jen.Return(jen.Id("mutator")),
		)),
	)

	// Chain.Append
	f.Comment("Append extends a chain, adding the specified hook")
	f.Comment("as the last ones in the mutation flow.")
	f.Func().Params(jen.Id("c").Id("Chain")).Id("Append").Params(
		jen.Id("hooks").Op("...").Qual(veloxPkg, "Hook"),
	).Id("Chain").Block(
		jen.Id("newHooks").Op(":=").Make(jen.Index().Qual(veloxPkg, "Hook"), jen.Lit(0), jen.Len(jen.Id("c").Dot("hooks")).Op("+").Len(jen.Id("hooks"))),
		jen.Id("newHooks").Op("=").Append(jen.Id("newHooks"), jen.Id("c").Dot("hooks").Op("...")),
		jen.Id("newHooks").Op("=").Append(jen.Id("newHooks"), jen.Id("hooks").Op("...")),
		jen.Return(jen.Id("Chain").Values(jen.Dict{jen.Id("hooks"): jen.Id("newHooks")})),
	)

	// Chain.Extend
	f.Comment("Extend extends a chain, adding the specified chain")
	f.Comment("as the last ones in the mutation flow.")
	f.Func().Params(jen.Id("c").Id("Chain")).Id("Extend").Params(
		jen.Id("chain").Id("Chain"),
	).Id("Chain").Block(
		jen.Return(jen.Id("c").Dot("Append").Call(jen.Id("chain").Dot("hooks").Op("..."))),
	)

	return f
}
