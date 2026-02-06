package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genDelete generates the delete builder file ({entity}_delete.go).
func genDelete(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	f := h.NewFile(h.Pkg())

	// Generate Delete builder
	genDeleteBuilder(h, f, t)

	// Generate DeleteOne builder
	genDeleteOneBuilder(h, f, t)

	return f
}

// genDeleteBuilder generates the Delete builder struct and methods.
func genDeleteBuilder(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	deleteName := t.DeleteName()
	mutationName := t.MutationName()

	// Delete struct
	f.Commentf("%s is the builder for deleting a %s entity.", deleteName, t.Name)
	f.Type().Id(deleteName).Struct(
		jen.Id("config"), // embedded config
		jen.Id("mutation").Op("*").Id(mutationName),
		jen.Id("hooks").Index().Id("Hook"),
	)

	// Where adds predicates
	f.Commentf("Where appends a list predicates to the %s.", deleteName)
	f.Func().Params(jen.Id(t.DeleteReceiver()).Op("*").Id(deleteName)).Id("Where").Params(
		jen.Id("ps").Op("...").Add(h.PredicateType(t)),
	).Op("*").Id(deleteName).Block(
		jen.Id(t.DeleteReceiver()).Dot("mutation").Dot("Where").Call(jen.Id("ps").Op("...")),
		jen.Return(jen.Id(t.DeleteReceiver())),
	)

	// Exec executes the deletion
	f.Commentf("Exec executes the deletion query and returns how many vertices were deleted.")
	f.Func().Params(jen.Id(t.DeleteReceiver()).Op("*").Id(deleteName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Int(), jen.Error()).Block(
		jen.Return(jen.Id("withHooks").Index(jen.Int()).Call(
			jen.Id("ctx"),
			jen.Id(t.DeleteReceiver()).Dot("sqlExec"),
			jen.Id(t.DeleteReceiver()).Dot("mutation"),
			jen.Id(t.DeleteReceiver()).Dot("hooks"),
		)),
	)

	// ExecX is like Exec but panics
	f.Commentf("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.DeleteReceiver()).Op("*").Id(deleteName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Int().Block(
		jen.List(jen.Id("n"), jen.Id("err")).Op(":=").Id(t.DeleteReceiver()).Dot("Exec").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("n")),
	)

	// sqlExec executes the SQL delete
	genDeleteSQLExec(h, f, t, deleteName)
}

// genDeleteOneBuilder generates the DeleteOne builder struct and methods.
func genDeleteOneBuilder(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	deleteOneName := t.DeleteOneName()
	deleteName := t.DeleteName()

	// DeleteOne struct
	f.Commentf("%s is the builder for deleting a single %s entity.", deleteOneName, t.Name)
	f.Type().Id(deleteOneName).Struct(
		jen.Id("builder").Op("*").Id(deleteName),
	)

	// Where adds predicates
	f.Commentf("Where appends a list predicates to the %s.", deleteOneName)
	f.Func().Params(jen.Id(t.DeleteOneReceiver()).Op("*").Id(deleteOneName)).Id("Where").Params(
		jen.Id("ps").Op("...").Add(h.PredicateType(t)),
	).Op("*").Id(deleteOneName).Block(
		jen.Id(t.DeleteOneReceiver()).Dot("builder").Dot("Where").Call(jen.Id("ps").Op("...")),
		jen.Return(jen.Id(t.DeleteOneReceiver())),
	)

	// Exec executes the deletion
	f.Commentf("Exec executes the deletion query.")
	f.Func().Params(jen.Id(t.DeleteOneReceiver()).Op("*").Id(deleteOneName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.List(jen.Id("n"), jen.Id("err")).Op(":=").Id(t.DeleteOneReceiver()).Dot("builder").Dot("Exec").Call(jen.Id("ctx")),
		jen.Switch().BlockFunc(func(grp *jen.Group) {
			grp.Case(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Id("err")),
			)
			grp.Case(jen.Id("n").Op("==").Lit(0)).Block(
				jen.Return(jen.Op("&").Id("NotFoundError").Values(jen.Qual(h.EntityPkgPath(t), "Label"))),
			)
			grp.Default().Block(
				jen.Return(jen.Nil()),
			)
		}),
	)

	// ExecX is like Exec but panics
	f.Commentf("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.DeleteOneReceiver()).Op("*").Id(deleteOneName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id(t.DeleteOneReceiver()).Dot("Exec").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)
}

// genDeleteSQLExec generates the sqlExec method that executes the SQL delete.
func genDeleteSQLExec(h gen.GeneratorHelper, f *jen.File, t *gen.Type, deleteName string) {
	f.Func().Params(jen.Id(t.DeleteReceiver()).Op("*").Id(deleteName)).Id("sqlExec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Int(), jen.Error()).BlockFunc(func(grp *jen.Group) {
		// Create the DeleteSpec
		grp.Id("_spec").Op(":=").Qual(h.SQLGraphPkg(), "NewDeleteSpec").CallFunc(func(call *jen.Group) {
			call.Qual(h.EntityPkgPath(t), "Table")
			if t.HasOneFieldID() {
				call.Qual(h.SQLGraphPkg(), "NewFieldSpec").Call(
					jen.Qual(h.EntityPkgPath(t), t.ID.Constant()),
					jen.Qual(schemaPkg(), t.ID.Type.ConstName()),
				)
			} else {
				call.Nil()
			}
		})

		// Apply predicates from mutation
		grp.If(jen.Id("ps").Op(":=").Id(t.DeleteReceiver()).Dot("mutation").Dot("predicates"), jen.Len(jen.Id("ps")).Op(">").Lit(0)).Block(
			jen.Id("_spec").Dot("Predicate").Op("=").Func().Params(
				jen.Id("selector").Op("*").Qual(h.SQLPkg(), "Selector"),
			).Block(
				jen.For(jen.Id("i").Op(":=").Range().Id("ps")).Block(
					jen.Id("ps").Index(jen.Id("i")).Call(jen.Id("selector")),
				),
			),
		)

		// Execute the delete
		grp.List(jen.Id("affected"), jen.Id("err")).Op(":=").Qual(h.SQLGraphPkg(), "DeleteNodes").Call(
			jen.Id("ctx"),
			jen.Id(t.DeleteReceiver()).Dot("driver"),
			jen.Id("_spec"),
		)

		// Handle constraint error
		grp.If(jen.Id("err").Op("!=").Nil().Op("&&").Qual(h.SQLGraphPkg(), "IsConstraintError").Call(jen.Id("err"))).Block(
			jen.Id("err").Op("=").Op("&").Id("ConstraintError").Values(jen.Dict{
				jen.Id("msg"):  jen.Id("err").Dot("Error").Call(),
				jen.Id("wrap"): jen.Id("err"),
			}),
		)

		// Mark mutation as done
		grp.Id(t.DeleteReceiver()).Dot("mutation").Dot("done").Op("=").True()

		grp.Return(jen.Id("affected"), jen.Id("err"))
	})
}
