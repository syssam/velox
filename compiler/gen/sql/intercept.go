package sql

import (
	"strings"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genIntercept generates the intercept package (intercept/intercept.go).
// This is part of the intercept feature.
func genIntercept(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile("intercept")
	graph := h.Graph()
	pkg := h.Pkg()

	// veloxPkg is the framework package for base types (Querier, Query interface, Value, etc.)
	veloxPkg := h.VeloxPkg()
	f.ImportName("context", "context")
	f.ImportName("fmt", "fmt")
	f.ImportName(h.SQLPkg(), "sql")
	f.ImportName(h.PredicatePkg(), "predicate")
	for _, n := range graph.Nodes {
		f.ImportName(h.EntityPkgPath(n), n.PackageDir())
	}
	// Use "ormquery" alias to avoid conflict with the local generic type "query[T,P,R]".
	f.ImportAlias(h.QueryPkg(), "ormquery")

	// Query interface
	f.Comment("The Query interface represents an operation that queries a graph.")
	f.Comment("By using this interface, users can write generic code that manipulates")
	f.Comment("query builders of different types.")
	f.Type().Id("Query").Interface(
		jen.Comment("Type returns the string representation of the query type."),
		jen.Id("Type").Params().String(),
		jen.Comment("Limit the number of records to be returned by this query."),
		jen.Id("Limit").Params(jen.Int()),
		jen.Comment("Offset to start from."),
		jen.Id("Offset").Params(jen.Int()),
		jen.Comment("Unique configures the query builder to filter duplicate records."),
		jen.Id("Unique").Params(jen.Bool()),
		jen.Comment("Order specifies how the records should be ordered."),
		jen.Id("Order").Params(jen.Op("...").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector"))),
		jen.Comment("WhereP appends storage-level predicates to the query builder."),
		jen.Id("WhereP").Params(jen.Op("...").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector"))),
	)

	// Func type
	f.Comment("The Func type is an adapter that allows ordinary functions to be used as interceptors.")
	f.Comment("Unlike traversal functions, interceptors are skipped during graph traversals.")
	f.Type().Id("Func").Func().Params(
		jen.Qual("context", "Context"),
		jen.Id("Query"),
	).Error()

	// Func.Intercept method - uses velox framework types
	f.Comment("Intercept calls f(ctx, q) and then applied the next Querier.")
	f.Func().Params(jen.Id("f").Id("Func")).Id("Intercept").Params(
		jen.Id("next").Qual(veloxPkg, "Querier"),
	).Qual(veloxPkg, "Querier").Block(
		jen.Return(jen.Qual(veloxPkg, "QuerierFunc").Call(
			jen.Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("q").Qual(veloxPkg, "Query"),
			).Params(jen.Qual(veloxPkg, "Value"), jen.Error()).Block(
				jen.List(jen.Id("query"), jen.Id("err")).Op(":=").Id("NewQuery").Call(jen.Id("q")),
				jen.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
				jen.If(jen.Id("err").Op(":=").Id("f").Call(jen.Id("ctx"), jen.Id("query")), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
				jen.Return(jen.Id("next").Dot("Query").Call(jen.Id("ctx"), jen.Id("q"))),
			),
		)),
	)

	// TraverseFunc type
	f.Comment("The TraverseFunc type is an adapter to allow the use of ordinary function as Traverser.")
	f.Comment("If f is a function with the appropriate signature, TraverseFunc(f) is a Traverser that calls f.")
	f.Type().Id("TraverseFunc").Func().Params(
		jen.Qual("context", "Context"),
		jen.Id("Query"),
	).Error()

	// TraverseFunc.Intercept method - uses velox framework types
	f.Comment("Intercept is a dummy implementation of Intercept that returns the next Querier in the pipeline.")
	f.Func().Params(jen.Id("f").Id("TraverseFunc")).Id("Intercept").Params(
		jen.Id("next").Qual(veloxPkg, "Querier"),
	).Qual(veloxPkg, "Querier").Block(
		jen.Return(jen.Id("next")),
	)

	// TraverseFunc.Traverse method - uses velox framework Query type
	f.Comment("Traverse calls f(ctx, q).")
	f.Func().Params(jen.Id("f").Id("TraverseFunc")).Id("Traverse").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("q").Qual(veloxPkg, "Query"),
	).Error().Block(
		jen.List(jen.Id("query"), jen.Id("err")).Op(":=").Id("NewQuery").Call(jen.Id("q")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.Return(jen.Id("f").Call(jen.Id("ctx"), jen.Id("query"))),
	)

	// Per-entity Func and Traverse types
	for _, n := range graph.Nodes {
		genInterceptEntityTypes(h, f, n, veloxPkg)
	}

	// NewQuery wraps a concrete *XxxQuery into the intercept.Query interface.
	// Uses a type-switch over concrete query types.
	f.Comment("NewQuery returns the generic Query interface for the given typed query.")
	f.Func().Id("NewQuery").Params(
		jen.Id("q").Qual(veloxPkg, "Query"),
	).Params(jen.Id("Query"), jen.Error()).Block(
		jen.Switch(jen.Id("q").Op(":=").Id("q").Op(".").Parens(jen.Type())).BlockFunc(func(grp *jen.Group) {
			queryPkgBase := h.QueryPkg()
			for _, n := range graph.Nodes {
				wrapName := strings.ToLower(n.Name[:1]) + n.Name[1:] + "Query"
				grp.Case(jen.Op("*").Qual(queryPkgBase, n.QueryName())).Block(
					jen.Return(
						jen.Op("&").Id(wrapName).Values(jen.Dict{
							jen.Id("typ"): jen.Lit(n.Name),
							jen.Id("tq"):  jen.Id("q"),
						}),
						jen.Nil(),
					),
				)
			}
			grp.Default().Block(
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("unknown query type %T"), jen.Id("q"))),
			)
		}),
	)

	// Builder methods (Limit, Offset, etc.) return entity.XxxQuerier (an interface),
	// not *XxxQuery. The generic query[T,P,R] struct cannot be used because the
	// constraint Limit(int) T won't match. Instead, generate per-entity concrete
	// query wrapper types that hold *XxxQuery directly.
	genInterceptEntityQueryTypes(h, f, graph)

	_ = pkg // suppress unused warning

	return f
}

// genInterceptEntityTypes generates entity-specific Func and Traverse types.
func genInterceptEntityTypes(h gen.GeneratorHelper, f *jen.File, n *gen.Type, veloxPkg string) {
	funcName := n.Name + "Func"
	// Entity-specific query type: query types live in query/ pkg.
	queryType := jen.Op("*").Qual(h.QueryPkg(), n.QueryName())

	// EntityFunc type
	f.Commentf("The %s type is an adapter to allow the use of ordinary function as a Querier.", funcName)
	f.Type().Id(funcName).Func().Params(
		jen.Qual("context", "Context"),
		queryType,
	).Params(jen.Qual(veloxPkg, "Value"), jen.Error())

	// EntityFunc.Query method - uses velox framework types for interface but entity types for casting
	f.Comment("Query calls f(ctx, q).")
	f.Func().Params(jen.Id("f").Id(funcName)).Id("Query").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("q").Qual(veloxPkg, "Query"),
	).Params(jen.Qual(veloxPkg, "Value"), jen.Error()).Block(
		jen.If(
			jen.List(jen.Id("q"), jen.Id("ok")).Op(":=").Id("q").Op(".").Parens(queryType),
			jen.Id("ok"),
		).Block(
			jen.Return(jen.Id("f").Call(jen.Id("ctx"), jen.Id("q"))),
		),
		jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected query type %T. expect "+n.QueryName()), jen.Id("q"))),
	)

	traverseName := "Traverse" + n.Name

	// TraverseEntity type
	f.Commentf("The %s type is an adapter to allow the use of ordinary function as Traverser.", traverseName)
	f.Type().Id(traverseName).Func().Params(
		jen.Qual("context", "Context"),
		queryType,
	).Error()

	// TraverseEntity.Intercept method - uses velox framework types
	f.Comment("Intercept is a dummy implementation of Intercept that returns the next Querier in the pipeline.")
	f.Func().Params(jen.Id("f").Id(traverseName)).Id("Intercept").Params(
		jen.Id("next").Qual(veloxPkg, "Querier"),
	).Qual(veloxPkg, "Querier").Block(
		jen.Return(jen.Id("next")),
	)

	// TraverseEntity.Traverse method - uses velox framework Query type but entity type for casting
	f.Comment("Traverse calls f(ctx, q).")
	f.Func().Params(jen.Id("f").Id(traverseName)).Id("Traverse").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("q").Qual(veloxPkg, "Query"),
	).Error().Block(
		jen.If(
			jen.List(jen.Id("q"), jen.Id("ok")).Op(":=").Id("q").Op(".").Parens(queryType),
			jen.Id("ok"),
		).Block(
			jen.Return(jen.Id("f").Call(jen.Id("ctx"), jen.Id("q"))),
		),
		jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected query type %T. expect "+n.QueryName()), jen.Id("q"))),
	)
}

// genInterceptEntityQueryTypes generates per-entity concrete query wrapper types for
// the intercept package in entity mode.
//
// In entity mode, builder methods like Limit(int) return entity.XxxQuerier (interface),
// not *XxxQuery (concrete). The generic query[T,P,R] struct cannot be used because
// the constraint Limit(int) T won't match. Instead, generate per-entity concrete types:
//
//	type categoryQuery struct { typ string; tq *ormquery.CategoryQuery }
//	func (q categoryQuery) Type() string { return q.typ }
//	func (q categoryQuery) Limit(n int)  { q.tq.Limit(n) }
//	...
func genInterceptEntityQueryTypes(h gen.GeneratorHelper, f *jen.File, graph *gen.Graph) {
	queryPkg := h.QueryPkg()
	for _, n := range graph.Nodes {
		typeName := strings.ToLower(n.Name[:1]) + n.Name[1:] + "Query"
		qField := jen.Op("*").Qual(queryPkg, n.QueryName())

		// Concrete wrapper struct.
		f.Commentf("%s is a concrete Query wrapper for *%s (entity mode).", typeName, n.QueryName())
		f.Type().Id(typeName).Struct(
			jen.Id("typ").String(),
			jen.Id("tq").Add(qField),
		)

		// Type method.
		f.Func().Params(jen.Id("q").Id(typeName)).Id("Type").Params().String().Block(
			jen.Return(jen.Id("q").Dot("typ")),
		)
		// Limit method.
		f.Func().Params(jen.Id("q").Id(typeName)).Id("Limit").Params(jen.Id("limit").Int()).Block(
			jen.Id("q").Dot("tq").Dot("Limit").Call(jen.Id("limit")),
		)
		// Offset method.
		f.Func().Params(jen.Id("q").Id(typeName)).Id("Offset").Params(jen.Id("offset").Int()).Block(
			jen.Id("q").Dot("tq").Dot("Offset").Call(jen.Id("offset")),
		)
		// Unique method.
		f.Func().Params(jen.Id("q").Id(typeName)).Id("Unique").Params(jen.Id("unique").Bool()).Block(
			jen.Id("q").Dot("tq").Dot("Unique").Call(jen.Id("unique")),
		)
		// Order method.
		// CategoryQuery.Order takes ...func(*sql.Selector) directly — pass through.
		f.Func().Params(jen.Id("q").Id(typeName)).Id("Order").Params(
			jen.Id("orders").Op("...").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector")),
		).Block(
			jen.Id("q").Dot("tq").Dot("Order").Call(jen.Id("orders").Op("...")),
		)
		// WhereP method.
		// Convert []func(*sql.Selector) to []predicate.Entity for the typed Where.
		f.Func().Params(jen.Id("q").Id(typeName)).Id("WhereP").Params(
			jen.Id("ps").Op("...").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector")),
		).Block(
			jen.Id("preds").Op(":=").Make(jen.Index().Qual(h.PredicatePkg(), n.Name), jen.Len(jen.Id("ps"))),
			jen.For(jen.List(jen.Id("i"), jen.Id("p")).Op(":=").Range().Id("ps")).Block(
				jen.Id("preds").Op("[").Id("i").Op("]").Op("=").Id("p"),
			),
			jen.Id("q").Dot("tq").Dot("Where").Call(jen.Id("preds").Op("...")),
		)
	}
}
