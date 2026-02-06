package sql

import (
	"strings"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genEntity generates the entity struct file ({entity}.go).
func genEntity(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	f := h.NewFile(h.Pkg())

	// NOTE: Enum types are generated in the subpackage only (e.g., abtesting.Type)
	// to avoid import cycles. The entity struct uses qualified subpackage types.

	// Generate entity struct
	genEntityStruct(h, f, t)

	// Generate Edges struct for the entity
	genEdgesStruct(h, f, t)

	// Generate Query{Edge} methods on the entity struct for lazy loading
	for _, e := range t.Edges {
		genQueryEdgeMethod(h, f, t, e)
	}

	// Generate entity client
	genEntityClient(h, f, t)

	return f
}

// genEntityStruct generates the entity struct.
func genEntityStruct(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	f.Commentf("%s is the model entity for the %s schema.", t.Name, t.Name)
	f.Type().Id(t.Name).StructFunc(func(group *jen.Group) {
		// Embedded config (unexported)
		group.Id("config").Tag(map[string]string{"json": "-"})

		// ID field
		if t.ID != nil {
			group.Id("ID").Add(h.GoType(t.ID)).Tag(h.StructTags(t.ID))
		}

		// Regular fields
		for _, field := range t.Fields {
			if field.IsEdgeField() {
				continue // Skip edge fields, they're handled with edges
			}
			group.Id(field.StructField()).Add(h.GoType(field)).Tag(h.StructTags(field))
		}

		// Edge fields (foreign keys)
		for _, field := range t.Fields {
			if field.IsEdgeField() {
				group.Id(field.StructField()).Add(h.GoType(field)).Tag(h.StructTags(field))
			}
		}

		// Unexported foreign keys (edge foreign keys not defined as explicit fields)
		for _, fk := range t.UnexportedForeignKeys() {
			fkField := fk.Field
			var fieldType jen.Code
			if fkField.Nillable {
				fieldType = jen.Op("*").Add(h.BaseType(fkField))
			} else {
				fieldType = h.BaseType(fkField)
			}
			group.Id(fk.StructField()).Add(fieldType)
		}

		// selectValues for storing dynamically selected values
		group.Id("selectValues").Qual(h.SQLPkg(), "SelectValues")

		// Edges struct
		group.Id("Edges").Id(t.Name + "Edges").Tag(map[string]string{"json": "edges"})
	})

	// String method
	f.Func().Params(jen.Id("_e").Op("*").Id(t.Name)).Id("String").Params().String().Block(
		jen.Var().Id("builder").Qual("strings", "Builder"),
		jen.Id("builder").Dot("WriteString").Call(jen.Lit(t.Name+"(")),
		jen.If(jen.Id("_e").Dot("ID").Op("!=").Add(zeroValue(h, t.ID))).Block(
			jen.Id("builder").Dot("WriteString").Call(jen.Qual("fmt", "Sprintf").Call(
				jen.Lit("id=%v, "),
				jen.Id("_e").Dot("ID"),
			)),
		),
		jen.For(jen.List(jen.Id("_"), jen.Id("f")).Op(":=").Range().Id("_e").Dot("fields").Call()).Block(
			jen.Id("builder").Dot("WriteString").Call(jen.Qual("fmt", "Sprintf").Call(
				jen.Lit("%s=%v, "),
				jen.Id("f").Dot("name"),
				jen.Id("f").Dot("value"),
			)),
		),
		jen.Id("builder").Dot("WriteString").Call(jen.Lit(")")),
		jen.Return(jen.Id("builder").Dot("String").Call()),
	)

	// fields helper method for String()
	f.Func().Params(jen.Id("_e").Op("*").Id(t.Name)).Id("fields").Params().Index().Struct(
		jen.Id("name").String(),
		jen.Id("value").Any(),
	).Block(
		jen.Return(jen.Index().Struct(
			jen.Id("name").String(),
			jen.Id("value").Any(),
		).ValuesFunc(func(vals *jen.Group) {
			for _, field := range t.Fields {
				if !field.IsEdgeField() {
					vals.Values(jen.Dict{
						jen.Id("name"):  jen.Lit(field.Name),
						jen.Id("value"): jen.Id("_e").Dot(field.StructField()),
					})
				}
			}
		})),
	)

	// Unwrap returns the underlying entity
	f.Commentf("Unwrap unwraps the %s entity that was returned from a transaction after it was closed.", t.Name)
	f.Func().Params(jen.Id("_e").Op("*").Id(t.Name)).Id("Unwrap").Params().Op("*").Id(t.Name).Block(
		jen.List(jen.Id("_tx"), jen.Id("ok")).Op(":=").Id("_e").Dot("config").Dot("driver").Op(".").Parens(jen.Op("*").Id("txDriver")),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Panic(jen.Lit("velox: "+t.Name+" is not a transactional entity")),
		),
		jen.Id("_e").Dot("config").Dot("driver").Op("=").Id("_tx").Dot("drv"),
		jen.Return(jen.Id("_e")),
	)

	// Update returns an update builder for this entity.
	// Note: You need to call Unwrap() before calling this method if this entity
	// was returned from a transaction, and the transaction was committed or rolled back.
	f.Commentf("Update returns a builder for updating this %s.", t.Name)
	f.Commentf("Note: You need to call %s.Unwrap() before calling this method if this %s", t.Name, t.Name)
	f.Comment("was returned from a transaction, and the transaction was committed or rolled back.")
	f.Func().Params(jen.Id("_e").Op("*").Id(t.Name)).Id("Update").Params().Op("*").Id(t.Name + "UpdateOne").Block(
		jen.Return(jen.Id("New" + t.Name + "Client").Call(jen.Id("_e").Dot("config")).Dot("UpdateOne").Call(jen.Id("_e"))),
	)

	// Generate scanValues method
	genScanValues(h, f, t)

	// Generate assignValues method
	genAssignValues(h, f, t)

	// Generate Value method for accessing dynamically selected values
	genValueMethod(h, f, t)
}

// genScanValues generates the scanValues method that returns scan types for sql.Rows.
func genScanValues(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	entityPkg := h.EntityPkgPath(t)

	f.Comment("scanValues returns the types for scanning values from sql.Rows.")
	f.Func().Params(jen.Op("*").Id(t.Name)).Id("scanValues").Params(
		jen.Id("columns").Index().String(),
	).Params(jen.Index().Any(), jen.Error()).BlockFunc(func(grp *jen.Group) {
		grp.Id("values").Op(":=").Make(jen.Index().Any(), jen.Len(jen.Id("columns")))
		grp.For(jen.Id("i").Op(":=").Range().Id("columns")).BlockFunc(func(forGrp *jen.Group) {
			forGrp.Switch(jen.Id("columns").Index(jen.Id("i"))).BlockFunc(func(switchGrp *jen.Group) {
				// Group fields by scan type to generate combined cases
				scanTypeFields := make(map[string][]string)

				// Add ID field
				if t.ID != nil {
					scanType := t.ID.NewScanType()
					scanTypeFields[scanType] = append(scanTypeFields[scanType], t.ID.Constant())
				}

				// Add regular fields (excluding those with ValueScanner)
				// Note: User-defined FK fields (IsEdgeField() but UserDefined) should be included
				for _, field := range t.Fields {
					if field.HasValueScanner() || (field.IsEdgeField() && !field.UserDefined) || field.Type == nil {
						continue
					}
					scanType := field.NewScanType()
					scanTypeFields[scanType] = append(scanTypeFields[scanType], field.Constant())
				}

				// Generate cases grouped by scan type
				for scanType, constants := range scanTypeFields {
					caseExprs := make([]jen.Code, len(constants))
					for i, constant := range constants {
						caseExprs[i] = jen.Qual(entityPkg, constant)
					}
					switchGrp.Case(caseExprs...).Block(
						jen.Id("values").Index(jen.Id("i")).Op("=").Id(scanType),
					)
				}

				// Handle fields with ValueScanner separately
				for _, field := range t.Fields {
					if !field.HasValueScanner() || (field.IsEdgeField() && !field.UserDefined) || field.Type == nil {
						continue
					}
					scanValueFunc, err := field.ScanValueFunc()
					if err != nil {
						continue
					}
					switchGrp.Case(jen.Qual(entityPkg, field.Constant())).Block(
						jen.Id("values").Index(jen.Id("i")).Op("=").Id(scanValueFunc).Call(),
					)
				}

				// Handle unexported foreign keys
				unexportedFKs := t.UnexportedForeignKeys()
				for i, fk := range unexportedFKs {
					fkField := fk.Field
					// Use the FK field's actual type for scanning (not hardcoded NullInt64)
					scanType := fkField.NewScanType()
					switchGrp.Commentf("// %s", fkField.Name)
					switchGrp.Case(jen.Qual(entityPkg, "ForeignKeys").Index(jen.Lit(i))).Block(
						jen.Id("values").Index(jen.Id("i")).Op("=").Id(scanType),
					)
				}

				// Default case for unknown columns
				switchGrp.Default().Block(
					jen.Id("values").Index(jen.Id("i")).Op("=").New(jen.Qual(h.SQLPkg(), "UnknownType")),
				)
			})
		})
		grp.Return(jen.Id("values"), jen.Nil())
	})
}

// genAssignValues generates the assignValues method that assigns scanned values to entity fields.
func genAssignValues(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	entityPkg := h.EntityPkgPath(t)

	f.Comment("assignValues assigns the values that were returned from sql.Rows (after scanning)")
	f.Commentf("to the %s fields.", t.Name)
	f.Func().Params(jen.Id("_e").Op("*").Id(t.Name)).Id("assignValues").Params(
		jen.Id("columns").Index().String(),
		jen.Id("values").Index().Any(),
	).Error().BlockFunc(func(grp *jen.Group) {
		grp.If(
			jen.List(jen.Id("m"), jen.Id("n")).Op(":=").Len(jen.Id("values")).Op(",").Len(jen.Id("columns")),
			jen.Id("m").Op("<").Id("n"),
		).Block(
			jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("mismatch number of scan values: %d != %d"), jen.Id("m"), jen.Id("n"))),
		)
		grp.For(jen.Id("i").Op(":=").Range().Id("columns")).BlockFunc(func(forGrp *jen.Group) {
			forGrp.Switch(jen.Id("columns").Index(jen.Id("i"))).BlockFunc(func(switchGrp *jen.Group) {
				// Handle ID field
				if t.ID != nil {
					switchGrp.Case(jen.Qual(entityPkg, t.ID.Constant())).BlockFunc(func(caseGrp *jen.Group) {
						genFieldAssignment(h, caseGrp, t, t.ID, "i", "_e", "ID")
					})
				}

				// Handle regular fields
				// Note: User-defined FK fields (IsEdgeField() but UserDefined) should be included
				for _, field := range t.Fields {
					if (field.IsEdgeField() && !field.UserDefined) || field.Type == nil {
						continue
					}
					switchGrp.Case(jen.Qual(entityPkg, field.Constant())).BlockFunc(func(caseGrp *jen.Group) {
						genFieldAssignment(h, caseGrp, t, field, "i", "_e", field.StructField())
					})
				}

				// Handle unexported foreign keys
				unexportedFKs := t.UnexportedForeignKeys()
				for i, fk := range unexportedFKs {
					fkField := fk.Field
					if fk.UserDefined {
						switchGrp.Case(jen.Qual(entityPkg, fkField.Constant())).BlockFunc(func(caseGrp *jen.Group) {
							genFieldAssignment(h, caseGrp, t, fkField, "i", "_e", fk.StructField())
						})
					} else {
						switchGrp.Case(jen.Qual(entityPkg, "ForeignKeys").Index(jen.Lit(i))).BlockFunc(func(caseGrp *jen.Group) {
							// Use genFieldAssignment which handles all field types correctly
							genFieldAssignment(h, caseGrp, t, fkField, "i", "_e", fk.StructField())
						})
					}
				}

				// Default case: store in selectValues
				switchGrp.Default().Block(
					jen.Id("_e").Dot("selectValues").Dot("Set").Call(jen.Id("columns").Index(jen.Id("i")), jen.Id("values").Index(jen.Id("i"))),
				)
			})
		})
		grp.Return(jen.Nil())
	})
}

// genFieldAssignment generates the assignment code for a field in assignValues.
func genFieldAssignment(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, field *gen.Field, idx string, receiver string, structField string) {
	if field.HasValueScanner() {
		fromValueFunc, err := field.FromValueFunc()
		if err != nil {
			return
		}
		grp.If(
			jen.List(jen.Id("value"), jen.Id("err")).Op(":=").Id(fromValueFunc).Call(jen.Id("values").Index(jen.Id(idx))),
			jen.Id("err").Op("!=").Nil(),
		).Block(
			jen.Return(jen.Id("err")),
		).Else().Block(
			jen.Id(receiver).Dot(structField).Op("=").Id("value"),
		)
		return
	}

	if field.IsJSON() {
		grp.If(
			jen.List(jen.Id("value"), jen.Id("ok")).Op(":=").Id("values").Index(jen.Id(idx)).Op(".").Parens(jen.Op("*").Index().Byte()),
			jen.Op("!").Id("ok"),
		).Block(
			jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected type %T for field "+field.Name), jen.Id("values").Index(jen.Id(idx)))),
		).Else().If(jen.Id("value").Op("!=").Nil().Op("&&").Len(jen.Op("*").Id("value")).Op(">").Lit(0)).Block(
			jen.If(jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(jen.Op("*").Id("value"), jen.Op("&").Id(receiver).Dot(structField)), jen.Id("err").Op("!=").Nil()).Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unmarshal field "+field.Name+": %w"), jen.Id("err"))),
			),
		)
		return
	}

	scanType := field.ScanType()
	grp.If(
		jen.List(jen.Id("value"), jen.Id("ok")).Op(":=").Id("values").Index(jen.Id(idx)).Op(".").Parens(jen.Op("*").Id(scanType)),
		jen.Op("!").Id("ok"),
	).Block(
		jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected type %T for field "+field.Name), jen.Id("values").Index(jen.Id(idx)))),
	).Else().BlockFunc(func(elseGrp *jen.Group) {
		if strings.HasPrefix(scanType, "sql.Null") {
			// Nullable type
			elseGrp.If(jen.Id("value").Dot("Valid")).BlockFunc(func(validGrp *jen.Group) {
				if field.NillableValue() {
					validGrp.Id(receiver).Dot(structField).Op("=").New(jen.Id(field.Type.String()))
					validGrp.Op("*").Id(receiver).Dot(structField).Op("=").Add(genScanTypeFieldExpr(field))
				} else {
					validGrp.Id(receiver).Dot(structField).Op("=").Add(genScanTypeFieldExpr(field))
				}
			})
		} else {
			// Non-nullable type
			if !field.Nillable && (field.Type.RType == nil || !field.Type.RType.IsPtr()) {
				elseGrp.If(jen.Id("value").Op("!=").Nil()).Block(
					jen.Id(receiver).Dot(structField).Op("=").Op("*").Id("value"),
				)
			} else {
				elseGrp.If(jen.Id("value").Op("!=").Nil()).Block(
					jen.Id(receiver).Dot(structField).Op("=").Id("value"),
				)
			}
		}
	})
}

// genScanTypeFieldExpr generates the expression for extracting a value from a scanned nullable type.
// Returns jen.Code that evaluates to the field value.
// For enum fields, we need to use the subpackage enum type for the conversion.
func genScanTypeFieldExpr(f *gen.Field) jen.Code {
	// Handle enum fields specially because:
	// - If HasGoType() is true: use f.Type.String() which gives the custom type (e.g., schematype.Currency)
	// - If HasGoType() is false: the enum is defined in the entity's subpackage (e.g., abtesting.Type)
	if f.IsEnum() && !f.HasGoType() {
		// Subpackage enum type: abtesting.Type(value.String)
		return jen.Qual(f.EnumPkgPath(), f.SubpackageEnumTypeName()).Call(jen.Id("value").Dot("String"))
	}
	// For all other cases (including enums with custom Go types), use the standard method
	return jen.Id(f.ScanTypeField("value"))
}

// genValueMethod generates the Value method for accessing dynamically selected values.
func genValueMethod(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	valueName := t.ValueName() // Returns "Value" or "GetValue" based on field conflicts
	f.Commentf("%s returns the ent.Value that was dynamically selected and assigned to the %s.", valueName, t.Name)
	f.Comment("This includes values selected through modifiers, order, etc.")
	f.Func().Params(jen.Id("_e").Op("*").Id(t.Name)).Id(valueName).Params(
		jen.Id("name").String(),
	).Params(jen.Qual(h.VeloxPkg(), "Value"), jen.Error()).Block(
		jen.Return(jen.Id("_e").Dot("selectValues").Dot("Get").Call(jen.Id("name"))),
	)
}

// genEdgesStruct generates the Edges struct for an entity.
func genEdgesStruct(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	edgesName := t.Name + "Edges"
	namedEdgesEnabled := h.FeatureEnabled("namedges")

	f.Commentf("%s holds the relations/edges for the %s entity.", edgesName, t.Name)
	f.Type().Id(edgesName).StructFunc(func(group *jen.Group) {
		for _, edge := range t.Edges {
			edgeType := edgeGoType(edge)
			group.Id(edge.StructField()).Add(edgeType).Tag(h.EdgeStructTags(edge))

			// loadedTypes field for tracking loaded edges
			group.Id("loaded" + edge.StructField()).Bool().Tag(map[string]string{"-": ""})

			// Named edge maps for O2M/M2M edges (namedges feature)
			if namedEdgesEnabled && !edge.Unique {
				group.Id("named" + edge.StructField()).Map(jen.String()).Index().Op("*").Id(edge.Type.Name).Tag(map[string]string{"-": ""})
			}
		}

		// totalCount field for GraphQL pagination - stores edge counts indexed by edge position
		// Used by gql_collection.go to track totalCount for Relay connections
		if len(t.Edges) > 0 {
			group.Id("totalCount").Index(jen.Lit(len(t.Edges))).Map(jen.String()).Int().Tag(map[string]string{"-": ""})
		}
	})

	// Generate edge accessor methods
	for _, edge := range t.Edges {
		genEdgeAccessor(f, t, edge)
	}

	// Generate named edge methods when feature is enabled
	if namedEdgesEnabled {
		for _, edge := range t.Edges {
			if !edge.Unique {
				genNamedEdgeMethods(f, t, edge)
			}
		}
	}

	// Generate bidirectional edge reference methods when feature is enabled
	bidiEdgesEnabled := h.FeatureEnabled("bidiedges")
	if bidiEdgesEnabled {
		for _, edge := range t.Edges {
			genBidiEdgeRefMethod(f, t, edge)
		}
	}
}

// edgeGoType returns the Go type for an edge.
func edgeGoType(e *gen.Edge) jen.Code {
	if e.Unique {
		return jen.Op("*").Id(e.Type.Name)
	}
	return jen.Index().Op("*").Id(e.Type.Name)
}

// genEdgeAccessor generates an accessor method for an edge.
func genEdgeAccessor(f *jen.File, t *gen.Type, e *gen.Edge) {
	methodName := e.StructField() + "OrErr"

	if e.Unique {
		f.Commentf("%s returns the %s edge or an error if it was not loaded.", methodName, e.Name)
		f.Func().Params(jen.Id("e").Id(t.Name+"Edges")).Id(methodName).Params().Params(
			jen.Op("*").Id(e.Type.Name),
			jen.Error(),
		).Block(
			jen.If(jen.Id("e").Dot("loaded"+e.StructField())).Block(
				jen.Return(jen.Id("e").Dot(e.StructField()), jen.Nil()),
			),
			jen.Return(jen.Nil(), jen.Op("&").Id("NotLoadedError").Values(jen.Dict{
				jen.Id("edge"): jen.Lit(e.Name),
			})),
		)
	} else {
		f.Commentf("%s returns the %s edge or an error if it was not loaded.", methodName, e.Name)
		f.Func().Params(jen.Id("e").Id(t.Name+"Edges")).Id(methodName).Params().Params(
			jen.Index().Op("*").Id(e.Type.Name),
			jen.Error(),
		).Block(
			jen.If(jen.Id("e").Dot("loaded"+e.StructField())).Block(
				jen.Return(jen.Id("e").Dot(e.StructField()), jen.Nil()),
			),
			jen.Return(jen.Nil(), jen.Op("&").Id("NotLoadedError").Values(jen.Dict{
				jen.Id("edge"): jen.Lit(e.Name),
			})),
		)
	}
}

// genBidiEdgeRefMethod generates a method to set bidirectional edge references.
// This is part of the bidiedges feature.
func genBidiEdgeRefMethod(f *jen.File, t *gen.Type, e *gen.Edge) {
	if e.Ref == nil {
		return // No inverse edge to set
	}
	methodName := "set" + e.StructField() + "BidiRef"
	edgeName := e.StructField()
	inverseField := e.Ref.StructField()

	if e.Unique && e.Ref.Unique {
		// For O2O edges where both sides are unique, set the inverse directly
		f.Commentf("%s sets the bidirectional reference on the loaded %s edge.", methodName, e.Name)
		f.Func().Params(jen.Id("_e").Op("*").Id(t.Name)).Id(methodName).Params().Block(
			jen.If(jen.Id("_e").Dot("Edges").Dot(edgeName).Op("!=").Nil()).Block(
				jen.Id("_e").Dot("Edges").Dot(edgeName).Dot("Edges").Dot(inverseField).Op("=").Id("_e"),
				jen.Id("_e").Dot("Edges").Dot(edgeName).Dot("Edges").Dot("loaded"+inverseField).Op("=").True(),
			),
		)
	} else if e.Unique && !e.Ref.Unique {
		// For M2O edges where this side is unique but inverse is a slice,
		// we can't easily set the bidirectional reference without appending to a slice.
		// Generate a no-op method to satisfy interface requirements but avoid the type mismatch.
		f.Commentf("%s is a no-op for M2O edges with O2M inverse.", methodName)
		f.Func().Params(jen.Id("_e").Op("*").Id(t.Name)).Id(methodName).Params().Block(
			jen.Comment("Bidirectional reference not set for M2O->O2M relationship"),
		)
	} else if !e.Unique && e.Ref.Unique {
		// For O2M/M2M edges where inverse is unique, set the inverse on all loaded entities
		f.Commentf("%s sets the bidirectional reference on all loaded %s edges.", methodName, e.Name)
		f.Func().Params(jen.Id("_e").Op("*").Id(t.Name)).Id(methodName).Params().Block(
			jen.For(jen.List(jen.Id("_"), jen.Id("edge")).Op(":=").Range().Id("_e").Dot("Edges").Dot(edgeName)).Block(
				jen.Id("edge").Dot("Edges").Dot(inverseField).Op("=").Id("_e"),
				jen.Id("edge").Dot("Edges").Dot("loaded"+inverseField).Op("=").True(),
			),
		)
	} else {
		// For M2M edges where both sides are slices, generate a no-op
		f.Commentf("%s is a no-op for M2M edges.", methodName)
		f.Func().Params(jen.Id("_e").Op("*").Id(t.Name)).Id(methodName).Params().Block(
			jen.Comment("Bidirectional reference not set for M2M relationship"),
		)
	}
}

// genNamedEdgeMethods generates Named{Edge} and appendNamed{Edge} methods.
// This is part of the namedges feature.
func genNamedEdgeMethods(f *jen.File, t *gen.Type, e *gen.Edge) {
	edgeName := e.StructField()
	namedField := "named" + edgeName
	edgeType := jen.Index().Op("*").Id(e.Type.Name)

	// Named{Edge}(name string) ([]*EdgeType, error)
	f.Commentf("Named%s returns the %s edge with the given name or an error if it was not loaded.", edgeName, e.Name)
	f.Func().Params(jen.Id("e").Op("*").Id(t.Name)).Id("Named"+edgeName).Params(
		jen.Id("name").String(),
	).Params(edgeType, jen.Error()).Block(
		jen.If(jen.Id("e").Dot("Edges").Dot(namedField).Op("==").Nil()).Block(
			jen.Return(jen.Nil(), jen.Op("&").Id("NotLoadedError").Values(jen.Dict{
				jen.Id("edge"): jen.Lit(e.Name),
			})),
		),
		jen.List(jen.Id("edges"), jen.Id("ok")).Op(":=").Id("e").Dot("Edges").Dot(namedField).Index(jen.Id("name")),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Nil(), jen.Op("&").Id("NotLoadedError").Values(jen.Dict{
				jen.Id("edge"): jen.Lit(e.Name),
			})),
		),
		jen.Return(jen.Id("edges"), jen.Nil()),
	)

	// appendNamed{Edge}(name string, edges ...*EdgeType)
	f.Commentf("appendNamed%s adds the given edges to the named edge with the given name.", edgeName)
	f.Func().Params(jen.Id("e").Op("*").Id(t.Name)).Id("appendNamed"+edgeName).Params(
		jen.Id("name").String(),
		jen.Id("edges").Op("...").Op("*").Id(e.Type.Name),
	).Block(
		jen.If(jen.Id("e").Dot("Edges").Dot(namedField).Op("==").Nil()).Block(
			jen.Id("e").Dot("Edges").Dot(namedField).Op("=").Make(jen.Map(jen.String()).Add(edgeType)),
		),
		// append works on nil slices, so no need to initialize the map entry
		jen.Id("e").Dot("Edges").Dot(namedField).Index(jen.Id("name")).Op("=").Append(
			jen.Id("e").Dot("Edges").Dot(namedField).Index(jen.Id("name")),
			jen.Id("edges").Op("..."),
		),
	)
}

// genQueryEdgeMethod generates Query{Edge} method on the entity struct for lazy loading.
// This allows querying related entities from an entity instance:
//
//	category.QueryTodos() => *TodoQuery
func genQueryEdgeMethod(h gen.GeneratorHelper, f *jen.File, t *gen.Type, e *gen.Edge) {
	edgeName := e.StructField()
	methodName := "Query" + edgeName
	targetQueryType := e.Type.QueryName()
	targetClientType := e.Type.ClientName()
	targetEntityPkg := h.EntityPkgPath(e.Type)
	currentEntityPkg := h.EntityPkgPath(t)

	// Get the back-reference edge name for the predicate
	// For Category->todos: the back-ref on Todo is "category", so we use HasCategoryWith
	backRefName := ""
	if e.Ref != nil {
		// This is a back-reference edge (edge.From), the predicate is on the current edge target
		// e.g., Group.users edge (From) -> User has "groups" edge, predicate is HasGroupsWith
		backRefName = e.Ref.StructField()
	} else if e.IsInverse() {
		// Inverse edge - the predicate uses the forward edge name (e.Inverse) on target
		// e.g., TaxGroup.customers (From) with Ref("tax_group") -> Customer has "tax_group" edge
		// So predicate is HasTaxGroupWith, not HasCustomersWith
		backRefName = gen.Pascal(e.Inverse)
	} else {
		// Forward edge (edge.To) - need to find the back-reference edge on the target
		// e.g., Category.todos -> Todo has "category" edge
		for _, targetEdge := range e.Type.Edges {
			if targetEdge.Type == t && targetEdge.Ref != nil && targetEdge.Ref.Name == e.Name {
				backRefName = targetEdge.StructField()
				break
			}
			// Also check for inverse edges pointing to current type
			if targetEdge.Type == t && targetEdge.IsInverse() {
				backRefName = targetEdge.StructField()
				break
			}
		}
	}

	// Generate the method
	f.Commentf("%s queries the %q edge of a %s.", methodName, e.Name, t.Name)
	if backRefName == "" {
		// Add warning comment when back-reference wasn't found
		// This can happen with M2M through tables or missing inverse edges
		f.Comment("WARNING: No back-reference edge found. Query returns unfiltered results.")
		f.Comment("Consider defining an inverse edge on " + e.Type.Name + " pointing to " + t.Name + ".")
	}
	f.Func().Params(jen.Id("_e").Op("*").Id(t.Name)).Id(methodName).Params().Op("*").Id(targetQueryType).BlockFunc(func(grp *jen.Group) {
		// query := (&{TargetClient}{config: _e.config}).Query()
		grp.Id("query").Op(":=").Parens(jen.Op("&").Id(targetClientType).Values(jen.Dict{
			jen.Id("config"): jen.Id("_e").Dot("config"),
		})).Dot("Query").Call()

		if backRefName != "" {
			// Add where clause using Has{BackRef}With predicate
			// With generic predicates: query = query.Where(target.Has{BackRef}With(current.IDField.EQ(_e.ID)))
			// With Ent predicates: query = query.Where(target.Has{BackRef}With(current.ID(_e.ID)))
			var idPredicate jen.Code
			if h.FeatureEnabled("sql/entpredicates") {
				idPredicate = jen.Qual(currentEntityPkg, "ID").Call(jen.Id("_e").Dot("ID"))
			} else {
				idPredicate = jen.Qual(currentEntityPkg, "IDField").Dot("EQ").Call(jen.Id("_e").Dot("ID"))
			}
			grp.Id("query").Op("=").Id("query").Dot("Where").Call(
				jen.Qual(targetEntityPkg, "Has"+backRefName+"With").Call(idPredicate),
			)
		}

		grp.Return(jen.Id("query"))
	})
}

// genClientQueryEdgeMethod generates Query{Edge} method on the entity client.
// This follows Ent's pattern of providing centralized edge query logic on the client.
//
//	func (c *UserClient) QueryPosts(u *User) *PostQuery
//
// This is in addition to the Query{Edge} method on the entity instance.
func genClientQueryEdgeMethod(h gen.GeneratorHelper, f *jen.File, t *gen.Type, e *gen.Edge) {
	clientName := t.ClientName()
	edgeName := e.StructField()
	methodName := "Query" + edgeName
	targetQueryType := e.Type.QueryName()
	targetClientType := e.Type.ClientName()
	targetEntityPkg := h.EntityPkgPath(e.Type)
	currentEntityPkg := h.EntityPkgPath(t)

	// Get the back-reference edge name for the predicate (same logic as genQueryEdgeMethod)
	backRefName := ""
	if e.Ref != nil {
		backRefName = e.Ref.StructField()
	} else if e.IsInverse() {
		backRefName = gen.Pascal(e.Inverse)
	} else {
		for _, targetEdge := range e.Type.Edges {
			if targetEdge.Type == t && targetEdge.Ref != nil && targetEdge.Ref.Name == e.Name {
				backRefName = targetEdge.StructField()
				break
			}
			if targetEdge.Type == t && targetEdge.IsInverse() {
				backRefName = targetEdge.StructField()
				break
			}
		}
	}

	// Generate the method on the client
	f.Commentf("%s queries the %q edge of a %s.", methodName, e.Name, t.Name)
	if backRefName == "" {
		// Add warning comment when back-reference wasn't found
		f.Comment("WARNING: No back-reference edge found. Query returns unfiltered results.")
		f.Comment("Consider defining an inverse edge on " + e.Type.Name + " pointing to " + t.Name + ".")
	}
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id(methodName).Params(
		jen.Id("_e").Op("*").Id(t.Name),
	).Op("*").Id(targetQueryType).BlockFunc(func(grp *jen.Group) {
		// query := (&{TargetClient}{config: c.config}).Query()
		grp.Id("query").Op(":=").Parens(jen.Op("&").Id(targetClientType).Values(jen.Dict{
			jen.Id("config"): jen.Id("c").Dot("config"),
		})).Dot("Query").Call()

		if backRefName != "" {
			// Add where clause using Has{BackRef}With predicate
			var idPredicate jen.Code
			if h.FeatureEnabled("sql/entpredicates") {
				idPredicate = jen.Qual(currentEntityPkg, "ID").Call(jen.Id("_e").Dot("ID"))
			} else {
				idPredicate = jen.Qual(currentEntityPkg, "IDField").Dot("EQ").Call(jen.Id("_e").Dot("ID"))
			}
			grp.Id("query").Op("=").Id("query").Dot("Where").Call(
				jen.Qual(targetEntityPkg, "Has"+backRefName+"With").Call(idPredicate),
			)
		}

		grp.Return(jen.Id("query"))
	})
}

// genEntityClient generates the entity client struct and methods.
func genEntityClient(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	clientName := t.ClientName()

	// Client struct - uses embedded config type (like Ent)
	f.Commentf("%s is a client for the %s schema.", clientName, t.Name)
	f.Type().Id(clientName).Struct(
		jen.Id("config"), // embedded config
	)

	// New client constructor
	f.Commentf("New%s returns a new %s.", clientName, clientName)
	f.Func().Id("New" + clientName).Params(
		jen.Id("c").Id("config"),
	).Op("*").Id(clientName).Block(
		jen.Return(jen.Op("&").Id(clientName).Values(jen.Dict{
			jen.Id("config"): jen.Id("c"),
		})),
	)

	// Use adds hooks
	f.Commentf("Use adds a list of mutation hooks to the hooks stack.")
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Use").Params(
		jen.Id("hooks").Op("...").Id("Hook"),
	).Block(
		jen.Id("c").Dot("config").Dot("hooks").Dot(t.Name).Op("=").Append(
			jen.Id("c").Dot("config").Dot("hooks").Dot(t.Name),
			jen.Id("hooks").Op("..."),
		),
	)

	// Intercept adds interceptors
	f.Commentf("Intercept adds a list of query interceptors to the interceptors stack.")
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Intercept").Params(
		jen.Id("interceptors").Op("...").Id("Interceptor"),
	).Block(
		jen.Id("c").Dot("config").Dot("inters").Dot(t.Name).Op("=").Append(
			jen.Id("c").Dot("config").Dot("inters").Dot(t.Name),
			jen.Id("interceptors").Op("..."),
		),
	)

	// Create returns a create builder
	f.Commentf("Create returns a builder for creating a %s entity.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Create").Params().Op("*").Id(t.CreateName()).Block(
		jen.Id("mutation").Op(":=").Id("new"+t.MutationName()).Call(
			jen.Id("c").Dot("config"),
			jen.Id("OpCreate"),
		),
		jen.Return(jen.Op("&").Id(t.CreateName()).Values(jen.Dict{
			jen.Id("config"):   jen.Id("c").Dot("config"),
			jen.Id("hooks"):    jen.Id("c").Dot("Hooks").Call(),
			jen.Id("mutation"): jen.Id("mutation"),
		})),
	)

	// CreateBulk returns a bulk create builder
	f.Commentf("CreateBulk returns a builder for creating a bulk of %s entities.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("CreateBulk").Params(
		jen.Id("builders").Op("...").Op("*").Id(t.CreateName()),
	).Op("*").Id(t.CreateBulkName()).Block(
		jen.Return(jen.Op("&").Id(t.CreateBulkName()).Values(jen.Dict{
			jen.Id("config"):   jen.Id("c").Dot("config"),
			jen.Id("builders"): jen.Id("builders"),
		})),
	)

	// MapCreateBulk creates a bulk builder from a slice using a mapping function
	f.Commentf("MapCreateBulk creates a bulk creation builder from the given slice.")
	f.Commentf("For each item in the slice, the set function is called to configure the")
	f.Commentf("builder for that item.")
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("MapCreateBulk").Params(
		jen.Id("slice").Any(),
		jen.Id("setFunc").Func().Params(
			jen.Op("*").Id(t.CreateName()),
			jen.Int(),
		),
	).Op("*").Id(t.CreateBulkName()).Block(
		jen.Id("rv").Op(":=").Qual("reflect", "ValueOf").Call(jen.Id("slice")),
		jen.If(jen.Id("rv").Dot("Kind").Call().Op("!=").Qual("reflect", "Slice")).Block(
			jen.Return(jen.Op("&").Id(t.CreateBulkName()).Values(jen.Dict{
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
		jen.Return(jen.Op("&").Id(t.CreateBulkName()).Values(jen.Dict{
			jen.Id("config"):   jen.Id("c").Dot("config"),
			jen.Id("builders"): jen.Id("builders"),
		})),
	)

	// Update returns an update builder
	f.Commentf("Update returns an update builder for %s.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Update").Params().Op("*").Id(t.UpdateName()).Block(
		jen.Id("mutation").Op(":=").Id("new"+t.MutationName()).Call(
			jen.Id("c").Dot("config"),
			jen.Id("OpUpdate"),
		),
		jen.Return(jen.Op("&").Id(t.UpdateName()).Values(jen.Dict{
			jen.Id("config"):   jen.Id("c").Dot("config"),
			jen.Id("hooks"):    jen.Id("c").Dot("Hooks").Call(),
			jen.Id("mutation"): jen.Id("mutation"),
		})),
	)

	// UpdateOne returns an update-one builder
	f.Commentf("UpdateOne returns an update builder for the given %s entity.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("UpdateOne").Params(
		jen.Id("_e").Op("*").Id(t.Name),
	).Op("*").Id(t.UpdateOneName()).Block(
		jen.Id("mutation").Op(":=").Id("new"+t.MutationName()).Call(
			jen.Id("c").Dot("config"),
			jen.Id("OpUpdateOne"),
			jen.Id("with"+t.Name).Call(jen.Id("_e")),
		),
		jen.Return(jen.Op("&").Id(t.UpdateOneName()).Values(jen.Dict{
			jen.Id("config"):   jen.Id("c").Dot("config"),
			jen.Id("hooks"):    jen.Id("c").Dot("Hooks").Call(),
			jen.Id("mutation"): jen.Id("mutation"),
		})),
	)

	// UpdateOneID returns an update-one-id builder
	f.Commentf("UpdateOneID returns an update builder for the given id.")
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("UpdateOneID").Params(
		jen.Id("id").Add(h.IDType(t)),
	).Op("*").Id(t.UpdateOneName()).Block(
		jen.Id("mutation").Op(":=").Id("new"+t.MutationName()).Call(
			jen.Id("c").Dot("config"),
			jen.Id("OpUpdateOne"),
			jen.Id("with"+t.Name+"ID").Call(jen.Id("id")),
		),
		jen.Return(jen.Op("&").Id(t.UpdateOneName()).Values(jen.Dict{
			jen.Id("config"):   jen.Id("c").Dot("config"),
			jen.Id("hooks"):    jen.Id("c").Dot("Hooks").Call(),
			jen.Id("mutation"): jen.Id("mutation"),
		})),
	)

	// Delete returns a delete builder
	f.Commentf("Delete returns a delete builder for %s.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Delete").Params().Op("*").Id(t.DeleteName()).Block(
		jen.Id("mutation").Op(":=").Id("new"+t.MutationName()).Call(
			jen.Id("c").Dot("config"),
			jen.Id("OpDelete"),
		),
		jen.Return(jen.Op("&").Id(t.DeleteName()).Values(jen.Dict{
			jen.Id("config"):   jen.Id("c").Dot("config"),
			jen.Id("hooks"):    jen.Id("c").Dot("Hooks").Call(),
			jen.Id("mutation"): jen.Id("mutation"),
		})),
	)

	// DeleteOne returns a delete-one builder
	f.Commentf("DeleteOne returns a builder for deleting the given entity.")
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("DeleteOne").Params(
		jen.Id("_e").Op("*").Id(t.Name),
	).Op("*").Id(t.DeleteOneName()).Block(
		jen.Return(jen.Id("c").Dot("DeleteOneID").Call(jen.Id("_e").Dot("ID"))),
	)

	// DeleteOneID returns a delete-one-id builder
	f.Commentf("DeleteOneID returns a builder for deleting the given entity by its id.")
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("DeleteOneID").Params(
		jen.Id("id").Add(h.IDType(t)),
	).Op("*").Id(t.DeleteOneName()).Block(
		jen.Id("builder").Op(":=").Id("c").Dot("Delete").Call().Dot("Where").Call(
			idEQPredicate(h, t, jen.Id("id")),
		),
		jen.Id("builder").Dot("mutation").Dot("id").Op("=").Op("&").Id("id"),
		jen.Id("builder").Dot("mutation").Dot("op").Op("=").Id("OpDeleteOne"),
		jen.Return(jen.Op("&").Id(t.DeleteOneName()).Values(jen.Dict{
			jen.Id("builder"): jen.Id("builder"),
		})),
	)

	// Query returns a query builder
	f.Commentf("Query returns a query builder for %s.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Query").Params().Op("*").Id(t.QueryName()).Block(
		jen.Return(jen.Op("&").Id(t.QueryName()).Values(jen.Dict{
			jen.Id("config"): jen.Id("c").Dot("config"),
			jen.Id("ctx"):    jen.Op("&").Id("QueryContext").Values(jen.Dict{jen.Id("Type"): jen.Id(t.TypeName())}),
			jen.Id("inters"): jen.Id("c").Dot("Interceptors").Call(),
		})),
	)

	// Get returns an entity by id
	f.Commentf("Get returns a %s entity by its id.", t.Name)
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Get").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("id").Add(h.IDType(t)),
	).Params(jen.Op("*").Id(t.Name), jen.Error()).Block(
		jen.Return(jen.Id("c").Dot("Query").Call().Dot("Where").Call(
			idEQPredicate(h, t, jen.Id("id")),
		).Dot("Only").Call(jen.Id("ctx"))),
	)

	// GetX is like Get but panics on error
	f.Commentf("GetX is like Get, but panics if an error occurs.")
	f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("GetX").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("id").Add(h.IDType(t)),
	).Op("*").Id(t.Name).Block(
		jen.List(jen.Id("obj"), jen.Id("err")).Op(":=").Id("c").Dot("Get").Call(jen.Id("ctx"), jen.Id("id")),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Panic(jen.Id("err")),
		),
		jen.Return(jen.Id("obj")),
	)

	// Hooks returns the hooks (combines runtime-registered hooks with schema hooks)
	// Note: Schema hooks include both mixin hooks AND policy hook (at index 0 if policies exist)
	f.Commentf("Hooks returns the client hooks.")
	entityPkg := h.EntityPkgPath(t)
	if t.NumHooks() > 0 || t.NumPolicy() > 0 {
		// If schema has hooks or policies, append them to runtime hooks (matching Ent pattern)
		// Use 3-index slice to ensure append creates new backing array (thread-safe)
		f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Hooks").Params().Index().Id("Hook").Block(
			jen.Id("hooks").Op(":=").Id("c").Dot("config").Dot("hooks").Dot(t.Name),
			jen.Return(jen.Id("append").Call(
				jen.Id("hooks").Index(jen.Empty(), jen.Len(jen.Id("hooks")), jen.Len(jen.Id("hooks"))),
				jen.Qual(entityPkg, "Hooks").Op("[:]..."),
			)),
		)
	} else {
		// No schema hooks or policies, just return runtime hooks
		f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Hooks").Params().Index().Id("Hook").Block(
			jen.Return(jen.Id("c").Dot("config").Dot("hooks").Dot(t.Name)),
		)
	}

	// Interceptors returns the interceptors (combines runtime-registered interceptors with schema interceptors)
	f.Commentf("Interceptors returns the client interceptors.")
	if t.NumInterceptors() > 0 {
		// If schema has interceptors, append them to runtime interceptors (matching Ent pattern)
		// Use 3-index slice to ensure append creates new backing array (thread-safe)
		f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Interceptors").Params().Index().Id("Interceptor").Block(
			jen.Id("inters").Op(":=").Id("c").Dot("config").Dot("inters").Dot(t.Name),
			jen.Return(jen.Id("append").Call(
				jen.Id("inters").Index(jen.Empty(), jen.Len(jen.Id("inters")), jen.Len(jen.Id("inters"))),
				jen.Qual(entityPkg, "Interceptors").Op("[:]..."),
			)),
		)
	} else {
		// No schema interceptors, just return runtime interceptors
		f.Func().Params(jen.Id("c").Op("*").Id(clientName)).Id("Interceptors").Params().Index().Id("Interceptor").Block(
			jen.Return(jen.Id("c").Dot("config").Dot("inters").Dot(t.Name)),
		)
	}

	// Generate Query{Edge} methods on the client (Ent pattern)
	// This provides a centralized location for edge query logic
	for _, e := range t.Edges {
		genClientQueryEdgeMethod(h, f, t, e)
	}
}

// zeroValue returns the zero value for a field type.
// For nillable fields, returns nil (for pointer types).
func zeroValue(h gen.GeneratorHelper, f *gen.Field) jen.Code {
	if f == nil {
		return jen.Lit(0)
	}
	if f.Nillable {
		return jen.Nil()
	}
	return baseZeroValue(h, f)
}

// baseZeroValue returns the zero value for the base type (ignoring nillability).
// Used in mutation getters where the return type is always the base type.
func baseZeroValue(h gen.GeneratorHelper, f *gen.Field) jen.Code {
	if f == nil {
		return jen.Lit(0)
	}

	// Check for enum first - enums need type conversion: EnumType("")
	if f.IsEnum() {
		// Get the enum type and return EnumType("") which is the zero value
		// h.BaseType returns jen.Code, we need to wrap it in a call
		return jen.Add(h.BaseType(f)).Call(jen.Lit(""))
	}

	// Check type enum constant first for special types
	// Note: Type.String() returns the underlying Go type name, not the velox type name
	// TypeUUID.String() returns "[16]byte", TypeTime.String() returns "time.Time"
	typeStr := f.Type.Type.String()

	// Handle UUID type specially - use uuid.Nil constant
	// TypeUUID.String() returns "[16]byte"
	if typeStr == "[16]byte" {
		return jen.Qual("github.com/google/uuid", "Nil")
	}

	// Handle time type specially - time.Time.String() returns "time.Time"
	if typeStr == "time.Time" {
		return jen.Qual("time", "Time").Block()
	}

	switch typeStr {
	case "string":
		return jen.Lit("")
	case "int":
		return jen.Int().Call(jen.Lit(0))
	case "int8":
		return jen.Int8().Call(jen.Lit(0))
	case "int16":
		return jen.Int16().Call(jen.Lit(0))
	case "int32":
		return jen.Int32().Call(jen.Lit(0))
	case "int64":
		return jen.Int64().Call(jen.Lit(0))
	case "uint":
		return jen.Uint().Call(jen.Lit(0))
	case "uint8":
		return jen.Uint8().Call(jen.Lit(0))
	case "uint16":
		return jen.Uint16().Call(jen.Lit(0))
	case "uint32":
		return jen.Uint32().Call(jen.Lit(0))
	case "uint64":
		return jen.Uint64().Call(jen.Lit(0))
	case "float32":
		return jen.Float32().Call(jen.Lit(0))
	case "float64":
		return jen.Float64().Call(jen.Lit(0))
	case "bool":
		return jen.False()
	case "[]byte": // TypeBytes
		// Use empty slice literal instead of nil for non-nillable fields
		return jen.Index().Byte().Block()
	case "json.RawMessage": // TypeJSON
		// Check if field has a custom Go type (e.g., map[string]interface{}, []map[string]interface{})
		if f.HasGoType() {
			// Use the actual Go type's zero value
			return jsonFieldZeroValue(h, f)
		}
		// Default: use empty json.RawMessage
		return jen.Qual("encoding/json", "RawMessage").Block()
	case "other":
		// For custom types (field.Other), use empty struct literal
		if f.HasGoType() && f.Type.PkgPath != "" {
			typeName := f.Type.Ident
			if idx := strings.LastIndex(typeName, "."); idx >= 0 {
				typeName = typeName[idx+1:]
			}
			return jen.Qual(f.Type.PkgPath, typeName).Block()
		}
		if f.Type.Ident != "" {
			return jen.Id(f.Type.Ident).Block()
		}
		// Fallback: use empty struct literal with base type
		return jen.Add(h.BaseType(f)).Block()
	default:
		// For unknown types with custom Go type, use empty struct literal
		if f.HasGoType() && f.Type.PkgPath != "" {
			typeName := f.Type.Ident
			if idx := strings.LastIndex(typeName, "."); idx >= 0 {
				typeName = typeName[idx+1:]
			}
			return jen.Qual(f.Type.PkgPath, typeName).Block()
		}
		// For unknown types, try empty struct literal
		if f.Type.Ident != "" {
			return jen.Id(f.Type.Ident).Block()
		}
		// Fallback: use empty struct literal with base type
		return jen.Add(h.BaseType(f)).Block()
	}
}

// jsonFieldZeroValue returns the zero value for a JSON field with a custom Go type.
// Handles common patterns like map[string]interface{}, []map[string]interface{}, etc.
func jsonFieldZeroValue(h gen.GeneratorHelper, f *gen.Field) jen.Code {
	if f.Type == nil || f.Type.RType == nil {
		// Fallback to empty json.RawMessage if no type info
		return jen.Qual("encoding/json", "RawMessage").Block()
	}

	// Get the RType string representation
	rtypeStr := f.Type.RType.String()

	// Handle common JSON Go types
	switch {
	case rtypeStr == "map[string]interface {}" || rtypeStr == "map[string]interface{}":
		return jen.Map(jen.String()).Interface().Block()
	case rtypeStr == "map[string]any":
		return jen.Map(jen.String()).Any().Block()
	case rtypeStr == "[]map[string]interface {}" || rtypeStr == "[]map[string]interface{}":
		return jen.Index().Map(jen.String()).Interface().Block()
	case rtypeStr == "[]map[string]any":
		return jen.Index().Map(jen.String()).Any().Block()
	case rtypeStr == "[]interface {}" || rtypeStr == "[]interface{}":
		return jen.Index().Interface().Block()
	case rtypeStr == "[]any":
		return jen.Index().Any().Block()
	case strings.HasPrefix(rtypeStr, "[]"):
		// Generic slice type - return empty slice
		return jen.Add(h.BaseType(f)).Block()
	case strings.HasPrefix(rtypeStr, "map["):
		// Generic map type - return empty map
		return jen.Add(h.BaseType(f)).Block()
	default:
		// For struct types or other custom types, use empty literal
		if f.Type.PkgPath != "" {
			typeName := f.Type.Ident
			if idx := strings.LastIndex(typeName, "."); idx >= 0 {
				typeName = typeName[idx+1:]
			}
			return jen.Qual(f.Type.PkgPath, typeName).Block()
		}
		if f.Type.Ident != "" {
			return jen.Id(f.Type.Ident).Block()
		}
		// Fallback to empty json.RawMessage
		return jen.Qual("encoding/json", "RawMessage").Block()
	}
}

// idEQPredicate returns the appropriate ID equality predicate based on feature flags.
// With generic predicates (default): entity.IDField.EQ(id)
// With Ent predicates: entity.ID(id) - function call
func idEQPredicate(h gen.GeneratorHelper, t *gen.Type, idCode jen.Code) jen.Code {
	entityPkg := h.EntityPkgPath(t)
	if h.FeatureEnabled("sql/entpredicates") {
		return jen.Qual(entityPkg, "ID").Call(idCode)
	}
	return jen.Qual(entityPkg, "IDField").Dot("EQ").Call(idCode)
}

// idInPredicate returns the appropriate ID In predicate based on feature flags.
// With generic predicates (default): entity.IDField.In(ids...)
// With Ent predicates: entity.IDIn(ids...) - function call
func idInPredicate(h gen.GeneratorHelper, t *gen.Type, idsCode jen.Code) jen.Code {
	entityPkg := h.EntityPkgPath(t)
	if h.FeatureEnabled("sql/entpredicates") {
		return jen.Qual(entityPkg, "IDIn").Call(idsCode)
	}
	return jen.Qual(entityPkg, "IDField").Dot("In").Call(idsCode)
}
