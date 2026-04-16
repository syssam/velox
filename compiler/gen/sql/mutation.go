package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genMutation generates a mutation type with typed pointer fields, operation
// metadata, and a typed oldValue closure for UpdateOne operations. All mutation
// state lives directly on the generated struct — no embedded base type.
// Per-field typed Set/Get/Clear/Reset methods read and write typed fields
// directly. Edge state is fully typed per-edge on the mutation struct itself —
// matches Ent exactly.
func genMutation(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	f := h.NewFile(h.Pkg())

	mutName := t.MutationName()

	// Entity package path (e.g. "example.com/app/entity") for typed oldValue closure.
	entityReturnPkg := h.SharedEntityPkg()

	hasJSONField := false
	for _, fd := range t.Fields {
		if fd.IsJSON() {
			hasJSONField = true
			break
		}
	}
	hasMutableFields := len(t.MutableFields()) > 0

	// Mutation struct with typed pointer fields and operation metadata.
	f.Commentf("%s represents an operation that mutates the %s nodes in the graph.", mutName, t.Name)
	f.Type().Id(mutName).StructFunc(func(group *jen.Group) {
		group.Id("config").Qual(runtimePkg, "Config")
		group.Id("op").Qual(runtimePkg, "Op")
		group.Id("id").Op("*").Add(h.IDType(t))
		// Typed field values
		for _, fd := range t.Fields {
			if fd.IsEdgeField() && !fd.UserDefined {
				continue
			}
			group.Id("_" + fd.Name).Op("*").Add(h.BaseType(fd))
			if fd.SupportsMutationAdd() {
				group.Id("_add" + fd.Name).Op("*").Add(h.BaseType(fd))
			}
		}
		group.Id("clearedFields").Map(jen.String()).Struct()
		if hasJSONField {
			group.Id("appends").Map(jen.String()).Any()
		}
		if hasMutableFields {
			group.Id("oldValue").Func().Params(jen.Qual("context", "Context")).Params(jen.Op("*").Qual(entityReturnPkg, t.Name), jen.Error())
			group.Id("oldLoaded").Bool()
			group.Id("oldCache").Op("*").Qual(entityReturnPkg, t.Name)
		}
		// Typed edge state (matches Ent layout exactly)
		for _, edge := range t.EdgesWithID() {
			idType := h.IDType(edge.Type)
			group.Id(edge.BuilderField()).Map(idType).Struct()
			group.Id("removed" + edge.StructField()).Map(idType).Struct()
			group.Id("cleared" + edge.StructField()).Bool()
		}
		group.Id("predicates").Index().Add(h.PredicateType(t))
	})

	// Interface assertion - compile-time check that mutation implements ent.Mutation
	f.Var().Id("_").Qual(h.VeloxPkg(), "Mutation").Op("=").Parens(jen.Op("*").Id(mutName)).Call(jen.Nil())

	// Option type (unexported, internal use only)
	f.Commentf("%s allows management of the mutation configuration using functional options.", t.MutationOptionName())
	f.Type().Id(t.MutationOptionName()).Func().Params(jen.Op("*").Id(mutName))

	// Constructor
	f.Commentf("New%s creates new mutation for the %s entity.", mutName, t.Name)
	f.Func().Id("New"+mutName).Params(
		jen.Id("c").Qual(runtimePkg, "Config"),
		jen.Id("op").Qual(runtimePkg, "Op"),
		jen.Id("opts").Op("...").Id(t.MutationOptionName()),
	).Op("*").Id(mutName).BlockFunc(func(body *jen.Group) {
		body.Id("m").Op(":=").Op("&").Id(mutName).Values(jen.Dict{
			jen.Id("config"): jen.Id("c"),
			jen.Id("op"):     jen.Qual(runtimePkg, "Op").Call(jen.Id("op")),
		})
		body.For(jen.List(jen.Id("_"), jen.Id("opt")).Op(":=").Range().Id("opts")).Block(
			jen.Id("opt").Call(jen.Id("m")),
		)
		body.Return(jen.Id("m"))
	})

	// Op/SetOp — mutation operation accessors.
	f.Comment("Op returns the operation name.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("Op").Params().Qual(runtimePkg, "Op").Block(
		jen.Return(jen.Id("m").Dot("op")),
	)
	f.Comment("SetOp allows setting the mutation operation.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("SetOp").Params(
		jen.Id("op").Qual(runtimePkg, "Op"),
	).Block(
		jen.Id("m").Dot("op").Op("=").Id("op"),
	)

	// Type — schema type literal, no storage needed.
	f.Commentf("Type returns the schema type name of this mutation (%q).", t.Name)
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("Type").Params().String().Block(
		jen.Return(jen.Lit(t.Name)),
	)

	// ID/SetID — typed pointer to entity ID.
	f.Comment("ID returns the ID value in the mutation, if it was provided by the caller.")
	f.Comment("The second bool return indicates whether the ID field was set.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("ID").Params().Params(
		jen.Id("id").Any(), jen.Id("exists").Bool(),
	).Block(
		jen.If(jen.Id("m").Dot("id").Op("==").Nil()).Block(
			jen.Return(),
		),
		jen.Return(jen.Op("*").Id("m").Dot("id"), jen.True()),
	)
	f.Comment("SetID sets the entity ID value on the mutation.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("SetID").Params(
		jen.Id("id").Add(h.IDType(t)),
	).Block(
		jen.Id("m").Dot("id").Op("=").Op("&").Id("id"),
	)

	// loadOld — internal helper that lazily loads the old entity value for UpdateOne.
	if hasMutableFields {
		f.Comment("loadOld lazily loads the pre-mutation entity state for OldXxx methods.")
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("loadOld").Params(
			jen.Id("ctx").Qual("context", "Context"),
		).Params(jen.Op("*").Qual(entityReturnPkg, t.Name), jen.Error()).Block(
			jen.If(jen.Id("m").Dot("op").Op("!=").Qual(runtimePkg, "OpUpdateOne")).Block(
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("OldField is only allowed on UpdateOne operations"))),
			),
			jen.If(jen.Id("m").Dot("oldValue").Op("==").Nil()).Block(
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("OldField is not supported: no old value loader configured"))),
			),
			jen.If(jen.Id("m").Dot("oldLoaded")).Block(
				jen.Return(jen.Id("m").Dot("oldCache"), jen.Nil()),
			),
			jen.List(jen.Id("old"), jen.Id("err")).Op(":=").Id("m").Dot("oldValue").Call(jen.Id("ctx")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("querying old values: %w"), jen.Id("err"))),
			),
			jen.Id("m").Dot("oldCache").Op("=").Id("old"),
			jen.Id("m").Dot("oldLoaded").Op("=").True(),
			jen.Return(jen.Id("old"), jen.Nil()),
		)
	}

	// Where appends predicates
	f.Commentf("Where appends a list predicates to the %s builder.", mutName)
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("Where").Params(
		jen.Id("ps").Op("...").Add(h.PredicateType(t)),
	).Block(
		jen.Id("m").Dot("predicates").Op("=").Append(jen.Id("m").Dot("predicates"), jen.Id("ps").Op("...")),
	)

	// PredicatesFuncs returns predicates as untyped selector functions.
	// Used by root wrappers that can't access the unexported predicates field.
	f.Commentf("PredicatesFuncs returns the predicates as untyped selector functions.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("PredicatesFuncs").Params().Index().Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector")).BlockFunc(func(grp *jen.Group) {
		grp.Id("ps").Op(":=").Make(jen.Index().Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector")), jen.Len(jen.Id("m").Dot("predicates")))
		grp.For(jen.List(jen.Id("i"), jen.Id("p")).Op(":=").Range().Id("m").Dot("predicates")).Block(
			jen.Id("ps").Index(jen.Id("i")).Op("=").Id("p"),
		)
		grp.Return(jen.Id("ps"))
	})

	// AddPredicate + Filter — gated by FeaturePrivacy.
	// Together they make *XxxMutation implement runtime.PredicateAdder and
	// privacy.Filterable, so privacy.FilterFunc rules can inject WHERE
	// clauses on update/delete mutations the same way they do on queries.
	// Without this, every FilterFunc evaluated against a mutation returns
	// privacy.Deny — pinned by TestMutationImplementsFilterable.
	if h.FeatureEnabled(gen.FeaturePrivacy.Name) {
		const privacyPkgPath = "github.com/syssam/velox/privacy"

		f.Comment("AddPredicate appends a raw SQL-level predicate to the mutation.")
		f.Comment("Satisfies runtime.PredicateAdder so privacy filters can write")
		f.Comment("predicates through this method rather than touching internal state.")
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("AddPredicate").Params(
			jen.Id("p").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector")),
		).Block(
			jen.Id("m").Dot("predicates").Op("=").Append(
				jen.Id("m").Dot("predicates"),
				jen.Add(h.PredicateType(t)).Parens(jen.Id("p")),
			),
		)

		f.Commentf("Filter returns a %sFilter that writes predicates through this mutation.", t.Name)
		f.Comment("Implements privacy.Filterable so FilterFunc-based mutation rules")
		f.Comment("can inject WHERE clauses without knowing the concrete mutation type.")
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("Filter").Params().Qual(privacyPkgPath, "Filter").Block(
			jen.Return(jen.Id("New"+t.Name+"Filter").Call(
				jen.Id("m").Dot("config"),
				jen.Id("m"),
			)),
		)
	}

	// Per-field typed Set/Get/Clear/Reset
	for _, fd := range t.Fields {
		// Skip non-user-defined edge fields (they're handled by edge setters).
		// User-defined edge FK fields should be generated as regular fields.
		if fd.IsEdgeField() && !fd.UserDefined {
			continue
		}
		genMutationField(h, f, mutName, t, fd)
	}

	// Edge methods
	for _, edge := range t.EdgesWithID() {
		genMutationEdge(h, f, mutName, t, edge)
	}

	// SetField and Field (generic field accessors for hooks/interceptors)
	genMutationFieldAccessors(h, f, mutName, t)

	return f
}

// genMutationField generates typed Set/Get/Clear/Reset methods for a field.
// All methods read and write typed pointer fields directly — no dual-write.
func genMutationField(h gen.GeneratorHelper, f *jen.File, mutName string, t *gen.Type, fd *gen.Field) {
	fieldPascal := fd.StructField()
	column := fd.Name
	typedField := "_" + column  // e.g., _name, _age
	addField := "_add" + column // e.g., _addage

	// Check if field name conflicts with the mutation's own interface methods.
	// Fields like "type" and "op" would shadow the generated Type()/Op() methods.
	conflictsWithInterface := fieldPascal == "Type" || fieldPascal == "Op"

	// SetXxx — writes the typed pointer field.
	f.Commentf("Set%s sets the %q field.", fieldPascal, column)
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("Set" + fieldPascal).Params(
		jen.Id("v").Add(h.BaseType(fd)),
	).Block(
		jen.Id("m").Dot(typedField).Op("=").Op("&").Id("v"),
	)

	// Xxx returns the field value and whether it was set.
	// Reads from typed field — no type assertion, no panic risk.
	getterName := fieldPascal
	if conflictsWithInterface {
		getterName = "Get" + fieldPascal
	}
	comment := "%s returns the value of the %q field in the mutation."
	if conflictsWithInterface {
		comment = "Get%s returns the value of the %q field in the mutation."
	}
	f.Commentf(comment, getterName, column)
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(getterName).Params().Params(
		jen.Id("r").Add(h.BaseType(fd)), jen.Id("exists").Bool(),
	).Block(
		jen.If(jen.Id("m").Dot(typedField).Op("==").Nil()).Block(
			jen.Return(),
		),
		jen.Return(jen.Op("*").Id("m").Dot(typedField), jen.True()),
	)

	// AddXxx for numeric types
	if fd.SupportsMutationAdd() {
		f.Commentf("Add%s adds v to the %q field.", fieldPascal, column)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("Add" + fieldPascal).Params(
			jen.Id("v").Add(h.BaseType(fd)),
		).Block(
			jen.If(jen.Id("m").Dot(addField).Op("!=").Nil()).Block(
				jen.Op("*").Id("m").Dot(addField).Op("+=").Id("v"),
			).Else().Block(
				jen.Id("m").Dot(addField).Op("=").Op("&").Id("v"),
			),
		)

		f.Commentf("Added%s returns the value that was added to the %q field in this mutation.", fieldPascal, column)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("Added"+fieldPascal).Params().Params(
			jen.Id("r").Add(h.BaseType(fd)), jen.Id("exists").Bool(),
		).Block(
			jen.If(jen.Id("m").Dot(addField).Op("==").Nil()).Block(
				jen.Return(),
			),
			jen.Return(jen.Op("*").Id("m").Dot(addField), jen.True()),
		)
	}

	// AppendXxx for JSON slice fields
	if fd.IsJSON() {
		f.Commentf("Append%s appends v to the %q field.", fieldPascal, column)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("Append"+fieldPascal).Params(
			jen.Id("v").Add(h.BaseType(fd)),
		).Block(
			jen.If(jen.Id("m").Dot("appends").Op("==").Nil()).Block(
				jen.Id("m").Dot("appends").Op("=").Make(jen.Map(jen.String()).Any()),
			),
			jen.Id("m").Dot("appends").Index(jen.Lit(column)).Op("=").Id("v"),
		)
	}

	// ClearXxx for nillable fields only.
	// Optional (non-nillable) fields have NOT NULL columns — calling Clear would
	// attempt SET col = NULL which violates the DB constraint. Users should call
	// SetXxx(zeroValue) to reset optional non-nillable fields instead.
	if fd.Nillable {
		f.Commentf("Clear%s clears the value of the %q field.", fieldPascal, column)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("Clear" + fieldPascal).Params().BlockFunc(func(body *jen.Group) {
			body.Id("m").Dot(typedField).Op("=").Nil()
			if fd.SupportsMutationAdd() {
				body.Id("m").Dot(addField).Op("=").Nil()
			}
			body.If(jen.Id("m").Dot("clearedFields").Op("==").Nil()).Block(
				jen.Id("m").Dot("clearedFields").Op("=").Make(jen.Map(jen.String()).Struct()),
			)
			body.Id("m").Dot("clearedFields").Index(jen.Lit(column)).Op("=").Struct().Values()
		})

		f.Commentf("%sCleared returns if the %q field was cleared in this mutation.", fieldPascal, column)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(fieldPascal+"Cleared").Params().Bool().Block(
			jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("m").Dot("clearedFields").Index(jen.Lit(column)),
			jen.Return(jen.Id("ok")),
		)
	}

	// ResetXxx — clears the typed field pointer and any pending increment/clear.
	f.Commentf("Reset%s resets all changes to the %q field.", fieldPascal, column)
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("Reset" + fieldPascal).Params().BlockFunc(func(body *jen.Group) {
		body.Id("m").Dot(typedField).Op("=").Nil()
		if fd.SupportsMutationAdd() {
			body.Id("m").Dot(addField).Op("=").Nil()
		}
		body.Delete(jen.Id("m").Dot("clearedFields"), jen.Lit(column))
	})
}

// genMutationOldField generates a typed OldXxx(ctx) method that returns the
// pre-mutation value of a field. The value is lazy-loaded via the typed
// oldValue closure set on the mutation by the UpdateOne Save builder.
func genMutationOldField(h gen.GeneratorHelper, f *jen.File, mutName string, t *gen.Type, fd *gen.Field) {
	_ = t
	fieldPascal := fd.StructField()
	column := fd.Name

	f.Commentf("Old%s returns the old %q field value, if exists.", fieldPascal, column)
	f.Comment("An error is returned if the mutation operation is not UpdateOne, or the database query fails.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("Old"+fieldPascal).Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Id("v").Add(h.BaseType(fd)), jen.Id("err").Error()).BlockFunc(func(body *jen.Group) {
		body.List(jen.Id("old"), jen.Id("loadErr")).Op(":=").Id("m").Dot("loadOld").Call(jen.Id("ctx"))
		body.If(jen.Id("loadErr").Op("!=").Nil()).Block(
			jen.Id("err").Op("=").Id("loadErr"),
			jen.Return(),
		)
		if fd.NillableValue() {
			// Entity field is a pointer; deref if non-nil, otherwise return zero value.
			body.If(jen.Id("old").Dot(fieldPascal).Op("!=").Nil()).Block(
				jen.Id("v").Op("=").Op("*").Id("old").Dot(fieldPascal),
			)
		} else {
			body.Id("v").Op("=").Id("old").Dot(fieldPascal)
		}
		body.Return()
	})
}

// genMutationEdge generates typed edge methods for the mutation. Edge state
// lives on the mutation struct as typed map/bool fields (matches Ent).
func genMutationEdge(h gen.GeneratorHelper, f *jen.File, mutName string, t *gen.Type, edge *gen.Edge) {
	// For unique edges with user-defined FK field, skip Set/Clear methods
	// (the field setter handles it), but still generate XxxIDs getter
	// (needed by check() in create builders).
	skipSetters := edge.Unique && edge.Field() != nil && edge.Field().UserDefined

	edgeName := edge.Name
	idType := h.IDType(edge.Type)
	fieldName := edge.BuilderField()                   // e.g. "posts"
	removedFieldName := "removed" + edge.StructField() // e.g. "removedPosts"
	clearedFieldName := "cleared" + edge.StructField() // e.g. "clearedPosts"

	if !skipSetters && edge.Unique {
		// SetXxxID — unique edge stores single ID in the typed map.
		setMethod := edge.MutationSet()
		f.Commentf("%s sets the %q edge to the %s entity by id.", setMethod, edgeName, edge.Type.Name)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(setMethod).Params(
			jen.Id("id").Add(idType),
		).Block(
			jen.If(jen.Id("m").Dot(fieldName).Op("==").Nil()).Block(
				jen.Id("m").Dot(fieldName).Op("=").Make(jen.Map(idType).Struct()),
			),
			jen.Id("m").Dot(fieldName).Index(jen.Id("id")).Op("=").Struct().Values(),
		)

		// ClearXxx
		clearMethod := edge.MutationClear()
		f.Commentf("%s clears the %q edge.", clearMethod, edgeName)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(clearMethod).Params().Block(
			jen.Id("m").Dot(clearedFieldName).Op("=").True(),
		)

		// XxxCleared
		clearedMethod := edge.MutationCleared()
		f.Commentf("%s reports if the %q edge was cleared.", clearedMethod, edgeName)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(clearedMethod).Params().Bool().Block(
			jen.Return(jen.Id("m").Dot(clearedFieldName)),
		)
	} else if !skipSetters {
		// AddXxxIDs — append IDs to the typed map.
		addMethod := edge.MutationAdd()
		f.Commentf("%s adds the %q edge to the %s entity by ids.", addMethod, edgeName, edge.Type.Name)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(addMethod).Params(
			jen.Id("ids").Op("...").Add(idType),
		).Block(
			jen.If(jen.Id("m").Dot(fieldName).Op("==").Nil()).Block(
				jen.Id("m").Dot(fieldName).Op("=").Make(jen.Map(idType).Struct()),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("id")).Op(":=").Range().Id("ids")).Block(
				jen.Id("m").Dot(fieldName).Index(jen.Id("id")).Op("=").Struct().Values(),
			),
		)

		// RemoveXxxIDs — delete from added map, mark in removed map.
		removeMethod := edge.MutationRemove()
		f.Commentf("%s removes the %q edge to the %s entity by ids.", removeMethod, edgeName, edge.Type.Name)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(removeMethod).Params(
			jen.Id("ids").Op("...").Add(idType),
		).Block(
			jen.If(jen.Id("m").Dot(removedFieldName).Op("==").Nil()).Block(
				jen.Id("m").Dot(removedFieldName).Op("=").Make(jen.Map(idType).Struct()),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("id")).Op(":=").Range().Id("ids")).Block(
				jen.Delete(jen.Id("m").Dot(fieldName), jen.Id("id")),
				jen.Id("m").Dot(removedFieldName).Index(jen.Id("id")).Op("=").Struct().Values(),
			),
		)

		// ClearXxx
		clearMethod := edge.MutationClear()
		f.Commentf("%s clears the %q edge.", clearMethod, edgeName)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(clearMethod).Params().Block(
			jen.Id("m").Dot(clearedFieldName).Op("=").True(),
		)

		// XxxCleared reports whether the edge was cleared.
		clearedMethod := edge.MutationCleared()
		f.Commentf("%s reports if the %q edge was cleared.", clearedMethod, edgeName)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(clearedMethod).Params().Bool().Block(
			jen.Return(jen.Id("m").Dot(clearedFieldName)),
		)

		// RemovedXxxIDs returns the removed edge IDs (Ent parity).
		removedIDsMethod := "Removed" + edge.StructField() + "IDs"
		f.Commentf("%s returns the removed IDs of the %q edge.", removedIDsMethod, edgeName)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(removedIDsMethod).Params().Params(
			jen.Id("ids").Index().Add(idType),
		).Block(
			jen.For(jen.Id("id").Op(":=").Range().Id("m").Dot(removedFieldName)).Block(
				jen.Id("ids").Op("=").Append(jen.Id("ids"), jen.Id("id")),
			),
			jen.Return(),
		)
	} else {
		// skipSetters: still emit ClearXxx and XxxCleared methods so that
		// update check() / HasClearedEdge and the ClearEdge(name) dispatcher
		// can query/set the edge's cleared state. The FK field itself is
		// written via the user-defined field setter; clearing the edge only
		// toggles the cleared flag used by update planning.
		clearMethod := edge.MutationClear()
		f.Commentf("%s clears the %q edge.", clearMethod, edgeName)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(clearMethod).Params().Block(
			jen.Id("m").Dot(clearedFieldName).Op("=").True(),
		)

		clearedMethod := edge.MutationCleared()
		f.Commentf("%s reports if the %q edge was cleared.", clearedMethod, edgeName)
		f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(clearedMethod).Params().Bool().Block(
			jen.Return(jen.Id("m").Dot(clearedFieldName)),
		)
	}

	// ResetXxx — clears all typed edge state for the edge.
	resetMethod := edge.MutationReset()
	f.Commentf("%s resets all changes to the %q edge.", resetMethod, edgeName)
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(resetMethod).Params().BlockFunc(func(body *jen.Group) {
		body.Id("m").Dot(fieldName).Op("=").Nil()
		body.Id("m").Dot(clearedFieldName).Op("=").False()
		body.Id("m").Dot(removedFieldName).Op("=").Nil()
		// For field-backed edges, also reset the typed field pointer so the
		// user-defined FK field is cleared alongside the edge.
		if skipSetters {
			if fk, err := edge.ForeignKey(); err == nil && fk.Field != nil {
				body.Id("m").Dot("_" + fk.Field.Name).Op("=").Nil()
			}
		}
	})

	// XxxIDs returns the IDs added/set for this edge.
	// Used by check() in create builders to verify required edges.
	idsMethod := edge.StructField() + "IDs"
	f.Commentf("%s returns the %q edge IDs in the mutation.", idsMethod, edgeName)
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id(idsMethod).Params().Params(
		jen.Id("ids").Index().Add(idType),
	).Block(
		jen.For(jen.Id("id").Op(":=").Range().Id("m").Dot(fieldName)).Block(
			jen.Id("ids").Op("=").Append(jen.Id("ids"), jen.Id("id")),
		),
		jen.Return(),
	)
}

// genMutationFieldAccessors generates all methods required by the velox.Mutation interface.
func genMutationFieldAccessors(h gen.GeneratorHelper, f *jen.File, mutName string, t *gen.Type) {
	// Note: Op(), Type(), ID(), SetID(), SetOp() are emitted directly on the struct above.

	// Fields returns the list of set field names (reads from typed fields).
	f.Comment("Fields returns all fields that were changed during this mutation.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("Fields").Params().Index().String().BlockFunc(func(body *jen.Group) {
		body.Var().Id("fields").Index().String()
		for _, fd := range t.Fields {
			if fd.IsEdgeField() && !fd.UserDefined {
				continue
			}
			body.If(jen.Id("m").Dot("_" + fd.Name).Op("!=").Nil()).Block(
				jen.Id("fields").Op("=").Append(jen.Id("fields"), jen.Lit(fd.Name)),
			)
		}
		body.Return(jen.Id("fields"))
	})

	// Field returns a field value by name (reads from typed fields).
	f.Comment("Field returns the value of a field with the given name.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("Field").Params(
		jen.Id("name").String(),
	).Params(jen.Qual(runtimePkg, "Value"), jen.Bool()).BlockFunc(func(body *jen.Group) {
		body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, fd := range t.Fields {
				if fd.IsEdgeField() && !fd.UserDefined {
					continue
				}
				sw.Case(jen.Lit(fd.Name)).Block(
					jen.If(jen.Id("m").Dot("_" + fd.Name).Op("!=").Nil()).Block(
						jen.Return(jen.Op("*").Id("m").Dot("_"+fd.Name), jen.True()),
					),
				)
			}
		})
		body.Return(jen.Nil(), jen.False())
	})

	// SetField sets a field by name with type validation.
	f.Comment("SetField sets the value of a field with the given name. It returns an error if the field is not defined in the schema, or if the value type does not match.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("SetField").Params(
		jen.Id("name").String(), jen.Id("value").Qual(runtimePkg, "Value"),
	).Error().BlockFunc(func(g *jen.Group) {
		g.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, fd := range t.Fields {
				goType := h.BaseType(fd)
				sw.Case(jen.Lit(fd.Name)).BlockFunc(func(blk *jen.Group) {
					blk.List(jen.Id("v"), jen.Id("ok")).Op(":=").Id("value").Assert(goType)
					blk.If(jen.Op("!").Id("ok")).Block(
						jen.Return(jen.Qual("fmt", "Errorf").Call(
							jen.Lit("unexpected type %T for field "+fd.Name),
							jen.Id("value"),
						)),
					)
					blk.Id("m").Dot(fd.MutationSet()).Call(jen.Id("v"))
					blk.Return(jen.Nil())
				})
			}
			sw.Default().Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unknown "+t.Name+" field %s"), jen.Id("name"))),
			)
		})
	})

	// AddedFields returns all numeric fields that were incremented/decremented (reads from typed fields).
	f.Comment("AddedFields returns all numeric fields that were incremented or decremented during this mutation.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("AddedFields").Params().Index().String().BlockFunc(func(body *jen.Group) {
		body.Var().Id("fields").Index().String()
		for _, fd := range t.Fields {
			if fd.IsEdgeField() && !fd.UserDefined {
				continue
			}
			if fd.SupportsMutationAdd() {
				body.If(jen.Id("m").Dot("_add" + fd.Name).Op("!=").Nil()).Block(
					jen.Id("fields").Op("=").Append(jen.Id("fields"), jen.Lit(fd.Name)),
				)
			}
		}
		body.Return(jen.Id("fields"))
	})

	// AddedField returns the numeric value that was incremented/decremented (reads from typed fields).
	f.Comment("AddedField returns the numeric value that was incremented or decremented for a field.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("AddedField").Params(
		jen.Id("name").String(),
	).Params(jen.Qual(runtimePkg, "Value"), jen.Bool()).BlockFunc(func(body *jen.Group) {
		body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, fd := range t.Fields {
				if fd.IsEdgeField() && !fd.UserDefined {
					continue
				}
				if fd.SupportsMutationAdd() {
					sw.Case(jen.Lit(fd.Name)).Block(
						jen.If(jen.Id("m").Dot("_add" + fd.Name).Op("!=").Nil()).Block(
							jen.Return(jen.Op("*").Id("m").Dot("_add"+fd.Name), jen.True()),
						),
					)
				}
			}
		})
		body.Return(jen.Nil(), jen.False())
	})

	// AddField adds a numeric value to the given field with type validation.
	f.Comment("AddField adds the value for the given name. It returns an error if the field is not defined in the schema, or if the value type does not match.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("AddField").Params(
		jen.Id("name").String(), jen.Id("value").Qual(runtimePkg, "Value"),
	).Error().BlockFunc(func(g *jen.Group) {
		g.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, fd := range t.Fields {
				if !fd.Type.Numeric() {
					continue
				}
				// Edge-backing FK fields have no Add<X> method (adding a
				// delta to an ID is meaningless). Skip them here so the
				// AddField switch doesn't reference non-existent methods.
				if fd.IsEdgeField() {
					continue
				}
				goType := h.BaseType(fd)
				sw.Case(jen.Lit(fd.Name)).BlockFunc(func(blk *jen.Group) {
					blk.List(jen.Id("v"), jen.Id("ok")).Op(":=").Id("value").Assert(goType)
					blk.If(jen.Op("!").Id("ok")).Block(
						jen.Return(jen.Qual("fmt", "Errorf").Call(
							jen.Lit("unexpected type %T for field "+fd.Name),
							jen.Id("value"),
						)),
					)
					blk.Id("m").Dot("Add" + fd.StructField()).Call(jen.Id("v"))
					blk.Return(jen.Nil())
				})
			}
			sw.Default().Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unknown "+t.Name+" addable field %s"), jen.Id("name"))),
			)
		})
	})

	// ClearedFields (reads from typed clearedFields map).
	f.Comment("ClearedFields returns all fields that were cleared during this mutation.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("ClearedFields").Params().Index().String().Block(
		jen.Var().Id("fields").Index().String(),
		jen.For(jen.Id("f").Op(":=").Range().Id("m").Dot("clearedFields")).Block(
			jen.Id("fields").Op("=").Append(jen.Id("fields"), jen.Id("f")),
		),
		jen.Return(jen.Id("fields")),
	)

	// FieldCleared reports if a field was cleared (reads from typed clearedFields map).
	f.Comment("FieldCleared reports if a field with the given name was cleared in this mutation.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("FieldCleared").Params(
		jen.Id("name").String(),
	).Bool().Block(
		jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id("m").Dot("clearedFields").Index(jen.Id("name")),
		jen.Return(jen.Id("ok")),
	)

	// ClearField clears a field by name. Only Optional/Nillable fields can be cleared.
	// Returns an error for unknown or non-nullable field names.
	hasClearable := false
	for _, fd := range t.Fields {
		if fd.Optional || fd.Nillable {
			hasClearable = true
			break
		}
	}
	f.Comment("ClearField clears the value of the field with the given name. It returns an error if the field is not defined in the schema or is not nullable.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("ClearField").Params(
		jen.Id("name").String(),
	).Error().BlockFunc(func(g *jen.Group) {
		if !hasClearable {
			g.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unknown "+t.Name+" nullable field %s"), jen.Id("name")))
			return
		}
		g.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, fd := range t.Fields {
				if fd.Optional || fd.Nillable {
					sw.Case(jen.Lit(fd.Name)).BlockFunc(func(blk *jen.Group) {
						blk.Id("m").Dot("_" + fd.Name).Op("=").Nil()
						if fd.SupportsMutationAdd() {
							blk.Id("m").Dot("_add" + fd.Name).Op("=").Nil()
						}
					})
				}
			}
			sw.Default().Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unknown "+t.Name+" nullable field %s"), jen.Id("name"))),
			)
		})
		g.If(jen.Id("m").Dot("clearedFields").Op("==").Nil()).Block(
			jen.Id("m").Dot("clearedFields").Op("=").Make(jen.Map(jen.String()).Struct()),
		)
		g.Id("m").Dot("clearedFields").Index(jen.Id("name")).Op("=").Struct().Values()
		g.Return(jen.Nil())
	})

	// ResetField resets a field by name. Returns an error for unknown field names.
	f.Comment("ResetField resets all changes for the field with the given name. It returns an error if the field is not defined in the schema.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("ResetField").Params(
		jen.Id("name").String(),
	).Error().BlockFunc(func(g *jen.Group) {
		g.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, fd := range t.Fields {
				sw.Case(jen.Lit(fd.Name)).BlockFunc(func(blk *jen.Group) {
					blk.Id("m").Dot("_" + fd.Name).Op("=").Nil()
					if fd.SupportsMutationAdd() {
						blk.Id("m").Dot("_add" + fd.Name).Op("=").Nil()
					}
					blk.Delete(jen.Id("m").Dot("clearedFields"), jen.Lit(fd.Name))
				})
			}
			sw.Default().Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unknown "+t.Name+" field %s"), jen.Id("name"))),
			)
		})
		g.Return(jen.Nil())
	})

	edges := t.EdgesWithID()

	// AddedEdges returns all edge names that were added.
	f.Comment("AddedEdges returns all edge names that were set/added in this mutation.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("AddedEdges").Params().Index().String().BlockFunc(func(body *jen.Group) {
		body.Id("edges").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Lit(len(edges)))
		for _, e := range edges {
			body.If(jen.Id("m").Dot(e.BuilderField()).Op("!=").Nil()).Block(
				jen.Id("edges").Op("=").Append(jen.Id("edges"), jen.Lit(e.Name)),
			)
		}
		body.Return(jen.Id("edges"))
	})

	// AddedIDs returns the IDs added for an edge.
	f.Comment("AddedIDs returns all IDs that were added for the given edge name.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("AddedIDs").Params(
		jen.Id("name").String(),
	).Index().Qual(h.VeloxPkg(), "Value").BlockFunc(func(body *jen.Group) {
		body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, e := range edges {
				sw.Case(jen.Lit(e.Name)).Block(
					jen.Id("ids").Op(":=").Make(jen.Index().Qual(h.VeloxPkg(), "Value"), jen.Lit(0), jen.Len(jen.Id("m").Dot(e.BuilderField()))),
					jen.For(jen.Id("id").Op(":=").Range().Id("m").Dot(e.BuilderField())).Block(
						jen.Id("ids").Op("=").Append(jen.Id("ids"), jen.Id("id")),
					),
					jen.Return(jen.Id("ids")),
				)
			}
		})
		body.Return(jen.Nil())
	})

	// RemovedEdges returns all edge names that had removals.
	f.Comment("RemovedEdges returns all edge names that were removed in this mutation.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("RemovedEdges").Params().Index().String().BlockFunc(func(body *jen.Group) {
		body.Id("edges").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Lit(len(edges)))
		for _, e := range edges {
			body.If(jen.Id("m").Dot("removed" + e.StructField()).Op("!=").Nil()).Block(
				jen.Id("edges").Op("=").Append(jen.Id("edges"), jen.Lit(e.Name)),
			)
		}
		body.Return(jen.Id("edges"))
	})

	// RemovedIDs returns the IDs removed for an edge.
	f.Comment("RemovedIDs returns all IDs that were removed for the given edge name.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("RemovedIDs").Params(
		jen.Id("name").String(),
	).Index().Qual(h.VeloxPkg(), "Value").BlockFunc(func(body *jen.Group) {
		body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, e := range edges {
				sw.Case(jen.Lit(e.Name)).Block(
					jen.Id("ids").Op(":=").Make(jen.Index().Qual(h.VeloxPkg(), "Value"), jen.Lit(0), jen.Len(jen.Id("m").Dot("removed"+e.StructField()))),
					jen.For(jen.Id("id").Op(":=").Range().Id("m").Dot("removed"+e.StructField())).Block(
						jen.Id("ids").Op("=").Append(jen.Id("ids"), jen.Id("id")),
					),
					jen.Return(jen.Id("ids")),
				)
			}
		})
		body.Return(jen.Nil())
	})

	// ClearedEdges returns all edge names that were cleared.
	f.Comment("ClearedEdges returns all edge names that were cleared in this mutation.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("ClearedEdges").Params().Index().String().BlockFunc(func(body *jen.Group) {
		body.Id("edges").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Lit(len(edges)))
		for _, e := range edges {
			body.If(jen.Id("m").Dot("cleared" + e.StructField())).Block(
				jen.Id("edges").Op("=").Append(jen.Id("edges"), jen.Lit(e.Name)),
			)
		}
		body.Return(jen.Id("edges"))
	})

	// EdgeCleared reports if an edge was cleared.
	f.Comment("EdgeCleared reports if the given edge was cleared in this mutation.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("EdgeCleared").Params(
		jen.Id("name").String(),
	).Bool().BlockFunc(func(body *jen.Group) {
		body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, e := range edges {
				sw.Case(jen.Lit(e.Name)).Block(
					jen.Return(jen.Id("m").Dot("cleared" + e.StructField())),
				)
			}
		})
		body.Return(jen.False())
	})

	// ClearEdge clears an edge by name.
	f.Comment("ClearEdge clears the value of the edge with the given name.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("ClearEdge").Params(
		jen.Id("name").String(),
	).Error().BlockFunc(func(body *jen.Group) {
		body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, e := range edges {
				sw.Case(jen.Lit(e.Name)).Block(
					jen.Id("m").Dot(e.MutationClear()).Call(),
					jen.Return(jen.Nil()),
				)
			}
		})
		body.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unknown "+t.Name+" unique edge %s"), jen.Id("name")))
	})

	// ResetEdge resets an edge by name.
	f.Comment("ResetEdge resets all changes for the edge with the given name.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("ResetEdge").Params(
		jen.Id("name").String(),
	).Error().BlockFunc(func(body *jen.Group) {
		body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, e := range edges {
				sw.Case(jen.Lit(e.Name)).Block(
					jen.Id("m").Dot(e.MutationReset()).Call(),
					jen.Return(jen.Nil()),
				)
			}
		})
		body.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unknown "+t.Name+" edge %s"), jen.Id("name")))
	})

	// OldField dispatches by field name to the typed OldXxx helper.
	// Always generated (required by velox.Mutation interface).
	f.Comment("OldField returns the old value of a field from the database.")
	f.Comment("An error is returned if the mutation operation is not UpdateOne,")
	f.Comment("or the query to the database fails.")
	f.Func().Params(jen.Id("m").Op("*").Id(mutName)).Id("OldField").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("name").String(),
	).Params(jen.Qual(h.VeloxPkg(), "Value"), jen.Error()).BlockFunc(func(body *jen.Group) {
		if len(t.MutableFields()) > 0 {
			body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
				for _, fd := range t.Fields {
					if fd.IsEdgeField() && !fd.UserDefined {
						continue
					}
					sw.Case(jen.Lit(fd.Name)).Block(
						jen.Return(jen.Id("m").Dot("Old" + fd.StructField()).Call(jen.Id("ctx"))),
					)
				}
			})
		}
		body.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(jen.Lit("unknown "+t.Name+" field %s"), jen.Id("name")))
	})

	// Per-field OldXxx methods — only when entity has mutable fields,
	// because loadOld (which they depend on) is only generated for mutable entities.
	if len(t.MutableFields()) > 0 {
		for _, fd := range t.Fields {
			if fd.IsEdgeField() && !fd.UserDefined {
				continue
			}
			genMutationOldField(h, f, mutName, t, fd)
		}
	}
}
