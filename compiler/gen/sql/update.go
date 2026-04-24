package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genUpdate generates update builders as a standalone file.
// Returns (*jen.File, error) for interface consistency.
func genUpdate(h gen.GeneratorHelper, t *gen.Type) (*jen.File, error) { //nolint:unparam // error kept for interface consistency
	f := h.NewFile(h.Pkg())
	genUpdateInto(h, f, t)
	return f, nil
}

// genUpdateInto appends update builders to an existing *jen.File.
// Generated types:
//   - {Entity}Update      — bulk update (Save returns int, no wrapping needed)
//   - {Entity}UpdateOne   — single update (Save returns *Entity, needs wrapping)
func genUpdateInto(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	entityPkg := h.EntityPkgPath(t)
	// Entity package path for return types (entity.User).
	entityReturnPkg := h.SharedEntityPkg()
	mutName := t.MutationName()

	// Concrete return types for chaining methods — no interface indirection.
	var updaterIface jen.Code    // nil → chainReturnType falls back to *BuilderName
	var updateOnerIface jen.Code // nil → chainReturnType falls back to *BuilderName

	// NOTE: genEdgeConfigVars is NOT called here because wrapper_create.go
	// already generates the package-level edge config vars for this entity.

	genUpdateBulk(h, f, t, entityPkg, mutName, updaterIface)
	genUpdateOne(h, f, t, entityPkg, entityReturnPkg, mutName, updateOnerIface)

	// Shared defaults function and per-builder wrappers (only when entity has UpdateDefault fields).
	if t.HasUpdateDefault() {
		genUpdateDefaultsFunc(h, f, t)
		genUpdateDefaultsMethod(f, t, t.UpdateName(), "_u")
		genUpdateDefaultsMethod(f, t, t.UpdateOneName(), "_u")
		genUpdateSkipDefaults(f, t.UpdateName(), "_u", updaterIface)
		genUpdateSkipDefaults(f, t.UpdateOneName(), "_u", updateOnerIface)
	}
}

// genUpdateBulk generates the root-level UserUpdate builder.
// Save returns (int, error) — no entity wrapping needed.
func genUpdateBulk(h gen.GeneratorHelper, f *jen.File, t *gen.Type, entityPkg, mutName string, ifaceReturn jen.Code) {
	updateName := t.UpdateName() // "UserUpdate"
	recv := "_u"

	// --- Struct ---
	f.Commentf("%s is the update builder for %s entities.", updateName, t.Name)
	f.Type().Id(updateName).StructFunc(func(group *jen.Group) {
		group.Id("config").Qual(runtimePkg, "Config")
		if h.FeatureEnabled(gen.FeatureSchemaConfig.Name) {
			group.Id("schemaConfig").Qual(h.InternalPkg(), "SchemaConfig")
		}
		group.Id("mutation").Op("*").Id(mutName)
		group.Id("hooks").Index().Qual(runtimePkg, "Hook")
		if t.NumPolicy() > 0 {
			group.Id("policy").Qual(h.VeloxPkg(), "Policy")
		}
		group.Id("modifiers").Index().Func().Params(jen.Op("*").Qual(h.SQLPkg(), "UpdateBuilder"))
		if t.HasUpdateDefault() {
			group.Id("skipDefaults").Bool()
			group.Id("skipDefaultFields").Map(jen.String()).Struct()
		}
	})

	// --- Constructor ---
	f.Commentf("New%s creates a new %s builder.", updateName, updateName)
	f.Func().Id("New" + updateName).ParamsFunc(func(pg *jen.Group) {
		pg.Id("c").Qual(runtimePkg, "Config")
		pg.Id("mutation").Op("*").Id(mutName)
		pg.Id("hooks").Index().Qual(runtimePkg, "Hook")
		if t.NumPolicy() > 0 {
			pg.Id("policy").Qual(h.VeloxPkg(), "Policy")
		}
	}).Op("*").Id(updateName).BlockFunc(func(grp *jen.Group) {
		d := jen.Dict{
			jen.Id("config"):   jen.Id("c"),
			jen.Id("mutation"): jen.Id("mutation"),
			jen.Id("hooks"):    jen.Id("hooks"),
		}
		if t.NumPolicy() > 0 {
			d[jen.Id("policy")] = jen.Id("policy")
		}
		grp.Return(jen.Op("&").Id(updateName).Values(d))
	})

	// --- Where ---
	retType := chainReturnType(updateName, ifaceReturn)
	f.Commentf("Where appends a list predicates to the %s builder.", updateName)
	f.Func().Params(jen.Id(recv).Op("*").Id(updateName)).Id("Where").Params(
		jen.Id("ps").Op("...").Add(h.PredicateType(t)),
	).Add(retType).Block(
		jen.Id(recv).Dot("mutation").Dot("Where").Call(jen.Id("ps").Op("...")),
		jen.Return(jen.Id(recv)),
	)

	// --- Field setters (mutable fields only) ---
	for _, fd := range t.MutableFields() {
		genFieldSetter(h, f, updateName, recv, fd, true, "mutation", ifaceReturn)
	}

	// --- Edge setters ---
	for _, edge := range t.EdgesWithID() {
		if fld := edge.Field(); fld != nil && fld.UserDefined {
			continue
		}
		genEdgeSetter(h, f, updateName, recv, t, edge, true, "mutation", ifaceReturn)
	}

	// --- Entity-reference edge setters (typed convenience methods) ---
	for _, edge := range t.EdgesWithID() {
		if fld := edge.Field(); fld != nil && fld.UserDefined {
			continue
		}
		genEdgeEntitySetter(h, f, updateName, recv, edge, true, ifaceReturn)
	}

	// --- Mutation ---
	f.Commentf("Mutation returns the %s.", mutName)
	f.Func().Params(jen.Id(recv).Op("*").Id(updateName)).Id("Mutation").Params().Op("*").Id(mutName).Block(
		jen.Return(jen.Id(recv).Dot("mutation")),
	)

	// --- Modify ---
	genUpdateModify(h, f, updateName, recv, ifaceReturn)

	// --- check() for required-edge validation ---
	genUpdateCheck(f, t, updateName, recv)

	// --- sqlSave (named method — Ent pattern) ---
	f.Commentf("sqlSave executes the SQL update for %s after hooks have run.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(updateName)).Id("sqlSave").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Int(), jen.Error()).BlockFunc(func(grp *jen.Group) {
		// Validate required edges before executing SQL.
		if hasRequiredUniqueEdge(t) {
			grp.If(jen.Id("err").Op(":=").Id(recv).Dot("check").Call(), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Lit(0), jen.Id("err")),
			)
		}
		// Build UpdateSpec from typed mutation fields.
		genUpdateSpecBuild(h, grp, t, recv, false)
		// Add predicates from the mutation.
		grp.Id("ps").Op(":=").Id(recv).Dot("mutation").Dot("PredicatesFuncs").Call()
		grp.If(jen.Len(jen.Id("ps")).Op(">").Lit(0)).Block(
			jen.Id("spec").Dot("Predicate").Op("=").Func().Params(
				jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector"),
			).Block(
				jen.For(jen.Id("i").Op(":=").Range().Id("ps")).Block(
					jen.Id("ps").Index(jen.Id("i")).Call(jen.Id("s")),
				),
			),
		)
		// Emit edge operations into spec.Edges.Add / spec.Edges.Clear and apply modifiers.
		genUpdateEdgesAndModifiers(h, grp, t, recv, false)
		grp.List(jen.Id("affected"), jen.Id("err")).Op(":=").Qual(h.SQLGraphPkg(), "UpdateNodes").Call(
			jen.Id("ctx"), jen.Id(recv).Dot("config").Dot("Driver"), jen.Id("spec"),
		)
		grp.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Lit(0), jen.Qual(runtimePkg, "MayWrapConstraintError").Call(jen.Id("err"))),
		)
		grp.Return(jen.Id("affected"), jen.Nil())
	})

	// --- Save (returns int, no wrapping) ---
	f.Commentf("Save executes the query and returns the number of nodes affected by the update operation.")
	f.Func().Params(jen.Id(recv).Op("*").Id(updateName)).Id("Save").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Int(), jen.Error()).BlockFunc(func(grp *jen.Group) {
		if t.HasUpdateDefault() {
			grp.If(jen.Id("err").Op(":=").Id(recv).Dot("defaults").Call(), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Lit(0), jen.Id("err")),
			)
		}
		// Explicit privacy check — runs before hooks since privacy no longer rides on Hooks[0].
		if t.NumPolicy() > 0 {
			grp.If(jen.Id(recv).Dot("policy").Op("!=").Nil()).Block(
				jen.If(jen.Id("err").Op(":=").Id(recv).Dot("policy").Dot("EvalMutation").Call(
					jen.Id("ctx"), jen.Id(recv).Dot("mutation"),
				), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Lit(0), jen.Id("err")),
				),
			)
		}
		// Collect hooks: client-level (from Use) + schema-level (from codegen init).
		if t.NumHooks() > 0 {
			grp.Id("hooks").Op(":=").Id("append").Call(jen.Id(recv).Dot("hooks"), jen.Id("Hooks").Index(jen.Op(":")).Op("..."))
		} else {
			grp.Id("hooks").Op(":=").Id(recv).Dot("hooks")
		}
		mutationType := jen.Id(mutName)
		grp.Return(jen.Qual(h.VeloxPkg(), "WithHooks").Types(
			jen.Int(),
			mutationType,
			jen.Op("*").Add(mutationType),
		).Call(
			jen.Id("ctx"), jen.Id(recv).Dot("sqlSave"), jen.Id(recv).Dot("mutation"), jen.Id("hooks"),
		))
	})

	// --- SaveX ---
	f.Commentf("SaveX is like Save, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(updateName)).Id("SaveX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Int().Block(
		jen.List(jen.Id("affected"), jen.Err()).Op(":=").Id(recv).Dot("Save").Call(jen.Id("ctx")),
		jen.If(jen.Err().Op("!=").Nil()).Block(jen.Panic(jen.Err())),
		jen.Return(jen.Id("affected")),
	)

	// --- Exec ---
	f.Comment("Exec executes the query.")
	f.Func().Params(jen.Id(recv).Op("*").Id(updateName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id(recv).Dot("Save").Call(jen.Id("ctx")),
		jen.Return(jen.Id("err")),
	)

	// --- ExecX ---
	f.Comment("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(updateName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id(recv).Dot("Exec").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)
}

// genUpdateOne generates the root-level UserUpdateOne builder.
// Save returns (*entity.User, error) — no wrapping needed.
func genUpdateOne(h gen.GeneratorHelper, f *jen.File, t *gen.Type, entityPkg, entityReturnPkg, mutName string, ifaceReturn jen.Code) {
	updateOneName := t.UpdateOneName() // "UserUpdateOne"
	recv := "_u"

	// --- Struct ---
	f.Commentf("%s is the update builder for a single %s entity.", updateOneName, t.Name)
	f.Type().Id(updateOneName).StructFunc(func(group *jen.Group) {
		group.Id("config").Qual(runtimePkg, "Config")
		if h.FeatureEnabled(gen.FeatureSchemaConfig.Name) {
			group.Id("schemaConfig").Qual(h.InternalPkg(), "SchemaConfig")
		}
		group.Id("mutation").Op("*").Id(mutName)
		group.Id("hooks").Index().Qual(runtimePkg, "Hook")
		if t.NumPolicy() > 0 {
			group.Id("policy").Qual(h.VeloxPkg(), "Policy")
		}
		group.Id("modifiers").Index().Func().Params(jen.Op("*").Qual(h.SQLPkg(), "UpdateBuilder"))
		group.Id("selectFields").Index().String()
		if t.HasUpdateDefault() {
			group.Id("skipDefaults").Bool()
			group.Id("skipDefaultFields").Map(jen.String()).Struct()
		}
	})

	// --- Constructor ---
	f.Commentf("New%s creates a new %s builder.", updateOneName, updateOneName)
	f.Func().Id("New" + updateOneName).ParamsFunc(func(pg *jen.Group) {
		pg.Id("c").Qual(runtimePkg, "Config")
		pg.Id("mutation").Op("*").Id(mutName)
		pg.Id("hooks").Index().Qual(runtimePkg, "Hook")
		if t.NumPolicy() > 0 {
			pg.Id("policy").Qual(h.VeloxPkg(), "Policy")
		}
	}).Op("*").Id(updateOneName).BlockFunc(func(grp *jen.Group) {
		d := jen.Dict{
			jen.Id("config"):   jen.Id("c"),
			jen.Id("mutation"): jen.Id("mutation"),
			jen.Id("hooks"):    jen.Id("hooks"),
		}
		if t.NumPolicy() > 0 {
			d[jen.Id("policy")] = jen.Id("policy")
		}
		grp.Return(jen.Op("&").Id(updateOneName).Values(d))
	})

	// --- Where ---
	retType := chainReturnType(updateOneName, ifaceReturn)
	f.Commentf("Where appends a list predicates to the %s builder.", updateOneName)
	f.Func().Params(jen.Id(recv).Op("*").Id(updateOneName)).Id("Where").Params(
		jen.Id("ps").Op("...").Add(h.PredicateType(t)),
	).Add(retType).Block(
		jen.Id(recv).Dot("mutation").Dot("Where").Call(jen.Id("ps").Op("...")),
		jen.Return(jen.Id(recv)),
	)

	// --- Select ---
	f.Commentf("Select allows selecting one or more fields/columns for the given update query.")
	f.Func().Params(jen.Id(recv).Op("*").Id(updateOneName)).Id("Select").Params(
		jen.Id("field").String(),
		jen.Id("fields").Op("...").String(),
	).Add(retType).Block(
		jen.Id(recv).Dot("selectFields").Op("=").Append(
			jen.Index().String().Values(jen.Id("field")),
			jen.Id("fields").Op("..."),
		),
		jen.Return(jen.Id(recv)),
	)

	// --- Field setters (mutable fields only) ---
	for _, fd := range t.MutableFields() {
		genFieldSetter(h, f, updateOneName, recv, fd, true, "mutation", ifaceReturn)
	}

	// --- Edge setters ---
	for _, edge := range t.EdgesWithID() {
		if fld := edge.Field(); fld != nil && fld.UserDefined {
			continue
		}
		genEdgeSetter(h, f, updateOneName, recv, t, edge, true, "mutation", ifaceReturn)
	}

	// --- Entity-reference edge setters (typed convenience methods) ---
	for _, edge := range t.EdgesWithID() {
		if fld := edge.Field(); fld != nil && fld.UserDefined {
			continue
		}
		genEdgeEntitySetter(h, f, updateOneName, recv, edge, true, ifaceReturn)
	}

	// --- Mutation ---
	f.Commentf("Mutation returns the %s.", mutName)
	f.Func().Params(jen.Id(recv).Op("*").Id(updateOneName)).Id("Mutation").Params().Op("*").Id(mutName).Block(
		jen.Return(jen.Id(recv).Dot("mutation")),
	)

	// --- Modify ---
	genUpdateModify(h, f, updateOneName, recv, ifaceReturn)

	// --- check() for required-edge validation ---
	genUpdateCheck(f, t, updateOneName, recv)

	// --- sqlSave (named method — Ent pattern) ---
	f.Commentf("sqlSave executes the SQL update for a single %s after hooks have run.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(updateOneName)).Id("sqlSave").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Qual(entityReturnPkg, t.Name), jen.Error()).BlockFunc(func(grp *jen.Group) {
		// Validate required edges before executing SQL.
		if hasRequiredUniqueEdge(t) {
			grp.If(jen.Id("err").Op(":=").Id(recv).Dot("check").Call(), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			)
		}
		// Get ID from mutation.
		grp.Id("id").Op(",").Id("ok").Op(":=").Id(recv).Dot("mutation").Dot("ID").Call()
		grp.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Nil(), jen.Qual("errors", "New").Call(jen.Lit("velox: missing ID for UpdateOne"))),
		)
		// Build UpdateSpec from typed mutation fields — selectFields-aware for UpdateOne.
		genUpdateSpecBuild(h, grp, t, recv, true)
		grp.Id("spec").Dot("Predicate").Op("=").Func().Params(
			jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector"),
		).Block(
			jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "EQ").Call(
				jen.Id("s").Dot("C").Call(jen.Qual(entityPkg, "FieldID")),
				jen.Id("id"),
			)),
		)
		// Emit edge operations into spec.Edges.Add / spec.Edges.Clear and apply modifiers.
		genUpdateEdgesAndModifiers(h, grp, t, recv, true)
		grp.List(jen.Id("_"), jen.Id("err")).Op(":=").Qual(h.SQLGraphPkg(), "UpdateNodes").Call(
			jen.Id("ctx"), jen.Id(recv).Dot("config").Dot("Driver"), jen.Id("spec"),
		)
		grp.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Qual(runtimePkg, "MayWrapConstraintError").Call(jen.Id("err"))),
		)
		// Re-query the entity to return the updated version.
		// When selectFields is set, only query those columns (plus ID which is always needed).
		grp.Id("columns").Op(":=").Qual(entityPkg, "Columns")
		grp.If(jen.Len(jen.Id(recv).Dot("selectFields")).Op(">").Lit(0)).Block(
			jen.Id("columns").Op("=").Append(
				jen.Index().String().Values(jen.Qual(entityPkg, "FieldID")),
				jen.Id(recv).Dot("selectFields").Op("..."),
			),
		)
		grp.Id("build").Op(":=").Func().Params(
			jen.Id("_").Qual("context", "Context"),
		).Params(
			jen.Op("*").Qual(h.SQLPkg(), "Selector"), jen.Error(),
		).Block(
			jen.Id("s").Op(":=").Qual(h.SQLPkg(), "Select").Call(jen.Id("columns").Op("...")).Dot("From").Call(
				jen.Qual(h.SQLPkg(), "Table").Call(jen.Qual(entityPkg, "Table")),
			),
			jen.Id("s").Dot("SetDialect").Call(jen.Id(recv).Dot("config").Dot("Driver").Dot("Dialect").Call()),
			jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "EQ").Call(
				jen.Id("s").Dot("C").Call(jen.Qual(entityPkg, "FieldID")),
				jen.Id("id"),
			)),
			jen.Id("s").Dot("Limit").Call(jen.Lit(1)),
			jen.Return(jen.Id("s"), jen.Nil()),
		)
		grp.List(jen.Id("_node"), jen.Id("err2")).Op(":=").Qual(runtimePkg, "ScanFirst").Types(
			jen.Qual(entityReturnPkg, t.Name), jen.Op("*").Qual(entityReturnPkg, t.Name),
		).Call(jen.Id("ctx"), jen.Id(recv).Dot("config").Dot("Driver"), jen.Id("build"), jen.Lit(t.Name))
		grp.If(jen.Id("err2").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err2")),
		)
		// Propagate config onto the returned node — same rationale as Create:
		// without this, edge resolvers panic with a nil QueryContext.
		grp.Id("_node").Dot(t.SetConfigMethodName()).Call(jen.Id(recv).Dot("config"))
		grp.Return(jen.Id("_node"), jen.Nil())
	})

	// --- Save (returns *entity.User, no wrapping) ---
	f.Commentf("Save executes the query and returns the updated %s entity.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(updateOneName)).Id("Save").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Qual(entityReturnPkg, t.Name), jen.Error()).BlockFunc(func(grp *jen.Group) {
		// Wire up typed oldValue closure so OldXxx(ctx) methods work in hooks.
		if len(t.MutableFields()) > 0 {
			grp.Id(recv).Dot("mutation").Dot("oldValue").Op("=").Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
			).Params(jen.Op("*").Qual(entityReturnPkg, t.Name), jen.Error()).BlockFunc(func(loader *jen.Group) {
				loader.Id("id").Op(",").Id("ok").Op(":=").Id(recv).Dot("mutation").Dot("ID").Call()
				loader.If(jen.Op("!").Id("ok")).Block(
					jen.Return(jen.Nil(), jen.Qual("errors", "New").Call(jen.Lit("velox: missing ID for OldField"))),
				)
				loader.Id("build").Op(":=").Func().Params(
					jen.Id("_").Qual("context", "Context"),
				).Params(
					jen.Op("*").Qual(h.SQLPkg(), "Selector"), jen.Error(),
				).Block(
					jen.Id("s").Op(":=").Qual(h.SQLPkg(), "Select").Call(jen.Qual(entityPkg, "Columns").Op("...")).Dot("From").Call(
						jen.Qual(h.SQLPkg(), "Table").Call(jen.Qual(entityPkg, "Table")),
					),
					jen.Id("s").Dot("SetDialect").Call(jen.Id(recv).Dot("config").Dot("Driver").Dot("Dialect").Call()),
					jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "EQ").Call(
						jen.Id("s").Dot("C").Call(jen.Qual(entityPkg, "FieldID")),
						jen.Id("id"),
					)),
					jen.Id("s").Dot("Limit").Call(jen.Lit(1)),
					jen.Return(jen.Id("s"), jen.Nil()),
				)
				loader.List(jen.Id("_old"), jen.Id("err")).Op(":=").Qual(runtimePkg, "ScanFirst").Types(
					jen.Qual(entityReturnPkg, t.Name), jen.Op("*").Qual(entityReturnPkg, t.Name),
				).Call(jen.Id("ctx"), jen.Id(recv).Dot("config").Dot("Driver"), jen.Id("build"), jen.Lit(t.Name))
				loader.If(jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				)
				// Propagate config onto the loaded old entity so hooks that
				// traverse edges off it (e.g. old.QueryPosts()) don't panic.
				loader.Id("_old").Dot(t.SetConfigMethodName()).Call(jen.Id(recv).Dot("config"))
				loader.Return(jen.Id("_old"), jen.Nil())
			})
		}
		if t.HasUpdateDefault() {
			grp.If(jen.Id("err").Op(":=").Id(recv).Dot("defaults").Call(), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			)
		}
		// Explicit privacy check — runs before hooks since privacy no longer rides on Hooks[0].
		if t.NumPolicy() > 0 {
			grp.If(jen.Id(recv).Dot("policy").Op("!=").Nil()).Block(
				jen.If(jen.Id("err").Op(":=").Id(recv).Dot("policy").Dot("EvalMutation").Call(
					jen.Id("ctx"), jen.Id(recv).Dot("mutation"),
				), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				),
			)
		}
		// Collect hooks: client-level (from Use) + schema-level (from codegen init).
		if t.NumHooks() > 0 {
			grp.Id("hooks").Op(":=").Id("append").Call(jen.Id(recv).Dot("hooks"), jen.Id("Hooks").Index(jen.Op(":")).Op("..."))
		} else {
			grp.Id("hooks").Op(":=").Id(recv).Dot("hooks")
		}
		mutationType := jen.Id(mutName)
		grp.Return(jen.Qual(h.VeloxPkg(), "WithHooks").Types(
			jen.Op("*").Qual(entityReturnPkg, t.Name),
			mutationType,
			jen.Op("*").Add(mutationType),
		).Call(
			jen.Id("ctx"), jen.Id(recv).Dot("sqlSave"), jen.Id(recv).Dot("mutation"), jen.Id("hooks"),
		))
	})

	// --- SaveX ---
	f.Commentf("SaveX is like Save, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(updateOneName)).Id("SaveX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Op("*").Qual(entityReturnPkg, t.Name).Block(
		jen.List(jen.Id("node"), jen.Id("err")).Op(":=").Id(recv).Dot("Save").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Panic(jen.Id("err"))),
		jen.Return(jen.Id("node")),
	)

	// --- Exec ---
	f.Comment("Exec executes the query on the entity.")
	f.Func().Params(jen.Id(recv).Op("*").Id(updateOneName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id(recv).Dot("Save").Call(jen.Id("ctx")),
		jen.Return(jen.Id("err")),
	)

	// --- ExecX ---
	f.Comment("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(updateOneName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id(recv).Dot("Exec").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)
}

// genUpdateSpecBuild emits code that builds a sqlgraph.UpdateSpec directly
// from typed mutation fields (_name, _age, _addage, clearedFields). The emitted
// code declares a local variable `spec` used by the caller.
// When hasSelect is true, each field operation is guarded by a check that
// selectFields is empty or contains the field name — so UpdateOne.Select()
// restricts which fields are actually written in the UPDATE SET clause.
func genUpdateSpecBuild(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, recv string, hasSelect bool) {
	entityPkg := h.EntityPkgPath(t)
	fieldPkg := h.FieldPkg()
	sqlGraphPkg := h.SQLGraphPkg()

	grp.Id("spec").Op(":=").Qual(sqlGraphPkg, "NewUpdateSpec").Call(
		jen.Qual(entityPkg, "Table"),
		jen.Qual(entityPkg, "Columns"),
		jen.Op("&").Qual(sqlGraphPkg, "FieldSpec").Values(jen.Dict{
			jen.Id("Column"): jen.Qual(entityPkg, "FieldID"),
			jen.Id("Type"):   jen.Id(idFieldTypeVar(t)),
		}),
	)

	// selectGuard wraps an inner statement with a selectFields check when
	// hasSelect is true. When selectFields is non-empty, only fields present
	// in selectFields are included in the UPDATE SET clause.
	selectGuard := func(fieldName string, inner *jen.Statement) *jen.Statement {
		if !hasSelect {
			return inner
		}
		return jen.If(jen.Len(jen.Id(recv).Dot("selectFields")).Op("==").Lit(0).Op("||").Qual("slices", "Contains").Call(
			jen.Id(recv).Dot("selectFields"), jen.Lit(fieldName),
		)).Block(inner)
	}

	// Set fields from typed mutation pointers (only mutable fields are
	// settable on update builders; immutable fields have no setter).
	for _, fd := range t.MutableFields() {
		if fd.IsEdgeField() && !fd.UserDefined {
			continue
		}
		typedField := "_" + fd.Name
		setStmt := jen.If(jen.Id(recv).Dot("mutation").Dot(typedField).Op("!=").Nil()).Block(
			jen.Id("spec").Dot("SetField").Call(
				jen.Lit(fd.StorageKey()),
				jen.Qual(fieldPkg, h.FieldTypeConstant(fd)),
				jen.Op("*").Id(recv).Dot("mutation").Dot(typedField),
			),
		)
		grp.Add(selectGuard(fd.Name, setStmt))
	}

	// Add fields from typed _addX pointers (numeric increments).
	for _, fd := range t.MutableFields() {
		if fd.IsEdgeField() && !fd.UserDefined {
			continue
		}
		if !fd.SupportsMutationAdd() {
			continue
		}
		addField := "_add" + fd.Name
		addStmt := jen.If(jen.Id(recv).Dot("mutation").Dot(addField).Op("!=").Nil()).Block(
			jen.Id("spec").Dot("AddField").Call(
				jen.Lit(fd.StorageKey()),
				jen.Qual(fieldPkg, h.FieldTypeConstant(fd)),
				jen.Op("*").Id(recv).Dot("mutation").Dot(addField),
			),
		)
		grp.Add(selectGuard(fd.Name, addStmt))
	}

	// Clear fields from the typed clearedFields map. Only nillable fields can be
	// cleared (calling ClearX on non-nillable is not generated). The map is keyed
	// by fd.Name (matching how mutation.go writes it). We emit a direct lookup per
	// nillable field to avoid runtime type lookup and keep clearing deterministic.
	for _, fd := range t.MutableFields() {
		if fd.IsEdgeField() && !fd.UserDefined {
			continue
		}
		if !fd.Nillable {
			continue
		}
		clearStmt := jen.If(
			jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id(recv).Dot("mutation").Dot("clearedFields").Index(jen.Lit(fd.Name)),
			jen.Id("ok"),
		).Block(
			jen.Id("spec").Dot("ClearField").Call(
				jen.Lit(fd.StorageKey()),
				jen.Qual(fieldPkg, h.FieldTypeConstant(fd)),
			),
		)
		grp.Add(selectGuard(fd.Name, clearStmt))
	}
}

// genUpdateEdgesAndModifiers emits the code that appends EdgeSpecs to
// spec.Edges.Add / spec.Edges.Clear based on the mutation edge state, handles
// JSON append modifiers, and applies user modifiers. Mirrors Ent's generated
// update path. isUpdateOne controls whether ClearField/SetField for FK columns
// on non-owning-FK edges are supported (M2M/O2M add/remove needs owner ID).
func genUpdateEdgesAndModifiers(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, recv string, _ bool) {
	entityPkg := h.EntityPkgPath(t)
	fieldPkg := h.FieldPkg()
	sqlGraphPkg := h.SQLGraphPkg()

	// Append fields (JSON array append). Read from the typed `appends` map on the mutation.
	if jsonAppendRelevant(t) {
		grp.For(
			jen.List(jen.Id("col"), jen.Id("val")).Op(":=").Range().Id(recv).Dot("mutation").Dot("appends"),
		).BlockFunc(func(forGrp *jen.Group) {
			forGrp.Id("colCopy").Op(":=").Id("col")
			forGrp.Id("valCopy").Op(":=").Id("val")
			forGrp.Id("d").Op(":=").Id(recv).Dot("config").Dot("Driver").Dot("Dialect").Call()
			forGrp.Id("spec").Dot("AddModifier").Call(
				jen.Func().Params(jen.Id("u").Op("*").Qual(h.SQLPkg(), "UpdateBuilder")).Block(
					jen.List(jen.Id("appendJSON"), jen.Id("err")).Op(":=").Qual("encoding/json", "Marshal").Call(jen.Id("valCopy")),
					jen.If(jen.Id("err").Op("!=").Nil()).Block(
						jen.Id("u").Dot("AddError").Call(jen.Qual("fmt", "Errorf").Call(jen.Lit("marshal append value for %q: %w"), jen.Id("colCopy"), jen.Id("err"))),
						jen.Return(),
					),
					jen.Switch(jen.Id("d")).Block(
						jen.Case(jen.Qual(dialectPkg(), "MySQL")).Block(
							jen.Id("u").Dot("Set").Call(
								jen.Id("colCopy"),
								jen.Qual(h.SQLPkg(), "Expr").Call(
									jen.Qual("fmt", "Sprintf").Call(jen.Lit("JSON_MERGE_PRESERVE(COALESCE(%s, '[]'), ?)"), jen.Id("colCopy")),
									jen.String().Call(jen.Id("appendJSON")),
								),
							),
						),
						jen.Case(jen.Qual(dialectPkg(), "SQLite")).Block(
							// SQLite has no JSON array concat operator. Build the merged
							// array by unioning json_each over the existing column and the
							// incoming value, then re-packing with json_group_array.
							jen.Id("u").Dot("Set").Call(
								jen.Id("colCopy"),
								jen.Qual(h.SQLPkg(), "Expr").Call(
									jen.Qual("fmt", "Sprintf").Call(jen.Lit("(SELECT json_group_array(value) FROM (SELECT value FROM json_each(CAST(COALESCE(%s, '[]') AS TEXT)) UNION ALL SELECT value FROM json_each(?)))"), jen.Id("colCopy")),
									jen.String().Call(jen.Id("appendJSON")),
								),
							),
						),
						jen.Default().Block(
							// PostgreSQL: jsonb || jsonb concatenates arrays.
							jen.Id("u").Dot("Set").Call(
								jen.Id("colCopy"),
								jen.Qual(h.SQLPkg(), "Expr").Call(
									jen.Qual("fmt", "Sprintf").Call(jen.Lit("COALESCE(%s, '[]') || ?"), jen.Id("colCopy")),
									jen.String().Call(jen.Id("appendJSON")),
								),
							),
						),
					),
				),
			)
		})
	}

	// Emit one EdgeSpec block per edge based on its relation type.
	for _, edge := range t.EdgesWithID() {
		if fld := edge.Field(); fld != nil && fld.UserDefined {
			continue
		}
		genUpdateEdge(h, grp, t, edge, entityPkg, fieldPkg, sqlGraphPkg, recv)
	}

	// User modifiers.
	grp.If(jen.Len(jen.Id(recv).Dot("modifiers")).Op(">").Lit(0)).Block(
		jen.Id("spec").Dot("AddModifiers").Call(jen.Id(recv).Dot("modifiers").Op("...")),
	)

	if h.FeatureEnabled(gen.FeatureSchemaConfig.Name) {
		grp.Id("spec").Dot("Node").Dot("Schema").Op("=").Id(recv).Dot("schemaConfig").Dot(t.Name)
	}
}

// jsonAppendRelevant reports whether the type has any JSON fields that could receive Append values.
func jsonAppendRelevant(t *gen.Type) bool {
	for _, fd := range t.Fields {
		if fd.IsJSON() {
			return true
		}
	}
	return false
}

// genUpdateEdge emits EdgeSpec appends to spec.Edges.Add / spec.Edges.Clear for a single edge.
func genUpdateEdge(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, edge *gen.Edge, _, fieldPkg, sqlGraphPkg, recv string) {
	_ = t
	targetType := edge.Type
	targetIDStorage := "id"
	if targetType != nil && targetType.ID != nil {
		targetIDStorage = targetType.ID.StorageKey()
	}
	targetIDCol := jen.Lit(targetIDStorage)
	targetIDTypeConst := jen.Qual(fieldPkg, h.FieldTypeConstant(targetType.ID))

	rel, tableConst, columnsExpr, inverse, bidi := edgeSpecBase(edge, sqlGraphPkg)
	if rel == nil {
		return
	}

	makeEdgeSpec := func() *jen.Statement {
		return jen.Op("&").Qual(sqlGraphPkg, "EdgeSpec").Values(jen.Dict{
			jen.Id("Rel"):     rel,
			jen.Id("Inverse"): jen.Lit(inverse),
			jen.Id("Table"):   tableConst,
			jen.Id("Columns"): columnsExpr,
			jen.Id("Bidi"):    jen.Lit(bidi),
			jen.Id("Target"): jen.Op("&").Qual(sqlGraphPkg, "EdgeTarget").Values(jen.Dict{
				jen.Id("IDSpec"): jen.Op("&").Qual(sqlGraphPkg, "FieldSpec").Values(jen.Dict{
					jen.Id("Column"): targetIDCol,
					jen.Id("Type"):   targetIDTypeConst,
				}),
			}),
		})
	}

	_ = edge.Name
	// Typed edge state methods on the mutation (Ent parity).
	edgeClearedCall := jen.Id(recv).Dot("mutation").Dot(edge.MutationCleared()).Call()
	removedEdgeIDsCall := jen.Id(recv).Dot("mutation").Dot("Removed" + edge.StructField() + "IDs").Call()
	addedEdgeIDsCall := jen.Id(recv).Dot("mutation").Dot(edge.StructField() + "IDs").Call()

	// EdgeCleared → spec.Edges.Clear
	grp.If(edgeClearedCall.Clone()).Block(
		jen.Id("edge").Op(":=").Add(makeEdgeSpec()),
		jen.Id("spec").Dot("Edges").Dot("Clear").Op("=").Append(
			jen.Id("spec").Dot("Edges").Dot("Clear"),
			jen.Id("edge"),
		),
	)

	// RemovedIDs (not cleared) → spec.Edges.Clear (non-unique edges only)
	if !edge.Unique {
		grp.If(
			jen.Id("nodes").Op(":=").Add(removedEdgeIDsCall),
			jen.Len(jen.Id("nodes")).Op(">").Lit(0).Op("&&").Op("!").Add(edgeClearedCall.Clone()),
		).Block(
			jen.Id("edge").Op(":=").Add(makeEdgeSpec()),
			jen.For(jen.List(jen.Id("_"), jen.Id("k")).Op(":=").Range().Id("nodes")).Block(
				jen.Id("edge").Dot("Target").Dot("Nodes").Op("=").Append(jen.Id("edge").Dot("Target").Dot("Nodes"), jen.Id("k")),
			),
			jen.Id("spec").Dot("Edges").Dot("Clear").Op("=").Append(
				jen.Id("spec").Dot("Edges").Dot("Clear"),
				jen.Id("edge"),
			),
		)
	}

	// Added/Set IDs → spec.Edges.Add
	grp.If(
		jen.Id("nodes").Op(":=").Add(addedEdgeIDsCall),
		jen.Len(jen.Id("nodes")).Op(">").Lit(0),
	).Block(
		jen.Id("edge").Op(":=").Add(makeEdgeSpec()),
		jen.For(jen.List(jen.Id("_"), jen.Id("k")).Op(":=").Range().Id("nodes")).Block(
			jen.Id("edge").Dot("Target").Dot("Nodes").Op("=").Append(jen.Id("edge").Dot("Target").Dot("Nodes"), jen.Id("k")),
		),
		jen.Id("spec").Dot("Edges").Dot("Add").Op("=").Append(
			jen.Id("spec").Dot("Edges").Dot("Add"),
			jen.Id("edge"),
		),
	)
}

// genUpdateDefaultsFunc generates a package-level function that contains the shared
// defaults logic for both Update and UpdateOne root wrappers.
func genUpdateDefaultsFunc(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	funcName := lowerFirst(t.Name) + "WrapperUpdateDefaults"
	entityPkg := h.EntityPkgPath(t)
	mutName := t.MutationName()

	f.Commentf("%s applies update default values shared by %s and %s.", funcName, t.UpdateName(), t.UpdateOneName())
	f.Func().Id(funcName).Params(
		jen.Id("m").Op("*").Id(mutName),
		jen.Id("skipDefaults").Bool(),
		jen.Id("skipDefaultFields").Map(jen.String()).Struct(),
	).BlockFunc(func(grp *jen.Group) {
		grp.If(jen.Id("skipDefaults")).Block(jen.Return())
		for _, fd := range t.Fields {
			if !fd.UpdateDefault {
				continue
			}
			col := fd.StorageKey()
			// Skip UpdateDefault if the field was explicitly set OR explicitly cleared.
			// Without the cleared check, ClearXxx() followed by Save() would re-apply
			// the UpdateDefault, undoing the user's explicit clear. Nillable fields
			// expose a typed <Field>Cleared() method; non-nillable fields cannot be
			// cleared to NULL so we only need the "not set" check.
			var condition *jen.Statement
			if fd.Nillable {
				condition = jen.Op("!").Id("ok").Op("&&").Op("!").Id("m").Dot(fd.StructField() + "Cleared").Call()
			} else {
				condition = jen.Op("!").Id("ok")
			}
			grp.If(
				jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("m").Dot(fd.MutationGet()).Call(),
				condition,
			).BlockFunc(func(blk *jen.Group) {
				blk.If(jen.List(jen.Id("_"), jen.Id("skip")).Op(":=").Id("skipDefaultFields").Index(jen.Lit(col)),
					jen.Op("!").Id("skip"),
				).Block(
					jen.Id("v").Op(":=").Qual(entityPkg, "Update"+fd.DefaultName()).Call(),
					jen.Id("m").Dot(fd.MutationSet()).Call(jen.Id("v")),
				)
			})
		}
	})
}

// genUpdateDefaultsMethod generates a thin defaults() method on the given builder
// that delegates to the shared package-level defaults function.
func genUpdateDefaultsMethod(f *jen.File, t *gen.Type, builderName, recv string) {
	funcName := lowerFirst(t.Name) + "WrapperUpdateDefaults"
	f.Commentf("defaults sets the default values for %s before save.", builderName)
	f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("defaults").Params().Error().Block(
		jen.Id(funcName).Call(
			jen.Id(recv).Dot("mutation"),
			jen.Id(recv).Dot("skipDefaults"),
			jen.Id(recv).Dot("skipDefaultFields"),
		),
		jen.Return(jen.Nil()),
	)
}

// genUpdateModify generates the Modify method for update builders.
// Modify allows attaching custom logic to the UPDATE statement via sql.UpdateBuilder modifiers.
func genUpdateModify(h gen.GeneratorHelper, f *jen.File, builderName, recv string, ifaceReturn jen.Code) {
	retType := chainReturnType(builderName, ifaceReturn)
	f.Commentf("Modify adds a statement modifier for attaching custom logic to the UPDATE statement.")
	f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("Modify").Params(
		jen.Id("modifiers").Op("...").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "UpdateBuilder")),
	).Add(retType).Block(
		jen.Id(recv).Dot("modifiers").Op("=").Append(jen.Id(recv).Dot("modifiers"), jen.Id("modifiers").Op("...")),
		jen.Return(jen.Id(recv)),
	)
}

// genUpdateSkipDefaults generates SkipDefaults and SkipDefault methods for root wrappers.
func genUpdateSkipDefaults(f *jen.File, builderName, recv string, ifaceReturn jen.Code) {
	retType := chainReturnType(builderName, ifaceReturn)
	f.Commentf("SkipDefaults skips all update default values on %s.", builderName)
	f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("SkipDefaults").Params().Add(retType).Block(
		jen.Id(recv).Dot("skipDefaults").Op("=").True(),
		jen.Return(jen.Id(recv)),
	)

	f.Commentf("SkipDefault skips a specific field's update default on %s.", builderName)
	f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("SkipDefault").Params(
		jen.Id("fields").Op("...").String(),
	).Add(retType).Block(
		jen.If(jen.Id(recv).Dot("skipDefaultFields").Op("==").Nil()).Block(
			jen.Id(recv).Dot("skipDefaultFields").Op("=").Make(jen.Map(jen.String()).Struct()),
		),
		jen.For(jen.List(jen.Id("_"), jen.Id("f")).Op(":=").Range().Id("fields")).Block(
			jen.Id(recv).Dot("skipDefaultFields").Index(jen.Id("f")).Op("=").Struct().Values(),
		),
		jen.Return(jen.Id(recv)),
	)
}

// hasRequiredUniqueEdge reports whether the type has any required unique edges
// that need validation in update check().
func hasRequiredUniqueEdge(t *gen.Type) bool {
	for _, e := range t.EdgesWithID() {
		if e.Unique && !e.Optional {
			return true
		}
	}
	return false
}

// genUpdateCheck generates a check() method for update builders that validates
// required unique edges are not cleared without being replaced. Matches Ent's behavior.
func genUpdateCheck(f *jen.File, t *gen.Type, builderName, recv string) {
	// Collect required unique edges (O2O/M2O where !Optional).
	var requiredUniqueEdges []*gen.Edge
	for _, e := range t.EdgesWithID() {
		if e.Unique && !e.Optional {
			requiredUniqueEdges = append(requiredUniqueEdges, e)
		}
	}
	if len(requiredUniqueEdges) == 0 {
		return // No required unique edges — no check needed.
	}

	f.Comment("check validates required unique edges are not cleared without replacement.")
	f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("check").Params().Error().BlockFunc(func(grp *jen.Group) {
		for _, e := range requiredUniqueEdges {
			grp.If(
				jen.Id(recv).Dot("mutation").Dot(e.MutationCleared()).Call(),
			).Block(
				jen.Return(jen.Qual("errors", "New").Call(
					jen.Lit("clearing a required unique edge \"" + t.Name + "." + e.Name + "\""),
				)),
			)
		}
		grp.Return(jen.Nil())
	})
}
