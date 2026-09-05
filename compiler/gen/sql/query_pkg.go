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

// queryGen carries the per-entity state shared by every section of the
// generated query package. genQueryPkg builds one and calls the gen*
// methods in emission order; each method appends to qg.f. The sections
// live in three source files that all target the SAME output file:
//
//	query_pkg.go           struct, wiring, chainers, edges
//	query_pkg_terminals.go sqlAll, prepareQuery, terminals, locking
//	query_pkg_select.go    QueryReader, Select/GroupBy, clone
//
// That split is for readability only — there is still exactly one
// generator per output file (see AGENTS.md).
type queryGen struct {
	h gen.GeneratorHelper
	f *jen.File
	t *gen.Type

	queryName    string // "UserQuery"
	recv         string // receiver identifier in emitted methods
	selectName   string // "UserSelect"
	gbName       string // "UserGroupBy"
	querierIface string // "UserQuerier", in the shared entity package

	sqlPkg      string
	sqlgraphPkg string
	// entityPkgPath is the package the XxxQuerier interface and entity
	// types are qualified from (a parameter of genQueryPkg — callers pass
	// the shared entity package or, in wiring tests, the leaf package).
	entityPkgPath string
	// entityPkgImportPath is the central entity package that holds the
	// shared *entity.InterceptorStore (SP-2).
	entityPkgImportPath string
	// entitySubPkg is the per-entity leaf package (Table, Columns, FieldID).
	entitySubPkg string

	// hasPolicy is true when FeaturePrivacy is on AND the entity declares
	// a policy. Privacy is evaluated explicitly in prepareQuery via
	// q.policy.EvalQuery(); it is NOT part of the interceptor chain.
	hasPolicy           bool
	schemaConfigEnabled bool
	namedEdgesEnabled   bool

	idType jen.Code
}

func newQueryGen(h gen.GeneratorHelper, t *gen.Type, entityPkgPath string) *queryGen {
	return &queryGen{
		h:                   h,
		f:                   h.NewFile(h.Pkg()),
		t:                   t,
		queryName:           t.Name + "Query",
		recv:                "q",
		selectName:          t.Name + "Select",
		gbName:              t.Name + "GroupBy",
		querierIface:        t.Name + "Querier",
		sqlPkg:              h.SQLPkg(),
		sqlgraphPkg:         h.SQLGraphPkg(),
		entityPkgPath:       entityPkgPath,
		entityPkgImportPath: h.SharedEntityPkg(),
		entitySubPkg:        h.LeafPkgPath(t),
		hasPolicy:           h.FeatureEnabled(gen.FeaturePrivacy.Name) && t.NumPolicy() > 0,
		schemaConfigEnabled: h.FeatureEnabled(gen.FeatureSchemaConfig.Name),
		namedEdgesEnabled:   h.FeatureEnabled(gen.FeatureNamedEdges.Name),
		idType:              h.IDType(t),
	}
}

// entityType returns the qualified entity type (entity.User).
func (qg *queryGen) entityType() *jen.Statement {
	return jen.Qual(qg.entityPkgPath, qg.t.Name)
}

// inters returns the expression for this entity's interceptor slice on the
// shared *InterceptorStore: <receiver>.inters.<Entity>. Always the direct
// per-entity slice — privacy is evaluated separately in prepareQuery.
func (qg *queryGen) inters(receiver string) *jen.Statement {
	return jen.Id(receiver).Dot("inters").Dot(qg.t.Name)
}

// selectInters is inters for a select/group-by receiver that reaches the
// query through s.<QueryName> or g.build.
func (qg *queryGen) selectInters(queryAccess *jen.Statement) *jen.Statement {
	return queryAccess.Clone().Dot("inters").Dot(qg.t.Name)
}

// genQueryPkg generates the self-contained XxxQuery for one entity.
// Output: query/<entity>_query.go. The sections are emitted in a fixed
// order; goldens pin the layout byte-for-byte.
func genQueryPkg(h gen.GeneratorHelper, t *gen.Type, _ []*gen.Type, entityPkgPath string) *jen.File {
	qg := newQueryGen(h, t, entityPkgPath)

	qg.genStruct()
	qg.genConstructorAndWiring()
	qg.genFieldCollectable()
	qg.genFromEdge()
	qg.genSpecBuilders()
	qg.genChainers()
	qg.genWithEdges()
	qg.genWithNamedEdges()
	qg.genQueryEdges()
	qg.genClonePublic()
	qg.genSQLAll()
	qg.genPrepareQuery()
	qg.genEntityTerminals()
	qg.genCountExist()
	qg.genSQLExplain()
	qg.genIDTerminals()
	qg.genLocking()
	qg.genQueryReader()
	qg.genSelectEntry()
	qg.genSelectType()
	qg.genGroupByType()
	qg.genClonePrivate()

	// Per-edge typed loader methods — called from sqlAll inline dispatch.
	for _, edge := range t.Edges {
		genTypedEdgeLoader(qg.f, h, t, edge, qg.recv, qg.queryName, entityPkgPath, qg.entityType)
	}

	// Verify interface compliance at compile time
	qg.f.Commentf("Verify %s implements %s.%s at compile time.", qg.queryName, "entity", qg.querierIface)
	qg.f.Var().Id("_").Qual(entityPkgPath, qg.querierIface).Op("=").Parens(jen.Op("*").Id(qg.queryName)).Call(jen.Nil())

	return qg.f
}

// genStruct emits the XxxQuery struct: config, ctx, predicates, order,
// modifiers, the shared interceptor-store pointer, the optional policy, and
// one eager-load field per edge.
func (qg *queryGen) genStruct() {
	// =========================================================================
	// Query struct — self-contained, no runtime type embedding
	// =========================================================================

	qg.f.Commentf("%s is the query builder for %s entities.", qg.queryName, qg.t.Name)
	qg.f.Commentf("It implements %s.%s.", "entity", qg.querierIface)
	qg.f.Type().Id(qg.queryName).StructFunc(func(group *jen.Group) {
		group.Id("config").Qual(runtimePkg, "Config")
		if qg.schemaConfigEnabled {
			group.Id("schemaConfig").Qual(qg.h.InternalPkg(), "SchemaConfig")
		}
		group.Id("ctx").Op("*").Qual(runtimePkg, "QueryContext")
		group.Id("predicates").Index().Func().Params(jen.Op("*").Qual(qg.sqlPkg, "Selector"))
		group.Id("order").Index().Func().Params(jen.Op("*").Qual(qg.sqlPkg, "Selector"))
		group.Id("modifiers").Index().Func().Params(jen.Op("*").Qual(qg.sqlPkg, "Selector"))
		// SP-2: shared-pointer interceptor wiring. The query holds a
		// pointer to the central *entity.InterceptorStore, NOT a slice
		// copy. client.Intercept(...) mutates the shared store and is
		// immediately visible to every query holding the pointer —
		// even queries constructed before the call. Read sites access
		// q.inters.<EntityName> to enumerate this entity's chain.
		group.Id("inters").Op("*").Qual(qg.entityPkgImportPath, "InterceptorStore")
		// policy is the entity's privacy policy (nil when the entity has
		// no policy or when constructed via a code path that doesn't wire
		// it). Evaluated explicitly in prepareQuery — NOT part of the
		// interceptor chain.
		if qg.hasPolicy {
			group.Id("policy").Qual(qg.h.VeloxPkg(), "Policy")
		}
		group.Id("withFKs").Bool()
		// Edge eager-loading: concrete *XxxQuery pointers (same package)
		for _, edge := range qg.t.Edges {
			targetQueryName := edge.Type.Name + "Query"
			group.Id(edgeCallbackField(edge)).Op("*").Id(targetQueryName)
		}
		// loadTotal — registry of post-load hooks (Ent-style).
		group.Id("loadTotal").Index().Func().Params(
			jen.Qual("context", "Context"),
			jen.Index().Op("*").Add(qg.entityType()),
		).Error()
		// Named edge variants (FeatureNamedEdges).
		if qg.namedEdgesEnabled {
			for _, edge := range qg.t.Edges {
				if !edge.Unique {
					targetQueryName := edge.Type.Name + "Query"
					group.Id("withNamed" + edge.StructField()).Map(jen.String()).Op("*").Id(targetQueryName)
				}
			}
		}
		group.Id("path").Func().Params(jen.Qual("context", "Context")).Params(
			jen.Op("*").Qual(qg.sqlPkg, "Selector"), jen.Error(),
		)
	})
}

// genConstructorAndWiring emits NewXxxQuery plus the setters other packages
// use to wire a query: SetPath, SetInterStore, SetPolicy, AddPredicate /
// Filter (privacy) and SetSchemaConfig.
func (qg *queryGen) genConstructorAndWiring() {
	// =========================================================================
	// Constructor
	// =========================================================================

	qg.f.Commentf("New%s creates a new %s.", qg.queryName, qg.queryName)
	qg.f.Func().Id("New" + qg.queryName).Params(
		jen.Id("cfg").Qual(runtimePkg, "Config"),
	).Op("*").Id(qg.queryName).Block(
		jen.Return(jen.Op("&").Id(qg.queryName).Values(jen.Dict{
			jen.Id("config"): jen.Id("cfg"),
			jen.Id("ctx"):    jen.Op("&").Qual(runtimePkg, "QueryContext").Values(jen.Dict{jen.Id("Type"): jen.Lit(qg.t.Name)}),
		})),
	)

	// =========================================================================
	// SetPath — allows external callers (wrapper, contrib) to set the path
	// =========================================================================

	qg.f.Comment("SetPath sets the graph traversal path for this query.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("SetPath").Params(
		jen.Id("p").Func().Params(jen.Qual("context", "Context")).Params(
			jen.Op("*").Qual(qg.sqlPkg, "Selector"), jen.Error(),
		),
	).Block(
		jen.Id(qg.recv).Dot("path").Op("=").Id("p"),
	)

	// SetInterStore — wires the shared *entity.InterceptorStore pointer
	// onto a query that was constructed via cross-package registry
	// dispatch (runtime.NewEntityQuery). Called by the entity client
	// constructor and by every code path that derives a child query.
	// SP-2: replaces the previous SetInters([]Interceptor) slice setter
	// — interceptors no longer get copied per-query.
	qg.f.Comment("SetInterStore wires the shared client-level interceptor store onto this query.")
	qg.f.Comment("Called by the entity client constructor and by query derivation code paths.")
	qg.f.Comment("Not for direct use — call via the SetInterStore inline interface assertion.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("SetInterStore").Params(
		jen.Id("s").Op("*").Qual(qg.entityPkgImportPath, "InterceptorStore"),
	).Block(
		jen.Id(qg.recv).Dot("inters").Op("=").Id("s"),
	)

	// SetPolicy wires the entity's privacy policy onto this query so
	// prepareQuery can invoke policy.EvalQuery explicitly. Called by
	// the entity client constructor (direct path) and by cross-package
	// edge-query constructors (via runtime.EntityPolicy lookup). Only
	// emitted for entities that declare a privacy policy.
	if qg.hasPolicy {
		qg.f.Comment("SetPolicy wires the entity's privacy policy onto this query.")
		qg.f.Comment("Called via an inline interface type assertion from code that")
		qg.f.Comment("constructs the query (entity client, edge query builders).")
		qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("SetPolicy").Params(
			jen.Id("p").Qual(qg.h.VeloxPkg(), "Policy"),
		).Block(
			jen.Id(qg.recv).Dot("policy").Op("=").Id("p"),
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
	if qg.h.FeatureEnabled(gen.FeaturePrivacy.Name) {
		// After cycle-break, filter.go lives in client/{entity}/ (package {entity}client),
		// not the {entity}/ leaf — the filter constructor must be qualified there.
		clientPkgPath := qg.h.RootPkg() + "/client/" + qg.t.PackageDir()
		const privacyPkgPath = "github.com/syssam/velox/privacy"
		qg.f.Commentf("AddPredicate appends a raw SQL-level predicate to the query.")
		qg.f.Comment("Satisfies runtime.PredicateAdder so privacy filters can write")
		qg.f.Comment("predicates through this method rather than touching internal state.")
		qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("AddPredicate").Params(
			jen.Id("p").Func().Params(jen.Op("*").Qual(qg.h.SQLPkg(), "Selector")),
		).Block(
			jen.Id(qg.recv).Dot("predicates").Op("=").Append(jen.Id(qg.recv).Dot("predicates"), jen.Id("p")),
		)

		qg.f.Commentf("Filter returns a %sFilter that writes predicates through this query.", qg.t.Name)
		qg.f.Comment("Implements privacy.Filterable so FilterFunc-based query rules")
		qg.f.Comment("can inject WHERE clauses without knowing the concrete query type.")
		qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Filter").Params().Qual(privacyPkgPath, "Filter").Block(
			jen.Return(jen.Qual(clientPkgPath, "New"+qg.t.Name+"Filter").Call(
				jen.Id(qg.recv).Dot("config"),
				jen.Id(qg.recv),
			)),
		)
	}

	// SetSchemaConfig — allows callers to inject the schema config for multi-tenancy.
	if qg.schemaConfigEnabled {
		qg.f.Comment("SetSchemaConfig sets the schema config for multi-tenancy support.")
		qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("SetSchemaConfig").Params(
			jen.Id("sc").Qual(qg.h.InternalPkg(), "SchemaConfig"),
		).Block(
			jen.Id(qg.recv).Dot("schemaConfig").Op("=").Id("sc"),
		)
	}
}

// genFieldCollectable emits the accessors the GraphQL field collector uses:
// GetIDColumn, GetCtx and the by-name WithEdgeLoad switch.
func (qg *queryGen) genFieldCollectable() {
	// =========================================================================
	// FieldCollectable interface — enables GraphQL field collection.
	// =========================================================================

	qg.f.Commentf("GetIDColumn returns the primary key column name for %s.", qg.t.Name)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("GetIDColumn").Params().String().Block(
		jen.Return(jen.Qual(qg.entitySubPkg, "FieldID")),
	)

	qg.f.Comment("GetCtx returns the query context for field projection.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("GetCtx").Params().Op("*").Qual(runtimePkg, "QueryContext").Block(
		jen.Return(jen.Id(qg.recv).Dot("ctx")),
	)

	qg.f.Comment("WithEdgeLoad adds an edge to be eagerly loaded by name.")
	qg.f.Comment("Used by GraphQL field collector for generic edge loading.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("WithEdgeLoad").Params(
		jen.Id("name").String(),
		jen.Id("_").Op("...").Qual(runtimePkg, "LoadOption"),
	).BlockFunc(func(body *jen.Group) {
		// Switch on edge name to set the correct withXxx field.
		body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, edge := range qg.t.Edges {
				targetQueryName := edge.Type.Name + "Query"
				callbackField := edgeCallbackField(edge)
				// Build case body: initialize query if nil, and enable FK columns
				// only for edges where the FK resides on this entity's table (M2O, O2O inverse).
				var caseStmts []jen.Code
				caseStmts = append(caseStmts,
					jen.If(jen.Id(qg.recv).Dot(callbackField).Op("==").Nil()).Block(
						jen.Id(qg.recv).Dot(callbackField).Op("=").Id("New"+targetQueryName).Call(jen.Id(qg.recv).Dot("config")),
						// Thread the parent's interceptors into the child
						// query so client.Intercept() fires on eager-loads
						// as well as direct queries.
						jen.Id(qg.recv).Dot(callbackField).Dot("inters").Op("=").Id(qg.recv).Dot("inters"),
					),
				)
				if edge.OwnFK() {
					caseStmts = append(caseStmts, jen.Id(qg.recv).Dot("withFKs").Op("=").True())
				}
				sw.Case(jen.Lit(edge.Name)).Block(caseStmts...)
			}
		})
	})
}

// genFromEdge emits NewXxxQueryFromEdge, which adapts a *runtime.EdgeQuery
// into a self-contained query (used by contrib/graphql pagination).
func (qg *queryGen) genFromEdge() {
	// =========================================================================
	// NewXxxQueryFromEdge — adapts a *runtime.EdgeQuery into a self-contained query.
	// Used by GraphQL contrib (pagination) which receives an EdgeQuery from edge resolvers.
	// =========================================================================

	qg.f.Commentf("New%sFromEdge creates a %s from an existing EdgeQuery.", qg.queryName, qg.queryName)
	qg.f.Commentf("The EdgeQuery fields are copied into the self-contained query struct via exported getters.")
	qg.f.Comment("SP-2: the inters field is a *entity.InterceptorStore pointer recovered")
	qg.f.Comment("from cfg.InterStore (type-asserted with nil-safe fallback). Callers that")
	qg.f.Comment("need a populated store must pass a Config built via the standard client")
	qg.f.Comment("constructor; the EdgeQuery's own inters slice is no longer carried.")
	qg.f.Func().Id("New"+qg.queryName+"FromEdge").Params(
		jen.Id("cfg").Qual(runtimePkg, "Config"),
		jen.Id("eq").Op("*").Qual(runtimePkg, "EdgeQuery"),
	).Op("*").Id(qg.queryName).BlockFunc(func(g *jen.Group) {
		// inters, _ := cfg.InterStore.(*entity.InterceptorStore)
		// if inters == nil { inters = &entity.InterceptorStore{} }
		g.List(jen.Id("inters"), jen.Id("_")).Op(":=").Id("cfg").Dot("InterStore").Assert(
			jen.Op("*").Qual(qg.entityPkgImportPath, "InterceptorStore"),
		)
		g.If(jen.Id("inters").Op("==").Nil()).Block(
			jen.Id("inters").Op("=").Op("&").Qual(qg.entityPkgImportPath, "InterceptorStore").Values(),
		)
		g.Return(jen.Op("&").Id(qg.queryName).Values(jen.Dict{
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
}

// genSpecBuilders emits querySpec, buildQuery and buildSelector — thin
// delegations to the runtime helpers that read the query through
// runtime.QueryReader.
func (qg *queryGen) genSpecBuilders() {
	// =========================================================================
	// querySpec — builds a *sqlgraph.QuerySpec from the query's direct fields.
	// Used by Count and IDs which call sqlgraph functions directly.
	// =========================================================================

	qg.f.Comment("querySpec builds a *sqlgraph.QuerySpec from the query's direct fields.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("querySpec").Params().Op("*").Qual(qg.sqlgraphPkg, "QuerySpec").Block(
		jen.Return(jen.Qual(runtimePkg, "MakeQuerySpec").Call(
			jen.Id(qg.recv),
			jen.Qual(schemaPkg(), qg.t.ID.Type.ConstName()),
		)),
	)

	// =========================================================================
	// buildQuery — construct selector for graph traversal
	// =========================================================================

	qg.f.Comment("buildQuery constructs a *sql.Selector from the query state.")
	qg.f.Comment("Used by QueryXxx methods to create a sub-select for graph traversal.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("buildQuery").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Qual(qg.sqlPkg, "Selector"), jen.Error()).Block(
		jen.Return(jen.Qual(runtimePkg, "BuildQueryFrom").Call(jen.Id("ctx"), jen.Id(qg.recv))),
	)

	// =========================================================================
	// buildSelector — fully-configured selector ready for execution
	// =========================================================================

	qg.f.Comment("buildSelector constructs a fully-configured *sql.Selector ready for execution.")
	qg.f.Comment("Adds column selection, FK columns, and DISTINCT on top of buildQuery.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("buildSelector").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Qual(qg.sqlPkg, "Selector"), jen.Error()).Block(
		jen.Return(jen.Qual(runtimePkg, "BuildSelectorFrom").Call(jen.Id("ctx"), jen.Id(qg.recv))),
	)
}

// genChainers emits the chainable builders: Where, Limit, Offset, Unique,
// Order. Each returns the entity.XxxQuerier interface.
func (qg *queryGen) genChainers() {
	// =========================================================================
	// Chainable methods (return entity.XxxQuerier interface)
	// =========================================================================

	// Where
	qg.f.Commentf("Where adds predicates to the %s.", qg.queryName)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Where").Params(
		jen.Id("ps").Op("...").Qual(qg.h.PredicatePkg(), qg.t.Name),
	).Qual(qg.entityPkgPath, qg.querierIface).BlockFunc(func(body *jen.Group) {
		body.For(jen.List(jen.Id("_"), jen.Id("p")).Op(":=").Range().Id("ps")).Block(
			jen.Id(qg.recv).Dot("predicates").Op("=").Append(
				jen.Id(qg.recv).Dot("predicates"), jen.Id("p"),
			),
		)
		body.Return(jen.Id(qg.recv))
	})

	// Limit
	qg.f.Comment("Limit the number of records to be returned by this query.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Limit").Params(
		jen.Id("n").Int(),
	).Qual(qg.entityPkgPath, qg.querierIface).Block(
		jen.Id(qg.recv).Dot("ctx").Dot("Limit").Op("=").Op("&").Id("n"),
		jen.Return(jen.Id(qg.recv)),
	)

	// Offset
	qg.f.Comment("Offset to start from.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Offset").Params(
		jen.Id("n").Int(),
	).Qual(qg.entityPkgPath, qg.querierIface).Block(
		jen.Id(qg.recv).Dot("ctx").Dot("Offset").Op("=").Op("&").Id("n"),
		jen.Return(jen.Id(qg.recv)),
	)

	// Unique
	qg.f.Comment("Unique configures the query builder to filter duplicate records.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Unique").Params(
		jen.Id("unique").Bool(),
	).Qual(qg.entityPkgPath, qg.querierIface).Block(
		jen.Id(qg.recv).Dot("ctx").Dot("Unique").Op("=").Op("&").Id("unique"),
		jen.Return(jen.Id(qg.recv)),
	)

	// Order
	qg.f.Comment("Order specifies how the records should be ordered.")
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Order").Params(
		jen.Id("o").Op("...").Func().Params(jen.Op("*").Qual(qg.sqlPkg, "Selector")),
	).Qual(qg.entityPkgPath, qg.querierIface).Block(
		jen.Id(qg.recv).Dot("order").Op("=").Append(
			jen.Id(qg.recv).Dot("order"), jen.Id("o").Op("..."),
		),
		jen.Return(jen.Id(qg.recv)),
	)
}

// genWithEdges emits one WithXxx eager-load method per edge. The child
// query inherits the parent's interceptor store so client.Intercept()
// fires on eager loads too.
func (qg *queryGen) genWithEdges() {
	// =========================================================================
	// WithXxx edge eager-loading methods — stores concrete *XxxQuery
	// =========================================================================

	for _, edge := range qg.t.Edges {
		edgeName := edge.StructField()
		withName := "With" + edgeName
		targetIface := edge.Type.Name + "Querier"
		targetQueryName := edge.Type.Name + "Query"
		callbackField := edgeCallbackField(edge)
		ownFK := edge.OwnFK()

		qg.f.Commentf("%s tells the query-builder to eager-load the %q edge.", withName, edge.Name)
		qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id(withName).Params(
			jen.Id("opts").Op("...").Func().Params(jen.Qual(qg.entityPkgPath, targetIface)),
		).Qual(qg.entityPkgPath, qg.querierIface).BlockFunc(func(body *jen.Group) {
			body.Id("tq").Op(":=").Id("New" + targetQueryName).Call(jen.Id(qg.recv).Dot("config"))
			// Thread the parent's interceptors into the child query so
			// client.Intercept() fires on eager-loads too.
			body.Id("tq").Dot("inters").Op("=").Id(qg.recv).Dot("inters")
			body.For(jen.List(jen.Id("_"), jen.Id("opt")).Op(":=").Range().Id("opts")).Block(
				jen.Id("opt").Call(jen.Id("tq")),
			)
			body.Id(qg.recv).Dot(callbackField).Op("=").Id("tq")
			// Enable FK column selection for M2O and O2O-inverse edges
			// where the FK resides on this entity's table.
			if ownFK {
				body.Id(qg.recv).Dot("withFKs").Op("=").True()
			}
			body.Return(jen.Id(qg.recv))
		})
	}
}

// genWithNamedEdges emits WithNamedXxx for non-unique edges when
// FeatureNamedEdges is on.
func (qg *queryGen) genWithNamedEdges() {
	// =========================================================================
	// WithNamedXxx — named edge loading (FeatureNamedEdges)
	// =========================================================================

	if qg.namedEdgesEnabled {
		for _, edge := range qg.t.Edges {
			if edge.Unique {
				continue // Named edges only apply to non-unique (O2M/M2M) edges.
			}
			edgeName := edge.StructField()
			withNamedName := "WithNamed" + edgeName
			targetQueryName := edge.Type.Name + "Query"
			namedField := "withNamed" + edgeName

			qg.f.Commentf("%s tells the query-builder to eager-load the %q edge with the given name.", withNamedName, edge.Name)
			qg.f.Commentf("The optional arguments are used to configure the query builder of the edge.")
			qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id(withNamedName).Params(
				jen.Id("name").String(),
				jen.Id("opts").Op("...").Func().Params(jen.Op("*").Id(targetQueryName)),
			).Op("*").Id(qg.queryName).BlockFunc(func(body *jen.Group) {
				body.Id("query").Op(":=").Id("New" + targetQueryName).Call(jen.Id(qg.recv).Dot("config"))
				// Thread the parent's interceptors into the child query
				// so client.Intercept() fires on named eager-loads too.
				body.Id("query").Dot("inters").Op("=").Id(qg.recv).Dot("inters")
				body.For(jen.List(jen.Id("_"), jen.Id("opt")).Op(":=").Range().Id("opts")).Block(
					jen.Id("opt").Call(jen.Id("query")),
				)
				body.If(jen.Id(qg.recv).Dot(namedField).Op("==").Nil()).Block(
					jen.Id(qg.recv).Dot(namedField).Op("=").Make(jen.Map(jen.String()).Op("*").Id(targetQueryName)),
				)
				body.Id(qg.recv).Dot(namedField).Index(jen.Id("name")).Op("=").Id("query")
				body.Return(jen.Id(qg.recv))
			})
		}
	}
}

// genQueryEdges emits one QueryXxx traversal per edge. The child query's
// path closure builds the parent selector and steps across the edge via
// sqlgraph.SetNeighbors.
func (qg *queryGen) genQueryEdges() {
	// =========================================================================
	// QueryXxx edge traversal methods
	// =========================================================================

	for _, edge := range qg.t.Edges {
		edgeName := edge.StructField()
		methodName := "Query" + edgeName
		targetIface := edge.Type.Name + "Querier"
		targetQueryName := edge.Type.Name + "Query"

		// Get the entity sub-package paths
		srcEntitySubPkg := qg.h.LeafPkgPath(qg.t)
		targetEntitySubPkg := qg.h.LeafPkgPath(edge.Type)

		qg.f.Commentf("%s chains the current query on the %q edge.", methodName, edge.Name)
		qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id(methodName).Params().Qual(qg.entityPkgPath, targetIface).BlockFunc(func(grp *jen.Group) {
			// Create new target query (SAME PACKAGE!)
			grp.Id("tq").Op(":=").Id("New" + targetQueryName).Call(jen.Id(qg.recv).Dot("config"))
			// Thread the parent's interceptors into the child query so
			// client.Intercept() fires on chained edge traversals too.
			grp.Id("tq").Dot("inters").Op("=").Id(qg.recv).Dot("inters")

			// Set up the path closure for sub-select traversal
			grp.Id("tq").Dot("path").Op("=").Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
			).Params(
				jen.Op("*").Qual(qg.sqlPkg, "Selector"),
				jen.Error(),
			).BlockFunc(func(body *jen.Group) {
				body.List(jen.Id("from"), jen.Err()).Op(":=").Id(qg.recv).Dot("buildQuery").Call(jen.Id("ctx"))
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
				body.Id("step").Op(":=").Qual(qg.sqlgraphPkg, "NewStep").Call(
					jen.Qual(qg.sqlgraphPkg, "From").Call(jen.Qual(srcEntitySubPkg, "Table"), jen.Qual(srcEntitySubPkg, qg.t.ID.Constant())),
					jen.Qual(qg.sqlgraphPkg, "To").Call(
						jen.Qual(targetEntitySubPkg, "Table"),
						jen.Qual(targetEntitySubPkg, "FieldID"),
					),
					jen.Qual(qg.sqlgraphPkg, "Edge").Call(
						jen.Qual(qg.sqlgraphPkg, qg.h.EdgeRelType(edge)),
						jen.Lit(edge.IsInverse()),
						jen.Qual(srcEntitySubPkg, edge.TableConstant()),
						edgeColumns,
					),
				)
				body.Id("step").Dot("From").Dot("V").Op("=").Id("from")
				// Schema config stamping for multi-tenancy.
				if qg.schemaConfigEnabled {
					body.Id("schemaConfig").Op(":=").Id(qg.recv).Dot("schemaConfig")
					for _, stmt := range genSchemaConfigStampStep(qg.t, edge) {
						body.Add(stmt)
					}
				}
				body.Return(
					jen.Qual(qg.sqlgraphPkg, "SetNeighbors").Call(
						jen.Id(qg.recv).Dot("config").Dot("Driver").Dot("Dialect").Call(),
						jen.Id("step"),
					),
					jen.Nil(),
				)
			})
			grp.Return(jen.Id("tq"))
		})
	}
}

// genClonePublic emits the interface-typed Clone wrapper over clone().
func (qg *queryGen) genClonePublic() {
	// =========================================================================
	// Clone
	// =========================================================================

	qg.f.Commentf("Clone returns a duplicate of the %s builder.", qg.queryName)
	qg.f.Func().Params(jen.Id(qg.recv).Op("*").Id(qg.queryName)).Id("Clone").Params().Qual(qg.entityPkgPath, qg.querierIface).Block(
		jen.Return(jen.Id(qg.recv).Dot("clone").Call()),
	)
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
