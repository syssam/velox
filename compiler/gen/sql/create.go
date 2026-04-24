package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
	schemafield "github.com/syssam/velox/schema/field"
)

// genCreate generates a create builder that holds the entity mutation
// directly (no inner delegation). Field setters call mutation methods.
// Save runs defaults, validation, hooks, and delegates to runtime.CreateNodeCore,
// returning *entity.User directly.
//
// Output: {entity}/create.go in the entity sub-package.
// Returns (*jen.File, error) — the error is reserved for future validation
// although no error is currently produced. The signature is kept consistent
// with other generator entry points (EntityGenerator interface).
func genCreate(h gen.GeneratorHelper, t *gen.Type) (*jen.File, error) { //nolint:unparam // error kept for interface consistency
	f := h.NewFile(h.Pkg())

	createName := t.CreateName() // "UserCreate"
	// Entity package path for return types (entity.User).
	entityReturnPkg := h.SharedEntityPkg()
	mutName := t.MutationName()
	upsertEnabled := h.FeatureEnabled(gen.FeatureUpsert.Name)

	// Concrete return type for chaining methods — no interface indirection.
	var creatorIface jen.Code // nil → chainReturnType falls back to *BuilderName

	recv := "c"

	// --- IDFieldType / FieldTypes package-level vars ---
	genIDFieldTypeAndFieldTypesVars(h, f, t)

	// --- Struct ---
	f.Commentf("%s is the builder for creating a %s entity.", createName, t.Name)
	f.Type().Id(createName).StructFunc(func(group *jen.Group) {
		group.Id("config").Qual(runtimePkg, "Config")
		if h.FeatureEnabled(gen.FeatureSchemaConfig.Name) {
			group.Id("schemaConfig").Qual(h.InternalPkg(), "SchemaConfig")
		}
		group.Id("mutation").Op("*").Id(mutName)
		group.Id("hooks").Index().Qual(runtimePkg, "Hook")
		if t.NumPolicy() > 0 {
			group.Id("policy").Qual(h.VeloxPkg(), "Policy")
		}
		if upsertEnabled {
			group.Id("conflict").Index().Qual(h.SQLPkg(), "ConflictOption")
		}
	})

	// --- Constructor ---
	f.Commentf("New%s creates a new %s builder.", createName, createName)
	f.Func().Id("New" + createName).ParamsFunc(func(pg *jen.Group) {
		pg.Id("c").Qual(runtimePkg, "Config")
		pg.Id("mutation").Op("*").Id(mutName)
		pg.Id("hooks").Index().Qual(runtimePkg, "Hook")
		if t.NumPolicy() > 0 {
			pg.Id("policy").Qual(h.VeloxPkg(), "Policy")
		}
	}).Op("*").Id(createName).BlockFunc(func(grp *jen.Group) {
		d := jen.Dict{
			jen.Id("config"):   jen.Id("c"),
			jen.Id("mutation"): jen.Id("mutation"),
			jen.Id("hooks"):    jen.Id("hooks"),
		}
		if t.NumPolicy() > 0 {
			d[jen.Id("policy")] = jen.Id("policy")
		}
		grp.Return(jen.Op("&").Id(createName).Values(d))
	})

	// --- Field setters ---
	fieldsForSetters := t.Fields
	if !t.HasCompositeID() && t.HasOneFieldID() && t.ID.UserDefined {
		fieldsForSetters = append(fieldsForSetters, t.ID)
	}
	for _, fd := range fieldsForSetters {
		if fd.IsEdgeField() && !fd.UserDefined {
			continue
		}
		genFieldSetter(h, f, createName, recv, fd, false, "mutation", creatorIface)
	}

	// --- Edge setters (ID-based) ---
	for _, edge := range t.EdgesWithID() {
		if fld := edge.Field(); fld != nil && fld.UserDefined {
			continue
		}
		genEdgeSetter(h, f, createName, recv, t, edge, false, "mutation", creatorIface)
	}

	// --- Entity-reference edge setters (typed convenience methods) ---
	for _, edge := range t.EdgesWithID() {
		if fld := edge.Field(); fld != nil && fld.UserDefined {
			continue
		}
		genEdgeEntitySetter(h, f, createName, recv, edge, false, creatorIface)
	}

	// --- Mutation ---
	f.Commentf("Mutation returns the %s.", mutName)
	f.Func().Params(jen.Id(recv).Op("*").Id(createName)).Id("Mutation").Params().Op("*").Id(mutName).Block(
		jen.Return(jen.Id(recv).Dot("mutation")),
	)

	// --- defaults ---
	if t.NeedsDefaults() {
		genCreateDefaults(h, f, t, createName, recv)
	}

	// --- check ---
	genCreateCheck(h, f, t, createName, recv)

	// --- sqlSave (named method extracted from Save closure — Ent pattern) ---
	genCreateSQLSave(h, f, t, createName, recv, entityReturnPkg, upsertEnabled)

	// --- Save ---
	genCreateSave(h, f, t, createName, recv, entityReturnPkg)

	// --- SaveX ---
	f.Commentf("SaveX calls Save and panics if Save returns an error.")
	f.Func().Params(jen.Id(recv).Op("*").Id(createName)).Id("SaveX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Op("*").Qual(entityReturnPkg, t.Name).Block(
		jen.List(jen.Id("v"), jen.Id("err")).Op(":=").Id(recv).Dot("Save").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Panic(jen.Id("err"))),
		jen.Return(jen.Id("v")),
	)

	// --- Exec ---
	f.Comment("Exec executes the query.")
	f.Func().Params(jen.Id(recv).Op("*").Id(createName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id(recv).Dot("Save").Call(jen.Id("ctx")),
		jen.Return(jen.Id("err")),
	)

	// --- ExecX ---
	f.Comment("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id(recv).Op("*").Id(createName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id(recv).Dot("Exec").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)

	// --- CreateBulk ---
	genCreateBulk(h, f, t, createName, entityReturnPkg)

	// --- Upsert ---
	if upsertEnabled {
		genCreateUpsert(h, f, t, createName, recv, entityReturnPkg)
	}

	return f, nil
}

// genCreateDefaults generates the defaults method for the root Create builder.
// Ported from create.go genCreateDefaults, adapted to use root wrapper fields.
func genCreateDefaults(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName, recv string) {
	fields := t.Fields
	if t.HasOneFieldID() && t.ID.UserDefined {
		fields = append(fields, t.ID)
	}

	entityPkg := h.LeafPkgPath(t)
	pkg := t.PackageDir()

	autoDefault := h.FeatureEnabled(gen.FeatureAutoDefault.Name)
	fieldNeedsDefault := func(fd *gen.Field) bool {
		if fd.Default {
			return true
		}
		if autoDefault && fd.Optional && !fd.Nillable && fd.Type != nil && (fd.Type.Type.IsStandardType() || fd.Type.Type == schemafield.TypeOther) {
			return true
		}
		return false
	}

	genFieldDefault := func(grp *jen.Group, fd *gen.Field) {
		grp.If(
			jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id(recv).Dot("mutation").Dot(fd.MutationGet()).Call(),
			jen.Op("!").Id("ok"),
		).BlockFunc(func(blk *jen.Group) {
			if fd.Default {
				if fd.DefaultFunc() {
					blk.If(jen.Qual(entityPkg, fd.DefaultName()).Op("==").Nil()).Block(
						jen.Return(jen.Qual("fmt", "Errorf").Call(
							jen.Lit(pkg + ": uninitialized " + t.Package() + "." + fd.DefaultName() + " (forgotten import " + pkg + "?)"),
						)),
					)
					blk.Id("v").Op(":=").Qual(entityPkg, fd.DefaultName()).Call()
				} else if fd.Nillable {
					blk.Id("v").Op(":=").Op("*").Qual(entityPkg, fd.DefaultName())
				} else {
					blk.Id("v").Op(":=").Qual(entityPkg, fd.DefaultName())
				}
			} else {
				blk.Id("v").Op(":=").Add(baseZeroValue(h, fd))
			}
			blk.Id(recv).Dot("mutation").Dot(fd.MutationSet()).Call(jen.Id("v"))
		})
	}

	f.Comment("defaults sets the default values of the builder before save.")
	f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("defaults").Params().Error().BlockFunc(func(grp *jen.Group) {
		for _, fd := range fields {
			if fieldNeedsDefault(fd) {
				genFieldDefault(grp, fd)
			}
		}
		grp.Return(jen.Nil())
	})
}

// genCreateCheck generates the check method for the root Create builder.
// Ported from create.go genCreateCheck, adapted for root package (uses local ValidationError).
func genCreateCheck(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName, recv string) {
	fields := t.Fields
	if t.HasOneFieldID() && t.ID.UserDefined {
		fields = append(fields, t.ID)
	}

	entityPkg := h.LeafPkgPath(t)
	validatorsEnabled, _ := h.Graph().FeatureEnabled(gen.FeatureValidator.Name)

	// Uses runtime.ValidationError with uppercase Err field.
	validationErr := func(name jen.Code, errVal jen.Code) jen.Code {
		return jen.Op("&").Qual(runtimePkg, "ValidationError").Values(jen.Dict{
			jen.Id("Name"):   name,
			jen.Id("Err"):    errVal,
			jen.Id("Entity"): jen.Lit(t.Name),
			jen.Id("Field"):  name,
		})
	}

	f.Comment("check runs all checks and user-defined validators on the builder.")
	f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("check").Params().Error().BlockFunc(func(grp *jen.Group) {
		for _, fd := range fields {
			if t.HasOneFieldID() && fd.Name == t.ID.Name {
				continue
			}
			// Required field check
			if !fd.Optional && !fd.Nillable {
				grp.If(
					jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id(recv).Dot("mutation").Dot(fd.MutationGet()).Call(),
					jen.Op("!").Id("ok"),
				).Block(
					jen.Return(validationErr(
						jen.Lit(fd.Name),
						jen.Qual("errors", "New").Call(jen.Lit("missing required field \""+t.Name+"."+fd.Name+"\"")),
					)),
				)
			}
			// Validator check
			isValidator := fd.HasGoType() && fd.Type != nil && fd.Type.Validator()
			if (validatorsEnabled && (fd.Validators > 0 || fd.IsEnum())) || isValidator {
				grp.If(
					jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Id(recv).Dot("mutation").Dot(fd.MutationGet()).Call(),
					jen.Id("ok"),
				).BlockFunc(func(blk *jen.Group) {
					var validationCall *jen.Statement
					if validatorsEnabled && (fd.Validators > 0 || fd.IsEnum()) {
						validationCall = jen.Qual(entityPkg, fd.Validator()).Call(jen.Id("v"))
					} else {
						validationCall = jen.Id("v").Dot("Validate").Call()
					}
					blk.If(jen.Id("err").Op(":=").Add(validationCall), jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(validationErr(
							jen.Lit(fd.Name),
							jen.Qual("fmt", "Errorf").Call(jen.Lit("validator failed for field \""+t.Name+"."+fd.Name+"\": %w"), jen.Id("err")),
						)),
					)
				})
			}
		}
		// Required edge checks.
		// Build set of required field keys to skip field-backed edges.
		checkedFieldKeys := make(map[string]bool)
		for _, fd := range fields {
			if !fd.Optional && !fd.Nillable {
				checkedFieldKeys[fd.Name] = true
				checkedFieldKeys[fd.StorageKey()] = true
			}
		}
		for _, edge := range t.EdgesWithID() {
			if edge.Optional {
				continue
			}
			// Skip edges whose FK field is already validated by field checks above.
			if df := edge.DefinedField(); df != "" && checkedFieldKeys[df] {
				continue
			}
			grp.If(jen.Len(jen.Id(recv).Dot("mutation").Dot(edge.StructField() + "IDs").Call()).Op("==").Lit(0)).Block(
				jen.Return(validationErr(
					jen.Lit(edge.Name),
					jen.Qual("errors", "New").Call(jen.Lit("missing required edge \""+t.Name+"."+edge.Name+"\"")),
				)),
			)
		}
		grp.Return(jen.Nil())
	})
}

// genCreateSQLSave generates sqlSave + createSpec for the Create builder.
// sqlSave calls sqlgraph.CreateNode directly (no runtime middleman) and
// assigns the auto-generated ID back on the node. createSpec builds both the
// entity node AND the CreateSpec from typed mutation fields, mirroring Ent's
// proven two-output pattern. Edges (O2M/M2O/M2M) are added via spec.Edges so
// sqlgraph handles junction inserts and FK column materialization.
func genCreateSQLSave(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName, recv, entityReturnPkg string, upsertEnabled bool) {
	f.Commentf("sqlSave executes the SQL create for %s after hooks have run.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("sqlSave").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Qual(entityReturnPkg, t.Name), jen.Error()).BlockFunc(func(grp *jen.Group) {
		// Call check() to validate required fields and run validators (after hooks).
		grp.If(jen.Id("err").Op(":=").Id(recv).Dot("check").Call(), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		)
		grp.List(jen.Id("_node"), jen.Id("_spec")).Op(":=").Id(recv).Dot("createSpec").Call()
		if upsertEnabled {
			grp.If(jen.Len(jen.Id(recv).Dot("conflict")).Op(">").Lit(0)).Block(
				jen.Id("_spec").Dot("OnConflict").Op("=").Id(recv).Dot("conflict"),
			)
		}
		grp.If(
			jen.Id("err").Op(":=").Qual(h.SQLGraphPkg(), "CreateNode").Call(
				jen.Id("ctx"), jen.Id(recv).Dot("config").Dot("Driver"), jen.Id("_spec"),
			),
			jen.Id("err").Op("!=").Nil(),
		).Block(
			jen.Return(jen.Nil(), jen.Qual(runtimePkg, "MayWrapConstraintError").Call(jen.Id("err"))),
		)
		// Propagate the builder's config onto the returned node. Without this,
		// cross-package entities (e.g. a `task` package returning *entity.Task)
		// come back with a zero-value runtime.Config, and any gqlgen field
		// resolver that walks an edge (Category, AssignedTasker, Milestones...)
		// panics with a nil QueryContext. The in-package struct literal
		// `{config: recv.config}` isn't possible for cross-package outputs
		// because `config` is unexported, so we call the exported SetConfig.
		grp.Id("_node").Dot(t.SetConfigMethodName()).Call(jen.Id(recv).Dot("config"))
		// Assign auto-generated ID if the type has a single numeric auto-ID.
		genCreateAssignID(grp, t, "_node", "_spec")
		// Record the ID on mutation state (for hooks and follow-up calls).
		if t.HasOneFieldID() {
			grp.Id(recv).Dot("mutation").Dot("SetID").Call(
				jen.Id("_node").Dot(t.ID.StructField()),
			)
		}
		grp.Return(jen.Id("_node"), jen.Nil())
	})

	// createSpec is a separate method returning (*Entity, *sqlgraph.CreateSpec).
	genCreateSpecMethod(h, f, t, builderName, recv, entityReturnPkg)
}

// genCreateAssignID emits the code that assigns the auto-generated ID from
// spec.ID.Value onto the returned node. Only applies to numeric ID types where
// the user did not explicitly supply an ID.
func genCreateAssignID(grp *jen.Group, t *gen.Type, nodeVar, specVar string) {
	if !t.HasOneFieldID() {
		return
	}
	id := t.ID
	// Non-numeric IDs (string, UUID, bytes, other) must be user-supplied and
	// are already set in createSpec. Skip the auto-ID extraction.
	if !id.Type.Type.Numeric() {
		return
	}
	// Numeric ID: if the user did not supply one, sqlgraph sets _spec.ID.Value
	// to an int64 after insert. Cast to the field's declared Go type.
	cond := jen.Id(specVar).Dot("ID").Dot("Value").Op("!=").Nil()
	if id.UserDefined {
		cond = jen.Id(specVar).Dot("ID").Dot("Value").Op("!=").Nil().Op("&&").
			Id(nodeVar).Dot(id.StructField()).Op("==").Lit(0)
	}
	grp.If(cond).Block(
		jen.Id("id").Op(":=").Id(specVar).Dot("ID").Dot("Value").Assert(jen.Int64()),
		jen.Id(nodeVar).Dot(id.StructField()).Op("=").Id(id.Type.Type.String()).Call(jen.Id("id")),
	)
}

// genCreateSpecMethod emits the createSpec method that builds both the entity
// node and the sqlgraph.CreateSpec from typed mutation fields.
func genCreateSpecMethod(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName, recv, entityReturnPkg string) {
	entityPkg := h.LeafPkgPath(t)
	fieldPkg := h.FieldPkg()
	sqlGraphPkg := h.SQLGraphPkg()

	f.Commentf("createSpec builds the %s node and sqlgraph.CreateSpec from mutation fields.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("createSpec").Params().Params(
		jen.Op("*").Qual(entityReturnPkg, t.Name),
		jen.Op("*").Qual(sqlGraphPkg, "CreateSpec"),
	).BlockFunc(func(grp *jen.Group) {
		grp.Var().Defs(
			jen.Id("_node").Op("=").Op("&").Qual(entityReturnPkg, t.Name).Values(),
			jen.Id("_spec").Op("=").Qual(sqlGraphPkg, "NewCreateSpec").Call(
				jen.Qual(entityPkg, "Table"),
				jen.Op("&").Qual(sqlGraphPkg, "FieldSpec").Values(jen.Dict{
					jen.Id("Column"): jen.Qual(entityPkg, "FieldID"),
					jen.Id("Type"):   jen.Id(idFieldTypeVar(t)),
				}),
			),
		)
		// User-defined ID: read the typed id pointer from mutation and set
		// both node and spec. Direct field access preserves the typed IDType
		// (e.g. uuid.UUID) — going through mutation.ID() would lose type info
		// since that method returns (any, bool) to satisfy the generic contract.
		if t.HasOneFieldID() && t.ID.UserDefined {
			grp.If(
				jen.Id(recv).Dot("mutation").Dot("id").Op("!=").Nil(),
			).Block(
				jen.Id("_node").Dot(t.ID.StructField()).Op("=").Op("*").Id(recv).Dot("mutation").Dot("id"),
				jen.Id("_spec").Dot("ID").Dot("Value").Op("=").Op("*").Id(recv).Dot("mutation").Dot("id"),
			)
		}
		// Fields: set on both spec and node.
		for _, fd := range t.Fields {
			if fd.IsEdgeField() && !fd.UserDefined {
				continue
			}
			typedField := "_" + fd.Name
			grp.If(jen.Id(recv).Dot("mutation").Dot(typedField).Op("!=").Nil()).BlockFunc(func(blk *jen.Group) {
				blk.Id("v").Op(":=").Op("*").Id(recv).Dot("mutation").Dot(typedField)
				blk.Id("_spec").Dot("SetField").Call(
					jen.Lit(fd.StorageKey()),
					jen.Qual(fieldPkg, h.FieldTypeConstant(fd)),
					jen.Id("v"),
				)
				if fd.NillableValue() {
					blk.Id("_node").Dot(fd.StructField()).Op("=").Op("&").Id("v")
				} else {
					blk.Id("_node").Dot(fd.StructField()).Op("=").Id("v")
				}
			})
		}
		// Edges: emit EdgeSpec entries into _spec.Edges.
		for _, edge := range t.EdgesWithID() {
			if fld := edge.Field(); fld != nil && fld.UserDefined {
				continue
			}
			genCreateEdge(h, grp, t, edge, entityPkg, fieldPkg, sqlGraphPkg, "_node", "_spec")
		}
		grp.Return(jen.Id("_node"), jen.Id("_spec"))
	})
}

// genCreateEdge emits the code that appends a single EdgeSpec to _spec.Edges
// for an edge on a create operation. Mirrors Ent's dialect/sql/defedge.tmpl.
// All edge metadata (table, columns, target ID column) is emitted as literals
// so an entity sub-package does not need to import the target sub-package
// (avoids cross-entity import cycles).
func genCreateEdge(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, edge *gen.Edge, _, fieldPkg, sqlGraphPkg, nodeVar, specVar string) {
	_ = t
	idsMethod := edge.StructField() + "IDs"
	targetType := edge.Type
	targetIDStorage := "id"
	if targetType != nil && targetType.ID != nil {
		targetIDStorage = targetType.ID.StorageKey()
	}
	targetIDCol := jen.Lit(targetIDStorage)
	targetIDTypeConst := jen.Qual(fieldPkg, h.FieldTypeConstant(targetType.ID))

	rel, tableLit, columnsExpr, inverse, bidi := edgeSpecBase(edge, sqlGraphPkg)
	if rel == nil {
		return
	}

	dict := jen.Dict{
		jen.Id("Rel"):     rel,
		jen.Id("Inverse"): jen.Lit(inverse),
		jen.Id("Table"):   tableLit,
		jen.Id("Columns"): columnsExpr,
		jen.Id("Bidi"):    jen.Lit(bidi),
		jen.Id("Target"): jen.Op("&").Qual(sqlGraphPkg, "EdgeTarget").Values(jen.Dict{
			jen.Id("IDSpec"): jen.Op("&").Qual(sqlGraphPkg, "FieldSpec").Values(jen.Dict{
				jen.Id("Column"): targetIDCol,
				jen.Id("Type"):   targetIDTypeConst,
			}),
		}),
	}

	grp.If(
		jen.Id("nodes").Op(":=").Id("c").Dot("mutation").Dot(idsMethod).Call(),
		jen.Len(jen.Id("nodes")).Op(">").Lit(0),
	).BlockFunc(func(blk *jen.Group) {
		blk.Id("edge").Op(":=").Op("&").Qual(sqlGraphPkg, "EdgeSpec").Values(dict)
		blk.For(jen.List(jen.Id("_"), jen.Id("k")).Op(":=").Range().Id("nodes")).Block(
			jen.Id("edge").Dot("Target").Dot("Nodes").Op("=").Append(
				jen.Id("edge").Dot("Target").Dot("Nodes"),
				jen.Id("k"),
			),
		)
		// Note: for M2O/O2O-inverse (OwnFK), sqlgraph materializes the FK column
		// on the insert statement. The returned _node will not have that FK field
		// populated, but the ID is in mutation state and the user's Save caller
		// can re-query if needed. This matches Velox's behavior before this refactor.
		_ = nodeVar
		blk.Id(specVar).Dot("Edges").Op("=").Append(
			jen.Id(specVar).Dot("Edges"),
			jen.Id("edge"),
		)
	})
}

// genCreateSave generates the Save method for the root Create builder.
// Returns *entity.User directly (no wrapping). Delegates to sqlSave via WithHooks.
func genCreateSave(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName, recv, entityReturnPkg string) {
	f.Commentf("Save creates the %s in the database.", t.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("Save").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Qual(entityReturnPkg, t.Name), jen.Error()).BlockFunc(func(grp *jen.Group) {
		// Call defaults() if type has defaults
		if t.NeedsDefaults() {
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
			grp.Id("hooks").Op(":=").Id("append").Call(jen.Id(recv).Dot("hooks"), jen.Qual(h.LeafPkgPath(t), "Hooks").Index(jen.Op(":")).Op("..."))
		} else {
			grp.Id("hooks").Op(":=").Id(recv).Dot("hooks")
		}
		mutationType := jen.Id(t.MutationName())
		grp.Return(jen.Qual(h.VeloxPkg(), "WithHooks").Types(
			jen.Op("*").Qual(entityReturnPkg, t.Name),
			mutationType,
			jen.Op("*").Add(mutationType),
		).Call(
			jen.Id("ctx"), jen.Id(recv).Dot("sqlSave"), jen.Id(recv).Dot("mutation"), jen.Id("hooks"),
		))
	})
}

// genCreateBulk generates the CreateBulk struct and its methods in the root package.
// Returns []*entity.User directly (no wrapping).
func genCreateBulk(h gen.GeneratorHelper, f *jen.File, t *gen.Type, createName, entityReturnPkg string) {
	bulkName := t.CreateBulkName()
	upsertEnabled := h.FeatureEnabled(gen.FeatureUpsert.Name)
	bulkCreatorIface := jen.Op("*").Id(bulkName)

	f.Commentf("%s is the builder for creating many %s entities in bulk.", bulkName, t.Name)
	f.Type().Id(bulkName).StructFunc(func(grp *jen.Group) {
		grp.Id("config").Qual(runtimePkg, "Config")
		grp.Id("err").Error()
		grp.Id("builders").Index().Op("*").Id(createName)
		grp.Id("batchSize").Int()
		if upsertEnabled {
			grp.Id("conflict").Index().Qual(h.SQLPkg(), "ConflictOption")
		}
	})

	// Constructor for CreateBulk
	f.Commentf("New%s creates a new %s builder.", bulkName, bulkName)
	f.Func().Id("New"+bulkName).Params(
		jen.Id("c").Qual(runtimePkg, "Config"),
		jen.Id("builders").Index().Op("*").Id(createName),
	).Op("*").Id(bulkName).Block(
		jen.Return(jen.Op("&").Id(bulkName).Values(jen.Dict{
			jen.Id("config"):   jen.Id("c"),
			jen.Id("builders"): jen.Id("builders"),
		})),
	)

	// BatchSize configures the maximum number of rows per generated
	// INSERT statement. The default (0) means no chunking: the entire
	// bulk goes through a single BatchCreate call. When set to a
	// positive value N, Save slices the builders into chunks of at
	// most N and runs one mutator chain + BatchCreate per chunk.
	//
	// The primary reason to set this is to stay under SQLite's
	// "too many SQL variables" cap (~32766 parameters/stmt). For an
	// entity with K insertable columns, a safe batch size is ~32000/K.
	// This parameter is the velox analog of Django's
	// Model.objects.bulk_create(batch_size=N) and GORM's
	// db.CreateInBatches(slice, N).
	//
	// Caveat: when BatchSize triggers more than one chunk, atomicity
	// across chunks is NOT automatic — a mid-loop error leaves the
	// already-committed chunks in the database. Wrap the call in
	// client.Tx(ctx) to get all-or-nothing semantics; the
	// mutator-chain path transparently inherits the caller's tx.
	f.Commentf("BatchSize sets the chunk size for the bulk insert.")
	f.Comment("A value of 0 (default) means no chunking — all builders")
	f.Comment("go through a single BatchCreate. When set, Save splits the")
	f.Comment("builders into chunks of at most n and runs one INSERT per")
	f.Comment("chunk. Use this to stay under SQLite's SQL-variable cap.")
	f.Comment("")
	f.Comment("Atomicity across chunks is NOT automatic. Wrap the call")
	f.Comment("in client.Tx(ctx) if you need all-or-nothing semantics.")
	f.Func().Params(jen.Id("_cb").Op("*").Id(bulkName)).Id("BatchSize").Params(
		jen.Id("n").Int(),
	).Add(bulkCreatorIface.Clone()).Block(
		jen.Id("_cb").Dot("batchSize").Op("=").Id("n"),
		jen.Return(jen.Id("_cb")),
	)

	if upsertEnabled {
		// OnConflict on bulk: variadic setter that returns the bulk
		// builder for chaining. Unlike single-create which transitions
		// to a separate XxxUpsert builder, bulk takes the raw
		// sql.ConflictOption values (including resolver shortcuts like
		// sql.ResolveWithNewValues()). The conflict opts flow into
		// BatchCreateSpec.OnConflict at the bottom of the mutator chain.
		f.Commentf("OnConflict configures the ON CONFLICT clause for the bulk insert.")
		f.Commentf("For example:")
		f.Comment("")
		f.Commentf("\tclient.%s.CreateBulk(builders...).", t.Name)
		f.Comment("\t\tOnConflict(sql.ConflictColumns(\"email\"), sql.ResolveWithNewValues()).")
		f.Comment("\t\tSave(ctx)")
		f.Comment("")
		f.Comment("Note: when using sql.DoNothing(), the returned slice's ID")
		f.Comment("fields are not reliable for rows that were skipped due to")
		f.Comment("conflicts. The DB state is correct (duplicate rows are left")
		f.Comment("untouched, new rows are inserted), but the RETURNING clause")
		f.Comment("produces fewer rows than inputs under DO NOTHING, which")
		f.Comment("means positional ID assignment cannot distinguish skipped")
		f.Comment("rows from new ones. If you need every returned row to have")
		f.Comment("a correct ID, use sql.ResolveWithIgnore() instead: it emits")
		f.Comment("DO UPDATE SET col=col so RETURNING produces one row per")
		f.Comment("input while preserving DO-NOTHING semantics at the DB level.")
		f.Func().Params(jen.Id("_cb").Op("*").Id(bulkName)).Id("OnConflict").Params(
			jen.Id("opts").Op("...").Qual(h.SQLPkg(), "ConflictOption"),
		).Add(bulkCreatorIface.Clone()).Block(
			jen.Id("_cb").Dot("conflict").Op("=").Append(jen.Id("_cb").Dot("conflict"), jen.Id("opts").Op("...")),
			jen.Return(jen.Id("_cb")),
		)
	}

	// Save — Ent-style mutator chain.
	//
	// Every call goes through the same shape: build a chain of
	// per-row mutators where each row's hook chain delegates to the
	// next row's mutator, and the innermost mutator of the LAST row
	// calls a single sqlgraph.BatchCreate for all rows at once. This
	// gives three properties at once:
	//
	//   - Hooks run per row (correct mutation semantics).
	//   - The SQL is a single INSERT ... VALUES (...), (...), ...
	//     statement — batching is preserved even with hooks, which
	//     avoids the N-round-trip performance cliff the old "detect
	//     hooks and fall back to per-row" design had.
	//   - Atomicity is free: one SQL statement either inserts every
	//     row or none, no tx wrapping required.
	//
	// The chain is built as a forward reference — mutators[i+1] is
	// looked up at call time, not construction time, so the closure
	// over the growing slice works.
	//
	// Note on ON CONFLICT DO NOTHING: this generator matches Ent and
	// goes through the same single BatchCreate path. On a mixed
	// duplicate/new bulk with DO NOTHING, the database returns fewer
	// RETURNING rows than inputs, and the positional scan in
	// sqlgraph.batchCreator.insertLastIDs leaves some returned Go
	// objects with unreliable IDs. Callers that need reliable IDs
	// for every row should use sql.ResolveWithIgnore() instead,
	// which produces a RETURNING row for every input. See the
	// OnConflict method docstring for the full explanation.
	f.Commentf("Save creates the %s entities in the database.", t.Name)
	f.Comment("")
	f.Comment("When BatchSize is 0 (default), all builders go through a")
	f.Comment("single mutator chain ending in one BatchCreate. When")
	f.Comment("BatchSize > 0, the builders are sliced into chunks of at")
	f.Comment("most N and each chunk runs its own mutator chain + one")
	f.Comment("INSERT. A mid-loop error returns the rows persisted so far")
	f.Comment("together with the error — wrap in client.Tx(ctx) for")
	f.Comment("all-or-nothing semantics.")
	f.Func().Params(jen.Id("_cb").Op("*").Id(bulkName)).Id("Save").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Op("*").Qual(entityReturnPkg, t.Name), jen.Error()).BlockFunc(func(grp *jen.Group) {
		grp.If(jen.Id("_cb").Dot("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("_cb").Dot("err")),
		)
		// Fast path: no chunking requested (or fewer builders than the
		// chunk size). Hand the whole slice to saveChunk in one shot.
		grp.If(
			jen.Id("_cb").Dot("batchSize").Op("<=").Lit(0).
				Op("||").Id("_cb").Dot("batchSize").Op(">=").Len(jen.Id("_cb").Dot("builders")),
		).Block(
			jen.Return(jen.Id("_cb").Dot("saveChunk").Call(jen.Id("ctx"), jen.Id("_cb").Dot("builders"))),
		)
		// Chunked path: loop over consecutive slices of at most
		// batchSize builders. Aggregate returned nodes; surface the
		// first error immediately (rows from earlier successful chunks
		// remain in the database unless the caller wrapped in a tx).
		grp.Id("nodes").Op(":=").Make(
			jen.Index().Op("*").Qual(entityReturnPkg, t.Name),
			jen.Lit(0),
			jen.Len(jen.Id("_cb").Dot("builders")),
		)
		grp.For(
			jen.Id("start").Op(":=").Lit(0),
			jen.Id("start").Op("<").Len(jen.Id("_cb").Dot("builders")),
			jen.Id("start").Op("+=").Id("_cb").Dot("batchSize"),
		).BlockFunc(func(loop *jen.Group) {
			loop.Id("end").Op(":=").Id("start").Op("+").Id("_cb").Dot("batchSize")
			loop.If(jen.Id("end").Op(">").Len(jen.Id("_cb").Dot("builders"))).Block(
				jen.Id("end").Op("=").Len(jen.Id("_cb").Dot("builders")),
			)
			loop.List(jen.Id("chunk"), jen.Id("err")).Op(":=").
				Id("_cb").Dot("saveChunk").Call(
				jen.Id("ctx"),
				jen.Id("_cb").Dot("builders").Index(jen.Id("start"), jen.Id("end")),
			)
			loop.Id("nodes").Op("=").Append(jen.Id("nodes"), jen.Id("chunk").Op("..."))
			loop.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Id("nodes"), jen.Id("err")),
			)
		})
		grp.Return(jen.Id("nodes"), jen.Nil())
	})

	// saveChunk — Ent-style mutator chain run on a sub-slice of the
	// builders. The chunking wrapper in Save calls this either once
	// (no BatchSize / fits in one chunk) or multiple times (chunked).
	//
	// Each call builds a chain of per-row mutators over the `builders`
	// argument where each row's hook chain delegates to the next row's
	// mutator, and the innermost mutator of the LAST row calls a
	// single sqlgraph.BatchCreate for every spec in the chunk. Hooks
	// run per row AND batching is preserved — matches Ent's design.
	//
	// Note on ON CONFLICT DO NOTHING: on a mixed duplicate/new input
	// the database returns fewer RETURNING rows than inputs, and the
	// positional scan in sqlgraph.batchCreator.insertLastIDs leaves
	// some returned Go objects with unreliable IDs. Callers that need
	// reliable IDs for every row should use sql.ResolveWithIgnore()
	// instead, which produces a RETURNING row for every input. See
	// the OnConflict method docstring for the full explanation.
	f.Commentf("saveChunk runs one mutator chain over a sub-slice of the builders.")
	f.Func().Params(jen.Id("_cb").Op("*").Id(bulkName)).Id("saveChunk").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("builders").Index().Op("*").Id(createName),
	).Params(jen.Index().Op("*").Qual(entityReturnPkg, t.Name), jen.Error()).BlockFunc(func(grp *jen.Group) {
		grp.If(jen.Len(jen.Id("builders")).Op("==").Lit(0)).Block(
			jen.Return(jen.Index().Op("*").Qual(entityReturnPkg, t.Name).Values(), jen.Nil()),
		)
		// Explicit per-row privacy check — runs before hooks since privacy no longer rides on Hooks[0].
		if t.NumPolicy() > 0 {
			grp.For(jen.List(jen.Id("_"), jen.Id("b")).Op(":=").Range().Id("builders")).Block(
				jen.If(jen.Id("b").Dot("policy").Op("!=").Nil()).Block(
					jen.If(jen.Id("err").Op(":=").Id("b").Dot("policy").Dot("EvalMutation").Call(
						jen.Id("ctx"), jen.Id("b").Dot("mutation"),
					), jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Id("err")),
					),
				),
			)
		}
		grp.Id("specs").Op(":=").Make(
			jen.Index().Op("*").Qual(h.SQLGraphPkg(), "CreateSpec"),
			jen.Len(jen.Id("builders")),
		)
		grp.Id("nodes").Op(":=").Make(jen.Index().Op("*").Qual(entityReturnPkg, t.Name), jen.Len(jen.Id("builders")))
		grp.Id("mutators").Op(":=").Make(jen.Index().Qual(runtimePkg, "Mutator"), jen.Len(jen.Id("builders")))

		// Track a chunk-local defaults error so a failing defaults()
		// call in the IIFE stops the loop without panicking on a nil
		// mutator when we kick the chain.
		if t.NeedsDefaults() {
			grp.Var().Id("defaultsErr").Error()
		}

		grp.For(jen.Id("i").Op(":=").Range().Id("builders")).BlockFunc(func(outer *jen.Group) {
			outer.Func().Params(
				jen.Id("i").Int(),
				jen.Id("root").Qual("context", "Context"),
			).BlockFunc(func(iife *jen.Group) {
				iife.Id("builder").Op(":=").Id("builders").Index(jen.Id("i"))
				if t.NeedsDefaults() {
					iife.If(jen.Id("err").Op(":=").Id("builder").Dot("defaults").Call(), jen.Id("err").Op("!=").Nil()).Block(
						jen.Id("defaultsErr").Op("=").Id("err"),
						jen.Return(),
					)
				}
				iife.Var().Id("mut").Qual(runtimePkg, "Mutator").Op("=").Qual(runtimePkg, "MutateFunc").Call(
					jen.Func().Params(
						jen.Id("ctx").Qual("context", "Context"),
						jen.Id("m").Qual(runtimePkg, "Mutation"),
					).Params(jen.Qual(runtimePkg, "Value"), jen.Error()).BlockFunc(func(inner *jen.Group) {
						inner.List(jen.Id("mutation"), jen.Id("ok")).Op(":=").Id("m").Assert(jen.Op("*").Id(t.MutationName()))
						inner.If(jen.Op("!").Id("ok")).Block(
							jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("velox: unexpected mutation type %T"), jen.Id("m"))),
						)
						inner.If(jen.Id("err").Op(":=").Id("builder").Dot("check").Call(), jen.Id("err").Op("!=").Nil()).Block(
							jen.Return(jen.Nil(), jen.Id("err")),
						)
						inner.Id("builder").Dot("mutation").Op("=").Id("mutation")
						inner.Var().Id("err").Error()
						inner.List(
							jen.Id("nodes").Index(jen.Id("i")),
							jen.Id("specs").Index(jen.Id("i")),
						).Op("=").Id("builder").Dot("createSpec").Call()
						// Propagate bulk builder config onto each returned node.
						// Without this, cross-package entities come back with a
						// zero-value runtime.Config and any gqlgen edge resolver
						// panics on nil QueryContext. Mirrors the single-row
						// Create.sqlSave path.
						inner.Id("nodes").Index(jen.Id("i")).Dot(t.SetConfigMethodName()).Call(jen.Id("_cb").Dot("config"))
						inner.If(jen.Id("i").Op("<").Len(jen.Id("mutators")).Op("-").Lit(1)).Block(
							jen.List(jen.Id("_"), jen.Id("err")).Op("=").
								Id("mutators").Index(jen.Id("i").Op("+").Lit(1)).Dot("Mutate").
								Call(
									jen.Id("root"),
									jen.Id("builders").Index(jen.Id("i").Op("+").Lit(1)).Dot("mutation"),
								),
						).Else().BlockFunc(func(leaf *jen.Group) {
							batchDict := jen.Dict{
								jen.Id("Nodes"): jen.Id("specs"),
							}
							if upsertEnabled {
								batchDict[jen.Id("OnConflict")] = jen.Id("_cb").Dot("conflict")
							}
							leaf.Id("spec").Op(":=").Op("&").Qual(h.SQLGraphPkg(), "BatchCreateSpec").Values(batchDict)
							leaf.If(
								jen.Id("err").Op("=").Qual(h.SQLGraphPkg(), "BatchCreate").Call(
									jen.Id("ctx"),
									jen.Id("_cb").Dot("config").Dot("Driver"),
									jen.Id("spec"),
								),
								jen.Id("err").Op("!=").Nil(),
							).Block(
								jen.Id("err").Op("=").Qual(runtimePkg, "MayWrapConstraintError").Call(jen.Id("err")),
							)
						})
						inner.If(jen.Id("err").Op("!=").Nil()).Block(
							jen.Return(jen.Nil(), jen.Id("err")),
						)
						if t.HasOneFieldID() && t.ID.Type.Type.Numeric() {
							cond := jen.Id("specs").Index(jen.Id("i")).Dot("ID").Dot("Value").Op("!=").Nil()
							if t.ID.UserDefined {
								cond = cond.Clone().Op("&&").
									Id("nodes").Index(jen.Id("i")).Dot(t.ID.StructField()).Op("==").Lit(0)
							}
							inner.If(cond).Block(
								jen.Id("id").Op(":=").Id("specs").Index(jen.Id("i")).Dot("ID").Dot("Value").Assert(jen.Int64()),
								jen.Id("nodes").Index(jen.Id("i")).Dot(t.ID.StructField()).Op("=").Id(t.ID.Type.Type.String()).Call(jen.Id("id")),
								jen.Id("mutation").Dot("SetID").Call(jen.Id(t.ID.Type.Type.String()).Call(jen.Id("id"))),
							)
						} else if t.HasOneFieldID() {
							inner.Id("mutation").Dot("SetID").Call(
								jen.Id("nodes").Index(jen.Id("i")).Dot(t.ID.StructField()),
							)
						}
						inner.Return(jen.Id("nodes").Index(jen.Id("i")), jen.Nil())
					}),
				)
				// Combine the builder's runtime hooks (from c.Use) with
				// the package-level Hooks slice. Single-row Save does
				// the same `append(c.hooks, Hooks[:]...)`. Gated on
				// NumHooks because the Hooks package-level var only
				// exists when it is non-zero; otherwise there is no
				// Hooks identifier to reference. Privacy is evaluated
				// separately in saveChunk before the mutator chain.
				if t.NumHooks() > 0 {
					// Hooks is a package-level var declared in the {entity}/ leaf
					// package (by genPackageRuntimeVars). After cycle-break, this
					// bulk builder lives in client/{entity}/, so qualify the ref.
					iife.Id("allHooks").Op(":=").Append(
						jen.Id("builder").Dot("hooks"),
						jen.Qual(h.LeafPkgPath(t), "Hooks").Index(jen.Op(":")).Op("..."),
					)
					iife.For(
						jen.Id("j").Op(":=").Len(jen.Id("allHooks")).Op("-").Lit(1),
						jen.Id("j").Op(">=").Lit(0),
						jen.Id("j").Op("--"),
					).Block(
						jen.Id("mut").Op("=").Id("allHooks").Index(jen.Id("j")).Call(jen.Id("mut")),
					)
				} else {
					iife.For(
						jen.Id("j").Op(":=").Len(jen.Id("builder").Dot("hooks")).Op("-").Lit(1),
						jen.Id("j").Op(">=").Lit(0),
						jen.Id("j").Op("--"),
					).Block(
						jen.Id("mut").Op("=").Id("builder").Dot("hooks").Index(jen.Id("j")).Call(jen.Id("mut")),
					)
				}
				iife.Id("mutators").Index(jen.Id("i")).Op("=").Id("mut")
			}).Call(jen.Id("i"), jen.Id("ctx"))
		})
		if t.NeedsDefaults() {
			grp.If(jen.Id("defaultsErr").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("defaultsErr")),
			)
		}
		grp.If(jen.Len(jen.Id("mutators")).Op(">").Lit(0)).Block(
			jen.If(
				jen.List(jen.Id("_"), jen.Id("err")).Op(":=").
					Id("mutators").Index(jen.Lit(0)).Dot("Mutate").
					Call(
						jen.Id("ctx"),
						jen.Id("builders").Index(jen.Lit(0)).Dot("mutation"),
					),
				jen.Id("err").Op("!=").Nil(),
			).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
		)
		grp.Return(jen.Id("nodes"), jen.Nil())
	})

	// SaveX
	f.Commentf("SaveX is like Save, but panics if an error occurs.")
	f.Func().Params(jen.Id("_cb").Op("*").Id(bulkName)).Id("SaveX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Index().Op("*").Qual(entityReturnPkg, t.Name).Block(
		jen.List(jen.Id("v"), jen.Id("err")).Op(":=").Id("_cb").Dot("Save").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("v")),
	)

	// Exec
	f.Comment("Exec executes the query.")
	f.Func().Params(jen.Id("_cb").Op("*").Id(bulkName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id("_cb").Dot("Save").Call(jen.Id("ctx")),
		jen.Return(jen.Id("err")),
	)

	// ExecX
	f.Comment("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id("_cb").Op("*").Id(bulkName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id("_cb").Dot("Exec").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)
}

// genCreateUpsert generates OnConflict/OnConflictColumns methods and the Upsert builder
// on the root Create builder. Ported from create.go genCreateUpsert.
//
// All chainable methods on the Upsert builder return the entity package's
// XxxUpserter interface so the upsert chain is reachable through the
// public client API (entity.TagCreator.OnConflict() → entity.TagUpserter)
// without callers having to grab the concrete *xxx.XxxUpsert type.
func genCreateUpsert(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName, recv, entityReturnPkg string) {
	upsertName := t.Name + "Upsert"
	sqlPkg := h.SQLPkg()
	upserterRet := jen.Op("*").Id(upsertName)

	// OnConflict method
	f.Commentf("OnConflict allows configuring the `ON CONFLICT` / `ON DUPLICATE KEY` clause")
	f.Commentf("of the INSERT statement. For example:")
	f.Comment("")
	f.Commentf("\tclient.%s.Create().", t.Name)
	f.Comment("\t\tOnConflict(sql.ConflictColumns(\"email\")).")
	f.Comment("\t\tUpdateNewValues().")
	f.Comment("\t\tExec(ctx)")
	f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("OnConflict").Params(
		jen.Id("opts").Op("...").Qual(sqlPkg, "ConflictOption"),
	).Add(upserterRet.Clone()).Block(
		jen.Id(recv).Dot("conflict").Op("=").Id("opts"),
		jen.Return(jen.Op("&").Id(upsertName).Values(jen.Dict{
			jen.Id("create"): jen.Id(recv),
		})),
	)

	// OnConflictColumns method
	f.Comment("OnConflictColumns calls `OnConflict` and configures the columns")
	f.Comment("as conflict target. Using this option is equivalent to using:")
	f.Comment("")
	f.Commentf("\tclient.%s.Create().", t.Name)
	f.Comment("\t\tOnConflict(sql.ConflictColumns(columns...)).")
	f.Comment("\t\tExec(ctx)")
	f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("OnConflictColumns").Params(
		jen.Id("columns").Op("...").String(),
	).Add(upserterRet.Clone()).Block(
		jen.Id(recv).Dot("conflict").Op("=").Append(
			jen.Id(recv).Dot("conflict"),
			jen.Qual(sqlPkg, "ConflictColumns").Call(jen.Id("columns").Op("...")),
		),
		jen.Return(jen.Op("&").Id(upsertName).Values(jen.Dict{
			jen.Id("create"): jen.Id(recv),
		})),
	)

	// Upsert builder struct
	f.Commentf("%s is the builder for \"upsert\"-ing %s nodes.", upsertName, t.Name)
	f.Type().Id(upsertName).Struct(
		jen.Id("create").Op("*").Id(builderName),
		jen.Id("update").Index().Func().Params(jen.Op("*").Qual(sqlPkg, "UpdateSet")),
	)

	// UpdateNewValues method
	f.Comment("UpdateNewValues updates the mutable fields using the new values that were set on create.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertName)).Id("UpdateNewValues").Params().Add(upserterRet.Clone()).Block(
		jen.Id("u").Dot("create").Dot("conflict").Op("=").Append(
			jen.Id("u").Dot("create").Dot("conflict"),
			jen.Qual(sqlPkg, "ResolveWithNewValues").Call(),
		),
		jen.Return(jen.Id("u")),
	)

	// Ignore method
	f.Comment("Ignore sets each column to itself in case of conflict.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertName)).Id("Ignore").Params().Add(upserterRet.Clone()).Block(
		jen.Id("u").Dot("create").Dot("conflict").Op("=").Append(
			jen.Id("u").Dot("create").Dot("conflict"),
			jen.Qual(sqlPkg, "ResolveWithIgnore").Call(),
		),
		jen.Return(jen.Id("u")),
	)

	// DoNothing method
	f.Comment("DoNothing configures the conflict_action to `DO NOTHING`.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertName)).Id("DoNothing").Params().Add(upserterRet.Clone()).Block(
		jen.Id("u").Dot("create").Dot("conflict").Op("=").Append(
			jen.Id("u").Dot("create").Dot("conflict"),
			jen.Qual(sqlPkg, "DoNothing").Call(),
		),
		jen.Return(jen.Id("u")),
	)

	// Update method — applies custom update functions
	f.Comment("Update allows overriding fields `ON CONFLICT` clause.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertName)).Id("Update").Params(
		jen.Id("set").Func().Params(jen.Op("*").Qual(sqlPkg, "UpdateSet")),
	).Add(upserterRet.Clone()).Block(
		jen.Id("u").Dot("update").Op("=").Append(jen.Id("u").Dot("update"), jen.Id("set")),
		jen.Return(jen.Id("u")),
	)

	// Per-field SetXxx methods on Upsert builder
	for _, fd := range t.MutableFields() {
		fieldPascal := fd.StructField()
		column := fd.Name
		f.Commentf("Set%s sets the %q field.", fieldPascal, column)
		f.Func().Params(jen.Id("u").Op("*").Id(upsertName)).Id("Set"+fieldPascal).Params(
			jen.Id("v").Add(h.BaseType(fd)),
		).Add(upserterRet.Clone()).Block(
			jen.Id("u").Dot("update").Op("=").Append(jen.Id("u").Dot("update"),
				jen.Func().Params(jen.Id("s").Op("*").Qual(sqlPkg, "UpdateSet")).Block(
					jen.Id("s").Dot("Set").Call(jen.Lit(column), jen.Id("v")),
				),
			),
			jen.Return(jen.Id("u")),
		)

		// ClearXxx for nillable fields
		if fd.Nillable {
			f.Commentf("Clear%s clears the value of the %q field.", fieldPascal, column)
			f.Func().Params(jen.Id("u").Op("*").Id(upsertName)).Id("Clear"+fieldPascal).Params().Add(upserterRet.Clone()).Block(
				jen.Id("u").Dot("update").Op("=").Append(jen.Id("u").Dot("update"),
					jen.Func().Params(jen.Id("s").Op("*").Qual(sqlPkg, "UpdateSet")).Block(
						jen.Id("s").Dot("SetNull").Call(jen.Lit(column)),
					),
				),
				jen.Return(jen.Id("u")),
			)
		}

		// AddXxx for numeric types
		if fd.SupportsMutationAdd() {
			f.Commentf("Add%s adds v to the %q field.", fieldPascal, column)
			f.Func().Params(jen.Id("u").Op("*").Id(upsertName)).Id("Add"+fieldPascal).Params(
				jen.Id("v").Add(h.BaseType(fd)),
			).Add(upserterRet.Clone()).Block(
				jen.Id("u").Dot("update").Op("=").Append(jen.Id("u").Dot("update"),
					jen.Func().Params(jen.Id("s").Op("*").Qual(sqlPkg, "UpdateSet")).Block(
						jen.Id("s").Dot("Add").Call(jen.Lit(column), jen.Id("v")),
					),
				),
				jen.Return(jen.Id("u")),
			)
		}
	}

	// applyConflictOpts — private helper on the upsert builder that
	// folds any accumulated SetXxx / Update callbacks into a single
	// sql.ResolveWith conflict option. Called by both Exec and ID so
	// they share the same conflict-resolver assembly logic.
	f.Comment("applyConflictOpts folds any accumulated SetXxx / Update callbacks")
	f.Comment("into a single sql.ResolveWith option on the underlying Create.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertName)).Id("applyConflictOpts").Params().Block(
		jen.If(jen.Len(jen.Id("u").Dot("update")).Op(">").Lit(0)).Block(
			jen.Id("u").Dot("create").Dot("conflict").Op("=").Append(
				jen.Id("u").Dot("create").Dot("conflict"),
				jen.Qual(sqlPkg, "ResolveWith").Call(
					jen.Func().Params(jen.Id("s").Op("*").Qual(sqlPkg, "UpdateSet")).Block(
						jen.For(jen.List(jen.Id("_"), jen.Id("fn")).Op(":=").Range().Id("u").Dot("update")).Block(
							jen.Id("fn").Call(jen.Id("s")),
						),
					),
				),
			),
			jen.Id("u").Dot("update").Op("=").Nil(),
		),
	)

	// Exec method on Upsert builder
	f.Comment("Exec executes the upsert query.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.Id("u").Dot("applyConflictOpts").Call(),
		jen.Return(jen.Id("u").Dot("create").Dot("Exec").Call(jen.Id("ctx"))),
	)

	// ExecX method on Upsert builder
	f.Comment("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id("u").Dot("Exec").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)

	// ID / IDX — Ent-style terminals that run the upsert and return
	// the inserted-or-updated row's primary key. Only emitted for
	// entities with a single-field ID. Under the hood they delegate
	// to the underlying Create.Save (which returns the saved
	// entity) and read the ID off the returned node.
	if t.HasOneFieldID() {
		idType := h.IDType(t)
		f.Commentf("ID executes the upsert and returns the inserted or updated %s ID.", t.Name)
		f.Func().Params(jen.Id("u").Op("*").Id(upsertName)).Id("ID").Params(
			jen.Id("ctx").Qual("context", "Context"),
		).Params(jen.Id("id").Add(idType), jen.Id("err").Error()).BlockFunc(func(body *jen.Group) {
			body.Id("u").Dot("applyConflictOpts").Call()
			body.List(jen.Id("node"), jen.Id("saveErr")).Op(":=").
				Id("u").Dot("create").Dot("Save").Call(jen.Id("ctx"))
			body.If(jen.Id("saveErr").Op("!=").Nil()).Block(
				jen.Return(jen.Id("id"), jen.Id("saveErr")),
			)
			body.Return(jen.Id("node").Dot(t.ID.StructField()), jen.Nil())
		})

		f.Comment("IDX is like ID, but panics if an error occurs.")
		f.Func().Params(jen.Id("u").Op("*").Id(upsertName)).Id("IDX").Params(
			jen.Id("ctx").Qual("context", "Context"),
		).Add(idType).Block(
			jen.List(jen.Id("id"), jen.Id("err")).Op(":=").Id("u").Dot("ID").Call(jen.Id("ctx")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Panic(jen.Id("err")),
			),
			jen.Return(jen.Id("id")),
		)
	}
}

// genFieldSetter generates a field setter method on a root wrapper builder.
// The method delegates to the specified target field (e.g., "mutation" or "inner")
// and returns the wrapper for chaining.
//
// ifaceReturn is the interface return type for chaining methods (e.g., entity.UserCreator).
// When non-nil, chaining methods return the interface type instead of the concrete pointer.
func genFieldSetter(h gen.GeneratorHelper, f *jen.File, builderName, recv string, fd *gen.Field, isUpdate bool, target string, ifaceReturn jen.Code) {
	fieldPascal := fd.StructField()

	retType := chainReturnType(builderName, ifaceReturn)

	// SetXxx
	f.Commentf("Set%s sets the %q field.", fieldPascal, fd.Name)
	f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("Set"+fieldPascal).Params(
		jen.Id("v").Add(h.BaseType(fd)),
	).Add(retType).Block(
		jen.Id(recv).Dot(target).Dot("Set"+fieldPascal).Call(jen.Id("v")),
		jen.Return(jen.Id(recv)),
	)

	// SetNillableXxx — same conditions as entity setters.go
	nillableCreate := !isUpdate && (fd.Optional || fd.Default || fd.Nillable)
	nillableUpdate := isUpdate && !fd.UpdateDefault
	if nillableCreate || nillableUpdate {
		f.Commentf("SetNillable%s sets the %q field if the given value is not nil.", fieldPascal, fd.Name)
		f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("SetNillable"+fieldPascal).Params(
			jen.Id("v").Op("*").Add(h.BaseType(fd)),
		).Add(retType).Block(
			jen.If(jen.Id("v").Op("!=").Nil()).Block(
				jen.Id(recv).Dot("Set"+fieldPascal).Call(jen.Op("*").Id("v")),
			),
			jen.Return(jen.Id(recv)),
		)
	}

	// AddXxx for numeric types (update builders only)
	if isUpdate && fd.SupportsMutationAdd() {
		f.Commentf("Add%s adds v to the %q field.", fieldPascal, fd.Name)
		f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("Add"+fieldPascal).Params(
			jen.Id("v").Add(h.BaseType(fd)),
		).Add(retType).Block(
			jen.Id(recv).Dot(target).Dot("Add"+fieldPascal).Call(jen.Id("v")),
			jen.Return(jen.Id(recv)),
		)
	}

	// AppendXxx for JSON slice fields (update builders only)
	if isUpdate && fd.IsJSON() {
		f.Commentf("Append%s appends v to the %q field.", fieldPascal, fd.Name)
		f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("Append"+fieldPascal).Params(
			jen.Id("v").Add(h.BaseType(fd)),
		).Add(retType).Block(
			jen.Id(recv).Dot(target).Dot("Append"+fieldPascal).Call(jen.Id("v")),
			jen.Return(jen.Id(recv)),
		)
	}

	// ClearXxx for nillable fields only (update builders only)
	if isUpdate && fd.Nillable {
		f.Commentf("Clear%s clears the value of the %q field.", fieldPascal, fd.Name)
		f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id("Clear"+fieldPascal).Params().Add(retType).Block(
			jen.Id(recv).Dot(target).Dot("Clear"+fieldPascal).Call(),
			jen.Return(jen.Id(recv)),
		)
	}
}

// genEdgeSetter generates edge setter methods on a root wrapper builder.
// Only ID-based methods are generated. These delegate to the specified target field.
//
// ifaceReturn is the interface return type for chaining methods (e.g., entity.UserCreator).
// When non-nil, chaining methods return the interface type instead of the concrete pointer.
func genEdgeSetter(h gen.GeneratorHelper, f *jen.File, builderName, recv string, _ *gen.Type, edge *gen.Edge, isUpdate bool, target string, ifaceReturn jen.Code) {
	edgePascal := edge.StructField()

	retType := chainReturnType(builderName, ifaceReturn)

	if edge.Unique {
		// SetXxxID
		setMethod := edge.MutationSet()
		f.Commentf("%s sets the %q edge by id.", setMethod, edge.Name)
		f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id(setMethod).Params(
			jen.Id("id").Add(h.IDType(edge.Type)),
		).Add(retType).Block(
			jen.Id(recv).Dot(target).Dot(setMethod).Call(jen.Id("id")),
			jen.Return(jen.Id(recv)),
		)

		// SetNillableXxxID
		nillableMethod := "SetNillable" + edgePascal + "ID"
		f.Commentf("%s sets the %q edge by id if the given value is not nil.", nillableMethod, edge.Name)
		f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id(nillableMethod).Params(
			jen.Id("id").Op("*").Add(h.IDType(edge.Type)),
		).Add(retType).Block(
			jen.If(jen.Id("id").Op("!=").Nil()).Block(
				jen.Id(recv).Dot(setMethod).Call(jen.Op("*").Id("id")),
			),
			jen.Return(jen.Id(recv)),
		)

		// ClearXxx (update only)
		if isUpdate {
			clearMethod := edge.MutationClear()
			f.Commentf("%s clears the %q edge.", clearMethod, edge.Name)
			f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id(clearMethod).Params().Add(retType).Block(
				jen.Id(recv).Dot(target).Dot(clearMethod).Call(),
				jen.Return(jen.Id(recv)),
			)
		}
	} else {
		// AddXxxIDs
		addMethod := edge.MutationAdd()
		f.Commentf("%s adds the %q edge by ids.", addMethod, edge.Name)
		f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id(addMethod).Params(
			jen.Id("ids").Op("...").Add(h.IDType(edge.Type)),
		).Add(retType).Block(
			jen.Id(recv).Dot(target).Dot(addMethod).Call(jen.Id("ids").Op("...")),
			jen.Return(jen.Id(recv)),
		)

		// RemoveXxxIDs and ClearXxx (update only)
		if isUpdate {
			removeMethod := edge.MutationRemove()
			f.Commentf("%s removes the %q edge by ids.", removeMethod, edge.Name)
			f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id(removeMethod).Params(
				jen.Id("ids").Op("...").Add(h.IDType(edge.Type)),
			).Add(retType).Block(
				jen.Id(recv).Dot(target).Dot(removeMethod).Call(jen.Id("ids").Op("...")),
				jen.Return(jen.Id(recv)),
			)

			clearMethod := edge.MutationClear()
			f.Commentf("%s clears the %q edge.", clearMethod, edge.Name)
			f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id(clearMethod).Params().Add(retType).Block(
				jen.Id(recv).Dot(target).Dot(clearMethod).Call(),
				jen.Return(jen.Id(recv)),
			)
		}
	}
}

// genEdgeEntitySetter generates entity-reference edge setter methods on a root wrapper builder.
// These accept root wrapper types (e.g., *Post) and extract IDs to delegate to the ID-based methods.
//
// ifaceReturn is the interface return type for chaining methods (e.g., entity.UserCreator).
// When non-nil, chaining methods return the interface type instead of the concrete pointer.
//
// For unique edges:
//
//	func (c *PostCreate) SetAuthor(v *User) entity.PostCreator {
//	    c.SetAuthorID(v.ID)
//	    return c
//	}
//
// For non-unique edges:
//
//	func (c *UserCreate) AddPosts(v ...*Post) entity.UserCreator {
//	    ids := make([]int, len(v))
//	    for i := range v { ids[i] = v[i].ID }
//	    c.AddPostIDs(ids...)
//	    return c
//	}
func genEdgeEntitySetter(h gen.GeneratorHelper, f *jen.File, builderName, recv string, edge *gen.Edge, isUpdate bool, ifaceReturn jen.Code) {
	edgePascal := edge.StructField()
	idType := h.IDType(edge.Type)

	retType := chainReturnType(builderName, ifaceReturn)

	// Reference entity/ package types for edge targets.
	edgeTypeRef := jen.Qual(h.SharedEntityPkg(), edge.Type.Name)

	if edge.Unique {
		// SetXxx(v *TargetType)
		methodName := "Set" + edgePascal
		f.Commentf("%s sets the %q edge to the given entity.", methodName, edge.Name)
		f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id(methodName).Params(
			jen.Id("v").Op("*").Add(edgeTypeRef),
		).Add(retType).Block(
			jen.Id(recv).Dot(edge.MutationSet()).Call(jen.Id("v").Dot("ID")),
			jen.Return(jen.Id(recv)),
		)
	} else {
		// AddXxx(v ...*TargetType)
		methodName := "Add" + edgePascal
		addIDsMethod := edge.MutationAdd()
		f.Commentf("%s adds the %q edge to the given entities.", methodName, edge.Name)
		f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id(methodName).Params(
			jen.Id("v").Op("...").Op("*").Add(edgeTypeRef),
		).Add(retType).Block(
			jen.Id("ids").Op(":=").Make(jen.Index().Add(idType), jen.Len(jen.Id("v"))),
			jen.For(jen.Id("i").Op(":=").Range().Id("v")).Block(
				jen.Id("ids").Index(jen.Id("i")).Op("=").Id("v").Index(jen.Id("i")).Dot("ID"),
			),
			jen.Id(recv).Dot(addIDsMethod).Call(jen.Id("ids").Op("...")),
			jen.Return(jen.Id(recv)),
		)

		// RemoveXxx(v ...*TargetType) — update only
		if isUpdate {
			removeMethodName := "Remove" + edgePascal
			removeIDsMethod := edge.MutationRemove()
			f.Commentf("%s removes the %q edge to the given entities.", removeMethodName, edge.Name)
			f.Func().Params(jen.Id(recv).Op("*").Id(builderName)).Id(removeMethodName).Params(
				jen.Id("v").Op("...").Op("*").Add(edgeTypeRef),
			).Add(retType).Block(
				jen.Id("ids").Op(":=").Make(jen.Index().Add(idType), jen.Len(jen.Id("v"))),
				jen.For(jen.Id("i").Op(":=").Range().Id("v")).Block(
					jen.Id("ids").Index(jen.Id("i")).Op("=").Id("v").Index(jen.Id("i")).Dot("ID"),
				),
				jen.Id(recv).Dot(removeIDsMethod).Call(jen.Id("ids").Op("...")),
				jen.Return(jen.Id(recv)),
			)
		}
	}
}

// chainReturnType returns the jen.Code for a chaining method's return type.
// If ifaceReturn is non-nil, it returns the interface type; otherwise the concrete pointer.
func chainReturnType(builderName string, ifaceReturn jen.Code) jen.Code {
	if ifaceReturn != nil {
		return ifaceReturn
	}
	return jen.Op("*").Id(builderName)
}
