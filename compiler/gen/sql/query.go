package sql

import (
	"fmt"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// genQuery generates the query builder file ({entity}_query.go).
func genQuery(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	f := h.NewFile(h.Pkg())

	// Generate Query builder
	genQueryBuilder(h, f, t)

	// Generate Select builder (required by Modify method when sql/modifier feature is enabled)
	genSelectBuilder(h, f, t)

	return f
}

// genQueryBuilder generates the Query builder struct and methods.
func genQueryBuilder(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	queryName := t.QueryName()
	lockEnabled := h.FeatureEnabled("sql/lock")
	modifierEnabled := h.FeatureEnabled("sql/modifier")
	namedEdgesEnabled := h.FeatureEnabled("namedges")
	bidiEdgesEnabled := h.FeatureEnabled("bidiedges")

	// Query struct - follows Ent template field order exactly
	f.Commentf("%s is the builder for querying %s entities.", queryName, t.Name)
	f.Type().Id(queryName).StructFunc(func(group *jen.Group) {
		group.Id("config") // embedded config
		group.Id("ctx").Op("*").Id("QueryContext")
		group.Id("order").Index().Qual(h.EntityPkgPath(t), "OrderOption")
		group.Id("inters").Index().Id("Interceptor")
		group.Id("predicates").Index().Add(h.PredicateType(t))
		// Eager loading fields
		for _, edge := range t.Edges {
			group.Id(edge.EagerLoadField()).Op("*").Id(edge.Type.QueryName())
		}
		// Named eager loading fields for non-unique edges (namedges feature)
		if namedEdgesEnabled {
			for _, edge := range t.Edges {
				if !edge.Unique {
					group.Id(edge.EagerLoadNamedField()).Map(jen.String()).Op("*").Id(edge.Type.QueryName())
				}
			}
		}
		// Modifiers field (used by sql/lock, sql/modifier features, and GraphQL pagination)
		group.Id("modifiers").Index().Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector"))
		// withFKs is used to include foreign keys in queries for eager loading O2M edges
		group.Id("withFKs").Bool()
		// loadTotal callbacks for GraphQL pagination (loads edge counts)
		group.Id("loadTotal").Index().Func().Params(
			jen.Qual("context", "Context"),
			jen.Index().Op("*").Id(t.Name),
		).Error()
		// intermediate query (i.e. traversal path).
		group.Id("sql").Op("*").Qual(h.SQLPkg(), "Selector")
		group.Id("path").Func().Params(jen.Qual("context", "Context")).Params(
			jen.Op("*").Qual(h.SQLPkg(), "Selector"),
			jen.Error(),
		)
	})

	// Where adds predicates
	f.Commentf("Where adds a new predicate for the %s.", queryName)
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("Where").Params(
		jen.Id("ps").Op("...").Add(h.PredicateType(t)),
	).Op("*").Id(queryName).Block(
		jen.Id(t.QueryReceiver()).Dot("predicates").Op("=").Append(jen.Id(t.QueryReceiver()).Dot("predicates"), jen.Id("ps").Op("...")),
		jen.Return(jen.Id(t.QueryReceiver())),
	)

	// Limit sets the limit
	f.Commentf("Limit the number of records to be returned by this query.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("Limit").Params(
		jen.Id("limit").Int(),
	).Op("*").Id(queryName).Block(
		jen.Id(t.QueryReceiver()).Dot("ctx").Dot("Limit").Op("=").Op("&").Id("limit"),
		jen.Return(jen.Id(t.QueryReceiver())),
	)

	// Offset sets the offset
	f.Commentf("Offset to start from.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("Offset").Params(
		jen.Id("offset").Int(),
	).Op("*").Id(queryName).Block(
		jen.Id(t.QueryReceiver()).Dot("ctx").Dot("Offset").Op("=").Op("&").Id("offset"),
		jen.Return(jen.Id(t.QueryReceiver())),
	)

	// Unique sets the unique flag
	f.Commentf("Unique configures the query builder to filter duplicate records on query.")
	f.Commentf("By default, unique is set to true, and can be disabled using this method.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("Unique").Params(
		jen.Id("unique").Bool(),
	).Op("*").Id(queryName).Block(
		jen.Id(t.QueryReceiver()).Dot("ctx").Dot("Unique").Op("=").Op("&").Id("unique"),
		jen.Return(jen.Id(t.QueryReceiver())),
	)

	// Order sets the order
	f.Commentf("Order specifies how the records should be ordered.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("Order").Params(
		jen.Id("o").Op("...").Qual(h.EntityPkgPath(t), "OrderOption"),
	).Op("*").Id(queryName).Block(
		jen.Id(t.QueryReceiver()).Dot("order").Op("=").Append(jen.Id(t.QueryReceiver()).Dot("order"), jen.Id("o").Op("...")),
		jen.Return(jen.Id(t.QueryReceiver())),
	)

	// Define builder names for later use
	selectName := t.Name + "Select"
	groupByName := t.Name + "GroupBy"

	// WithXxx methods for eager loading
	for _, edge := range t.Edges {
		genQueryWithEdge(h, f, t, edge)
		// Generate WithNamed{Edge} for non-unique edges when feature enabled
		if namedEdgesEnabled && !edge.Unique {
			genQueryWithNamedEdge(h, f, t, edge)
		}
	}

	// load{Edge} methods for eager loading
	for _, edge := range t.Edges {
		if edge.M2M() {
			genLoadEdgeM2M(h, f, t, edge)
		} else {
			genLoadEdge(h, f, t, edge)
		}
	}

	// First returns the first entity
	f.Commentf("First returns the first %s entity from the query.", t.Name)
	f.Commentf("Returns a *NotFoundError when no %s was found.", t.Name)
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("First").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Id(t.Name), jen.Error()).Block(
		jen.List(jen.Id("nodes"), jen.Id("err")).Op(":=").Id(t.QueryReceiver()).Dot("Limit").Call(jen.Lit(1)).Dot("All").Call(
			jen.Id("setContextOp").Call(jen.Id("ctx"), jen.Id(t.QueryReceiver()).Dot("ctx"), jen.Id("OpQueryFirst")),
		),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		),
		jen.If(jen.Len(jen.Id("nodes")).Op("==").Lit(0)).Block(
			jen.Return(jen.Nil(), jen.Op("&").Id("NotFoundError").Values(jen.Qual(h.EntityPkgPath(t), "Label"))),
		),
		jen.Return(jen.Id("nodes").Index(jen.Lit(0)), jen.Nil()),
	)

	// FirstX is like First but panics on errors (except NotFound which returns nil)
	f.Commentf("FirstX is like First, but panics if an error occurs.")
	f.Comment("Returns nil (without panicking) if no entity was found.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("FirstX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Op("*").Id(t.Name).Block(
		jen.List(jen.Id("node"), jen.Id("err")).Op(":=").Id(t.QueryReceiver()).Dot("First").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil().Op("&&").Op("!").Id("IsNotFound").Call(jen.Id("err"))).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("node")),
	)

	// FirstID returns the first ID
	f.Commentf("FirstID returns the first %s ID from the query.", t.Name)
	f.Commentf("Returns a *NotFoundError when no %s ID was found.", t.Name)
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("FirstID").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Id("id").Add(h.IDType(t)), jen.Id("err").Error()).Block(
		jen.Var().Id("ids").Index().Add(h.IDType(t)),
		jen.If(
			jen.List(jen.Id("ids"), jen.Id("err")).Op("=").Id(t.QueryReceiver()).Dot("Limit").Call(jen.Lit(1)).Dot("IDs").Call(
				jen.Id("setContextOp").Call(jen.Id("ctx"), jen.Id(t.QueryReceiver()).Dot("ctx"), jen.Id("OpQueryFirstID")),
			),
			jen.Id("err").Op("!=").Nil(),
		).Block(
			jen.Return(),
		),
		jen.If(jen.Len(jen.Id("ids")).Op("==").Lit(0)).Block(
			jen.Id("err").Op("=").Op("&").Id("NotFoundError").Values(jen.Qual(h.EntityPkgPath(t), "Label")),
			jen.Return(),
		),
		jen.Return(jen.Id("ids").Index(jen.Lit(0)), jen.Nil()),
	)

	// FirstIDX is like FirstID but panics on errors (except NotFound which returns zero value)
	f.Commentf("FirstIDX is like FirstID, but panics if an error occurs.")
	f.Comment("Returns zero value (without panicking) if no entity was found.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("FirstIDX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Add(h.IDType(t)).Block(
		jen.List(jen.Id("id"), jen.Id("err")).Op(":=").Id(t.QueryReceiver()).Dot("FirstID").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil().Op("&&").Op("!").Id("IsNotFound").Call(jen.Id("err"))).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("id")),
	)

	// Only returns the only entity
	f.Commentf("Only returns a single %s entity found by the query, ensuring it only returns one.", t.Name)
	f.Commentf("Returns a *NotSingularError when more than one %s entity is found.", t.Name)
	f.Commentf("Returns a *NotFoundError when no %s entities are found.", t.Name)
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("Only").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Id(t.Name), jen.Error()).Block(
		jen.List(jen.Id("nodes"), jen.Id("err")).Op(":=").Id(t.QueryReceiver()).Dot("Limit").Call(jen.Lit(2)).Dot("All").Call(
			jen.Id("setContextOp").Call(jen.Id("ctx"), jen.Id(t.QueryReceiver()).Dot("ctx"), jen.Id("OpQueryOnly")),
		),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		),
		jen.Switch(jen.Len(jen.Id("nodes"))).BlockFunc(func(grp *jen.Group) {
			grp.Case(jen.Lit(1)).Block(
				jen.Return(jen.Id("nodes").Index(jen.Lit(0)), jen.Nil()),
			)
			grp.Case(jen.Lit(0)).Block(
				jen.Return(jen.Nil(), jen.Op("&").Id("NotFoundError").Values(jen.Qual(h.EntityPkgPath(t), "Label"))),
			)
			grp.Default().Block(
				jen.Return(jen.Nil(), jen.Op("&").Id("NotSingularError").Values(jen.Qual(h.EntityPkgPath(t), "Label"))),
			)
		}),
	)

	// OnlyX is like Only but panics
	f.Commentf("OnlyX is like Only, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("OnlyX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Op("*").Id(t.Name).Block(
		jen.List(jen.Id("node"), jen.Id("err")).Op(":=").Id(t.QueryReceiver()).Dot("Only").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("node")),
	)

	// OnlyID returns the only ID
	f.Commentf("OnlyID is like Only, but returns the only %s ID in the query.", t.Name)
	f.Commentf("Returns a *NotSingularError when more than one %s ID is found.", t.Name)
	f.Comment("Returns a *NotFoundError when no entities are found.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("OnlyID").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Id("id").Add(h.IDType(t)), jen.Id("err").Error()).Block(
		jen.Var().Id("ids").Index().Add(h.IDType(t)),
		jen.If(
			jen.List(jen.Id("ids"), jen.Id("err")).Op("=").Id(t.QueryReceiver()).Dot("Limit").Call(jen.Lit(2)).Dot("IDs").Call(
				jen.Id("setContextOp").Call(jen.Id("ctx"), jen.Id(t.QueryReceiver()).Dot("ctx"), jen.Id("OpQueryOnlyID")),
			),
			jen.Id("err").Op("!=").Nil(),
		).Block(
			jen.Return(),
		),
		jen.Switch(jen.Len(jen.Id("ids"))).BlockFunc(func(grp *jen.Group) {
			grp.Case(jen.Lit(1)).Block(
				jen.Id("id").Op("=").Id("ids").Index(jen.Lit(0)),
			)
			grp.Case(jen.Lit(0)).Block(
				jen.Id("err").Op("=").Op("&").Id("NotFoundError").Values(jen.Qual(h.EntityPkgPath(t), "Label")),
			)
			grp.Default().Block(
				jen.Id("err").Op("=").Op("&").Id("NotSingularError").Values(jen.Qual(h.EntityPkgPath(t), "Label")),
			)
		}),
		jen.Return(),
	)

	// OnlyIDX is like OnlyID but panics
	f.Commentf("OnlyIDX is like OnlyID, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("OnlyIDX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Add(h.IDType(t)).Block(
		jen.List(jen.Id("id"), jen.Id("err")).Op(":=").Id(t.QueryReceiver()).Dot("OnlyID").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("id")),
	)

	// All returns all entities
	f.Commentf("All executes the query and returns a list of %ss.", t.Name)
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("All").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Op("*").Id(t.Name), jen.Error()).Block(
		jen.Id("ctx").Op("=").Id("setContextOp").Call(jen.Id("ctx"), jen.Id(t.QueryReceiver()).Dot("ctx"), jen.Id("OpQueryAll")),
		jen.If(jen.Id("err").Op(":=").Id(t.QueryReceiver()).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		),
		jen.Id("qr").Op(":=").Id("querierAll").Types(jen.Index().Op("*").Id(t.Name), jen.Op("*").Id(queryName)).Call(),
		jen.Return(jen.Id("withInterceptors").Types(jen.Index().Op("*").Id(t.Name)).Call(
			jen.Id("ctx"),
			jen.Id(t.QueryReceiver()),
			jen.Id("qr"),
			jen.Id(t.QueryReceiver()).Dot("inters"),
		)),
	)

	// AllX is like All but panics
	f.Commentf("AllX is like All, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("AllX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Index().Op("*").Id(t.Name).Block(
		jen.List(jen.Id("nodes"), jen.Id("err")).Op(":=").Id(t.QueryReceiver()).Dot("All").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("nodes")),
	)

	// IDs returns all IDs
	f.Commentf("IDs executes the query and returns a list of %s IDs.", t.Name)
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("IDs").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Id("ids").Index().Add(h.IDType(t)), jen.Id("err").Error()).Block(
		jen.If(jen.Id(t.QueryReceiver()).Dot("ctx").Dot("Unique").Op("==").Nil().Op("&&").Id(t.QueryReceiver()).Dot("path").Op("!=").Nil()).Block(
			jen.Id(t.QueryReceiver()).Dot("Unique").Call(jen.True()),
		),
		jen.Id("ctx").Op("=").Id("setContextOp").Call(jen.Id("ctx"), jen.Id(t.QueryReceiver()).Dot("ctx"), jen.Id("OpQueryIDs")),
		jen.If(
			jen.Id("err").Op("=").Id(t.QueryReceiver()).Dot("Select").Call(jen.Qual(h.EntityPkgPath(t), "FieldID")).Dot("Scan").Call(jen.Id("ctx"), jen.Op("&").Id("ids")),
			jen.Id("err").Op("!=").Nil(),
		).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		),
		jen.Return(jen.Id("ids"), jen.Nil()),
	)

	// IDsX is like IDs but panics
	f.Commentf("IDsX is like IDs, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("IDsX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Index().Add(h.IDType(t)).Block(
		jen.List(jen.Id("ids"), jen.Id("err")).Op(":=").Id(t.QueryReceiver()).Dot("IDs").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("ids")),
	)

	// Count returns the count
	f.Commentf("Count returns the count of the given query.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("Count").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Int(), jen.Error()).Block(
		jen.Id("ctx").Op("=").Id("setContextOp").Call(jen.Id("ctx"), jen.Id(t.QueryReceiver()).Dot("ctx"), jen.Id("OpQueryCount")),
		jen.If(jen.Id("err").Op(":=").Id(t.QueryReceiver()).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Lit(0), jen.Id("err")),
		),
		jen.Return(jen.Id("withInterceptors").Types(jen.Int()).Call(
			jen.Id("ctx"),
			jen.Id(t.QueryReceiver()),
			jen.Id("querierCount").Types(jen.Op("*").Id(queryName)).Call(),
			jen.Id(t.QueryReceiver()).Dot("inters"),
		)),
	)

	// CountX is like Count but panics
	f.Commentf("CountX is like Count, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("CountX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Int().Block(
		jen.List(jen.Id("count"), jen.Id("err")).Op(":=").Id(t.QueryReceiver()).Dot("Count").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("count")),
	)

	// Exist returns whether entities exist
	f.Commentf("Exist returns true if the query has elements in the graph.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("Exist").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Bool(), jen.Error()).Block(
		jen.Id("ctx").Op("=").Id("setContextOp").Call(jen.Id("ctx"), jen.Id(t.QueryReceiver()).Dot("ctx"), jen.Id("OpQueryExist")),
		jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id(t.QueryReceiver()).Dot("FirstID").Call(jen.Id("ctx")),
		jen.If(jen.Id("IsNotFound").Call(jen.Id("err"))).Block(
			jen.Return(jen.False(), jen.Nil()),
		),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.False(), jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: check existence: %w"), jen.Id("err"))),
		),
		jen.Return(jen.True(), jen.Nil()),
	)

	// ExistX is like Exist but panics
	f.Commentf("ExistX is like Exist, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("ExistX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Bool().Block(
		jen.List(jen.Id("exist"), jen.Id("err")).Op(":=").Id(t.QueryReceiver()).Dot("Exist").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("exist")),
	)

	// Clone returns a copy
	f.Commentf("Clone returns a duplicate of the %s builder, including all associated steps. It can be", queryName)
	f.Commentf("used to prepare common query builders and use them differently after the clone is made.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("Clone").Params().Op("*").Id(queryName).Block(
		jen.If(jen.Id(t.QueryReceiver()).Op("==").Nil()).Block(
			jen.Return(jen.Nil()),
		),
		jen.Return(jen.Op("&").Id(queryName).ValuesFunc(func(vals *jen.Group) {
			vals.Id("config").Op(":").Id(t.QueryReceiver()).Dot("config")
			vals.Id("ctx").Op(":").Id(t.QueryReceiver()).Dot("ctx").Dot("Clone").Call()
			vals.Id("order").Op(":").Append(jen.Index().Qual(h.EntityPkgPath(t), "OrderOption").Values(), jen.Id(t.QueryReceiver()).Dot("order").Op("..."))
			vals.Id("inters").Op(":").Append(jen.Index().Id("Interceptor").Values(), jen.Id(t.QueryReceiver()).Dot("inters").Op("..."))
			vals.Id("predicates").Op(":").Append(jen.Index().Add(h.PredicateType(t)).Values(), jen.Id(t.QueryReceiver()).Dot("predicates").Op("..."))
			// Clone eager loading fields
			for _, edge := range t.Edges {
				vals.Id(edge.EagerLoadField()).Op(":").Id(t.QueryReceiver()).Dot(edge.EagerLoadField()).Dot("Clone").Call()
			}
			// clone intermediate query.
			vals.Id("sql").Op(":").Id(t.QueryReceiver()).Dot("sql").Dot("Clone").Call()
			vals.Id("path").Op(":").Id(t.QueryReceiver()).Dot("path")
			vals.Id("modifiers").Op(":").Append(
				jen.Index().Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector")).Values(),
				jen.Id(t.QueryReceiver()).Dot("modifiers").Op("..."),
			)
		})),
	)

	// Feature: sql/lock - ForUpdate and ForShare methods
	if lockEnabled {
		genQueryLockMethods(h, f, t, queryName)
	}

	// GroupBy is used to group vertices by one or more fields/columns
	f.Comment("GroupBy is used to group vertices by one or more fields/columns.")
	f.Comment("It is often used with aggregate functions, like: count, max, mean, min, sum.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("GroupBy").Params(
		jen.Id("field").String(),
		jen.Id("fields").Op("...").String(),
	).Op("*").Id(groupByName).Block(
		jen.Id(t.QueryReceiver()).Dot("ctx").Dot("Fields").Op("=").Append(jen.Index().String().Values(jen.Id("field")), jen.Id("fields").Op("...")),
		jen.Id("grbuild").Op(":=").Op("&").Id(groupByName).Values(jen.Dict{
			jen.Id("build"): jen.Id(t.QueryReceiver()),
		}),
		jen.Id("grbuild").Dot("flds").Op("=").Op("&").Id(t.QueryReceiver()).Dot("ctx").Dot("Fields"),
		jen.Id("grbuild").Dot("label").Op("=").Qual(h.EntityPkgPath(t), "Label"),
		jen.Id("grbuild").Dot("scan").Op("=").Id("grbuild").Dot("Scan"),
		jen.Return(jen.Id("grbuild")),
	)

	// Select allows selecting one or more fields (columns) of the returned entity
	f.Commentf("Select allows the selection one or more fields/columns for the given query,")
	f.Commentf("instead of selecting all fields in the entity.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("Select").Params(
		jen.Id("fields").Op("...").String(),
	).Op("*").Id(selectName).Block(
		jen.Id(t.QueryReceiver()).Dot("ctx").Dot("Fields").Op("=").Append(jen.Id(t.QueryReceiver()).Dot("ctx").Dot("Fields"), jen.Id("fields").Op("...")),
		jen.Id("sbuild").Op(":=").Op("&").Id(selectName).Values(jen.Dict{
			jen.Id(queryName): jen.Id(t.QueryReceiver()),
		}),
		jen.Id("sbuild").Dot("label").Op("=").Qual(h.EntityPkgPath(t), "Label"),
		jen.List(jen.Id("sbuild").Dot("flds"), jen.Id("sbuild").Dot("scan")).Op("=").List(
			jen.Op("&").Id(t.QueryReceiver()).Dot("ctx").Dot("Fields"),
			jen.Id("sbuild").Dot("Scan"),
		),
		jen.Return(jen.Id("sbuild")),
	)

	// Aggregate returns a Select configured with aggregations
	f.Commentf("Aggregate returns a %s configured with the given aggregations.", selectName)
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("Aggregate").Params(
		jen.Id("fns").Op("...").Id("AggregateFunc"),
	).Op("*").Id(selectName).Block(
		jen.Return(jen.Id(t.QueryReceiver()).Dot("Select").Call().Dot("Aggregate").Call(jen.Id("fns").Op("..."))),
	)

	// prepareQuery prepares the query
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("prepareQuery").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().BlockFunc(func(grp *jen.Group) {
		// Check for nil interceptors and traverse
		grp.For(jen.List(jen.Id("_"), jen.Id("inter")).Op(":=").Range().Id(t.QueryReceiver()).Dot("inters")).Block(
			jen.If(jen.Id("inter").Op("==").Nil()).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: uninitialized interceptor (forgotten import velox/runtime?)"))),
			),
			jen.If(jen.List(jen.Id("trv"), jen.Id("ok")).Op(":=").Id("inter").Op(".").Parens(jen.Id("Traverser")), jen.Id("ok")).Block(
				jen.If(jen.Id("err").Op(":=").Id("trv").Dot("Traverse").Call(jen.Id("ctx"), jen.Id(t.QueryReceiver())), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Id("err")),
				),
			),
		)
		// Validate columns
		grp.For(jen.List(jen.Id("_"), jen.Id("f")).Op(":=").Range().Id(t.QueryReceiver()).Dot("ctx").Dot("Fields")).Block(
			jen.If(jen.Op("!").Qual(h.EntityPkgPath(t), "ValidColumn").Call(jen.Id("f"))).Block(
				jen.Return(jen.Op("&").Id("ValidationError").Values(jen.Dict{
					jen.Id("Name"): jen.Id("f"),
					jen.Id("err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: invalid field %q for query"), jen.Id("f")),
				})),
			),
		)
		// Handle path (traversal)
		grp.If(jen.Id(t.QueryReceiver()).Dot("path").Op("!=").Nil()).Block(
			jen.List(jen.Id("prev"), jen.Id("err")).Op(":=").Id(t.QueryReceiver()).Dot("path").Call(jen.Id("ctx")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Id("err")),
			),
			jen.Id(t.QueryReceiver()).Dot("sql").Op("=").Id("prev"),
		)
		grp.Return(jen.Nil())
	})

	// sqlQuery returns a new selector for the query.
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("sqlQuery").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Op("*").Qual(h.SQLPkg(), "Selector").BlockFunc(func(grp *jen.Group) {
		// Build selector using dialect
		grp.Id("builder").Op(":=").Qual(h.SQLPkg(), "Dialect").Call(jen.Id(t.QueryReceiver()).Dot("driver").Dot("Dialect").Call())
		grp.Id("t1").Op(":=").Id("builder").Dot("Table").Call(jen.Qual(h.EntityPkgPath(t), "Table"))
		grp.Id("columns").Op(":=").Id(t.QueryReceiver()).Dot("ctx").Dot("Fields")
		grp.If(jen.Len(jen.Id("columns")).Op("==").Lit(0)).Block(
			jen.Id("columns").Op("=").Qual(h.EntityPkgPath(t), "Columns"),
		)
		grp.Id("selector").Op(":=").Id("builder").Dot("Select").Call(jen.Id("t1").Dot("Columns").Call(jen.Id("columns").Op("...")).Op("...")).Dot("From").Call(jen.Id("t1"))
		// Handle existing sql selector (from traversal)
		grp.If(jen.Id(t.QueryReceiver()).Dot("sql").Op("!=").Nil()).Block(
			jen.Id("selector").Op("=").Id(t.QueryReceiver()).Dot("sql"),
			jen.Id("selector").Dot("Select").Call(jen.Id("selector").Dot("Columns").Call(jen.Id("columns").Op("...")).Op("...")),
		)
		// Apply unique/distinct
		grp.If(jen.Id(t.QueryReceiver()).Dot("ctx").Dot("Unique").Op("!=").Nil().Op("&&").Op("*").Id(t.QueryReceiver()).Dot("ctx").Dot("Unique")).Block(
			jen.Id("selector").Dot("Distinct").Call(),
		)
		// Apply modifiers
		grp.For(jen.List(jen.Id("_"), jen.Id("m")).Op(":=").Range().Id(t.QueryReceiver()).Dot("modifiers")).Block(
			jen.Id("m").Call(jen.Id("selector")),
		)
		// Apply predicates
		grp.For(jen.List(jen.Id("_"), jen.Id("p")).Op(":=").Range().Id(t.QueryReceiver()).Dot("predicates")).Block(
			jen.Id("p").Call(jen.Id("selector")),
		)
		// Apply ordering
		grp.For(jen.List(jen.Id("_"), jen.Id("p")).Op(":=").Range().Id(t.QueryReceiver()).Dot("order")).Block(
			jen.Id("p").Call(jen.Id("selector")),
		)
		// Apply offset (with mandatory limit)
		grp.If(jen.Id("offset").Op(":=").Id(t.QueryReceiver()).Dot("ctx").Dot("Offset"), jen.Id("offset").Op("!=").Nil()).Block(
			jen.Comment("limit is mandatory for offset clause. We start"),
			jen.Comment("with default value, and override it below if needed."),
			jen.Id("selector").Dot("Offset").Call(jen.Op("*").Id("offset")).Dot("Limit").Call(jen.Qual("math", "MaxInt32")),
		)
		// Apply limit
		grp.If(jen.Id("limit").Op(":=").Id(t.QueryReceiver()).Dot("ctx").Dot("Limit"), jen.Id("limit").Op("!=").Nil()).Block(
			jen.Id("selector").Dot("Limit").Call(jen.Op("*").Id("limit")),
		)
		grp.Return(jen.Id("selector"))
	})

	// querySpec returns the query specification for sqlgraph operations
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("querySpec").Params().Op("*").Qual(h.SQLGraphPkg(), "QuerySpec").BlockFunc(func(grp *jen.Group) {
		// Create new query spec
		if t.HasOneFieldID() {
			grp.Id("_spec").Op(":=").Qual(h.SQLGraphPkg(), "NewQuerySpec").Call(
				jen.Qual(h.EntityPkgPath(t), "Table"),
				jen.Qual(h.EntityPkgPath(t), "Columns"),
				jen.Qual(h.SQLGraphPkg(), "NewFieldSpec").Call(
					jen.Qual(h.EntityPkgPath(t), t.ID.Constant()),
					jen.Qual(h.FieldPkg(), t.ID.Type.ConstName()),
				),
			)
		} else {
			grp.Id("_spec").Op(":=").Qual(h.SQLGraphPkg(), "NewQuerySpec").Call(
				jen.Qual(h.EntityPkgPath(t), "Table"),
				jen.Qual(h.EntityPkgPath(t), "Columns"),
				jen.Nil(),
			)
		}
		// Set From (intermediate query)
		grp.Id("_spec").Dot("From").Op("=").Id(t.QueryReceiver()).Dot("sql")
		// Set Unique
		grp.If(jen.Id("unique").Op(":=").Id(t.QueryReceiver()).Dot("ctx").Dot("Unique"), jen.Id("unique").Op("!=").Nil()).Block(
			jen.Id("_spec").Dot("Unique").Op("=").Op("*").Id("unique"),
		).Else().If(jen.Id(t.QueryReceiver()).Dot("path").Op("!=").Nil()).Block(
			jen.Id("_spec").Dot("Unique").Op("=").True(),
		)
		// Set Fields
		grp.If(jen.Id("fields").Op(":=").Id(t.QueryReceiver()).Dot("ctx").Dot("Fields"), jen.Len(jen.Id("fields")).Op(">").Lit(0)).BlockFunc(func(inner *jen.Group) {
			inner.Id("_spec").Dot("Node").Dot("Columns").Op("=").Make(jen.Index().String(), jen.Lit(0), jen.Len(jen.Id("fields")))
			if t.HasOneFieldID() {
				inner.Id("_spec").Dot("Node").Dot("Columns").Op("=").Append(
					jen.Id("_spec").Dot("Node").Dot("Columns"),
					jen.Qual(h.EntityPkgPath(t), t.ID.Constant()),
				)
				inner.For(jen.Id("i").Op(":=").Range().Id("fields")).Block(
					jen.If(jen.Id("fields").Index(jen.Id("i")).Op("!=").Qual(h.EntityPkgPath(t), t.ID.Constant())).Block(
						jen.Id("_spec").Dot("Node").Dot("Columns").Op("=").Append(
							jen.Id("_spec").Dot("Node").Dot("Columns"),
							jen.Id("fields").Index(jen.Id("i")),
						),
					),
				)
			} else {
				inner.For(jen.Id("i").Op(":=").Range().Id("fields")).Block(
					jen.Id("_spec").Dot("Node").Dot("Columns").Op("=").Append(
						jen.Id("_spec").Dot("Node").Dot("Columns"),
						jen.Id("fields").Index(jen.Id("i")),
					),
				)
			}
		})
		// Add FK columns when withFKs is true (for eager loading O2M edges)
		// Only unexported (auto-generated) FKs need to be added - user-defined FK fields are already in Columns
		if len(t.UnexportedForeignKeys()) > 0 {
			grp.If(jen.Id(t.QueryReceiver()).Dot("withFKs")).Block(
				jen.Id("_spec").Dot("Node").Dot("Columns").Op("=").Append(
					jen.Id("_spec").Dot("Node").Dot("Columns"),
					jen.Qual(h.EntityPkgPath(t), "ForeignKeys").Op("..."),
				),
			)
		}
		// Set Predicate
		grp.If(jen.Id("ps").Op(":=").Id(t.QueryReceiver()).Dot("predicates"), jen.Len(jen.Id("ps")).Op(">").Lit(0)).Block(
			jen.Id("_spec").Dot("Predicate").Op("=").Func().Params(jen.Id("selector").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
				jen.For(jen.Id("i").Op(":=").Range().Id("ps")).Block(
					jen.Id("ps").Index(jen.Id("i")).Call(jen.Id("selector")),
				),
			),
		)
		// Set Limit and Offset
		grp.If(jen.Id("limit").Op(":=").Id(t.QueryReceiver()).Dot("ctx").Dot("Limit"), jen.Id("limit").Op("!=").Nil()).Block(
			jen.Id("_spec").Dot("Limit").Op("=").Op("*").Id("limit"),
		)
		grp.If(jen.Id("offset").Op(":=").Id(t.QueryReceiver()).Dot("ctx").Dot("Offset"), jen.Id("offset").Op("!=").Nil()).Block(
			jen.Id("_spec").Dot("Offset").Op("=").Op("*").Id("offset"),
		)
		// Set Order
		grp.If(jen.Id("ps").Op(":=").Id(t.QueryReceiver()).Dot("order"), jen.Len(jen.Id("ps")).Op(">").Lit(0)).Block(
			jen.Id("_spec").Dot("Order").Op("=").Func().Params(jen.Id("selector").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
				jen.For(jen.Id("i").Op(":=").Range().Id("ps")).Block(
					jen.Id("ps").Index(jen.Id("i")).Call(jen.Id("selector")),
				),
			),
		)
		grp.Return(jen.Id("_spec"))
	})

	// sqlAll executes the query and returns all entities
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("sqlAll").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("hooks").Op("...").Id("queryHook"),
	).Params(jen.Index().Op("*").Id(t.Name), jen.Error()).BlockFunc(func(grp *jen.Group) {
		grp.Var().Defs(
			jen.Id("nodes").Op("=").Index().Op("*").Id(t.Name).Values(),
			jen.Id("_spec").Op("=").Id(t.QueryReceiver()).Dot("querySpec").Call(),
		)
		grp.Id("_spec").Dot("ScanValues").Op("=").Func().Params(jen.Id("columns").Index().String()).Params(jen.Index().Any(), jen.Error()).Block(
			jen.Return(jen.Parens(jen.Op("*").Id(t.Name)).Dot("scanValues").Call(jen.Nil(), jen.Id("columns"))),
		)
		grp.Id("_spec").Dot("Assign").Op("=").Func().Params(jen.Id("columns").Index().String(), jen.Id("values").Index().Any()).Error().Block(
			jen.Id("node").Op(":=").Op("&").Id(t.Name).Values(jen.Dict{
				jen.Id("config"): jen.Id(t.QueryReceiver()).Dot("config"),
			}),
			jen.Id("nodes").Op("=").Append(jen.Id("nodes"), jen.Id("node")),
			jen.Return(jen.Id("node").Dot("assignValues").Call(jen.Id("columns"), jen.Id("values"))),
		)
		// Apply modifiers
		grp.If(jen.Len(jen.Id(t.QueryReceiver()).Dot("modifiers")).Op(">").Lit(0)).Block(
			jen.Id("_spec").Dot("Modifiers").Op("=").Id(t.QueryReceiver()).Dot("modifiers"),
		)
		// Apply hooks
		grp.For(jen.Id("i").Op(":=").Range().Id("hooks")).Block(
			jen.Id("hooks").Index(jen.Id("i")).Call(jen.Id("ctx"), jen.Id("_spec")),
		)
		// Execute query
		grp.If(jen.Id("err").Op(":=").Qual(h.SQLGraphPkg(), "QueryNodes").Call(
			jen.Id("ctx"), jen.Id(t.QueryReceiver()).Dot("driver"), jen.Id("_spec"),
		), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		)
		grp.If(jen.Len(jen.Id("nodes")).Op("==").Lit(0)).Block(
			jen.Return(jen.Id("nodes"), jen.Nil()),
		)
		// Eager loading for each edge (O2O, O2M, M2O)
		for _, edge := range t.Edges {
			if edge.M2M() {
				continue // M2M handled separately below
			}
			loadMethod := "load" + edge.StructField()
			edgeField := edge.EagerLoadField()
			grp.If(jen.Id("query").Op(":=").Id(t.QueryReceiver()).Dot(edgeField), jen.Id("query").Op("!=").Nil()).BlockFunc(func(ifGrp *jen.Group) {
				if edge.Unique {
					// Unique edge (O2O, M2O): assign directly
					ifGrp.If(jen.Id("err").Op(":=").Id(t.QueryReceiver()).Dot(loadMethod).Call(
						jen.Id("ctx"),
						jen.Id("query"),
						jen.Id("nodes"),
						jen.Nil(),
						jen.Func().Params(jen.Id("n").Op("*").Id(t.Name), jen.Id("e").Op("*").Id(edge.Type.Name)).Block(
							jen.Id("n").Dot("Edges").Dot(edge.StructField()).Op("=").Id("e"),
						),
					), jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Id("err")),
					)
				} else {
					// Non-unique edge (O2M): append to slice
					ifGrp.If(jen.Id("err").Op(":=").Id(t.QueryReceiver()).Dot(loadMethod).Call(
						jen.Id("ctx"),
						jen.Id("query"),
						jen.Id("nodes"),
						jen.Func().Params(jen.Id("n").Op("*").Id(t.Name)).Block(
							jen.Id("n").Dot("Edges").Dot(edge.StructField()).Op("=").Index().Op("*").Id(edge.Type.Name).Values(),
						),
						jen.Func().Params(jen.Id("n").Op("*").Id(t.Name), jen.Id("e").Op("*").Id(edge.Type.Name)).Block(
							jen.Id("n").Dot("Edges").Dot(edge.StructField()).Op("=").Append(
								jen.Id("n").Dot("Edges").Dot(edge.StructField()),
								jen.Id("e"),
							),
						),
					), jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Id("err")),
					)
				}
			})
		}
		// Eager loading for M2M edges
		for _, edge := range t.Edges {
			if !edge.M2M() {
				continue
			}
			loadMethod := "load" + edge.StructField()
			edgeField := edge.EagerLoadField()
			grp.If(jen.Id("query").Op(":=").Id(t.QueryReceiver()).Dot(edgeField), jen.Id("query").Op("!=").Nil()).Block(
				jen.If(jen.Id("err").Op(":=").Id(t.QueryReceiver()).Dot(loadMethod).Call(
					jen.Id("ctx"),
					jen.Id("query"),
					jen.Id("nodes"),
					jen.Func().Params(jen.Id("n").Op("*").Id(t.Name)).Block(
						jen.Id("n").Dot("Edges").Dot(edge.StructField()).Op("=").Index().Op("*").Id(edge.Type.Name).Values(),
					),
					jen.Func().Params(jen.Id("n").Op("*").Id(t.Name), jen.Id("e").Op("*").Id(edge.Type.Name)).Block(
						jen.Id("n").Dot("Edges").Dot(edge.StructField()).Op("=").Append(
							jen.Id("n").Dot("Edges").Dot(edge.StructField()),
							jen.Id("e"),
						),
					),
				), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
			)
		}
		// Named eager loading for non-unique edges (namedges feature)
		if namedEdgesEnabled {
			for _, edge := range t.Edges {
				if edge.Unique {
					continue // Named edges only for non-unique edges
				}
				loadMethod := "load" + edge.StructField()
				namedField := edge.EagerLoadNamedField()
				grp.For(jen.List(jen.Id("name"), jen.Id("query")).Op(":=").Range().Id(t.QueryReceiver()).Dot(namedField)).Block(
					jen.If(jen.Id("err").Op(":=").Id(t.QueryReceiver()).Dot(loadMethod).Call(
						jen.Id("ctx"),
						jen.Id("query"),
						jen.Id("nodes"),
						jen.Func().Params(jen.Id("n").Op("*").Id(t.Name)).Block(
							jen.Id("n").Dot("appendNamed"+edge.StructField()).Call(jen.Id("name")),
						),
						jen.Func().Params(jen.Id("n").Op("*").Id(t.Name), jen.Id("e").Op("*").Id(edge.Type.Name)).Block(
							jen.Id("n").Dot("appendNamed"+edge.StructField()).Call(jen.Id("name"), jen.Id("e")),
						),
					), jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Id("err")),
					),
				)
			}
			// Named eager loading for M2M edges
			for _, edge := range t.Edges {
				if !edge.M2M() || edge.Unique {
					continue
				}
				loadMethod := "load" + edge.StructField()
				namedField := edge.EagerLoadNamedField()
				grp.For(jen.List(jen.Id("name"), jen.Id("query")).Op(":=").Range().Id(t.QueryReceiver()).Dot(namedField)).Block(
					jen.If(jen.Id("err").Op(":=").Id(t.QueryReceiver()).Dot(loadMethod).Call(
						jen.Id("ctx"),
						jen.Id("query"),
						jen.Id("nodes"),
						jen.Func().Params(jen.Id("n").Op("*").Id(t.Name)).Block(
							jen.Id("n").Dot("appendNamed"+edge.StructField()).Call(jen.Id("name")),
						),
						jen.Func().Params(jen.Id("n").Op("*").Id(t.Name), jen.Id("e").Op("*").Id(edge.Type.Name)).Block(
							jen.Id("n").Dot("appendNamed"+edge.StructField()).Call(jen.Id("name"), jen.Id("e")),
						),
					), jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Id("err")),
					),
				)
			}
		}
		// Load total callbacks
		grp.For(jen.Id("i").Op(":=").Range().Id(t.QueryReceiver()).Dot("loadTotal")).Block(
			jen.If(jen.Id("err").Op(":=").Id(t.QueryReceiver()).Dot("loadTotal").Index(jen.Id("i")).Call(
				jen.Id("ctx"), jen.Id("nodes"),
			), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
		)
		// Set bidirectional edge references (bidiedges feature)
		if bidiEdgesEnabled {
			genBidiEdgeRefCalls(grp, t)
		}
		grp.Return(jen.Id("nodes"), jen.Nil())
	})

	// sqlCount executes the query and returns the count
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("sqlCount").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Int(), jen.Error()).BlockFunc(func(grp *jen.Group) {
		grp.Id("_spec").Op(":=").Id(t.QueryReceiver()).Dot("querySpec").Call()
		// Apply modifiers
		grp.If(jen.Len(jen.Id(t.QueryReceiver()).Dot("modifiers")).Op(">").Lit(0)).Block(
			jen.Id("_spec").Dot("Modifiers").Op("=").Id(t.QueryReceiver()).Dot("modifiers"),
		)
		grp.Id("_spec").Dot("Node").Dot("Columns").Op("=").Id(t.QueryReceiver()).Dot("ctx").Dot("Fields")
		grp.If(jen.Len(jen.Id(t.QueryReceiver()).Dot("ctx").Dot("Fields")).Op(">").Lit(0)).Block(
			jen.Id("_spec").Dot("Unique").Op("=").Id(t.QueryReceiver()).Dot("ctx").Dot("Unique").Op("!=").Nil().Op("&&").Op("*").Id(t.QueryReceiver()).Dot("ctx").Dot("Unique"),
		)
		grp.Return(jen.Qual(h.SQLGraphPkg(), "CountNodes").Call(
			jen.Id("ctx"), jen.Id(t.QueryReceiver()).Dot("driver"), jen.Id("_spec"),
		))
	})

	// Feature: sql/modifier - Modify method (at end of Query methods)
	if modifierEnabled {
		genQueryModifyMethod(h, f, t, queryName)
	}

	// Feature: privacy - Filter method for privacy rules
	if h.FeatureEnabled("privacy") {
		genQueryFilterMethod(h, f, t, queryName)
	}
}

// genQueryFilterMethod generates the Filter method for privacy filtering.
func genQueryFilterMethod(h gen.GeneratorHelper, f *jen.File, t *gen.Type, queryName string) {
	filterName := t.Name + "Filter"
	privacyPkg := "github.com/syssam/velox/privacy"

	f.Comment("Filter returns a Filter implementation to apply filters on the query.")
	f.Comment("For use with privacy rules and dynamic filtering in privacy policies.")
	// Return privacy.Filter interface to allow core FilterFunc to work
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("Filter").Params().Qual(privacyPkg, "Filter").Block(
		// Set unique to true for filtering
		jen.Id("unique").Op(":=").True(),
		jen.Id(t.QueryReceiver()).Dot("ctx").Dot("Unique").Op("=").Op("&").Id("unique"),
		jen.Return(jen.Op("&").Id(filterName).Values(jen.Dict{
			jen.Id("config"):     jen.Id(t.QueryReceiver()).Dot("config"),
			jen.Id("predicates"): jen.Op("&").Id(t.QueryReceiver()).Dot("predicates"),
		})),
	)
}

// genQueryWithEdge generates WithXxx methods for eager loading.
func genQueryWithEdge(h gen.GeneratorHelper, f *jen.File, t *gen.Type, edge *gen.Edge) {
	queryName := t.QueryName()
	methodName := "With" + edge.StructField()
	optionType := edge.Type.QueryName()

	f.Commentf("%s tells the query-builder to eager-load the %q edge.", methodName, edge.Name)
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id(methodName).Params(
		jen.Id("opts").Op("...").Func().Params(jen.Op("*").Id(optionType)),
	).Op("*").Id(queryName).Block(
		jen.Id("query").Op(":=").Parens(jen.Op("&").Id(edge.Type.Name+"Client").Values(jen.Dict{
			jen.Id("config"): jen.Id(t.QueryReceiver()).Dot("config"),
		})).Dot("Query").Call(),
		jen.For(jen.List(jen.Id("_"), jen.Id("opt")).Op(":=").Range().Id("opts")).Block(
			jen.Id("opt").Call(jen.Id("query")),
		),
		jen.Id(t.QueryReceiver()).Dot(edge.EagerLoadField()).Op("=").Id("query"),
		jen.Return(jen.Id(t.QueryReceiver())),
	)
}

// genQueryWithNamedEdge generates WithNamed{Edge} methods for dynamic named edge loading.
// This is part of the namedges feature. The method stores named queries in a map so that
// the loaded edges can be retrieved by name later.
func genQueryWithNamedEdge(h gen.GeneratorHelper, f *jen.File, t *gen.Type, edge *gen.Edge) {
	queryName := t.QueryName()
	methodName := "WithNamed" + edge.StructField()
	optionType := edge.Type.QueryName()
	namedField := edge.EagerLoadNamedField()

	f.Commentf("%s tells the query-builder to eager-load the nodes that are connected to the %q", methodName, edge.Name)
	f.Comment("edge with the given name. The optional arguments are used to configure the query builder of the edge.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id(methodName).Params(
		jen.Id("name").String(),
		jen.Id("opts").Op("...").Func().Params(jen.Op("*").Id(optionType)),
	).Op("*").Id(queryName).Block(
		jen.Id("query").Op(":=").Parens(jen.Op("&").Id(edge.Type.Name+"Client").Values(jen.Dict{
			jen.Id("config"): jen.Id(t.QueryReceiver()).Dot("config"),
		})).Dot("Query").Call(),
		jen.For(jen.List(jen.Id("_"), jen.Id("opt")).Op(":=").Range().Id("opts")).Block(
			jen.Id("opt").Call(jen.Id("query")),
		),
		jen.If(jen.Id(t.QueryReceiver()).Dot(namedField).Op("==").Nil()).Block(
			jen.Id(t.QueryReceiver()).Dot(namedField).Op("=").Make(jen.Map(jen.String()).Op("*").Id(optionType)),
		),
		jen.Id(t.QueryReceiver()).Dot(namedField).Index(jen.Id("name")).Op("=").Id("query"),
		jen.Return(jen.Id(t.QueryReceiver())),
	)
}

// genBidiEdgeRefCalls generates calls to set{Edge}BidiRef() methods for all edges
// that have inverse references. This is called after eager loading to enable
// bidirectional navigation (e.g., user.Edges.Posts[0].Edges.Owner).
func genBidiEdgeRefCalls(grp *jen.Group, t *gen.Type) {
	// Collect edges that have inverse references
	var bidiEdges []*gen.Edge
	for _, edge := range t.Edges {
		if edge.Ref != nil {
			bidiEdges = append(bidiEdges, edge)
		}
	}
	if len(bidiEdges) == 0 {
		return
	}
	// Generate: for _, n := range nodes { n.set{Edge}BidiRef(); ... }
	var stmts []jen.Code
	for _, edge := range bidiEdges {
		methodName := "set" + edge.StructField() + "BidiRef"
		stmts = append(stmts, jen.Id("n").Dot(methodName).Call())
	}
	grp.For(jen.List(jen.Id("_"), jen.Id("n")).Op(":=").Range().Id("nodes")).Block(stmts...)
}

// genLoadEdge generates load{Edge} method for eager loading a single edge.
// The load method fetches related entities and assigns them to the parent nodes.
func genLoadEdge(h gen.GeneratorHelper, f *jen.File, t *gen.Type, edge *gen.Edge) {
	queryName := t.QueryName()
	methodName := "load" + edge.StructField()
	edgeQueryName := edge.Type.QueryName()

	// Determine if this edge owns the FK (FK is on our side)
	// M2O and inverse O2O edges have FK on our side
	ownFK := edge.OwnFK()

	// Get the FK field info for edges that own the FK
	var fkStructField string // Go struct field name (e.g., "CategoryID" or "todo_children")
	var fkColumnName string  // Database column name (e.g., "category_id" or "todo_children")
	var fkNillable bool      // Whether the FK field is a pointer type

	if ownFK {
		if fk, err := edge.ForeignKey(); err == nil {
			fkStructField = fk.StructField()
			fkColumnName = fk.Field.StorageKey()
			fkNillable = fk.Field.Nillable
		} else {
			// Fallback: derive from edge relation
			if len(edge.Rel.Columns) > 0 {
				fkColumnName = edge.Rel.Columns[0]
				fkStructField = fkColumnName
				fkNillable = edge.Optional
			}
		}
	}

	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id(methodName).Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("query").Op("*").Id(edgeQueryName),
		jen.Id("nodes").Index().Op("*").Id(t.Name),
		jen.Id("init").Func().Params(jen.Op("*").Id(t.Name)),
		jen.Id("assign").Func().Params(jen.Op("*").Id(t.Name), jen.Op("*").Id(edge.Type.Name)),
	).Error().BlockFunc(func(grp *jen.Group) {
		if ownFK {
			// M2O edge: FK is on our side (e.g., loadParent, loadCategory)
			// Collect FKs from nodes, then query related entities by their IDs
			// Use the related entity's ID type (not hardcoded int)
			grp.Id("ids").Op(":=").Make(jen.Index().Add(h.IDType(edge.Type)), jen.Lit(0), jen.Len(jen.Id("nodes")))
			grp.Id("nodeids").Op(":=").Make(jen.Map(h.IDType(edge.Type)).Index().Op("*").Id(t.Name))
			grp.For(jen.Id("i").Op(":=").Range().Id("nodes")).BlockFunc(func(forGrp *jen.Group) {
				// Check if FK field is nil (only for nillable/pointer FK fields)
				if fkNillable {
					forGrp.If(jen.Id("nodes").Index(jen.Id("i")).Dot(fkStructField).Op("==").Nil()).Block(
						jen.Continue(),
					)
					forGrp.Id("fk").Op(":=").Op("*").Id("nodes").Index(jen.Id("i")).Dot(fkStructField)
				} else {
					forGrp.Id("fk").Op(":=").Id("nodes").Index(jen.Id("i")).Dot(fkStructField)
				}
				forGrp.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("nodeids").Index(jen.Id("fk")), jen.Op("!").Id("ok")).Block(
					jen.Id("ids").Op("=").Append(jen.Id("ids"), jen.Id("fk")),
				)
				forGrp.Id("nodeids").Index(jen.Id("fk")).Op("=").Append(jen.Id("nodeids").Index(jen.Id("fk")), jen.Id("nodes").Index(jen.Id("i")))
			})
			grp.If(jen.Len(jen.Id("ids")).Op("==").Lit(0)).Block(
				jen.Return(jen.Nil()),
			)
			grp.Id("query").Dot("Where").Call(
				idInPredicate(h, edge.Type, jen.Id("ids").Op("...")),
			)
			grp.List(jen.Id("neighbors"), jen.Id("err")).Op(":=").Id("query").Dot("All").Call(jen.Id("ctx"))
			grp.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Id("err")),
			)
			grp.For(jen.List(jen.Id("_"), jen.Id("n")).Op(":=").Range().Id("neighbors")).Block(
				jen.List(jen.Id("nodes"), jen.Id("ok")).Op(":=").Id("nodeids").Index(jen.Id("n").Dot("ID")),
				jen.If(jen.Op("!").Id("ok")).Block(
					jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected foreign-key %q returned %v"), jen.Lit(fkColumnName), jen.Id("n").Dot("ID"))),
				),
				jen.For(jen.Id("i").Op(":=").Range().Id("nodes")).Block(
					jen.Id("assign").Call(jen.Id("nodes").Index(jen.Id("i")), jen.Id("n")),
				),
			)
		} else {
			// O2M or M2M edge: FK is on the other side (e.g., loadChildren)
			// Collect our IDs, then query related entities that have FK pointing to our IDs

			// Get the FK info from the inverse edge (Ref) on the related type
			var refFKStructField string
			var refFKColumnName string
			refFKNillable := true // FK on related type is typically nillable

			// The Ref edge points to the inverse (M2O) edge on the related type
			if edge.Ref != nil {
				if fk, err := edge.Ref.ForeignKey(); err == nil {
					refFKStructField = fk.StructField()
					refFKColumnName = fk.Field.StorageKey()
					refFKNillable = fk.Field.Nillable || !fk.UserDefined
				} else if len(edge.Ref.Rel.Columns) > 0 {
					refFKColumnName = edge.Ref.Rel.Columns[0]
					refFKStructField = refFKColumnName
				}
			}

			// If no Ref (edges not explicitly linked), search for a M2O edge on the
			// related type that points back to the current type. This handles schemas
			// where both sides use edge.To() instead of edge.From().Ref().
			if refFKStructField == "" && edge.Type != nil {
				for _, relEdge := range edge.Type.Edges {
					// Look for M2O edge pointing to current type with a FK
					if relEdge.M2O() && relEdge.Type == t {
						if fk, err := relEdge.ForeignKey(); err == nil {
							refFKStructField = fk.StructField()
							refFKColumnName = fk.Field.StorageKey()
							refFKNillable = fk.Field.Nillable || !fk.UserDefined
							break
						}
					}
				}
			}

			// Fallback: use edge's relation columns
			if refFKStructField == "" && len(edge.Rel.Columns) > 0 {
				refFKColumnName = edge.Rel.Columns[0]
				// Convert column name to Go struct field name (PascalCase)
				refFKStructField = gen.Pascal(refFKColumnName)
			}
			// Final fallback: generate FK field name from type label (already snake_case)
			if refFKStructField == "" {
				if t.ID == nil {
					panic(fmt.Sprintf("velox/gen: cannot generate FK column name for edge %q on type %q: type has no ID field (view type?)", edge.Name, t.Name))
				}
				refFKColumnName = t.Label() + "_" + t.ID.Name
				refFKStructField = gen.Pascal(refFKColumnName)
			}

			grp.Id("fks").Op(":=").Make(jen.Index().Qual("database/sql/driver", "Value"), jen.Lit(0), jen.Len(jen.Id("nodes")))
			// Use current entity's ID type (not hardcoded int)
			grp.Id("nodeids").Op(":=").Make(jen.Map(h.IDType(t)).Op("*").Id(t.Name))
			grp.For(jen.Id("i").Op(":=").Range().Id("nodes")).Block(
				jen.Id("fks").Op("=").Append(jen.Id("fks"), jen.Id("nodes").Index(jen.Id("i")).Dot("ID")),
				jen.Id("nodeids").Index(jen.Id("nodes").Index(jen.Id("i")).Dot("ID")).Op("=").Id("nodes").Index(jen.Id("i")),
				jen.If(jen.Id("init").Op("!=").Nil()).Block(
					jen.Id("init").Call(jen.Id("nodes").Index(jen.Id("i"))),
				),
			)

			// Only set withFKs if the FK is not user-defined (auto-generated FK needs to be included)
			if refFKNillable {
				grp.Id("query").Dot("withFKs").Op("=").True()
			}

			// Use column constant from the edge owner's package (current type's package)
			grp.Id("query").Dot("Where").Call(
				jen.Qual(h.PredicatePkg(), edge.Type.Name).Call(
					jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
						jen.Id("s").Dot("Where").Call(
							jen.Qual(h.SQLPkg(), "InValues").Call(
								// Column constant from current type's package
								jen.Id("s").Dot("C").Call(jen.Qual(h.EntityPkgPath(t), edge.ColumnConstant())),
								jen.Id("fks").Op("..."),
							),
						),
					),
				),
			)
			grp.List(jen.Id("neighbors"), jen.Id("err")).Op(":=").Id("query").Dot("All").Call(jen.Id("ctx"))
			grp.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Id("err")),
			)
			grp.For(jen.List(jen.Id("_"), jen.Id("n")).Op(":=").Range().Id("neighbors")).BlockFunc(func(forGrp *jen.Group) {
				if refFKNillable {
					// FK is a pointer type
					forGrp.Id("fk").Op(":=").Id("n").Dot(refFKStructField)
					forGrp.If(jen.Id("fk").Op("==").Nil()).Block(
						jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("foreign-key %q is nil for node %v"), jen.Lit(refFKColumnName), jen.Id("n").Dot("ID"))),
					)
					forGrp.List(jen.Id("node"), jen.Id("ok")).Op(":=").Id("nodeids").Index(jen.Op("*").Id("fk"))
					forGrp.If(jen.Op("!").Id("ok")).Block(
						jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected referenced foreign-key %q returned %v for node %v"), jen.Lit(refFKColumnName), jen.Op("*").Id("fk"), jen.Id("n").Dot("ID"))),
					)
				} else {
					// FK is a value type (user-defined field)
					forGrp.Id("fk").Op(":=").Id("n").Dot(refFKStructField)
					forGrp.List(jen.Id("node"), jen.Id("ok")).Op(":=").Id("nodeids").Index(jen.Id("fk"))
					forGrp.If(jen.Op("!").Id("ok")).Block(
						jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected referenced foreign-key %q returned %v for node %v"), jen.Lit(refFKColumnName), jen.Id("fk"), jen.Id("n").Dot("ID"))),
					)
				}
				forGrp.Id("assign").Call(jen.Id("node"), jen.Id("n"))
			})
		}
		grp.Return(jen.Nil())
	})
}

// genLoadEdgeM2M generates the load{Edge} method for M2M edges.
// M2M edges require querying the join table first to get the relationships,
// then querying the related entities.
func genLoadEdgeM2M(h gen.GeneratorHelper, f *jen.File, t *gen.Type, edge *gen.Edge) {
	// Validate that both types have ID fields (required for M2M relationships)
	if t.ID == nil {
		panic(fmt.Sprintf("velox/gen: cannot generate M2M edge %q: owner type %q has no ID field (view type?)", edge.Name, t.Name))
	}
	if edge.Type.ID == nil {
		panic(fmt.Sprintf("velox/gen: cannot generate M2M edge %q: related type %q has no ID field (view type?)", edge.Name, edge.Type.Name))
	}

	queryName := t.QueryName()
	methodName := "load" + edge.StructField()
	edgeQueryName := edge.Type.QueryName()

	// M2M has a join table with two columns
	// Columns[0] is our side (e.g., category_id)
	// Columns[1] is the related side (e.g., sub_category_id)
	var ourColumn, relatedColumn string
	if len(edge.Rel.Columns) >= 2 {
		if edge.IsInverse() {
			// For inverse edges, the columns are reversed
			ourColumn = edge.Rel.Columns[1]
			relatedColumn = edge.Rel.Columns[0]
		} else {
			ourColumn = edge.Rel.Columns[0]
			relatedColumn = edge.Rel.Columns[1]
		}
	}

	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id(methodName).Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("query").Op("*").Id(edgeQueryName),
		jen.Id("nodes").Index().Op("*").Id(t.Name),
		jen.Id("init").Func().Params(jen.Op("*").Id(t.Name)),
		jen.Id("assign").Func().Params(jen.Op("*").Id(t.Name), jen.Op("*").Id(edge.Type.Name)),
	).Error().BlockFunc(func(grp *jen.Group) {
		// Step 1: Collect all node IDs and create lookup map
		grp.Id("ids").Op(":=").Make(jen.Index().Qual("database/sql/driver", "Value"), jen.Lit(0), jen.Len(jen.Id("nodes")))
		// Use current entity's ID type (not hardcoded int)
		grp.Id("nodeids").Op(":=").Make(jen.Map(h.IDType(t)).Op("*").Id(t.Name))
		grp.For(jen.Id("i").Op(":=").Range().Id("nodes")).Block(
			jen.Id("ids").Op("=").Append(jen.Id("ids"), jen.Id("nodes").Index(jen.Id("i")).Dot("ID")),
			jen.Id("nodeids").Index(jen.Id("nodes").Index(jen.Id("i")).Dot("ID")).Op("=").Id("nodes").Index(jen.Id("i")),
			jen.If(jen.Id("init").Op("!=").Nil()).Block(
				jen.Id("init").Call(jen.Id("nodes").Index(jen.Id("i"))),
			),
		)
		grp.If(jen.Len(jen.Id("ids")).Op("==").Lit(0)).Block(
			jen.Return(jen.Nil()),
		)

		// Step 2: Query the join table to get relationships using EdgeQuerySpec
		// Use the correct ID types for both entities (not hardcoded int)
		grp.Id("edgeIDs").Op(":=").Make(jen.Index().Add(h.IDType(edge.Type)), jen.Lit(0))
		grp.Id("edges").Op(":=").Make(jen.Map(h.IDType(t)).Index().Add(h.IDType(edge.Type)))
		grp.Id("_spec").Op(":=").Op("&").Qual(h.SQLGraphPkg(), "EdgeQuerySpec").Values(
			jen.Dict{
				jen.Id("Edge"): jen.Op("&").Qual(h.SQLGraphPkg(), "EdgeSpec").Values(
					jen.Dict{
						jen.Id("Rel"):     jen.Qual(h.SQLGraphPkg(), "M2M"),
						jen.Id("Inverse"): jen.Lit(edge.IsInverse()),
						jen.Id("Table"):   jen.Lit(edge.Rel.Table),
						jen.Id("Columns"): jen.Index().String().Values(jen.Lit(ourColumn), jen.Lit(relatedColumn)),
					},
				),
				jen.Id("Predicate"): jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
					jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "InValues").Call(
						jen.Id("s").Dot("C").Call(jen.Lit(ourColumn)),
						jen.Id("ids").Op("..."),
					)),
				),
				jen.Id("ScanValues"): jen.Func().Params().Index(jen.Lit(2)).Any().Block(
					jen.Return(jen.Index(jen.Lit(2)).Any().Values(
						genIDScanType(t.ID),
						genIDScanType(edge.Type.ID),
					)),
				),
				jen.Id("Assign"): jen.Func().Params(jen.Id("out"), jen.Id("in").Any()).Error().BlockFunc(func(assignGrp *jen.Group) {
					// Convert the scanned values to the correct ID types
					genIDScanExtract(assignGrp, t.ID, "out", "outID")
					genIDScanExtract(assignGrp, edge.Type.ID, "in", "inID")
					// Track unique edge IDs and build the edges map
					assignGrp.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("edges").Index(jen.Id("outID")), jen.Op("!").Id("ok")).Block(
						jen.Id("edgeIDs").Op("=").Append(jen.Id("edgeIDs"), jen.Id("inID")),
					)
					assignGrp.Id("edges").Index(jen.Id("outID")).Op("=").Append(jen.Id("edges").Index(jen.Id("outID")), jen.Id("inID"))
					assignGrp.Return(jen.Nil())
				}),
			},
		)
		grp.If(jen.Id("err").Op(":=").Qual(h.SQLGraphPkg(), "QueryEdges").Call(
			jen.Id("ctx"),
			jen.Id(t.QueryReceiver()).Dot("driver"),
			jen.Id("_spec"),
		), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		)
		grp.If(jen.Len(jen.Id("edgeIDs")).Op("==").Lit(0)).Block(
			jen.Return(jen.Nil()),
		)

		// Step 3: Query the related entities
		grp.Id("query").Dot("Where").Call(
			idInPredicate(h, edge.Type, jen.Id("edgeIDs").Op("...")),
		)
		grp.List(jen.Id("neighbors"), jen.Id("err")).Op(":=").Id("query").Dot("All").Call(jen.Id("ctx"))
		grp.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		)

		// Step 4: Create lookup map for neighbors
		// Use the related entity's ID type (not hardcoded int)
		grp.Id("neighborMap").Op(":=").Make(jen.Map(h.IDType(edge.Type)).Op("*").Id(edge.Type.Name))
		grp.For(jen.List(jen.Id("_"), jen.Id("n")).Op(":=").Range().Id("neighbors")).Block(
			jen.Id("neighborMap").Index(jen.Id("n").Dot("ID")).Op("=").Id("n"),
		)

		// Step 5: Assign neighbors to nodes
		grp.For(jen.List(jen.Id("nodeID"), jen.Id("edgeIDs")).Op(":=").Range().Id("edges")).Block(
			jen.Id("node").Op(":=").Id("nodeids").Index(jen.Id("nodeID")),
			jen.For(jen.List(jen.Id("_"), jen.Id("edgeID")).Op(":=").Range().Id("edgeIDs")).Block(
				jen.If(jen.List(jen.Id("neighbor"), jen.Id("ok")).Op(":=").Id("neighborMap").Index(jen.Id("edgeID")), jen.Id("ok")).Block(
					jen.Id("assign").Call(jen.Id("node"), jen.Id("neighbor")),
				),
			),
		)

		grp.Return(jen.Nil())
	})
}

// genQueryLockMethods generates ForUpdate and ForShare methods for row-level locking.
// This is part of the sql/lock feature.
func genQueryLockMethods(h gen.GeneratorHelper, f *jen.File, t *gen.Type, queryName string) {
	dialectPkg := "github.com/syssam/velox/dialect"

	// ForUpdate method
	f.Comment("ForUpdate locks the selected rows against concurrent updates, and prevent them from being")
	f.Comment("updated, deleted or \"selected ... for update\" by other sessions, until the transaction is")
	f.Comment("either committed or rolled-back.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("ForUpdate").Params(
		jen.Id("opts").Op("...").Qual(h.SQLPkg(), "LockOption"),
	).Op("*").Id(queryName).Block(
		jen.If(jen.Id(t.QueryReceiver()).Dot("config").Dot("driver").Dot("Dialect").Call().Op("==").Qual(dialectPkg, "Postgres")).Block(
			jen.Id(t.QueryReceiver()).Dot("Unique").Call(jen.False()),
		),
		jen.Id(t.QueryReceiver()).Dot("modifiers").Op("=").Append(
			jen.Id(t.QueryReceiver()).Dot("modifiers"),
			jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
				jen.Id("s").Dot("ForUpdate").Call(jen.Id("opts").Op("...")),
			),
		),
		jen.Return(jen.Id(t.QueryReceiver())),
	)

	// ForShare method
	f.Comment("ForShare behaves similarly to ForUpdate, except that it acquires a shared mode lock")
	f.Comment("on any rows that are read. Other sessions can read the rows, but cannot modify them")
	f.Comment("until your transaction commits.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("ForShare").Params(
		jen.Id("opts").Op("...").Qual(h.SQLPkg(), "LockOption"),
	).Op("*").Id(queryName).Block(
		jen.If(jen.Id(t.QueryReceiver()).Dot("config").Dot("driver").Dot("Dialect").Call().Op("==").Qual(dialectPkg, "Postgres")).Block(
			jen.Id(t.QueryReceiver()).Dot("Unique").Call(jen.False()),
		),
		jen.Id(t.QueryReceiver()).Dot("modifiers").Op("=").Append(
			jen.Id(t.QueryReceiver()).Dot("modifiers"),
			jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
				jen.Id("s").Dot("ForShare").Call(jen.Id("opts").Op("...")),
			),
		),
		jen.Return(jen.Id(t.QueryReceiver())),
	)
}

// genQueryModifyMethod generates the Modify method for custom query modifiers.
// This is part of the sql/modifier feature.
func genQueryModifyMethod(h gen.GeneratorHelper, f *jen.File, t *gen.Type, queryName string) {
	selectName := t.Name + "Select"

	f.Comment("Modify adds a query modifier for attaching custom logic to queries.")
	f.Func().Params(jen.Id(t.QueryReceiver()).Op("*").Id(queryName)).Id("Modify").Params(
		jen.Id("modifiers").Op("...").Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")),
	).Op("*").Id(selectName).Block(
		jen.Id(t.QueryReceiver()).Dot("modifiers").Op("=").Append(jen.Id(t.QueryReceiver()).Dot("modifiers"), jen.Id("modifiers").Op("...")),
		jen.Return(jen.Id(t.QueryReceiver()).Dot("Select").Call()),
	)
}

// genSelectBuilder generates the Select and GroupBy builder structs and methods.
// In Ent, GroupBy is generated before Select.
func genSelectBuilder(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	// Generate GroupBy builder first (Ent generates GroupBy before Select)
	genGroupByBuilder(h, f, t)

	queryName := t.QueryName()
	selectName := t.Name + "Select"

	// Select struct - embeds the query builder and selector
	f.Commentf("%s is the builder for selecting fields of %s entities.", selectName, t.Name)
	f.Type().Id(selectName).Struct(
		jen.Op("*").Id(queryName),
		jen.Id("selector"),
	)

	// Aggregate method - adds aggregation functions
	f.Comment("Aggregate adds the given aggregation functions to the selector query.")
	f.Func().Params(jen.Id(t.SelectReceiver()).Op("*").Id(selectName)).Id("Aggregate").Params(
		jen.Id("fns").Op("...").Id("AggregateFunc"),
	).Op("*").Id(selectName).Block(
		jen.Id(t.SelectReceiver()).Dot("fns").Op("=").Append(jen.Id(t.SelectReceiver()).Dot("fns"), jen.Id("fns").Op("...")),
		jen.Return(jen.Id(t.SelectReceiver())),
	)

	// Scan method - scans the result into a value (uses embedded Query methods directly)
	f.Comment("Scan applies the selector query and scans the result into the given value.")
	f.Func().Params(jen.Id(t.SelectReceiver()).Op("*").Id(selectName)).Id("Scan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Error().Block(
		jen.Id("ctx").Op("=").Id("setContextOp").Call(jen.Id("ctx"), jen.Id(t.SelectReceiver()).Dot("ctx"), jen.Id("OpQuerySelect")),
		jen.If(jen.Id("err").Op(":=").Id(t.SelectReceiver()).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.Return(jen.Id("scanWithInterceptors").Types(jen.Op("*").Id(queryName), jen.Op("*").Id(selectName)).Call(
			jen.Id("ctx"),
			jen.Id(t.SelectReceiver()).Dot(queryName),
			jen.Id(t.SelectReceiver()),
			jen.Id(t.SelectReceiver()).Dot("inters"),
			jen.Id("v"),
		)),
	)

	// sqlScan - internal method for SQL scanning (matches Ent signature with root parameter)
	f.Func().Params(jen.Id(t.SelectReceiver()).Op("*").Id(selectName)).Id("sqlScan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("root").Op("*").Id(queryName),
		jen.Id("v").Any(),
	).Error().Block(
		jen.Id("selector").Op(":=").Id("root").Dot("sqlQuery").Call(jen.Id("ctx")),
		jen.Id("aggregation").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Len(jen.Id(t.SelectReceiver()).Dot("fns"))),
		jen.For(jen.List(jen.Id("_"), jen.Id("fn")).Op(":=").Range().Id(t.SelectReceiver()).Dot("fns")).Block(
			jen.Id("aggregation").Op("=").Append(jen.Id("aggregation"), jen.Id("fn").Call(jen.Id("selector"))),
		),
		jen.Switch(jen.Id("n").Op(":=").Len(jen.Op("*").Id(t.SelectReceiver()).Dot("selector").Dot("flds")), jen.Empty()).BlockFunc(func(grp *jen.Group) {
			grp.Case(jen.Id("n").Op("==").Lit(0).Op("&&").Len(jen.Id("aggregation")).Op(">").Lit(0)).Block(
				jen.Id("selector").Dot("Select").Call(jen.Id("aggregation").Op("...")),
			)
			grp.Case(jen.Id("n").Op("!=").Lit(0).Op("&&").Len(jen.Id("aggregation")).Op(">").Lit(0)).Block(
				jen.Id("selector").Dot("AppendSelect").Call(jen.Id("aggregation").Op("...")),
			)
		}),
		jen.Id("rows").Op(":=").Op("&").Qual(h.SQLPkg(), "Rows").Values(),
		jen.List(jen.Id("query"), jen.Id("args")).Op(":=").Id("selector").Dot("Query").Call(),
		jen.If(jen.Id("err").Op(":=").Id(t.SelectReceiver()).Dot("driver").Dot("Query").Call(
			jen.Id("ctx"), jen.Id("query"), jen.Id("args"), jen.Id("rows"),
		), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.Defer().Id("rows").Dot("Close").Call(),
		jen.Return(jen.Qual(h.SQLPkg(), "ScanSlice").Call(jen.Id("rows"), jen.Id("v"))),
	)

	// Note: Strings, StringsX, String, StringX, Ints, IntsX, Int, IntX, Float64s, Float64sX, Float64, Float64X,
	// Bools, BoolsX, Bool, BoolX, and ScanX methods are inherited from the embedded `selector` struct in velox.go

	// Modify method - adds query modifiers
	f.Comment("Modify adds a query modifier for attaching custom logic to queries.")
	f.Func().Params(jen.Id(t.SelectReceiver()).Op("*").Id(selectName)).Id("Modify").Params(
		jen.Id("modifiers").Op("...").Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")),
	).Op("*").Id(selectName).Block(
		jen.Id(t.SelectReceiver()).Dot("modifiers").Op("=").Append(jen.Id(t.SelectReceiver()).Dot("modifiers"), jen.Id("modifiers").Op("...")),
		jen.Return(jen.Id(t.SelectReceiver())),
	)
}

// genGroupByBuilder generates the GroupBy builder struct and methods.
func genGroupByBuilder(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	queryName := t.QueryName()
	groupByName := t.Name + "GroupBy"

	// GroupBy struct - embeds selector for common functionality
	f.Commentf("%s is the group-by builder for %s entities.", groupByName, t.Name)
	f.Type().Id(groupByName).Struct(
		jen.Id("selector"),
		jen.Id("build").Op("*").Id(queryName),
	)

	// Aggregate method - adds aggregation functions
	f.Comment("Aggregate adds the given aggregation functions to the group-by query.")
	f.Func().Params(jen.Id("_gb").Op("*").Id(groupByName)).Id("Aggregate").Params(
		jen.Id("fns").Op("...").Id("AggregateFunc"),
	).Op("*").Id(groupByName).Block(
		jen.Id("_gb").Dot("fns").Op("=").Append(jen.Id("_gb").Dot("fns"), jen.Id("fns").Op("...")),
		jen.Return(jen.Id("_gb")),
	)

	// Scan method - applies the selector query and scans the result
	f.Comment("Scan applies the selector query and scans the result into the given value.")
	f.Func().Params(jen.Id("_gb").Op("*").Id(groupByName)).Id("Scan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Error().Block(
		jen.Id("ctx").Op("=").Id("setContextOp").Call(jen.Id("ctx"), jen.Id("_gb").Dot("build").Dot("ctx"), jen.Id("OpQueryGroupBy")),
		jen.If(jen.Id("err").Op(":=").Id("_gb").Dot("build").Dot("prepareQuery").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.Return(jen.Id("scanWithInterceptors").Types(jen.Op("*").Id(queryName), jen.Op("*").Id(groupByName)).Call(
			jen.Id("ctx"),
			jen.Id("_gb").Dot("build"),
			jen.Id("_gb"),
			jen.Id("_gb").Dot("build").Dot("inters"),
			jen.Id("v"),
		)),
	)

	// sqlScan - internal method for SQL scanning in GroupBy
	f.Func().Params(jen.Id("_gb").Op("*").Id(groupByName)).Id("sqlScan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("root").Op("*").Id(queryName),
		jen.Id("v").Any(),
	).Error().Block(
		jen.Comment("Build the SQL selector from the query"),
		jen.Id("selector").Op(":=").Id("root").Dot("sqlQuery").Call(jen.Id("ctx")).Dot("Select").Call(),
		jen.Line(),
		jen.Comment("Apply aggregation functions"),
		jen.Id("aggregation").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Len(jen.Id("_gb").Dot("fns"))),
		jen.For(jen.List(jen.Id("_"), jen.Id("fn")).Op(":=").Range().Id("_gb").Dot("fns")).Block(
			jen.Id("aggregation").Op("=").Append(jen.Id("aggregation"), jen.Id("fn").Call(jen.Id("selector"))),
		),
		jen.Line(),
		jen.Comment("Build column list: groupby fields + aggregations"),
		jen.If(jen.Len(jen.Id("selector").Dot("SelectedColumns").Call()).Op("==").Lit(0)).Block(
			jen.Id("columns").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Len(jen.Op("*").Id("_gb").Dot("flds")).Op("+").Len(jen.Id("_gb").Dot("fns"))),
			jen.For(jen.List(jen.Id("_"), jen.Id("f")).Op(":=").Range().Op("*").Id("_gb").Dot("flds")).Block(
				jen.Id("columns").Op("=").Append(jen.Id("columns"), jen.Id("selector").Dot("C").Call(jen.Id("f"))),
			),
			jen.Id("columns").Op("=").Append(jen.Id("columns"), jen.Id("aggregation").Op("...")),
			jen.Id("selector").Dot("Select").Call(jen.Id("columns").Op("...")),
		),
		jen.Line(),
		jen.Comment("Apply GROUP BY clause"),
		jen.Id("selector").Dot("GroupBy").Call(jen.Id("selector").Dot("Columns").Call(jen.Op("*").Id("_gb").Dot("flds").Op("...")).Op("...")),
		jen.Line(),
		jen.If(jen.Id("err").Op(":=").Id("selector").Dot("Err").Call(), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.Line(),
		jen.Comment("Execute query"),
		jen.Id("rows").Op(":=").Op("&").Qual(h.SQLPkg(), "Rows").Values(),
		jen.List(jen.Id("query"), jen.Id("args")).Op(":=").Id("selector").Dot("Query").Call(),
		jen.If(jen.Id("err").Op(":=").Id("_gb").Dot("build").Dot("driver").Dot("Query").Call(jen.Id("ctx"), jen.Id("query"), jen.Id("args"), jen.Id("rows")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.Defer().Id("rows").Dot("Close").Call(),
		jen.Line(),
		jen.Comment("Scan results"),
		jen.Return(jen.Qual(h.SQLPkg(), "ScanSlice").Call(jen.Id("rows"), jen.Id("v"))),
	)

	// Note: Strings, StringsX, String, StringX, Ints, IntsX, Int, IntX, Float64s, Float64sX, Float64, Float64X,
	// Bools, BoolsX, Bool, BoolX, and ScanX methods are inherited from the embedded `selector` struct in velox.go
}

// genIDScanType generates the Jennifer code for creating a new scan type for an ID field.
// The scan type is used by sql.Rows.Scan to scan values from the database.
func genIDScanType(f *gen.Field) jen.Code {
	if f == nil {
		return jen.Qual("database/sql", "NullInt64").Values()
	}
	// Handle custom ValueScanner types (like UUID)
	if f.Type.ValueScanner() {
		expr := jen.New(jen.Id(f.Type.RType.String()))
		if f.Nillable {
			return jen.Op("&").Qual("database/sql", "NullScanner").Values(
				jen.Dict{jen.Id("S"): expr},
			)
		}
		return expr
	}
	// Handle standard types based on field type
	switch f.Type.Type {
	case field.TypeString, field.TypeEnum:
		return jen.New(jen.Qual("database/sql", "NullString"))
	case field.TypeBool:
		return jen.New(jen.Qual("database/sql", "NullBool"))
	case field.TypeTime:
		return jen.New(jen.Qual("database/sql", "NullTime"))
	case field.TypeFloat32, field.TypeFloat64:
		return jen.New(jen.Qual("database/sql", "NullFloat64"))
	case field.TypeBytes:
		return jen.New(jen.Index().Byte())
	default:
		// int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64
		return jen.New(jen.Qual("database/sql", "NullInt64"))
	}
}

// genIDScanExtract generates the code to extract the value from a scanned type and assign it to a variable.
// It handles type assertions and conversions based on the field type.
func genIDScanExtract(grp *jen.Group, f *gen.Field, srcVar, dstVar string) {
	if f == nil {
		// Default to int
		grp.Id(dstVar).Op(":=").Int().Call(
			jen.Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullInt64")).Dot("Int64"),
		)
		return
	}
	// Handle custom ValueScanner types (like UUID)
	if f.Type.ValueScanner() {
		if f.Type.RType.IsPtr() {
			grp.Id(dstVar).Op(":=").Op("*").Id(srcVar).Dot("").Parens(jen.Op("*").Id(f.Type.RType.String()))
		} else {
			grp.Id(dstVar).Op(":=").Op("*").Id(srcVar).Dot("").Parens(jen.Op("*").Id(f.Type.RType.String()))
		}
		return
	}
	// Handle standard types
	switch f.Type.Type {
	case field.TypeString:
		grp.Id(dstVar).Op(":=").Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullString")).Dot("String")
	case field.TypeEnum:
		grp.Id(dstVar).Op(":=").Id(f.Type.String()).Call(
			jen.Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullString")).Dot("String"),
		)
	case field.TypeBool:
		grp.Id(dstVar).Op(":=").Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullBool")).Dot("Bool")
	case field.TypeTime:
		grp.Id(dstVar).Op(":=").Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullTime")).Dot("Time")
	case field.TypeFloat64:
		grp.Id(dstVar).Op(":=").Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullFloat64")).Dot("Float64")
	case field.TypeFloat32:
		grp.Id(dstVar).Op(":=").Id("float32").Call(
			jen.Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullFloat64")).Dot("Float64"),
		)
	case field.TypeInt64:
		grp.Id(dstVar).Op(":=").Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullInt64")).Dot("Int64")
	case field.TypeInt:
		grp.Id(dstVar).Op(":=").Int().Call(
			jen.Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullInt64")).Dot("Int64"),
		)
	case field.TypeInt8:
		grp.Id(dstVar).Op(":=").Int8().Call(
			jen.Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullInt64")).Dot("Int64"),
		)
	case field.TypeInt16:
		grp.Id(dstVar).Op(":=").Int16().Call(
			jen.Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullInt64")).Dot("Int64"),
		)
	case field.TypeInt32:
		grp.Id(dstVar).Op(":=").Int32().Call(
			jen.Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullInt64")).Dot("Int64"),
		)
	case field.TypeUint:
		grp.Id(dstVar).Op(":=").Uint().Call(
			jen.Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullInt64")).Dot("Int64"),
		)
	case field.TypeUint8:
		grp.Id(dstVar).Op(":=").Uint8().Call(
			jen.Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullInt64")).Dot("Int64"),
		)
	case field.TypeUint16:
		grp.Id(dstVar).Op(":=").Uint16().Call(
			jen.Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullInt64")).Dot("Int64"),
		)
	case field.TypeUint32:
		grp.Id(dstVar).Op(":=").Uint32().Call(
			jen.Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullInt64")).Dot("Int64"),
		)
	case field.TypeUint64:
		grp.Id(dstVar).Op(":=").Uint64().Call(
			jen.Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullInt64")).Dot("Int64"),
		)
	default:
		// Default to int for any other types
		grp.Id(dstVar).Op(":=").Int().Call(
			jen.Id(srcVar).Dot("").Parens(jen.Op("*").Qual("database/sql", "NullInt64")).Dot("Int64"),
		)
	}
}
