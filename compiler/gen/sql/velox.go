package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genVelox generates the velox.go file with common types and helpers.
// Error types are generated separately in genErrors() (errors.go).
// This follows Ent's base.tmpl pattern for familiarity.
func genVelox(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile(h.Pkg())
	graph := h.Graph()

	// Type aliases to avoid import conflicts in user's code
	f.Comment("velox aliases to avoid import conflicts in user's code.")
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

	// Generic helper functions.
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

	// tables stub - only generate if GraphQL extension is NOT being used.
	// When GraphQL is used, gql_node.go generates the full implementation.
	if !h.AnnotationExists("GraphQL") {
		f.Line()
		f.Comment("tables is a stub type for universal-id support.")
		f.Comment("Full implementation is generated when GraphQL is enabled.")
		f.Type().Id("tables").Struct()
	}

	// In per-entity mode, entity types live in model/ and entity sub-packages.
	// No type aliases are generated here because:
	// 1. Go disallows adding methods to type aliases from other packages,
	//    which breaks GraphQL extension methods (e.g., CollectFields on *UserQuery).
	// 2. Users import model/ and entity sub-packages directly, which is the
	//    intended usage pattern for per-entity mode.
	// 3. Fewer aliases = faster compile times for large schemas.

	return f
}

// genAggregateFunctions generates AggregateFunc type and aggregate functions.
func genAggregateFunctions(h gen.GeneratorHelper, f *jen.File) {
	// AggregateFunc type
	f.Comment("AggregateFunc applies an aggregation step on the group-by traversal/selector.")
	f.Type().Id("AggregateFunc").Op("=").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector")).String()
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
					jen.Id("Err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: %w"), jen.Id("err")),
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
					jen.Id("Err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: %w"), jen.Id("err")),
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
					jen.Id("Err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: %w"), jen.Id("err")),
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
					jen.Id("Err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: %w"), jen.Id("err")),
				})),
				jen.Return(jen.Lit("")),
			),
			jen.Return(jen.Qual(h.SQLPkg(), "Sum").Call(jen.Id("s").Dot("C").Call(jen.Id("field")))),
		)),
	)
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

func genErrorTypes(h gen.GeneratorHelper, f *jen.File) {
	// Type aliases to runtime error types ensures errors.As works uniformly.
	f.Comment("Error type aliases from the runtime package.")
	f.Comment("Using aliases ensures errors.As works uniformly across runtime and generated code.")
	f.Type().Defs(
		jen.Id("ValidationError").Op("=").Qual(runtimePkg, "ValidationError"),
		jen.Id("NotFoundError").Op("=").Qual(runtimePkg, "NotFoundError"),
		jen.Id("NotSingularError").Op("=").Qual(runtimePkg, "NotSingularError"),
		jen.Id("NotLoadedError").Op("=").Qual(runtimePkg, "NotLoadedError"),
		jen.Id("ConstraintError").Op("=").Qual(runtimePkg, "ConstraintError"),
	)

	// Error checker function aliases.
	f.Comment("Error checker functions — aliases to runtime package.")
	f.Var().Defs(
		jen.Id("IsValidationError").Op("=").Qual(runtimePkg, "IsValidationError"),
		jen.Id("IsNotFound").Op("=").Qual(runtimePkg, "IsNotFound"),
		jen.Id("IsNotSingular").Op("=").Qual(runtimePkg, "IsNotSingular"),
		jen.Id("IsNotLoaded").Op("=").Qual(runtimePkg, "IsNotLoaded"),
		jen.Id("IsConstraintError").Op("=").Qual(runtimePkg, "IsConstraintError"),
		jen.Id("NewConstraintError").Op("=").Qual(runtimePkg, "NewConstraintError"),
	)

	// MaskNotFound
	f.Comment("MaskNotFound masks not found error.")
	f.Func().Id("MaskNotFound").Params(jen.Id("err").Error()).Error().Block(
		jen.If(jen.Id("IsNotFound").Call(jen.Id("err"))).Block(
			jen.Return(jen.Nil()),
		),
		jen.Return(jen.Id("err")),
	)
}

// genErrors generates the errors.go file with error type aliases and checker functions.
// Follows Go stdlib convention (os/error.go, html/template/error.go).
func genErrors(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile(h.Pkg())
	genErrorTypes(h, f)
	return f
}

// genGenericHelpers generates generic helper functions.
func genGenericHelpers(_ gen.GeneratorHelper, f *jen.File) {
	genModernGenericUtilities(f)
}

// genModernGenericUtilities generates modern Go 1.21+ generic helper functions.
func genModernGenericUtilities(f *jen.File) {
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
}

// genOrderAndColumnCheck generates OrderFunc type and checkColumn function.
// checkColumn delegates to runtime.ValidColumn which uses the column registry
// populated by each entity's init() — no entity sub-package imports needed.
func genOrderAndColumnCheck(h gen.GeneratorHelper, f *jen.File) {
	// OrderFunc type (deprecated)
	f.Comment("OrderFunc applies an ordering on the sql selector.")
	f.Comment("Deprecated: Use Asc/Desc functions or the package builders instead.")
	f.Type().Id("OrderFunc").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector"))
	f.Line()

	// checkColumn function — delegates to runtime registry
	f.Comment("checkColumn checks if the column exists in the given table.")
	f.Func().Id("checkColumn").Params(
		jen.Id("t").Op(",").Id("c").String(),
	).Error().Block(
		jen.Return(jen.Qual(runtimePkg, "ValidColumn").Call(jen.Id("t"), jen.Id("c"))),
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
						jen.Id("Err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: %w"), jen.Id("err")),
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
						jen.Id("Err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: %w"), jen.Id("err")),
					})),
				),
				jen.Id("s").Dot("OrderBy").Call(jen.Qual(h.SQLPkg(), "Desc").Call(jen.Id("s").Dot("C").Call(jen.Id("f")))),
			),
		)),
	)
}
