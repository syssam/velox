package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genVelox generates the velox.go file with common types and helpers.
// This follows Ent's base.tmpl pattern for familiarity.
func genVelox(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile(h.Pkg())
	graph := h.Graph()

	// Import runtime package for field defaults and validators initialization
	if graph.Config != nil && graph.Config.Package != "" {
		f.Anon(graph.Config.Package + "/runtime")
	}

	// Type aliases to avoid import conflicts in user's code
	f.Comment("ent aliases to avoid import conflicts in user's code.")
	f.Type().DefsFunc(func(defs *jen.Group) {
		defs.Id("Op").Op("=").Qual(h.VeloxPkg(), "Op")
		defs.Id("Hook").Op("=").Qual(h.VeloxPkg(), "Hook")
		defs.Id("Value").Op("=").Qual(h.VeloxPkg(), "Value")
		defs.Id("Query").Op("=").Qual(h.VeloxPkg(), "Query")
		defs.Id("QueryContext").Op("=").Qual(h.VeloxPkg(), "QueryContext")
		defs.Id("Querier").Op("=").Qual(h.VeloxPkg(), "Querier")
		defs.Id("QuerierFunc").Op("=").Qual(h.VeloxPkg(), "QuerierFunc")
		defs.Id("Interceptor").Op("=").Qual(h.VeloxPkg(), "Interceptor")
		defs.Id("InterceptFunc").Op("=").Qual(h.VeloxPkg(), "InterceptFunc")
		defs.Id("Traverser").Op("=").Qual(h.VeloxPkg(), "Traverser")
		defs.Id("TraverseFunc").Op("=").Qual(h.VeloxPkg(), "TraverseFunc")
		defs.Id("Policy").Op("=").Qual(h.VeloxPkg(), "Policy")
		defs.Id("Mutator").Op("=").Qual(h.VeloxPkg(), "Mutator")
		defs.Id("MutateFunc").Op("=").Qual(h.VeloxPkg(), "MutateFunc")
		defs.Id("Mutation").Op("=").Qual(h.VeloxPkg(), "Mutation")
	})

	// Client context key and functions
	genClientContext(h, f)

	// Tx context key and functions
	genTxContext(h, f)

	// OrderFunc type (deprecated) and column check
	genOrderAndColumnCheck(h, f)

	// Asc/Desc functions
	genAscDesc(h, f)

	// AggregateFunc type and aggregate functions
	genAggregateFunctions(h, f)

	// Error types
	genErrorTypes(h, f)

	// selector struct with typed methods
	genSelectorStruct(h, f)

	// Generic helper functions
	genGenericHelpers(h, f)

	// Node type constants
	if len(graph.Nodes) > 0 {
		f.Comment("Node type constants.")
		f.Const().DefsFunc(func(defs *jen.Group) {
			for _, t := range graph.Nodes {
				defs.Id(t.TypeName()).Op("=").Lit(t.Name)
			}
		})
	}

	// Operation constants
	f.Comment("Operation constants for mutations.")
	f.Const().Defs(
		jen.Id("OpCreate").Op("=").Qual(h.VeloxPkg(), "OpCreate"),
		jen.Id("OpUpdate").Op("=").Qual(h.VeloxPkg(), "OpUpdate"),
		jen.Id("OpUpdateOne").Op("=").Qual(h.VeloxPkg(), "OpUpdateOne"),
		jen.Id("OpDelete").Op("=").Qual(h.VeloxPkg(), "OpDelete"),
		jen.Id("OpDeleteOne").Op("=").Qual(h.VeloxPkg(), "OpDeleteOne"),
	)

	// Query operation constants
	f.Comment("Operation constants for queries.")
	f.Const().Defs(
		jen.Id("OpQueryFirst").Op("=").Qual(h.VeloxPkg(), "OpQueryFirst"),
		jen.Id("OpQueryFirstID").Op("=").Qual(h.VeloxPkg(), "OpQueryFirstID"),
		jen.Id("OpQueryOnly").Op("=").Qual(h.VeloxPkg(), "OpQueryOnly"),
		jen.Id("OpQueryOnlyID").Op("=").Qual(h.VeloxPkg(), "OpQueryOnlyID"),
		jen.Id("OpQueryAll").Op("=").Qual(h.VeloxPkg(), "OpQueryAll"),
		jen.Id("OpQueryIDs").Op("=").Qual(h.VeloxPkg(), "OpQueryIDs"),
		jen.Id("OpQueryCount").Op("=").Qual(h.VeloxPkg(), "OpQueryCount"),
		jen.Id("OpQueryExist").Op("=").Qual(h.VeloxPkg(), "OpQueryExist"),
		jen.Id("OpQueryGroupBy").Op("=").Qual(h.VeloxPkg(), "OpQueryGroupBy"),
		jen.Id("OpQuerySelect").Op("=").Qual(h.VeloxPkg(), "OpQuerySelect"),
	)

	// queryHook type (SQL-specific global)
	f.Comment("queryHook describes an internal hook for the different sqlAll methods.")
	f.Type().Id("queryHook").Func().Params(
		jen.Qual("context", "Context"),
		jen.Op("*").Qual(h.SQLGraphPkg(), "QuerySpec"),
	)

	// tables stub - only generate if GraphQL extension is NOT being used.
	// When GraphQL is used, gql_node.go generates the full implementation.
	if !h.AnnotationExists("GraphQL") {
		f.Line()
		f.Comment("tables is a stub type for universal-id support.")
		f.Comment("Full implementation is generated when GraphQL is enabled.")
		f.Type().Id("tables").Struct()
	}

	return f
}

// genClientContext generates clientCtxKey and FromContext/NewContext functions.
func genClientContext(_ gen.GeneratorHelper, f *jen.File) {
	f.Type().Id("clientCtxKey").Struct()
	f.Line()

	// FromContext
	f.Comment("FromContext returns a Client stored inside a context, or nil if there isn't one.")
	f.Func().Id("FromContext").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Op("*").Id("Client").Block(
		jen.List(jen.Id("c"), jen.Id("_")).Op(":=").Id("ctx").Dot("Value").Call(
			jen.Id("clientCtxKey").Values(),
		).Op(".").Parens(jen.Op("*").Id("Client")),
		jen.Return(jen.Id("c")),
	)

	// NewContext
	f.Comment("NewContext returns a new context with the given Client attached.")
	f.Func().Id("NewContext").Params(
		jen.Id("parent").Qual("context", "Context"),
		jen.Id("c").Op("*").Id("Client"),
	).Qual("context", "Context").Block(
		jen.Return(jen.Qual("context", "WithValue").Call(
			jen.Id("parent"),
			jen.Id("clientCtxKey").Values(),
			jen.Id("c"),
		)),
	)
}

// genTxContext generates txCtxKey and TxFromContext/NewTxContext functions.
func genTxContext(h gen.GeneratorHelper, f *jen.File) {
	f.Type().Id("txCtxKey").Struct()
	f.Line()

	// TxFromContext
	f.Comment("TxFromContext returns a Tx stored inside a context, or nil if there isn't one.")
	f.Func().Id("TxFromContext").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Op("*").Id("Tx").Block(
		jen.List(jen.Id("tx"), jen.Id("_")).Op(":=").Id("ctx").Dot("Value").Call(
			jen.Id("txCtxKey").Values(),
		).Op(".").Parens(jen.Op("*").Id("Tx")),
		jen.Return(jen.Id("tx")),
	)

	// NewTxContext
	f.Comment("NewTxContext returns a new context with the given Tx attached.")
	f.Func().Id("NewTxContext").Params(
		jen.Id("parent").Qual("context", "Context"),
		jen.Id("tx").Op("*").Id("Tx"),
	).Qual("context", "Context").Block(
		jen.Return(jen.Qual("context", "WithValue").Call(
			jen.Id("parent"),
			jen.Id("txCtxKey").Values(),
			jen.Id("tx"),
		)),
	)
}

// genOrderAndColumnCheck generates OrderFunc type, column check vars/functions.
func genOrderAndColumnCheck(h gen.GeneratorHelper, f *jen.File) {
	graph := h.Graph()

	// OrderFunc type (deprecated)
	f.Comment("OrderFunc applies an ordering on the sql selector.")
	f.Comment("Deprecated: Use Asc/Desc functions or the package builders instead.")
	f.Type().Id("OrderFunc").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector"))
	f.Line()

	// Column check variables
	f.Var().Defs(
		jen.Id("initCheck").Qual("sync", "Once"),
		jen.Id("columnCheck").Qual(h.SQLPkg(), "ColumnCheck"),
	)
	f.Line()

	// checkColumn function
	f.Comment("checkColumn checks if the column exists in the given table.")
	f.Func().Id("checkColumn").Params(
		jen.Id("t").Op(",").Id("c").String(),
	).Error().Block(
		jen.Id("initCheck").Dot("Do").Call(jen.Func().Params().BlockFunc(func(grp *jen.Group) {
			grp.Id("columnCheck").Op("=").Qual(h.SQLPkg(), "NewColumnCheck").Call(
				jen.Map(jen.String()).Func().Params(jen.String()).Bool().ValuesFunc(func(vals *jen.Group) {
					for _, t := range graph.Nodes {
						vals.Qual(h.EntityPkgPath(t), "Table").Op(":").Qual(h.EntityPkgPath(t), "ValidColumn")
					}
				}),
			)
		})),
		jen.Return(jen.Id("columnCheck").Call(jen.Id("t"), jen.Id("c"))),
	)
}

// genAscDesc generates Asc and Desc ordering functions.
func genAscDesc(h gen.GeneratorHelper, f *jen.File) {
	// Asc function
	f.Comment("Asc applies the given fields in ASC order.")
	f.Func().Id("Asc").Params(
		jen.Id("fields").Op("...").String(),
	).Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector")).Block(
		jen.Return(jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
			jen.For(jen.List(jen.Id("_"), jen.Id("f")).Op(":=").Range().Id("fields")).Block(
				jen.If(jen.Id("err").Op(":=").Id("checkColumn").Call(
					jen.Id("s").Dot("TableName").Call(),
					jen.Id("f"),
				), jen.Id("err").Op("!=").Nil()).Block(
					jen.Id("s").Dot("AddError").Call(jen.Op("&").Id("ValidationError").Values(jen.Dict{
						jen.Id("Name"): jen.Id("f"),
						jen.Id("err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: %w"), jen.Id("err")),
					})),
				),
				jen.Id("s").Dot("OrderBy").Call(jen.Qual(h.SQLPkg(), "Asc").Call(jen.Id("s").Dot("C").Call(jen.Id("f")))),
			),
		)),
	)

	// Desc function
	f.Comment("Desc applies the given fields in DESC order.")
	f.Func().Id("Desc").Params(
		jen.Id("fields").Op("...").String(),
	).Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector")).Block(
		jen.Return(jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
			jen.For(jen.List(jen.Id("_"), jen.Id("f")).Op(":=").Range().Id("fields")).Block(
				jen.If(jen.Id("err").Op(":=").Id("checkColumn").Call(
					jen.Id("s").Dot("TableName").Call(),
					jen.Id("f"),
				), jen.Id("err").Op("!=").Nil()).Block(
					jen.Id("s").Dot("AddError").Call(jen.Op("&").Id("ValidationError").Values(jen.Dict{
						jen.Id("Name"): jen.Id("f"),
						jen.Id("err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: %w"), jen.Id("err")),
					})),
				),
				jen.Id("s").Dot("OrderBy").Call(jen.Qual(h.SQLPkg(), "Desc").Call(jen.Id("s").Dot("C").Call(jen.Id("f")))),
			),
		)),
	)
}

// genAggregateFunctions generates AggregateFunc type and aggregate functions.
func genAggregateFunctions(h gen.GeneratorHelper, f *jen.File) {
	// AggregateFunc type
	f.Comment("AggregateFunc applies an aggregation step on the group-by traversal/selector.")
	f.Type().Id("AggregateFunc").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector")).String()
	f.Line()

	// As function
	f.Comment("As is a pseudo aggregation function for renaming another other functions with custom names. For example:")
	f.Comment("")
	f.Comment("	GroupBy(field1, field2).")
	f.Comment("	Aggregate(ent.As(ent.Sum(field1), \"sum_field1\"), (ent.As(ent.Sum(field2), \"sum_field2\")).")
	f.Comment("	Scan(ctx, &v)")
	f.Func().Id("As").Params(
		jen.Id("fn").Id("AggregateFunc"),
		jen.Id("end").String(),
	).Id("AggregateFunc").Block(
		jen.Return(jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).String().Block(
			jen.Return(jen.Qual(h.SQLPkg(), "As").Call(jen.Id("fn").Call(jen.Id("s")), jen.Id("end"))),
		)),
	)

	// Count function
	f.Comment("Count applies the \"count\" aggregation function on each group.")
	f.Func().Id("Count").Params().Id("AggregateFunc").Block(
		jen.Return(jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).String().Block(
			jen.Return(jen.Qual(h.SQLPkg(), "Count").Call(jen.Lit("*"))),
		)),
	)

	// Max function
	f.Comment("Max applies the \"max\" aggregation function on the given field of each group.")
	f.Func().Id("Max").Params(jen.Id("field").String()).Id("AggregateFunc").Block(
		jen.Return(jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).String().Block(
			jen.If(jen.Id("err").Op(":=").Id("checkColumn").Call(
				jen.Id("s").Dot("TableName").Call(),
				jen.Id("field"),
			), jen.Id("err").Op("!=").Nil()).Block(
				jen.Id("s").Dot("AddError").Call(jen.Op("&").Id("ValidationError").Values(jen.Dict{
					jen.Id("Name"): jen.Id("field"),
					jen.Id("err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: %w"), jen.Id("err")),
				})),
				jen.Return(jen.Lit("")),
			),
			jen.Return(jen.Qual(h.SQLPkg(), "Max").Call(jen.Id("s").Dot("C").Call(jen.Id("field")))),
		)),
	)

	// Mean function
	f.Comment("Mean applies the \"mean\" aggregation function on the given field of each group.")
	f.Func().Id("Mean").Params(jen.Id("field").String()).Id("AggregateFunc").Block(
		jen.Return(jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).String().Block(
			jen.If(jen.Id("err").Op(":=").Id("checkColumn").Call(
				jen.Id("s").Dot("TableName").Call(),
				jen.Id("field"),
			), jen.Id("err").Op("!=").Nil()).Block(
				jen.Id("s").Dot("AddError").Call(jen.Op("&").Id("ValidationError").Values(jen.Dict{
					jen.Id("Name"): jen.Id("field"),
					jen.Id("err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: %w"), jen.Id("err")),
				})),
				jen.Return(jen.Lit("")),
			),
			jen.Return(jen.Qual(h.SQLPkg(), "Avg").Call(jen.Id("s").Dot("C").Call(jen.Id("field")))),
		)),
	)

	// Min function
	f.Comment("Min applies the \"min\" aggregation function on the given field of each group.")
	f.Func().Id("Min").Params(jen.Id("field").String()).Id("AggregateFunc").Block(
		jen.Return(jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).String().Block(
			jen.If(jen.Id("err").Op(":=").Id("checkColumn").Call(
				jen.Id("s").Dot("TableName").Call(),
				jen.Id("field"),
			), jen.Id("err").Op("!=").Nil()).Block(
				jen.Id("s").Dot("AddError").Call(jen.Op("&").Id("ValidationError").Values(jen.Dict{
					jen.Id("Name"): jen.Id("field"),
					jen.Id("err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: %w"), jen.Id("err")),
				})),
				jen.Return(jen.Lit("")),
			),
			jen.Return(jen.Qual(h.SQLPkg(), "Min").Call(jen.Id("s").Dot("C").Call(jen.Id("field")))),
		)),
	)

	// Sum function
	f.Comment("Sum applies the \"sum\" aggregation function on the given field of each group.")
	f.Func().Id("Sum").Params(jen.Id("field").String()).Id("AggregateFunc").Block(
		jen.Return(jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).String().Block(
			jen.If(jen.Id("err").Op(":=").Id("checkColumn").Call(
				jen.Id("s").Dot("TableName").Call(),
				jen.Id("field"),
			), jen.Id("err").Op("!=").Nil()).Block(
				jen.Id("s").Dot("AddError").Call(jen.Op("&").Id("ValidationError").Values(jen.Dict{
					jen.Id("Name"): jen.Id("field"),
					jen.Id("err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: %w"), jen.Id("err")),
				})),
				jen.Return(jen.Lit("")),
			),
			jen.Return(jen.Qual(h.SQLPkg(), "Sum").Call(jen.Id("s").Dot("C").Call(jen.Id("field")))),
		)),
	)
}

func genErrorTypes(h gen.GeneratorHelper, f *jen.File) {
	// ValidationError
	f.Comment("ValidationError returns when validating a field or edge fails.")
	f.Type().Id("ValidationError").Struct(
		jen.Id("Name").String().Comment("Field or edge name."),
		jen.Id("err").Error(),
	)
	f.Comment("Error implements the error interface.")
	f.Func().Params(jen.Id("e").Op("*").Id("ValidationError")).Id("Error").Params().String().Block(
		jen.Return(jen.Id("e").Dot("err").Dot("Error").Call()),
	)
	f.Comment("Unwrap implements the errors.Wrapper interface.")
	f.Func().Params(jen.Id("e").Op("*").Id("ValidationError")).Id("Unwrap").Params().Error().Block(
		jen.Return(jen.Id("e").Dot("err")),
	)

	f.Comment("IsValidationError returns a boolean indicating whether the error is a validation error.")
	f.Func().Id("IsValidationError").Params(jen.Id("err").Error()).Bool().Block(
		jen.If(jen.Id("err").Op("==").Nil()).Block(jen.Return(jen.False())),
		jen.Var().Id("e").Op("*").Id("ValidationError"),
		jen.Return(jen.Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("e"))),
	)

	// NotFoundError
	f.Comment("NotFoundError returns when trying to fetch a specific entity and it was not found in the database.")
	f.Type().Id("NotFoundError").Struct(jen.Id("label").String())
	f.Comment("Error implements the error interface.")
	f.Func().Params(jen.Id("e").Op("*").Id("NotFoundError")).Id("Error").Params().String().Block(
		jen.Return(jen.Lit("velox: ").Op("+").Id("e").Dot("label").Op("+").Lit(" not found")),
	)

	f.Comment("IsNotFound returns a boolean indicating whether the error is a not found error.")
	f.Func().Id("IsNotFound").Params(jen.Id("err").Error()).Bool().Block(
		jen.If(jen.Id("err").Op("==").Nil()).Block(jen.Return(jen.False())),
		jen.Var().Id("e").Op("*").Id("NotFoundError"),
		jen.Return(jen.Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("e"))),
	)

	// MaskNotFound
	f.Comment("MaskNotFound masks not found error.")
	f.Func().Id("MaskNotFound").Params(jen.Id("err").Error()).Error().Block(
		jen.If(jen.Id("IsNotFound").Call(jen.Id("err"))).Block(
			jen.Return(jen.Nil()),
		),
		jen.Return(jen.Id("err")),
	)

	// NotSingularError
	f.Comment("NotSingularError returns when trying to fetch a singular entity and more then one was found in the database.")
	f.Type().Id("NotSingularError").Struct(jen.Id("label").String())
	f.Comment("Error implements the error interface.")
	f.Func().Params(jen.Id("e").Op("*").Id("NotSingularError")).Id("Error").Params().String().Block(
		jen.Return(jen.Lit("velox: ").Op("+").Id("e").Dot("label").Op("+").Lit(" not singular")),
	)

	f.Comment("IsNotSingular returns a boolean indicating whether the error is a not singular error.")
	f.Func().Id("IsNotSingular").Params(jen.Id("err").Error()).Bool().Block(
		jen.If(jen.Id("err").Op("==").Nil()).Block(jen.Return(jen.False())),
		jen.Var().Id("e").Op("*").Id("NotSingularError"),
		jen.Return(jen.Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("e"))),
	)

	// NotLoadedError
	f.Comment("NotLoadedError returns when trying to get a node that was not loaded by the query.")
	f.Type().Id("NotLoadedError").Struct(jen.Id("edge").String())
	f.Comment("Error implements the error interface.")
	f.Func().Params(jen.Id("e").Op("*").Id("NotLoadedError")).Id("Error").Params().String().Block(
		jen.Return(jen.Lit("velox: ").Op("+").Id("e").Dot("edge").Op("+").Lit(" edge was not loaded")),
	)

	f.Comment("IsNotLoaded returns a boolean indicating whether the error is a not loaded error.")
	f.Func().Id("IsNotLoaded").Params(jen.Id("err").Error()).Bool().Block(
		jen.If(jen.Id("err").Op("==").Nil()).Block(jen.Return(jen.False())),
		jen.Var().Id("e").Op("*").Id("NotLoadedError"),
		jen.Return(jen.Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("e"))),
	)

	// ConstraintError
	f.Comment("ConstraintError returns when trying to create/update one or more entities and")
	f.Comment("one or more of their constraints failed. For example, violation of edge or")
	f.Comment("field uniqueness.")
	f.Type().Id("ConstraintError").Struct(
		jen.Id("msg").String(),
		jen.Id("wrap").Error(),
	)
	f.Comment("Error implements the error interface.")
	f.Func().Params(jen.Id("e").Id("ConstraintError")).Id("Error").Params().String().Block(
		jen.Return(jen.Lit("velox: constraint failed: ").Op("+").Id("e").Dot("msg")),
	)
	f.Comment("Unwrap implements the errors.Wrapper interface.")
	f.Func().Params(jen.Id("e").Op("*").Id("ConstraintError")).Id("Unwrap").Params().Error().Block(
		jen.Return(jen.Id("e").Dot("wrap")),
	)

	f.Comment("IsConstraintError returns a boolean indicating whether the error is a constraint failure.")
	f.Func().Id("IsConstraintError").Params(jen.Id("err").Error()).Bool().Block(
		jen.If(jen.Id("err").Op("==").Nil()).Block(jen.Return(jen.False())),
		jen.Var().Id("e").Op("*").Id("ConstraintError"),
		jen.Return(jen.Qual("errors", "As").Call(jen.Id("err"), jen.Op("&").Id("e"))),
	)
}

// genSelectorStruct generates the selector struct with typed return methods.
func genSelectorStruct(h gen.GeneratorHelper, f *jen.File) {
	// selector struct
	f.Comment("selector embedded by the different Select/GroupBy builders.")
	f.Type().Id("selector").Struct(
		jen.Id("label").String(),
		jen.Id("flds").Op("*").Index().String(),
		jen.Id("fns").Index().Id("AggregateFunc"),
		jen.Id("scan").Func().Params(jen.Qual("context", "Context"), jen.Any()).Error(),
	)
	f.Line()

	// ScanX method
	f.Comment("ScanX is like Scan, but panics if an error occurs.")
	f.Func().Params(jen.Id("s").Op("*").Id("selector")).Id("ScanX").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id("s").Dot("scan").Call(jen.Id("ctx"), jen.Id("v")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)

	// Generate typed methods for primitives: string, int, float64, bool
	primitives := []struct {
		typ     string
		plural  string
		single  string
		zeroVal jen.Code
	}{
		{"string", "Strings", "String", jen.Lit("")},
		{"int", "Ints", "Int", jen.Lit(0)},
		{"float64", "Float64s", "Float64", jen.Lit(0.0)},
		{"bool", "Bools", "Bool", jen.False()},
	}

	for _, p := range primitives {
		genSelectorPrimitiveMethods(h, f, p.typ, p.plural, p.single)
	}
}

// genSelectorPrimitiveMethods generates typed selector methods for a primitive type.
func genSelectorPrimitiveMethods(h gen.GeneratorHelper, f *jen.File, typ, plural, single string) {
	// Plural method (e.g., Strings)
	f.Commentf("%s returns list of %ss from a selector. It is only allowed when selecting one field.", plural, typ)
	f.Func().Params(jen.Id("s").Op("*").Id("selector")).Id(plural).Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Id(typ), jen.Error()).Block(
		jen.If(jen.Len(jen.Op("*").Id("s").Dot("flds")).Op(">").Lit(1)).Block(
			jen.Return(jen.Nil(), jen.Qual("errors", "New").Call(jen.Lit("velox: "+plural+" is not achievable when selecting more than 1 field"))),
		),
		jen.Var().Id("v").Index().Id(typ),
		jen.If(jen.Id("err").Op(":=").Id("s").Dot("scan").Call(jen.Id("ctx"), jen.Op("&").Id("v")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		),
		jen.Return(jen.Id("v"), jen.Nil()),
	)

	// PluralX method (e.g., StringsX)
	f.Commentf("%sX is like %s, but panics if an error occurs.", plural, plural)
	f.Func().Params(jen.Id("s").Op("*").Id("selector")).Id(plural+"X").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Index().Id(typ).Block(
		jen.List(jen.Id("v"), jen.Id("err")).Op(":=").Id("s").Dot(plural).Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("v")),
	)

	// Single method (e.g., String)
	f.Commentf("%s returns a single %s from a selector. It is only allowed when selecting one field.", single, typ)
	f.Func().Params(jen.Id("s").Op("*").Id("selector")).Id(single).Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Id("_").Id(typ), jen.Id("err").Error()).Block(
		jen.Var().Id("v").Index().Id(typ),
		jen.If(jen.List(jen.Id("v"), jen.Id("err")).Op("=").Id("s").Dot(plural).Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(),
		),
		jen.Switch(jen.Len(jen.Id("v"))).Block(
			jen.Case(jen.Lit(1)).Block(
				jen.Return(jen.Id("v").Index(jen.Lit(0)), jen.Nil()),
			),
			jen.Case(jen.Lit(0)).Block(
				jen.Id("err").Op("=").Op("&").Id("NotFoundError").Values(jen.Id("s").Dot("label")),
			),
			jen.Default().Block(
				jen.Id("err").Op("=").Qual("fmt", "Errorf").Call(jen.Lit("velox: "+plural+" returned %d results when one was expected"), jen.Len(jen.Id("v"))),
			),
		),
		jen.Return(),
	)

	// SingleX method (e.g., StringX)
	f.Commentf("%sX is like %s, but panics if an error occurs.", single, single)
	f.Func().Params(jen.Id("s").Op("*").Id("selector")).Id(single+"X").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Id(typ).Block(
		jen.List(jen.Id("v"), jen.Id("err")).Op(":=").Id("s").Dot(single).Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("v")),
	)
}

// genGenericHelpers generates generic helper functions.
func genGenericHelpers(h gen.GeneratorHelper, f *jen.File) {
	// withHooks helper (using generics with PM constraint like Ent)
	f.Comment("withHooks invokes the builder operation with the given hooks, if any.")
	f.Func().Id("withHooks").Types(
		jen.Id("V").Id("Value"),
		jen.Id("M").Any(),
		jen.Id("PM").Interface(
			jen.Op("*").Id("M"),
			jen.Id("Mutation"),
		),
	).Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("exec").Func().Params(jen.Qual("context", "Context")).Params(jen.Id("V"), jen.Error()),
		jen.Id("mutation").Id("PM"),
		jen.Id("hooks").Index().Id("Hook"),
	).Params(jen.Id("value").Id("V"), jen.Id("err").Error()).Block(
		jen.If(jen.Len(jen.Id("hooks")).Op("==").Lit(0)).Block(
			jen.Return(jen.Id("exec").Call(jen.Id("ctx"))),
		),
		jen.Var().Id("mut").Id("Mutator").Op("=").Id("MutateFunc").Call(
			jen.Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("m").Id("Mutation"),
			).Params(jen.Id("Value"), jen.Error()).Block(
				jen.List(jen.Id("mutationT"), jen.Id("ok")).Op(":=").Any().Call(jen.Id("m")).Op(".").Parens(jen.Id("PM")),
				jen.If(jen.Op("!").Id("ok")).Block(
					jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected mutation type %T"), jen.Id("m"))),
				),
				jen.Comment("Set the mutation to the builder."),
				jen.Op("*").Id("mutation").Op("=").Op("*").Id("mutationT"),
				jen.Return(jen.Id("exec").Call(jen.Id("ctx"))),
			),
		),
		jen.For(jen.Id("i").Op(":=").Len(jen.Id("hooks")).Op("-").Lit(1), jen.Id("i").Op(">=").Lit(0), jen.Id("i").Op("--")).Block(
			jen.If(jen.Id("hooks").Index(jen.Id("i")).Op("==").Nil()).Block(
				jen.Return(jen.Id("value"), jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: uninitialized hook (forgotten import velox/runtime?)"))),
			),
			jen.Id("mut").Op("=").Id("hooks").Index(jen.Id("i")).Call(jen.Id("mut")),
		),
		jen.List(jen.Id("v"), jen.Id("err")).Op(":=").Id("mut").Dot("Mutate").Call(jen.Id("ctx"), jen.Id("mutation")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("value"), jen.Id("err")),
		),
		jen.List(jen.Id("nv"), jen.Id("ok")).Op(":=").Id("v").Op(".").Parens(jen.Id("V")),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Id("value"), jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected node type %T returned from %T"), jen.Id("v"), jen.Id("mutation"))),
		),
		jen.Return(jen.Id("nv"), jen.Nil()),
	)

	// setContextOp helper
	f.Comment("setContextOp returns a new context with the given QueryContext attached (including its op) in case it does not exist.")
	f.Func().Id("setContextOp").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("qc").Op("*").Id("QueryContext"),
		jen.Id("op").String(),
	).Qual("context", "Context").Block(
		jen.If(jen.Qual(h.VeloxPkg(), "QueryFromContext").Call(jen.Id("ctx")).Op("==").Nil()).Block(
			jen.Id("qc").Dot("Op").Op("=").Id("op"),
			jen.Id("ctx").Op("=").Qual(h.VeloxPkg(), "NewQueryContext").Call(jen.Id("ctx"), jen.Id("qc")),
		),
		jen.Return(jen.Id("ctx")),
	)

	// querierAll generic helper
	f.Func().Id("querierAll").Types(
		jen.Id("V").Id("Value"),
		jen.Id("Q").Interface(
			jen.Id("sqlAll").Params(jen.Qual("context", "Context"), jen.Op("...").Id("queryHook")).Params(jen.Id("V"), jen.Error()),
		),
	).Params().Id("Querier").Block(
		jen.Return(jen.Id("QuerierFunc").Call(
			jen.Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("q").Id("Query"),
			).Params(jen.Id("Value"), jen.Error()).Block(
				jen.List(jen.Id("query"), jen.Id("ok")).Op(":=").Id("q").Op(".").Parens(jen.Id("Q")),
				jen.If(jen.Op("!").Id("ok")).Block(
					jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected query type %T"), jen.Id("q"))),
				),
				jen.Return(jen.Id("query").Dot("sqlAll").Call(jen.Id("ctx"))),
			),
		)),
	)

	// querierCount generic helper
	f.Func().Id("querierCount").Types(
		jen.Id("Q").Interface(
			jen.Id("sqlCount").Params(jen.Qual("context", "Context")).Params(jen.Int(), jen.Error()),
		),
	).Params().Id("Querier").Block(
		jen.Return(jen.Id("QuerierFunc").Call(
			jen.Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("q").Id("Query"),
			).Params(jen.Id("Value"), jen.Error()).Block(
				jen.List(jen.Id("query"), jen.Id("ok")).Op(":=").Id("q").Op(".").Parens(jen.Id("Q")),
				jen.If(jen.Op("!").Id("ok")).Block(
					jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected query type %T"), jen.Id("q"))),
				),
				jen.Return(jen.Id("query").Dot("sqlCount").Call(jen.Id("ctx"))),
			),
		)),
	)

	// withInterceptors generic helper
	f.Func().Id("withInterceptors").Types(
		jen.Id("V").Id("Value"),
	).Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("q").Id("Query"),
		jen.Id("qr").Id("Querier"),
		jen.Id("inters").Index().Id("Interceptor"),
	).Params(jen.Id("v").Id("V"), jen.Id("err").Error()).Block(
		jen.For(jen.Id("i").Op(":=").Len(jen.Id("inters")).Op("-").Lit(1), jen.Id("i").Op(">=").Lit(0), jen.Id("i").Op("--")).Block(
			jen.Id("qr").Op("=").Id("inters").Index(jen.Id("i")).Dot("Intercept").Call(jen.Id("qr")),
		),
		jen.List(jen.Id("rv"), jen.Id("err")).Op(":=").Id("qr").Dot("Query").Call(jen.Id("ctx"), jen.Id("q")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("v"), jen.Id("err")),
		),
		jen.List(jen.Id("vt"), jen.Id("ok")).Op(":=").Id("rv").Op(".").Parens(jen.Id("V")),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Id("v"), jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected type %T returned from %T. expected type: %T"), jen.Id("vt"), jen.Id("q"), jen.Id("v"))),
		),
		jen.Return(jen.Id("vt"), jen.Nil()),
	)

	// scanWithInterceptors generic helper
	f.Func().Id("scanWithInterceptors").Types(
		jen.Id("Q1").Qual(h.VeloxPkg(), "Query"),
		jen.Id("Q2").Interface(
			jen.Id("sqlScan").Params(jen.Qual("context", "Context"), jen.Id("Q1"), jen.Any()).Error(),
		),
	).Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("rootQuery").Id("Q1"),
		jen.Id("selectOrGroup").Id("Q2"),
		jen.Id("inters").Index().Id("Interceptor"),
		jen.Id("v").Any(),
	).Error().Block(
		jen.Id("rv").Op(":=").Qual("reflect", "ValueOf").Call(jen.Id("v")),
		jen.Var().Id("qr").Id("Querier").Op("=").Id("QuerierFunc").Call(
			jen.Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("q").Id("Query"),
			).Params(jen.Id("Value"), jen.Error()).Block(
				jen.List(jen.Id("query"), jen.Id("ok")).Op(":=").Id("q").Op(".").Parens(jen.Id("Q1")),
				jen.If(jen.Op("!").Id("ok")).Block(
					jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected query type %T"), jen.Id("q"))),
				),
				jen.If(jen.Id("err").Op(":=").Id("selectOrGroup").Dot("sqlScan").Call(jen.Id("ctx"), jen.Id("query"), jen.Id("v")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
				jen.If(
					jen.Id("k").Op(":=").Id("rv").Dot("Kind").Call(),
					jen.Id("k").Op("==").Qual("reflect", "Pointer").Op("&&").Id("rv").Dot("Elem").Call().Dot("CanInterface").Call(),
				).Block(
					jen.Return(jen.Id("rv").Dot("Elem").Call().Dot("Interface").Call(), jen.Nil()),
				),
				jen.Return(jen.Id("v"), jen.Nil()),
			),
		),
		jen.For(jen.Id("i").Op(":=").Len(jen.Id("inters")).Op("-").Lit(1), jen.Id("i").Op(">=").Lit(0), jen.Id("i").Op("--")).Block(
			jen.Id("qr").Op("=").Id("inters").Index(jen.Id("i")).Dot("Intercept").Call(jen.Id("qr")),
		),
		jen.List(jen.Id("vv"), jen.Id("err")).Op(":=").Id("qr").Dot("Query").Call(jen.Id("ctx"), jen.Id("rootQuery")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		// Tagless switch with init: switch rv2 := reflect.ValueOf(vv); { case ...: }
		// The semicolon after the init statement makes it a tagless switch with boolean cases
		jen.Switch(jen.Id("rv2").Op(":=").Qual("reflect", "ValueOf").Call(jen.Id("vv")).Op(";")).Block(
			jen.Case(jen.Id("rv").Dot("IsNil").Call(), jen.Id("rv2").Dot("IsNil").Call(), jen.Id("rv").Dot("Kind").Call().Op("!=").Qual("reflect", "Pointer")).Block(),
			jen.Case(jen.Id("rv").Dot("Type").Call().Op("==").Id("rv2").Dot("Type").Call()).Block(
				jen.Id("rv").Dot("Elem").Call().Dot("Set").Call(jen.Id("rv2").Dot("Elem").Call()),
			),
			jen.Case(jen.Id("rv").Dot("Elem").Call().Dot("Type").Call().Op("==").Id("rv2").Dot("Type").Call()).Block(
				jen.Id("rv").Dot("Elem").Call().Dot("Set").Call(jen.Id("rv2")),
			),
		),
		jen.Return(jen.Nil()),
	)

	// Generate modern Go 1.21+ generic utilities
	genModernGenericUtilities(h, f)
}

// genModernGenericUtilities generates modern Go 1.21+ generic helper functions.
func genModernGenericUtilities(_ gen.GeneratorHelper, f *jen.File) {
	f.Line()
	f.Comment("// =============================================================================")
	f.Comment("// Generic Utilities (Go 1.21+)")
	f.Comment("// =============================================================================")
	f.Line()

	// Ptr - creates a pointer to a value
	f.Comment("Ptr returns a pointer to the given value.")
	f.Func().Id("Ptr").Types(jen.Id("T").Any()).Params(
		jen.Id("v").Id("T"),
	).Op("*").Id("T").Block(
		jen.Return(jen.Op("&").Id("v")),
	)

	// Deref - dereferences a pointer with a default fallback
	f.Comment("Deref returns the value of a pointer or a zero value if nil.")
	f.Func().Id("Deref").Types(jen.Id("T").Any()).Params(
		jen.Id("p").Op("*").Id("T"),
	).Id("T").Block(
		jen.If(jen.Id("p").Op("!=").Nil()).Block(
			jen.Return(jen.Op("*").Id("p")),
		),
		jen.Var().Id("zero").Id("T"),
		jen.Return(jen.Id("zero")),
	)

	// DerefOr - dereferences a pointer with a custom default
	f.Comment("DerefOr returns the value of a pointer or a default value if nil.")
	f.Func().Id("DerefOr").Types(jen.Id("T").Any()).Params(
		jen.Id("p").Op("*").Id("T"),
		jen.Id("defaultVal").Id("T"),
	).Id("T").Block(
		jen.If(jen.Id("p").Op("!=").Nil()).Block(
			jen.Return(jen.Op("*").Id("p")),
		),
		jen.Return(jen.Id("defaultVal")),
	)

	// Map - transforms a slice using a function
	f.Comment("Map applies a function to each element of a slice and returns a new slice.")
	f.Func().Id("Map").Types(
		jen.Id("T").Any(),
		jen.Id("U").Any(),
	).Params(
		jen.Id("items").Index().Id("T"),
		jen.Id("fn").Func().Params(jen.Id("T")).Id("U"),
	).Index().Id("U").Block(
		jen.If(jen.Id("items").Op("==").Nil()).Block(
			jen.Return(jen.Nil()),
		),
		jen.Id("result").Op(":=").Make(jen.Index().Id("U"), jen.Len(jen.Id("items"))),
		jen.For(jen.List(jen.Id("i"), jen.Id("item")).Op(":=").Range().Id("items")).Block(
			jen.Id("result").Index(jen.Id("i")).Op("=").Id("fn").Call(jen.Id("item")),
		),
		jen.Return(jen.Id("result")),
	)

	// Filter - filters a slice using a predicate
	f.Comment("Filter returns a new slice containing only elements that satisfy the predicate.")
	f.Func().Id("Filter").Types(jen.Id("T").Any()).Params(
		jen.Id("items").Index().Id("T"),
		jen.Id("pred").Func().Params(jen.Id("T")).Bool(),
	).Index().Id("T").Block(
		jen.If(jen.Id("items").Op("==").Nil()).Block(
			jen.Return(jen.Nil()),
		),
		jen.Id("result").Op(":=").Make(jen.Index().Id("T"), jen.Lit(0), jen.Len(jen.Id("items"))),
		jen.For(jen.List(jen.Id("_"), jen.Id("item")).Op(":=").Range().Id("items")).Block(
			jen.If(jen.Id("pred").Call(jen.Id("item"))).Block(
				jen.Id("result").Op("=").Append(jen.Id("result"), jen.Id("item")),
			),
		),
		jen.Return(jen.Id("result")),
	)

	// First - returns the first element matching a predicate
	f.Comment("First returns the first element matching the predicate, or zero value if none found.")
	f.Func().Id("First").Types(jen.Id("T").Any()).Params(
		jen.Id("items").Index().Id("T"),
		jen.Id("pred").Func().Params(jen.Id("T")).Bool(),
	).Params(jen.Id("T"), jen.Bool()).Block(
		jen.For(jen.List(jen.Id("_"), jen.Id("item")).Op(":=").Range().Id("items")).Block(
			jen.If(jen.Id("pred").Call(jen.Id("item"))).Block(
				jen.Return(jen.Id("item"), jen.True()),
			),
		),
		jen.Var().Id("zero").Id("T"),
		jen.Return(jen.Id("zero"), jen.False()),
	)

	// getOptionalField - generic helper for optional field access
	f.Comment("getOptionalField returns the value and existence of an optional field.")
	f.Func().Id("getOptionalField").Types(jen.Id("T").Any()).Params(
		jen.Id("ptr").Op("*").Id("T"),
	).Params(jen.Id("T"), jen.Bool()).Block(
		jen.If(jen.Id("ptr").Op("==").Nil()).Block(
			jen.Var().Id("zero").Id("T"),
			jen.Return(jen.Id("zero"), jen.False()),
		),
		jen.Return(jen.Op("*").Id("ptr"), jen.True()),
	)

	// must - panics if error is not nil (useful for testing/init)
	f.Comment("must panics if err is not nil, otherwise returns v. Use sparingly.")
	f.Func().Id("must").Types(jen.Id("T").Any()).Params(
		jen.Id("v").Id("T"),
		jen.Id("err").Error(),
	).Id("T").Block(
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("v")),
	)
}
