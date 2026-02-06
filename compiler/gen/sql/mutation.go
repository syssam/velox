package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genMutation generates the mutation file ({entity}_mutation.go).
func genMutation(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	f := h.NewFile(h.Pkg())

	// Generate Mutation struct
	genMutationStruct(h, f, t)

	// Generate mutation constructor and methods
	genMutationMethods(h, f, t)

	return f
}

// genMutationStruct generates the Mutation struct.
func genMutationStruct(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	mutationName := t.MutationName()

	f.Commentf("%s represents an operation that mutates the %s nodes in the graph.", mutationName, t.Name)
	f.Type().Id(mutationName).StructFunc(func(group *jen.Group) {
		// Base fields - config is embedded (like Ent)
		group.Id("config")
		group.Id("op").Id("Op")
		group.Id("typ").String()

		// ID field (per Ent template: only if HasOneFieldID - single field ID, not composite)
		if t.HasOneFieldID() {
			group.Id(t.ID.BuilderField()).Op("*").Add(h.IDType(t))
		}

		// MutationFields (excludes edge fields) - per Ent template: range $f := $n.MutationFields
		for _, field := range t.Fields {
			if field.IsEdgeField() {
				continue
			}
			group.Id(field.BuilderField()).Op("*").Add(h.BaseType(field))
			// Per Ent template: add{BuilderField} uses *SignedType (not *Type)
			if field.SupportsMutationAdd() {
				signedType, _ := field.SignedType()
				addType := h.BaseType(field)
				if signedType != nil {
					addType = jen.Id(signedType.String())
				}
				group.Id("add" + field.BuilderField()).Op("*").Add(addType)
			}
			// Per Ent template: append{BuilderField} for JSON array fields (no pointer, uses full slice Type)
			if field.SupportsMutationAppend() {
				group.Id("append" + field.BuilderField()).Add(h.GoType(field))
			}
		}

		// clearedFields
		group.Id("clearedFields").Map(jen.String()).Struct()

		// EdgesWithID (per Ent template: range $e := $n.EdgesWithID)
		for _, edge := range t.EdgesWithID() {
			if edge.Unique {
				group.Id(edge.BuilderField()).Op("*").Add(h.IDType(edge.Type))
			} else {
				group.Id(edge.BuilderField()).Map(h.IDType(edge.Type)).Struct()
				group.Id("removed" + edge.BuilderField()).Map(h.IDType(edge.Type)).Struct()
			}
			group.Id("cleared" + edge.BuilderField()).Bool()
		}

		// done, oldValue, predicates
		group.Id("done").Bool()
		group.Id("oldValue").Func().Params(
			jen.Qual("context", "Context"),
		).Params(jen.Op("*").Id(t.Name), jen.Error())
		group.Id("predicates").Index().Add(h.PredicateType(t))
	})
}

// genMutationMethods generates methods for the mutation struct.
func genMutationMethods(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	mutationName := t.MutationName()

	// Interface assertion - compile-time check that mutation implements ent.Mutation
	f.Var().Id("_").Qual(h.VeloxPkg(), "Mutation").Op("=").Parens(jen.Op("*").Id(mutationName)).Call(jen.Nil())

	// Option type (unexported, internal use only)
	f.Commentf("%s allows management of the mutation configuration using functional options.", t.MutationOptionName())
	f.Type().Id(t.MutationOptionName()).Func().Params(jen.Op("*").Id(mutationName))

	// Constructor
	f.Commentf("new%s creates new mutation for the %s entity.", mutationName, t.Name)
	f.Func().Id("new"+mutationName).Params(
		jen.Id("c").Id("config"),
		jen.Id("op").Id("Op"),
		jen.Id("opts").Op("...").Id(t.MutationOptionName()),
	).Op("*").Id(mutationName).Block(
		jen.Id("m").Op(":=").Op("&").Id(mutationName).Values(jen.Dict{
			jen.Id("config"):        jen.Id("c"),
			jen.Id("op"):            jen.Id("op"),
			jen.Id("typ"):           jen.Id(t.TypeName()),
			jen.Id("clearedFields"): jen.Make(jen.Map(jen.String()).Struct()),
		}),
		jen.For(jen.List(jen.Id("_"), jen.Id("opt")).Op(":=").Range().Id("opts")).Block(
			jen.Id("opt").Call(jen.Id("m")),
		),
		jen.Return(jen.Id("m")),
	)

	// with{Entity} option (per Ent template: sets oldValue first, then ID)
	f.Commentf("with%s sets the old %s of the mutation.", t.Name, t.Name)
	f.Func().Id("with" + t.Name).Params(
		jen.Id("node").Op("*").Id(t.Name),
	).Id(t.MutationOptionName()).Block(
		jen.Return(jen.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Block(
			jen.Id(t.Receiver()).Dot("oldValue").Op("=").Func().Params(
				jen.Qual("context", "Context"),
			).Params(jen.Op("*").Id(t.Name), jen.Error()).Block(
				jen.Return(jen.Id("node"), jen.Nil()),
			),
			jen.Id(t.Receiver()).Dot(t.ID.BuilderField()).Op("=").Op("&").Id("node").Dot("ID"),
		)),
	)

	// with{Entity}ID option (per Ent template: sets oldValue, then ID)
	f.Commentf("with%sID sets the ID field of the mutation.", t.Name)
	f.Func().Id("with" + t.Name + "ID").Params(
		jen.Id("id").Add(h.IDType(t)),
	).Id(t.MutationOptionName()).Block(
		jen.Return(jen.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Block(
			jen.Var().Defs(
				jen.Id("err").Error(),
				jen.Id("once").Qual("sync", "Once"),
				jen.Id("value").Op("*").Id(t.Name),
			),
			jen.Id(t.Receiver()).Dot("oldValue").Op("=").Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
			).Params(jen.Op("*").Id(t.Name), jen.Error()).Block(
				jen.Id("once").Dot("Do").Call(jen.Func().Params().Block(
					jen.If(jen.Id(t.Receiver()).Dot("done")).Block(
						jen.Id("err").Op("=").Qual("errors", "New").Call(jen.Lit("querying old values post mutation is not allowed")),
					).Else().Block(
						jen.List(jen.Id("value"), jen.Id("err")).Op("=").Id(t.Receiver()).Dot("Client").Call().Dot(t.Name).Dot("Get").Call(jen.Id("ctx"), jen.Id("id")),
					),
				)),
				jen.Return(jen.Id("value"), jen.Id("err")),
			),
			jen.Id(t.Receiver()).Dot(t.ID.BuilderField()).Op("=").Op("&").Id("id"),
		)),
	)

	// Op returns the operation type
	f.Comment("Op returns the operation name.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("Op").Params().Id("Op").Block(
		jen.Return(jen.Id(t.Receiver()).Dot("op")),
	)

	// Type returns the entity type name
	f.Comment("Type returns the node type of this mutation.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("Type").Params().String().Block(
		jen.Return(jen.Id(t.Receiver()).Dot("typ")),
	)

	// ID returns the id if available (per Ent template: uses t.ID.BuilderField)
	f.Comment("ID returns the ID value in the mutation.")
	f.Comment("Note that the ID is only available if it was provided to the builder or after it was returned from the database.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("ID").Params().Params(
		jen.Id("id").Add(h.IDType(t)),
		jen.Id("exists").Bool(),
	).Block(
		jen.If(jen.Id(t.Receiver()).Dot(t.ID.BuilderField()).Op("==").Nil()).Block(
			jen.Return(),
		),
		jen.Return(jen.Op("*").Id(t.Receiver()).Dot(t.ID.BuilderField()), jen.True()),
	)

	// IDs returns all IDs for batch mutations (per Ent template: uses switch { case m.op.Is(...) })
	f.Comment("IDs queries the database and returns the entity ids that match the mutation's predicate.")
	f.Comment("That means, if the mutation is applied within a transaction with an isolation level such")
	f.Comment("as sql.LevelSerializable, the returned ids match the ids of the rows that will be updated")
	f.Comment("or updated by the mutation.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("IDs").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Add(h.IDType(t)), jen.Error()).Block(
		jen.Switch().BlockFunc(func(grp *jen.Group) {
			// case m.op.Is(OpUpdateOne | OpDeleteOne):
			grp.Case(jen.Id(t.Receiver()).Dot("op").Dot("Is").Call(jen.Id("OpUpdateOne").Op("|").Id("OpDeleteOne"))).Block(
				jen.List(jen.Id("id"), jen.Id("exists")).Op(":=").Id(t.Receiver()).Dot("ID").Call(),
				jen.If(jen.Id("exists")).Block(
					jen.Return(jen.Index().Add(h.IDType(t)).Values(jen.Id("id")), jen.Nil()),
				),
				jen.Id("fallthrough"),
			)
			// case m.op.Is(OpUpdate | OpDelete):
			grp.Case(jen.Id(t.Receiver()).Dot("op").Dot("Is").Call(jen.Id("OpUpdate").Op("|").Id("OpDelete"))).Block(
				jen.Return(jen.Id(t.Receiver()).Dot("Client").Call().Dot(t.Name).Dot("Query").Call().
					Dot("Where").Call(jen.Id(t.Receiver()).Dot("predicates").Op("...")).
					Dot("IDs").Call(jen.Id("ctx"))),
			)
			grp.Default().Block(
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(
					jen.Lit("IDs is not allowed on %s operations"),
					jen.Id(t.Receiver()).Dot("op"),
				)),
			)
		}),
	)

	// SetID for setting ID (per Ent template: only if HasOneFieldID and UserDefined)
	if t.HasOneFieldID() && t.ID.UserDefined {
		f.Comment("SetID sets the value of the id field. Note that this")
		f.Commentf("operation is only accepted on creation of %s entities.", t.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("SetID").Params(
			jen.Id("id").Add(h.IDType(t)),
		).Block(
			jen.Id(t.Receiver()).Dot(t.ID.BuilderField()).Op("=").Op("&").Id("id"),
		)
	}

	// Field setters and getters
	for _, field := range t.Fields {
		if field.IsEdgeField() {
			// Edge-backed fields get special treatment - generate methods that use the edge's struct field
			genEdgeFieldMutationMethods(h, f, t, field)
			continue
		}
		genMutationFieldMethods(h, f, t, field)
	}

	// Edge methods (per Ent template: uses EdgesWithID, not all Edges)
	for _, edge := range t.EdgesWithID() {
		genMutationEdgeMethods(h, f, t, edge)
	}

	// Where adds predicates
	f.Comment("Where appends a list predicates to the mutation.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("Where").Params(
		jen.Id("ps").Op("...").Add(h.PredicateType(t)),
	).Block(
		jen.Id(t.Receiver()).Dot("predicates").Op("=").Append(jen.Id(t.Receiver()).Dot("predicates"), jen.Id("ps").Op("...")),
	)

	// Client returns the client
	f.Comment("Client returns a new `ent.Client` from the mutation.")
	f.Func().Params(jen.Id(t.Receiver()).Id(mutationName)).Id("Client").Params().Op("*").Id("Client").Block(
		jen.Id("client").Op(":=").Op("&").Id("Client").Values(jen.Dict{
			jen.Id("config"): jen.Id(t.Receiver()).Dot("config"),
		}),
		jen.Id("client").Dot("init").Call(),
		jen.Return(jen.Id("client")),
	)

	// Tx returns the transaction
	f.Comment("Tx returns an `ent.Tx` for mutations that were executed in transactions.")
	f.Func().Params(jen.Id(t.Receiver()).Id(mutationName)).Id("Tx").Params().Params(
		jen.Op("*").Id("Tx"),
		jen.Error(),
	).Block(
		jen.If(
			jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id(t.Receiver()).Dot("driver").Op(".").Parens(jen.Op("*").Id("txDriver")),
			jen.Op("!").Id("ok"),
		).Block(
			jen.Return(jen.Nil(), jen.Qual("errors", "New").Call(jen.Lit(h.Pkg()+": mutation is not running in a transaction"))),
		),
		jen.Id("tx").Op(":=").Op("&").Id("Tx").Values(jen.Dict{
			jen.Id("config"): jen.Id(t.Receiver()).Dot("config"),
		}),
		jen.Id("tx").Dot("init").Call(),
		jen.Return(jen.Id("tx"), jen.Nil()),
	)

	// SetOp sets the operation
	f.Comment("SetOp allows to set the mutation operation.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("SetOp").Params(
		jen.Id("op").Id("Op"),
	).Block(
		jen.Id(t.Receiver()).Dot("op").Op("=").Id("op"),
	)

	// Generate generic Mutation interface methods
	genMutationInterfaceMethods(h, f, t)

	// Feature: privacy - Filter and WhereP methods for privacy rules
	if h.FeatureEnabled("privacy") {
		genMutationFilterMethods(h, f, t, mutationName)
	}
}

// genMutationFilterMethods generates Filter() and WhereP() methods for privacy filtering.
func genMutationFilterMethods(h gen.GeneratorHelper, f *jen.File, t *gen.Type, mutationName string) {
	filterName := t.Name + "Filter"
	privacyPkg := "github.com/syssam/velox/privacy"

	// WhereP adds predicates using raw sql.Selector functions
	f.Comment("WhereP appends storage-level predicates to the mutation.")
	f.Comment("For use with privacy rules and dynamic filtering.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("WhereP").Params(
		jen.Id("ps").Op("...").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector")),
	).Block(
		jen.For(jen.List(jen.Id("_"), jen.Id("p")).Op(":=").Range().Id("ps")).Block(
			jen.Id(t.Receiver()).Dot("predicates").Op("=").Append(
				jen.Id(t.Receiver()).Dot("predicates"),
				jen.Add(h.PredicateType(t)).Call(jen.Id("p")),
			),
		),
	)

	// Filter returns a Filter implementation (returns interface for core FilterFunc compatibility)
	f.Comment("Filter returns a Filter implementation to apply filters on the mutation.")
	f.Comment("For use with privacy rules and dynamic filtering in privacy policies.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("Filter").Params().Qual(privacyPkg, "Filter").Block(
		jen.Return(jen.Op("&").Id(filterName).Values(jen.Dict{
			jen.Id("config"):     jen.Id(t.Receiver()).Dot("config"),
			jen.Id("predicates"): jen.Op("&").Id(t.Receiver()).Dot("predicates"),
		})),
	)
}

// genMutationFieldMethods generates field getter/setter methods.
func genMutationFieldMethods(h gen.GeneratorHelper, f *jen.File, t *gen.Type, field *gen.Field) {
	mutationName := t.MutationName()
	builderField := field.BuilderField()
	structField := field.StructField()

	// SetXxx
	setter := field.MutationSet()
	f.Commentf("%s sets the %q field.", setter, field.Name)
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(setter).Params(
		jen.Id("v").Add(h.BaseType(field)),
	).BlockFunc(func(body *jen.Group) {
		body.Id(t.Receiver()).Dot(builderField).Op("=").Op("&").Id("v")
		// Per Ent template: setting numeric type overrides previous calls to Add
		if field.SupportsMutationAdd() {
			body.Id(t.Receiver()).Dot("add" + field.BuilderField()).Op("=").Nil()
		}
		// Per Ent template: setting JSON type overrides previous calls to Append
		if field.SupportsMutationAppend() {
			body.Id(t.Receiver()).Dot("append" + field.BuilderField()).Op("=").Nil()
		}
	})

	// Xxx getter
	getter := field.MutationGet()
	f.Commentf("%s returns the value of the %q field in the mutation.", getter, field.Name)
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(getter).Params().Params(
		jen.Id("r").Add(h.BaseType(field)),
		jen.Id("exists").Bool(),
	).Block(
		jen.If(jen.Id(t.Receiver()).Dot(builderField).Op("==").Nil()).Block(
			jen.Return(jen.Add(h.BaseZeroValue(field)), jen.False()),
		),
		jen.Return(jen.Op("*").Id(t.Receiver()).Dot(builderField), jen.True()),
	)

	// OldXxx for updates (per Ent template: only if HasOneFieldID)
	if t.HasOneFieldID() {
		oldGetter := field.MutationGetOld()
		f.Commentf("%s returns the old %q field's value of the %s entity.", oldGetter, field.Name, t.Name)
		f.Comment("If the " + t.Name + " object wasn't provided to the builder, the object is fetched from the database.")
		f.Comment("An error is returned if the mutation operation is not UpdateOne, or the database query fails.")
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(oldGetter).Params(
			jen.Id("ctx").Qual("context", "Context"),
		).Params(jen.Id("v").Add(h.GoType(field)), jen.Id("err").Error()).Block(
			jen.If(jen.Op("!").Id(t.Receiver()).Dot("op").Dot("Is").Call(jen.Id("OpUpdateOne"))).Block(
				jen.Return(jen.Id("v"), jen.Qual("errors", "New").Call(
					jen.Lit(oldGetter+" is only allowed on UpdateOne operations"),
				)),
			),
			jen.If(jen.Id(t.Receiver()).Dot(t.ID.BuilderField()).Op("==").Nil().Op("||").Id(t.Receiver()).Dot("oldValue").Op("==").Nil()).Block(
				jen.Return(jen.Id("v"), jen.Qual("errors", "New").Call(
					jen.Lit(oldGetter+" requires an ID field in the mutation"),
				)),
			),
			jen.Id("oldValue").Op(",").Id("err").Op(":=").Id(t.Receiver()).Dot("oldValue").Call(jen.Id("ctx")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Id("v"), jen.Qual("fmt", "Errorf").Call(
					jen.Lit("querying old value for "+oldGetter+": %w"),
					jen.Id("err"),
				)),
			),
			jen.Return(jen.Id("oldValue").Dot(structField), jen.Nil()),
		)
	}

	// ResetXxx
	resetter := field.MutationReset()
	f.Commentf("%s resets all changes to the %q field.", resetter, field.Name)
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(resetter).Params().BlockFunc(func(body *jen.Group) {
		body.Id(t.Receiver()).Dot(builderField).Op("=").Nil()
		// Per Ent template: clear add field
		if field.SupportsMutationAdd() {
			body.Id(t.Receiver()).Dot("add" + field.BuilderField()).Op("=").Nil()
		}
		// Per Ent template: clear append field
		if field.SupportsMutationAppend() {
			body.Id(t.Receiver()).Dot("append" + field.BuilderField()).Op("=").Nil()
		}
		// Delete from clearedFields only if field can be cleared (Nillable)
		// Note: Velox Optional() means NOT NULL in DB, only Nillable() allows NULL
		if field.Nillable {
			body.Delete(jen.Id(t.Receiver()).Dot("clearedFields"), jen.Qual(h.EntityPkgPath(t), field.Constant()))
		}
	})

	// ClearXxx for Nillable fields only (can set NULL in DB)
	// Note: Unlike Ent where Optional implies NULL-able, Velox separates these concerns
	if field.Nillable {
		clearer := field.MutationClear()
		f.Commentf("%s clears the value of the %q field.", clearer, field.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(clearer).Params().BlockFunc(func(body *jen.Group) {
			body.Id(t.Receiver()).Dot(builderField).Op("=").Nil()
			// Per Ent template: clear add field
			if field.SupportsMutationAdd() {
				body.Id(t.Receiver()).Dot("add" + field.BuilderField()).Op("=").Nil()
			}
			// Per Ent template: clear append field
			if field.SupportsMutationAppend() {
				body.Id(t.Receiver()).Dot("append" + field.BuilderField()).Op("=").Nil()
			}
			body.Id(t.Receiver()).Dot("clearedFields").Index(jen.Qual(h.EntityPkgPath(t), field.Constant())).Op("=").Struct().Values()
		})

		cleared := field.MutationCleared()
		f.Commentf("%s returns if the %q field was cleared in this mutation.", cleared, field.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(cleared).Params().Bool().Block(
			jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id(t.Receiver()).Dot("clearedFields").Index(jen.Qual(h.EntityPkgPath(t), field.Constant())),
			jen.Return(jen.Id("ok")),
		)
	}

	// AddXxx for numeric fields
	if field.SupportsMutationAdd() {
		adder := field.MutationAdd()
		addBuilderField := "add" + field.BuilderField()
		signedType, _ := field.SignedType()
		addType := h.BaseType(field)
		if signedType != nil {
			addType = jen.Id(signedType.String())
		}

		f.Commentf("%s adds the value to the %q field.", adder, field.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(adder).Params(
			jen.Id("v").Add(addType),
		).Block(
			jen.If(jen.Id(t.Receiver()).Dot(addBuilderField).Op("!=").Nil()).Block(
				jen.Op("*").Id(t.Receiver()).Dot(addBuilderField).Op("+=").Id("v"),
			).Else().Block(
				jen.Id(t.Receiver()).Dot(addBuilderField).Op("=").Op("&").Id("v"),
			),
		)

		addedGetter := field.MutationAdded()
		f.Commentf("%s returns the value that was added to the %q field in this mutation.", addedGetter, field.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(addedGetter).Params().Params(
			jen.Id("r").Add(addType),
			jen.Id("exists").Bool(),
		).Block(
			jen.If(jen.Id(t.Receiver()).Dot(addBuilderField).Op("==").Nil()).Block(
				jen.Return(),
			),
			jen.Return(jen.Op("*").Id(t.Receiver()).Dot(addBuilderField), jen.True()),
		)
	}

	// AppendXxx for JSON array fields (per Ent template: {{ if $f.SupportsMutationAppend }})
	if field.SupportsMutationAppend() {
		appendBuilderField := "append" + field.BuilderField()

		// MutationAppend - appends values to the field (takes full slice type)
		appender := field.MutationAppend()
		f.Commentf("%s adds v to the %q field.", appender, field.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(appender).Params(
			jen.Id("v").Add(h.GoType(field)),
		).Block(
			jen.Id(t.Receiver()).Dot(appendBuilderField).Op("=").Append(
				jen.Id(t.Receiver()).Dot(appendBuilderField),
				jen.Id("v").Op("..."),
			),
		)

		// MutationAppended - returns the appended values (full slice type)
		appendedGetter := field.MutationAppended()
		f.Commentf("%s returns the list of values that were appended to the %q field in this mutation.", appendedGetter, field.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(appendedGetter).Params().Params(
			h.GoType(field),
			jen.Bool(),
		).Block(
			jen.If(jen.Len(jen.Id(t.Receiver()).Dot(appendBuilderField)).Op("==").Lit(0)).Block(
				jen.Return(jen.Nil(), jen.False()),
			),
			jen.Return(jen.Id(t.Receiver()).Dot(appendBuilderField), jen.True()),
		)
	}
}

// genEdgeFieldMutationMethods generates mutation methods for edge-backed FK fields.
// These fields use the edge's struct field for storage but have field-style methods.
// For example, field "user_id" with edge "user" uses m.user for storage.
func genEdgeFieldMutationMethods(h gen.GeneratorHelper, f *jen.File, t *gen.Type, field *gen.Field) {
	edge, err := field.Edge()
	if err != nil {
		return // Skip if we can't get the edge
	}

	mutationName := t.MutationName()
	// Use the edge's builder field for struct access
	builderField := edge.BuilderField()
	structField := field.StructField()
	fieldName := field.Name

	// SetXxxID (like Ent's SetUserID sets m.user)
	setter := field.MutationSet()
	f.Commentf("%s sets the %q field.", setter, fieldName)
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(setter).Params(
		jen.Id("v").Add(h.BaseType(field)),
	).Block(
		jen.Id(t.Receiver()).Dot(builderField).Op("=").Op("&").Id("v"),
	)

	// XxxID getter (like Ent's UserID returns m.user)
	getter := field.MutationGet()
	f.Commentf("%s returns the value of the %q field in the mutation.", getter, fieldName)
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(getter).Params().Params(
		jen.Id("r").Add(h.BaseType(field)),
		jen.Id("exists").Bool(),
	).Block(
		jen.If(jen.Id(t.Receiver()).Dot(builderField).Op("==").Nil()).Block(
			jen.Return(jen.Add(h.BaseZeroValue(field)), jen.False()),
		),
		jen.Return(jen.Op("*").Id(t.Receiver()).Dot(builderField), jen.True()),
	)

	// OldXxxID for updates (per Ent template: only if HasOneFieldID)
	if t.HasOneFieldID() {
		oldGetter := field.MutationGetOld()
		f.Commentf("%s returns the old %q field's value of the %s entity.", oldGetter, fieldName, t.Name)
		f.Comment("If the " + t.Name + " object wasn't provided to the builder, the object is fetched from the database.")
		f.Comment("An error is returned if the mutation operation is not UpdateOne, or the database query fails.")
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(oldGetter).Params(
			jen.Id("ctx").Qual("context", "Context"),
		).Params(jen.Id("v").Add(h.GoType(field)), jen.Id("err").Error()).Block(
			jen.If(jen.Op("!").Id(t.Receiver()).Dot("op").Dot("Is").Call(jen.Id("OpUpdateOne"))).Block(
				jen.Return(jen.Id("v"), jen.Qual("errors", "New").Call(
					jen.Lit(oldGetter+" is only allowed on UpdateOne operations"),
				)),
			),
			jen.If(jen.Id(t.Receiver()).Dot(t.ID.BuilderField()).Op("==").Nil().Op("||").Id(t.Receiver()).Dot("oldValue").Op("==").Nil()).Block(
				jen.Return(jen.Id("v"), jen.Qual("errors", "New").Call(
					jen.Lit(oldGetter+" requires an ID field in the mutation"),
				)),
			),
			jen.Id("oldValue").Op(",").Id("err").Op(":=").Id(t.Receiver()).Dot("oldValue").Call(jen.Id("ctx")),
			jen.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Id("v"), jen.Qual("fmt", "Errorf").Call(
					jen.Lit("querying old value for "+oldGetter+": %w"),
					jen.Id("err"),
				)),
			),
			jen.Return(jen.Id("oldValue").Dot(structField), jen.Nil()),
		)
	}

	// ClearXxxID (like Ent's ClearUserID - clears the value AND marks field as cleared)
	clearer := field.MutationClear()
	f.Commentf("%s clears the value of the %q field.", clearer, fieldName)
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(clearer).Params().Block(
		jen.Id(t.Receiver()).Dot(builderField).Op("=").Nil(),
		jen.Id(t.Receiver()).Dot("clearedFields").Index(jen.Qual(h.EntityPkgPath(t), field.Constant())).Op("=").Struct().Values(),
	)

	// XxxIDCleared (checks if field was cleared)
	cleared := field.MutationCleared()
	f.Commentf("%s returns if the %q field was cleared in this mutation.", cleared, fieldName)
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(cleared).Params().Bool().Block(
		jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id(t.Receiver()).Dot("clearedFields").Index(jen.Qual(h.EntityPkgPath(t), field.Constant())),
		jen.Return(jen.Id("ok")),
	)

	// ResetXxxID (resets the field value AND removes from clearedFields)
	resetter := field.MutationReset()
	f.Commentf("%s resets all changes to the %q field.", resetter, fieldName)
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(resetter).Params().Block(
		jen.Id(t.Receiver()).Dot(builderField).Op("=").Nil(),
		jen.Delete(jen.Id(t.Receiver()).Dot("clearedFields"), jen.Qual(h.EntityPkgPath(t), field.Constant())),
	)
}

// genMutationEdgeMethods generates edge mutation methods.
func genMutationEdgeMethods(h gen.GeneratorHelper, f *jen.File, t *gen.Type, edge *gen.Edge) {
	mutationName := t.MutationName()
	builderField := edge.BuilderField()
	// Check if this edge has an associated FK field (like Ent's edge.Field("user_id"))
	fkField := edge.Field()

	if edge.Unique {
		// SetXxxID - generate for unique edges, but skip if FK field method has the same name
		// This avoids collision when edge "ab_test" and field "ab_test_id" both generate "SetAbTestID"
		setter := edge.MutationSet()
		if fkField == nil || fkField.MutationSet() != setter {
			f.Commentf("%s sets the %q edge by id.", setter, edge.Name)
			f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(setter).Params(
				jen.Id("id").Add(h.IDType(edge.Type)),
			).Block(
				jen.Id(t.Receiver()).Dot(builderField).Op("=").Op("&").Id("id"),
			)
		}

		// XxxID getter (singular) - only generate if no FK field (field method handles it otherwise)
		if fkField == nil {
			getter := edge.StructField() + "ID"
			f.Commentf("%s returns the %q edge id in the mutation.", getter, edge.Name)
			f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(getter).Params().Params(
				jen.Id("id").Add(h.IDType(edge.Type)),
				jen.Id("exists").Bool(),
			).Block(
				jen.If(jen.Id(t.Receiver()).Dot(builderField).Op("==").Nil()).Block(
					jen.Return(),
				),
				jen.Return(jen.Op("*").Id(t.Receiver()).Dot(builderField), jen.True()),
			)
		}

		// XxxIDs getter (plural) - always generate for edges
		idsGetter := edge.StructField() + "IDs"
		f.Commentf("%s returns the %q edge IDs in the mutation.", idsGetter, edge.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(idsGetter).Params().Index().Add(h.IDType(edge.Type)).Block(
			jen.If(jen.Id("id").Op(":=").Id(t.Receiver()).Dot(builderField), jen.Id("id").Op("!=").Nil()).Block(
				jen.Return(jen.Index().Add(h.IDType(edge.Type)).Values(jen.Op("*").Id("id"))),
			),
			jen.Return(jen.Nil()),
		)

		// ClearXxx - mark edge as cleared, and add FK field to clearedFields if exists (like Ent template)
		// Template: m.cleared{{ $e.BuilderField }} = true; if $e.Field: m.clearedFields[{{ $const }}] = struct{}{}
		clearer := edge.MutationClear()
		f.Commentf("%s clears the %q edge.", clearer, edge.Name)
		if fkField != nil {
			// With FK field: mark edge cleared AND add FK field to clearedFields
			f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(clearer).Params().Block(
				jen.Id(t.Receiver()).Dot("cleared"+edge.BuilderField()).Op("=").True(),
				jen.Id(t.Receiver()).Dot("clearedFields").Index(jen.Qual(h.EntityPkgPath(t), fkField.Constant())).Op("=").Struct().Values(),
			)
		} else {
			// Without FK field: just mark edge cleared (don't set builderField to nil - Ent doesn't)
			f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(clearer).Params().Block(
				jen.Id(t.Receiver()).Dot("cleared" + edge.BuilderField()).Op("=").True(),
			)
		}

		// XxxCleared - for edges with nillable FK fields, check both edge cleared and field cleared
		// Note: FK field is Nillable when edge is Optional (can set NULL in DB)
		cleared := edge.MutationCleared()
		f.Commentf("%s reports if the %q edge was cleared.", cleared, edge.Name)
		if fkField != nil && fkField.Nillable {
			// With nillable FK field: check both field cleared and edge cleared
			fieldClearedMethod := fkField.MutationCleared()
			f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(cleared).Params().Bool().Block(
				jen.Return(jen.Id(t.Receiver()).Dot(fieldClearedMethod).Call().Op("||").Id(t.Receiver()).Dot("cleared" + edge.BuilderField())),
			)
		} else {
			// Without FK field or required FK field: just check edge cleared
			f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(cleared).Params().Bool().Block(
				jen.Return(jen.Id(t.Receiver()).Dot("cleared" + edge.BuilderField())),
			)
		}

		// ResetXxx
		resetter := edge.MutationReset()
		f.Commentf("%s resets all changes to the %q edge.", resetter, edge.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(resetter).Params().Block(
			jen.Id(t.Receiver()).Dot(builderField).Op("=").Nil(),
			jen.Id(t.Receiver()).Dot("cleared"+edge.BuilderField()).Op("=").False(),
		)
	} else {
		// AddXxxIDs
		adder := edge.MutationAdd()
		f.Commentf("%s adds the %q edge by ids.", adder, edge.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(adder).Params(
			jen.Id("ids").Op("...").Add(h.IDType(edge.Type)),
		).Block(
			jen.If(jen.Id(t.Receiver()).Dot(builderField).Op("==").Nil()).Block(
				jen.Id(t.Receiver()).Dot(builderField).Op("=").Make(jen.Map(h.IDType(edge.Type)).Struct()),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("id")).Op(":=").Range().Id("ids")).Block(
				jen.Id(t.Receiver()).Dot(builderField).Index(jen.Id("id")).Op("=").Struct().Values(),
			),
		)

		// XxxIDs getter
		getter := edge.StructField() + "IDs"
		f.Commentf("%s returns the %q edge ids in the mutation.", getter, edge.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(getter).Params().Index().Add(h.IDType(edge.Type)).Block(
			jen.Id("ids").Op(":=").Make(jen.Index().Add(h.IDType(edge.Type)), jen.Lit(0), jen.Len(jen.Id(t.Receiver()).Dot(builderField))),
			jen.For(jen.Id("id").Op(":=").Range().Id(t.Receiver()).Dot(builderField)).Block(
				jen.Id("ids").Op("=").Append(jen.Id("ids"), jen.Id("id")),
			),
			jen.Return(jen.Id("ids")),
		)

		// RemoveXxxIDs
		remover := edge.MutationRemove()
		f.Commentf("%s removes the %q edge by ids.", remover, edge.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(remover).Params(
			jen.Id("ids").Op("...").Add(h.IDType(edge.Type)),
		).Block(
			jen.If(jen.Id(t.Receiver()).Dot("removed"+edge.BuilderField()).Op("==").Nil()).Block(
				jen.Id(t.Receiver()).Dot("removed"+edge.BuilderField()).Op("=").Make(jen.Map(h.IDType(edge.Type)).Struct()),
			),
			jen.For(jen.List(jen.Id("_"), jen.Id("id")).Op(":=").Range().Id("ids")).Block(
				jen.Delete(jen.Id(t.Receiver()).Dot(builderField), jen.Id("id")),
				jen.Id(t.Receiver()).Dot("removed"+edge.BuilderField()).Index(jen.Id("id")).Op("=").Struct().Values(),
			),
		)

		// RemovedXxxIDs getter
		removedGetter := "Removed" + edge.StructField() + "IDs"
		f.Commentf("%s returns the removed ids of the %q edge.", removedGetter, edge.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(removedGetter).Params().Index().Add(h.IDType(edge.Type)).Block(
			jen.Id("ids").Op(":=").Make(jen.Index().Add(h.IDType(edge.Type)), jen.Lit(0), jen.Len(jen.Id(t.Receiver()).Dot("removed"+edge.BuilderField()))),
			jen.For(jen.Id("id").Op(":=").Range().Id(t.Receiver()).Dot("removed"+edge.BuilderField())).Block(
				jen.Id("ids").Op("=").Append(jen.Id("ids"), jen.Id("id")),
			),
			jen.Return(jen.Id("ids")),
		)

		// ClearXxx
		clearer := edge.MutationClear()
		f.Commentf("%s clears the %q edge.", clearer, edge.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(clearer).Params().Block(
			jen.Id(t.Receiver()).Dot("cleared" + edge.BuilderField()).Op("=").True(),
		)

		// XxxCleared
		cleared := edge.MutationCleared()
		f.Commentf("%s reports if the %q edge was cleared.", cleared, edge.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(cleared).Params().Bool().Block(
			jen.Return(jen.Id(t.Receiver()).Dot("cleared" + edge.BuilderField())),
		)

		// ResetXxx
		resetter := edge.MutationReset()
		f.Commentf("%s resets all changes to the %q edge.", resetter, edge.Name)
		f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id(resetter).Params().Block(
			jen.Id(t.Receiver()).Dot(builderField).Op("=").Nil(),
			jen.Id(t.Receiver()).Dot("cleared"+edge.BuilderField()).Op("=").False(),
			jen.Id(t.Receiver()).Dot("removed"+edge.BuilderField()).Op("=").Nil(),
		)
	}
}

// genMutationInterfaceMethods generates the methods required by the velox.Mutation interface.
func genMutationInterfaceMethods(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	mutationName := t.MutationName()

	// Fields returns all fields that were changed
	// Count non-edge fields for capacity
	var fieldCount int
	for _, field := range t.Fields {
		if !field.IsEdgeField() {
			fieldCount++
		}
	}
	f.Comment("Fields returns all fields that were changed during this mutation.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("Fields").Params().Index().String().BlockFunc(func(body *jen.Group) {
		body.Id("fields").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Lit(fieldCount))
		for _, field := range t.Fields {
			if field.IsEdgeField() {
				continue
			}
			body.If(jen.Id(t.Receiver()).Dot(field.BuilderField()).Op("!=").Nil()).Block(
				jen.Id("fields").Op("=").Append(jen.Id("fields"), jen.Qual(h.EntityPkgPath(t), field.Constant())),
			)
		}
		body.Return(jen.Id("fields"))
	})

	// Field returns the value of a field with the given name
	f.Comment("Field returns the value of a field with the given name. The second boolean")
	f.Comment("return value indicates that this field was not set, or was not defined in the")
	f.Comment("schema.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("Field").Params(
		jen.Id("name").String(),
	).Params(jen.Qual(h.VeloxPkg(), "Value"), jen.Bool()).Block(
		jen.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, field := range t.Fields {
				if field.IsEdgeField() {
					continue
				}
				sw.Case(jen.Qual(h.EntityPkgPath(t), field.Constant())).Block(
					jen.Return(jen.Id(t.Receiver()).Dot(field.MutationGet()).Call()),
				)
			}
		}),
		jen.Return(jen.Nil(), jen.False()),
	)

	// SetField sets the value of a field with the given name
	f.Comment("SetField sets the value of a field with the given name. It returns an error if")
	f.Comment("the field is not defined in the schema, or if the type mismatched the field")
	f.Comment("type.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("SetField").Params(
		jen.Id("name").String(),
		jen.Id("value").Qual(h.VeloxPkg(), "Value"),
	).Error().Block(
		jen.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, field := range t.Fields {
				if field.IsEdgeField() {
					continue
				}
				sw.Case(jen.Qual(h.EntityPkgPath(t), field.Constant())).Block(
					jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Id("value").Op(".").Parens(h.BaseType(field)),
					jen.If(jen.Op("!").Id("ok")).Block(
						jen.Return(jen.Qual("fmt", "Errorf").Call(
							jen.Lit("unexpected type %T for field "+field.Name),
							jen.Id("value"),
						)),
					),
					jen.Id(t.Receiver()).Dot(field.MutationSet()).Call(jen.Id("v")),
					jen.Return(jen.Nil()),
				)
			}
		}),
		jen.Return(jen.Qual("fmt", "Errorf").Call(
			jen.Lit("unknown "+t.Name+" field %s"),
			jen.Id("name"),
		)),
	)

	// AddedFields returns all numeric fields that were incremented/decremented
	// Check if there are any numeric fields (per Ent template: {{ if $n.HasNumeric }})
	var hasNumeric bool
	for _, field := range t.Fields {
		if field.SupportsMutationAdd() {
			hasNumeric = true
			break
		}
	}
	f.Comment("AddedFields returns all numeric fields that were incremented/decremented.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("AddedFields").Params().Index().String().BlockFunc(func(body *jen.Group) {
		if hasNumeric {
			body.Var().Id("fields").Index().String()
			for _, field := range t.Fields {
				if !field.SupportsMutationAdd() {
					continue
				}
				body.If(jen.Id(t.Receiver()).Dot("add" + field.BuilderField()).Op("!=").Nil()).Block(
					jen.Id("fields").Op("=").Append(jen.Id("fields"), jen.Qual(h.EntityPkgPath(t), field.Constant())),
				)
			}
			body.Return(jen.Id("fields"))
		} else {
			body.Return(jen.Nil())
		}
	})

	// AddedField returns the numeric value that was added to a field
	f.Comment("AddedField returns the numeric value that was incremented/decremented in a field")
	f.Comment("with the given name. The second boolean return value indicates that this field")
	f.Comment("was not set, or was not defined in the schema.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("AddedField").Params(
		jen.Id("name").String(),
	).Params(jen.Qual(h.VeloxPkg(), "Value"), jen.Bool()).Block(
		jen.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, field := range t.Fields {
				if !field.SupportsMutationAdd() {
					continue
				}
				sw.Case(jen.Qual(h.EntityPkgPath(t), field.Constant())).Block(
					jen.Return(jen.Id(t.Receiver()).Dot(field.MutationAdded()).Call()),
				)
			}
		}),
		jen.Return(jen.Nil(), jen.False()),
	)

	// ClearedFields returns all nullable fields that were cleared
	// Check if there are any nillable fields (can be set to NULL in DB)
	// Note: Velox Optional() means NOT NULL in DB, only Nillable() allows NULL
	var hasNillable bool
	for _, field := range t.Fields {
		if field.Nillable && !field.IsEdgeField() {
			hasNillable = true
			break
		}
	}
	f.Comment("ClearedFields returns all nullable fields that were cleared.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("ClearedFields").Params().Index().String().BlockFunc(func(body *jen.Group) {
		if hasNillable {
			body.Var().Id("fields").Index().String()
			for _, field := range t.Fields {
				if !field.Nillable || field.IsEdgeField() {
					continue
				}
				body.If(jen.Id(t.Receiver()).Dot("FieldCleared").Call(jen.Qual(h.EntityPkgPath(t), field.Constant()))).Block(
					jen.Id("fields").Op("=").Append(jen.Id("fields"), jen.Qual(h.EntityPkgPath(t), field.Constant())),
				)
			}
			body.Return(jen.Id("fields"))
		} else {
			body.Return(jen.Nil())
		}
	})

	// FieldCleared returns if a field was cleared
	f.Comment("FieldCleared returns if a field was cleared in this mutation.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("FieldCleared").Params(
		jen.Id("name").String(),
	).Bool().Block(
		jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id(t.Receiver()).Dot("clearedFields").Index(jen.Id("name")),
		jen.Return(jen.Id("ok")),
	)

	// ClearField clears a nillable field (can set NULL in DB)
	// Note: Unlike Ent where Optional implies NULL-able, Velox separates these concerns
	f.Comment("ClearField clears the value of the field with the given name. It returns an")
	f.Comment("error if the field is not defined in the schema.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("ClearField").Params(
		jen.Id("name").String(),
	).Error().BlockFunc(func(body *jen.Group) {
		if hasNillable {
			body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
				for _, field := range t.Fields {
					if !field.Nillable || field.IsEdgeField() {
						continue
					}
					sw.Case(jen.Qual(h.EntityPkgPath(t), field.Constant())).Block(
						jen.Id(t.Receiver()).Dot(field.MutationClear()).Call(),
						jen.Return(jen.Nil()),
					)
				}
			})
		}
		body.Return(jen.Qual("fmt", "Errorf").Call(
			jen.Lit("unknown "+t.Name+" nullable field %s"),
			jen.Id("name"),
		))
	})

	// ResetField resets all changes to a field
	f.Comment("ResetField resets all changes in the mutation for the field with the given name.")
	f.Comment("It returns an error if the field is not defined in the schema.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("ResetField").Params(
		jen.Id("name").String(),
	).Error().Block(
		jen.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, field := range t.Fields {
				if field.IsEdgeField() {
					continue
				}
				sw.Case(jen.Qual(h.EntityPkgPath(t), field.Constant())).Block(
					jen.Id(t.Receiver()).Dot(field.MutationReset()).Call(),
					jen.Return(jen.Nil()),
				)
			}
		}),
		jen.Return(jen.Qual("fmt", "Errorf").Call(
			jen.Lit("unknown "+t.Name+" field %s"),
			jen.Id("name"),
		)),
	)

	// AddedEdges returns all edge names that were set/added (per Ent template: uses EdgesWithID)
	f.Comment("AddedEdges returns all edge names that were set/added.")
	edges := t.EdgesWithID()
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("AddedEdges").Params().Index().String().BlockFunc(func(body *jen.Group) {
		body.Id("edges").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Lit(len(edges)))
		for _, edge := range edges {
			body.If(jen.Id(t.Receiver()).Dot(edge.BuilderField()).Op("!=").Nil()).Block(
				jen.Id("edges").Op("=").Append(jen.Id("edges"), jen.Qual(h.EntityPkgPath(t), edge.Constant())),
			)
		}
		body.Return(jen.Id("edges"))
	})

	// AddedIDs returns all ids that were added for an edge (per Ent template: uses EdgesWithID)
	f.Comment("AddedIDs returns all IDs (to other nodes) that were added for the given edge")
	f.Comment("name in this mutation.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("AddedIDs").Params(
		jen.Id("name").String(),
	).Index().Qual(h.VeloxPkg(), "Value").BlockFunc(func(body *jen.Group) {
		if len(edges) > 0 {
			body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
				for _, edge := range edges {
					if edge.Unique {
						sw.Case(jen.Qual(h.EntityPkgPath(t), edge.Constant())).Block(
							jen.If(jen.Id("id").Op(":=").Id(t.Receiver()).Dot(edge.BuilderField()), jen.Id("id").Op("!=").Nil()).Block(
								jen.Return(jen.Index().Qual(h.VeloxPkg(), "Value").Values(jen.Op("*").Id("id"))),
							),
						)
					} else {
						sw.Case(jen.Qual(h.EntityPkgPath(t), edge.Constant())).Block(
							jen.Id("ids").Op(":=").Make(jen.Index().Qual(h.VeloxPkg(), "Value"), jen.Lit(0), jen.Len(jen.Id(t.Receiver()).Dot(edge.BuilderField()))),
							jen.For(jen.Id("id").Op(":=").Range().Id(t.Receiver()).Dot(edge.BuilderField())).Block(
								jen.Id("ids").Op("=").Append(jen.Id("ids"), jen.Id("id")),
							),
							jen.Return(jen.Id("ids")),
						)
					}
				}
			})
		}
		body.Return(jen.Nil())
	})

	// RemovedEdges returns all edge names that were removed (per Ent template: uses EdgesWithID, non-unique only)
	// Count non-unique edges for capacity
	var nonUniqueEdges []*gen.Edge
	for _, edge := range edges {
		if !edge.Unique {
			nonUniqueEdges = append(nonUniqueEdges, edge)
		}
	}
	f.Comment("RemovedEdges returns all edge names that were removed.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("RemovedEdges").Params().Index().String().BlockFunc(func(body *jen.Group) {
		body.Id("edges").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Lit(len(nonUniqueEdges)))
		for _, edge := range nonUniqueEdges {
			body.If(jen.Id(t.Receiver()).Dot("removed" + edge.BuilderField()).Op("!=").Nil()).Block(
				jen.Id("edges").Op("=").Append(jen.Id("edges"), jen.Qual(h.EntityPkgPath(t), edge.Constant())),
			)
		}
		body.Return(jen.Id("edges"))
	})

	// RemovedIDs returns all ids that were removed for an edge (per Ent template: uses EdgesWithID, non-unique only)
	f.Comment("RemovedIDs returns all IDs (to other nodes) that were removed for the edge with")
	f.Comment("the given name in this mutation.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("RemovedIDs").Params(
		jen.Id("name").String(),
	).Index().Qual(h.VeloxPkg(), "Value").BlockFunc(func(body *jen.Group) {
		if len(nonUniqueEdges) > 0 {
			body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
				for _, edge := range nonUniqueEdges {
					sw.Case(jen.Qual(h.EntityPkgPath(t), edge.Constant())).Block(
						jen.Id("ids").Op(":=").Make(jen.Index().Qual(h.VeloxPkg(), "Value"), jen.Lit(0), jen.Len(jen.Id(t.Receiver()).Dot("removed"+edge.BuilderField()))),
						jen.For(jen.Id("id").Op(":=").Range().Id(t.Receiver()).Dot("removed"+edge.BuilderField())).Block(
							jen.Id("ids").Op("=").Append(jen.Id("ids"), jen.Id("id")),
						),
						jen.Return(jen.Id("ids")),
					)
				}
			})
		}
		body.Return(jen.Nil())
	})

	// ClearedEdges returns all edge names that were cleared (per Ent template: uses EdgesWithID)
	f.Comment("ClearedEdges returns all edge names that were cleared in this mutation.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("ClearedEdges").Params().Index().String().BlockFunc(func(body *jen.Group) {
		body.Id("edges").Op(":=").Make(jen.Index().String(), jen.Lit(0), jen.Lit(len(edges)))
		for _, edge := range edges {
			body.If(jen.Id(t.Receiver()).Dot("cleared" + edge.BuilderField())).Block(
				jen.Id("edges").Op("=").Append(jen.Id("edges"), jen.Qual(h.EntityPkgPath(t), edge.Constant())),
			)
		}
		body.Return(jen.Id("edges"))
	})

	// EdgeCleared returns if an edge was cleared (per Ent template: uses EdgesWithID)
	f.Comment("EdgeCleared returns a boolean which indicates if the edge with the given name")
	f.Comment("was cleared in this mutation.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("EdgeCleared").Params(
		jen.Id("name").String(),
	).Bool().BlockFunc(func(body *jen.Group) {
		if len(edges) > 0 {
			body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
				for _, edge := range edges {
					sw.Case(jen.Qual(h.EntityPkgPath(t), edge.Constant())).Block(
						jen.Return(jen.Id(t.Receiver()).Dot("cleared" + edge.BuilderField())),
					)
				}
			})
		}
		body.Return(jen.False())
	})

	// ClearEdge clears a unique edge (per Ent template: only unique edges)
	var uniqueEdges []*gen.Edge
	for _, edge := range edges {
		if edge.Unique {
			uniqueEdges = append(uniqueEdges, edge)
		}
	}
	f.Comment("ClearEdge clears the value of the edge with the given name. It returns an error")
	f.Comment("if that edge is not defined in the schema.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("ClearEdge").Params(
		jen.Id("name").String(),
	).Error().BlockFunc(func(body *jen.Group) {
		if len(uniqueEdges) > 0 {
			body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
				for _, edge := range uniqueEdges {
					sw.Case(jen.Qual(h.EntityPkgPath(t), edge.Constant())).Block(
						jen.Id(t.Receiver()).Dot(edge.MutationClear()).Call(),
						jen.Return(jen.Nil()),
					)
				}
			})
		}
		body.Return(jen.Qual("fmt", "Errorf").Call(
			jen.Lit("unknown "+t.Name+" unique edge %s"),
			jen.Id("name"),
		))
	})

	// ResetEdge resets all changes to an edge (per Ent template: uses EdgesWithID)
	f.Comment("ResetEdge resets all changes to the edge with the given name in this mutation.")
	f.Comment("It returns an error if the edge is not defined in the schema.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("ResetEdge").Params(
		jen.Id("name").String(),
	).Error().BlockFunc(func(body *jen.Group) {
		if len(edges) > 0 {
			body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
				for _, edge := range edges {
					sw.Case(jen.Qual(h.EntityPkgPath(t), edge.Constant())).Block(
						jen.Id(t.Receiver()).Dot(edge.MutationReset()).Call(),
						jen.Return(jen.Nil()),
					)
				}
			})
		}
		body.Return(jen.Qual("fmt", "Errorf").Call(
			jen.Lit("unknown "+t.Name+" edge %s"),
			jen.Id("name"),
		))
	})

	// OldField returns the old value of a field from the database (per Ent template: checks HasCompositeID)
	f.Comment("OldField returns the old value of the field from the database. An error is")
	f.Comment("returned if the mutation operation is not UpdateOne, or the query to the")
	f.Comment("database failed.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("OldField").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("name").String(),
	).Params(jen.Qual(h.VeloxPkg(), "Value"), jen.Error()).BlockFunc(func(body *jen.Group) {
		// Per Ent template: edge schemas with composite ID don't support getting old values
		if t.HasCompositeID() {
			body.Return(jen.Nil(), jen.Qual("errors", "New").Call(
				jen.Lit("edge schema "+t.Name+" does not support getting old values"),
			))
		} else {
			body.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
				for _, field := range t.Fields {
					if field.IsEdgeField() {
						continue
					}
					sw.Case(jen.Qual(h.EntityPkgPath(t), field.Constant())).Block(
						jen.Return(jen.Id(t.Receiver()).Dot(field.MutationGetOld()).Call(jen.Id("ctx"))),
					)
				}
			})
			body.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(
				jen.Lit("unknown "+t.Name+" field %s"),
				jen.Id("name"),
			))
		}
	})

	// AddField adds the value to a numeric field
	f.Comment("AddField adds the value to the field with the given name. It returns an error if")
	f.Comment("the field is not defined in the schema, or if the type mismatched the field")
	f.Comment("type.")
	f.Func().Params(jen.Id(t.Receiver()).Op("*").Id(mutationName)).Id("AddField").Params(
		jen.Id("name").String(),
		jen.Id("value").Qual(h.VeloxPkg(), "Value"),
	).Error().Block(
		jen.Switch(jen.Id("name")).BlockFunc(func(sw *jen.Group) {
			for _, field := range t.Fields {
				if !field.SupportsMutationAdd() {
					continue
				}
				signedType, _ := field.SignedType()
				addType := h.BaseType(field)
				if signedType != nil {
					addType = jen.Id(signedType.String())
				}
				sw.Case(jen.Qual(h.EntityPkgPath(t), field.Constant())).Block(
					jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Id("value").Op(".").Parens(addType),
					jen.If(jen.Op("!").Id("ok")).Block(
						jen.Return(jen.Qual("fmt", "Errorf").Call(
							jen.Lit("unexpected type %T for field "+field.Name),
							jen.Id("value"),
						)),
					),
					jen.Id(t.Receiver()).Dot(field.MutationAdd()).Call(jen.Id("v")),
					jen.Return(jen.Nil()),
				)
			}
		}),
		jen.Return(jen.Qual("fmt", "Errorf").Call(
			jen.Lit("unknown "+t.Name+" numeric field %s"),
			jen.Id("name"),
		)),
	)
}
