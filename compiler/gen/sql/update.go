package sql

import (
	"fmt"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genUpdate generates the update builder file ({entity}_update.go).
func genUpdate(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	f := h.NewFile(h.Pkg())

	// Generate Update builder (batch update)
	genUpdateBuilder(h, f, t)

	// Generate UpdateOne builder
	genUpdateOneBuilder(h, f, t)

	return f
}

// genUpdateBuilder generates the Update builder struct and methods.
func genUpdateBuilder(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	updateName := t.UpdateName()
	mutationName := t.MutationName()
	modifierEnabled := h.FeatureEnabled("sql/modifier")

	// Update struct
	f.Commentf("%s is the builder for updating %s entities.", updateName, t.Name)
	f.Type().Id(updateName).StructFunc(func(group *jen.Group) {
		group.Id("config") // embedded config
		group.Id("mutation").Op("*").Id(mutationName)
		group.Id("hooks").Index().Id("Hook")
		// Modifiers field (used by sql/modifier feature)
		if modifierEnabled {
			group.Id("modifiers").Index().Func().Params(jen.Op("*").Qual(h.SQLPkg(), "UpdateBuilder"))
		}
	})

	// Where adds predicates
	f.Commentf("Where appends a list predicates to the %s.", updateName)
	f.Func().Params(jen.Id(t.UpdateReceiver()).Op("*").Id(updateName)).Id("Where").Params(
		jen.Id("ps").Op("...").Add(h.PredicateType(t)),
	).Op("*").Id(updateName).Block(
		jen.Id(t.UpdateReceiver()).Dot("mutation").Dot("Where").Call(jen.Id("ps").Op("...")),
		jen.Return(jen.Id(t.UpdateReceiver())),
	)

	// SetXxx methods for mutable fields (skip auto-generated edge fields - they're handled by edge methods)
	for _, field := range t.MutableFields() {
		// Skip auto-generated edge fields (FK columns), but include user-defined edge fields
		if field.IsEdgeField() && !field.UserDefined {
			continue
		}
		genUpdateFieldSetter(h, f, t, field, updateName, t.UpdateReceiver())
	}

	// Edge mutation methods (setter.tmpl uses EdgesWithID)
	for _, edge := range t.EdgesWithID() {
		if edge.Immutable {
			continue
		}
		genUpdateEdgeMethods(h, f, t, edge, updateName, t.UpdateReceiver())
	}

	// Mutation returns the mutation
	f.Commentf("Mutation returns the %s of this builder.", mutationName)
	f.Func().Params(jen.Id(t.UpdateReceiver()).Op("*").Id(updateName)).Id("Mutation").Params().Op("*").Id(mutationName).Block(
		jen.Return(jen.Id(t.UpdateReceiver()).Dot("mutation")),
	)

	// Save executes the update
	runtimeRequired := t.NumHooks() > 0 || t.NumPolicy() > 0
	f.Commentf("Save executes the query and returns the number of nodes affected by the update.")
	f.Func().Params(jen.Id(t.UpdateReceiver()).Op("*").Id(updateName)).Id("Save").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Int(), jen.Error()).BlockFunc(func(grp *jen.Group) {
		// Call defaults() if type has update defaults
		if t.HasUpdateDefault() {
			if runtimeRequired {
				// defaults() returns error when hooks/policies exist
				grp.If(jen.Id("err").Op(":=").Id(t.UpdateReceiver()).Dot("defaults").Call(), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Lit(0), jen.Id("err")),
				)
			} else {
				grp.Id(t.UpdateReceiver()).Dot("defaults").Call()
			}
		}
		grp.Return(jen.Id("withHooks").Index(jen.Int()).Call(
			jen.Id("ctx"),
			jen.Id(t.UpdateReceiver()).Dot("sqlSave"),
			jen.Id(t.UpdateReceiver()).Dot("mutation"),
			jen.Id(t.UpdateReceiver()).Dot("hooks"),
		))
	})

	// SaveX is like Save but panics
	f.Commentf("SaveX is like Save, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.UpdateReceiver()).Op("*").Id(updateName)).Id("SaveX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Int().Block(
		jen.List(jen.Id("affected"), jen.Id("err")).Op(":=").Id(t.UpdateReceiver()).Dot("Save").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("affected")),
	)

	// Exec executes the query
	f.Commentf("Exec executes the query.")
	f.Func().Params(jen.Id(t.UpdateReceiver()).Op("*").Id(updateName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id(t.UpdateReceiver()).Dot("Save").Call(jen.Id("ctx")),
		jen.Return(jen.Id("err")),
	)

	// ExecX is like Exec but panics
	f.Commentf("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.UpdateReceiver()).Op("*").Id(updateName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id(t.UpdateReceiver()).Dot("Exec").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)

	// Feature: sql/modifier - Modify method
	if modifierEnabled {
		f.Comment("Modify adds a statement modifier for attaching custom logic to the UPDATE statement.")
		f.Func().Params(jen.Id(t.UpdateReceiver()).Op("*").Id(updateName)).Id("Modify").Params(
			jen.Id("modifiers").Op("...").Func().Params(jen.Id("u").Op("*").Qual(h.SQLPkg(), "UpdateBuilder")),
		).Op("*").Id(updateName).Block(
			jen.Id(t.UpdateReceiver()).Dot("modifiers").Op("=").Append(jen.Id(t.UpdateReceiver()).Dot("modifiers"), jen.Id("modifiers").Op("...")),
			jen.Return(jen.Id(t.UpdateReceiver())),
		)
	}

	// sqlSave executes the SQL
	genUpdateSQLSave(h, f, t, updateName, false, modifierEnabled, t.UpdateReceiver())

	// defaults method - sets update default values of the builder before save
	if t.HasUpdateDefault() {
		genUpdateDefaults(h, f, t, updateName, t.UpdateReceiver())
	}

	// check method - validates mutable fields
	if t.HasUpdateCheckers() {
		genUpdateCheck(h, f, t, updateName, t.UpdateReceiver())
	}
}

// genUpdateOneBuilder generates the UpdateOne builder struct and methods.
func genUpdateOneBuilder(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	updateOneName := t.UpdateOneName()
	mutationName := t.MutationName()
	modifierEnabled := h.FeatureEnabled("sql/modifier")

	// UpdateOne struct
	f.Commentf("%s is the builder for updating a single %s entity.", updateOneName, t.Name)
	f.Type().Id(updateOneName).StructFunc(func(group *jen.Group) {
		group.Id("config") // embedded config
		group.Id("fields").Index().String()
		group.Id("hooks").Index().Id("Hook")
		group.Id("mutation").Op("*").Id(mutationName)
		// Modifiers field (used by sql/modifier feature)
		if modifierEnabled {
			group.Id("modifiers").Index().Func().Params(jen.Op("*").Qual(h.SQLPkg(), "UpdateBuilder"))
		}
	})

	// SetXxx methods for mutable fields (skip auto-generated edge fields - they're handled by edge methods)
	for _, field := range t.MutableFields() {
		// Skip auto-generated edge fields (FK columns), but include user-defined edge fields
		if field.IsEdgeField() && !field.UserDefined {
			continue
		}
		genUpdateFieldSetter(h, f, t, field, updateOneName, t.UpdateOneReceiver())
	}

	// Edge mutation methods (setter.tmpl uses EdgesWithID)
	for _, edge := range t.EdgesWithID() {
		if edge.Immutable {
			continue
		}
		genUpdateEdgeMethods(h, f, t, edge, updateOneName, t.UpdateOneReceiver())
	}

	// Mutation returns the mutation
	f.Commentf("Mutation returns the %s of this builder.", mutationName)
	f.Func().Params(jen.Id(t.UpdateOneReceiver()).Op("*").Id(updateOneName)).Id("Mutation").Params().Op("*").Id(mutationName).Block(
		jen.Return(jen.Id(t.UpdateOneReceiver()).Dot("mutation")),
	)

	// Where adds predicates (for conditional update)
	f.Commentf("Where appends a list predicates to the %s.", updateOneName)
	f.Func().Params(jen.Id(t.UpdateOneReceiver()).Op("*").Id(updateOneName)).Id("Where").Params(
		jen.Id("ps").Op("...").Add(h.PredicateType(t)),
	).Op("*").Id(updateOneName).Block(
		jen.Id(t.UpdateOneReceiver()).Dot("mutation").Dot("Where").Call(jen.Id("ps").Op("...")),
		jen.Return(jen.Id(t.UpdateOneReceiver())),
	)

	// Select allows selecting fields to update
	f.Commentf("Select allows selecting one or more fields (columns) of the returned entity.")
	f.Func().Params(jen.Id(t.UpdateOneReceiver()).Op("*").Id(updateOneName)).Id("Select").Params(
		jen.Id("field").String(),
		jen.Id("fields").Op("...").String(),
	).Op("*").Id(updateOneName).Block(
		jen.Id(t.UpdateOneReceiver()).Dot("fields").Op("=").Append(jen.Index().String().Values(jen.Id("field")), jen.Id("fields").Op("...")),
		jen.Return(jen.Id(t.UpdateOneReceiver())),
	)

	// Save executes the update
	runtimeRequiredOne := t.NumHooks() > 0 || t.NumPolicy() > 0
	f.Commentf("Save executes the query and returns the updated %s entity.", t.Name)
	f.Func().Params(jen.Id(t.UpdateOneReceiver()).Op("*").Id(updateOneName)).Id("Save").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Id(t.Name), jen.Error()).BlockFunc(func(grp *jen.Group) {
		// Call defaults() if type has update defaults
		if t.HasUpdateDefault() {
			if runtimeRequiredOne {
				// defaults() returns error when hooks/policies exist
				grp.If(jen.Id("err").Op(":=").Id(t.UpdateOneReceiver()).Dot("defaults").Call(), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				)
			} else {
				grp.Id(t.UpdateOneReceiver()).Dot("defaults").Call()
			}
		}
		grp.Return(jen.Id("withHooks").Index(jen.Op("*").Id(t.Name)).Call(
			jen.Id("ctx"),
			jen.Id(t.UpdateOneReceiver()).Dot("sqlSave"),
			jen.Id(t.UpdateOneReceiver()).Dot("mutation"),
			jen.Id(t.UpdateOneReceiver()).Dot("hooks"),
		))
	})

	// SaveX is like Save but panics
	f.Commentf("SaveX is like Save, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.UpdateOneReceiver()).Op("*").Id(updateOneName)).Id("SaveX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Op("*").Id(t.Name).Block(
		jen.List(jen.Id("node"), jen.Id("err")).Op(":=").Id(t.UpdateOneReceiver()).Dot("Save").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("node")),
	)

	// Exec executes the query
	f.Commentf("Exec executes the query on the entity.")
	f.Func().Params(jen.Id(t.UpdateOneReceiver()).Op("*").Id(updateOneName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id(t.UpdateOneReceiver()).Dot("Save").Call(jen.Id("ctx")),
		jen.Return(jen.Id("err")),
	)

	// ExecX is like Exec but panics
	f.Commentf("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.UpdateOneReceiver()).Op("*").Id(updateOneName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id(t.UpdateOneReceiver()).Dot("Exec").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)

	// Feature: sql/modifier - Modify method
	if modifierEnabled {
		f.Comment("Modify adds a statement modifier for attaching custom logic to the UPDATE statement.")
		f.Func().Params(jen.Id(t.UpdateOneReceiver()).Op("*").Id(updateOneName)).Id("Modify").Params(
			jen.Id("modifiers").Op("...").Func().Params(jen.Id("u").Op("*").Qual(h.SQLPkg(), "UpdateBuilder")),
		).Op("*").Id(updateOneName).Block(
			jen.Id(t.UpdateOneReceiver()).Dot("modifiers").Op("=").Append(jen.Id(t.UpdateOneReceiver()).Dot("modifiers"), jen.Id("modifiers").Op("...")),
			jen.Return(jen.Id(t.UpdateOneReceiver())),
		)
	}

	// sqlSave executes the SQL
	genUpdateSQLSave(h, f, t, updateOneName, true, modifierEnabled, t.UpdateOneReceiver())

	// defaults method - sets update default values of the builder before save
	if t.HasUpdateDefault() {
		genUpdateDefaults(h, f, t, updateOneName, t.UpdateOneReceiver())
	}

	// check method - validates mutable fields
	if t.HasUpdateCheckers() {
		genUpdateCheck(h, f, t, updateOneName, t.UpdateOneReceiver())
	}
}

// genUpdateFieldSetter generates SetXxx, ClearXxx, AddXxx methods for a field.
func genUpdateFieldSetter(h gen.GeneratorHelper, f *jen.File, t *gen.Type, field *gen.Field, builderName string, receiver string) {
	setter := field.MutationSet()

	// SetXxx - always uses base type (non-pointer), nillability handled via SetNillableXxx
	// For numeric fields that support Add, call Reset first to clear any previous Add operations
	f.Commentf("%s sets the %q field.", setter, field.Name)
	f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(setter).Params(
		jen.Id("v").Add(h.BaseType(field)),
	).Op("*").Id(builderName).BlockFunc(func(grp *jen.Group) {
		// For numeric fields, reset first to clear any prior Add operations (like Ent)
		if field.SupportsMutationAdd() {
			grp.Id(receiver).Dot("mutation").Dot(field.MutationReset()).Call()
		}
		grp.Id(receiver).Dot("mutation").Dot(setter).Call(jen.Id("v"))
		grp.Return(jen.Id(receiver))
	})

	// SetNillableXxx for updaters: generated when field has no UpdateDefault and type is not already nillable
	// Per Ent template: $nillableU := and $updater (not $f.UpdateDefault)
	// Combined with: (not $f.Type.Nillable) (not $skipNillable)
	nillableU := !field.UpdateDefault
	typeNillable := field.Type != nil && field.Type.Nillable

	// Check for naming collision with other fields
	// Per Ent template: updater uses $fields = $.MutableFields for collision check
	nillableName := "SetNillable" + field.StructField()
	skipNillable := false
	for _, otherField := range t.MutableFields() {
		if otherField.Name != field.Name && otherField.MutationSet() == nillableName {
			skipNillable = true
			break
		}
	}

	if nillableU && !typeNillable && !skipNillable {
		f.Commentf("%s sets the %q field if the given value is not nil.", nillableName, field.Name)
		f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(nillableName).Params(
			jen.Id("v").Op("*").Add(h.BaseType(field)),
		).Op("*").Id(builderName).Block(
			jen.If(jen.Id("v").Op("!=").Nil()).Block(
				jen.Id(receiver).Dot(setter).Call(jen.Op("*").Id("v")),
			),
			jen.Return(jen.Id(receiver)),
		)
	}

	// ClearXxx for Nillable fields in update builders (can set NULL in DB)
	// Note: Velox Optional() means NOT NULL in DB, only Nillable() allows NULL
	// Unlike Ent where Optional implies NULL-able, Velox separates these concerns
	if field.Nillable {
		clearer := field.MutationClear()
		f.Commentf("%s clears the value of the %q field.", clearer, field.Name)
		f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(clearer).Params().Op("*").Id(builderName).Block(
			jen.Id(receiver).Dot("mutation").Dot(clearer).Call(),
			jen.Return(jen.Id(receiver)),
		)
	}

	// AddXxx for numeric fields
	if field.SupportsMutationAdd() {
		adder := field.MutationAdd()
		signedType, _ := field.SignedType()
		addType := h.BaseType(field)
		if signedType != nil {
			// Use the signed type for the adder parameter
			addType = jen.Id(signedType.String())
		}
		f.Commentf("%s adds the value to the %q field.", adder, field.Name)
		f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(adder).Params(
			jen.Id("v").Add(addType),
		).Op("*").Id(builderName).Block(
			jen.Id(receiver).Dot("mutation").Dot(adder).Call(jen.Id("v")),
			jen.Return(jen.Id(receiver)),
		)
	}

	// AppendXxx for JSON array fields
	// Per Ent template (setter.tmpl lines 59-65): only for updaters with SupportsMutationAppend
	if field.SupportsMutationAppend() {
		appender := field.MutationAppend()
		f.Commentf("%s appends value to the %q field.", appender, field.Name)
		f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(appender).Params(
			jen.Id("v").Add(h.BaseType(field)),
		).Op("*").Id(builderName).Block(
			jen.Id(receiver).Dot("mutation").Dot(appender).Call(jen.Id("v")),
			jen.Return(jen.Id(receiver)),
		)
	}
}

// genUpdateEdgeMethods generates edge mutation methods for update builders.
// This combines logic from setter.tmpl (Set/Add methods) and update/edges (Clear/Remove methods).
func genUpdateEdgeMethods(h gen.GeneratorHelper, f *jen.File, t *gen.Type, edge *gen.Edge, builderName string, receiver string) {
	// Per Ent template: $withSetter := not $e.HasFieldSetter
	withSetter := !edge.HasFieldSetter()

	if edge.Unique {
		// === setter.tmpl logic for unique edges ===
		setter := edge.MutationSet() // e.g., "SetOwnerID"

		// SetXxxID for unique edges (only if no field setter)
		if withSetter {
			f.Commentf("%s sets the %q edge to %s by ID.", setter, edge.Name, edge.Type.Name)
			f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(setter).Params(
				jen.Id("id").Add(h.IDType(edge.Type)),
			).Op("*").Id(builderName).Block(
				jen.Id(receiver).Dot("mutation").Dot(setter).Call(jen.Id("id")),
				jen.Return(jen.Id(receiver)),
			)
		}

		// SetNillable{Edge}ID for unique optional edges (only if withSetter)
		// Per Ent template: {{ if and $e.Unique $e.Optional $withSetter }}
		if edge.Optional && withSetter {
			nillableSetterName := "SetNillable" + edge.StructField() + "ID"
			f.Commentf("%s sets the %q edge to %s by ID if not nil.", nillableSetterName, edge.Name, edge.Type.Name)
			f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(nillableSetterName).Params(
				jen.Id("id").Op("*").Add(h.IDType(edge.Type)),
			).Op("*").Id(builderName).Block(
				jen.If(jen.Id("id").Op("!=").Nil()).Block(
					jen.Id(receiver).Op("=").Id(receiver).Dot(setter).Call(jen.Op("*").Id("id")),
				),
				jen.Return(jen.Id(receiver)),
			)
		}

		// SetXxx sets the edge entity (always generated for unique edges)
		entitySetter := "Set" + edge.StructField()
		f.Commentf("%s sets the %q edge to %s.", entitySetter, edge.Name, edge.Type.Name)
		f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(entitySetter).Params(
			jen.Id("v").Op("*").Id(edge.Type.Name),
		).Op("*").Id(builderName).BlockFunc(func(grp *jen.Group) {
			if withSetter {
				// Call the builder method
				grp.Return(jen.Id(receiver).Dot(setter).Call(jen.Id("v").Dot("ID")))
			} else {
				// Call mutation directly when there's no builder setter (field setter exists)
				grp.Id(receiver).Dot("mutation").Dot(setter).Call(jen.Id("v").Dot("ID"))
				grp.Return(jen.Id(receiver))
			}
		})

		// === update/edges logic: ClearXxx for unique edges ===
		clearer := edge.MutationClear()
		f.Commentf("%s clears the %q edge to %s.", clearer, edge.Name, edge.Type.Name)
		f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(clearer).Params().Op("*").Id(builderName).Block(
			jen.Id(receiver).Dot("mutation").Dot(clearer).Call(),
			jen.Return(jen.Id(receiver)),
		)
	} else {
		// === setter.tmpl logic for non-unique edges ===
		adder := edge.MutationAdd() // e.g., "AddTagIDs"

		// AddXxxIDs for non-unique edges (only if no field setter)
		if withSetter {
			f.Commentf("%s adds the %q edge to %s by IDs.", adder, edge.Name, edge.Type.Name)
			f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(adder).Params(
				jen.Id("ids").Op("...").Add(h.IDType(edge.Type)),
			).Op("*").Id(builderName).Block(
				jen.Id(receiver).Dot("mutation").Dot(adder).Call(jen.Id("ids").Op("...")),
				jen.Return(jen.Id(receiver)),
			)
		}

		// AddXxx adds edge entities (always generated)
		entityAdder := "Add" + edge.StructField()
		f.Commentf("%s adds the %q edges to %s.", entityAdder, edge.Name, edge.Type.Name)
		f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(entityAdder).Params(
			jen.Id("v").Op("...").Op("*").Id(edge.Type.Name),
		).Op("*").Id(builderName).Block(
			jen.Id("ids").Op(":=").Make(jen.Index().Add(h.IDType(edge.Type)), jen.Len(jen.Id("v"))),
			jen.For(jen.List(jen.Id("i"), jen.Id("e")).Op(":=").Range().Id("v")).Block(
				jen.Id("ids").Index(jen.Id("i")).Op("=").Id("e").Dot("ID"),
			),
			jen.Return(jen.Id(receiver).Dot(adder).Call(jen.Id("ids").Op("..."))),
		)

		// === update/edges logic: Clear/Remove for non-unique edges ===
		// RemoveXxxIDs
		remover := edge.MutationRemove()
		f.Commentf("%s removes the %q edge to %s by IDs.", remover, edge.Name, edge.Type.Name)
		f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(remover).Params(
			jen.Id("ids").Op("...").Add(h.IDType(edge.Type)),
		).Op("*").Id(builderName).Block(
			jen.Id(receiver).Dot("mutation").Dot(remover).Call(jen.Id("ids").Op("...")),
			jen.Return(jen.Id(receiver)),
		)

		// RemoveXxx removes edge entities
		entityRemover := "Remove" + edge.StructField()
		f.Commentf("%s removes the %q edges to %s.", entityRemover, edge.Name, edge.Type.Name)
		f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(entityRemover).Params(
			jen.Id("v").Op("...").Op("*").Id(edge.Type.Name),
		).Op("*").Id(builderName).Block(
			jen.Id("ids").Op(":=").Make(jen.Index().Add(h.IDType(edge.Type)), jen.Len(jen.Id("v"))),
			jen.For(jen.List(jen.Id("i"), jen.Id("e")).Op(":=").Range().Id("v")).Block(
				jen.Id("ids").Index(jen.Id("i")).Op("=").Id("e").Dot("ID"),
			),
			jen.Return(jen.Id(receiver).Dot(remover).Call(jen.Id("ids").Op("..."))),
		)

		// ClearXxx clears all edges
		clearer := edge.MutationClear()
		f.Commentf("%s clears all %q edges to %s.", clearer, edge.Name, edge.Type.Name)
		f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id(clearer).Params().Op("*").Id(builderName).Block(
			jen.Id(receiver).Dot("mutation").Dot(clearer).Call(),
			jen.Return(jen.Id(receiver)),
		)
	}
}

// genUpdateDefaults generates the defaults method for the Update/UpdateOne builder.
func genUpdateDefaults(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName string, receiver string) {
	runtimeRequired := t.NumHooks() > 0 || t.NumPolicy() > 0
	pkg := t.PackageDir()

	f.Comment("defaults sets the default values of the builder before save.")
	if runtimeRequired {
		// Return error when hooks/policies exist
		f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id("defaults").Params().Error().BlockFunc(func(grp *jen.Group) {
			for _, field := range t.Fields {
				if !field.UpdateDefault {
					continue
				}
				// if _, ok := u.mutation.{MutationGet}(); !ok && !u.mutation.{StructField}Cleared() { ... set default ... }
				condition := jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id(receiver).Dot("mutation").Dot(field.MutationGet()).Call()

				// For nillable fields, also check if field was cleared
				var notOkCondition jen.Code
				if field.Nillable {
					notOkCondition = jen.Op("!").Id("ok").Op("&&").Op("!").Id(receiver).Dot("mutation").Dot(field.StructField() + "Cleared").Call()
				} else {
					notOkCondition = jen.Op("!").Id("ok")
				}

				grp.If(condition, notOkCondition).BlockFunc(func(blk *jen.Group) {
					// Check for nil update default function when runtimeRequired
					blk.If(jen.Qual(h.EntityPkgPath(t), field.UpdateDefaultName()).Op("==").Nil()).Block(
						jen.Return(jen.Qual("fmt", "Errorf").Call(
							jen.Lit(pkg + ": uninitialized " + t.Package() + "." + field.UpdateDefaultName() + " (forgotten import " + pkg + "/runtime?)"),
						)),
					)
					// v := {pkg}.{UpdateDefaultName}()
					blk.Id("v").Op(":=").Qual(h.EntityPkgPath(t), field.UpdateDefaultName()).Call()
					// u.mutation.{MutationSet}(v)
					blk.Id(receiver).Dot("mutation").Dot(field.MutationSet()).Call(jen.Id("v"))
				})
			}
			grp.Return(jen.Nil())
		})
	} else {
		// No return when no hooks/policies
		f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id("defaults").Params().BlockFunc(func(grp *jen.Group) {
			for _, field := range t.Fields {
				if !field.UpdateDefault {
					continue
				}
				// if _, ok := u.mutation.{MutationGet}(); !ok && !u.mutation.{StructField}Cleared() { ... set default ... }
				condition := jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id(receiver).Dot("mutation").Dot(field.MutationGet()).Call()

				// For nillable fields, also check if field was cleared
				var notOkCondition jen.Code
				if field.Nillable {
					notOkCondition = jen.Op("!").Id("ok").Op("&&").Op("!").Id(receiver).Dot("mutation").Dot(field.StructField() + "Cleared").Call()
				} else {
					notOkCondition = jen.Op("!").Id("ok")
				}

				grp.If(condition, notOkCondition).Block(
					// v := {pkg}.{UpdateDefaultName}()
					jen.Id("v").Op(":=").Qual(h.EntityPkgPath(t), field.UpdateDefaultName()).Call(),
					// u.mutation.{MutationSet}(v)
					jen.Id(receiver).Dot("mutation").Dot(field.MutationSet()).Call(jen.Id("v")),
				)
			}
		})
	}
}

// genUpdateCheck generates the check method for the Update/UpdateOne builder.
func genUpdateCheck(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName string, receiver string) {
	// Check if validators feature is enabled
	validatorsEnabled, _ := h.Graph().Config.FeatureEnabled(gen.FeatureValidator.Name)

	f.Comment("check runs all checks and user-defined validators on the builder.")
	f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id("check").Params().Error().BlockFunc(func(grp *jen.Group) {
		// Validate mutable fields that have validators (not immutable)
		// Per Ent template: {{ range $f := $.Fields }} with {{ with and (not $f.Immutable) (or $f.Validators $f.IsEnum $isValidator) }}
		for _, field := range t.Fields {
			// Skip immutable fields
			if field.Immutable {
				continue
			}

			// Per Ent template: $isValidator := and ($f.HasGoType) ($f.Type.Validator)
			isValidator := field.HasGoType() && field.Type != nil && field.Type.Validator()

			// Enter validation block if: (validatorsEnabled && (Validators > 0 || IsEnum)) OR (HasGoType && Type.Validator)
			if (!validatorsEnabled || (field.Validators == 0 && !field.IsEnum())) && !isValidator {
				continue
			}

			// if v, ok := u.mutation.{MutationGet}(); ok { ... validate ... }
			grp.If(
				jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Id(receiver).Dot("mutation").Dot(field.MutationGet()).Call(),
				jen.Id("ok"),
			).BlockFunc(func(blk *jen.Group) {
				// Per template: if or $f.Validators $f.IsEnum => call {pkg}.{Validator}(v)
				// else => call v.Validate()
				var validationCall *jen.Statement
				if validatorsEnabled && (field.Validators > 0 || field.IsEnum()) {
					validationCall = jen.Qual(h.EntityPkgPath(t), field.Validator()).Call(jen.Id("v"))
				} else {
					// HasGoType && Type.Validator case: call v.Validate()
					validationCall = jen.Id("v").Dot("Validate").Call()
				}
				blk.If(
					jen.Id("err").Op(":=").Add(validationCall),
					jen.Id("err").Op("!=").Nil(),
				).Block(
					jen.Return(jen.Op("&").Id("ValidationError").Values(jen.Dict{
						jen.Id("Name"): jen.Lit(field.Name),
						jen.Id("err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit("validator failed for field \""+t.Name+"."+field.Name+"\": %w"), jen.Id("err")),
					})),
				)
			})
		}

		// Check required unique edges - clearing a required edge without replacement is not allowed
		for _, edge := range t.Edges {
			if edge.Unique && !edge.Optional {
				// if u.mutation.{StructField}Cleared() && len(u.mutation.{StructField}IDs()) == 0 { ... error ... }
				grp.If(
					jen.Id(receiver).Dot("mutation").Dot(edge.StructField() + "Cleared").Call().Op("&&").
						Len(jen.Id(receiver).Dot("mutation").Dot(edge.StructField() + "IDs").Call()).Op("==").Lit(0),
				).Block(
					jen.Return(jen.Qual("errors", "New").Call(jen.Lit("clearing a required unique edge \"" + t.Name + "." + edge.Name + "\""))),
				)
			}
		}

		grp.Return(jen.Nil())
	})
}

// genUpdateSQLSave generates the sqlSave method for Update/UpdateOne builders.
// isOne: true for UpdateOne (returns *Entity), false for Update (returns int)
func genUpdateSQLSave(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName string, isOne bool, modifierEnabled bool, receiver string) {
	pkg := t.PackageDir()

	// Return type differs: UpdateOne returns (*Entity, error), Update returns (int, error)
	var returnType jen.Code
	var zeroVal jen.Code
	if isOne {
		returnType = jen.Params(jen.Id("_node").Op("*").Id(t.Name), jen.Id("err").Error())
		zeroVal = jen.Nil()
	} else {
		returnType = jen.Params(jen.Id("_node").Int(), jen.Id("err").Error())
		zeroVal = jen.Lit(0)
	}

	f.Func().Params(jen.Id(receiver).Op("*").Id(builderName)).Id("sqlSave").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Add(returnType).BlockFunc(func(grp *jen.Group) {
		// Call check() if there are update checkers
		if t.HasUpdateCheckers() {
			grp.If(jen.Id("err").Op(":=").Id(receiver).Dot("check").Call(), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(zeroVal, jen.Id("err")),
			)
		}

		// Build the UpdateSpec
		// _spec := sqlgraph.NewUpdateSpec({pkg}.Table, {pkg}.Columns, sqlgraph.NewFieldSpec({pkg}.{ID.Constant}, field.{ID.Type.ConstName}))
		if t.HasOneFieldID() {
			grp.Id("_spec").Op(":=").Qual(h.SQLGraphPkg(), "NewUpdateSpec").Call(
				jen.Qual(h.EntityPkgPath(t), "Table"),
				jen.Qual(h.EntityPkgPath(t), "Columns"),
				jen.Qual(h.SQLGraphPkg(), "NewFieldSpec").Call(
					jen.Qual(h.EntityPkgPath(t), t.ID.Constant()),
					jen.Qual(schemaPkg(), t.ID.Type.ConstName()),
				),
			)
		} else {
			// Composite ID - generate multiple field specs
			var idSpecs []jen.Code
			for _, id := range t.EdgeSchema.ID {
				idSpecs = append(idSpecs, jen.Qual(h.SQLGraphPkg(), "NewFieldSpec").Call(
					jen.Qual(h.EntityPkgPath(t), id.Constant()),
					jen.Qual(schemaPkg(), id.Type.ConstName()),
				))
			}
			grp.Id("_spec").Op(":=").Qual(h.SQLGraphPkg(), "NewUpdateSpec").Call(
				jen.Qual(h.EntityPkgPath(t), "Table"),
				jen.Qual(h.EntityPkgPath(t), "Columns"),
				jen.List(idSpecs...),
			)
		}

		// For UpdateOne: validate ID and handle field selection
		if isOne {
			if t.HasOneFieldID() {
				// Single field ID
				// Get ID from mutation
				grp.List(jen.Id("id"), jen.Id("ok")).Op(":=").Id(receiver).Dot("mutation").Dot(t.ID.MutationGet()).Call()
				grp.If(jen.Op("!").Id("ok")).Block(
					jen.Return(zeroVal, jen.Op("&").Id("ValidationError").Values(jen.Dict{
						jen.Id("Name"): jen.Lit(t.ID.Name),
						jen.Id("err"):  jen.Qual("errors", "New").Call(jen.Lit(pkg + `: missing "` + t.Name + "." + t.ID.Name + `" for update`)),
					})),
				)
				grp.Id("_spec").Dot("Node").Dot("ID").Dot("Value").Op("=").Id("id")

				// Handle field selection for single ID
				grp.If(jen.Id("fields").Op(":=").Id(receiver).Dot("fields"), jen.Len(jen.Id("fields")).Op(">").Lit(0)).Block(
					jen.Id("_spec").Dot("Node").Dot("Columns").Op("=").Make(jen.Index().String(), jen.Lit(0), jen.Len(jen.Id("fields"))),
					jen.Id("_spec").Dot("Node").Dot("Columns").Op("=").Append(
						jen.Id("_spec").Dot("Node").Dot("Columns"),
						jen.Qual(h.EntityPkgPath(t), t.ID.Constant()),
					),
					jen.For(jen.List(jen.Id("_"), jen.Id("f")).Op(":=").Range().Id("fields")).Block(
						jen.If(jen.Op("!").Qual(h.EntityPkgPath(t), "ValidColumn").Call(jen.Id("f"))).Block(
							jen.Return(jen.Nil(), jen.Op("&").Id("ValidationError").Values(jen.Dict{
								jen.Id("Name"): jen.Id("f"),
								jen.Id("err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit(pkg+`: invalid field %q for query`), jen.Id("f")),
							})),
						),
						jen.If(jen.Id("f").Op("!=").Qual(h.EntityPkgPath(t), t.ID.Constant())).Block(
							jen.Id("_spec").Dot("Node").Dot("Columns").Op("=").Append(
								jen.Id("_spec").Dot("Node").Dot("Columns"),
								jen.Id("f"),
							),
						),
					),
				)
			} else {
				// Composite ID - validate each ID field
				for i, id := range t.EdgeSchema.ID {
					grp.If(
						jen.List(jen.Id("id"), jen.Id("ok")).Op(":=").Id(receiver).Dot("mutation").Dot(id.MutationGet()).Call(),
						jen.Op("!").Id("ok"),
					).Block(
						jen.Return(zeroVal, jen.Op("&").Id("ValidationError").Values(jen.Dict{
							jen.Id("Name"): jen.Lit(id.Name),
							jen.Id("err"):  jen.Qual("errors", "New").Call(jen.Lit(pkg + `: missing "` + t.Name + "." + id.Name + `" for update`)),
						})),
					).Else().Block(
						jen.Id("_spec").Dot("Node").Dot("CompositeID").Index(jen.Lit(i)).Dot("Value").Op("=").Id("id"),
					)
				}

				// Handle field selection for composite ID
				grp.If(jen.Id("fields").Op(":=").Id(receiver).Dot("fields"), jen.Len(jen.Id("fields")).Op(">").Lit(0)).Block(
					jen.Id("_spec").Dot("Node").Dot("Columns").Op("=").Make(jen.Index().String(), jen.Len(jen.Id("fields"))),
					jen.For(jen.List(jen.Id("i"), jen.Id("f")).Op(":=").Range().Id("fields")).Block(
						jen.If(jen.Op("!").Qual(h.EntityPkgPath(t), "ValidColumn").Call(jen.Id("f"))).Block(
							jen.Return(jen.Nil(), jen.Op("&").Id("ValidationError").Values(jen.Dict{
								jen.Id("Name"): jen.Id("f"),
								jen.Id("err"):  jen.Qual("fmt", "Errorf").Call(jen.Lit(pkg+`: invalid field %q for query`), jen.Id("f")),
							})),
						),
						jen.Id("_spec").Dot("Node").Dot("Columns").Index(jen.Id("i")).Op("=").Id("f"),
					),
				)
			}
		}

		// Set up predicates from mutation
		grp.If(jen.Id("ps").Op(":=").Id(receiver).Dot("mutation").Dot("predicates"), jen.Len(jen.Id("ps")).Op(">").Lit(0)).Block(
			jen.Id("_spec").Dot("Predicate").Op("=").Func().Params(jen.Id("selector").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
				jen.For(jen.Id("i").Op(":=").Range().Id("ps")).Block(
					jen.Id("ps").Index(jen.Id("i")).Call(jen.Id("selector")),
				),
			),
		)

		// Handle field updates
		for _, field := range t.MutationFields() {
			// Skip immutable fields unless they have UpdateDefault
			if field.Immutable && !field.UpdateDefault {
				continue
			}

			// SetField - with HasValueScanner support for custom type conversion
			if field.HasValueScanner() {
				valueFn, _ := field.ValueFunc()
				grp.If(
					jen.List(jen.Id("value"), jen.Id("ok")).Op(":=").Id(receiver).Dot("mutation").Dot(field.MutationGet()).Call(),
					jen.Id("ok"),
				).BlockFunc(func(setBlock *jen.Group) {
					setBlock.List(jen.Id("vv"), jen.Id("err")).Op(":=").Id(valueFn).Call(jen.Id("value"))
					setBlock.If(jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(zeroVal, jen.Id("err")),
					)
					setBlock.Id("_spec").Dot("SetField").Call(
						jen.Qual(h.EntityPkgPath(t), field.Constant()),
						jen.Qual(schemaPkg(), field.Type.ConstName()),
						jen.Id("vv"),
					)
				})
			} else {
				grp.If(
					jen.List(jen.Id("value"), jen.Id("ok")).Op(":=").Id(receiver).Dot("mutation").Dot(field.MutationGet()).Call(),
					jen.Id("ok"),
				).Block(
					jen.Id("_spec").Dot("SetField").Call(
						jen.Qual(h.EntityPkgPath(t), field.Constant()),
						jen.Qual(schemaPkg(), field.Type.ConstName()),
						jen.Id("value"),
					),
				)
			}

			// AddField for numeric fields - also with HasValueScanner support
			if field.SupportsMutationAdd() {
				if field.HasValueScanner() {
					valueFn, _ := field.ValueFunc()
					grp.If(
						jen.List(jen.Id("value"), jen.Id("ok")).Op(":=").Id(receiver).Dot("mutation").Dot(field.MutationAdded()).Call(),
						jen.Id("ok"),
					).BlockFunc(func(addBlock *jen.Group) {
						addBlock.List(jen.Id("vv"), jen.Id("err")).Op(":=").Id(valueFn).Call(jen.Id("value"))
						addBlock.If(jen.Id("err").Op("!=").Nil()).Block(
							jen.Return(zeroVal, jen.Id("err")),
						)
						addBlock.Id("_spec").Dot("AddField").Call(
							jen.Qual(h.EntityPkgPath(t), field.Constant()),
							jen.Qual(schemaPkg(), field.Type.ConstName()),
							jen.Id("vv"),
						)
					})
				} else {
					grp.If(
						jen.List(jen.Id("value"), jen.Id("ok")).Op(":=").Id(receiver).Dot("mutation").Dot(field.MutationAdded()).Call(),
						jen.Id("ok"),
					).Block(
						jen.Id("_spec").Dot("AddField").Call(
							jen.Qual(h.EntityPkgPath(t), field.Constant()),
							jen.Qual(schemaPkg(), field.Type.ConstName()),
							jen.Id("value"),
						),
					)
				}
			}

			// AppendField for JSON arrays
			if field.SupportsMutationAppend() {
				grp.If(
					jen.List(jen.Id("value"), jen.Id("ok")).Op(":=").Id(receiver).Dot("mutation").Dot(field.MutationAppended()).Call(),
					jen.Id("ok"),
				).Block(
					jen.Id("_spec").Dot("AddModifier").Call(
						jen.Func().Params(jen.Id("u").Op("*").Qual(h.SQLPkg(), "UpdateBuilder")).Block(
							jen.Qual("github.com/syssam/velox/dialect/sql/sqljson", "Append").Call(
								jen.Id("u"),
								jen.Qual(h.EntityPkgPath(t), field.Constant()),
								jen.Id("value"),
							),
						),
					),
				)
			}

			// ClearField for nillable fields (only nillable fields can be set to NULL)
			if field.Nillable {
				grp.If(jen.Id(receiver).Dot("mutation").Dot(field.StructField() + "Cleared").Call()).Block(
					jen.Id("_spec").Dot("ClearField").Call(
						jen.Qual(h.EntityPkgPath(t), field.Constant()),
						jen.Qual(schemaPkg(), field.Type.ConstName()),
					),
				)
			}
		}

		// Handle edge updates
		for _, edge := range t.EdgesWithID() {
			if edge.Immutable {
				continue
			}

			// Clear edge
			grp.If(jen.Id(receiver).Dot("mutation").Dot(edge.MutationCleared()).Call()).BlockFunc(func(clearBlock *jen.Group) {
				genEdgeSpec(h, clearBlock, t, edge, false, receiver)
				clearBlock.Id("_spec").Dot("Edges").Dot("Clear").Op("=").Append(
					jen.Id("_spec").Dot("Edges").Dot("Clear"),
					jen.Id("edge"),
				)
			})

			// For non-unique edges: remove specific IDs
			if !edge.Unique {
				grp.If(
					jen.Id("nodes").Op(":=").Id(receiver).Dot("mutation").Dot("Removed"+edge.StructField()+"IDs").Call(),
					jen.Len(jen.Id("nodes")).Op(">").Lit(0).Op("&&").Op("!").Id(receiver).Dot("mutation").Dot(edge.MutationCleared()).Call(),
				).BlockFunc(func(removeBlock *jen.Group) {
					genEdgeSpec(h, removeBlock, t, edge, true, receiver)
					removeBlock.Id("_spec").Dot("Edges").Dot("Clear").Op("=").Append(
						jen.Id("_spec").Dot("Edges").Dot("Clear"),
						jen.Id("edge"),
					)
				})
			}

			// Add edges
			grp.If(
				jen.Id("nodes").Op(":=").Id(receiver).Dot("mutation").Dot(edge.StructField()+"IDs").Call(),
				jen.Len(jen.Id("nodes")).Op(">").Lit(0),
			).BlockFunc(func(addBlock *jen.Group) {
				genEdgeSpec(h, addBlock, t, edge, true, receiver)
				addBlock.Id("_spec").Dot("Edges").Dot("Add").Op("=").Append(
					jen.Id("_spec").Dot("Edges").Dot("Add"),
					jen.Id("edge"),
				)
			})
		}

		// Handle modifiers (sql/modifier feature)
		if modifierEnabled {
			grp.Id("_spec").Dot("AddModifiers").Call(jen.Id(receiver).Dot("modifiers").Op("..."))
		}

		// Execute the update
		if isOne {
			// UpdateOne: create node, set assign/scan, call UpdateNode
			grp.Id("_node").Op("=").Op("&").Id(t.Name).Values(jen.Dict{
				jen.Id("config"): jen.Id(receiver).Dot("config"),
			})
			grp.Id("_spec").Dot("Assign").Op("=").Id("_node").Dot("assignValues")
			grp.Id("_spec").Dot("ScanValues").Op("=").Id("_node").Dot("scanValues")
			grp.If(
				jen.Id("err").Op("=").Qual(h.SQLGraphPkg(), "UpdateNode").Call(
					jen.Id("ctx"),
					jen.Id(receiver).Dot("driver"),
					jen.Id("_spec"),
				),
				jen.Id("err").Op("!=").Nil(),
			).Block(
				jen.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("err").Op(".").Parens(jen.Op("*").Qual(h.SQLGraphPkg(), "NotFoundError")), jen.Id("ok")).Block(
					jen.Id("err").Op("=").Op("&").Id("NotFoundError").Values(jen.Qual(h.EntityPkgPath(t), "Label")),
				).Else().If(jen.Qual(h.SQLGraphPkg(), "IsConstraintError").Call(jen.Id("err"))).Block(
					jen.Id("err").Op("=").Op("&").Id("ConstraintError").Values(jen.Dict{
						jen.Id("msg"):  jen.Id("err").Dot("Error").Call(),
						jen.Id("wrap"): jen.Id("err"),
					}),
				),
				jen.Return(zeroVal, jen.Id("err")),
			)
		} else {
			// Update (batch): call UpdateNodes
			grp.If(
				jen.List(jen.Id("_node"), jen.Id("err")).Op("=").Qual(h.SQLGraphPkg(), "UpdateNodes").Call(
					jen.Id("ctx"),
					jen.Id(receiver).Dot("driver"),
					jen.Id("_spec"),
				),
				jen.Id("err").Op("!=").Nil(),
			).Block(
				jen.If(jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("err").Op(".").Parens(jen.Op("*").Qual(h.SQLGraphPkg(), "NotFoundError")), jen.Id("ok")).Block(
					jen.Id("err").Op("=").Op("&").Id("NotFoundError").Values(jen.Qual(h.EntityPkgPath(t), "Label")),
				).Else().If(jen.Qual(h.SQLGraphPkg(), "IsConstraintError").Call(jen.Id("err"))).Block(
					jen.Id("err").Op("=").Op("&").Id("ConstraintError").Values(jen.Dict{
						jen.Id("msg"):  jen.Id("err").Dot("Error").Call(),
						jen.Id("wrap"): jen.Id("err"),
					}),
				),
				jen.Return(zeroVal, jen.Id("err")),
			)
		}

		// Mark mutation as done
		grp.Id(receiver).Dot("mutation").Dot("done").Op("=").True()
		grp.Return(jen.Id("_node"), jen.Nil())
	})
}

// genEdgeSpec generates the edge spec for update operations.
func genEdgeSpec(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, edge *gen.Edge, withNodes bool, receiver string) {
	// Validate that edge target type has an ID field
	if edge.Type.ID == nil {
		panic(fmt.Sprintf("velox/gen: cannot generate edge spec for %q: related type %q has no ID field (view type?)", edge.Name, edge.Type.Name))
	}

	// edge := &sqlgraph.EdgeSpec{...}
	var columnsExpr jen.Code
	if edge.M2M() {
		columnsExpr = jen.Qual(h.EntityPkgPath(t), edge.PKConstant())
	} else {
		columnsExpr = jen.Index().String().Values(jen.Qual(h.EntityPkgPath(t), edge.ColumnConstant()))
	}

	grp.Id("edge").Op(":=").Op("&").Qual(h.SQLGraphPkg(), "EdgeSpec").Values(jen.Dict{
		jen.Id("Rel"):     jen.Qual(h.SQLGraphPkg(), edge.Rel.Type.String()),
		jen.Id("Inverse"): jen.Lit(edge.IsInverse()),
		jen.Id("Table"):   jen.Qual(h.EntityPkgPath(t), edge.TableConstant()),
		jen.Id("Columns"): columnsExpr,
		jen.Id("Bidi"):    jen.Lit(edge.Bidi),
		jen.Id("Target"): jen.Op("&").Qual(h.SQLGraphPkg(), "EdgeTarget").Values(jen.Dict{
			jen.Id("IDSpec"): jen.Qual(h.SQLGraphPkg(), "NewFieldSpec").Call(
				jen.Qual(h.EntityPkgPath(edge.Type), edge.Type.ID.Constant()),
				jen.Qual(schemaPkg(), edge.Type.ID.Type.ConstName()),
			),
		}),
	})

	// Add nodes if needed
	if withNodes {
		grp.For(jen.List(jen.Id("_"), jen.Id("k")).Op(":=").Range().Id("nodes")).Block(
			jen.Id("edge").Dot("Target").Dot("Nodes").Op("=").Append(
				jen.Id("edge").Dot("Target").Dot("Nodes"),
				jen.Id("k"),
			),
		)
	}

	// Handle Through edge with default values (M2M edges with join tables that have defaults)
	if edge.Through != nil && edge.Through.HasDefault() {
		// createE := &{Through}Create{config: u.config, mutation: new{Through}Mutation(u.config, OpCreate)}
		grp.Id("createE").Op(":=").Op("&").Id(edge.Through.CreateName()).Values(jen.Dict{
			jen.Id("config"):   jen.Id(receiver).Dot("config"),
			jen.Id("mutation"): jen.Id("new"+edge.Through.MutationName()).Call(jen.Id(receiver).Dot("config"), jen.Id("OpCreate")),
		})

		// Call defaults() - if there are hooks or policy, assign to blank identifier
		if edge.Through.NumHooks() > 0 || edge.Through.NumPolicy() > 0 {
			grp.Id("_").Op("=").Id("createE").Dot("defaults").Call()
		} else {
			grp.Id("createE").Dot("defaults").Call()
		}

		// _, specE := createE.createSpec()
		grp.List(jen.Id("_"), jen.Id("specE")).Op(":=").Id("createE").Dot("createSpec").Call()

		// edge.Target.Fields = specE.Fields
		grp.Id("edge").Dot("Target").Dot("Fields").Op("=").Id("specE").Dot("Fields")

		// If through type has one field ID with default, conditionally add it
		if edge.Through.HasOneFieldID() && edge.Through.ID.Default {
			grp.If(jen.Id("specE").Dot("ID").Dot("Value").Op("!=").Nil()).Block(
				jen.Id("edge").Dot("Target").Dot("Fields").Op("=").Append(
					jen.Id("edge").Dot("Target").Dot("Fields"),
					jen.Id("specE").Dot("ID"),
				),
			)
		}
	}
}
