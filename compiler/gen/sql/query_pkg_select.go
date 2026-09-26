package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// QueryReader, Select/GroupBy and clone sections of genQueryPkg. See
// queryGen in query_pkg.go.

// genQueryReader emits the Get* getters that implement runtime.QueryReader.
func (qg *queryGen) genQueryReader() {
	// =========================================================================
	// QueryReader getters — implement runtime.QueryReader interface
	// =========================================================================

	qg.f.Comment("GetDriver returns the dialect driver. Implements runtime.QueryReader.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("GetDriver").Params().Qual(dialectPkg(), "Driver").Block(
		jen.Return(jen.Id(qg.recv).Dot("config").Dot("Driver")),
	)

	qg.f.Comment("GetTable returns the primary table name. Implements runtime.QueryReader.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("GetTable").Params().String().Block(
		jen.Return(jen.Qual(qg.entitySubPkg, "Table")),
	)

	qg.f.Comment("GetColumns returns the default column list. Implements runtime.QueryReader.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("GetColumns").Params().Index().String().Block(
		jen.Return(jen.Qual(qg.entitySubPkg, "Columns")),
	)

	qg.f.Comment("GetFKColumns returns the foreign-key columns. Implements runtime.QueryReader.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("GetFKColumns").Params().Index().String().Block(
		jen.Return(jen.Qual(qg.entitySubPkg, "ForeignKeys")),
	)

	qg.f.Comment("GetIDFieldType returns the schema type of the ID field. Implements runtime.QueryReader.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("GetIDFieldType").Params().Qual(schemaPkg(), "Type").Block(
		jen.Return(jen.Qual(schemaPkg(), qg.t.ID.Type.ConstName())),
	)

	qg.f.Comment("GetPath returns the graph-traversal path function. Implements runtime.QueryReader.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("GetPath").Params().Func().Params(
		jen.Qual("context", "Context"),
	).Params(jen.Op("*").Qual(qg.sqlPkg, "Selector"), jen.Error()).Block(
		jen.Return(jen.Id(qg.recv).Dot("path")),
	)

	qg.f.Comment("GetPredicates returns the registered WHERE predicates. Implements runtime.QueryReader.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("GetPredicates").Params().Index().Func().Params(
		jen.Op("*").Qual(qg.sqlPkg, "Selector"),
	).Block(
		jen.Return(jen.Id(qg.recv).Dot("predicates")),
	)

	qg.f.Comment("GetOrder returns the registered ORDER BY functions. Implements runtime.QueryReader.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("GetOrder").Params().Index().Func().Params(
		jen.Op("*").Qual(qg.sqlPkg, "Selector"),
	).Block(
		jen.Return(jen.Id(qg.recv).Dot("order")),
	)

	qg.f.Comment("GetModifiers returns the registered query modifiers. Implements runtime.QueryReader.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("GetModifiers").Params().Index().Func().Params(
		jen.Op("*").Qual(qg.sqlPkg, "Selector"),
	).Block(
		jen.Return(jen.Id(qg.recv).Dot("modifiers")),
	)

	qg.f.Comment("GetWithFKs returns whether FK columns should be included. Implements runtime.QueryReader.")
	withFKs := jen.False()
	if qg.hasFKs {
		withFKs = jen.Id(qg.recv).Dot("withFKs")
	}
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("GetWithFKs").Params().Bool().Block(
		jen.Return(withFKs),
	)
}

// genSelectEntry emits the entry points into projection and aggregation:
// Select, Modify, Aggregate, GroupBy, Scan and ScanX.
func (qg *queryGen) genSelectEntry() {
	// =========================================================================
	// Select — returns *UserSelect which embeds runtime.Selector for scalar methods
	// =========================================================================

	qg.f.Commentf("Select allows the selection of one or more fields/columns for the given query,")
	qg.f.Commentf("returning a %s builder with scalar accessor methods (Strings, Ints, etc.).", qg.selectName)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Select").Params(
		jen.Id("fields").Op("...").String(),
	).Qual(qg.entityPkgPath, qg.t.Name+"Selector").Block(
		jen.Id(qg.recv).Dot("ctx").Dot("Fields").Op("=").Append(
			jen.Id(qg.recv).Dot("ctx").Dot("Fields"),
			jen.Id("fields").Op("..."),
		),
		jen.Id("s").Op(":=").Op("&").Id(qg.selectName).Values(jen.Dict{
			jen.Id(qg.queryName): jen.Id(qg.recv),
		}),
		// Wire the public Scan method (which runs through the
		// interceptor chain), NOT the raw sqlScan helper. This is what
		// makes .Strings() / .Ints() / .Int() / etc. honor interceptors
		// — all of those runtime.Selector terminal methods call the
		// scan function stored here.
		jen.Id("s").Dot("Selector").Op("=").Qual(runtimePkg, "NewSelector").Call(
			jen.Lit(qg.selectName),
			jen.Op("&").Id(qg.recv).Dot("ctx").Dot("Fields"),
			jen.Id("s").Dot("Scan"),
		),
		jen.Return(jen.Id("s")),
	)

	// =========================================================================
	// Modify — adds query modifier for attaching custom logic to queries
	// =========================================================================

	qg.f.Commentf("Modify adds a query modifier for attaching custom logic to queries.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Modify").Params(
		jen.Id("modifiers").Op("...").Func().Params(jen.Op("*").Qual(qg.sqlPkg, "Selector")),
	).Qual(qg.entityPkgPath, qg.querierIface).Block(
		jen.Id(qg.recv).Dot("modifiers").Op("=").Append(jen.Id(qg.recv).Dot("modifiers"), jen.Id("modifiers").Op("...")),
		jen.Return(jen.Id(qg.recv)),
	)

	// =========================================================================
	// Aggregate without GroupBy — routes through Select, matching Ent's API
	// =========================================================================

	qg.f.Commentf("Aggregate returns a %s configured with the given aggregations.", qg.selectName)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Aggregate").Params(
		jen.Id("fns").Op("...").Qual(runtimePkg, "AggregateFunc"),
	).Qual(qg.entityPkgPath, qg.t.Name+"Selector").Block(
		jen.Return(jen.Id(qg.recv).Dot("Select").Call().Dot("Aggregate").Call(jen.Id("fns").Op("..."))),
	)

	// =========================================================================
	// GroupBy — groups vertices by one or more fields/columns
	// =========================================================================

	qg.f.Commentf("GroupBy is used to group vertices by one or more fields/columns.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("GroupBy").Params(
		jen.Id("field").String(),
		jen.Id("fields").Op("...").String(),
	).Qual(qg.entityPkgPath, qg.t.Name+"GroupByer").Block(
		jen.Id("g").Op(":=").Op("&").Id(qg.gbName).Values(jen.Dict{
			jen.Id("build"):  jen.Id(qg.recv),
			jen.Id("fields"): jen.Append(jen.Index().String().Values(jen.Id("field")), jen.Id("fields").Op("...")),
		}),
		// Wire the public Scan method (which runs through the
		// interceptor chain), NOT the raw sqlScan helper. Same
		// rationale as UserSelect above.
		jen.Id("g").Dot("Selector").Op("=").Qual(runtimePkg, "NewSelector").Call(
			jen.Lit(qg.gbName),
			jen.Op("&").Id("g").Dot("fields"),
			jen.Id("g").Dot("Scan"),
		),
		jen.Return(jen.Id("g")),
	)

	// =========================================================================
	// Scan / ScanX — direct scan without Select
	// =========================================================================

	qg.f.Comment("Scan applies the selector query and scans the result into the given value.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Scan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Error().BlockFunc(func(body *jen.Group) {
		body.If(jen.Err().Op(":=").Id(qg.recv).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		)
		qg.schemaConfigCtx(body, jen.Id(qg.recv))
		body.Return(jen.Qual(runtimePkg, "QueryScan").Call(
			jen.Id("ctx"), jen.Id(qg.recv), jen.Id("v"),
		))
	})

	qg.f.Comment("ScanX is like Scan, but panics if an error occurs.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("ScanX").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Block(
		jen.If(jen.Err().Op(":=").Id(qg.recv).Dot("Scan").Call(jen.Id("ctx"), jen.Id("v")), jen.Err().Op("!=").Nil()).Block(
			jen.Panic(jen.Err()),
		),
	)
}

// genSelectType emits the XxxSelect type with Aggregate, sqlScan, Scan and
// ScanX.
func (qg *queryGen) genSelectType() {
	// =========================================================================
	// UserSelect type and methods
	// =========================================================================

	// The query is a NAMED field, not an embedded one. Embedding *XxxQuery
	// made the compiler emit a promoted-method wrapper for every one of
	// its ~58 methods per entity (about a third of all functions in the
	// query package at scale), while the public entity.XxxSelector
	// interface needs only the 13 terminals forwarded below. The field
	// keeps the query's type name so composite literals and s.XxxQuery
	// accesses read the same as before. Pinned by TestSelectForwardsInsteadOfEmbedding.
	qg.f.Commentf("%s is the builder for selecting fields of %s entities.", qg.selectName, qg.t.Name)
	qg.f.Type().Id(qg.selectName).Struct(
		jen.Id(qg.queryName).Op("*").Id(qg.queryName),
		jen.Qual(runtimePkg, "Selector"),
	)

	// Forwarders for the query terminals the Selector interface exposes
	// (Ent's `Select(...).All(ctx)` shape).
	ctxParam := jen.Id("ctx").Qual("context", "Context")
	fwd := func(name string, results ...jen.Code) {
		qg.f.Commentf("%s forwards to the underlying %s.", name, qg.queryName)
		qg.f.Func().Params(jen.Id("s").Op("*").Id(qg.selectName)).Id(name).Params(ctxParam).Params(results...).Block(
			jen.Return(jen.Id("s").Dot(qg.queryName).Dot(name).Call(jen.Id("ctx"))),
		)
	}
	fwd("All", jen.Index().Op("*").Add(qg.entityType()), jen.Error())
	fwd("AllX", jen.Index().Op("*").Add(qg.entityType()))
	fwd("First", jen.Op("*").Add(qg.entityType()), jen.Error())
	fwd("FirstX", jen.Op("*").Add(qg.entityType()))
	fwd("Only", jen.Op("*").Add(qg.entityType()), jen.Error())
	fwd("OnlyX", jen.Op("*").Add(qg.entityType()))
	fwd("Count", jen.Int(), jen.Error())
	fwd("CountX", jen.Int())
	fwd("Exist", jen.Bool(), jen.Error())
	fwd("ExistX", jen.Bool())
	fwd("IDs", jen.Index().Add(qg.idType), jen.Error())
	fwd("FirstID", jen.Add(qg.idType), jen.Error())
	fwd("OnlyID", jen.Add(qg.idType), jen.Error())

	qg.f.Comment("Aggregate adds the given aggregation functions to the selector query.")
	qg.f.Func().Params(jen.Id("s").Op("*").Id(qg.selectName)).Id("Aggregate").Params(
		jen.Id("fns").Op("...").Qual(runtimePkg, "AggregateFunc"),
	).Qual(qg.entityPkgPath, qg.t.Name+"Selector").Block(
		jen.Id("s").Dot("AppendFns").Call(jen.Id("fns").Op("...")),
		jen.Return(jen.Id("s")),
	)

	// sqlScan for UserSelect — raw SQL execution that sqlScan is
	// wired to via runtime.Selector.Scan. Uses QuerySelect (not
	// QueryScan) to honor aggregate functions registered via
	// Aggregate(...). When no aggregates are present, QuerySelect
	// behaves identically to QueryScan.
	qg.f.Func().Params(jen.Id("s").Op("*").Id(qg.selectName)).Id("sqlScan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Error().BlockFunc(func(body *jen.Group) {
		qg.schemaConfigCtx(body, jen.Id("s").Dot(qg.queryName))
		body.Return(jen.Qual(runtimePkg, "QuerySelect").Call(
			jen.Id("ctx"),
			jen.Id("s").Dot(qg.queryName),
			jen.Id("s").Dot("Fns").Call(),
			jen.Id("v"),
		))
	})

	// Scan and ScanX on *XxxSelect — explicit methods so the call is
	// not the promoted runtime.Selector one. Scan threads the call through the
	// parent UserQuery's interceptor chain before running sqlScan
	// so client.Intercept() fires on .Strings() / .Int() / etc.
	// Uses runtime.ScanWithInterceptors to avoid per-entity boilerplate.
	qg.f.Comment("Scan applies the selector query and scans the result into the given value.")
	qg.f.Func().Params(jen.Id("s").Op("*").Id(qg.selectName)).Id("Scan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Error().BlockFunc(func(body *jen.Group) {
		body.Id("ctx").Op("=").Id("setContextOp").Call(
			jen.Id("ctx"), jen.Id("s").Dot(qg.queryName).Dot("ctx"), jen.Qual(qg.h.VeloxPkg(), "OpQuerySelect"),
		)
		body.If(jen.Err().Op(":=").Id("s").Dot(qg.queryName).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Err()),
		)
		body.Return(jen.Qual(runtimePkg, "ScanWithInterceptors").Call(
			jen.Id("ctx"),
			jen.Id("s").Dot(qg.queryName),
			qg.selectInters(jen.Id("s").Dot(qg.queryName)),
			jen.Id("s").Dot("sqlScan"),
			jen.Id("v"),
		))
	})

	qg.f.Comment("ScanX is like Scan, but panics if an error occurs.")
	qg.f.Func().Params(jen.Id("s").Op("*").Id(qg.selectName)).Id("ScanX").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Block(
		jen.If(jen.Err().Op(":=").Id("s").Dot("Scan").Call(jen.Id("ctx"), jen.Id("v")), jen.Err().Op("!=").Nil()).Block(
			jen.Panic(jen.Err()),
		),
	)
}

// genGroupByType emits the XxxGroupBy type with Aggregate, Scan and sqlScan.
func (qg *queryGen) genGroupByType() {
	// =========================================================================
	// UserGroupBy type and methods
	// =========================================================================

	qg.f.Commentf("%s is the group-by builder for %s entities.", qg.gbName, qg.t.Name)
	qg.f.Type().Id(qg.gbName).StructFunc(func(group *jen.Group) {
		group.Qual(runtimePkg, "Selector")
		group.Id("build").Op("*").Id(qg.queryName)
		group.Id("fields").Index().String()
	})

	qg.f.Comment("Aggregate adds the given aggregation functions to the group-by query.")
	qg.f.Func().Params(jen.Id("g").Op("*").Id(qg.gbName)).Id("Aggregate").Params(
		jen.Id("fns").Op("...").Qual(runtimePkg, "AggregateFunc"),
	).Qual(qg.entityPkgPath, qg.t.Name+"GroupByer").Block(
		jen.Id("g").Dot("AppendFns").Call(jen.Id("fns").Op("...")),
		jen.Return(jen.Id("g")),
	)

	// Scan applies the group-by query and scans the result into the given value.
	// Uses runtime.ScanWithInterceptors to avoid per-entity boilerplate.
	qg.f.Comment("Scan applies the group-by query and scans the result into the given value.")
	qg.f.Func().Params(jen.Id("g").Op("*").Id(qg.gbName)).Id("Scan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Error().BlockFunc(func(body *jen.Group) {
		body.Id("ctx").Op("=").Id("setContextOp").Call(
			jen.Id("ctx"), jen.Id("g").Dot("build").Dot("ctx"), jen.Qual(qg.h.VeloxPkg(), "OpQueryGroupBy"),
		)
		qg.genValidFieldsCheck(body, jen.Id("g").Dot("fields"))
		body.If(jen.Id("g").Dot("build").Op("==").Nil()).Block(
			jen.Return(jen.Id("g").Dot("sqlScan").Call(jen.Id("ctx"), jen.Id("v"))),
		)
		body.If(jen.Err().Op(":=").Id("g").Dot("build").Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Err()),
		)
		body.Return(jen.Qual(runtimePkg, "ScanWithInterceptors").Call(
			jen.Id("ctx"),
			jen.Id("g").Dot("build"),
			qg.selectInters(jen.Id("g").Dot("build")),
			jen.Id("g").Dot("sqlScan"),
			jen.Id("v"),
		))
	})

	// sqlScan for GroupBy — wired as Selector.scan
	qg.f.Func().Params(jen.Id("g").Op("*").Id(qg.gbName)).Id("sqlScan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Error().BlockFunc(func(body *jen.Group) {
		qg.schemaConfigCtx(body, jen.Id("g").Dot("build"))
		body.Return(jen.Qual(runtimePkg, "QueryGroupBy").Call(
			jen.Id("ctx"),
			jen.Id("g").Dot("build"),
			jen.Id("g").Dot("fields"),
			jen.Id("g").Dot("Fns").Call(),
			jen.Id("v"),
		))
	})
}

// genClonePrivate emits clone(), which must copy every typed field —
// pinned by TestQueryCloneCopiesEveryField.
func (qg *queryGen) genClonePrivate() {
	// =========================================================================
	// clone (private, returns concrete type for First/Only)
	// =========================================================================

	qg.f.Commentf("clone returns a concrete clone of the %s for internal use by First/Only.", qg.queryName)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("clone").Params().Op("*").Id(qg.queryName).BlockFunc(func(body *jen.Group) {
		body.If(jen.Id(qg.recv).Op("==").Nil()).Block(
			jen.Return(jen.Nil()),
		)
		cloneDict := jen.Dict{
			jen.Id("config"):     jen.Id(qg.recv).Dot("config"),
			jen.Id("ctx"):        jen.Id(qg.recv).Dot("ctx").Dot("Clone").Call(),
			jen.Id("predicates"): jen.Qual(runtimePkg, "CloneSlice").Call(jen.Id(qg.recv).Dot("predicates")),
			jen.Id("order"):      jen.Qual(runtimePkg, "CloneSlice").Call(jen.Id(qg.recv).Dot("order")),
			jen.Id("modifiers"):  jen.Qual(runtimePkg, "CloneSlice").Call(jen.Id(qg.recv).Dot("modifiers")),
			jen.Id("path"):       jen.Id(qg.recv).Dot("path"),
			// SP-2: pointer copy of the shared *entity.InterceptorStore.
			// Without this, clone()s lose all client-level interceptors
			// and prepareQuery nil-derefs on q.inters.<EntityName>.
			jen.Id("inters"): jen.Id(qg.recv).Dot("inters"),
		}
		if qg.schemaConfigEnabled {
			cloneDict[jen.Id("schemaConfig")] = jen.Id(qg.recv).Dot("schemaConfig")
		}
		if qg.hasFKs {
			cloneDict[jen.Id("withFKs")] = jen.Id(qg.recv).Dot("withFKs")
		}
		// Policy must survive clone — First/Only/FirstID/OnlyID/Exist all
		// call q.clone().IDs(ctx) which re-enters prepareQuery; without
		// this, q.policy is nil in the clone and tenant/privacy filters
		// are silently skipped.
		if qg.hasPolicy {
			cloneDict[jen.Id("policy")] = jen.Id(qg.recv).Dot("policy")
		}
		// Copy edge pointers (deep-cloned, like Ent)
		for _, edge := range qg.t.Edges {
			field := edgeCallbackField(edge)
			cloneDict[jen.Id(field)] = jen.Id(qg.recv).Dot(field).Dot("clone").Call()
		}
		body.Id("c").Op(":=").Op("&").Id(qg.queryName).Values(cloneDict)
		// Copy named edge maps (deep clone each query).
		if qg.h.FeatureEnabled(gen.FeatureNamedEdges.Name) {
			for _, edge := range qg.t.Edges {
				if edge.Unique {
					continue
				}
				namedField := "withNamed" + edge.StructField()
				targetQueryName := edge.Type.Name + "Query"
				body.If(jen.Id(qg.recv).Dot(namedField).Op("!=").Nil()).Block(
					jen.Id("c").Dot(namedField).Op("=").Make(jen.Map(jen.String()).Op("*").Id(targetQueryName), jen.Len(jen.Id(qg.recv).Dot(namedField))),
					jen.For(jen.List(jen.Id("name"), jen.Id("q")).Op(":=").Range().Id(qg.recv).Dot(namedField)).Block(
						jen.Id("c").Dot(namedField).Index(jen.Id("name")).Op("=").Id("q").Dot("clone").Call(),
					),
				)
			}
		}
		body.Return(jen.Id("c"))
	})
}
