package sql

import (
	"fmt"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
	schemafield "github.com/syssam/velox/schema/field"
)

// genCreate generates the create builder file ({entity}_create.go).
func genCreate(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	f := h.NewFile(h.Pkg())

	// Create builder struct
	genCreateBuilder(h, f, t)

	// CreateBulk builder
	genCreateBulkBuilder(h, f, t)

	return f
}

func genCreateBuilder(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	builderName := t.CreateName()
	upsertEnabled := h.FeatureEnabled("sql/upsert")

	f.Commentf("%s is the builder for creating a %s entity.", builderName, t.Name)
	f.Type().Id(builderName).StructFunc(func(group *jen.Group) {
		group.Id("config")
		group.Id("mutation").Op("*").Id(t.MutationName())
		group.Id("hooks").Index().Id("Hook")
		// Conflict field (used by sql/upsert feature)
		if upsertEnabled {
			group.Id("conflict").Index().Qual(h.SQLPkg(), "ConflictOption")
		}
	})

	// Field setters - Per setter.tmpl: $fields includes ID if user-defined
	// Template: {{- else if $.ID.UserDefined }} {{ $fields = append $fields $.ID }}
	fieldsForSetters := t.Fields
	if !t.HasCompositeID() && t.HasOneFieldID() && t.ID.UserDefined {
		fieldsForSetters = append(fieldsForSetters, t.ID)
	}
	for _, field := range fieldsForSetters {
		// Skip auto-generated edge fields (FK columns), but include user-defined edge fields
		if field.IsEdgeField() && !field.UserDefined {
			continue
		}
		genCreateFieldSetter(h, f, t, field)
	}

	// Edge setters - Per setter.tmpl: uses EdgesWithID (not all Edges)
	for _, edge := range t.EdgesWithID() {
		genCreateEdgeSetter(h, f, t, edge)
	}

	// Mutation method
	f.Commentf("Mutation returns the %s.", t.MutationName())
	f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id("Mutation").Params().Op("*").Id(t.MutationName()).Block(
		jen.Return(jen.Id(t.CreateReceiver()).Dot("mutation")),
	)

	// Save method
	runtimeRequired := t.NumHooks() > 0 || t.NumPolicy() > 0
	f.Commentf("Save creates the %s in the database.", t.Name)
	f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id("Save").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Id(t.Name), jen.Error()).BlockFunc(func(grp *jen.Group) {
		// Call defaults() if type has defaults or Optional fields with standard types
		if t.NeedsDefaults() {
			if runtimeRequired {
				// defaults() returns error when hooks/policies exist
				grp.If(jen.Id("err").Op(":=").Id(t.CreateReceiver()).Dot("defaults").Call(), jen.Id("err").Op("!=").Nil()).Block(
					jen.Return(jen.Nil(), jen.Id("err")),
				)
			} else {
				grp.Id(t.CreateReceiver()).Dot("defaults").Call()
			}
		}
		grp.Return(jen.Id("withHooks").Call(
			jen.Id("ctx"),
			jen.Id(t.CreateReceiver()).Dot("sqlSave"),
			jen.Id(t.CreateReceiver()).Dot("mutation"),
			jen.Id(t.CreateReceiver()).Dot("hooks"),
		))
	})

	// SaveX method
	f.Commentf("SaveX calls Save and panics if Save returns an error.")
	f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id("SaveX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Op("*").Id(t.Name).Block(
		jen.List(jen.Id("v"), jen.Id("err")).Op(":=").Id(t.CreateReceiver()).Dot("Save").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Panic(jen.Id("err"))),
		jen.Return(jen.Id("v")),
	)

	// Exec method
	f.Commentf("Exec executes the query.")
	f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id(t.CreateReceiver()).Dot("Save").Call(jen.Id("ctx")),
		jen.Return(jen.Id("err")),
	)

	// ExecX method
	f.Commentf("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id(t.CreateReceiver()).Dot("Exec").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)

	// Feature: sql/upsert - OnConflict methods
	if upsertEnabled {
		genCreateUpsertMethods(h, f, t, builderName)
	}

	// sqlSave method - executes the SQL insert
	genCreateSQLSave(h, f, t, builderName)

	// createSpec method - builds the sqlgraph.CreateSpec
	genCreateSpec(h, f, t, builderName)

	// defaults method - sets default values of the builder before save
	if t.NeedsDefaults() {
		genCreateDefaults(h, f, t, builderName)
	}

	// check method - validates required fields and runs validators
	genCreateCheck(h, f, t, builderName)
}

// genCreateDefaults generates the defaults method for the Create builder.
func genCreateDefaults(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName string) {
	// Get fields that need defaults (including user-defined ID if applicable)
	// Per Ent template: $fields = append $fields $.ID (ID appended to END)
	fields := t.Fields
	if t.HasOneFieldID() && t.ID.UserDefined {
		fields = append(fields, t.ID)
	}

	runtimeRequired := t.NumHooks() > 0 || t.NumPolicy() > 0
	pkg := t.PackageDir()

	// Helper to generate default setting code for a field
	genFieldDefault := func(grp *jen.Group, field *gen.Field, withRuntimeCheck bool) {
		grp.If(
			jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id(t.CreateReceiver()).Dot("mutation").Dot(field.MutationGet()).Call(),
			jen.Op("!").Id("ok"),
		).BlockFunc(func(blk *jen.Group) {
			if field.Default {
				// Explicit default value
				if withRuntimeCheck && field.DefaultFunc() {
					blk.If(jen.Qual(h.EntityPkgPath(t), field.DefaultName()).Op("==").Nil()).Block(
						jen.Return(jen.Qual("fmt", "Errorf").Call(
							jen.Lit(pkg + ": uninitialized " + t.Package() + "." + field.DefaultName() + " (forgotten import " + pkg + "/runtime?)"),
						)),
					)
				}
				// v := {pkg}.{DefaultName}() or {pkg}.{DefaultName}
				if field.DefaultFunc() {
					blk.Id("v").Op(":=").Qual(h.EntityPkgPath(t), field.DefaultName()).Call()
				} else {
					blk.Id("v").Op(":=").Qual(h.EntityPkgPath(t), field.DefaultName())
				}
			} else {
				// Optional field with standard type: use Go zero value
				blk.Id("v").Op(":=").Add(baseZeroValue(h, field))
			}
			// c.mutation.{MutationSet}(v)
			blk.Id(t.CreateReceiver()).Dot("mutation").Dot(field.MutationSet()).Call(jen.Id("v"))
		})
	}

	// Check if field needs default: explicit Default OR Optional with standard/custom type
	fieldNeedsDefault := func(field *gen.Field) bool {
		if field.Default {
			return true
		}
		// Optional fields with standard types or custom types (TypeOther) need zero value defaults
		// Only TypeEnum is excluded (requires explicit Default)
		if field.Optional && !field.Nillable && field.Type != nil && (field.Type.Type.IsStandardType() || field.Type.Type == schemafield.TypeOther) {
			return true
		}
		return false
	}

	f.Comment("defaults sets the default values of the builder before save.")
	if runtimeRequired {
		// Return error when hooks/policies exist
		f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id("defaults").Params().Error().BlockFunc(func(grp *jen.Group) {
			for _, field := range fields {
				if fieldNeedsDefault(field) {
					genFieldDefault(grp, field, true)
				}
			}
			grp.Return(jen.Nil())
		})
	} else {
		// No return when no hooks/policies
		f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id("defaults").Params().BlockFunc(func(grp *jen.Group) {
			for _, field := range fields {
				if fieldNeedsDefault(field) {
					genFieldDefault(grp, field, false)
				}
			}
		})
	}
}

// genCreateCheck generates the check method for the Create builder.
func genCreateCheck(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName string) {
	// Get fields to check (including user-defined ID appended to END per Ent template)
	fields := t.Fields
	if t.HasOneFieldID() && t.ID.UserDefined {
		fields = append(fields, t.ID)
	}

	// Check if we have multiple dialects configured (for dialect-specific field requirements)
	configuredDialects := 0
	if g := h.Graph(); g != nil && g.Config != nil && g.Config.Storage != nil {
		configuredDialects = len(g.Config.Storage.Dialects)
	}

	// Check if validators feature is enabled
	validatorsEnabled, _ := h.Graph().Config.FeatureEnabled(gen.FeatureValidator.Name)

	f.Comment("check runs all checks and user-defined validators on the builder.")
	f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id("check").Params().Error().BlockFunc(func(grp *jen.Group) {
		// Check required fields
		for _, field := range fields {
			// Skip ID field check for single field ID
			if t.HasOneFieldID() && field.Name == t.ID.Name {
				continue
			}
			if !field.Optional {
				// Get dialects where this field is required (handles multi-dialect scenarios)
				requiredDialects := field.RequiredFor()
				numRequired := len(requiredDialects)

				if numRequired > 0 && configuredDialects > 1 {
					// Multi-dialect mode: field is required in some/all dialects
					partially := numRequired != configuredDialects

					if partially {
						// Field is partially required (only in some dialects)
						// Generate dialect-specific check with switch statement
						grp.If(
							jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id(t.CreateReceiver()).Dot("mutation").Dot(field.MutationGet()).Call(),
							jen.Op("!").Id("ok"),
						).BlockFunc(func(blk *jen.Group) {
							blk.Switch(jen.Id(t.CreateReceiver()).Dot("driver").Dot("Dialect").Call()).BlockFunc(func(sw *jen.Group) {
								for _, d := range requiredDialects {
									sw.Case(jen.Qual(dialectPkg(), d)).Block(
										jen.Return(jen.Op("&").Id("ValidationError").Values(jen.Dict{
											jen.Id("Name"): jen.Lit(field.Name),
											jen.Id("err"):  jen.Qual("errors", "New").Call(jen.Lit("missing required field \"" + t.Name + "." + field.Name + "\"")),
										})),
									)
								}
							})
						})
					} else {
						// Field is required in all configured dialects
						grp.If(
							jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id(t.CreateReceiver()).Dot("mutation").Dot(field.MutationGet()).Call(),
							jen.Op("!").Id("ok"),
						).Block(
							jen.Return(jen.Op("&").Id("ValidationError").Values(jen.Dict{
								jen.Id("Name"): jen.Lit(field.Name),
								jen.Id("err"):  jen.Qual("errors", "New").Call(jen.Lit("missing required field \"" + t.Name + "." + field.Name + "\"")),
							})),
						)
					}
				} else if numRequired == 0 && configuredDialects > 1 {
					// Multi-dialect mode: field has database defaults in all dialects, skip check
				} else {
					// Single-dialect mode (default): use simple check
					grp.If(
						jen.List(jen.Id("_"), jen.Id("ok")).Op(":=").Id(t.CreateReceiver()).Dot("mutation").Dot(field.MutationGet()).Call(),
						jen.Op("!").Id("ok"),
					).Block(
						jen.Return(jen.Op("&").Id("ValidationError").Values(jen.Dict{
							jen.Id("Name"): jen.Lit(field.Name),
							jen.Id("err"):  jen.Qual("errors", "New").Call(jen.Lit("missing required field \"" + t.Name + "." + field.Name + "\"")),
						})),
					)
				}
			}

			// Run field validators if any (enums automatically have validators)
			// Per Ent template: $isValidator := and ($f.HasGoType) ($f.Type.Validator)
			// Enter validation block if: (validatorsEnabled && (Validators > 0 || IsEnum)) OR (HasGoType && Type.Validator)
			isValidator := field.HasGoType() && field.Type != nil && field.Type.Validator()
			if (validatorsEnabled && (field.Validators > 0 || field.IsEnum())) || isValidator {
				grp.If(
					jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Id(t.CreateReceiver()).Dot("mutation").Dot(field.MutationGet()).Call(),
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
		}

		// Check required edges
		for _, edge := range t.EdgesWithID() {
			if !edge.Optional {
				// if len(c.mutation.{StructField}IDs()) == 0 { return &ValidationError{...} }
				grp.If(
					jen.Len(jen.Id(t.CreateReceiver()).Dot("mutation").Dot(edge.StructField() + "IDs").Call()).Op("==").Lit(0),
				).Block(
					jen.Return(jen.Op("&").Id("ValidationError").Values(jen.Dict{
						jen.Id("Name"): jen.Lit(edge.Name),
						jen.Id("err"):  jen.Qual("errors", "New").Call(jen.Lit("missing required edge \"" + t.Name + "." + edge.Name + "\"")),
					})),
				)
			}
		}

		grp.Return(jen.Nil())
	})
}

func genCreateFieldSetter(h gen.GeneratorHelper, f *jen.File, t *gen.Type, field *gen.Field) {
	builderName := t.CreateName()
	setterName := "Set" + field.StructField()

	f.Commentf("%s sets the %q field.", setterName, field.Name)
	f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id(setterName).Params(
		jen.Id("v").Add(h.BaseType(field)),
	).Op("*").Id(builderName).Block(
		jen.Id(t.CreateReceiver()).Dot("mutation").Dot(setterName).Call(jen.Id("v")),
		jen.Return(jen.Id(t.CreateReceiver())),
	)

	// SetNillable for creator: generated when (Optional || Default) AND type is not already nillable
	// Template: $nillableC := and $creator (or $f.Optional $f.Default)
	// Template: if and (not $f.Type.Nillable) (not $skipNillable) (or $nillableC $nillableU)
	nillableC := field.Optional || field.Default
	typeNillable := field.Type != nil && field.Type.Nillable

	// Check for naming collision (skip if another field's MutationSet matches the nillable func name)
	// Per Ent template: collision check uses $fields which includes ID if user-defined
	nillableName := "SetNillable" + field.StructField()
	skipNillable := false
	// Build fields list with ID if user-defined (same as template $fields)
	fieldsForCheck := t.Fields
	if t.HasOneFieldID() && t.ID.UserDefined {
		fieldsForCheck = append(fieldsForCheck, t.ID)
	}
	for _, otherField := range fieldsForCheck {
		if otherField.Name != field.Name && otherField.MutationSet() == nillableName {
			skipNillable = true
			break
		}
	}

	if nillableC && !typeNillable && !skipNillable {
		f.Commentf("%s sets the %q field if not nil.", nillableName, field.Name)
		f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id(nillableName).Params(
			jen.Id("v").Op("*").Add(h.BaseType(field)),
		).Op("*").Id(builderName).Block(
			jen.If(jen.Id("v").Op("!=").Nil()).Block(
				jen.Id(t.CreateReceiver()).Dot(setterName).Call(jen.Op("*").Id("v")),
			),
			jen.Return(jen.Id(t.CreateReceiver())),
		)
	}
}

func genCreateEdgeSetter(h gen.GeneratorHelper, f *jen.File, t *gen.Type, edge *gen.Edge) {
	builderName := t.CreateName()

	// Per Ent template: $withSetter := not $e.HasFieldSetter
	// Skip generating setters if edge already has a field setter (edge-field with same name)
	withSetter := !edge.HasFieldSetter()

	if edge.Unique {
		// SetXxxID for unique edges (only if no field setter)
		setterName := edge.MutationSet() // e.g., "SetOwnerID"
		if withSetter {
			f.Commentf("%s sets the %q edge to %s by ID.", setterName, edge.Name, edge.Type.Name)
			f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id(setterName).Params(
				jen.Id("id").Add(h.IDType(edge.Type)),
			).Op("*").Id(builderName).Block(
				jen.Id(t.CreateReceiver()).Dot("mutation").Dot(setterName).Call(jen.Id("id")),
				jen.Return(jen.Id(t.CreateReceiver())),
			)
		}

		// SetNillable{Edge}ID for unique optional edges (only if withSetter)
		// Per Ent template: {{ if and $e.Unique $e.Optional $withSetter }}
		if edge.Optional && withSetter {
			nillableSetterName := "SetNillable" + edge.StructField() + "ID"
			f.Commentf("%s sets the %q edge to %s by ID if not nil.", nillableSetterName, edge.Name, edge.Type.Name)
			f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id(nillableSetterName).Params(
				jen.Id("id").Op("*").Add(h.IDType(edge.Type)),
			).Op("*").Id(builderName).Block(
				jen.If(jen.Id("id").Op("!=").Nil()).Block(
					jen.Id(t.CreateReceiver()).Op("=").Id(t.CreateReceiver()).Dot(setterName).Call(jen.Op("*").Id("id")),
				),
				jen.Return(jen.Id(t.CreateReceiver())),
			)
		}

		// SetXxx sets the edge entity
		entitySetter := "Set" + edge.StructField()
		f.Commentf("%s sets the %q edge to %s.", entitySetter, edge.Name, edge.Type.Name)
		f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id(entitySetter).Params(
			jen.Id("v").Op("*").Id(edge.Type.Name),
		).Op("*").Id(builderName).BlockFunc(func(grp *jen.Group) {
			if withSetter {
				// Call the builder method
				grp.Return(jen.Id(t.CreateReceiver()).Dot(setterName).Call(jen.Id("v").Dot("ID")))
			} else {
				// Call mutation directly when there's no builder setter (field setter exists)
				grp.Id(t.CreateReceiver()).Dot("mutation").Dot(setterName).Call(jen.Id("v").Dot("ID"))
				grp.Return(jen.Id(t.CreateReceiver()))
			}
		})
	} else {
		// AddXxxIDs for non-unique edges (only if no field setter)
		adderName := edge.MutationAdd() // e.g., "AddVariantIDs" (singularized)
		if withSetter {
			f.Commentf("%s adds the %q edge to %s by IDs.", adderName, edge.Name, edge.Type.Name)
			f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id(adderName).Params(
				jen.Id("ids").Op("...").Add(h.IDType(edge.Type)),
			).Op("*").Id(builderName).Block(
				jen.Id(t.CreateReceiver()).Dot("mutation").Dot(adderName).Call(jen.Id("ids").Op("...")),
				jen.Return(jen.Id(t.CreateReceiver())),
			)
		}

		// AddXxx adds edge entities
		entityAdder := "Add" + edge.StructField()
		f.Commentf("%s adds the %q edge to %s.", entityAdder, edge.Name, edge.Type.Name)
		f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id(entityAdder).Params(
			jen.Id("v").Op("...").Op("*").Id(edge.Type.Name),
		).Op("*").Id(builderName).Block(
			jen.Id("ids").Op(":=").Make(jen.Index().Add(h.IDType(edge.Type)), jen.Len(jen.Id("v"))),
			jen.For(jen.Id("i").Op(":=").Range().Id("v")).Block(
				jen.Id("ids").Index(jen.Id("i")).Op("=").Id("v").Index(jen.Id("i")).Dot("ID"),
			),
			jen.Return(jen.Id(t.CreateReceiver()).Dot(adderName).Call(jen.Id("ids").Op("..."))),
		)
	}
}

func genCreateBulkBuilder(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	bulkName := t.CreateBulkName()
	upsertEnabled := h.FeatureEnabled("sql/upsert")

	f.Commentf("%s is the builder for creating many %s entities in bulk.", bulkName, t.Name)
	f.Type().Id(bulkName).StructFunc(func(group *jen.Group) {
		group.Id("config")
		group.Id("err").Error()
		group.Id("builders").Index().Op("*").Id(t.CreateName())
		// Conflict field (used by sql/upsert feature)
		if upsertEnabled {
			group.Id("conflict").Index().Qual(h.SQLPkg(), "ConflictOption")
		}
	})

	// Save method - uses sqlgraph.BatchCreate for performance
	genCreateBulkSave(h, f, t, bulkName)

	// SaveX method
	f.Commentf("SaveX is like Save, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.CreateBulReceiver()).Op("*").Id(bulkName)).Id("SaveX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Index().Op("*").Id(t.Name).Block(
		jen.List(jen.Id("v"), jen.Id("err")).Op(":=").Id(t.CreateBulReceiver()).Dot("Save").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(jen.Panic(jen.Id("err"))),
		jen.Return(jen.Id("v")),
	)

	// Exec method
	f.Commentf("Exec executes the query.")
	f.Func().Params(jen.Id(t.CreateBulReceiver()).Op("*").Id(bulkName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id(t.CreateBulReceiver()).Dot("Save").Call(jen.Id("ctx")),
		jen.Return(jen.Id("err")),
	)

	// ExecX method
	f.Commentf("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id(t.CreateBulReceiver()).Op("*").Id(bulkName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id(t.CreateBulReceiver()).Dot("Exec").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)

	// Feature: sql/upsert - OnConflict methods for bulk
	if upsertEnabled {
		genCreateBulkUpsertMethods(h, f, t, bulkName)
	}
}

// genCreateUpsertMethods generates OnConflict methods for the Create builder.
// This is part of the sql/upsert feature.
func genCreateUpsertMethods(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName string) {
	upsertOneName := t.Name + "UpsertOne"
	upsertSetName := t.Name + "Upsert"
	entityPkg := t.PackageDir()

	// OnConflict method
	f.Comment("OnConflict allows configuring the `ON CONFLICT` / `ON DUPLICATE KEY` clause")
	f.Comment("of the `INSERT` statement.")
	f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id("OnConflict").Params(
		jen.Id("opts").Op("...").Qual(h.SQLPkg(), "ConflictOption"),
	).Op("*").Id(upsertOneName).Block(
		jen.Id(t.CreateReceiver()).Dot("conflict").Op("=").Id("opts"),
		jen.Return(jen.Op("&").Id(upsertOneName).Values(jen.Dict{
			jen.Id("create"): jen.Id(t.CreateReceiver()),
		})),
	)

	// OnConflictColumns method
	f.Comment("OnConflictColumns calls `OnConflict` and configures the columns")
	f.Comment("as conflict target.")
	f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id("OnConflictColumns").Params(
		jen.Id("columns").Op("...").String(),
	).Op("*").Id(upsertOneName).Block(
		jen.Id(t.CreateReceiver()).Dot("conflict").Op("=").Append(
			jen.Id(t.CreateReceiver()).Dot("conflict"),
			jen.Qual(h.SQLPkg(), "ConflictColumns").Call(jen.Id("columns").Op("...")),
		),
		jen.Return(jen.Op("&").Id(upsertOneName).Values(jen.Dict{
			jen.Id("create"): jen.Id(t.CreateReceiver()),
		})),
	)

	// UpsertOne type
	f.Commentf("%s is the builder for \"upsert\"-ing one %s node.", upsertOneName, t.Name)
	f.Type().Id(upsertOneName).Struct(
		jen.Id("create").Op("*").Id(builderName),
	)

	// Upsert type (the setter)
	f.Commentf("%s is the \"OnConflict\" setter.", upsertSetName)
	f.Type().Id(upsertSetName).Struct(
		jen.Op("*").Qual(h.SQLPkg(), "UpdateSet"),
	)

	// SetXxx methods on Upsert
	for _, field := range t.MutableFields() {
		structField := field.StructField()
		setter := "Set" + structField

		// SetXxx
		f.Commentf("%s sets the %q field.", setter, field.Name)
		f.Func().Params(jen.Id("u").Op("*").Id(upsertSetName)).Id(setter).Params(
			jen.Id("v").Add(h.BaseType(field)),
		).Op("*").Id(upsertSetName).Block(
			jen.Id("u").Dot("Set").Call(jen.Qual(h.EntityPkgPath(t), field.Constant()), jen.Id("v")),
			jen.Return(jen.Id("u")),
		)

		// UpdateXxx
		updater := "Update" + structField
		f.Commentf("%s sets the %q field to the value that was provided on create.", updater, field.Name)
		f.Func().Params(jen.Id("u").Op("*").Id(upsertSetName)).Id(updater).Params().Op("*").Id(upsertSetName).Block(
			jen.Id("u").Dot("SetExcluded").Call(jen.Qual(h.EntityPkgPath(t), field.Constant())),
			jen.Return(jen.Id("u")),
		)

		// ClearXxx for nillable fields only (can set NULL in DB)
		// Optional fields have NOT NULL constraint, they can't be cleared
		if field.Nillable {
			clearer := "Clear" + structField
			f.Commentf("%s clears the value of the %q field.", clearer, field.Name)
			f.Func().Params(jen.Id("u").Op("*").Id(upsertSetName)).Id(clearer).Params().Op("*").Id(upsertSetName).Block(
				jen.Id("u").Dot("SetNull").Call(jen.Qual(h.EntityPkgPath(t), field.Constant())),
				jen.Return(jen.Id("u")),
			)
		}
	}

	// UpdateNewValues on UpsertOne
	f.Comment("UpdateNewValues updates the mutable fields using the new values that were set on create.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertOneName)).Id("UpdateNewValues").Params().Op("*").Id(upsertOneName).Block(
		jen.Id("u").Dot("create").Dot("conflict").Op("=").Append(
			jen.Id("u").Dot("create").Dot("conflict"),
			jen.Qual(h.SQLPkg(), "ResolveWithNewValues").Call(),
		),
		jen.Return(jen.Id("u")),
	)

	// Ignore on UpsertOne
	f.Comment("Ignore sets each column to itself in case of conflict.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertOneName)).Id("Ignore").Params().Op("*").Id(upsertOneName).Block(
		jen.Id("u").Dot("create").Dot("conflict").Op("=").Append(
			jen.Id("u").Dot("create").Dot("conflict"),
			jen.Qual(h.SQLPkg(), "ResolveWithIgnore").Call(),
		),
		jen.Return(jen.Id("u")),
	)

	// DoNothing on UpsertOne
	f.Comment("DoNothing configures the conflict_action to `DO NOTHING`.")
	f.Comment("Supported only by SQLite and PostgreSQL.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertOneName)).Id("DoNothing").Params().Op("*").Id(upsertOneName).Block(
		jen.Id("u").Dot("create").Dot("conflict").Op("=").Append(
			jen.Id("u").Dot("create").Dot("conflict"),
			jen.Qual(h.SQLPkg(), "DoNothing").Call(),
		),
		jen.Return(jen.Id("u")),
	)

	// Update on UpsertOne
	f.Comment("Update allows overriding fields `UPDATE` values.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertOneName)).Id("Update").Params(
		jen.Id("set").Func().Params(jen.Op("*").Id(upsertSetName)),
	).Op("*").Id(upsertOneName).Block(
		jen.Id("u").Dot("create").Dot("conflict").Op("=").Append(
			jen.Id("u").Dot("create").Dot("conflict"),
			jen.Qual(h.SQLPkg(), "ResolveWith").Call(
				jen.Func().Params(jen.Id("update").Op("*").Qual(h.SQLPkg(), "UpdateSet")).Block(
					jen.Id("set").Call(jen.Op("&").Id(upsertSetName).Values(jen.Dict{
						jen.Id("UpdateSet"): jen.Id("update"),
					})),
				),
			),
		),
		jen.Return(jen.Id("u")),
	)

	// Exec on UpsertOne
	f.Comment("Exec executes the query.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertOneName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.If(jen.Len(jen.Id("u").Dot("create").Dot("conflict")).Op("==").Lit(0)).Block(
			jen.Return(jen.Qual("errors", "New").Call(jen.Lit(entityPkg+": missing options for "+builderName+".OnConflict"))),
		),
		jen.Return(jen.Id("u").Dot("create").Dot("Exec").Call(jen.Id("ctx"))),
	)

	// ExecX on UpsertOne
	f.Comment("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertOneName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id("u").Dot("create").Dot("Exec").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)

	// ID on UpsertOne (returns the ID)
	f.Comment("ID returns the inserted/updated ID.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertOneName)).Id("ID").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Id("id").Add(h.IDType(t)), jen.Id("err").Error()).Block(
		jen.Id("node").Op(",").Id("err").Op(":=").Id("u").Dot("create").Dot("Save").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("id"), jen.Id("err")),
		),
		jen.Return(jen.Id("node").Dot("ID"), jen.Nil()),
	)

	// IDX on UpsertOne
	f.Comment("IDX is like ID, but panics if an error occurs.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertOneName)).Id("IDX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Add(h.IDType(t)).Block(
		jen.List(jen.Id("id"), jen.Id("err")).Op(":=").Id("u").Dot("ID").Call(jen.Id("ctx")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("id")),
	)
}

// genCreateSQLSave generates the sqlSave method that executes the SQL insert.
func genCreateSQLSave(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName string) {
	hasValueScanner := t.HasValueScanner()

	f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id("sqlSave").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Op("*").Id(t.Name), jen.Error()).BlockFunc(func(grp *jen.Group) {
		// Call check() to validate
		grp.If(jen.Id("err").Op(":=").Id(t.CreateReceiver()).Dot("check").Call(), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		)

		// Call createSpec() - returns (_node, _spec) or (_node, _spec, err) if HasValueScanner
		if hasValueScanner {
			grp.List(jen.Id("_node"), jen.Id("_spec"), jen.Id("err")).Op(":=").Id(t.CreateReceiver()).Dot("createSpec").Call()
			grp.If(jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			)
		} else {
			grp.List(jen.Id("_node"), jen.Id("_spec")).Op(":=").Id(t.CreateReceiver()).Dot("createSpec").Call()
		}

		// Call sqlgraph.CreateNode
		grp.If(
			jen.Id("err").Op(":=").Qual(h.SQLGraphPkg(), "CreateNode").Call(
				jen.Id("ctx"),
				jen.Id(t.CreateReceiver()).Dot("driver"),
				jen.Id("_spec"),
			),
			jen.Id("err").Op("!=").Nil(),
		).Block(
			jen.If(jen.Qual(h.SQLGraphPkg(), "IsConstraintError").Call(jen.Id("err"))).Block(
				jen.Id("err").Op("=").Op("&").Id("ConstraintError").Values(jen.Dict{
					jen.Id("msg"):  jen.Id("err").Dot("Error").Call(),
					jen.Id("wrap"): jen.Id("err"),
				}),
			),
			jen.Return(jen.Nil(), jen.Id("err")),
		)

		// Handle ID assignment after successful insert
		if !t.HasCompositeID() && t.HasOneFieldID() {
			genCreateIDAssignment(h, grp, t)
		}

		// Mark mutation as done and set ID
		if t.HasOneFieldID() {
			grp.Id(t.CreateReceiver()).Dot("mutation").Dot(t.ID.BuilderField()).Op("=").Op("&").Id("_node").Dot(t.ID.StructField())
			grp.Id(t.CreateReceiver()).Dot("mutation").Dot("done").Op("=").True()
		}

		grp.Return(jen.Id("_node"), jen.Nil())
	})
}

// genCreateIDAssignment generates the ID assignment logic after CreateNode.
func genCreateIDAssignment(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type) {
	id := t.ID
	// Handle different ID type scenarios
	switch {
	case id.Type.ValueScanner() && !id.Type.RType.IsPtr():
		// ValueScanner with non-pointer type (e.g. UUID)
		grp.If(jen.Id("_spec").Dot("ID").Dot("Value").Op("!=").Nil()).BlockFunc(func(blk *jen.Group) {
			blk.If(
				jen.List(jen.Id("id"), jen.Id("ok")).Op(":=").Id("_spec").Dot("ID").Dot("Value").Op(".").Parens(jen.Op("*").Add(h.BaseType(id))),
				jen.Id("ok"),
			).Block(
				jen.Id("_node").Dot("ID").Op("=").Op("*").Id("id"),
			).Else().If(
				jen.Id("err").Op(":=").Id("_node").Dot("ID").Dot("Scan").Call(jen.Id("_spec").Dot("ID").Dot("Value")),
				jen.Id("err").Op("!=").Nil(),
			).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			)
		})
	case id.Type.ValueScanner():
		// ValueScanner with pointer type
		grp.If(jen.Id("_spec").Dot("ID").Dot("Value").Op("!=").Nil()).BlockFunc(func(blk *jen.Group) {
			blk.If(
				jen.List(jen.Id("id"), jen.Id("ok")).Op(":=").Id("_spec").Dot("ID").Dot("Value").Op(".").Parens(h.BaseType(id)),
				jen.Id("ok"),
			).Block(
				jen.Id("_node").Dot("ID").Op("=").Id("id"),
			).Else().If(
				jen.Id("err").Op(":=").Id("_node").Dot("ID").Dot("Scan").Call(jen.Id("_spec").Dot("ID").Dot("Value")),
				jen.Id("err").Op("!=").Nil(),
			).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			)
		})
	case !id.Type.Numeric():
		// Non-numeric types (string, UUID, bytes, etc.) - must be supplied by user
		grp.If(jen.Id("_spec").Dot("ID").Dot("Value").Op("!=").Nil()).BlockFunc(func(blk *jen.Group) {
			blk.If(
				jen.List(jen.Id("id"), jen.Id("ok")).Op(":=").Id("_spec").Dot("ID").Dot("Value").Op(".").Parens(h.BaseType(id)),
				jen.Id("ok"),
			).Block(
				jen.Id("_node").Dot("ID").Op("=").Id("id"),
			).Else().Block(
				jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(
					jen.Lit("unexpected "+t.Name+".ID type: %T"),
					jen.Id("_spec").Dot("ID").Dot("Value"),
				)),
			)
		})
	default:
		// Numeric types - auto-increment or user-defined
		// Use safe type assertion to avoid panic if Value is nil or wrong type
		if id.UserDefined {
			grp.If(jen.Id("_spec").Dot("ID").Dot("Value").Op("!=").Nil()).BlockFunc(func(blk *jen.Group) {
				blk.If(
					jen.List(jen.Id("id"), jen.Id("ok")).Op(":=").Id("_spec").Dot("ID").Dot("Value").Op(".").Parens(jen.Int64()),
					jen.Id("ok"),
				).Block(
					jen.Id("_node").Dot("ID").Op("=").Add(h.BaseType(id)).Call(jen.Id("id")),
				)
			})
		} else {
			// Auto-increment: the database returns the ID, use safe type assertion
			grp.If(jen.Id("_spec").Dot("ID").Dot("Value").Op("!=").Nil()).BlockFunc(func(blk *jen.Group) {
				blk.If(
					jen.List(jen.Id("id"), jen.Id("ok")).Op(":=").Id("_spec").Dot("ID").Dot("Value").Op(".").Parens(jen.Int64()),
					jen.Id("ok"),
				).Block(
					jen.Id("_node").Dot("ID").Op("=").Add(h.BaseType(id)).Call(jen.Id("id")),
				)
			})
		}
	}
}

// genCreateSpec generates the createSpec method that builds the sqlgraph.CreateSpec.
func genCreateSpec(h gen.GeneratorHelper, f *jen.File, t *gen.Type, builderName string) {
	hasValueScanner := t.HasValueScanner()

	// Return type: (*Entity, *sqlgraph.CreateSpec) or (*Entity, *sqlgraph.CreateSpec, error)
	var returnType jen.Code
	if hasValueScanner {
		returnType = jen.Params(
			jen.Op("*").Id(t.Name),
			jen.Op("*").Qual(h.SQLGraphPkg(), "CreateSpec"),
			jen.Error(),
		)
	} else {
		returnType = jen.Params(
			jen.Op("*").Id(t.Name),
			jen.Op("*").Qual(h.SQLGraphPkg(), "CreateSpec"),
		)
	}

	f.Func().Params(jen.Id(t.CreateReceiver()).Op("*").Id(builderName)).Id("createSpec").Params().Add(returnType).BlockFunc(func(grp *jen.Group) {
		// Initialize _node and _spec
		grp.Var().Defs(
			jen.Id("_node").Op("=").Op("&").Id(t.Name).Values(jen.Dict{
				jen.Id("config"): jen.Id(t.CreateReceiver()).Dot("config"),
			}),
			jen.Id("_spec").Op("=").Qual(h.SQLGraphPkg(), "NewCreateSpec").CallFunc(func(call *jen.Group) {
				call.Qual(h.EntityPkgPath(t), "Table")
				if t.HasOneFieldID() {
					call.Qual(h.SQLGraphPkg(), "NewFieldSpec").Call(
						jen.Qual(h.EntityPkgPath(t), t.ID.Constant()),
						jen.Qual(schemaPkg(), t.ID.Type.ConstName()),
					)
				} else {
					call.Nil()
				}
			}),
		)

		// Handle user-defined ID
		if !t.HasCompositeID() && t.HasOneFieldID() && t.ID.UserDefined {
			grp.If(
				jen.List(jen.Id("id"), jen.Id("ok")).Op(":=").Id(t.CreateReceiver()).Dot("mutation").Dot(t.ID.MutationGet()).Call(),
				jen.Id("ok"),
			).Block(
				jen.Id("_node").Dot("ID").Op("=").Id("id"),
				jen.Id("_spec").Dot("ID").Dot("Value").Op("=").Add(genIDValue(h, t)).Id("id"),
			)
		}

		// Handle mutation fields
		for _, field := range t.MutationFields() {
			if field.HasValueScanner() {
				valueFn, _ := field.ValueFunc()
				grp.If(
					jen.List(jen.Id("value"), jen.Id("ok")).Op(":=").Id(t.CreateReceiver()).Dot("mutation").Dot(field.MutationGet()).Call(),
					jen.Id("ok"),
				).BlockFunc(func(blk *jen.Group) {
					blk.List(jen.Id("vv"), jen.Id("err")).Op(":=").Id(valueFn).Call(jen.Id("value"))
					blk.If(jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Nil(), jen.Id("err")),
					)
					blk.Id("_spec").Dot("SetField").Call(
						jen.Qual(h.EntityPkgPath(t), field.Constant()),
						jen.Qual(schemaPkg(), field.Type.ConstName()),
						jen.Id("vv"),
					)
					// Set the node field value
					if field.NillableValue() {
						blk.Id("_node").Dot(field.StructField()).Op("=").Op("&").Id("value")
					} else {
						blk.Id("_node").Dot(field.StructField()).Op("=").Id("value")
					}
				})
			} else {
				grp.If(
					jen.List(jen.Id("value"), jen.Id("ok")).Op(":=").Id(t.CreateReceiver()).Dot("mutation").Dot(field.MutationGet()).Call(),
					jen.Id("ok"),
				).Block(
					jen.Id("_spec").Dot("SetField").Call(
						jen.Qual(h.EntityPkgPath(t), field.Constant()),
						jen.Qual(schemaPkg(), field.Type.ConstName()),
						jen.Id("value"),
					),
					// Set the node field value
					jen.Do(func(s *jen.Statement) {
						if field.NillableValue() {
							s.Id("_node").Dot(field.StructField()).Op("=").Op("&").Id("value")
						} else {
							s.Id("_node").Dot(field.StructField()).Op("=").Id("value")
						}
					}),
				)
			}
		}

		// Handle edges
		for _, edge := range t.EdgesWithID() {
			grp.If(
				jen.Id("nodes").Op(":=").Id(t.CreateReceiver()).Dot("mutation").Dot(edge.StructField()+"IDs").Call(),
				jen.Len(jen.Id("nodes")).Op(">").Lit(0),
			).BlockFunc(func(blk *jen.Group) {
				genCreateEdgeSpec(h, blk, t, edge)
				// Set FK field on node if edge owns the FK
				if edge.OwnFK() {
					fk, err := edge.ForeignKey()
					if err == nil {
						if fk.Field.NillableValue() {
							blk.Id("_node").Dot(fk.StructField()).Op("=").Op("&").Id("nodes").Index(jen.Lit(0))
						} else {
							blk.Id("_node").Dot(fk.StructField()).Op("=").Id("nodes").Index(jen.Lit(0))
						}
					}
				}
				blk.Id("_spec").Dot("Edges").Op("=").Append(
					jen.Id("_spec").Dot("Edges"),
					jen.Id("edge"),
				)
			})
		}

		// Return statement
		if hasValueScanner {
			grp.Return(jen.Id("_node"), jen.Id("_spec"), jen.Nil())
		} else {
			grp.Return(jen.Id("_node"), jen.Id("_spec"))
		}
	})
}

// genIDValue generates the ID value expression for CreateSpec.
// For ValueScanner types that are not pointers, we need to take the address.
func genIDValue(_ gen.GeneratorHelper, t *gen.Type) jen.Code {
	if t.ID.Type.ValueScanner() && !t.ID.Type.RType.IsPtr() {
		return jen.Op("&")
	}
	return jen.Empty()
}

// genCreateEdgeSpec generates the edge spec for create operations.
func genCreateEdgeSpec(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, edge *gen.Edge) {
	// Validate that edge target type has an ID field
	if edge.Type.ID == nil {
		panic(fmt.Sprintf("velox/gen: cannot generate edge spec for %q: related type %q has no ID field (view type?)", edge.Name, edge.Type.Name))
	}

	// Build columns expression based on edge type
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

	// Add nodes to the edge target
	grp.For(jen.List(jen.Id("_"), jen.Id("k")).Op(":=").Range().Id("nodes")).Block(
		jen.Id("edge").Dot("Target").Dot("Nodes").Op("=").Append(
			jen.Id("edge").Dot("Target").Dot("Nodes"),
			jen.Id("k"),
		),
	)
}

// genCreateBulkSave generates the Save method for CreateBulk builder.
// It uses sqlgraph.BatchCreate for efficient bulk inserts with proper hook chain support.
func genCreateBulkSave(h gen.GeneratorHelper, f *jen.File, t *gen.Type, bulkName string) {
	hasValueScanner := t.HasValueScanner()

	f.Commentf("Save creates the %s entities in the database.", t.Name)
	f.Func().Params(jen.Id(t.CreateBulReceiver()).Op("*").Id(bulkName)).Id("Save").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Params(jen.Index().Op("*").Id(t.Name), jen.Error()).BlockFunc(func(grp *jen.Group) {
		// Check for initialization errors (set by MapCreateBulk)
		grp.If(jen.Id(t.CreateBulReceiver()).Dot("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id(t.CreateBulReceiver()).Dot("err")),
		)

		// Initialize specs, nodes, and mutators slices
		grp.Id("specs").Op(":=").Make(
			jen.Index().Op("*").Qual(h.SQLGraphPkg(), "CreateSpec"),
			jen.Len(jen.Id(t.CreateBulReceiver()).Dot("builders")),
		)
		grp.Id("nodes").Op(":=").Make(
			jen.Index().Op("*").Id(t.Name),
			jen.Len(jen.Id(t.CreateBulReceiver()).Dot("builders")),
		)
		grp.Id("mutators").Op(":=").Make(
			jen.Index().Id("Mutator"),
			jen.Len(jen.Id(t.CreateBulReceiver()).Dot("builders")),
		)

		// Build mutator chain for each builder
		grp.For(jen.Id("i").Op(":=").Range().Id(t.CreateBulReceiver()).Dot("builders")).BlockFunc(func(forBlock *jen.Group) {
			forBlock.Func().Params(
				jen.Id("i").Int(),
				jen.Id("root").Qual("context", "Context"),
			).BlockFunc(func(funcBlock *jen.Group) {
				funcBlock.Id("builder").Op(":=").Id(t.CreateBulReceiver()).Dot("builders").Index(jen.Id("i"))

				// Call defaults if the type has defaults
				if t.HasDefault() {
					funcBlock.Id("builder").Dot("defaults").Call()
				}

				// Create the mutator function
				funcBlock.Var().Id("mut").Id("Mutator").Op("=").Id("MutateFunc").Call(
					jen.Func().Params(
						jen.Id("ctx").Qual("context", "Context"),
						jen.Id("m").Id("Mutation"),
					).Params(jen.Id("Value"), jen.Error()).BlockFunc(func(mutBlock *jen.Group) {
						// Type assert mutation
						mutBlock.List(jen.Id("mutation"), jen.Id("ok")).Op(":=").Id("m").Op(".").Parens(jen.Op("*").Id(t.MutationName()))
						mutBlock.If(jen.Op("!").Id("ok")).Block(
							jen.Return(jen.Nil(), jen.Qual("fmt", "Errorf").Call(
								jen.Lit("unexpected mutation type %T"),
								jen.Id("m"),
							)),
						)

						// Run check
						mutBlock.If(jen.Id("err").Op(":=").Id("builder").Dot("check").Call(), jen.Id("err").Op("!=").Nil()).Block(
							jen.Return(jen.Nil(), jen.Id("err")),
						)

						// Set mutation on builder
						mutBlock.Id("builder").Dot("mutation").Op("=").Id("mutation")

						// Call createSpec
						mutBlock.Var().Id("err").Error()
						if hasValueScanner {
							mutBlock.List(
								jen.Id("nodes").Index(jen.Id("i")),
								jen.Id("specs").Index(jen.Id("i")),
								jen.Id("err"),
							).Op("=").Id("builder").Dot("createSpec").Call()
							mutBlock.If(jen.Id("err").Op("!=").Nil()).Block(
								jen.Return(jen.Nil(), jen.Id("err")),
							)
						} else {
							mutBlock.List(
								jen.Id("nodes").Index(jen.Id("i")),
								jen.Id("specs").Index(jen.Id("i")),
							).Op("=").Id("builder").Dot("createSpec").Call()
						}

						// Chain to next mutator or execute batch create
						mutBlock.If(jen.Id("i").Op("<").Len(jen.Id("mutators")).Op("-").Lit(1)).BlockFunc(func(chainBlock *jen.Group) {
							chainBlock.List(jen.Id("_"), jen.Id("err")).Op("=").Id("mutators").Index(jen.Id("i").Op("+").Lit(1)).Dot("Mutate").Call(
								jen.Id("root"),
								jen.Id(t.CreateBulReceiver()).Dot("builders").Index(jen.Id("i").Op("+").Lit(1)).Dot("mutation"),
							)
						}).Else().BlockFunc(func(execBlock *jen.Group) {
							// Execute batch create
							execBlock.Id("spec").Op(":=").Op("&").Qual(h.SQLGraphPkg(), "BatchCreateSpec").Values(jen.Dict{
								jen.Id("Nodes"): jen.Id("specs"),
							})
							execBlock.Comment("Invoke the actual operation on the latest mutation in the chain.")
							execBlock.If(
								jen.Id("err").Op("=").Qual(h.SQLGraphPkg(), "BatchCreate").Call(
									jen.Id("ctx"),
									jen.Id(t.CreateBulReceiver()).Dot("driver"),
									jen.Id("spec"),
								),
								jen.Id("err").Op("!=").Nil(),
							).Block(
								jen.If(jen.Qual(h.SQLGraphPkg(), "IsConstraintError").Call(jen.Id("err"))).Block(
									jen.Id("err").Op("=").Op("&").Id("ConstraintError").Values(jen.Dict{
										jen.Id("msg"):  jen.Id("err").Dot("Error").Call(),
										jen.Id("wrap"): jen.Id("err"),
									}),
								),
							)
						})

						mutBlock.If(jen.Id("err").Op("!=").Nil()).Block(
							jen.Return(jen.Nil(), jen.Id("err")),
						)

						// Handle ID assignment after batch create
						if t.HasOneFieldID() {
							mutBlock.Id("mutation").Dot(t.ID.BuilderField()).Op("=").Op("&").Id("nodes").Index(jen.Id("i")).Dot(t.ID.StructField())
							// Handle numeric ID types that need assignment from spec
							genBulkCreateIDAssignment(h, mutBlock, t)
						}

						mutBlock.Id("mutation").Dot("done").Op("=").True()
						mutBlock.Return(jen.Id("nodes").Index(jen.Id("i")), jen.Nil())
					}),
				)

				// Wrap with hooks in reverse order
				funcBlock.For(jen.Id("i").Op(":=").Len(jen.Id("builder").Dot("hooks")).Op("-").Lit(1), jen.Id("i").Op(">=").Lit(0), jen.Id("i").Op("--")).Block(
					jen.Id("mut").Op("=").Id("builder").Dot("hooks").Index(jen.Id("i")).Call(jen.Id("mut")),
				)

				funcBlock.Id("mutators").Index(jen.Id("i")).Op("=").Id("mut")
			}).Call(jen.Id("i"), jen.Id("ctx"))
		})

		// Execute the mutation chain starting from the first mutator
		grp.If(jen.Len(jen.Id("mutators")).Op(">").Lit(0)).Block(
			jen.If(
				jen.List(jen.Id("_"), jen.Id("err")).Op(":=").Id("mutators").Index(jen.Lit(0)).Dot("Mutate").Call(
					jen.Id("ctx"),
					jen.Id(t.CreateBulReceiver()).Dot("builders").Index(jen.Lit(0)).Dot("mutation"),
				),
				jen.Id("err").Op("!=").Nil(),
			).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
		)

		grp.Return(jen.Id("nodes"), jen.Nil())
	})
}

// genBulkCreateIDAssignment generates the ID assignment logic for bulk create.
func genBulkCreateIDAssignment(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type) {
	if !t.HasOneFieldID() {
		return
	}
	id := t.ID

	// Handle numeric ID types - the ID is assigned from spec.ID.Value after batch create
	// Use safe type assertions to avoid panic if Value is wrong type
	if id.Type.Numeric() && !id.Type.ValueScanner() {
		if id.UserDefined {
			// For user-defined IDs, only assign if the spec has a value and node ID is zero
			grp.If(
				jen.Id("specs").Index(jen.Id("i")).Dot("ID").Dot("Value").Op("!=").Nil().Op("&&").
					Id("nodes").Index(jen.Id("i")).Dot("ID").Op("==").Lit(0),
			).BlockFunc(func(blk *jen.Group) {
				blk.If(
					jen.List(jen.Id("id"), jen.Id("ok")).Op(":=").Id("specs").Index(jen.Id("i")).Dot("ID").Dot("Value").Op(".").Parens(jen.Int64()),
					jen.Id("ok"),
				).Block(
					jen.Id("nodes").Index(jen.Id("i")).Dot("ID").Op("=").Add(h.BaseType(id)).Call(jen.Id("id")),
				)
			})
		} else {
			// For auto-increment IDs, always assign from spec if present
			grp.If(jen.Id("specs").Index(jen.Id("i")).Dot("ID").Dot("Value").Op("!=").Nil()).BlockFunc(func(blk *jen.Group) {
				blk.If(
					jen.List(jen.Id("id"), jen.Id("ok")).Op(":=").Id("specs").Index(jen.Id("i")).Dot("ID").Dot("Value").Op(".").Parens(jen.Int64()),
					jen.Id("ok"),
				).Block(
					jen.Id("nodes").Index(jen.Id("i")).Dot("ID").Op("=").Add(h.BaseType(id)).Call(jen.Id("id")),
				)
			})
		}
	} else if id.Type.ValueScanner() {
		// For ValueScanner types like UUID
		grp.If(jen.Id("specs").Index(jen.Id("i")).Dot("ID").Dot("Value").Op("!=").Nil()).Block(
			jen.If(
				jen.Id("err").Op(":=").Id("nodes").Index(jen.Id("i")).Dot("ID").Dot("Scan").Call(
					jen.Id("specs").Index(jen.Id("i")).Dot("ID").Dot("Value"),
				),
				jen.Id("err").Op("!=").Nil(),
			).Block(
				jen.Return(jen.Nil(), jen.Id("err")),
			),
		)
	}
}

// genCreateBulkUpsertMethods generates OnConflict methods for the CreateBulk builder.
// This is part of the sql/upsert feature.
func genCreateBulkUpsertMethods(h gen.GeneratorHelper, f *jen.File, t *gen.Type, bulkName string) {
	upsertBulkName := t.Name + "UpsertBulk"
	upsertSetName := t.Name + "Upsert"
	entityPkg := t.PackageDir()

	// OnConflict method
	f.Comment("OnConflict allows configuring the `ON CONFLICT` / `ON DUPLICATE KEY` clause")
	f.Comment("of the `INSERT` statement.")
	f.Func().Params(jen.Id(t.CreateBulReceiver()).Op("*").Id(bulkName)).Id("OnConflict").Params(
		jen.Id("opts").Op("...").Qual(h.SQLPkg(), "ConflictOption"),
	).Op("*").Id(upsertBulkName).Block(
		jen.Id(t.CreateBulReceiver()).Dot("conflict").Op("=").Id("opts"),
		jen.Return(jen.Op("&").Id(upsertBulkName).Values(jen.Dict{
			jen.Id("create"): jen.Id(t.CreateBulReceiver()),
		})),
	)

	// OnConflictColumns method
	f.Comment("OnConflictColumns calls `OnConflict` and configures the columns")
	f.Comment("as conflict target.")
	f.Func().Params(jen.Id(t.CreateBulReceiver()).Op("*").Id(bulkName)).Id("OnConflictColumns").Params(
		jen.Id("columns").Op("...").String(),
	).Op("*").Id(upsertBulkName).Block(
		jen.Id(t.CreateBulReceiver()).Dot("conflict").Op("=").Append(
			jen.Id(t.CreateBulReceiver()).Dot("conflict"),
			jen.Qual(h.SQLPkg(), "ConflictColumns").Call(jen.Id("columns").Op("...")),
		),
		jen.Return(jen.Op("&").Id(upsertBulkName).Values(jen.Dict{
			jen.Id("create"): jen.Id(t.CreateBulReceiver()),
		})),
	)

	// UpsertBulk type
	f.Commentf("%s is the builder for \"upsert\"-ing a bulk of %s nodes.", upsertBulkName, t.Name)
	f.Type().Id(upsertBulkName).Struct(
		jen.Id("create").Op("*").Id(bulkName),
	)

	// UpdateNewValues on UpsertBulk
	f.Comment("UpdateNewValues updates the mutable fields using the new values that were set on create.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertBulkName)).Id("UpdateNewValues").Params().Op("*").Id(upsertBulkName).Block(
		jen.Id("u").Dot("create").Dot("conflict").Op("=").Append(
			jen.Id("u").Dot("create").Dot("conflict"),
			jen.Qual(h.SQLPkg(), "ResolveWithNewValues").Call(),
		),
		jen.Return(jen.Id("u")),
	)

	// Ignore on UpsertBulk
	f.Comment("Ignore sets each column to itself in case of conflict.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertBulkName)).Id("Ignore").Params().Op("*").Id(upsertBulkName).Block(
		jen.Id("u").Dot("create").Dot("conflict").Op("=").Append(
			jen.Id("u").Dot("create").Dot("conflict"),
			jen.Qual(h.SQLPkg(), "ResolveWithIgnore").Call(),
		),
		jen.Return(jen.Id("u")),
	)

	// DoNothing on UpsertBulk
	f.Comment("DoNothing configures the conflict_action to `DO NOTHING`.")
	f.Comment("Supported only by SQLite and PostgreSQL.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertBulkName)).Id("DoNothing").Params().Op("*").Id(upsertBulkName).Block(
		jen.Id("u").Dot("create").Dot("conflict").Op("=").Append(
			jen.Id("u").Dot("create").Dot("conflict"),
			jen.Qual(h.SQLPkg(), "DoNothing").Call(),
		),
		jen.Return(jen.Id("u")),
	)

	// Update on UpsertBulk
	f.Comment("Update allows overriding fields `UPDATE` values.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertBulkName)).Id("Update").Params(
		jen.Id("set").Func().Params(jen.Op("*").Id(upsertSetName)),
	).Op("*").Id(upsertBulkName).Block(
		jen.Id("u").Dot("create").Dot("conflict").Op("=").Append(
			jen.Id("u").Dot("create").Dot("conflict"),
			jen.Qual(h.SQLPkg(), "ResolveWith").Call(
				jen.Func().Params(jen.Id("update").Op("*").Qual(h.SQLPkg(), "UpdateSet")).Block(
					jen.Id("set").Call(jen.Op("&").Id(upsertSetName).Values(jen.Dict{
						jen.Id("UpdateSet"): jen.Id("update"),
					})),
				),
			),
		),
		jen.Return(jen.Id("u")),
	)

	// Exec on UpsertBulk
	f.Comment("Exec executes the query.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertBulkName)).Id("Exec").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Error().Block(
		jen.If(jen.Id("u").Dot("create").Dot("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("u").Dot("create").Dot("err")),
		),
		jen.For(jen.List(jen.Id("i"), jen.Id("b")).Op(":=").Range().Id("u").Dot("create").Dot("builders")).Block(
			jen.If(jen.Len(jen.Id("b").Dot("conflict")).Op("!=").Lit(0)).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(
					jen.Lit(entityPkg+": OnConflict was set for builder %d. Set it on the "+bulkName+" instead"),
					jen.Id("i"),
				)),
			),
		),
		jen.If(jen.Len(jen.Id("u").Dot("create").Dot("conflict")).Op("==").Lit(0)).Block(
			jen.Return(jen.Qual("errors", "New").Call(jen.Lit(entityPkg+": missing options for "+bulkName+".OnConflict"))),
		),
		jen.Return(jen.Id("u").Dot("create").Dot("Exec").Call(jen.Id("ctx"))),
	)

	// ExecX on UpsertBulk
	f.Comment("ExecX is like Exec, but panics if an error occurs.")
	f.Func().Params(jen.Id("u").Op("*").Id(upsertBulkName)).Id("ExecX").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Block(
		jen.If(jen.Id("err").Op(":=").Id("u").Dot("create").Dot("Exec").Call(jen.Id("ctx")), jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
	)
}
