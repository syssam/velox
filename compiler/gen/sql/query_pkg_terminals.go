package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// Terminal-method sections of genQueryPkg. See queryGen in query_pkg.go.

// genSQLAll emits sqlAll (scan, then eagerLoad) and eagerLoad: three phases (standard
// edges, named edges, loadTotal hooks), then config injection.
func (qg *queryGen) genSQLAll() {
	// =========================================================================
	// Terminal methods
	// =========================================================================

	// sqlAll — actual scan + edge loading logic, extracted for interceptor support.
	qg.f.Commentf("sqlAll executes the SQL query and returns scanned %s entities.", qg.t.Name)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("sqlAll").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Op("*").Add(qg.entityType()), jen.Error()).BlockFunc(func(allBody *jen.Group) {
		// Push SchemaConfig into context so where predicates can read it.
		if qg.schemaConfigEnabled {
			allBody.Id("ctx").Op("=").Qual(qg.h.InternalPkg(), "NewSchemaConfigContext").Call(
				jen.Id("ctx"),
				jen.Id(qg.recv).Dot("schemaConfig"),
			)
		}
		allBody.List(jen.Id("nodes"), jen.Err()).Op(":=").Qual(runtimePkg, "ScanAll").Types(
			qg.entityType(), jen.Op("*").Add(qg.entityType()),
		).Call(
			jen.Id("ctx"), jen.Id(qg.recv).Dot("config").Dot("Driver"), jen.Id(qg.recv).Dot("buildSelector"),
		)
		allBody.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)
		allBody.If(jen.Len(jen.Id("nodes")).Op("==").Lit(0)).Block(
			jen.Return(jen.Id("nodes"), jen.Nil()),
		)
		allBody.If(jen.Err().Op(":=").Id(qg.recv).Dot("eagerLoad").Call(jen.Id("ctx"), jen.Id("nodes")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)
		allBody.Return(jen.Id("nodes"), jen.Nil())
	})

	// eagerLoad — the edge-loading half of sqlAll. The M2M loader scans its
	// join rows itself and calls it on the targets, so edges loaded under a
	// many-to-many edge are loaded too (Ent runs sqlAll there).
	qg.f.Comment("eagerLoad loads the edges this query was asked to eager-load into nodes")
	qg.f.Comment("and injects the runtime config. sqlAll calls it after scanning; the")
	qg.f.Comment("many-to-many loaders of other queries call it on the rows they scan.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("eagerLoad").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("nodes").Index().Op("*").Add(qg.entityType()),
	).Error().BlockFunc(func(allBody *jen.Group) {
		// Phase 1 — Standard eager loading.
		for _, edge := range qg.t.Edges {
			edgeField := edge.StructField()
			callbackField := edgeCallbackField(edge)
			loaderName := "load" + edgeField
			targetEntityType := func() *jen.Statement { return jen.Qual(qg.entityPkgPath, edge.Type.Name) }

			allBody.If(jen.Id("query").Op(":=").Id(qg.recv).Dot(callbackField), jen.Id("query").Op("!=").Nil()).BlockFunc(func(ifBody *jen.Group) {
				// init callback
				var initFn *jen.Statement
				if edge.Unique {
					// O2O/M2O: just mark loaded.
					initFn = jen.Func().Params(jen.Id("n").Op("*").Add(qg.entityType())).Block(
						jen.Id("n").Dot("Edges").Dot("Mark" + edgeField + "Loaded").Call(),
					)
				} else {
					// O2M/M2M: init empty slice + mark loaded.
					initFn = jen.Func().Params(jen.Id("n").Op("*").Add(qg.entityType())).Block(
						jen.Id("n").Dot("Edges").Dot(edgeField).Op("=").Index().Op("*").Add(targetEntityType()).Values(),
						jen.Id("n").Dot("Edges").Dot("Mark"+edgeField+"Loaded").Call(),
					)
				}

				// assign callback
				var assignFn *jen.Statement
				if edge.Unique {
					assignFn = jen.Func().Params(
						jen.Id("n").Op("*").Add(qg.entityType()),
						jen.Id("e").Op("*").Add(targetEntityType()),
					).BlockFunc(func(fnBody *jen.Group) {
						fnBody.Id("n").Dot("Edges").Dot(edgeField).Op("=").Id("e")
						qg.genBackRef(fnBody, edge)
					})
				} else {
					assignFn = jen.Func().Params(
						jen.Id("n").Op("*").Add(qg.entityType()),
						jen.Id("e").Op("*").Add(targetEntityType()),
					).BlockFunc(func(fnBody *jen.Group) {
						fnBody.Id("n").Dot("Edges").Dot(edgeField).Op("=").Append(
							jen.Id("n").Dot("Edges").Dot(edgeField), jen.Id("e"),
						)
						qg.genBackRef(fnBody, edge)
					})
				}

				ifBody.If(
					jen.Err().Op(":=").Id(qg.recv).Dot(loaderName).Call(
						jen.Id("ctx"), jen.Id("query"), jen.Id("nodes"),
						initFn, assignFn,
					),
					jen.Err().Op("!=").Nil(),
				).Block(
					jen.Return(jen.Err()),
				)
			})
		}

		// Phase 2 — Named edge variants (FeatureNamedEdges).
		if qg.namedEdgesEnabled {
			for _, edge := range qg.t.Edges {
				if edge.Unique {
					continue
				}
				edgeField := edge.StructField()
				loaderName := "load" + edgeField
				namedField := "withNamed" + edgeField
				targetEntityType := func() *jen.Statement { return jen.Qual(qg.entityPkgPath, edge.Type.Name) }

				allBody.For(
					jen.List(jen.Id("name"), jen.Id("query")).Op(":=").Range().Id(qg.recv).Dot(namedField),
				).BlockFunc(func(forBody *jen.Group) {
					initFn := jen.Func().Params(jen.Id("n").Op("*").Add(qg.entityType())).Block(
						jen.Id("n").Dot("AppendNamed" + edgeField).Call(jen.Id("name")),
					)
					assignFn := jen.Func().Params(
						jen.Id("n").Op("*").Add(qg.entityType()),
						jen.Id("e").Op("*").Add(targetEntityType()),
					).BlockFunc(func(fnBody *jen.Group) {
						fnBody.Id("n").Dot("AppendNamed"+edgeField).Call(jen.Id("name"), jen.Id("e"))
						qg.genBackRef(fnBody, edge)
					})
					forBody.If(
						jen.Err().Op(":=").Id(qg.recv).Dot(loaderName).Call(
							jen.Id("ctx"), jen.Id("query"), jen.Id("nodes"),
							initFn, assignFn,
						),
						jen.Err().Op("!=").Nil(),
					).Block(
						jen.Return(jen.Err()),
					)
				})
			}
		}

		// Phase 3 — loadTotal registry loop.
		allBody.For(jen.Id("i").Op(":=").Range().Id(qg.recv).Dot("loadTotal")).Block(
			jen.If(jen.Err().Op(":=").Id(qg.recv).Dot("loadTotal").Index(jen.Id("i")).Call(jen.Id("ctx"), jen.Id("nodes")), jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Err()),
			),
		)

		// Inject runtime config so entity-level methods can access the driver.
		allBody.For(jen.List(jen.Id("_"), jen.Id("node")).Op(":=").Range().Id("nodes")).Block(
			jen.Id("node").Dot(qg.t.SetConfigMethodName()).Call(jen.Id(qg.recv).Dot("config")),
		)

		allBody.Return(jen.Nil())
	})
}

// genBackRef emits, inside an eager-load assign callback, the back-reference
// from the loaded child e to its parent n:
//
//	if !e.Edges.<Ref>Loaded() {
//		e.Edges.<Ref> = n
//		e.Edges.Mark<Ref>Loaded()
//	}
//
// Only under FeatureBidiEdgeRefs and only when the paired edge is unique,
// as in Ent (dialect/sql/query.tmpl, "bidiedges"). Emitted unconditionally,
// it made json.Marshal of any WithXxx() result fail with "encountered a
// cycle".
func (qg *queryGen) genBackRef(fnBody *jen.Group, edge *gen.Edge) {
	if !qg.bidiEdgeRefs || edge.Ref == nil || !edge.Ref.Unique {
		return
	}
	refField := edge.Ref.StructField()
	fnBody.If(jen.Op("!").Id("e").Dot("Edges").Dot(refField+"Loaded").Call()).Block(
		jen.Id("e").Dot("Edges").Dot(refField).Op("=").Id("n"),
		jen.Id("e").Dot("Edges").Dot("Mark"+refField+"Loaded").Call(),
	)
}

// genPrepareQuery emits prepareQuery: explicit policy evaluation followed
// by runtime.RunTraversers over the entity's interceptor slice.
func (qg *queryGen) genPrepareQuery() {
	// prepareQuery — evaluates the privacy policy (if any) and then runs
	// Traversers from the interceptor list (Ent-style). Privacy is no
	// longer part of the interceptor chain — it is invoked explicitly
	// here via q.policy.EvalQuery(). Interceptors never see the privacy
	// call at all.
	qg.f.Comment("prepareQuery evaluates the privacy policy (if any) and runs")
	qg.f.Comment("Traversers from the interceptor list. Privacy is invoked")
	qg.f.Comment("explicitly here — it is NOT part of the interceptor chain.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("prepareQuery").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().BlockFunc(func(body *jen.Group) {
		if qg.hasPolicy {
			body.If(jen.Id(qg.recv).Dot("policy").Op("!=").Nil()).Block(
				jen.If(
					jen.Err().Op(":=").Id(qg.recv).Dot("policy").Dot("EvalQuery").Call(
						jen.Id("ctx"), jen.Id(qg.recv),
					),
					jen.Err().Op("!=").Nil(),
				).Block(
					jen.Return(jen.Err()),
				),
			)
		}
		body.Return(jen.Qual(runtimePkg, "RunTraversers").Call(
			jen.Id("ctx"), jen.Id(qg.recv), qg.inters(qg.recv),
		))
	})
}

// genEntityTerminals emits the entity-returning terminals and their X
// variants: All, First, Only.
func (qg *queryGen) genEntityTerminals() {
	// All — wraps sqlAll with interceptor support (Ent-style).
	qg.f.Commentf("All executes the query and returns a list of %s.", qg.t.Name)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("All").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Op("*").Add(qg.entityType()), jen.Error()).BlockFunc(func(allBody *jen.Group) {
		veloxPkg := qg.h.VeloxPkg()

		// ctx = setContextOp(ctx, _q.ctx, velox.OpQueryAll)
		allBody.Id("ctx").Op("=").Id("setContextOp").Call(
			jen.Id("ctx"), jen.Id(qg.recv).Dot("ctx"), jen.Qual(veloxPkg, "OpQueryAll"),
		)
		// prepareQuery first (runs Traversers), then interceptor chain.
		allBody.If(jen.Err().Op(":=").Id(qg.recv).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)
		allBody.If(jen.Len(qg.inters(qg.recv)).Op(">").Lit(0)).Block(
			jen.Return(jen.Qual(veloxPkg, "WithInterceptors").Types(
				jen.Index().Op("*").Add(qg.entityType()),
			).Call(
				jen.Id("ctx"),
				jen.Id(qg.recv),
				jen.Id("querierAll").Types(
					jen.Index().Op("*").Add(qg.entityType()),
					jen.Op("*").Id(qg.queryName),
				).Call(),
				qg.inters(qg.recv),
			)),
		)
		allBody.Return(jen.Id(qg.recv).Dot("sqlAll").Call(jen.Id("ctx")))
	})

	// AllX — panics on error.
	qg.f.Commentf("AllX is like All, but panics if an error occurs.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("AllX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Index().Op("*").Add(qg.entityType()).Block(
		jen.List(jen.Id("nodes"), jen.Err()).Op(":=").Id(qg.recv).Dot("All").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("nodes")),
	)

	// First — delegates to All with limit 1.
	qg.f.Commentf("First returns the first %s entity from the query.", qg.t.Name)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("First").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Add(qg.entityType()), jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Id("clone").Op(":=").Id(qg.recv).Dot("clone").Call()
		body.Id("clone").Dot("ctx").Dot("Limit").Op("=").Id("intP").Call(jen.Lit(1))
		body.List(jen.Id("nodes"), jen.Err()).Op(":=").Id("clone").Dot("All").Call(
			jen.Id("setContextOp").Call(jen.Id("ctx"), jen.Id(qg.recv).Dot("ctx"), jen.Qual(qg.h.VeloxPkg(), "OpQueryFirst")),
		)
		body.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)
		body.If(jen.Len(jen.Id("nodes")).Op("==").Lit(0)).Block(
			jen.Return(jen.Nil(), jen.Qual(runtimePkg, "NewNotFoundError").Call(jen.Lit(qg.t.Name))),
		)
		body.Return(jen.Id("nodes").Index(jen.Lit(0)), jen.Nil())
	})

	// FirstX — panics on error.
	qg.f.Commentf("FirstX is like First, but panics if an error occurs.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("FirstX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Op("*").Add(qg.entityType()).Block(
		jen.List(jen.Id("node"), jen.Err()).Op(":=").Id(qg.recv).Dot("First").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("node")),
	)

	// Only — delegates to All with limit 2.
	qg.f.Commentf("Only returns a single %s entity found by the query, ensuring it only returns one.", qg.t.Name)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Only").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Add(qg.entityType()), jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Id("clone").Op(":=").Id(qg.recv).Dot("clone").Call()
		body.Id("clone").Dot("ctx").Dot("Limit").Op("=").Id("intP").Call(jen.Lit(2))
		body.List(jen.Id("nodes"), jen.Err()).Op(":=").Id("clone").Dot("All").Call(
			jen.Id("setContextOp").Call(jen.Id("ctx"), jen.Id(qg.recv).Dot("ctx"), jen.Qual(qg.h.VeloxPkg(), "OpQueryOnly")),
		)
		body.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)
		body.Switch(jen.Len(jen.Id("nodes"))).Block(
			jen.Case(jen.Lit(0)).Block(
				jen.Return(jen.Nil(), jen.Qual(runtimePkg, "NewNotFoundError").Call(jen.Lit(qg.t.Name))),
			),
			jen.Case(jen.Lit(1)).Block(
				jen.Return(jen.Id("nodes").Index(jen.Lit(0)), jen.Nil()),
			),
			jen.Default().Block(
				jen.Return(jen.Nil(), jen.Qual(runtimePkg, "NewNotSingularError").Call(jen.Lit(qg.t.Name))),
			),
		)
	})

	// OnlyX — panics on error.
	qg.f.Commentf("OnlyX is like Only, but panics if an error occurs.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("OnlyX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Op("*").Add(qg.entityType()).Block(
		jen.List(jen.Id("node"), jen.Err()).Op(":=").Id(qg.recv).Dot("Only").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("node")),
	)
}

// genCountExist emits sqlCount, Count, CountX, Exist and ExistX.
func (qg *queryGen) genCountExist() {
	// sqlCount — actual count execution, extracted for interceptor support.
	qg.f.Comment("sqlCount executes the SQL COUNT query and returns the result.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("sqlCount").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Int(), jen.Error()).BlockFunc(func(body *jen.Group) {
		// Resolve graph traversal path.
		body.Var().Id("from").Op("*").Qual(qg.sqlPkg, "Selector")
		body.If(jen.Id(qg.recv).Dot("path").Op("!=").Nil()).BlockFunc(func(ifBody *jen.Group) {
			ifBody.Var().Id("err").Error()
			ifBody.List(jen.Id("from"), jen.Id("err")).Op("=").Id(qg.recv).Dot("path").Call(jen.Id("ctx"))
			ifBody.If(jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Lit(0), jen.Err()),
			)
		})
		// Build a spec with nil columns so the SQL is COUNT(*).
		body.Id("spec").Op(":=").Id(qg.recv).Dot("querySpec").Call()
		body.Id("spec").Dot("Node").Dot("Columns").Op("=").Nil()
		body.Id("spec").Dot("From").Op("=").Id("from")
		body.Return(jen.Qual(qg.sqlgraphPkg, "CountNodes").Call(
			jen.Id("ctx"), jen.Id(qg.recv).Dot("config").Dot("Driver"), jen.Id("spec"),
		))
	})

	// Count — wraps sqlCount with interceptor support (Ent-style).
	qg.f.Comment("Count returns the count of the given query.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Count").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Int(), jen.Error()).BlockFunc(func(body *jen.Group) {
		veloxPkg := qg.h.VeloxPkg()
		// ctx = setContextOp(ctx, _q.ctx, velox.OpQueryCount)
		body.Id("ctx").Op("=").Id("setContextOp").Call(
			jen.Id("ctx"), jen.Id(qg.recv).Dot("ctx"), jen.Qual(veloxPkg, "OpQueryCount"),
		)
		body.If(jen.Err().Op(":=").Id(qg.recv).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Lit(0), jen.Err()),
		)
		body.If(jen.Len(qg.inters(qg.recv)).Op(">").Lit(0)).Block(
			jen.Return(jen.Qual(veloxPkg, "WithInterceptors").Types(
				jen.Int(),
			).Call(
				jen.Id("ctx"),
				jen.Id(qg.recv),
				jen.Id("querierCount").Types(
					jen.Op("*").Id(qg.queryName),
				).Call(),
				qg.inters(qg.recv),
			)),
		)
		body.Return(jen.Id(qg.recv).Dot("sqlCount").Call(jen.Id("ctx")))
	})

	// CountX — panics on error.
	qg.f.Comment("CountX is like Count, but panics if an error occurs.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("CountX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Int().Block(
		jen.List(jen.Id("count"), jen.Err()).Op(":=").Id(qg.recv).Dot("Count").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("count")),
	)

	// Exist — uses FirstID (more efficient than Count — stops after 1 row).
	qg.f.Comment("Exist returns true if the query has results.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Exist").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Bool(), jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Id("ctx").Op("=").Id("setContextOp").Call(
			jen.Id("ctx"), jen.Id(qg.recv).Dot("ctx"), jen.Qual(qg.h.VeloxPkg(), "OpQueryExist"),
		)
		body.List(jen.Id("_"), jen.Err()).Op(":=").Id(qg.recv).Dot("FirstID").Call(jen.Id("ctx"))
		body.If(jen.Err().Op("==").Nil()).Block(
			jen.Return(jen.True(), jen.Nil()),
		)
		body.If(jen.Qual(runtimePkg, "IsNotFound").Call(jen.Err())).Block(
			jen.Return(jen.False(), jen.Nil()),
		)
		body.Return(jen.False(), jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: check existence: %w"), jen.Err()))
	})

	// ExistX — panics on error.
	qg.f.Comment("ExistX is like Exist, but panics if an error occurs.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("ExistX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Bool().Block(
		jen.List(jen.Id("exist"), jen.Err()).Op(":=").Id(qg.recv).Dot("Exist").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("exist")),
	)
}

// genSQLExplain emits the non-executing introspection terminals SQL and
// Explain.
func (qg *queryGen) genSQLExplain() {
	// SQL — returns the generated SQL string and args without executing.
	qg.f.Comment("SQL returns the SQL query string and arguments for debugging.")
	qg.f.Comment("It runs prepareQuery (privacy traversers) and builds the selector,")
	qg.f.Comment("but does not execute the query.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("SQL").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.String(), jen.Index().Any(), jen.Error()).Block(
		jen.If(jen.Err().Op(":=").Id(qg.recv).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Lit(""), jen.Nil(), jen.Err()),
		),
		jen.List(jen.Id("selector"), jen.Err()).Op(":=").Id(qg.recv).Dot("buildSelector").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Lit(""), jen.Nil(), jen.Err()),
		),
		jen.List(jen.Id("query"), jen.Id("args")).Op(":=").Id("selector").Dot("Query").Call(),
		jen.Return(jen.Id("query"), jen.Id("args"), jen.Nil()),
	)

	// Explain — returns a *runtime.QueryPlan describing the query without executing.
	qg.f.Comment("Explain returns the query's execution plan without executing it.")
	qg.f.Comment("Includes the SQL, arguments, planned edge loads, and active interceptors.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Explain").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Qual(runtimePkg, "QueryPlan"), jen.Error()).BlockFunc(func(body *jen.Group) {
		// prepareQuery
		body.If(jen.Err().Op(":=").Id(qg.recv).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)

		// Build selector
		body.List(jen.Id("selector"), jen.Err()).Op(":=").Id(qg.recv).Dot("buildSelector").Call(jen.Id("ctx"))
		body.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)
		body.List(jen.Id("query"), jen.Id("args")).Op(":=").Id("selector").Dot("Query").Call()

		// Initialize plan
		body.Id("plan").Op(":=").Op("&").Qual(runtimePkg, "QueryPlan").Values(jen.Dict{
			jen.Id("SQL"):  jen.Id("query"),
			jen.Id("Args"): jen.Id("args"),
		})

		// Collect interceptor type names
		body.For(jen.List(jen.Id("_"), jen.Id("inter")).Op(":=").Range().Id(qg.recv).Dot("inters").Dot(qg.t.Name)).Block(
			jen.Id("plan").Dot("Interceptors").Op("=").Append(
				jen.Id("plan").Dot("Interceptors"),
				jen.Qual("fmt", "Sprintf").Call(jen.Lit("%T"), jen.Id("inter")),
			),
		)

		// Edge plans
		for _, edge := range qg.t.Edges {
			edgeField := edgeCallbackField(edge)
			body.If(jen.Id(qg.recv).Dot(edgeField).Op("!=").Nil()).BlockFunc(func(inner *jen.Group) {
				inner.List(jen.Id("eSel"), jen.Id("eErr")).Op(":=").Id(qg.recv).Dot(edgeField).Dot("buildSelector").Call(jen.Id("ctx"))
				inner.If(jen.Id("eErr").Op("==").Nil()).Block(
					jen.List(jen.Id("eq"), jen.Id("ea")).Op(":=").Id("eSel").Dot("Query").Call(),
					jen.Id("plan").Dot("Edges").Op("=").Append(
						jen.Id("plan").Dot("Edges"),
						jen.Qual(runtimePkg, "EdgePlan").Values(jen.Dict{
							jen.Id("Name"): jen.Lit(edge.Name),
							jen.Id("SQL"):  jen.Id("eq"),
							jen.Id("Args"): jen.Id("ea"),
						}),
					),
				)
			})
		}

		body.Return(jen.Id("plan"), jen.Nil())
	})
}

// genIDTerminals emits the id-returning terminals and their X variants:
// IDs (+ sqlIDs), FirstID, OnlyID.
func (qg *queryGen) genIDTerminals() {
	// IDs — user-facing method that wraps sqlIDs in the interceptor
	// chain. sqlIDs contains the actual SQL execution.
	qg.f.Commentf("IDs executes the query and returns a list of %s IDs.", qg.t.Name)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("IDs").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Add(qg.idType), jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Id("ctx").Op("=").Id("setContextOp").Call(
			jen.Id("ctx"), jen.Id(qg.recv).Dot("ctx"), jen.Qual(qg.h.VeloxPkg(), "OpQueryIDs"),
		)
		body.If(jen.Id("err").Op(":=").Id(qg.recv).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		)
		body.If(jen.Len(qg.inters(qg.recv)).Op(">").Lit(0)).Block(
			jen.Return(jen.Qual(qg.h.VeloxPkg(), "WithInterceptors").Types(
				jen.Index().Add(qg.idType),
			).Call(
				jen.Id("ctx"),
				jen.Id(qg.recv),
				jen.Id("querierIDs").Types(
					jen.Index().Add(qg.idType),
					jen.Op("*").Id(qg.queryName),
				).Call(),
				qg.inters(qg.recv),
			)),
		)
		body.Return(jen.Id(qg.recv).Dot("sqlIDs").Call(jen.Id("ctx")))
	})

	// sqlIDs — the actual SQL execution, wrapped by IDs above.
	qg.f.Commentf("sqlIDs executes the ID-only SQL SELECT for %s.", qg.t.Name)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("sqlIDs").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Add(qg.idType), jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Var().Id("from").Op("*").Qual(qg.sqlPkg, "Selector")
		body.If(jen.Id(qg.recv).Dot("path").Op("!=").Nil()).BlockFunc(func(ifBody *jen.Group) {
			ifBody.Var().Id("err").Error()
			ifBody.List(jen.Id("from"), jen.Id("err")).Op("=").Id(qg.recv).Dot("path").Call(jen.Id("ctx"))
			ifBody.If(jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Err()),
			)
			ifBody.If(jen.Id(qg.recv).Dot("ctx").Dot("Unique").Op("==").Nil()).Block(
				jen.Id(qg.recv).Dot("Unique").Call(jen.True()),
			)
		})
		body.Id("spec").Op(":=").Id(qg.recv).Dot("querySpec").Call()
		body.Id("spec").Dot("Node").Dot("Columns").Op("=").Index().String().Values(jen.Qual(qg.entitySubPkg, "FieldID"))
		body.Id("spec").Dot("From").Op("=").Id("from")
		body.Var().Id("ids").Index().Add(qg.idType)
		body.Id("spec").Dot("ScanValues").Op("=").Func().Params(jen.Id("_").Index().String()).Params(jen.Index().Any(), jen.Error()).Block(
			jen.Return(jen.Qual(runtimePkg, "IDScanValues").Call(jen.Qual(schemaPkg(), qg.t.ID.Type.ConstName())), jen.Nil()),
		)
		body.Id("spec").Dot("Assign").Op("=").Func().Params(jen.Id("_").Index().String(), jen.Id("values").Index().Any()).Error().BlockFunc(func(fnBody *jen.Group) {
			fnBody.If(jen.Len(jen.Id("values")).Op("==").Lit(0)).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: IDs: no values returned"))),
			)
			fnBody.List(jen.Id("id"), jen.Id("err")).Op(":=").Qual(runtimePkg, "ExtractID").Call(jen.Id("values").Index(jen.Lit(0)), jen.Qual(schemaPkg(), qg.t.ID.Type.ConstName()))
			fnBody.If(jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Err()),
			)
			fnBody.Id("ids").Op("=").Append(jen.Id("ids"), jen.Id("id").Assert(qg.idType))
			fnBody.Return(jen.Nil())
		})
		body.If(jen.Err().Op(":=").Qual(qg.sqlgraphPkg, "QueryNodes").Call(
			jen.Id("ctx"), jen.Id(qg.recv).Dot("config").Dot("Driver"), jen.Id("spec"),
		), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)
		body.Return(jen.Id("ids"), jen.Nil())
	})

	// IDsX — panics on error.
	qg.f.Commentf("IDsX is like IDs, but panics if an error occurs.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("IDsX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Index().Add(qg.idType).Block(
		jen.List(jen.Id("ids"), jen.Err()).Op(":=").Id(qg.recv).Dot("IDs").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("ids")),
	)

	// FirstID — clones, sets limit 1, calls IDs.
	qg.f.Commentf("FirstID returns the first %s ID from the query.", qg.t.Name)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("FirstID").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(qg.idType, jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Var().Id("zero").Add(qg.idType)
		body.Id("clone").Op(":=").Id(qg.recv).Dot("clone").Call()
		body.Id("clone").Dot("ctx").Dot("Limit").Op("=").Id("intP").Call(jen.Lit(1))
		body.List(jen.Id("ids"), jen.Err()).Op(":=").Id("clone").Dot("IDs").Call(
			jen.Id("setContextOp").Call(jen.Id("ctx"), jen.Id(qg.recv).Dot("ctx"), jen.Qual(qg.h.VeloxPkg(), "OpQueryFirstID")),
		)
		body.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Id("zero"), jen.Err()),
		)
		body.If(jen.Len(jen.Id("ids")).Op("==").Lit(0)).Block(
			jen.Return(jen.Id("zero"), jen.Qual(runtimePkg, "NewNotFoundError").Call(jen.Lit(qg.t.Name))),
		)
		body.Return(jen.Id("ids").Index(jen.Lit(0)), jen.Nil())
	})

	// FirstIDX — panics on error.
	qg.f.Commentf("FirstIDX is like FirstID, but panics if an error occurs.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("FirstIDX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Add(qg.idType).Block(
		jen.List(jen.Id("id"), jen.Err()).Op(":=").Id(qg.recv).Dot("FirstID").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("id")),
	)

	// OnlyID — clones, sets limit 2, calls IDs.
	qg.f.Commentf("OnlyID returns the only %s ID in the query.", qg.t.Name)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("OnlyID").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(qg.idType, jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Var().Id("zero").Add(qg.idType)
		body.Id("clone").Op(":=").Id(qg.recv).Dot("clone").Call()
		body.Id("clone").Dot("ctx").Dot("Limit").Op("=").Id("intP").Call(jen.Lit(2))
		body.List(jen.Id("ids"), jen.Err()).Op(":=").Id("clone").Dot("IDs").Call(
			jen.Id("setContextOp").Call(jen.Id("ctx"), jen.Id(qg.recv).Dot("ctx"), jen.Qual(qg.h.VeloxPkg(), "OpQueryOnlyID")),
		)
		body.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Id("zero"), jen.Err()),
		)
		body.Switch(jen.Len(jen.Id("ids"))).Block(
			jen.Case(jen.Lit(0)).Block(
				jen.Return(jen.Id("zero"), jen.Qual(runtimePkg, "NewNotFoundError").Call(jen.Lit(qg.t.Name))),
			),
			jen.Case(jen.Lit(1)).Block(
				jen.Return(jen.Id("ids").Index(jen.Lit(0)), jen.Nil()),
			),
			jen.Default().Block(
				jen.Return(jen.Id("zero"), jen.Qual(runtimePkg, "NewNotSingularError").Call(jen.Lit(qg.t.Name))),
			),
		)
	})

	// OnlyIDX — panics on error.
	qg.f.Commentf("OnlyIDX is like OnlyID, but panics if an error occurs.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("OnlyIDX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Add(qg.idType).Block(
		jen.List(jen.Id("id"), jen.Err()).Op(":=").Id(qg.recv).Dot("OnlyID").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("id")),
	)
}

// genLocking emits ForUpdate and ForShare.
func (qg *queryGen) genLocking() {
	// =========================================================================
	// ForUpdate and ForShare — row-level locking
	// =========================================================================

	qg.f.Comment("ForUpdate locks the selected rows against concurrent updates, and prevent them from being")
	qg.f.Comment("updated, deleted or \"selected ... for update\" by other sessions, until the transaction is")
	qg.f.Comment("either committed or rolled-back.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("ForUpdate").Params(
		jen.Id("opts").Op("...").Qual(qg.sqlPkg, "LockOption"),
	).Qual(qg.entityPkgPath, qg.querierIface).Block(
		lockDropsDistinct(qg.recv, "CapForUpdate"),
		jen.Id(qg.recv).Dot("modifiers").Op("=").Append(
			jen.Id(qg.recv).Dot("modifiers"),
			jen.Func().Params(jen.Id("s").Op("*").Qual(qg.sqlPkg, "Selector")).Block(
				jen.Id("s").Dot("ForUpdate").Call(jen.Id("opts").Op("...")),
			),
		),
		jen.Return(jen.Id(qg.recv)),
	)

	qg.f.Comment("ForShare behaves similarly to ForUpdate, except that it acquires a shared mode lock")
	qg.f.Comment("on any rows that are read. Other sessions can read the rows, but cannot modify them")
	qg.f.Comment("until your transaction commits.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("ForShare").Params(
		jen.Id("opts").Op("...").Qual(qg.sqlPkg, "LockOption"),
	).Qual(qg.entityPkgPath, qg.querierIface).Block(
		lockDropsDistinct(qg.recv, "CapForShare"),
		jen.Id(qg.recv).Dot("modifiers").Op("=").Append(
			jen.Id(qg.recv).Dot("modifiers"),
			jen.Func().Params(jen.Id("s").Op("*").Qual(qg.sqlPkg, "Selector")).Block(
				jen.Id("s").Dot("ForShare").Call(jen.Id("opts").Op("...")),
			),
		),
		jen.Return(jen.Id(qg.recv)),
	)
}

// lockDropsDistinct emits the guard at the top of ForUpdate/ForShare:
//
//	if caps := dialect.GetCapabilities(q.config.Driver.Dialect()); caps.Has(dialect.<lockCap>) && !caps.Has(dialect.CapLockWithDistinct) {
//		q.Unique(false)
//	}
//
// Postgres rejects a locking clause on SELECT DISTINCT, so the builder
// drops DISTINCT there (Ent does the same, keyed on the dialect name).
// The decision is a capability lookup rather than a name comparison so a
// new dialect only has to declare its flags.
func lockDropsDistinct(recv, lockCap string) jen.Code {
	return jen.If(
		jen.Id("caps").Op(":=").Qual(dialectPkg(), "GetCapabilities").Call(
			jen.Id(recv).Dot("config").Dot("Driver").Dot("Dialect").Call(),
		),
		jen.Id("caps").Dot("Has").Call(jen.Qual(dialectPkg(), lockCap)).Op("&&").
			Op("!").Id("caps").Dot("Has").Call(jen.Qual(dialectPkg(), "CapLockWithDistinct")),
	).Block(
		jen.Id(recv).Dot("Unique").Call(jen.False()),
	)
}
