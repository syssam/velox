package sql

import (
	"go/token"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genQueryPkg generates a query builder for the shared query/ package.
// All query types live in one package so edge loading can create target queries
// directly (same package, no import cycle). Each query implements the
// entity.XxxQuerier interface from the entity/ package.
//
// The generated query struct holds ALL query state directly — no embedded
// *runtime.QueryBase (no embedding). This makes query builders self-contained like Ent ORM.
//
// Output: query/{entity_name}.go
func genQueryPkg(h gen.GeneratorHelper, t *gen.Type, allNodes []*gen.Type, entityPkgPath string) *jen.File {
	f := h.NewFile(h.Pkg())

	queryName := t.Name + "Query"
	recv := "q"
	sqlPkg := h.SQLPkg()
	sqlgraphPkg := h.SQLGraphPkg()

	// SP-2: import path for the central entity package that holds
	// the shared *entity.InterceptorStore.
	entityPkgImportPath := h.SharedEntityPkg()
	// Per-entity sub-package import path; needed by many later emitters.
	entitySubPkg := h.LeafPkgPath(t)

	// intersField returns Jen for the effective interceptor slice the
	// query should use at execute time — always the per-entity slice
	// on the shared *InterceptorStore. Privacy is NO LONGER part of the
	// interceptor chain: it is evaluated explicitly at prepareQuery time
	// via q.policy.EvalQuery(). This unification means all entities
	// (with or without policy) use the same direct access pattern.
	hasPolicy := h.FeatureEnabled(gen.FeaturePrivacy.Name) && t.NumPolicy() > 0
	intersField := func(receiver string) *jen.Statement {
		return jen.Id(receiver).Dot("inters").Dot(t.Name)
	}
	_ = intersField // referenced below; alias to silence linter if a path drops out

	// Querier interface name in entity/ package
	querierIface := t.Name + "Querier"

	// Entity type reference (always qualified from query/ -> entity/)
	entityType := func() *jen.Statement { return jen.Qual(entityPkgPath, t.Name) }
	// entitySubPkg is hoisted to the top of the function (above intersField)
	// so the policy-prepend path can reference it.

	// ID type from the entity definition
	idType := h.IDType(t)

	// =========================================================================
	// Query struct — self-contained, no runtime type embedding
	// =========================================================================

	f.Commentf("%s is the query builder for %s entities.", queryName, t.Name)
	f.Commentf("It implements %s.%s.", "entity", querierIface)
	schemaConfigEnabled := h.FeatureEnabled(gen.FeatureSchemaConfig.Name)

	namedEdgesEnabled := h.FeatureEnabled(gen.FeatureNamedEdges.Name)

	f.Type().Id(queryName).StructFunc(func(group *jen.Group) {
		group.Id("config").Qual(runtimePkg, "Config")
		if schemaConfigEnabled {
			group.Id("schemaConfig").Qual(h.InternalPkg(), "SchemaConfig")
		}
		group.Id("ctx").Op("*").Qual(runtimePkg, "QueryContext")
		group.Id("predicates").Index().Func().Params(jen.Op("*").Qual(sqlPkg, "Selector"))
		group.Id("order").Index().Func().Params(jen.Op("*").Qual(sqlPkg, "Selector"))
		group.Id("modifiers").Index().Func().Params(jen.Op("*").Qual(sqlPkg, "Selector"))
		// SP-2: shared-pointer interceptor wiring. The query holds a
		// pointer to the central *entity.InterceptorStore, NOT a slice
		// copy. client.Intercept(...) mutates the shared store and is
		// immediately visible to every query holding the pointer —
		// even queries constructed before the call. Read sites access
		// q.inters.<EntityName> to enumerate this entity's chain.
		group.Id("inters").Op("*").Qual(entityPkgImportPath, "InterceptorStore")
		// policy is the entity's privacy policy (nil when the entity has
		// no policy or when constructed via a code path that doesn't wire
		// it). Evaluated explicitly in prepareQuery — NOT part of the
		// interceptor chain.
		if hasPolicy {
			group.Id("policy").Qual(h.VeloxPkg(), "Policy")
		}
		group.Id("withFKs").Bool()
		// Edge eager-loading: concrete *XxxQuery pointers (same package)
		for _, edge := range t.Edges {
			targetQueryName := edge.Type.Name + "Query"
			group.Id(edgeCallbackField(edge)).Op("*").Id(targetQueryName)
		}
		// loadTotal — registry of post-load hooks (Ent-style).
		group.Id("loadTotal").Index().Func().Params(
			jen.Qual("context", "Context"),
			jen.Index().Op("*").Add(entityType()),
		).Error()
		// Named edge variants (FeatureNamedEdges).
		if namedEdgesEnabled {
			for _, edge := range t.Edges {
				if !edge.Unique {
					targetQueryName := edge.Type.Name + "Query"
					group.Id("withNamed" + edge.StructField()).Map(jen.String()).Op("*").Id(targetQueryName)
				}
			}
		}
		group.Id("path").Func().Params(jen.Qual("context", "Context")).Params(
			jen.Op("*").Qual(sqlPkg, "Selector"), jen.Error(),
		)
	})

	// =========================================================================
	// Constructor
	// =========================================================================

	f.Commentf("New%s creates a new %s.", queryName, queryName)
	f.Func().Id("New" + queryName).Params(
		jen.Id("cfg").Qual(runtimePkg, "Config"),
	).Op("*").Id(queryName).Block(
		jen.Return(jen.Op("&").Id(queryName).Values(jen.Dict{
			jen.Id("config"): jen.Id("cfg"),
			jen.Id("ctx"):    jen.Op("&").Qual(runtimePkg, "QueryContext").Values(jen.Dict{jen.Id("Type"): jen.Lit(t.Name)}),
		})),
	)

	// =========================================================================
	// SetPath — allows external callers (wrapper, contrib) to set the path
	// =========================================================================

	f.Comment("SetPath sets the graph traversal path for this query.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("SetPath").Params(
		jen.Id("p").Func().Params(jen.Qual("context", "Context")).Params(
			jen.Op("*").Qual(sqlPkg, "Selector"), jen.Error(),
		),
	).Block(
		jen.Id(recv).Dot("path").Op("=").Id("p"),
	)

	// SetInterStore — wires the shared *entity.InterceptorStore pointer
	// onto a query that was constructed via cross-package registry
	// dispatch (runtime.NewEntityQuery). Called by the entity client
	// constructor and by every code path that derives a child query.
	// SP-2: replaces the previous SetInters([]Interceptor) slice setter
	// — interceptors no longer get copied per-query.
	f.Comment("SetInterStore wires the shared client-level interceptor store onto this query.")
	f.Comment("Called by the entity client constructor and by query derivation code paths.")
	f.Comment("Not for direct use — call via the SetInterStore inline interface assertion.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("SetInterStore").Params(
		jen.Id("s").Op("*").Qual(entityPkgImportPath, "InterceptorStore"),
	).Block(
		jen.Id(recv).Dot("inters").Op("=").Id("s"),
	)

	// SetPolicy wires the entity's privacy policy onto this query so
	// prepareQuery can invoke policy.EvalQuery explicitly. Called by
	// the entity client constructor (direct path) and by cross-package
	// edge-query constructors (via runtime.EntityPolicy lookup). Only
	// emitted for entities that declare a privacy policy.
	if hasPolicy {
		f.Comment("SetPolicy wires the entity's privacy policy onto this query.")
		f.Comment("Called via an inline interface type assertion from code that")
		f.Comment("constructs the query (entity client, edge query builders).")
		f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("SetPolicy").Params(
			jen.Id("p").Qual(h.VeloxPkg(), "Policy"),
		).Block(
			jen.Id(recv).Dot("policy").Op("=").Id("p"),
		)
	}

	// Filter — returns an entity Filter that forwards predicate
	// additions through the query's AddPredicate method. Implements
	// privacy.Filterable so query policy rules can use
	// privacy.FilterFunc to inject predicates without knowing the
	// concrete query type. The filter never touches q.predicates
	// directly — it goes through the runtime.PredicateAdder interface
	// so the query's internal representation can evolve freely.
	//
	// AddPredicate is the companion method that lets the filter write
	// predicates back to this query via the interface. It's
	// deliberately exported despite being an internal-ish hook: the
	// filter lives in a sibling generated package, so structural
	// interface satisfaction requires an exported method.
	if h.FeatureEnabled(gen.FeaturePrivacy.Name) {
		// After cycle-break, filter.go lives in client/{entity}/ (package {entity}client),
		// not the {entity}/ leaf — the filter constructor must be qualified there.
		clientPkgPath := h.RootPkg() + "/client/" + t.PackageDir()
		const privacyPkgPath = "github.com/syssam/velox/privacy"
		f.Commentf("AddPredicate appends a raw SQL-level predicate to the query.")
		f.Comment("Satisfies runtime.PredicateAdder so privacy filters can write")
		f.Comment("predicates through this method rather than touching internal state.")
		f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("AddPredicate").Params(
			jen.Id("p").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector")),
		).Block(
			jen.Id(recv).Dot("predicates").Op("=").Append(jen.Id(recv).Dot("predicates"), jen.Id("p")),
		)

		f.Commentf("Filter returns a %sFilter that writes predicates through this query.", t.Name)
		f.Comment("Implements privacy.Filterable so FilterFunc-based query rules")
		f.Comment("can inject WHERE clauses without knowing the concrete query type.")
		f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Filter").Params().Qual(privacyPkgPath, "Filter").Block(
			jen.Return(jen.Qual(clientPkgPath, "New"+t.Name+"Filter").Call(
				jen.Id(recv).Dot("config"),
				jen.Id(recv),
			)),
		)
	}

	// SetSchemaConfig — allows callers to inject the schema config for multi-tenancy.
	if schemaConfigEnabled {
		f.Comment("SetSchemaConfig sets the schema config for multi-tenancy support.")
		f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("SetSchemaConfig").Params(
			jen.Id("sc").Qual(h.InternalPkg(), "SchemaConfig"),
		).Block(
			jen.Id(recv).Dot("schemaConfig").Op("=").Id("sc"),
		)
	}

	// =========================================================================
	// FieldCollectable interface — enables GraphQL field collection.
	// =========================================================================

	f.Commentf("GetIDColumn returns the primary key column name for %s.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("GetIDColumn").Params().String().Block(
		jen.Return(jen.Qual(entitySubPkg, "FieldID")),
	)

	f.Comment("GetCtx returns the query context for field projection.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("GetCtx").Params().Op("*").Qual(runtimePkg, "QueryContext").Block(
		jen.Return(jen.Id(recv).Dot("ctx")),
	)

	f.Comment("WithEdgeLoad adds an edge to be eagerly loaded by name.")
	f.Comment("Used by GraphQL field collector for generic edge loading.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("WithEdgeLoad").Params(
		jen.Id("name").String(),
		jen.Id("_").Op("...").Qual(runtimePkg, "LoadOption"),
	).BlockFunc(func(body *jen.Group) {
		// Switch on edge name to set the correct withXxx field.
		body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, edge := range t.Edges {
				targetQueryName := edge.Type.Name + "Query"
				callbackField := edgeCallbackField(edge)
				// Build case body: initialize query if nil, and enable FK columns
				// only for edges where the FK resides on this entity's table (M2O, O2O inverse).
				var caseStmts []jen.Code
				caseStmts = append(caseStmts,
					jen.If(jen.Id(recv).Dot(callbackField).Op("==").Nil()).Block(
						jen.Id(recv).Dot(callbackField).Op("=").Id("New"+targetQueryName).Call(jen.Id(recv).Dot("config")),
						// Thread the parent's interceptors into the child
						// query so client.Intercept() fires on eager-loads
						// as well as direct queries.
						jen.Id(recv).Dot(callbackField).Dot("inters").Op("=").Id(recv).Dot("inters"),
					),
				)
				if edge.OwnFK() {
					caseStmts = append(caseStmts, jen.Id(recv).Dot("withFKs").Op("=").True())
				}
				sw.Case(jen.Lit(edge.Name)).Block(caseStmts...)
			}
		})
	})

	// =========================================================================
	// NewXxxQueryFromEdge — adapts a *runtime.EdgeQuery into a self-contained query.
	// Used by GraphQL contrib (pagination) which receives an EdgeQuery from edge resolvers.
	// =========================================================================

	f.Commentf("New%sFromEdge creates a %s from an existing EdgeQuery.", queryName, queryName)
	f.Commentf("The EdgeQuery fields are copied into the self-contained query struct via exported getters.")
	f.Comment("SP-2: the inters field is a *entity.InterceptorStore pointer recovered")
	f.Comment("from cfg.InterStore (type-asserted with nil-safe fallback). Callers that")
	f.Comment("need a populated store must pass a Config built via the standard client")
	f.Comment("constructor; the EdgeQuery's own inters slice is no longer carried.")
	f.Func().Id("New"+queryName+"FromEdge").Params(
		jen.Id("cfg").Qual(runtimePkg, "Config"),
		jen.Id("eq").Op("*").Qual(runtimePkg, "EdgeQuery"),
	).Op("*").Id(queryName).BlockFunc(func(g *jen.Group) {
		// inters, _ := cfg.InterStore.(*entity.InterceptorStore)
		// if inters == nil { inters = &entity.InterceptorStore{} }
		g.List(jen.Id("inters"), jen.Id("_")).Op(":=").Id("cfg").Dot("InterStore").Assert(
			jen.Op("*").Qual(entityPkgImportPath, "InterceptorStore"),
		)
		g.If(jen.Id("inters").Op("==").Nil()).Block(
			jen.Id("inters").Op("=").Op("&").Qual(entityPkgImportPath, "InterceptorStore").Values(),
		)
		g.Return(jen.Op("&").Id(queryName).Values(jen.Dict{
			jen.Id("config"):     jen.Id("cfg"),
			jen.Id("ctx"):        jen.Id("eq").Dot("GetCtx").Call(),
			jen.Id("predicates"): jen.Id("eq").Dot("GetPredicates").Call(),
			jen.Id("order"):      jen.Id("eq").Dot("GetOrder").Call(),
			jen.Id("modifiers"):  jen.Id("eq").Dot("GetModifiers").Call(),
			jen.Id("inters"):     jen.Id("inters"),
			jen.Id("withFKs"):    jen.Id("eq").Dot("GetWithFKs").Call(),
			jen.Id("path"):       jen.Id("eq").Dot("GetPath").Call(),
		}))
	})

	// =========================================================================
	// querySpec — builds a *sqlgraph.QuerySpec from the query's direct fields.
	// Used by Count and IDs which call sqlgraph functions directly.
	// =========================================================================

	f.Comment("querySpec builds a *sqlgraph.QuerySpec from the query's direct fields.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("querySpec").Params().Op("*").Qual(sqlgraphPkg, "QuerySpec").Block(
		jen.Return(jen.Qual(runtimePkg, "MakeQuerySpec").Call(
			jen.Id(recv),
			jen.Qual(schemaPkg(), t.ID.Type.ConstName()),
		)),
	)

	// =========================================================================
	// buildQuery — construct selector for graph traversal
	// =========================================================================

	f.Comment("buildQuery constructs a *sql.Selector from the query state.")
	f.Comment("Used by QueryXxx methods to create a sub-select for graph traversal.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("buildQuery").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Qual(sqlPkg, "Selector"), jen.Error()).Block(
		jen.Return(jen.Qual(runtimePkg, "BuildQueryFrom").Call(jen.Id("ctx"), jen.Id(recv))),
	)

	// =========================================================================
	// buildSelector — fully-configured selector ready for execution
	// =========================================================================

	f.Comment("buildSelector constructs a fully-configured *sql.Selector ready for execution.")
	f.Comment("Adds column selection, FK columns, and DISTINCT on top of buildQuery.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("buildSelector").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Qual(sqlPkg, "Selector"), jen.Error()).Block(
		jen.Return(jen.Qual(runtimePkg, "BuildSelectorFrom").Call(jen.Id("ctx"), jen.Id(recv))),
	)

	// =========================================================================
	// Chainable methods (return entity.XxxQuerier interface)
	// =========================================================================

	// Where
	f.Commentf("Where adds predicates to the %s.", queryName)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Where").Params(
		jen.Id("ps").Op("...").Qual(h.PredicatePkg(), t.Name),
	).Qual(entityPkgPath, querierIface).BlockFunc(func(body *jen.Group) {
		body.For(jen.List(jen.Id("_"), jen.Id("p")).Op(":=").Range().Id("ps")).Block(
			jen.Id(recv).Dot("predicates").Op("=").Append(
				jen.Id(recv).Dot("predicates"), jen.Id("p"),
			),
		)
		body.Return(jen.Id(recv))
	})

	// Limit
	f.Comment("Limit the number of records to be returned by this query.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Limit").Params(
		jen.Id("n").Int(),
	).Qual(entityPkgPath, querierIface).Block(
		jen.Id(recv).Dot("ctx").Dot("Limit").Op("=").Op("&").Id("n"),
		jen.Return(jen.Id(recv)),
	)

	// Offset
	f.Comment("Offset to start from.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Offset").Params(
		jen.Id("n").Int(),
	).Qual(entityPkgPath, querierIface).Block(
		jen.Id(recv).Dot("ctx").Dot("Offset").Op("=").Op("&").Id("n"),
		jen.Return(jen.Id(recv)),
	)

	// Unique
	f.Comment("Unique configures the query builder to filter duplicate records.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Unique").Params(
		jen.Id("unique").Bool(),
	).Qual(entityPkgPath, querierIface).Block(
		jen.Id(recv).Dot("ctx").Dot("Unique").Op("=").Op("&").Id("unique"),
		jen.Return(jen.Id(recv)),
	)

	// Order
	f.Comment("Order specifies how the records should be ordered.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Order").Params(
		jen.Id("o").Op("...").Func().Params(jen.Op("*").Qual(sqlPkg, "Selector")),
	).Qual(entityPkgPath, querierIface).Block(
		jen.Id(recv).Dot("order").Op("=").Append(
			jen.Id(recv).Dot("order"), jen.Id("o").Op("..."),
		),
		jen.Return(jen.Id(recv)),
	)

	// =========================================================================
	// WithXxx edge eager-loading methods — stores concrete *XxxQuery
	// =========================================================================

	for _, edge := range t.Edges {
		edgeName := edge.StructField()
		withName := "With" + edgeName
		targetIface := edge.Type.Name + "Querier"
		targetQueryName := edge.Type.Name + "Query"
		callbackField := edgeCallbackField(edge)
		ownFK := edge.OwnFK()

		f.Commentf("%s tells the query-builder to eager-load the %q edge.", withName, edge.Name)
		f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id(withName).Params(
			jen.Id("opts").Op("...").Func().Params(jen.Qual(entityPkgPath, targetIface)),
		).Qual(entityPkgPath, querierIface).BlockFunc(func(body *jen.Group) {
			body.Id("tq").Op(":=").Id("New" + targetQueryName).Call(jen.Id(recv).Dot("config"))
			// Thread the parent's interceptors into the child query so
			// client.Intercept() fires on eager-loads too.
			body.Id("tq").Dot("inters").Op("=").Id(recv).Dot("inters")
			body.For(jen.List(jen.Id("_"), jen.Id("opt")).Op(":=").Range().Id("opts")).Block(
				jen.Id("opt").Call(jen.Id("tq")),
			)
			body.Id(recv).Dot(callbackField).Op("=").Id("tq")
			// Enable FK column selection for M2O and O2O-inverse edges
			// where the FK resides on this entity's table.
			if ownFK {
				body.Id(recv).Dot("withFKs").Op("=").True()
			}
			body.Return(jen.Id(recv))
		})
	}

	// =========================================================================
	// WithNamedXxx — named edge loading (FeatureNamedEdges)
	// =========================================================================

	if namedEdgesEnabled {
		for _, edge := range t.Edges {
			if edge.Unique {
				continue // Named edges only apply to non-unique (O2M/M2M) edges.
			}
			edgeName := edge.StructField()
			withNamedName := "WithNamed" + edgeName
			targetQueryName := edge.Type.Name + "Query"
			namedField := "withNamed" + edgeName

			f.Commentf("%s tells the query-builder to eager-load the %q edge with the given name.", withNamedName, edge.Name)
			f.Commentf("The optional arguments are used to configure the query builder of the edge.")
			f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id(withNamedName).Params(
				jen.Id("name").String(),
				jen.Id("opts").Op("...").Func().Params(jen.Op("*").Id(targetQueryName)),
			).Op("*").Id(queryName).BlockFunc(func(body *jen.Group) {
				body.Id("query").Op(":=").Id("New" + targetQueryName).Call(jen.Id(recv).Dot("config"))
				// Thread the parent's interceptors into the child query
				// so client.Intercept() fires on named eager-loads too.
				body.Id("query").Dot("inters").Op("=").Id(recv).Dot("inters")
				body.For(jen.List(jen.Id("_"), jen.Id("opt")).Op(":=").Range().Id("opts")).Block(
					jen.Id("opt").Call(jen.Id("query")),
				)
				body.If(jen.Id(recv).Dot(namedField).Op("==").Nil()).Block(
					jen.Id(recv).Dot(namedField).Op("=").Make(jen.Map(jen.String()).Op("*").Id(targetQueryName)),
				)
				body.Id(recv).Dot(namedField).Index(jen.Id("name")).Op("=").Id("query")
				body.Return(jen.Id(recv))
			})
		}
	}

	// =========================================================================
	// QueryXxx edge traversal methods
	// =========================================================================

	for _, edge := range t.Edges {
		edgeName := edge.StructField()
		methodName := "Query" + edgeName
		targetIface := edge.Type.Name + "Querier"
		targetQueryName := edge.Type.Name + "Query"

		// Get the entity sub-package paths
		srcEntitySubPkg := h.LeafPkgPath(t)
		targetEntitySubPkg := h.LeafPkgPath(edge.Type)

		f.Commentf("%s chains the current query on the %q edge.", methodName, edge.Name)
		f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id(methodName).Params().Qual(entityPkgPath, targetIface).BlockFunc(func(grp *jen.Group) {
			// Create new target query (SAME PACKAGE!)
			grp.Id("tq").Op(":=").Id("New" + targetQueryName).Call(jen.Id(recv).Dot("config"))
			// Thread the parent's interceptors into the child query so
			// client.Intercept() fires on chained edge traversals too.
			grp.Id("tq").Dot("inters").Op("=").Id(recv).Dot("inters")

			// Set up the path closure for sub-select traversal
			grp.Id("tq").Dot("path").Op("=").Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
			).Params(
				jen.Op("*").Qual(sqlPkg, "Selector"),
				jen.Error(),
			).BlockFunc(func(body *jen.Group) {
				body.List(jen.Id("from"), jen.Err()).Op(":=").Id(recv).Dot("buildQuery").Call(jen.Id("ctx"))
				body.If(jen.Err().Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Err()),
				)

				// Edge columns
				var edgeColumns jen.Code
				if edge.M2M() {
					edgeColumns = jen.Qual(srcEntitySubPkg, edge.PKConstant()).Op("...")
				} else {
					edgeColumns = jen.Qual(srcEntitySubPkg, edge.ColumnConstant())
				}

				// Target-package To (Ent style)
				body.Id("step").Op(":=").Qual(sqlgraphPkg, "NewStep").Call(
					jen.Qual(sqlgraphPkg, "From").Call(jen.Qual(srcEntitySubPkg, "Table"), jen.Qual(srcEntitySubPkg, t.ID.Constant())),
					jen.Qual(sqlgraphPkg, "To").Call(
						jen.Qual(targetEntitySubPkg, "Table"),
						jen.Qual(targetEntitySubPkg, "FieldID"),
					),
					jen.Qual(sqlgraphPkg, "Edge").Call(
						jen.Qual(sqlgraphPkg, h.EdgeRelType(edge)),
						jen.Lit(edge.IsInverse()),
						jen.Qual(srcEntitySubPkg, edge.TableConstant()),
						edgeColumns,
					),
				)
				body.Id("step").Dot("From").Dot("V").Op("=").Id("from")
				// Schema config stamping for multi-tenancy.
				if schemaConfigEnabled {
					body.Id("schemaConfig").Op(":=").Id(recv).Dot("schemaConfig")
					for _, stmt := range genSchemaConfigStampStep(t, edge) {
						body.Add(stmt)
					}
				}
				body.Return(
					jen.Qual(sqlgraphPkg, "SetNeighbors").Call(
						jen.Id(recv).Dot("config").Dot("Driver").Dot("Dialect").Call(),
						jen.Id("step"),
					),
					jen.Nil(),
				)
			})
			grp.Return(jen.Id("tq"))
		})
	}

	// =========================================================================
	// Clone
	// =========================================================================

	f.Commentf("Clone returns a duplicate of the %s builder.", queryName)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Clone").Params().Qual(entityPkgPath, querierIface).Block(
		jen.Return(jen.Id(recv).Dot("clone").Call()),
	)

	// =========================================================================
	// Terminal methods
	// =========================================================================

	// sqlAll — actual scan + edge loading logic, extracted for interceptor support.
	f.Commentf("sqlAll executes the SQL query and returns scanned %s entities.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("sqlAll").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Op("*").Add(entityType()), jen.Error()).BlockFunc(func(allBody *jen.Group) {
		// Push SchemaConfig into context so where predicates can read it.
		if schemaConfigEnabled {
			allBody.Id("ctx").Op("=").Qual(h.InternalPkg(), "NewSchemaConfigContext").Call(
				jen.Id("ctx"),
				jen.Id(recv).Dot("schemaConfig"),
			)
		}
		allBody.List(jen.Id("nodes"), jen.Err()).Op(":=").Qual(runtimePkg, "ScanAll").Types(
			entityType(), jen.Op("*").Add(entityType()),
		).Call(
			jen.Id("ctx"), jen.Id(recv).Dot("config").Dot("Driver"), jen.Id(recv).Dot("buildSelector"),
		)
		allBody.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)
		allBody.If(jen.Len(jen.Id("nodes")).Op("==").Lit(0)).Block(
			jen.Return(jen.Id("nodes"), jen.Nil()),
		)

		// Phase 1 — Standard eager loading.
		for _, edge := range t.Edges {
			edgeField := edge.StructField()
			callbackField := edgeCallbackField(edge)
			loaderName := "load" + edgeField
			targetEntityType := func() *jen.Statement { return jen.Qual(entityPkgPath, edge.Type.Name) }

			allBody.If(jen.Id("query").Op(":=").Id(recv).Dot(callbackField), jen.Id("query").Op("!=").Nil()).BlockFunc(func(ifBody *jen.Group) {
				// init callback
				var initFn *jen.Statement
				if edge.Unique {
					// O2O/M2O: just mark loaded.
					initFn = jen.Func().Params(jen.Id("n").Op("*").Add(entityType())).Block(
						jen.Id("n").Dot("Edges").Dot("Mark" + edgeField + "Loaded").Call(),
					)
				} else {
					// O2M/M2M: init empty slice + mark loaded.
					initFn = jen.Func().Params(jen.Id("n").Op("*").Add(entityType())).Block(
						jen.Id("n").Dot("Edges").Dot(edgeField).Op("=").Index().Op("*").Add(targetEntityType()).Values(),
						jen.Id("n").Dot("Edges").Dot("Mark"+edgeField+"Loaded").Call(),
					)
				}

				// assign callback
				var assignFn *jen.Statement
				if edge.Unique {
					assignFn = jen.Func().Params(
						jen.Id("n").Op("*").Add(entityType()),
						jen.Id("e").Op("*").Add(targetEntityType()),
					).BlockFunc(func(fnBody *jen.Group) {
						fnBody.Id("n").Dot("Edges").Dot(edgeField).Op("=").Id("e")
						// Back-reference: if the child has an inverse unique edge, set it.
						if edge.Ref != nil && edge.Ref.Unique {
							refField := edge.Ref.StructField()
							fnBody.If(jen.Op("!").Id("e").Dot("Edges").Dot(refField+"Loaded").Call()).Block(
								jen.Id("e").Dot("Edges").Dot(refField).Op("=").Id("n"),
								jen.Id("e").Dot("Edges").Dot("Mark"+refField+"Loaded").Call(),
							)
						}
					})
				} else {
					assignFn = jen.Func().Params(
						jen.Id("n").Op("*").Add(entityType()),
						jen.Id("e").Op("*").Add(targetEntityType()),
					).BlockFunc(func(fnBody *jen.Group) {
						fnBody.Id("n").Dot("Edges").Dot(edgeField).Op("=").Append(
							jen.Id("n").Dot("Edges").Dot(edgeField), jen.Id("e"),
						)
						// Back-reference for O2M.
						if edge.Ref != nil && edge.Ref.Unique {
							refField := edge.Ref.StructField()
							fnBody.If(jen.Op("!").Id("e").Dot("Edges").Dot(refField+"Loaded").Call()).Block(
								jen.Id("e").Dot("Edges").Dot(refField).Op("=").Id("n"),
								jen.Id("e").Dot("Edges").Dot("Mark"+refField+"Loaded").Call(),
							)
						}
					})
				}

				ifBody.If(
					jen.Err().Op(":=").Id(recv).Dot(loaderName).Call(
						jen.Id("ctx"), jen.Id("query"), jen.Id("nodes"),
						initFn, assignFn,
					),
					jen.Err().Op("!=").Nil(),
				).Block(
					jen.Return(jen.Nil(), jen.Err()),
				)
			})
		}

		// Phase 2 — Named edge variants (FeatureNamedEdges).
		if namedEdgesEnabled {
			for _, edge := range t.Edges {
				if edge.Unique {
					continue
				}
				edgeField := edge.StructField()
				loaderName := "load" + edgeField
				namedField := "withNamed" + edgeField
				targetEntityType := func() *jen.Statement { return jen.Qual(entityPkgPath, edge.Type.Name) }

				allBody.For(
					jen.List(jen.Id("name"), jen.Id("query")).Op(":=").Range().Id(recv).Dot(namedField),
				).BlockFunc(func(forBody *jen.Group) {
					initFn := jen.Func().Params(jen.Id("n").Op("*").Add(entityType())).Block(
						jen.Id("n").Dot("AppendNamed" + edgeField).Call(jen.Id("name")),
					)
					assignFn := jen.Func().Params(
						jen.Id("n").Op("*").Add(entityType()),
						jen.Id("e").Op("*").Add(targetEntityType()),
					).BlockFunc(func(fnBody *jen.Group) {
						fnBody.Id("n").Dot("AppendNamed"+edgeField).Call(jen.Id("name"), jen.Id("e"))
						if edge.Ref != nil && edge.Ref.Unique {
							refField := edge.Ref.StructField()
							fnBody.If(jen.Op("!").Id("e").Dot("Edges").Dot(refField+"Loaded").Call()).Block(
								jen.Id("e").Dot("Edges").Dot(refField).Op("=").Id("n"),
								jen.Id("e").Dot("Edges").Dot("Mark"+refField+"Loaded").Call(),
							)
						}
					})
					forBody.If(
						jen.Err().Op(":=").Id(recv).Dot(loaderName).Call(
							jen.Id("ctx"), jen.Id("query"), jen.Id("nodes"),
							initFn, assignFn,
						),
						jen.Err().Op("!=").Nil(),
					).Block(
						jen.Return(jen.Nil(), jen.Err()),
					)
				})
			}
		}

		// Phase 3 — loadTotal registry loop.
		allBody.For(jen.Id("i").Op(":=").Range().Id(recv).Dot("loadTotal")).Block(
			jen.If(jen.Err().Op(":=").Id(recv).Dot("loadTotal").Index(jen.Id("i")).Call(jen.Id("ctx"), jen.Id("nodes")), jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Err()),
			),
		)

		// Inject runtime config so entity-level methods can access the driver.
		allBody.For(jen.List(jen.Id("_"), jen.Id("node")).Op(":=").Range().Id("nodes")).Block(
			jen.Id("node").Dot(t.SetConfigMethodName()).Call(jen.Id(recv).Dot("config")),
		)

		allBody.Return(jen.Id("nodes"), jen.Nil())
	})

	// prepareQuery — evaluates the privacy policy (if any) and then runs
	// Traversers from the interceptor list (Ent-style). Privacy is no
	// longer part of the interceptor chain — it is invoked explicitly
	// here via q.policy.EvalQuery(). Interceptors never see the privacy
	// call at all.
	f.Comment("prepareQuery evaluates the privacy policy (if any) and runs")
	f.Comment("Traversers from the interceptor list. Privacy is invoked")
	f.Comment("explicitly here — it is NOT part of the interceptor chain.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("prepareQuery").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().BlockFunc(func(body *jen.Group) {
		if hasPolicy {
			body.If(jen.Id(recv).Dot("policy").Op("!=").Nil()).Block(
				jen.If(
					jen.Err().Op(":=").Id(recv).Dot("policy").Dot("EvalQuery").Call(
						jen.Id("ctx"), jen.Id(recv),
					),
					jen.Err().Op("!=").Nil(),
				).Block(
					jen.Return(jen.Err()),
				),
			)
		}
		body.Return(jen.Qual(runtimePkg, "RunTraversers").Call(
			jen.Id("ctx"), jen.Id(recv), intersField(recv),
		))
	})

	// All — wraps sqlAll with interceptor support (Ent-style).
	f.Commentf("All executes the query and returns a list of %s.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("All").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Op("*").Add(entityType()), jen.Error()).BlockFunc(func(allBody *jen.Group) {
		veloxPkg := h.VeloxPkg()

		// ctx = setContextOp(ctx, _q.ctx, velox.OpQueryAll)
		allBody.Id("ctx").Op("=").Id("setContextOp").Call(
			jen.Id("ctx"), jen.Id(recv).Dot("ctx"), jen.Qual(veloxPkg, "OpQueryAll"),
		)
		// prepareQuery first (runs Traversers), then interceptor chain.
		allBody.If(jen.Err().Op(":=").Id(recv).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)
		allBody.If(jen.Len(intersField(recv)).Op(">").Lit(0)).Block(
			jen.Return(jen.Qual(veloxPkg, "WithInterceptors").Types(
				jen.Index().Op("*").Add(entityType()),
			).Call(
				jen.Id("ctx"),
				jen.Id(recv),
				jen.Id("querierAll").Types(
					jen.Index().Op("*").Add(entityType()),
					jen.Op("*").Id(queryName),
				).Call(),
				intersField(recv),
			)),
		)
		allBody.Return(jen.Id(recv).Dot("sqlAll").Call(jen.Id("ctx")))
	})

	// AllX — panics on error.
	f.Commentf("AllX is like All, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("AllX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Index().Op("*").Add(entityType()).Block(
		jen.List(jen.Id("nodes"), jen.Err()).Op(":=").Id(recv).Dot("All").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("nodes")),
	)

	// First — delegates to All with limit 1.
	f.Commentf("First returns the first %s entity from the query.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("First").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Add(entityType()), jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Id("clone").Op(":=").Id(recv).Dot("clone").Call()
		body.Id("clone").Dot("ctx").Dot("Limit").Op("=").Id("intP").Call(jen.Lit(1))
		body.List(jen.Id("nodes"), jen.Err()).Op(":=").Id("clone").Dot("All").Call(
			jen.Id("setContextOp").Call(jen.Id("ctx"), jen.Id(recv).Dot("ctx"), jen.Qual(h.VeloxPkg(), "OpQueryFirst")),
		)
		body.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)
		body.If(jen.Len(jen.Id("nodes")).Op("==").Lit(0)).Block(
			jen.Return(jen.Nil(), jen.Qual(runtimePkg, "NewNotFoundError").Call(jen.Lit(t.Name))),
		)
		body.Return(jen.Id("nodes").Index(jen.Lit(0)), jen.Nil())
	})

	// FirstX — panics on error.
	f.Commentf("FirstX is like First, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("FirstX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Op("*").Add(entityType()).Block(
		jen.List(jen.Id("node"), jen.Err()).Op(":=").Id(recv).Dot("First").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("node")),
	)

	// Only — delegates to All with limit 2.
	f.Commentf("Only returns a single %s entity found by the query, ensuring it only returns one.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Only").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Add(entityType()), jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Id("clone").Op(":=").Id(recv).Dot("clone").Call()
		body.Id("clone").Dot("ctx").Dot("Limit").Op("=").Id("intP").Call(jen.Lit(2))
		body.List(jen.Id("nodes"), jen.Err()).Op(":=").Id("clone").Dot("All").Call(
			jen.Id("setContextOp").Call(jen.Id("ctx"), jen.Id(recv).Dot("ctx"), jen.Qual(h.VeloxPkg(), "OpQueryOnly")),
		)
		body.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)
		body.Switch(jen.Len(jen.Id("nodes"))).Block(
			jen.Case(jen.Lit(0)).Block(
				jen.Return(jen.Nil(), jen.Qual(runtimePkg, "NewNotFoundError").Call(jen.Lit(t.Name))),
			),
			jen.Case(jen.Lit(1)).Block(
				jen.Return(jen.Id("nodes").Index(jen.Lit(0)), jen.Nil()),
			),
			jen.Default().Block(
				jen.Return(jen.Nil(), jen.Qual(runtimePkg, "NewNotSingularError").Call(jen.Lit(t.Name))),
			),
		)
	})

	// OnlyX — panics on error.
	f.Commentf("OnlyX is like Only, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("OnlyX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Op("*").Add(entityType()).Block(
		jen.List(jen.Id("node"), jen.Err()).Op(":=").Id(recv).Dot("Only").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("node")),
	)

	// sqlCount — actual count execution, extracted for interceptor support.
	f.Comment("sqlCount executes the SQL COUNT query and returns the result.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("sqlCount").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Int(), jen.Error()).BlockFunc(func(body *jen.Group) {
		// Resolve graph traversal path.
		body.Var().Id("from").Op("*").Qual(sqlPkg, "Selector")
		body.If(jen.Id(recv).Dot("path").Op("!=").Nil()).BlockFunc(func(ifBody *jen.Group) {
			ifBody.Var().Id("err").Error()
			ifBody.List(jen.Id("from"), jen.Id("err")).Op("=").Id(recv).Dot("path").Call(jen.Id("ctx"))
			ifBody.If(jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Lit(0), jen.Err()),
			)
		})
		// Build a spec with nil columns so the SQL is COUNT(*).
		body.Id("spec").Op(":=").Id(recv).Dot("querySpec").Call()
		body.Id("spec").Dot("Node").Dot("Columns").Op("=").Nil()
		body.Id("spec").Dot("From").Op("=").Id("from")
		body.Return(jen.Qual(sqlgraphPkg, "CountNodes").Call(
			jen.Id("ctx"), jen.Id(recv).Dot("config").Dot("Driver"), jen.Id("spec"),
		))
	})

	// Count — wraps sqlCount with interceptor support (Ent-style).
	f.Comment("Count returns the count of the given query.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Count").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Int(), jen.Error()).BlockFunc(func(body *jen.Group) {
		veloxPkg := h.VeloxPkg()
		// ctx = setContextOp(ctx, _q.ctx, velox.OpQueryCount)
		body.Id("ctx").Op("=").Id("setContextOp").Call(
			jen.Id("ctx"), jen.Id(recv).Dot("ctx"), jen.Qual(veloxPkg, "OpQueryCount"),
		)
		body.If(jen.Err().Op(":=").Id(recv).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Lit(0), jen.Err()),
		)
		body.If(jen.Len(intersField(recv)).Op(">").Lit(0)).Block(
			jen.Return(jen.Qual(veloxPkg, "WithInterceptors").Types(
				jen.Int(),
			).Call(
				jen.Id("ctx"),
				jen.Id(recv),
				jen.Id("querierCount").Types(
					jen.Op("*").Id(queryName),
				).Call(),
				intersField(recv),
			)),
		)
		body.Return(jen.Id(recv).Dot("sqlCount").Call(jen.Id("ctx")))
	})

	// CountX — panics on error.
	f.Comment("CountX is like Count, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("CountX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Int().Block(
		jen.List(jen.Id("count"), jen.Err()).Op(":=").Id(recv).Dot("Count").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("count")),
	)

	// Exist — uses FirstID (more efficient than Count — stops after 1 row).
	f.Comment("Exist returns true if the query has results.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Exist").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Bool(), jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Id("ctx").Op("=").Id("setContextOp").Call(
			jen.Id("ctx"), jen.Id(recv).Dot("ctx"), jen.Qual(h.VeloxPkg(), "OpQueryExist"),
		)
		body.List(jen.Id("_"), jen.Err()).Op(":=").Id(recv).Dot("FirstID").Call(jen.Id("ctx"))
		body.If(jen.Err().Op("==").Nil()).Block(
			jen.Return(jen.True(), jen.Nil()),
		)
		body.If(jen.Qual(runtimePkg, "IsNotFound").Call(jen.Err())).Block(
			jen.Return(jen.False(), jen.Nil()),
		)
		body.Return(jen.False(), jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: check existence: %w"), jen.Err()))
	})

	// ExistX — panics on error.
	f.Comment("ExistX is like Exist, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("ExistX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Bool().Block(
		jen.List(jen.Id("exist"), jen.Err()).Op(":=").Id(recv).Dot("Exist").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("exist")),
	)

	// SQL — returns the generated SQL string and args without executing.
	f.Comment("SQL returns the SQL query string and arguments for debugging.")
	f.Comment("It runs prepareQuery (privacy traversers) and builds the selector,")
	f.Comment("but does not execute the query.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("SQL").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.String(), jen.Index().Any(), jen.Error()).Block(
		jen.If(jen.Err().Op(":=").Id(recv).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Lit(""), jen.Nil(), jen.Err()),
		),
		jen.List(jen.Id("selector"), jen.Err()).Op(":=").Id(recv).Dot("buildSelector").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Lit(""), jen.Nil(), jen.Err()),
		),
		jen.List(jen.Id("query"), jen.Id("args")).Op(":=").Id("selector").Dot("Query").Call(),
		jen.Return(jen.Id("query"), jen.Id("args"), jen.Nil()),
	)

	// Explain — returns a *runtime.QueryPlan describing the query without executing.
	f.Comment("Explain returns the query's execution plan without executing it.")
	f.Comment("Includes the SQL, arguments, planned edge loads, and active interceptors.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Explain").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Qual(runtimePkg, "QueryPlan"), jen.Error()).BlockFunc(func(body *jen.Group) {
		// prepareQuery
		body.If(jen.Err().Op(":=").Id(recv).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)

		// Build selector
		body.List(jen.Id("selector"), jen.Err()).Op(":=").Id(recv).Dot("buildSelector").Call(jen.Id("ctx"))
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
		body.For(jen.List(jen.Id("_"), jen.Id("inter")).Op(":=").Range().Id(recv).Dot("inters").Dot(t.Name)).Block(
			jen.Id("plan").Dot("Interceptors").Op("=").Append(
				jen.Id("plan").Dot("Interceptors"),
				jen.Qual("fmt", "Sprintf").Call(jen.Lit("%T"), jen.Id("inter")),
			),
		)

		// Edge plans
		for _, edge := range t.Edges {
			edgeField := edgeCallbackField(edge)
			body.If(jen.Id(recv).Dot(edgeField).Op("!=").Nil()).BlockFunc(func(inner *jen.Group) {
				inner.List(jen.Id("eSel"), jen.Id("eErr")).Op(":=").Id(recv).Dot(edgeField).Dot("buildSelector").Call(jen.Id("ctx"))
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

	// IDs — user-facing method that wraps sqlIDs in the interceptor
	// chain. sqlIDs contains the actual SQL execution.
	f.Commentf("IDs executes the query and returns a list of %s IDs.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("IDs").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Add(idType), jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Id("ctx").Op("=").Id("setContextOp").Call(
			jen.Id("ctx"), jen.Id(recv).Dot("ctx"), jen.Qual(h.VeloxPkg(), "OpQueryIDs"),
		)
		body.If(jen.Id("err").Op(":=").Id(recv).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		)
		body.If(jen.Len(intersField(recv)).Op(">").Lit(0)).Block(
			jen.Return(jen.Qual(h.VeloxPkg(), "WithInterceptors").Types(
				jen.Index().Add(idType),
			).Call(
				jen.Id("ctx"),
				jen.Id(recv),
				jen.Id("querierIDs").Types(
					jen.Index().Add(idType),
					jen.Op("*").Id(queryName),
				).Call(),
				intersField(recv),
			)),
		)
		body.Return(jen.Id(recv).Dot("sqlIDs").Call(jen.Id("ctx")))
	})

	// sqlIDs — the actual SQL execution, wrapped by IDs above.
	f.Commentf("sqlIDs executes the ID-only SQL SELECT for %s.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("sqlIDs").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Add(idType), jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Var().Id("from").Op("*").Qual(sqlPkg, "Selector")
		body.If(jen.Id(recv).Dot("path").Op("!=").Nil()).BlockFunc(func(ifBody *jen.Group) {
			ifBody.Var().Id("err").Error()
			ifBody.List(jen.Id("from"), jen.Id("err")).Op("=").Id(recv).Dot("path").Call(jen.Id("ctx"))
			ifBody.If(jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Err()),
			)
			ifBody.If(jen.Id(recv).Dot("ctx").Dot("Unique").Op("==").Nil()).Block(
				jen.Id(recv).Dot("Unique").Call(jen.True()),
			)
		})
		body.Id("spec").Op(":=").Id(recv).Dot("querySpec").Call()
		body.Id("spec").Dot("Node").Dot("Columns").Op("=").Index().String().Values(jen.Qual(entitySubPkg, "FieldID"))
		body.Id("spec").Dot("From").Op("=").Id("from")
		body.Var().Id("ids").Index().Add(idType)
		body.Id("spec").Dot("ScanValues").Op("=").Func().Params(jen.Id("_").Index().String()).Params(jen.Index().Any(), jen.Error()).Block(
			jen.Return(jen.Qual(runtimePkg, "IDScanValues").Call(jen.Qual(schemaPkg(), t.ID.Type.ConstName())), jen.Nil()),
		)
		body.Id("spec").Dot("Assign").Op("=").Func().Params(jen.Id("_").Index().String(), jen.Id("values").Index().Any()).Error().BlockFunc(func(fnBody *jen.Group) {
			fnBody.If(jen.Len(jen.Id("values")).Op("==").Lit(0)).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: IDs: no values returned"))),
			)
			fnBody.List(jen.Id("id"), jen.Id("err")).Op(":=").Qual(runtimePkg, "ExtractID").Call(jen.Id("values").Index(jen.Lit(0)), jen.Qual(schemaPkg(), t.ID.Type.ConstName()))
			fnBody.If(jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Err()),
			)
			fnBody.Id("ids").Op("=").Append(jen.Id("ids"), jen.Id("id").Assert(idType))
			fnBody.Return(jen.Nil())
		})
		body.If(jen.Err().Op(":=").Qual(sqlgraphPkg, "QueryNodes").Call(
			jen.Id("ctx"), jen.Id(recv).Dot("config").Dot("Driver"), jen.Id("spec"),
		), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Err()),
		)
		body.Return(jen.Id("ids"), jen.Nil())
	})

	// IDsX — panics on error.
	f.Commentf("IDsX is like IDs, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("IDsX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Index().Add(idType).Block(
		jen.List(jen.Id("ids"), jen.Err()).Op(":=").Id(recv).Dot("IDs").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("ids")),
	)

	// FirstID — clones, sets limit 1, calls IDs.
	f.Commentf("FirstID returns the first %s ID from the query.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("FirstID").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(idType, jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Var().Id("zero").Add(idType)
		body.Id("clone").Op(":=").Id(recv).Dot("clone").Call()
		body.Id("clone").Dot("ctx").Dot("Limit").Op("=").Id("intP").Call(jen.Lit(1))
		body.List(jen.Id("ids"), jen.Err()).Op(":=").Id("clone").Dot("IDs").Call(
			jen.Id("setContextOp").Call(jen.Id("ctx"), jen.Id(recv).Dot("ctx"), jen.Qual(h.VeloxPkg(), "OpQueryFirstID")),
		)
		body.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Id("zero"), jen.Err()),
		)
		body.If(jen.Len(jen.Id("ids")).Op("==").Lit(0)).Block(
			jen.Return(jen.Id("zero"), jen.Qual(runtimePkg, "NewNotFoundError").Call(jen.Lit(t.Name))),
		)
		body.Return(jen.Id("ids").Index(jen.Lit(0)), jen.Nil())
	})

	// FirstIDX — panics on error.
	f.Commentf("FirstIDX is like FirstID, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("FirstIDX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Add(idType).Block(
		jen.List(jen.Id("id"), jen.Err()).Op(":=").Id(recv).Dot("FirstID").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("id")),
	)

	// OnlyID — clones, sets limit 2, calls IDs.
	f.Commentf("OnlyID returns the only %s ID in the query.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("OnlyID").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(idType, jen.Error()).BlockFunc(func(body *jen.Group) {
		body.Var().Id("zero").Add(idType)
		body.Id("clone").Op(":=").Id(recv).Dot("clone").Call()
		body.Id("clone").Dot("ctx").Dot("Limit").Op("=").Id("intP").Call(jen.Lit(2))
		body.List(jen.Id("ids"), jen.Err()).Op(":=").Id("clone").Dot("IDs").Call(
			jen.Id("setContextOp").Call(jen.Id("ctx"), jen.Id(recv).Dot("ctx"), jen.Qual(h.VeloxPkg(), "OpQueryOnlyID")),
		)
		body.If(jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Id("zero"), jen.Err()),
		)
		body.Switch(jen.Len(jen.Id("ids"))).Block(
			jen.Case(jen.Lit(0)).Block(
				jen.Return(jen.Id("zero"), jen.Qual(runtimePkg, "NewNotFoundError").Call(jen.Lit(t.Name))),
			),
			jen.Case(jen.Lit(1)).Block(
				jen.Return(jen.Id("ids").Index(jen.Lit(0)), jen.Nil()),
			),
			jen.Default().Block(
				jen.Return(jen.Id("zero"), jen.Qual(runtimePkg, "NewNotSingularError").Call(jen.Lit(t.Name))),
			),
		)
	})

	// OnlyIDX — panics on error.
	f.Commentf("OnlyIDX is like OnlyID, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("OnlyIDX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Add(idType).Block(
		jen.List(jen.Id("id"), jen.Err()).Op(":=").Id(recv).Dot("OnlyID").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("id")),
	)

	// =========================================================================
	// ForUpdate and ForShare — row-level locking
	// =========================================================================

	f.Comment("ForUpdate locks the selected rows against concurrent updates, and prevent them from being")
	f.Comment("updated, deleted or \"selected ... for update\" by other sessions, until the transaction is")
	f.Comment("either committed or rolled-back.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("ForUpdate").Params(
		jen.Id("opts").Op("...").Qual(sqlPkg, "LockOption"),
	).Qual(entityPkgPath, querierIface).Block(
		jen.If(jen.Id(recv).Dot("config").Dot("Driver").Dot("Dialect").Call().Op("==").Qual(dialectPkg(), "Postgres")).Block(
			jen.Id(recv).Dot("Unique").Call(jen.False()),
		),
		jen.Id(recv).Dot("modifiers").Op("=").Append(
			jen.Id(recv).Dot("modifiers"),
			jen.Func().Params(jen.Id("s").Op("*").Qual(sqlPkg, "Selector")).Block(
				jen.Id("s").Dot("ForUpdate").Call(jen.Id("opts").Op("...")),
			),
		),
		jen.Return(jen.Id(recv)),
	)

	f.Comment("ForShare behaves similarly to ForUpdate, except that it acquires a shared mode lock")
	f.Comment("on any rows that are read. Other sessions can read the rows, but cannot modify them")
	f.Comment("until your transaction commits.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("ForShare").Params(
		jen.Id("opts").Op("...").Qual(sqlPkg, "LockOption"),
	).Qual(entityPkgPath, querierIface).Block(
		jen.If(jen.Id(recv).Dot("config").Dot("Driver").Dot("Dialect").Call().Op("==").Qual(dialectPkg(), "Postgres")).Block(
			jen.Id(recv).Dot("Unique").Call(jen.False()),
		),
		jen.Id(recv).Dot("modifiers").Op("=").Append(
			jen.Id(recv).Dot("modifiers"),
			jen.Func().Params(jen.Id("s").Op("*").Qual(sqlPkg, "Selector")).Block(
				jen.Id("s").Dot("ForShare").Call(jen.Id("opts").Op("...")),
			),
		),
		jen.Return(jen.Id(recv)),
	)

	// =========================================================================
	// QueryReader getters — implement runtime.QueryReader interface
	// =========================================================================

	f.Comment("GetDriver returns the dialect driver. Implements runtime.QueryReader.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("GetDriver").Params().Qual(dialectPkg(), "Driver").Block(
		jen.Return(jen.Id(recv).Dot("config").Dot("Driver")),
	)

	f.Comment("GetTable returns the primary table name. Implements runtime.QueryReader.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("GetTable").Params().String().Block(
		jen.Return(jen.Qual(entitySubPkg, "Table")),
	)

	f.Comment("GetColumns returns the default column list. Implements runtime.QueryReader.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("GetColumns").Params().Index().String().Block(
		jen.Return(jen.Qual(entitySubPkg, "Columns")),
	)

	f.Comment("GetFKColumns returns the foreign-key columns. Implements runtime.QueryReader.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("GetFKColumns").Params().Index().String().Block(
		jen.Return(jen.Qual(entitySubPkg, "ForeignKeys")),
	)

	f.Comment("GetIDFieldType returns the schema type of the ID field. Implements runtime.QueryReader.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("GetIDFieldType").Params().Qual(schemaPkg(), "Type").Block(
		jen.Return(jen.Qual(schemaPkg(), t.ID.Type.ConstName())),
	)

	f.Comment("GetPath returns the graph-traversal path function. Implements runtime.QueryReader.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("GetPath").Params().Func().Params(
		jen.Qual("context", "Context"),
	).Params(jen.Op("*").Qual(sqlPkg, "Selector"), jen.Error()).Block(
		jen.Return(jen.Id(recv).Dot("path")),
	)

	f.Comment("GetPredicates returns the registered WHERE predicates. Implements runtime.QueryReader.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("GetPredicates").Params().Index().Func().Params(
		jen.Op("*").Qual(sqlPkg, "Selector"),
	).Block(
		jen.Return(jen.Id(recv).Dot("predicates")),
	)

	f.Comment("GetOrder returns the registered ORDER BY functions. Implements runtime.QueryReader.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("GetOrder").Params().Index().Func().Params(
		jen.Op("*").Qual(sqlPkg, "Selector"),
	).Block(
		jen.Return(jen.Id(recv).Dot("order")),
	)

	f.Comment("GetModifiers returns the registered query modifiers. Implements runtime.QueryReader.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("GetModifiers").Params().Index().Func().Params(
		jen.Op("*").Qual(sqlPkg, "Selector"),
	).Block(
		jen.Return(jen.Id(recv).Dot("modifiers")),
	)

	f.Comment("GetWithFKs returns whether FK columns should be included. Implements runtime.QueryReader.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("GetWithFKs").Params().Bool().Block(
		jen.Return(jen.Id(recv).Dot("withFKs")),
	)

	// =========================================================================
	// Select — returns *UserSelect which embeds runtime.Selector for scalar methods
	// =========================================================================

	selectName := t.Name + "Select"
	f.Commentf("Select allows the selection of one or more fields/columns for the given query,")
	f.Commentf("returning a %s builder with scalar accessor methods (Strings, Ints, etc.).", selectName)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Select").Params(
		jen.Id("fields").Op("...").String(),
	).Qual(entityPkgPath, t.Name+"Selector").Block(
		jen.Id(recv).Dot("ctx").Dot("Fields").Op("=").Append(
			jen.Id(recv).Dot("ctx").Dot("Fields"),
			jen.Id("fields").Op("..."),
		),
		jen.Id("s").Op(":=").Op("&").Id(selectName).Values(jen.Dict{
			jen.Id(queryName): jen.Id(recv),
		}),
		// Wire the public Scan method (which runs through the
		// interceptor chain), NOT the raw sqlScan helper. This is what
		// makes .Strings() / .Ints() / .Int() / etc. honor interceptors
		// — all of those runtime.Selector terminal methods call the
		// scan function stored here.
		jen.Id("s").Dot("Selector").Op("=").Qual(runtimePkg, "NewSelector").Call(
			jen.Lit(selectName),
			jen.Op("&").Id(recv).Dot("ctx").Dot("Fields"),
			jen.Id("s").Dot("Scan"),
		),
		jen.Return(jen.Id("s")),
	)

	// =========================================================================
	// Modify — adds query modifier for attaching custom logic to queries
	// =========================================================================

	f.Commentf("Modify adds a query modifier for attaching custom logic to queries.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Modify").Params(
		jen.Id("modifiers").Op("...").Func().Params(jen.Op("*").Qual(sqlPkg, "Selector")),
	).Qual(entityPkgPath, querierIface).Block(
		jen.Id(recv).Dot("modifiers").Op("=").Append(jen.Id(recv).Dot("modifiers"), jen.Id("modifiers").Op("...")),
		jen.Return(jen.Id(recv)),
	)

	// =========================================================================
	// Aggregate without GroupBy — routes through Select, matching Ent's API
	// =========================================================================

	f.Commentf("Aggregate returns a %s configured with the given aggregations.", selectName)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Aggregate").Params(
		jen.Id("fns").Op("...").Qual(runtimePkg, "AggregateFunc"),
	).Qual(entityPkgPath, t.Name+"Selector").Block(
		jen.Return(jen.Id(recv).Dot("Select").Call().Dot("Aggregate").Call(jen.Id("fns").Op("..."))),
	)

	// =========================================================================
	// GroupBy — groups vertices by one or more fields/columns
	// =========================================================================

	gbName := t.Name + "GroupBy"
	f.Commentf("GroupBy is used to group vertices by one or more fields/columns.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("GroupBy").Params(
		jen.Id("field").String(),
		jen.Id("fields").Op("...").String(),
	).Qual(entityPkgPath, t.Name+"GroupByer").Block(
		jen.Id("g").Op(":=").Op("&").Id(gbName).Values(jen.Dict{
			jen.Id("build"):  jen.Id(recv),
			jen.Id("fields"): jen.Append(jen.Index().String().Values(jen.Id("field")), jen.Id("fields").Op("...")),
		}),
		// Wire the public Scan method (which runs through the
		// interceptor chain), NOT the raw sqlScan helper. Same
		// rationale as UserSelect above.
		jen.Id("g").Dot("Selector").Op("=").Qual(runtimePkg, "NewSelector").Call(
			jen.Lit(gbName),
			jen.Op("&").Id("g").Dot("fields"),
			jen.Id("g").Dot("Scan"),
		),
		jen.Return(jen.Id("g")),
	)

	// =========================================================================
	// Scan / ScanX — direct scan without Select
	// =========================================================================

	f.Comment("Scan applies the selector query and scans the result into the given value.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("Scan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Error().Block(
		jen.If(jen.Err().Op(":=").Id(recv).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.Return(jen.Qual(runtimePkg, "QueryScan").Call(
			jen.Id("ctx"), jen.Id(recv), jen.Id("v"),
		)),
	)

	f.Comment("ScanX is like Scan, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("ScanX").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Block(
		jen.If(jen.Err().Op(":=").Id(recv).Dot("Scan").Call(jen.Id("ctx"), jen.Id("v")), jen.Err().Op("!=").Nil()).Block(
			jen.Panic(jen.Err()),
		),
	)

	// =========================================================================
	// UserSelect type and methods
	// =========================================================================

	f.Commentf("%s is the builder for selecting fields of %s entities.", selectName, t.Name)
	f.Type().Id(selectName).Struct(
		jen.Op("*").Id(queryName),
		jen.Qual(runtimePkg, "Selector"),
	)

	f.Comment("Aggregate adds the given aggregation functions to the selector query.")
	f.Func().Params(jen.Id("s").Op("*").Id(selectName)).Id("Aggregate").Params(
		jen.Id("fns").Op("...").Qual(runtimePkg, "AggregateFunc"),
	).Qual(entityPkgPath, t.Name+"Selector").Block(
		jen.Id("s").Dot("AppendFns").Call(jen.Id("fns").Op("...")),
		jen.Return(jen.Id("s")),
	)

	// sqlScan for UserSelect — raw SQL execution that sqlScan is
	// wired to via runtime.Selector.Scan. Uses QuerySelect (not
	// QueryScan) to honor aggregate functions registered via
	// Aggregate(...). When no aggregates are present, QuerySelect
	// behaves identically to QueryScan.
	f.Func().Params(jen.Id("s").Op("*").Id(selectName)).Id("sqlScan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Error().Block(
		jen.Return(jen.Qual(runtimePkg, "QuerySelect").Call(
			jen.Id("ctx"),
			jen.Id("s").Dot(queryName),
			jen.Id("s").Dot("Fns").Call(),
			jen.Id("v"),
		)),
	)

	// selectIntersExpr returns the Jen expression for the interceptor slice
	// when accessed through a select/groupby receiver that holds the query
	// via s.{queryName} or g.build. Always the direct per-entity slice —
	// privacy is no longer part of the interceptor chain (see prepareQuery).
	selectIntersExpr := func(queryAccess *jen.Statement) *jen.Statement {
		return queryAccess.Clone().Dot("inters").Dot(t.Name)
	}

	// Scan and ScanX on *XxxSelect — explicit methods disambiguate
	// the promoted ScanX from runtime.Selector vs *XxxQuery (both
	// embed into XxxSelect). Scan threads the call through the
	// parent UserQuery's interceptor chain before running sqlScan
	// so client.Intercept() fires on .Strings() / .Int() / etc.
	// Uses runtime.ScanWithInterceptors to avoid per-entity boilerplate.
	f.Comment("Scan applies the selector query and scans the result into the given value.")
	f.Func().Params(jen.Id("s").Op("*").Id(selectName)).Id("Scan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Error().BlockFunc(func(body *jen.Group) {
		body.Id("ctx").Op("=").Id("setContextOp").Call(
			jen.Id("ctx"), jen.Id("s").Dot(queryName).Dot("ctx"), jen.Qual(h.VeloxPkg(), "OpQuerySelect"),
		)
		body.If(jen.Err().Op(":=").Id("s").Dot(queryName).Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Err()),
		)
		body.Return(jen.Qual(runtimePkg, "ScanWithInterceptors").Call(
			jen.Id("ctx"),
			jen.Id("s").Dot(queryName),
			selectIntersExpr(jen.Id("s").Dot(queryName)),
			jen.Id("s").Dot("sqlScan"),
			jen.Id("v"),
		))
	})

	f.Comment("ScanX is like Scan, but panics if an error occurs.")
	f.Func().Params(jen.Id("s").Op("*").Id(selectName)).Id("ScanX").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Block(
		jen.If(jen.Err().Op(":=").Id("s").Dot("Scan").Call(jen.Id("ctx"), jen.Id("v")), jen.Err().Op("!=").Nil()).Block(
			jen.Panic(jen.Err()),
		),
	)

	// =========================================================================
	// UserGroupBy type and methods
	// =========================================================================

	f.Commentf("%s is the group-by builder for %s entities.", gbName, t.Name)
	f.Type().Id(gbName).StructFunc(func(group *jen.Group) {
		group.Qual(runtimePkg, "Selector")
		group.Id("build").Op("*").Id(queryName)
		group.Id("fields").Index().String()
	})

	f.Comment("Aggregate adds the given aggregation functions to the group-by query.")
	f.Func().Params(jen.Id("g").Op("*").Id(gbName)).Id("Aggregate").Params(
		jen.Id("fns").Op("...").Qual(runtimePkg, "AggregateFunc"),
	).Qual(entityPkgPath, t.Name+"GroupByer").Block(
		jen.Id("g").Dot("AppendFns").Call(jen.Id("fns").Op("...")),
		jen.Return(jen.Id("g")),
	)

	// Scan applies the group-by query and scans the result into the given value.
	// Uses runtime.ScanWithInterceptors to avoid per-entity boilerplate.
	f.Comment("Scan applies the group-by query and scans the result into the given value.")
	f.Func().Params(jen.Id("g").Op("*").Id(gbName)).Id("Scan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Error().BlockFunc(func(body *jen.Group) {
		body.Id("ctx").Op("=").Id("setContextOp").Call(
			jen.Id("ctx"), jen.Id("g").Dot("build").Dot("ctx"), jen.Qual(h.VeloxPkg(), "OpQueryGroupBy"),
		)
		body.If(jen.Id("g").Dot("build").Op("==").Nil()).Block(
			jen.Return(jen.Id("g").Dot("sqlScan").Call(jen.Id("ctx"), jen.Id("v"))),
		)
		body.If(jen.Err().Op(":=").Id("g").Dot("build").Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
			jen.Return(jen.Err()),
		)
		body.Return(jen.Qual(runtimePkg, "ScanWithInterceptors").Call(
			jen.Id("ctx"),
			jen.Id("g").Dot("build"),
			selectIntersExpr(jen.Id("g").Dot("build")),
			jen.Id("g").Dot("sqlScan"),
			jen.Id("v"),
		))
	})

	// sqlScan for GroupBy — wired as Selector.scan
	f.Func().Params(jen.Id("g").Op("*").Id(gbName)).Id("sqlScan").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("v").Any(),
	).Error().Block(
		jen.Return(jen.Qual(runtimePkg, "QueryGroupBy").Call(
			jen.Id("ctx"),
			jen.Id("g").Dot("build"),
			jen.Id("g").Dot("fields"),
			jen.Id("g").Dot("Fns").Call(),
			jen.Id("v"),
		)),
	)

	// =========================================================================
	// clone (private, returns concrete type for First/Only)
	// =========================================================================

	f.Commentf("clone returns a concrete clone of the %s for internal use by First/Only.", queryName)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id("clone").Params().Op("*").Id(queryName).BlockFunc(func(body *jen.Group) {
		body.If(jen.Id(recv).Op("==").Nil()).Block(
			jen.Return(jen.Nil()),
		)
		cloneDict := jen.Dict{
			jen.Id("config"):     jen.Id(recv).Dot("config"),
			jen.Id("ctx"):        jen.Id(recv).Dot("ctx").Dot("Clone").Call(),
			jen.Id("predicates"): jen.Qual(runtimePkg, "CloneSlice").Call(jen.Id(recv).Dot("predicates")),
			jen.Id("order"):      jen.Qual(runtimePkg, "CloneSlice").Call(jen.Id(recv).Dot("order")),
			jen.Id("modifiers"):  jen.Qual(runtimePkg, "CloneSlice").Call(jen.Id(recv).Dot("modifiers")),
			jen.Id("withFKs"):    jen.Id(recv).Dot("withFKs"),
			jen.Id("path"):       jen.Id(recv).Dot("path"),
			// SP-2: pointer copy of the shared *entity.InterceptorStore.
			// Without this, clone()s lose all client-level interceptors
			// and prepareQuery nil-derefs on q.inters.<EntityName>.
			jen.Id("inters"): jen.Id(recv).Dot("inters"),
		}
		if schemaConfigEnabled {
			cloneDict[jen.Id("schemaConfig")] = jen.Id(recv).Dot("schemaConfig")
		}
		// Policy must survive clone — First/Only/FirstID/OnlyID/Exist all
		// call q.clone().IDs(ctx) which re-enters prepareQuery; without
		// this, q.policy is nil in the clone and tenant/privacy filters
		// are silently skipped.
		if hasPolicy {
			cloneDict[jen.Id("policy")] = jen.Id(recv).Dot("policy")
		}
		// Copy edge pointers (deep-cloned, like Ent)
		for _, edge := range t.Edges {
			field := edgeCallbackField(edge)
			cloneDict[jen.Id(field)] = jen.Id(recv).Dot(field).Dot("clone").Call()
		}
		// Copy loadTotal slice.
		cloneDict[jen.Id("loadTotal")] = jen.Qual(runtimePkg, "CloneSlice").Call(jen.Id(recv).Dot("loadTotal"))
		body.Id("c").Op(":=").Op("&").Id(queryName).Values(cloneDict)
		// Copy named edge maps (deep clone each query).
		if h.FeatureEnabled(gen.FeatureNamedEdges.Name) {
			for _, edge := range t.Edges {
				if edge.Unique {
					continue
				}
				namedField := "withNamed" + edge.StructField()
				targetQueryName := edge.Type.Name + "Query"
				body.If(jen.Id(recv).Dot(namedField).Op("!=").Nil()).Block(
					jen.Id("c").Dot(namedField).Op("=").Make(jen.Map(jen.String()).Op("*").Id(targetQueryName), jen.Len(jen.Id(recv).Dot(namedField))),
					jen.For(jen.List(jen.Id("name"), jen.Id("q")).Op(":=").Range().Id(recv).Dot(namedField)).Block(
						jen.Id("c").Dot(namedField).Index(jen.Id("name")).Op("=").Id("q").Dot("clone").Call(),
					),
				)
			}
		}
		body.Return(jen.Id("c"))
	})

	// =========================================================================
	// Per-edge typed loader methods — called from sqlAll inline dispatch
	// =========================================================================

	for _, edge := range t.Edges {
		genTypedEdgeLoader(f, h, t, edge, recv, queryName, entityPkgPath, entityType)
	}

	// Verify interface compliance at compile time
	f.Commentf("Verify %s implements %s.%s at compile time.", queryName, "entity", querierIface)
	f.Var().Id("_").Qual(entityPkgPath, querierIface).Op("=").Parens(jen.Op("*").Id(queryName)).Call(jen.Nil())

	return f
}

// edgeCallbackField returns the unexported field name for edge loading callbacks.
// For example, edge "Posts" -> "withPosts".
func edgeCallbackField(e *gen.Edge) string {
	return "with" + e.StructField()
}

// genTypedEdgeLoader generates a typed per-edge loader method with init/assign callbacks (Ent-style).
// Signature: loadXxx(ctx, query *XxxQuery, nodes []*Entity, init func(*Entity), assign func(*Entity, *Target)) error
func genTypedEdgeLoader(
	f *jen.File,
	h gen.GeneratorHelper,
	t *gen.Type,
	edge *gen.Edge,
	recv, queryName, entityPkgPath string,
	entityType func() *jen.Statement,
) {
	edgeField := edge.StructField()
	loaderName := "load" + edgeField
	sqlPkg := h.SQLPkg()
	targetQueryName := edge.Type.Name + "Query"
	targetEntityType := func() *jen.Statement { return jen.Qual(entityPkgPath, edge.Type.Name) }
	idType := h.IDType(t)

	f.Commentf("%s eagerly loads the %q edge for the given nodes.", loaderName, edge.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(queryName)).Id(loaderName).Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("query").Op("*").Id(targetQueryName),
		jen.Id("nodes").Index().Op("*").Add(entityType()),
		jen.Id("init").Func().Params(jen.Op("*").Add(entityType())),
		jen.Id("assign").Func().Params(jen.Op("*").Add(entityType()), jen.Op("*").Add(targetEntityType())),
	).Error().BlockFunc(func(body *jen.Group) {
		if edge.OwnFK() {
			genTypedM2OLoader(body, h, t, edge, recv, entityPkgPath, entityType, targetEntityType, sqlPkg, idType)
		} else if edge.M2M() {
			genM2MLoaderFallback(body, h, t, edge, recv, entityPkgPath, entityType)
		} else {
			genTypedO2MLoader(body, h, t, edge, recv, entityPkgPath, entityType, targetEntityType, sqlPkg, idType)
		}
	})
}

// genTypedO2MLoader generates typed O2M edge loading with init/assign callbacks (Ent-style).
// Uses `fks []any` for driver compatibility, calls `query.All(ctx)` so interceptors apply.
func genTypedO2MLoader(
	body *jen.Group,
	h gen.GeneratorHelper,
	t *gen.Type,
	edge *gen.Edge,
	recv, entityPkgPath string,
	entityType, targetEntityType func() *jen.Statement,
	sqlPkg string,
	idType jen.Code,
) {
	srcSubPkg := h.LeafPkgPath(t)
	fkColumn := edge.ColumnConstant()

	// fks := make([]any, 0, len(nodes))
	// nodeids := make(map[IDType]*entity.User) — typed map avoids *T vs T mismatch
	body.Id("fks").Op(":=").Make(jen.Index().Any(), jen.Lit(0), jen.Len(jen.Id("nodes")))
	body.Id("nodeids").Op(":=").Make(jen.Map(idType).Op("*").Add(entityType()), jen.Len(jen.Id("nodes")))
	body.For(jen.List(jen.Id("i")).Op(":=").Range().Id("nodes")).BlockFunc(func(forBody *jen.Group) {
		forBody.Id("fks").Op("=").Append(jen.Id("fks"), jen.Id("nodes").Index(jen.Id("i")).Dot("ID"))
		forBody.Id("nodeids").Index(jen.Id("nodes").Index(jen.Id("i")).Dot("ID")).Op("=").Id("nodes").Index(jen.Id("i"))
		forBody.If(jen.Id("init").Op("!=").Nil()).Block(
			jen.Id("init").Call(jen.Id("nodes").Index(jen.Id("i"))),
		)
	})

	// Use query parameter directly — no clone, no reading from _q.withXxx.
	body.Id("query").Dot("withFKs").Op("=").True()
	body.Id("query").Dot("Where").Call(
		jen.Func().Params(jen.Id("s").Op("*").Qual(sqlPkg, "Selector")).Block(
			jen.Id("s").Dot("Where").Call(
				jen.Qual(h.SQLPkg(), "In").Call(
					jen.Id("s").Dot("C").Call(jen.Qual(srcSubPkg, fkColumn)),
					jen.Id("fks").Op("..."),
				),
			),
		),
	)

	// Execute sub-query: query.All(ctx) — interceptors apply!
	body.List(jen.Id("neighbors"), jen.Id("err")).Op(":=").Id("query").Dot("All").Call(jen.Id("ctx"))
	body.If(jen.Id("err").Op("!=").Nil()).Block(
		jen.Return(jen.Id("err")),
	)

	// Map children back to parents via FK extraction.
	// Determine at code-gen time if the FK on the child entity is exported and if it's a pointer.
	var o2mFKExported bool
	var o2mFKNillable bool
	var o2mFKStructField string
	if edge.Ref != nil {
		if refFK, fkErr := edge.Ref.ForeignKey(); fkErr == nil {
			o2mFKStructField = refFK.StructField()
			o2mFKExported = token.IsExported(o2mFKStructField)
			o2mFKNillable = refFK.Field.Nillable
		}
	}

	body.For(jen.List(jen.Id("_"), jen.Id("n")).Op(":=").Range().Id("neighbors")).BlockFunc(func(forBody *jen.Group) {
		if o2mFKExported {
			if o2mFKNillable {
				// Exported pointer FK (e.g., *uuid.UUID): nil-check, then dereference for map key.
				// The map key is the non-pointer value type, so we must dereference
				// to avoid *T vs T mismatch in map lookup.
				forBody.If(jen.Id("n").Dot(o2mFKStructField).Op("==").Nil()).Block(
					jen.Return(jen.Qual("fmt", "Errorf").Call(
						jen.Lit("velox: foreign-key %q is nil for node %v"), jen.Lit(edge.Rel.Column()), jen.Id("n").Dot("ID"),
					)),
				)
				forBody.Id("parentID").Op(":=").Op("*").Id("n").Dot(o2mFKStructField)
			} else {
				// Exported non-pointer FK: use directly.
				forBody.Id("parentID").Op(":=").Id("n").Dot(o2mFKStructField)
			}
		} else {
			// Unexported FK: use FKValue + derefFK + type assertion to typed map key.
			forBody.Id("fk").Op(":=").Id("n").Dot("FKValue").Call(jen.Lit(edge.Rel.Column()))
			forBody.If(jen.Id("fk").Op("==").Nil()).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(
					jen.Lit("velox: foreign-key %q is nil for node %v"), jen.Lit(edge.Rel.Column()), jen.Id("n").Dot("ID"),
				)),
			)
			forBody.Id("parentID").Op(":=").Id("derefFK").Call(jen.Id("fk")).Assert(idType)
		}

		forBody.List(jen.Id("node"), jen.Id("ok")).Op(":=").Id("nodeids").Index(jen.Id("parentID"))
		forBody.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Qual("fmt", "Errorf").Call(
				jen.Lit("velox: unexpected foreign-key %q returned %v for node %v"), jen.Lit(edge.Rel.Column()), jen.Id("parentID"), jen.Id("n").Dot("ID"),
			)),
		)
		forBody.Id("assign").Call(jen.Id("node"), jen.Id("n"))
	})

	body.Return(jen.Nil())
}

// genTypedM2OLoader generates typed M2O edge loading with init/assign callbacks (Ent-style).
// Uses `query` parameter directly (no clone), calls `query.All(ctx)` so interceptors apply.
func genTypedM2OLoader(
	body *jen.Group,
	h gen.GeneratorHelper,
	t *gen.Type,
	edge *gen.Edge,
	recv, entityPkgPath string,
	entityType, targetEntityType func() *jen.Statement,
	sqlPkg string,
	idType jen.Code,
) {
	targetSubPkg := h.LeafPkgPath(edge.Type)

	fk, err := edge.ForeignKey()
	if err != nil {
		body.Return(jen.Nil())
		return
	}

	// Collect unique FK values from parents.
	body.Id("fkSeen").Op(":=").Make(jen.Map(h.IDType(edge.Type)).Struct(), jen.Len(jen.Id("nodes")))
	body.Var().Id("fks").Index().Any()
	fkIsNillable := fk.Field.Nillable
	body.For(jen.List(jen.Id("_"), jen.Id("n")).Op(":=").Range().Id("nodes")).BlockFunc(func(forBody *jen.Group) {
		fkStructField := fk.StructField()
		if token.IsExported(fkStructField) {
			if fkIsNillable {
				// Exported pointer FK (e.g., *uuid.UUID): skip nil, dereference for map key.
				// The neighbor map key is the non-pointer value type, so we must dereference.
				forBody.If(jen.Id("n").Dot(fkStructField).Op("==").Nil()).Block(jen.Continue())
				forBody.Id("fkVal").Op(":=").Op("*").Id("n").Dot(fkStructField)
			} else {
				// Exported non-pointer FK: use directly.
				forBody.Id("fkVal").Op(":=").Id("n").Dot(fkStructField)
			}
		} else {
			// Unexported FK: get via FKValue (returns pointer), dereference for map key.
			forBody.Id("fkRaw").Op(":=").Id("n").Dot("FKValue").Call(jen.Lit(fk.Field.Name))
			forBody.If(jen.Id("fkRaw").Op("==").Nil()).Block(jen.Continue())
			forBody.Id("fkVal").Op(":=").Id("derefFK").Call(jen.Id("fkRaw")).Op(".").Parens(h.IDType(edge.Type))
		}
		forBody.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("fkSeen").Index(jen.Id("fkVal")), jen.Op("!").Id("ok")).Block(
			jen.Id("fkSeen").Index(jen.Id("fkVal")).Op("=").Struct().Values(),
			jen.Id("fks").Op("=").Append(jen.Id("fks"), jen.Any().Call(jen.Id("fkVal"))),
		)
		forBody.If(jen.Id("init").Op("!=").Nil()).Block(
			jen.Id("init").Call(jen.Id("n")),
		)
	})
	body.If(jen.Len(jen.Id("fks")).Op("==").Lit(0)).Block(
		jen.Return(jen.Nil()),
	)

	// Use query parameter directly, add WHERE target.id IN (fks...).
	body.Id("query").Dot("Where").Call(
		jen.Func().Params(jen.Id("s").Op("*").Qual(sqlPkg, "Selector")).Block(
			jen.Id("s").Dot("Where").Call(
				jen.Qual(h.SQLPkg(), "In").Call(
					jen.Id("s").Dot("C").Call(jen.Qual(targetSubPkg, "FieldID")),
					jen.Id("fks").Op("..."),
				),
			),
		),
	)

	// Execute sub-query: query.All(ctx) — interceptors apply!
	body.List(jen.Id("neighbors"), jen.Id("err")).Op(":=").Id("query").Dot("All").Call(jen.Id("ctx"))
	body.If(jen.Id("err").Op("!=").Nil()).Block(
		jen.Return(jen.Id("err")),
	)

	// Build lookup map: neighbor ID -> *TargetEntity.
	body.Id("neighborByID").Op(":=").Make(jen.Map(h.IDType(edge.Type)).Op("*").Add(targetEntityType()), jen.Len(jen.Id("neighbors")))
	body.For(jen.List(jen.Id("_"), jen.Id("n")).Op(":=").Range().Id("neighbors")).Block(
		jen.Id("neighborByID").Index(jen.Id("n").Dot("ID")).Op("=").Id("n"),
	)

	// Assign neighbors to parents via init/assign callbacks.
	body.For(jen.List(jen.Id("_"), jen.Id("n")).Op(":=").Range().Id("nodes")).BlockFunc(func(forBody *jen.Group) {
		fkStructField := fk.StructField()
		if token.IsExported(fkStructField) {
			if fkIsNillable {
				// Pointer FK: skip nil, dereference for map key match.
				forBody.If(jen.Id("n").Dot(fkStructField).Op("==").Nil()).Block(jen.Continue())
				forBody.Id("fkVal").Op(":=").Op("*").Id("n").Dot(fkStructField)
			} else {
				forBody.Id("fkVal").Op(":=").Id("n").Dot(fkStructField)
			}
		} else {
			forBody.Id("fkRaw").Op(":=").Id("n").Dot("FKValue").Call(jen.Lit(fk.Field.Name))
			forBody.If(jen.Id("fkRaw").Op("==").Nil()).Block(jen.Continue())
			forBody.Id("fkVal").Op(":=").Id("derefFK").Call(jen.Id("fkRaw")).Op(".").Parens(h.IDType(edge.Type))
		}
		forBody.If(
			jen.List(jen.Id("neighbor"), jen.Id("ok")).Op(":=").Id("neighborByID").Index(jen.Id("fkVal")),
			jen.Id("ok"),
		).Block(
			jen.Id("assign").Call(jen.Id("n"), jen.Id("neighbor")),
		)
	})

	body.Return(jen.Nil())
}

// genM2MLoaderFallback generates M2M edge loading using JOIN + interceptor chain.
// Instead of delegating to runtime.LoadM2MEdgeCore (which bypasses interceptors),
// this builds the SQL with a JOIN on the pivot table and wraps execution in the
// target query's interceptor chain, ensuring privacy rules are enforced.
func genM2MLoaderFallback(
	body *jen.Group,
	h gen.GeneratorHelper,
	t *gen.Type,
	edge *gen.Edge,
	recv, entityPkgPath string,
	entityType func() *jen.Statement,
) {
	srcSubPkg := h.LeafPkgPath(t)
	targetSubPkg := h.LeafPkgPath(edge.Type)
	sqlPkg := h.SQLPkg()
	veloxPkg := h.VeloxPkg()
	idType := h.IDType(t)
	targetEntityType := func() *jen.Statement { return jen.Qual(entityPkgPath, edge.Type.Name) }

	if len(edge.Rel.Columns) < 2 {
		body.Return(jen.Nil())
		return
	}

	// Determine FK and ref columns based on inverse flag.
	// For normal edges: Columns[0] = parent FK, Columns[1] = child FK in join table.
	// For inverse edges: swap them.
	var parentFKCol, childFKCol string
	if edge.IsInverse() {
		parentFKCol = edge.Rel.Columns[1]
		childFKCol = edge.Rel.Columns[0]
	} else {
		parentFKCol = edge.Rel.Columns[0]
		childFKCol = edge.Rel.Columns[1]
	}

	// edgeIDs := make([]any, len(nodes))
	// byID := make(map[int]*entity.Post)
	// nids := make(map[int]map[*entity.Post]struct{})
	body.Id("edgeIDs").Op(":=").Make(jen.Index().Any(), jen.Len(jen.Id("nodes")))
	body.Id("byID").Op(":=").Make(jen.Map(idType).Op("*").Add(entityType()), jen.Len(jen.Id("nodes")))
	body.Id("nids").Op(":=").Make(jen.Map(h.IDType(edge.Type)).Map(jen.Op("*").Add(entityType())).Struct(), jen.Len(jen.Id("nodes")))
	body.For(jen.List(jen.Id("i"), jen.Id("node")).Op(":=").Range().Id("nodes")).BlockFunc(func(forBody *jen.Group) {
		forBody.Id("edgeIDs").Index(jen.Id("i")).Op("=").Id("node").Dot("ID")
		forBody.Id("byID").Index(jen.Id("node").Dot("ID")).Op("=").Id("node")
		forBody.If(jen.Id("init").Op("!=").Nil()).Block(
			jen.Id("init").Call(jen.Id("node")),
		)
	})

	// Build the QuerierFunc that does the JOIN + scan.
	body.Var().Id("qr").Qual(veloxPkg, "Querier").Op("=").Qual(veloxPkg, "QuerierFunc").Call(
		jen.Func().Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("q").Qual(veloxPkg, "Query"),
		).Params(jen.Qual(veloxPkg, "Value"), jen.Error()).BlockFunc(func(fnBody *jen.Group) {
			targetQueryName := edge.Type.Name + "Query"

			// tq := q.(*TagQuery)
			fnBody.Id("tq").Op(":=").Id("q").Assert(jen.Op("*").Id(targetQueryName))

			// selector, err := tq.buildSelector(ctx)
			fnBody.List(jen.Id("selector"), jen.Err()).Op(":=").Id("tq").Dot("buildSelector").Call(jen.Id("ctx"))
			fnBody.If(jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Err()),
			)

			// joinT := sql.Table(post.TagsTable)
			fnBody.Id("joinT").Op(":=").Qual(sqlPkg, "Table").Call(jen.Qual(srcSubPkg, edge.TableConstant()))

			// selector.Join(joinT).On(selector.C(tag.FieldID), joinT.C("tag_id"))
			fnBody.Id("selector").Dot("Join").Call(jen.Id("joinT")).Dot("On").Call(
				jen.Id("selector").Dot("C").Call(jen.Qual(targetSubPkg, "FieldID")),
				jen.Id("joinT").Dot("C").Call(jen.Lit(childFKCol)),
			)

			// selector.Where(sql.In(joinT.C("post_id"), edgeIDs...))
			fnBody.Id("selector").Dot("Where").Call(
				jen.Qual(sqlPkg, "In").Call(
					jen.Id("joinT").Dot("C").Call(jen.Lit(parentFKCol)),
					jen.Id("edgeIDs").Op("..."),
				),
			)

			// cols := selector.SelectedColumns()
			// selector.Select(joinT.C("post_id"))
			// selector.AppendSelect(cols...)
			// selector.SetDistinct(false)
			fnBody.Id("cols").Op(":=").Id("selector").Dot("SelectedColumns").Call()
			fnBody.Id("selector").Dot("Select").Call(jen.Id("joinT").Dot("C").Call(jen.Lit(parentFKCol)))
			fnBody.Id("selector").Dot("AppendSelect").Call(jen.Id("cols").Op("..."))
			fnBody.Id("selector").Dot("SetDistinct").Call(jen.False())

			// rows := &sql.Rows{}
			// queryStr, args := selector.Query()
			fnBody.Id("rows").Op(":=").Op("&").Qual(sqlPkg, "Rows").Values()
			fnBody.List(jen.Id("queryStr"), jen.Id("args")).Op(":=").Id("selector").Dot("Query").Call()
			fnBody.If(
				jen.Err().Op(":=").Id("tq").Dot("config").Dot("Driver").Dot("Query").Call(
					jen.Id("ctx"), jen.Id("queryStr"), jen.Id("args"), jen.Id("rows"),
				),
				jen.Err().Op("!=").Nil(),
			).Block(
				jen.Return(jen.Nil(), jen.Err()),
			)
			fnBody.Defer().Id("rows").Dot("Close").Call()

			// columns, err := rows.Columns()
			fnBody.List(jen.Id("columns"), jen.Err()).Op(":=").Id("rows").Dot("Columns").Call()
			fnBody.If(jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Err()),
			)

			// var result []*entity.Tag
			fnBody.Var().Id("result").Index().Op("*").Add(targetEntityType())

			// Scan loop
			fnBody.For(jen.Id("rows").Dot("Next").Call()).BlockFunc(func(scanBody *jen.Group) {
				// pivotScan := new(sql.NullInt64)
				pivotNewScanType := t.ID.NewScanType()
				scanBody.Id("pivotScan").Op(":=").Id(pivotNewScanType)

				// scanValues, err := (&entity.Tag{}).ScanValues(columns[1:])
				scanBody.List(jen.Id("scanValues"), jen.Err()).Op(":=").Parens(jen.Op("&").Add(targetEntityType()).Values()).Dot("ScanValues").Call(
					jen.Id("columns").Index(jen.Lit(1).Op(":")),
				)
				scanBody.If(jen.Err().Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Err()),
				)

				// allValues := append([]any{pivotScan}, scanValues...)
				scanBody.Id("allValues").Op(":=").Append(
					jen.Index().Any().Values(jen.Id("pivotScan")),
					jen.Id("scanValues").Op("..."),
				)

				// if err := rows.Scan(allValues...); err != nil { return nil, err }
				scanBody.If(
					jen.Err().Op(":=").Id("rows").Dot("Scan").Call(jen.Id("allValues").Op("...")),
					jen.Err().Op("!=").Nil(),
				).Block(
					jen.Return(jen.Nil(), jen.Err()),
				)

				// node := &entity.Tag{}
				// if err := node.AssignValues(columns[1:], scanValues); err != nil { return nil, err }
				scanBody.Id("node").Op(":=").Op("&").Add(targetEntityType()).Values()
				scanBody.If(
					jen.Err().Op(":=").Id("node").Dot("AssignValues").Call(
						jen.Id("columns").Index(jen.Lit(1).Op(":")),
						jen.Id("scanValues"),
					),
					jen.Err().Op("!=").Nil(),
				).Block(
					jen.Return(jen.Nil(), jen.Err()),
				)

				// outValue := int(pivotScan.Int64)
				pivotExtract := t.ID.ScanTypeField("pivotScan")
				scanBody.Id("outValue").Op(":=").Id(pivotExtract)

				// Deduplicate: group by target node ID, map parents.
				scanBody.If(jen.Id("nids").Index(jen.Id("node").Dot("ID")).Op("==").Nil()).BlockFunc(func(ifBody *jen.Group) {
					ifBody.Id("nids").Index(jen.Id("node").Dot("ID")).Op("=").Map(jen.Op("*").Add(entityType())).Struct().Values(
						jen.Dict{jen.Id("byID").Index(jen.Id("outValue")): jen.Values()},
					)
					ifBody.Id("result").Op("=").Append(jen.Id("result"), jen.Id("node"))
				}).Else().Block(
					jen.Id("nids").Index(jen.Id("node").Dot("ID")).Index(jen.Id("byID").Index(jen.Id("outValue"))).Op("=").Struct().Values(),
				)
			})

			// if err := rows.Err(); err != nil { return nil, err }
			fnBody.If(jen.Err().Op(":=").Id("rows").Dot("Err").Call(), jen.Err().Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Err()),
			)

			fnBody.Return(jen.Id("result"), jen.Nil())
		}),
	)

	// Run Traversers (privacy, soft-delete) before interceptor chain — matches O2M/M2O path.
	body.If(jen.Err().Op(":=").Id("query").Dot("prepareQuery").Call(jen.Id("ctx")), jen.Err().Op("!=").Nil()).Block(
		jen.Return(jen.Err()),
	)

	// Execute through interceptor chain using velox.WithInterceptors.
	// query.inters is *entity.InterceptorStore (SP-2); read the per-edge-target slice.
	body.List(jen.Id("neighbors"), jen.Id("err")).Op(":=").Qual(veloxPkg, "WithInterceptors").Types(
		jen.Index().Op("*").Add(targetEntityType()),
	).Call(
		jen.Id("ctx"), jen.Id("query"), jen.Id("qr"), jen.Id("query").Dot("inters").Dot(edge.Type.Name),
	)
	body.If(jen.Id("err").Op("!=").Nil()).Block(
		jen.Return(jen.Id("err")),
	)

	// Assign neighbors to parents and inject config.
	body.For(jen.List(jen.Id("_"), jen.Id("n")).Op(":=").Range().Id("neighbors")).BlockFunc(func(forBody *jen.Group) {
		forBody.Id("n").Dot(edge.Type.SetConfigMethodName()).Call(jen.Id(recv).Dot("config"))
		forBody.For(jen.List(jen.Id("parent")).Op(":=").Range().Id("nids").Index(jen.Id("n").Dot("ID"))).Block(
			jen.Id("assign").Call(jen.Id("parent"), jen.Id("n")),
		)
	})
	body.Return(jen.Nil())
}

// genQueryHelpers generates the shared helpers.go for the query/ package.
// Contains intP, idsToAny, fkToID helpers, and query factory registration.
func genQueryHelpers(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile(h.Pkg())

	// Register a query factory per entity type
	// so runtime.NewEntityQuery("User", cfg) works without direct import coupling.
	nodes := h.Graph().Nodes
	if len(nodes) > 0 {
		f.Func().Id("init").Params().BlockFunc(func(grp *jen.Group) {
			for _, t := range nodes {
				queryName := t.QueryName()
				grp.Qual(runtimePkg, "RegisterQueryFactory").Call(
					jen.Lit(t.Name),
					jen.Func().Params(
						jen.Id("cfg").Qual(runtimePkg, "Config"),
					).Any().Block(
						jen.Return(jen.Id("New"+queryName).Call(jen.Id("cfg"))),
					),
				)
			}
		})
		f.Line()
	}

	// intP helper
	f.Comment("intP returns a pointer to the given int value.")
	f.Func().Id("intP").Params(jen.Id("v").Int()).Op("*").Int().Block(
		jen.Return(jen.Op("&").Id("v")),
	)
	f.Line()

	// idsToAny converts a typed ID slice to []any for sql.InValues.
	f.Comment("idsToAny converts a typed int ID slice to []any for sql.InValues.")
	f.Func().Id("idsToAny").Params(jen.Id("ids").Index().Int()).Index().Any().Block(
		jen.Id("out").Op(":=").Make(jen.Index().Any(), jen.Len(jen.Id("ids"))),
		jen.For(jen.List(jen.Id("i"), jen.Id("v")).Op(":=").Range().Id("ids")).Block(
			jen.Id("out").Index(jen.Id("i")).Op("=").Id("v"),
		),
		jen.Return(jen.Id("out")),
	)
	f.Line()

	// fkToID extracts an int from an FK any value (supports *int and int).
	f.Comment("fkToID extracts an int from an FK any value (supports *int and int).")
	f.Func().Id("fkToID").Params(jen.Id("v").Any()).Int().Block(
		jen.Switch(jen.Id("val").Op(":=").Id("v").Assert(jen.Type())).BlockFunc(func(sw *jen.Group) {
			sw.Case(jen.Int()).Block(jen.Return(jen.Id("val")))
			sw.Case(jen.Op("*").Int()).Block(
				jen.If(jen.Id("val").Op("!=").Nil()).Block(jen.Return(jen.Op("*").Id("val"))),
			)
			sw.Case(jen.Int64()).Block(jen.Return(jen.Int().Call(jen.Id("val"))))
			sw.Case(jen.Op("*").Int64()).Block(
				jen.If(jen.Id("val").Op("!=").Nil()).Block(jen.Return(jen.Int().Call(jen.Op("*").Id("val")))),
			)
		}),
		jen.Return(jen.Lit(0)),
	)
	f.Line()

	// derefFK dereferences a pointer FK value to its underlying comparable value.
	// Uses reflect for generic pointer dereference to handle all ID types (int, string, uuid.UUID, etc.).
	f.Comment("derefFK dereferences a pointer FK value to its underlying comparable value.")
	f.Func().Id("derefFK").Params(jen.Id("v").Any()).Any().Block(
		jen.Id("rv").Op(":=").Qual("reflect", "ValueOf").Call(jen.Id("v")),
		jen.If(jen.Id("rv").Dot("Kind").Call().Op("==").Qual("reflect", "Ptr").Op("&&").
			Op("!").Id("rv").Dot("IsNil").Call()).Block(
			jen.Return(jen.Id("rv").Dot("Elem").Call().Dot("Interface").Call()),
		),
		jen.Return(jen.Id("v")),
	)
	f.Line()

	// querierAll — generic factory that wraps sqlAll into a Querier (Ent-style).
	// Uses unexported sqlAll method as type constraint, so must be in the same package.
	veloxPkg := h.VeloxPkg()
	f.Comment("querierAll returns a Querier that calls sqlAll on the concrete query type.")
	f.Func().Id("querierAll").Types(
		jen.Id("V").Any(),
		jen.Id("Q").Interface(
			jen.Id("sqlAll").Params(jen.Qual("context", "Context")).Params(jen.Id("V"), jen.Error()),
		),
	).Params().Qual(veloxPkg, "Querier").Block(
		jen.Return(jen.Qual(veloxPkg, "QuerierFunc").Call(
			jen.Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("q").Qual(veloxPkg, "Query"),
			).Params(jen.Qual(veloxPkg, "Value"), jen.Error()).Block(
				jen.Return(jen.Id("q").Assert(jen.Id("Q")).Dot("sqlAll").Call(jen.Id("ctx"))),
			),
		)),
	)
	f.Line()

	// querierCount — generic factory that wraps sqlCount into a Querier (Ent-style).
	f.Comment("querierCount returns a Querier that calls sqlCount on the concrete query type.")
	f.Func().Id("querierCount").Types(
		jen.Id("Q").Interface(
			jen.Id("sqlCount").Params(jen.Qual("context", "Context")).Params(jen.Int(), jen.Error()),
		),
	).Params().Qual(veloxPkg, "Querier").Block(
		jen.Return(jen.Qual(veloxPkg, "QuerierFunc").Call(
			jen.Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("q").Qual(veloxPkg, "Query"),
			).Params(jen.Qual(veloxPkg, "Value"), jen.Error()).Block(
				jen.Return(jen.Id("q").Assert(jen.Id("Q")).Dot("sqlCount").Call(jen.Id("ctx"))),
			),
		)),
	)
	f.Line()

	// querierIDs — generic factory that wraps sqlIDs into a Querier
	// so the IDs() method can run through the interceptor chain.
	// Parameterised on the ID slice type so user-defined IDs (UUID,
	// string) work as well as the numeric default.
	f.Comment("querierIDs returns a Querier that calls sqlIDs on the concrete query type.")
	f.Func().Id("querierIDs").Types(
		jen.Id("IDs").Any(),
		jen.Id("Q").Interface(
			jen.Id("sqlIDs").Params(jen.Qual("context", "Context")).Params(jen.Id("IDs"), jen.Error()),
		),
	).Params().Qual(veloxPkg, "Querier").Block(
		jen.Return(jen.Qual(veloxPkg, "QuerierFunc").Call(
			jen.Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
				jen.Id("q").Qual(veloxPkg, "Query"),
			).Params(jen.Qual(veloxPkg, "Value"), jen.Error()).Block(
				jen.Return(jen.Id("q").Assert(jen.Id("Q")).Dot("sqlIDs").Call(jen.Id("ctx"))),
			),
		)),
	)
	f.Line()

	// setContextOp bridges runtime.QueryContext to velox.QueryContext for interceptor context propagation.
	f.Comment("setContextOp returns a new context with the given QueryContext attached (including its op) in case it does not exist.")
	f.Func().Id("setContextOp").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("qc").Op("*").Qual(runtimePkg, "QueryContext"),
		jen.Id("op").String(),
	).Qual("context", "Context").Block(
		jen.If(jen.Qual(veloxPkg, "QueryFromContext").Call(jen.Id("ctx")).Op("==").Nil()).Block(
			jen.Id("ctx").Op("=").Qual(veloxPkg, "NewQueryContext").Call(
				jen.Id("ctx"),
				jen.Op("&").Qual(veloxPkg, "QueryContext").Values(jen.Dict{
					jen.Id("Op"):     jen.Id("op"),
					jen.Id("Type"):   jen.Id("qc").Dot("Type"),
					jen.Id("Fields"): jen.Id("qc").Dot("Fields"),
					jen.Id("Unique"): jen.Id("qc").Dot("Unique"),
					jen.Id("Limit"):  jen.Id("qc").Dot("Limit"),
					jen.Id("Offset"): jen.Id("qc").Dot("Offset"),
				}),
			),
		),
		jen.Return(jen.Id("ctx")),
	)

	return f
}
