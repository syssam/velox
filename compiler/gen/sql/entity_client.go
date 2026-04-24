package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genEntityClient generates an entity client for entity sub-packages.
// The client provides CRUD methods that create builder instances.
// Create/Update/Delete call local constructors, Query uses runtime.NewEntityQuery,
// and edge queries use runtime.NewEntityQuery with SetPath.
func genEntityClient(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	f := h.NewFile(h.Pkg())

	clientName := t.ClientName()
	entityPkg := h.SharedEntityPkg()

	// Client struct — uses runtime types to avoid model/ dependency.
	// hookStore and interStore provide direct field access (zero indirection).
	f.Commentf("%s is the client for interacting with the %s builders.", clientName, t.Name)
	f.Type().Id(clientName).StructFunc(func(grp *jen.Group) {
		grp.Id("config").Qual(runtimePkg, "Config")
		grp.Id("hookStore").Op("*").Qual(entityPkg, "HookStore")
		grp.Id("interStore").Op("*").Qual(entityPkg, "InterceptorStore")
		if t.NumPolicy() > 0 {
			grp.Id("policy").Qual(h.VeloxPkg(), "Policy")
		}
	})

	// Constructor — type-asserts HookStore/InterStore once from Config.
	// Falls back to empty stores if Config carries nil (e.g., in tests).
	f.Commentf("New%s creates a new %s.", clientName, clientName)
	f.Func().Id("New" + clientName).Params(
		jen.Id("c").Qual(runtimePkg, "Config"),
	).Op("*").Id(clientName).BlockFunc(func(grp *jen.Group) {
		grp.Id("hs").Op(",").Id("_").Op(":=").Id("c").Dot("HookStore").Assert(jen.Op("*").Qual(entityPkg, "HookStore"))
		grp.If(jen.Id("hs").Op("==").Nil()).Block(
			jen.Id("hs").Op("=").Op("&").Qual(entityPkg, "HookStore").Values(),
		)
		grp.Id("is").Op(",").Id("_").Op(":=").Id("c").Dot("InterStore").Assert(jen.Op("*").Qual(entityPkg, "InterceptorStore"))
		grp.If(jen.Id("is").Op("==").Nil()).Block(
			jen.Id("is").Op("=").Op("&").Qual(entityPkg, "InterceptorStore").Values(),
		)
		vals := jen.Dict{
			jen.Id("config"):     jen.Id("c"),
			jen.Id("hookStore"):  jen.Id("hs"),
			jen.Id("interStore"): jen.Id("is"),
		}
		if t.NumPolicy() > 0 {
			vals[jen.Id("policy")] = jen.Qual(h.EntityPkgPath(t), "RuntimePolicy")
		}
		grp.Return(jen.Op("&").Id(clientName).Values(vals))
	})

	// Generate full CRUD + Query methods.
	genEntityClientCRUD(h, f, t)
	genEntityClientQueryMethod(h, f, t)
	genEntityClientGetMethods(h, f, t)
	genEntityClientEdgeQueryMethods(h, f, t)

	// Use adds mutation hooks to this entity client via direct field access.
	f.Commentf("Use adds the mutation hooks to the %s.", clientName)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Use").Params(
		jen.Id("hooks").Op("...").Qual(runtimePkg, "Hook"),
	).Block(
		jen.Id("c").Dot("hookStore").Dot(t.Name).Op("=").Append(
			jen.Id("c").Dot("hookStore").Dot(t.Name),
			jen.Id("hooks").Op("..."),
		),
	)

	// Intercept adds query interceptors to this entity client via direct field access.
	f.Commentf("Intercept adds the query interceptors to the %s.", clientName)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Intercept").Params(
		jen.Id("interceptors").Op("...").Qual(runtimePkg, "Interceptor"),
	).Block(
		jen.Id("c").Dot("interStore").Dot(t.Name).Op("=").Append(
			jen.Id("c").Dot("interStore").Dot(t.Name),
			jen.Id("interceptors").Op("..."),
		),
	)

	// Hooks returns the registered hooks for this entity client via direct field access.
	f.Commentf("Hooks returns the registered hooks of the %s.", clientName)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Hooks").Params().Index().Qual(runtimePkg, "Hook").Block(
		jen.Return(jen.Id("c").Dot("hookStore").Dot(t.Name)),
	)

	// Interceptors returns the registered interceptors for this entity client via direct field access.
	f.Commentf("Interceptors returns the registered interceptors of the %s.", clientName)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Interceptors").Params().Index().Qual(runtimePkg, "Interceptor").Block(
		jen.Return(jen.Id("c").Dot("interStore").Dot(t.Name)),
	)

	// mutate — private dispatch method.
	genEntitySubPkgMutateMethod(h, f, t)

	return f
}

// genEntityClientCRUD generates Create, CreateBulk, MapCreateBulk, Update, Delete,
// UpdateOneID, UpdateOne, DeleteOneID, DeleteOne methods for entity sub-package mode.
// All builder types are local (same package), so no package qualification is needed.
func genEntityClientCRUD(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	clientName := t.ClientName()
	mutName := t.MutationName()
	idType := h.IDType(t)

	entityPkg := h.SharedEntityPkg()

	// Create returns a create builder (concrete type for full API access including SetInput).
	f.Commentf("Create returns a builder for creating a %s entity.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Create").Params().Op("*").Id(t.CreateName()).BlockFunc(func(grp *jen.Group) {
		grp.Id("mutation").Op(":=").Id("New"+mutName).Call(
			jen.Id("c").Dot("config"),
			jen.Qual(runtimePkg, "OpCreate"),
		)
		args := []jen.Code{
			jen.Id("c").Dot("config"),
			jen.Id("mutation"),
			jen.Id("c").Dot("Hooks").Call(),
		}
		if t.NumPolicy() > 0 {
			args = append(args, jen.Id("c").Dot("policy"))
		}
		grp.Return(jen.Id("New" + t.CreateName()).Call(args...))
	})

	// CreateBulk returns a bulk create builder.
	bulkName := t.CreateBulkName()
	f.Commentf("CreateBulk returns a builder for creating a bulk of %s entities.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("CreateBulk").Params(
		jen.Id("builders").Op("...").Op("*").Id(t.CreateName()),
	).Op("*").Id(bulkName).Block(
		jen.Return(jen.Id("New"+bulkName).Call(
			jen.Id("c").Dot("config"),
			jen.Id("builders"),
		)),
	)

	// MapCreateBulk creates a bulk builder from a slice using a mapping function.
	f.Commentf("MapCreateBulk creates a bulk creation builder from the given slice.")
	f.Commentf("For each item in the slice, the set function is called to configure the")
	f.Commentf("builder for that item.")
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("MapCreateBulk").Params(
		jen.Id("slice").Any(),
		jen.Id("setFunc").Func().Params(
			jen.Op("*").Id(t.CreateName()),
			jen.Int(),
		),
	).Op("*").Id(bulkName).Block(
		jen.Id("rv").Op(":=").Qual("reflect", "ValueOf").Call(jen.Id("slice")),
		jen.If(jen.Id("rv").Dot("Kind").Call().Op("!=").Qual("reflect", "Slice")).Block(
			jen.Return(jen.Op("&").Id(bulkName).Values(jen.Dict{
				jen.Id("err"): jen.Qual("fmt", "Errorf").Call(
					jen.Lit("calling to %T.MapCreateBulk with wrong type %T, need slice"),
					jen.Id("c"),
					jen.Id("slice"),
				),
			})),
		),
		jen.Id("builders").Op(":=").Make(
			jen.Index().Op("*").Id(t.CreateName()),
			jen.Id("rv").Dot("Len").Call(),
		),
		jen.For(jen.Id("i").Op(":=").Lit(0), jen.Id("i").Op("<").Id("rv").Dot("Len").Call(), jen.Id("i").Op("++")).Block(
			jen.Id("builders").Index(jen.Id("i")).Op("=").Id("c").Dot("Create").Call(),
			jen.Id("setFunc").Call(jen.Id("builders").Index(jen.Id("i")), jen.Id("i")),
		),
		jen.Return(jen.Id("New"+bulkName).Call(
			jen.Id("c").Dot("config"),
			jen.Id("builders"),
		)),
	)

	// Update returns an update builder.
	f.Commentf("Update returns an update builder for %s.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Update").Params().Op("*").Id(t.UpdateName()).BlockFunc(func(grp *jen.Group) {
		grp.Id("mutation").Op(":=").Id("New"+mutName).Call(
			jen.Id("c").Dot("config"),
			jen.Qual(runtimePkg, "OpUpdate"),
		)
		args := []jen.Code{
			jen.Id("c").Dot("config"),
			jen.Id("mutation"),
			jen.Id("c").Dot("Hooks").Call(),
		}
		if t.NumPolicy() > 0 {
			args = append(args, jen.Id("c").Dot("policy"))
		}
		grp.Return(jen.Id("New" + t.UpdateName()).Call(args...))
	})

	// Delete returns a delete builder.
	f.Commentf("Delete returns a delete builder for %s.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Delete").Params().Op("*").Id(t.DeleteName()).BlockFunc(func(grp *jen.Group) {
		grp.Id("mutation").Op(":=").Id("New"+mutName).Call(
			jen.Id("c").Dot("config"),
			jen.Qual(runtimePkg, "OpDelete"),
		)
		args := []jen.Code{
			jen.Id("c").Dot("config"),
			jen.Id("mutation"),
			jen.Id("c").Dot("Hooks").Call(),
		}
		if t.NumPolicy() > 0 {
			args = append(args, jen.Id("c").Dot("policy"))
		}
		grp.Return(jen.Id("New" + t.DeleteName()).Call(args...))
	})

	// UpdateOneID returns an update-one builder for the given id.
	f.Commentf("UpdateOneID returns an update builder for the given id.")
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("UpdateOneID").Params(
		jen.Id("id").Add(idType),
	).Op("*").Id(t.UpdateOneName()).BlockFunc(func(grp *jen.Group) {
		grp.Id("mutation").Op(":=").Id("New"+mutName).Call(
			jen.Id("c").Dot("config"),
			jen.Qual(runtimePkg, "OpUpdateOne"),
		)
		grp.Id("mutation").Dot("SetID").Call(jen.Id("id"))
		args := []jen.Code{
			jen.Id("c").Dot("config"),
			jen.Id("mutation"),
			jen.Id("c").Dot("Hooks").Call(),
		}
		if t.NumPolicy() > 0 {
			args = append(args, jen.Id("c").Dot("policy"))
		}
		grp.Return(jen.Id("New" + t.UpdateOneName()).Call(args...))
	})

	// UpdateOne returns an update builder for the given entity.
	f.Commentf("UpdateOne returns an update builder for the given %s entity.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("UpdateOne").Params(
		jen.Id("v").Op("*").Qual(entityPkg, t.Name),
	).Op("*").Id(t.UpdateOneName()).Block(
		jen.Return(jen.Id("c").Dot("UpdateOneID").Call(jen.Id("v").Dot("ID"))),
	)

	// DeleteOneID returns a delete-one builder for the given id.
	f.Commentf("DeleteOneID returns a delete builder for the given id.")
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("DeleteOneID").Params(
		jen.Id("id").Add(idType),
	).Op("*").Id(t.DeleteOneName()).BlockFunc(func(grp *jen.Group) {
		// Construct the delete builder directly (not via c.Delete()) so we can
		// access the concrete mutation field for SetID/SetOp before returning.
		grp.Id("mutation").Op(":=").Id("New"+mutName).Call(
			jen.Id("c").Dot("config"),
			jen.Qual(runtimePkg, "OpDeleteOne"),
		)
		grp.Id("mutation").Dot("SetID").Call(jen.Id("id"))
		grp.Id("mutation").Dot("Where").Call(
			jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
				jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "EQ").Call(
					jen.Id("s").Dot("C").Call(jen.Qual(h.EntityPkgPath(t), "FieldID")),
					jen.Id("id"),
				)),
			),
		)
		delArgs := []jen.Code{
			jen.Id("c").Dot("config"),
			jen.Id("mutation"),
			jen.Id("c").Dot("Hooks").Call(),
		}
		if t.NumPolicy() > 0 {
			delArgs = append(delArgs, jen.Id("c").Dot("policy"))
		}
		grp.Id("builder").Op(":=").Id("New" + t.DeleteName()).Call(delArgs...)
		grp.Return(jen.Id("New" + t.DeleteOneName()).Call(jen.Id("builder")))
	})

	// DeleteOne returns a delete builder for the given entity.
	f.Commentf("DeleteOne returns a builder for deleting the given %s entity.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("DeleteOne").Params(
		jen.Id("v").Op("*").Qual(entityPkg, t.Name),
	).Op("*").Id(t.DeleteOneName()).Block(
		jen.Return(jen.Id("c").Dot("DeleteOneID").Call(jen.Id("v").Dot("ID"))),
	)
}

// genEntityClientQueryMethod generates Query() on the entity client for entity sub-package mode.
// Uses runtime.NewEntityQuery to avoid importing query/ (which would cause an import cycle).
// Returns entity.XxxQuerier interface.
func genEntityClientQueryMethod(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	clientName := t.ClientName()

	entityPkg := h.SharedEntityPkg()

	querierType := t.Name + "Querier"

	hasPolicy := h.FeatureEnabled(gen.FeaturePrivacy.Name) && t.NumPolicy() > 0

	f.Commentf("Query returns a query builder for %s entities.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Query").Params().Qual(entityPkg, querierType).BlockFunc(func(grp *jen.Group) {
		grp.Id("q").Op(":=").Qual(runtimePkg, "NewEntityQuery").Call(
			jen.Lit(t.Name),
			jen.Id("c").Dot("config"),
		)
		// SP-2: wire the shared *entity.InterceptorStore pointer onto the
		// new query so client.Intercept(...) is immediately visible — even
		// to queries constructed before the call.
		grp.Add(assertSetInterStore("q", entityPkg, jen.Id("c").Dot("interStore")))
		// Privacy: wire the entity's policy so prepareQuery can invoke
		// policy.EvalQuery explicitly (outside the interceptor chain).
		if hasPolicy {
			grp.Add(assertSetPolicy("q", h.VeloxPkg(), jen.Id("c").Dot("policy")))
		}
		grp.Return(jen.Id("q").Op(".").Parens(jen.Qual(entityPkg, querierType)))
	})
}

// genEntityClientGetMethods generates Get and GetX methods for entity sub-package mode.
// Get uses Query().Where(...).Only(ctx) internally.
func genEntityClientGetMethods(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	clientName := t.ClientName()
	idType := h.IDType(t)

	entityPkg := h.SharedEntityPkg()

	// Get(ctx, id)
	f.Commentf("Get returns the %s entity with the given id.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Get").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("id").Add(idType),
	).Params(jen.Op("*").Qual(entityPkg, t.Name), jen.Error()).Block(
		jen.Return(jen.Id("c").Dot("Query").Call().Dot("Where").Call(
			jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
				jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "EQ").Call(
					jen.Id("s").Dot("C").Call(jen.Qual(h.EntityPkgPath(t), "FieldID")),
					jen.Id("id"),
				)),
			),
		).Dot("Only").Call(jen.Id("ctx"))),
	)

	// GetX(ctx, id) — panic version
	f.Commentf("GetX is like Get, but panics if an error occurs.")
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("GetX").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("id").Add(idType),
	).Op("*").Qual(entityPkg, t.Name).Block(
		jen.List(jen.Id("obj"), jen.Id("err")).Op(":=").Id("c").Dot("Get").Call(jen.Id("ctx"), jen.Id("id")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("obj")),
	)
}

// genEntityClientEdgeQueryMethods generates QueryXxx methods for each edge
// on the entity client in entity sub-package mode.
// Uses runtime.NewEntityQuery for the target type and sets the path via
// interface type assertion to avoid importing query/ directly.
func genEntityClientEdgeQueryMethods(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	clientName := t.ClientName()

	entityPkg := h.SharedEntityPkg()

	for _, e := range t.Edges {
		edgePascal := e.StructField()
		targetQuerierIface := e.Type.Name + "Querier"

		f.Commentf("Query%s queries the %q edge of a %s.", edgePascal, e.Name, t.Name)
		f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Query"+edgePascal).Params(
			jen.Id("v").Op("*").Qual(entityPkg, t.Name),
		).Qual(entityPkg, targetQuerierIface).BlockFunc(func(grp *jen.Group) {
			// tq := runtime.NewEntityQuery(targetTypeName, c.config)
			grp.Id("tq").Op(":=").Qual(runtimePkg, "NewEntityQuery").Call(
				jen.Lit(e.Type.Name),
				jen.Id("c").Dot("config"),
			)
			// SP-2: wire the shared *entity.InterceptorStore pointer for
			// the EDGE TARGET entity onto the new edge query.
			grp.Add(assertSetInterStore("tq", entityPkg, jen.Id("c").Dot("interStore")))
			// Privacy: look up the TARGET entity's policy in the runtime
			// registry (populated by the target sub-package's init) and
			// wire it onto the edge query. No-op for targets without a
			// policy — the SetPolicy assertion returns !ok.
			grp.Id("_tp").Op(":=").Qual(runtimePkg, "EntityPolicy").Call(jen.Lit(e.Type.Name))
			grp.If(jen.Id("_tp").Op("!=").Nil()).Block(
				assertSetPolicy("tq", h.VeloxPkg(), jen.Id("_tp")),
			)
			// Set graph traversal path via inline interface assertion.
			grp.Add(assertSetPath("tq", h.SQLPkg(), jen.Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
			).Params(
				jen.Op("*").Qual(h.SQLPkg(), "Selector"),
				jen.Error(),
			).BlockFunc(func(body *jen.Group) {
				genEntityClientEdgePathClosure(h, body, t, e, "v", "c")
			})))
			grp.Return(jen.Id("tq").Op(".").Parens(jen.Qual(entityPkg, targetQuerierIface)))
		})
	}
}

// genEntityClientEdgePathClosure generates the SetPath closure body for edge queries
// in entity sub-package mode. Uses string literals for target table/fieldID to avoid
// importing target entity subpackages (which would create import cycles).
//
// entityVar is the variable name holding the entity (e.g., "v" in client methods,
// "_e" in entity struct methods). configVar is the variable holding .config access
// (e.g., "c" for client receiver, "_e" for entity struct receiver).
func genEntityClientEdgePathClosure(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, e *gen.Edge, entityVar, configVar string) {
	sqlgraphPkg := h.SQLGraphPkg()

	// id := <entityVar>.ID
	grp.Id("id").Op(":=").Id(entityVar).Dot("ID")

	// Edge columns: M2M uses PKConstant (variadic), others use ColumnConstant (single)
	leafPkg := h.EntityPkgPath(t)
	var edgeColumns jen.Code
	if e.M2M() {
		grp.Id("_edgePKs").Op(":=").Qual(leafPkg, e.PKConstant())
		edgeColumns = jen.Id("_edgePKs").Op("...")
	} else {
		edgeColumns = jen.Qual(leafPkg, e.ColumnConstant())
	}

	// Use string literals for target table/fieldID to avoid cross-entity imports.
	// Source constants (Table, FieldID, edge constants) live in the leaf — qualify them.
	targetTable := jen.Lit(e.Type.Table())
	targetFieldID := jen.Lit(e.Type.ID.StorageKey())

	grp.Id("step").Op(":=").Qual(sqlgraphPkg, "NewStep").Call(
		jen.Qual(sqlgraphPkg, "From").Call(jen.Qual(leafPkg, "Table"), jen.Qual(leafPkg, t.ID.Constant()), jen.Id("id")),
		jen.Qual(sqlgraphPkg, "To").Call(targetTable, targetFieldID),
		jen.Qual(sqlgraphPkg, "Edge").Call(
			jen.Qual(sqlgraphPkg, h.EdgeRelType(e)),
			jen.Lit(e.IsInverse()),
			jen.Qual(leafPkg, e.TableConstant()),
			edgeColumns,
		),
	)
	grp.Return(
		jen.Qual(sqlgraphPkg, "Neighbors").Call(
			jen.Id(configVar).Dot("config").Dot("Driver").Dot("Dialect").Call(),
			jen.Id("step"),
		),
		jen.Nil(),
	)
}

// genEntitySubPkgMutateMethod generates the private mutate method on the entity client
// for entity sub-package mode. Dispatches based on Op to the appropriate local builder.
func genEntitySubPkgMutateMethod(_ gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	clientName := t.ClientName()
	mutName := t.MutationName()

	// Helper to generate a builder call for a given op.
	builderCall := func(grp *jen.Group, builderName, method string) {
		args := []jen.Code{
			jen.Id("c").Dot("config"),
			jen.Id("m"),
			jen.Id("c").Dot("Hooks").Call(),
		}
		if t.NumPolicy() > 0 {
			args = append(args, jen.Id("c").Dot("policy"))
		}
		grp.Id("builder").Op(":=").Id("New" + builderName).Call(args...)
		grp.Return(jen.Id("builder").Dot(method).Call(jen.Id("ctx")))
	}

	f.Commentf("mutate executes a mutation against the %s entity based on the operation type.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("mutate").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("m").Op("*").Id(mutName),
	).Params(jen.Any(), jen.Error()).Block(
		jen.Switch(jen.Id("m").Dot("Op").Call()).BlockFunc(func(sw *jen.Group) {
			sw.Case(jen.Qual(runtimePkg, "OpCreate")).BlockFunc(func(grp *jen.Group) {
				builderCall(grp, t.CreateName(), "Save")
			})
			sw.Case(jen.Qual(runtimePkg, "OpUpdate")).BlockFunc(func(grp *jen.Group) {
				builderCall(grp, t.UpdateName(), "Save")
			})
			sw.Case(jen.Qual(runtimePkg, "OpUpdateOne")).BlockFunc(func(grp *jen.Group) {
				builderCall(grp, t.UpdateOneName(), "Save")
			})
			sw.Case(jen.Qual(runtimePkg, "OpDelete"), jen.Qual(runtimePkg, "OpDeleteOne")).BlockFunc(func(grp *jen.Group) {
				builderCall(grp, t.DeleteName(), "Exec")
			})
			sw.Default().Block(
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(
					jen.Lit("unknown %s mutation op: %q"),
					jen.Lit(t.Name),
					jen.Id("m").Dot("Op").Call(),
				)),
			)
		}),
	)
}
