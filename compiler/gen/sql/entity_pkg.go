package sql

import (
	"cmp"
	"fmt"
	"log/slog"
	"slices"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genEntityPkgFileWithRegistry generates a single entity file for the shared entity/ package.
// Unlike entity_struct.go (per-entity sub-packages with any-typed edges), this places ALL
// entity structs in one package so edges can be strongly typed (e.g., []*Post instead of any).
// The enumReg parameter handles enum name collisions across entities. If nil, it is built
// from allNodes.
//
// Output: entity/{entity_name}.go
func genEntityPkgFileWithRegistry(h gen.GeneratorHelper, t *gen.Type, allNodes []*gen.Type, enumReg *entityPkgEnumRegistry) *jen.File {
	if enumReg == nil {
		enumReg = buildEntityPkgEnumRegistry(allNodes)
	}
	f := h.NewFile(h.Pkg())

	// Generate enum types in the entity/ package with entity-prefixed names
	// (e.g., UserRole, PostStatus) to avoid collisions across entities.
	// When two entities produce the same enum name with identical values,
	// only the canonical owner generates the type definition.
	for _, fld := range t.Fields {
		if fld.IsEnum() && !fld.HasGoType() && enumReg.isOwner(t.Name, fld.Name) {
			genEntityPkgEnumType(f, t, fld, enumReg)
		}
	}

	// Generate entity struct with typed edges.
	genEntityPkgStruct(h, f, t, enumReg)

	// Generate Edges struct with typed edge fields.
	genEntityPkgEdgesStruct(h, f, t)

	// Generate ScanValues method.
	genEntityPkgScanValues(h, f, t)

	// Generate AssignValues method.
	genEntityPkgAssignValues(h, f, t, enumReg)

	// Generate Value method for dynamically selected values.
	genEntityPkgValueMethod(h, f, t)

	// Generate FKValue method for unexported foreign keys.
	genEntityPkgFKValueMethod(f, t)

	// Generate IsNode marker for Relay interface.
	f.Comment("IsNode implements the Noder interface.")
	f.Func().Params(jen.Op("*").Id(t.Name)).Id("IsNode").Params().Block()

	// Generate String method.
	genEntityPkgStringMethod(h, f, t)

	// Generate Unwrap — swap the entity's driver from *txDriver back to the
	// base driver so it can be used for reads after Tx.Commit/Rollback.
	// Matches Ent's Unwrap() contract. Panics if the entity is not attached
	// to a transaction.
	f.Commentf("Unwrap detaches the %s from its transaction so it can be used after", t.Name)
	f.Comment("Tx.Commit or Tx.Rollback. Without Unwrap, edge queries and reads performed")
	f.Comment("via this entity after the transaction ends fail with")
	f.Comment(`"sql: transaction has already been committed".`)
	f.Comment("")
	f.Comment("Call this on any entity returned from inside a WithTx closure before")
	f.Comment("returning it to code that may read through it (e.g., GraphQL resolvers).")
	f.Comment("")
	f.Comment("Panics if the entity is not attached to a transaction.")
	f.Func().Params(jen.Id("e").Op("*").Id(t.Name)).Id("Unwrap").Params().Op("*").Id(t.Name).Block(
		jen.List(jen.Id("u"), jen.Id("ok")).Op(":=").Id("e").Dot("config").Dot("Driver").Op(".").Parens(jen.Qual(runtimePkg, "TxDriverUnwrapper")),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Panic(jen.Lit(fmt.Sprintf("velox: %s.Unwrap() called on non-transactional entity", t.Name))),
		),
		jen.Id("e").Dot("config").Dot("Driver").Op("=").Id("u").Dot("BaseDriver").Call(),
		jen.Return(jen.Id("e")),
	)

	// Generate Config accessor and setter for runtime config injection.
	genEntityPkgConfigMethods(f, t)

	// Generate Query{Edge} methods on the entity struct using registry dispatch.
	genEntityPkgEdgeQueryMethods(h, f, t)

	// Generate Querier interface for cross-entity references.
	genEntityPkgQuerierInterface(h, f, t)

	// Generate Selector interface for Select builder.
	genEntityPkgSelectorInterface(h, f, t)

	// Generate GroupByer interface for GroupBy builder.
	genEntityPkgGroupByerInterface(h, f, t)

	return f
}

// =============================================================================
// Entity struct
// =============================================================================

// genEntityPkgStruct generates the entity struct with typed edges for the shared package.
func genEntityPkgStruct(h gen.GeneratorHelper, f *jen.File, t *gen.Type, enumReg *entityPkgEnumRegistry) {
	f.Commentf("%s is the model entity for the %s schema.", t.Name, t.Name)
	f.Type().Id(t.Name).StructFunc(func(group *jen.Group) {
		// ID field
		if t.ID != nil {
			group.Id("ID").Add(h.GoType(t.ID)).Tag(h.StructTags(t.ID))
		}

		// Regular fields
		for _, fld := range t.Fields {
			if fld.IsEdgeField() {
				continue
			}
			group.Id(fld.StructField()).Add(entityPkgGoType(h, t, fld, enumReg)).Tag(h.StructTags(fld))
		}

		// Edge fields (foreign keys)
		for _, fld := range t.Fields {
			if fld.IsEdgeField() {
				group.Id(fld.StructField()).Add(entityPkgGoType(h, t, fld, enumReg)).Tag(h.StructTags(fld))
			}
		}

		// Unexported foreign keys
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

		// config holds the runtime config (driver, etc.) injected after query scan.
		group.Id("config").Qual(runtimePkg, "Config")

		// selectValues for dynamically selected values
		group.Id("selectValues").Qual(h.SQLPkg(), "SelectValues")

		// Edges struct
		group.Id("Edges").Id(t.Name + "Edges").Tag(map[string]string{"json": "edges"})
	})
}

// =============================================================================
// Typed edges struct
// =============================================================================

// genEntityPkgEdgesStruct generates the Edges struct with typed edge fields.
// Because all entities are in the same package, edges can reference concrete types.
func genEntityPkgEdgesStruct(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	edgesName := t.Name + "Edges"
	namedEdgesEnabled := h.FeatureEnabled(gen.FeatureNamedEdges.Name)

	f.Commentf("%s holds the relations/edges for the %s entity.", edgesName, t.Name)
	f.Type().Id(edgesName).StructFunc(func(group *jen.Group) {
		for _, edge := range t.Edges {
			edgeType := entityPkgEdgeGoType(edge)
			group.Id(edge.StructField()).Add(edgeType).Tag(map[string]string{
				"json": edge.Name + ",omitempty",
			})

			// Named edge maps for O2M/M2M edges (namedges feature)
			if namedEdgesEnabled && !edge.Unique {
				group.Id("named" + edge.StructField()).Map(jen.String()).Index().Op("*").Id(edge.Type.Name)
			}
		}

		// loadedTypes bitmask tracks which edges have been loaded.
		if len(t.Edges) > 0 {
			group.Id("loadedTypes").Index(jen.Lit(len(t.Edges))).Bool()
		}

		// totalCount field for GraphQL pagination - lazy-initialized map keyed by edge name.
		// Used by gql_collection.go to track totalCount for Relay connections.
		if len(t.Edges) > 0 {
			group.Id("totalCount").Map(jen.String()).Int()
		}
	})

	// Generate accessor methods for each edge.
	for i, edge := range t.Edges {
		genEntityPkgEdgeAccessors(f, t, edge, i)
	}

	// Generate named edge methods when feature is enabled.
	if namedEdgesEnabled {
		for _, edge := range t.Edges {
			if !edge.Unique {
				genEntityPkgNamedEdgeMethods(f, t, edge)
			}
		}
	}
}

// entityPkgEdgeGoType returns the Go type for an edge in the shared entity package.
// Uses concrete types since all entities are in the same package.
func entityPkgEdgeGoType(e *gen.Edge) jen.Code {
	if e.Unique {
		return jen.Op("*").Id(e.Type.Name)
	}
	return jen.Index().Op("*").Id(e.Type.Name)
}

// genEntityPkgEdgeAccessors generates typed Set/Loaded/OrErr methods for an edge.
func genEntityPkgEdgeAccessors(f *jen.File, t *gen.Type, e *gen.Edge, edgeIndex int) {
	edgesName := t.Name + "Edges"
	edgePascal := e.StructField()
	returnType := entityPkgEdgeGoType(e)

	// XxxOrErr — returns typed edge value or NotLoadedError
	f.Commentf("%s returns the %s value or an error if the edge was not loaded.", edgePascal+"OrErr", e.Name)
	f.Func().Params(jen.Id("e").Id(edgesName)).Id(edgePascal+"OrErr").Params().Params(
		returnType,
		jen.Error(),
	).Block(
		jen.If(jen.Id("e").Dot("loadedTypes").Index(jen.Lit(edgeIndex))).Block(
			jen.Return(jen.Id("e").Dot(edgePascal), jen.Nil()),
		),
		jen.Return(jen.Nil(), jen.Qual(runtimePkg, "NewNotLoadedError").Call(jen.Lit(e.Name))),
	)

	// SetXxx — sets the typed edge value and marks it as loaded
	f.Commentf("Set%s stores the %s edge value and marks it as loaded.", edgePascal, e.Name)
	f.Func().Params(jen.Id("e").Op("*").Id(edgesName)).Id("Set"+edgePascal).Params(
		jen.Id("v").Add(returnType),
	).Block(
		jen.Id("e").Dot(edgePascal).Op("=").Id("v"),
		jen.Id("e").Dot("loadedTypes").Index(jen.Lit(edgeIndex)).Op("=").True(),
	)

	// XxxLoaded — reports whether the edge was loaded
	f.Commentf("%sLoaded reports whether the %s edge was loaded.", edgePascal, e.Name)
	f.Func().Params(jen.Id("e").Id(edgesName)).Id(edgePascal + "Loaded").Params().Bool().Block(
		jen.Return(jen.Id("e").Dot("loadedTypes").Index(jen.Lit(edgeIndex))),
	)

	// MarkXxxLoaded — marks the edge as loaded even if empty (used by edge_load).
	f.Commentf("Mark%sLoaded marks the %s edge as loaded, even if empty.", edgePascal, e.Name)
	f.Func().Params(jen.Id("e").Op("*").Id(edgesName)).Id("Mark" + edgePascal + "Loaded").Params().Block(
		jen.Id("e").Dot("loadedTypes").Index(jen.Lit(edgeIndex)).Op("=").True(),
	)

	// SetXxxAny — any-typed setter for runtime edge loading (avoids cross-package type assertion).
	// For unique edges: accepts single any value.
	// For non-unique edges: accepts []any children.
	f.Commentf("Set%sAny sets the %s edge from an any value (used by runtime edge loading).", edgePascal, e.Name)
	f.Func().Params(jen.Id("e").Op("*").Id(edgesName)).Id("Set" + edgePascal + "Any").Params(
		jen.Id("v").Any(),
	).BlockFunc(func(grp *jen.Group) {
		if e.Unique {
			// Unique edge: v is *Type
			grp.If(jen.Id("v").Op("!=").Nil()).Block(
				jen.Id("e").Dot(edgePascal).Op("=").Id("v").Assert(jen.Op("*").Id(e.Type.Name)),
			)
		} else {
			// Non-unique edge: v is []any
			grp.Id("children").Op(":=").Id("v").Assert(jen.Index().Any())
			grp.Id("typed").Op(":=").Make(jen.Index().Op("*").Id(e.Type.Name), jen.Len(jen.Id("children")))
			grp.For(jen.List(jen.Id("i"), jen.Id("c")).Op(":=").Range().Id("children")).Block(
				jen.Id("typed").Index(jen.Id("i")).Op("=").Id("c").Assert(jen.Op("*").Id(e.Type.Name)),
			)
			grp.Id("e").Dot(edgePascal).Op("=").Id("typed")
		}
		grp.Id("e").Dot("loadedTypes").Index(jen.Lit(edgeIndex)).Op("=").True()
	})

	// GetXxx — returns the raw edge value as any (for runtime compatibility).
	f.Commentf("Get%s returns the %s edge value as any (for runtime edge loading).", edgePascal, e.Name)
	f.Func().Params(jen.Id("e").Id(edgesName)).Id("Get" + edgePascal).Params().Any().Block(
		jen.Return(jen.Id("e").Dot(edgePascal)),
	)
}

// genEntityPkgNamedEdgeMethods generates Named{Edge} and appendNamed{Edge} methods
// for the shared entity/ package. This is the entity-pkg equivalent of genNamedEdgeMethods.
func genEntityPkgNamedEdgeMethods(f *jen.File, t *gen.Type, e *gen.Edge) {
	edgeName := e.StructField()
	namedField := "named" + edgeName
	edgeType := jen.Index().Op("*").Id(e.Type.Name)

	// Named{Edge}(name string) ([]*EdgeType, error)
	f.Commentf("Named%s returns the %s edge with the given name or an error if it was not loaded.", edgeName, e.Name)
	f.Func().Params(jen.Id("e").Op("*").Id(t.Name)).Id("Named"+edgeName).Params(
		jen.Id("name").String(),
	).Params(edgeType, jen.Error()).Block(
		jen.If(jen.Id("e").Dot("Edges").Dot(namedField).Op("==").Nil()).Block(
			jen.Return(jen.Nil(), jen.Qual(runtimePkg, "NewNotLoadedError").Call(jen.Lit(e.Name))),
		),
		jen.List(jen.Id("edges"), jen.Id("ok")).Op(":=").Id("e").Dot("Edges").Dot(namedField).Index(jen.Id("name")),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Nil(), jen.Qual(runtimePkg, "NewNotLoadedError").Call(jen.Lit(e.Name))),
		),
		jen.Return(jen.Id("edges"), jen.Nil()),
	)

	// AppendNamed{Edge}(name string, edges ...*EdgeType)
	// Exported because query/ package (separate package) calls this method.
	f.Commentf("AppendNamed%s adds the given edges to the named edge with the given name.", edgeName)
	f.Func().Params(jen.Id("e").Op("*").Id(t.Name)).Id("AppendNamed"+edgeName).Params(
		jen.Id("name").String(),
		jen.Id("edges").Op("...").Op("*").Id(e.Type.Name),
	).Block(
		jen.If(jen.Id("e").Dot("Edges").Dot(namedField).Op("==").Nil()).Block(
			jen.Id("e").Dot("Edges").Dot(namedField).Op("=").Make(jen.Map(jen.String()).Add(edgeType)),
		),
		jen.Id("e").Dot("Edges").Dot(namedField).Index(jen.Id("name")).Op("=").Append(
			jen.Id("e").Dot("Edges").Dot(namedField).Index(jen.Id("name")),
			jen.Id("edges").Op("..."),
		),
	)
}

// =============================================================================
// ScanValues / AssignValues
// =============================================================================

// genEntityPkgScanValues generates ScanValues using string literals for column names.
// This avoids importing entity sub-packages, keeping entity/ as a leaf package.
func genEntityPkgScanValues(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	f.Comment("ScanValues returns the types for scanning values from sql.Rows.")
	f.Func().Params(jen.Op("*").Id(t.Name)).Id("ScanValues").Params(
		jen.Id("columns").Index().String(),
	).Params(jen.Index().Any(), jen.Error()).BlockFunc(func(grp *jen.Group) {
		grp.Id("values").Op(":=").Make(jen.Index().Any(), jen.Len(jen.Id("columns")))
		grp.For(jen.Id("i").Op(":=").Range().Id("columns")).BlockFunc(func(forGrp *jen.Group) {
			forGrp.Switch(jen.Id("columns").Index(jen.Id("i"))).BlockFunc(func(switchGrp *jen.Group) {
				// Group fields by scan type
				type fieldRef struct {
					constant jen.Code
				}
				scanTypeFields := make(map[string][]fieldRef)

				// ID field
				if t.ID != nil {
					scanType := t.ID.NewScanType()
					scanTypeFields[scanType] = append(scanTypeFields[scanType], fieldRef{
						constant: jen.Lit(t.ID.StorageKey()),
					})
				}

				// Regular fields
				for _, field := range t.Fields {
					if field.HasValueScanner() || (field.IsEdgeField() && !field.UserDefined) || field.Type == nil {
						continue
					}
					scanType := field.NewScanType()
					scanTypeFields[scanType] = append(scanTypeFields[scanType], fieldRef{
						constant: jen.Lit(field.StorageKey()),
					})
				}

				// Generate cases grouped by scan type
				scanTypes := make([]string, 0, len(scanTypeFields))
				for scanType := range scanTypeFields {
					scanTypes = append(scanTypes, scanType)
				}
				slices.Sort(scanTypes)
				for _, scanType := range scanTypes {
					refs := scanTypeFields[scanType]
					caseExprs := make([]jen.Code, len(refs))
					for i, ref := range refs {
						caseExprs[i] = ref.constant
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
						slog.Error("ScanValueFunc failed", "entity", t.Name, "field", field.Name, "error", err)
						continue
					}
					switchGrp.Case(jen.Lit(field.StorageKey())).Block(
						jen.Id("values").Index(jen.Id("i")).Op("=").Id(scanValueFunc).Call(),
					)
				}

				// Handle unexported foreign keys using inline column name literals
				for _, fk := range t.UnexportedForeignKeys() {
					fkField := fk.Field
					scanType := fkField.NewScanType()
					switchGrp.Case(jen.Lit(fkField.StorageKey())).Block(
						jen.Id("values").Index(jen.Id("i")).Op("=").Id(scanType),
					)
				}

				// Default
				switchGrp.Default().Block(
					jen.Id("values").Index(jen.Id("i")).Op("=").New(jen.Qual(h.SQLPkg(), "UnknownType")),
				)
			})
		})
		grp.Return(jen.Id("values"), jen.Nil())
	})
}

// genEntityPkgAssignValues generates AssignValues using string literals for column names.
// This avoids importing entity sub-packages, keeping entity/ as a leaf package.
func genEntityPkgAssignValues(h gen.GeneratorHelper, f *jen.File, t *gen.Type, enumReg *entityPkgEnumRegistry) {
	f.Comment("AssignValues assigns the values that were returned from sql.Rows (after scanning)")
	f.Commentf("to the %s fields.", t.Name)
	f.Func().Params(jen.Id("e").Op("*").Id(t.Name)).Id("AssignValues").Params(
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
				// ID field
				if t.ID != nil {
					switchGrp.Case(jen.Lit(t.ID.StorageKey())).BlockFunc(func(caseGrp *jen.Group) {
						genFieldAssignment(h, caseGrp, t, t.ID, "i", "e", "ID", true)
					})
				}

				// Regular fields
				for _, fld := range t.Fields {
					if fld.IsEdgeField() && !fld.UserDefined {
						continue
					}
					switchGrp.Case(jen.Lit(fld.StorageKey())).BlockFunc(func(caseGrp *jen.Group) {
						genEntityPkgFieldAssignment(h, caseGrp, t, fld, "i", "e", fld.StructField(), enumReg)
					})
				}

				// Unexported foreign keys using inline column name literals
				for _, fk := range t.UnexportedForeignKeys() {
					switchGrp.Case(jen.Lit(fk.Field.StorageKey())).BlockFunc(func(caseGrp *jen.Group) {
						genEntityPkgFieldAssignment(h, caseGrp, t, fk.Field, "i", "e", fk.StructField(), enumReg)
					})
				}
			})
		})
		grp.Return(jen.Nil())
	})
}

// genEntityPkgFieldAssignment generates a field assignment for the entity/ package.
// For enum fields without a custom Go type, the enum type is defined locally in the
// entity/ package (e.g., UserRole, PostStatus), so we use a local reference.
func genEntityPkgFieldAssignment(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, field *gen.Field, idx string, receiver string, structField string, enumReg *entityPkgEnumRegistry) {
	if field.IsEnum() && !field.HasGoType() {
		// Local enum type in entity/ package: e.g., UserRole, PostStatus.
		// Use the registry to resolve potential name collisions.
		enumTypeName := enumReg.resolve(t.Name, field.Name)
		scanType := field.ScanType() // "sql.NullString"
		grp.If(
			jen.List(jen.Id("value"), jen.Id("ok")).Op(":=").Id("values").Index(jen.Id(idx)).Op(".").Parens(jen.Op("*").Id(scanType)),
			jen.Op("!").Id("ok"),
		).Block(
			jen.Return(jen.Qual("fmt", "Errorf").Call(jen.Lit("unexpected type %T for field "+field.Name), jen.Id("values").Index(jen.Id(idx)))),
		).Else().Block(
			jen.If(jen.Id("value").Dot("Valid")).BlockFunc(func(validGrp *jen.Group) {
				if field.NillableValue() {
					// Nillable enum: allocate and assign via pointer.
					validGrp.Id("v").Op(":=").Id(enumTypeName).Call(jen.Id("value").Dot("String"))
					validGrp.Id(receiver).Dot(structField).Op("=").Op("&").Id("v")
				} else {
					validGrp.Id(receiver).Dot(structField).Op("=").Id(enumTypeName).Call(jen.Id("value").Dot("String"))
				}
			}),
		)
		return
	}
	genFieldAssignment(h, grp, t, field, idx, receiver, structField, true)
}

// =============================================================================
// Value / FKValue methods
// =============================================================================

// genEntityPkgValueMethod generates the Value method for accessing dynamically selected values.
func genEntityPkgValueMethod(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	valueName := t.ValueName()
	f.Commentf("%s returns the ent.Value that was dynamically selected and assigned to the %s.", valueName, t.Name)
	f.Comment("This includes values selected through modifiers, order, etc.")
	f.Func().Params(jen.Id("e").Op("*").Id(t.Name)).Id(valueName).Params(
		jen.Id("name").String(),
	).Params(jen.Qual(h.VeloxPkg(), "Value"), jen.Error()).Block(
		jen.Return(jen.Id("e").Dot("selectValues").Dot("Get").Call(jen.Id("name"))),
	)
}

// genEntityPkgFKValueMethod generates a FKValue method for unexported FK fields.
func genEntityPkgFKValueMethod(f *jen.File, t *gen.Type) {
	fks := t.UnexportedForeignKeys()
	if len(fks) == 0 {
		return
	}
	recv := "e"
	f.Comment("FKValue returns the value of a foreign key field by column name.")
	f.Func().Params(jen.Id(recv).Op("*").Id(t.Name)).Id("FKValue").Params(
		jen.Id("column").String(),
	).Any().BlockFunc(func(body *jen.Group) {
		body.Switch(jen.Id("column")).BlockFunc(func(sw *jen.Group) {
			for _, fk := range fks {
				sw.Case(jen.Lit(fk.Field.Name)).Block(
					jen.Return(jen.Id(recv).Dot(fk.StructField())),
				)
			}
			sw.Default().Block(
				jen.Return(jen.Nil()),
			)
		})
	})
}

// =============================================================================
// String method
// =============================================================================

// genEntityPkgStringMethod generates the String() method.
// Uses strings.Builder directly — no intermediate []string, no fmt.Fprintf overhead
// for string fields (uses WriteString instead of format-string parsing).
func genEntityPkgStringMethod(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	prefixLen := len(t.Name) + 1 // length of "User("

	writeSep := func(grp *jen.Group) {
		grp.If(jen.Id("b").Dot("Len").Call().Op(">").Lit(prefixLen)).Block(
			jen.Id("b").Dot("WriteString").Call(jen.Lit(", ")),
		)
	}

	// writeField emits the fastest serialization for the field type.
	// String fields use WriteString (no format parsing). Others use Fprintf.
	writeField := func(grp *jen.Group, fld *gen.Field, accessor jen.Code) {
		if fld.IsString() && !fld.IsEnum() {
			// Fast path: string fields — avoid fmt.Fprintf overhead.
			grp.Id("b").Dot("WriteString").Call(jen.Lit(fld.Name + "="))
			grp.Id("b").Dot("WriteString").Call(accessor)
		} else {
			grp.Qual("fmt", "Fprintf").Call(jen.Op("&").Id("b"), jen.Lit(fld.Name+"=%v"), accessor)
		}
	}

	f.Commentf("String implements the fmt.Stringer interface.")
	f.Func().Params(jen.Id("e").Op("*").Id(t.Name)).Id("String").Params().String().BlockFunc(func(grp *jen.Group) {
		grp.Var().Id("b").Qual("strings", "Builder")
		grp.Id("b").Dot("WriteString").Call(jen.Lit(t.Name + "("))

		if t.ID != nil {
			grp.If(jen.Id("e").Dot("ID").Op("!=").Add(zeroValue(h, t.ID))).Block(
				jen.Qual("fmt", "Fprintf").Call(jen.Op("&").Id("b"), jen.Lit("id=%v"), jen.Id("e").Dot("ID")),
			)
		}

		for _, fld := range t.Fields {
			if fld.IsEdgeField() {
				continue
			}
			if fld.Nillable {
				grp.If(jen.Id("e").Dot(fld.StructField()).Op("!=").Nil()).BlockFunc(func(ifGrp *jen.Group) {
					writeSep(ifGrp)
					writeField(ifGrp, fld, jen.Op("*").Id("e").Dot(fld.StructField()))
				})
			} else {
				writeSep(grp)
				writeField(grp, fld, jen.Id("e").Dot(fld.StructField()))
			}
		}

		grp.Id("b").Dot("WriteString").Call(jen.Lit(")"))
		grp.Return(jen.Id("b").Dot("String").Call())
	})
}

// =============================================================================
// Config accessor and setter
// =============================================================================

// genEntityPkgConfigMethods generates Config() and SetConfig() methods on the entity struct.
// The config is injected by the query/ package after scanning entities from the database.
// This allows edge traversal methods (QueryXxx) to access the driver dialect from the entity
// itself (Ent style) rather than requiring a client reference.
func genEntityPkgConfigMethods(f *jen.File, t *gen.Type) {
	configMethod := t.ConfigMethodName()
	setConfigMethod := t.SetConfigMethodName()

	// Config()/RuntimeConfig() — returns the runtime config.
	f.Commentf("%s returns the runtime config (injected after query scan).", configMethod)
	f.Func().Params(jen.Id("e").Op("*").Id(t.Name)).Id(configMethod).Params().Qual(runtimePkg, "Config").Block(
		jen.Return(jen.Id("e").Dot("config")),
	)

	// SetConfig()/SetRuntimeConfig() — sets the runtime config.
	f.Commentf("%s injects the runtime config (called by the query package after scan).", setConfigMethod)
	f.Func().Params(jen.Id("e").Op("*").Id(t.Name)).Id(setConfigMethod).Params(
		jen.Id("c").Qual(runtimePkg, "Config"),
	).Block(
		jen.Id("e").Dot("config").Op("=").Id("c"),
	)
}

// =============================================================================
// Edge query methods on entity struct
// =============================================================================

// genEntityPkgEdgeQueryMethods generates Query{Edge} methods on the entity struct
// in the shared entity/ package. Uses runtime.NewEntityQuery + SetPath to avoid
// importing query/ or entity sub-packages (which would create import cycles).
// All table names, field IDs, and edge metadata use string literals.
//
//	func (_e *Task) QueryMilestones() TaskMilestoneQuerier
func genEntityPkgEdgeQueryMethods(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	sqlgraphPkg := h.SQLGraphPkg()

	for _, e := range t.Edges {
		edgePascal := e.StructField()
		targetQuerierIface := e.Type.Name + "Querier"

		f.Commentf("Query%s queries the %q edge of the %s.", edgePascal, e.Name, t.Name)
		f.Func().Params(jen.Id("_e").Op("*").Id(t.Name)).Id("Query" + edgePascal).Params().Id(targetQuerierIface).BlockFunc(func(grp *jen.Group) {
			// tq := runtime.NewEntityQuery(targetTypeName, _e.config)
			grp.Id("tq").Op(":=").Qual(runtimePkg, "NewEntityQuery").Call(
				jen.Lit(e.Type.Name),
				jen.Id("_e").Dot("config"),
			)
			// Wire the shared *InterceptorStore pointer from config.
			grp.Id("_is").Op(",").Id("_").Op(":=").Id("_e").Dot("config").Dot("InterStore").Assert(jen.Op("*").Id("InterceptorStore"))
			grp.If(jen.Id("_is").Op("==").Nil()).Block(
				jen.Id("_is").Op("=").Op("&").Id("InterceptorStore").Values(),
			)
			grp.Add(assertSetInterStore("tq", "", jen.Id("_is")))
			// Privacy: wire the TARGET entity's policy (if any) via the
			// runtime registry — same pattern as the entity client edge
			// methods. No-op when the target has no policy.
			grp.Id("_tp").Op(":=").Qual(runtimePkg, "EntityPolicy").Call(jen.Lit(e.Type.Name))
			grp.If(jen.Id("_tp").Op("!=").Nil()).Block(
				assertSetPolicy("tq", h.VeloxPkg(), jen.Id("_tp")),
			)
			// Type-assert to SetPath interface
			grp.Id("tq").Op(".").Parens(
				jen.Interface(
					jen.Id("SetPath").Params(
						jen.Func().Params(
							jen.Qual("context", "Context"),
						).Params(
							jen.Op("*").Qual(h.SQLPkg(), "Selector"),
							jen.Error(),
						),
					),
				),
			).Dot("SetPath").Call(jen.Func().Params(
				jen.Id("ctx").Qual("context", "Context"),
			).Params(
				jen.Op("*").Qual(h.SQLPkg(), "Selector"),
				jen.Error(),
			).BlockFunc(func(body *jen.Group) {
				// All string literals to avoid importing sub-packages.
				body.Id("id").Op(":=").Id("_e").Dot("ID")

				// Edge columns — use Rel.Columns directly as string literals.
				// Guard: test fixtures may have empty Rel; fall back to empty string.
				var edgeColumns jen.Code
				if e.M2M() && len(e.Rel.Columns) >= 2 {
					vals := make([]jen.Code, len(e.Rel.Columns))
					for i, col := range e.Rel.Columns {
						vals[i] = jen.Lit(col)
					}
					body.Id("_edgePKs").Op(":=").Index().String().Values(vals...)
					edgeColumns = jen.Id("_edgePKs").Op("...")
				} else if len(e.Rel.Columns) > 0 {
					edgeColumns = jen.Lit(e.Rel.Columns[0])
				} else {
					edgeColumns = jen.Lit("")
				}

				body.Id("step").Op(":=").Qual(sqlgraphPkg, "NewStep").Call(
					jen.Qual(sqlgraphPkg, "From").Call(
						jen.Lit(t.Table()),
						jen.Lit(t.ID.StorageKey()),
						jen.Id("id"),
					),
					jen.Qual(sqlgraphPkg, "To").Call(
						jen.Lit(e.Type.Table()),
						jen.Lit(e.Type.ID.StorageKey()),
					),
					jen.Qual(sqlgraphPkg, "Edge").Call(
						jen.Qual(sqlgraphPkg, h.EdgeRelType(e)),
						jen.Lit(e.IsInverse()),
						jen.Lit(e.Rel.Table),
						edgeColumns,
					),
				)
				body.Return(
					jen.Qual(sqlgraphPkg, "Neighbors").Call(
						jen.Id("_e").Dot("config").Dot("Driver").Dot("Dialect").Call(),
						jen.Id("step"),
					),
					jen.Nil(),
				)
			}))
			grp.Return(jen.Id("tq").Op(".").Parens(jen.Id(targetQuerierIface)))
		})
	}
}

// =============================================================================
// Querier interface
// =============================================================================

// genEntityPkgQuerierInterface generates a query interface for the entity type.
// All interfaces live in the same package, so edge methods can reference other
// entity Querier types without import cycles (e.g., WithPosts takes func(PostQuerier)).
func genEntityPkgQuerierInterface(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	ifaceName := t.Name + "Querier"
	sqlPkg := h.SQLPkg()

	// Resolve ID type from the entity's ID field.
	idType := h.IDType(t)

	f.Commentf("%s defines the query interface for %s entities.", ifaceName, t.Name)
	f.Type().Id(ifaceName).InterfaceFunc(func(grp *jen.Group) {
		// --- Terminal methods ---
		grp.Id("All").Params(jen.Id("ctx").Qual("context", "Context")).Params(
			jen.Index().Op("*").Id(t.Name), jen.Error(),
		)
		grp.Id("First").Params(jen.Id("ctx").Qual("context", "Context")).Params(
			jen.Op("*").Id(t.Name), jen.Error(),
		)
		grp.Id("Only").Params(jen.Id("ctx").Qual("context", "Context")).Params(
			jen.Op("*").Id(t.Name), jen.Error(),
		)
		grp.Id("Count").Params(jen.Id("ctx").Qual("context", "Context")).Params(
			jen.Int(), jen.Error(),
		)
		grp.Id("Exist").Params(jen.Id("ctx").Qual("context", "Context")).Params(
			jen.Bool(), jen.Error(),
		)
		grp.Id("ExistX").Params(jen.Qual("context", "Context")).Bool()

		// --- Debug ---
		grp.Id("SQL").Params(jen.Id("ctx").Qual("context", "Context")).Params(
			jen.String(), jen.Index().Any(), jen.Error(),
		)

		// --- ID methods ---
		grp.Id("IDs").Params(jen.Id("ctx").Qual("context", "Context")).Params(
			jen.Index().Add(idType), jen.Error(),
		)
		grp.Id("FirstID").Params(jen.Id("ctx").Qual("context", "Context")).Params(
			idType, jen.Error(),
		)
		grp.Id("OnlyID").Params(jen.Id("ctx").Qual("context", "Context")).Params(
			idType, jen.Error(),
		)

		// --- Chainable methods (return self interface) ---
		grp.Id("Where").Params(
			jen.Id("ps").Op("...").Qual(h.PredicatePkg(), t.Name),
		).Id(ifaceName)
		grp.Id("Limit").Params(jen.Id("n").Int()).Id(ifaceName)
		grp.Id("Offset").Params(jen.Id("n").Int()).Id(ifaceName)
		grp.Id("Order").Params(
			jen.Id("o").Op("...").Func().Params(jen.Op("*").Qual(sqlPkg, "Selector")),
		).Id(ifaceName)
		grp.Id("Unique").Params(jen.Id("unique").Bool()).Id(ifaceName)

		// --- WithXxx edge eager loading ---
		for _, e := range t.Edges {
			targetIface := e.Type.Name + "Querier"
			withMethod := "With" + e.StructField()
			grp.Id(withMethod).Params(
				jen.Id("opts").Op("...").Func().Params(jen.Id(targetIface)),
			).Id(ifaceName)
		}

		// --- Select / GroupBy / Aggregate / Modify ---
		selectorName := t.Name + "Selector"
		groupByerName := t.Name + "GroupByer"
		grp.Id("Select").Params(jen.Id("fields").Op("...").String()).Id(selectorName)
		grp.Id("Modify").Params(
			jen.Id("modifiers").Op("...").Func().Params(jen.Op("*").Qual(sqlPkg, "Selector")),
		).Id(ifaceName)
		grp.Id("GroupBy").Params(
			jen.Id("field").String(), jen.Id("fields").Op("...").String(),
		).Id(groupByerName)
		grp.Id("Aggregate").Params(
			jen.Id("fns").Op("...").Qual(runtimePkg, "AggregateFunc"),
		).Id(selectorName)

		// --- Scan ---
		grp.Id("Scan").Params(jen.Qual("context", "Context"), jen.Any()).Error()
		grp.Id("ScanX").Params(jen.Qual("context", "Context"), jen.Any())

		// --- Clone ---
		grp.Id("Clone").Params().Id(ifaceName)

		// --- ForUpdate/ForShare (when Lock feature is enabled) ---
		if h.FeatureEnabled(gen.FeatureLock.Name) {
			grp.Id("ForUpdate").Params(
				jen.Id("opts").Op("...").Qual(sqlPkg, "LockOption"),
			).Id(ifaceName)
			grp.Id("ForShare").Params(
				jen.Id("opts").Op("...").Qual(sqlPkg, "LockOption"),
			).Id(ifaceName)
		}

		// --- Paginate (when GraphQL RelayConnection is annotated) ---
		// Check for graphql annotation with RelayConnection enabled.
		if ann, ok := t.Annotations["graphql"].(map[string]any); ok {
			if rc, _ := ann["RelayConnection"].(bool); rc {
				connName := t.Name + "Connection"
				optName := t.Name + "PaginateOption"
				grp.Id("Paginate").Params(
					jen.Id("ctx").Qual("context", "Context"),
					jen.Id("after").Op("*").Qual("github.com/syssam/velox/contrib/graphql/gqlrelay", "Cursor"),
					jen.Id("first").Op("*").Int(),
					jen.Id("before").Op("*").Qual("github.com/syssam/velox/contrib/graphql/gqlrelay", "Cursor"),
					jen.Id("last").Op("*").Int(),
					jen.Id("opts").Op("...").Id(optName),
				).Params(jen.Op("*").Id(connName), jen.Error())
			}
		}
	})
}

// genEntityPkgSelectorInterface generates the Selector interface for an entity.
// This mirrors the runtime.Selector public API: Aggregate (returns self),
// Scan, ScanX, and scalar accessors (Strings/StringsX/String/StringX, etc.).
func genEntityPkgSelectorInterface(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	ifaceName := t.Name + "Selector"

	idType := h.IDType(t)

	f.Commentf("%s defines the select interface for %s entities.", ifaceName, t.Name)
	f.Type().Id(ifaceName).InterfaceFunc(func(grp *jen.Group) {
		// Aggregate returns self.
		grp.Id("Aggregate").Params(
			jen.Id("fns").Op("...").Qual(runtimePkg, "AggregateFunc"),
		).Id(ifaceName)

		// Entity query execution methods — promoted from the embedded *XxxQuery.
		// These allow .Select(...).All(ctx) like Ent.
		grp.Id("All").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.Index().Op("*").Id(t.Name), jen.Error())
		grp.Id("AllX").Params(jen.Id("ctx").Qual("context", "Context")).Index().Op("*").Id(t.Name)
		grp.Id("First").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.Op("*").Id(t.Name), jen.Error())
		grp.Id("FirstX").Params(jen.Id("ctx").Qual("context", "Context")).Op("*").Id(t.Name)
		grp.Id("Only").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.Op("*").Id(t.Name), jen.Error())
		grp.Id("OnlyX").Params(jen.Id("ctx").Qual("context", "Context")).Op("*").Id(t.Name)
		grp.Id("Count").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.Int(), jen.Error())
		grp.Id("CountX").Params(jen.Id("ctx").Qual("context", "Context")).Int()
		grp.Id("Exist").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.Bool(), jen.Error())
		grp.Id("ExistX").Params(jen.Id("ctx").Qual("context", "Context")).Bool()
		grp.Id("IDs").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.Index().Add(idType), jen.Error())
		grp.Id("FirstID").Params(jen.Id("ctx").Qual("context", "Context")).Params(idType, jen.Error())
		grp.Id("OnlyID").Params(jen.Id("ctx").Qual("context", "Context")).Params(idType, jen.Error())

		// Scan / ScanX
		grp.Id("Scan").Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("v").Any(),
		).Error()
		grp.Id("ScanX").Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("v").Any(),
		)

		// Scalar accessors: Strings, Ints, Float64s, Bools and singular/X variants.
		genSelectorScalarMethods(grp)
	})
}

// genEntityPkgGroupByerInterface generates the GroupByer interface for an entity.
// Identical to the Selector interface except Aggregate returns GroupByer instead of Selector.
func genEntityPkgGroupByerInterface(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	ifaceName := t.Name + "GroupByer"

	f.Commentf("%s defines the group-by interface for %s entities.", ifaceName, t.Name)
	f.Type().Id(ifaceName).InterfaceFunc(func(grp *jen.Group) {
		// Aggregate returns self.
		grp.Id("Aggregate").Params(
			jen.Id("fns").Op("...").Qual(runtimePkg, "AggregateFunc"),
		).Id(ifaceName)

		// Scan / ScanX
		grp.Id("Scan").Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("v").Any(),
		).Error()
		grp.Id("ScanX").Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("v").Any(),
		)

		// Scalar accessors: Strings, Ints, Float64s, Bools and singular/X variants.
		genSelectorScalarMethods(grp)
	})
}

// genSelectorScalarMethods adds the scalar accessor methods shared by
// both Selector and GroupByer interfaces.
func genSelectorScalarMethods(grp *jen.Group) {
	// String accessors
	grp.Id("Strings").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.Index().String(), jen.Error())
	grp.Id("StringsX").Params(jen.Id("ctx").Qual("context", "Context")).Index().String()
	grp.Id("String").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.String(), jen.Error())
	grp.Id("StringX").Params(jen.Id("ctx").Qual("context", "Context")).String()

	// Int accessors
	grp.Id("Ints").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.Index().Int(), jen.Error())
	grp.Id("IntsX").Params(jen.Id("ctx").Qual("context", "Context")).Index().Int()
	grp.Id("Int").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.Int(), jen.Error())
	grp.Id("IntX").Params(jen.Id("ctx").Qual("context", "Context")).Int()

	// Float64 accessors
	grp.Id("Float64s").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.Index().Float64(), jen.Error())
	grp.Id("Float64sX").Params(jen.Id("ctx").Qual("context", "Context")).Index().Float64()
	grp.Id("Float64").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.Float64(), jen.Error())
	grp.Id("Float64X").Params(jen.Id("ctx").Qual("context", "Context")).Float64()

	// Bool accessors
	grp.Id("Bools").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.Index().Bool(), jen.Error())
	grp.Id("BoolsX").Params(jen.Id("ctx").Qual("context", "Context")).Index().Bool()
	grp.Id("Bool").Params(jen.Id("ctx").Qual("context", "Context")).Params(jen.Bool(), jen.Error())
	grp.Id("BoolX").Params(jen.Id("ctx").Qual("context", "Context")).Bool()
}

// =============================================================================
// Enum name collision detection
// =============================================================================

// enumOwner records which entity+field produced a given enum name.
type enumOwner struct {
	EntityName string
	FieldName  string
	Values     []string // sorted enum values for equality check
}

// entityPkgEnumRegistry maps a resolved enum type name to its canonical owner
// and tracks which entity+field should use which resolved name.
type entityPkgEnumRegistry struct {
	// resolvedName maps entity name -> field name -> resolved enum type name.
	resolvedName map[string]map[string]string
	// owner maps each resolved enum type name to the first entity that generates it.
	// Used to skip duplicate generation when two entities share the same enum.
	owner map[string]enumOwner
}

// setResolved records the resolved enum type name for an entity+field pair.
func (r *entityPkgEnumRegistry) setResolved(entityName, fieldName, enumName string) {
	if r.resolvedName[entityName] == nil {
		r.resolvedName[entityName] = make(map[string]string)
	}
	r.resolvedName[entityName][fieldName] = enumName
}

// resolve returns the enum type name for a given entity+field pair.
func (r *entityPkgEnumRegistry) resolve(entityName, fieldName string) string {
	if fields, ok := r.resolvedName[entityName]; ok {
		if name, ok := fields[fieldName]; ok {
			return name
		}
	}
	// Fallback to the default EnumTypeName behavior.
	return entityName + pascal(fieldName)
}

// isOwner returns true if this entity+field is the canonical owner that should
// generate the enum type definition (as opposed to a duplicate that reuses it).
func (r *entityPkgEnumRegistry) isOwner(entityName, fieldName string) bool {
	resolved := r.resolve(entityName, fieldName)
	own, ok := r.owner[resolved]
	if !ok {
		return true
	}
	return own.EntityName == entityName && own.FieldName == fieldName
}

// buildEntityPkgEnumRegistry scans all entities to detect enum name collisions
// in the shared entity/ package and produces a registry with resolved names.
//
// When two entities produce the same EnumTypeName:
//   - Same enum values: share the type; only the first entity (alphabetically) generates it.
//   - Different values: disambiguate by appending "Field" to the later entity's enum name.
func buildEntityPkgEnumRegistry(allNodes []*gen.Type) *entityPkgEnumRegistry {
	reg := &entityPkgEnumRegistry{
		resolvedName: make(map[string]map[string]string),
		owner:        make(map[string]enumOwner),
	}

	// Collect all enum fields grouped by their default EnumTypeName.
	type enumEntry struct {
		entityName string
		fieldName  string
		enumName   string
		values     []string
	}
	byName := make(map[string][]enumEntry)

	for _, t := range allNodes {
		for _, fld := range t.Fields {
			if !fld.IsEnum() || fld.HasGoType() {
				continue
			}
			name := fld.EnumTypeName()
			vals := make([]string, len(fld.Enums))
			for i, e := range fld.Enums {
				vals[i] = e.Value
			}
			slices.Sort(vals)
			byName[name] = append(byName[name], enumEntry{
				entityName: t.Name,
				fieldName:  fld.Name,
				enumName:   name,
				values:     vals,
			})
		}
	}

	// Process each group.
	for name, entries := range byName {
		if len(entries) == 1 {
			// No collision — use the default name.
			e := entries[0]
			reg.setResolved(e.entityName, e.fieldName, name)
			reg.owner[name] = enumOwner{EntityName: e.entityName, FieldName: e.fieldName, Values: e.values}
			continue
		}

		// Sort entries by entity name for deterministic owner selection.
		slices.SortFunc(entries, func(a, b enumEntry) int {
			return cmp.Compare(a.entityName, b.entityName)
		})

		// Check if all entries have identical values.
		allSame := true
		for i := 1; i < len(entries); i++ {
			if !slices.Equal(entries[0].values, entries[i].values) {
				allSame = false
				break
			}
		}

		if allSame {
			// Same values: share the type. First entity (alphabetically) owns it.
			for _, e := range entries {
				reg.setResolved(e.entityName, e.fieldName, name)
			}
			reg.owner[name] = enumOwner{
				EntityName: entries[0].entityName,
				FieldName:  entries[0].fieldName,
				Values:     entries[0].values,
			}
			slog.Debug("shared enum type across entities",
				"enum", name,
				"owner", entries[0].entityName,
				"shared_with", entries[1].entityName,
			)
		} else {
			// Different values: disambiguate. First (alphabetically) keeps the
			// original name, subsequent entries get a unique suffix.
			first := entries[0]
			reg.setResolved(first.entityName, first.fieldName, name)
			reg.owner[name] = enumOwner{EntityName: first.entityName, FieldName: first.fieldName, Values: first.values}

			for _, e := range entries[1:] {
				disambiguated := e.entityName + pascal(e.fieldName) + "Enum"
				// Ensure uniqueness: if the disambiguated name is already taken
				// (e.g., three entities with same enum name), append a counter.
				if _, exists := reg.owner[disambiguated]; exists {
					for i := 2; ; i++ {
						candidate := fmt.Sprintf("%s%d", disambiguated, i)
						if _, exists = reg.owner[candidate]; !exists {
							disambiguated = candidate
							break
						}
					}
				}
				reg.setResolved(e.entityName, e.fieldName, disambiguated)
				reg.owner[disambiguated] = enumOwner{EntityName: e.entityName, FieldName: e.fieldName, Values: e.values}
				slog.Warn("enum name collision with different values — disambiguating",
					"enum", name,
					"entity", e.entityName,
					"field", e.fieldName,
					"resolved_as", disambiguated,
				)
			}
		}
	}

	return reg
}

// entityPkgGoType returns the Go type for a field in the entity package context.
// For enums, uses the local entity-package type (e.g., UserRole, PostStatus).
func entityPkgGoType(h gen.GeneratorHelper, t *gen.Type, f *gen.Field, enumReg *entityPkgEnumRegistry) jen.Code {
	if f.IsEnum() && !f.HasGoType() {
		enumType := jen.Id(enumReg.resolve(t.Name, f.Name))
		if f.Nillable {
			return jen.Op("*").Add(enumType)
		}
		return enumType
	}
	return h.GoType(f)
}

// genEntityPkgEnumType generates an enum type definition in the entity/ package.
// Uses entity-prefixed names (e.g., UserRole, PostStatus) to avoid collisions
// since all entities share the same package.
func genEntityPkgEnumType(f *jen.File, t *gen.Type, field *gen.Field, enumReg *entityPkgEnumRegistry) {
	// Resolve the enum name via the registry to handle collisions.
	enumName := enumReg.resolve(t.Name, field.Name)

	// Type definition
	f.Commentf("%s defines the type for the %q enum field.", enumName, field.Name)
	f.Type().Id(enumName).String()

	// enumConstName builds a constant name: enumName + value suffix.
	// e.g., enumName="UserRole", value="admin" → "UserRoleAdmin"
	enumConstName := func(value string) string {
		fullEnumConst := field.EnumName(value) // "RoleAdmin" for role field, value "admin"
		sf := field.StructField()              // "Role"
		if len(fullEnumConst) > len(sf) && fullEnumConst[:len(sf)] == sf {
			return enumName + fullEnumConst[len(sf):] // "UserRole" + "Admin"
		}
		return enumName + fullEnumConst
	}

	// Enum constants — use resolved enum name as prefix to avoid collisions.
	f.Const().DefsFunc(func(defs *jen.Group) {
		for _, e := range field.Enums {
			defs.Id(enumConstName(e.Value)).Id(enumName).Op("=").Lit(e.Value)
		}
	})

	// String method
	f.Func().Params(jen.Id("e").Id(enumName)).Id("String").Params().String().Block(
		jen.Return(jen.String().Call(jen.Id("e"))),
	)

	// IsValid method
	f.Func().Params(jen.Id("e").Id(enumName)).Id("IsValid").Params().Bool().BlockFunc(func(body *jen.Group) {
		body.Switch(jen.Id("e")).BlockFunc(func(sw *jen.Group) {
			caseValues := make([]jen.Code, 0, len(field.Enums))
			for _, e := range field.Enums {
				caseValues = append(caseValues, jen.Id(enumConstName(e.Value)))
			}
			sw.Case(caseValues...).Block(jen.Return(jen.True()))
			sw.Default().Block(jen.Return(jen.False()))
		})
	})

	// Values function — entity-prefixed name to avoid collisions.
	valuesFunc := enumName + "Values"
	f.Commentf("%s returns all valid values for %s.", valuesFunc, enumName)
	f.Func().Id(valuesFunc).Params().Index().Id(enumName).BlockFunc(func(body *jen.Group) {
		body.Return(jen.Index().Id(enumName).ValuesFunc(func(vals *jen.Group) {
			for _, e := range field.Enums {
				vals.Id(enumConstName(e.Value))
			}
		}))
	})

	// Scan method (for sql.Scanner interface)
	f.Func().Params(jen.Id("e").Op("*").Id(enumName)).Id("Scan").Params(jen.Id("value").Any()).Error().Block(
		jen.Switch(jen.Id("v").Op(":=").Id("value").Assert(jen.Type())).Block(
			jen.Case(jen.String()).Block(
				jen.Op("*").Id("e").Op("=").Id(enumName).Call(jen.Id("v")),
				jen.Return(jen.Nil()),
			),
			jen.Case(jen.Index().Byte()).Block(
				jen.Op("*").Id("e").Op("=").Id(enumName).Call(jen.Id("v")),
				jen.Return(jen.Nil()),
			),
			jen.Default().Block(
				jen.Return(jen.Qual("fmt", "Errorf").Call(
					jen.Lit("invalid type %T for enum "+enumName),
					jen.Id("value"),
				)),
			),
		),
	)

	// Value method (for driver.Valuer interface)
	f.Func().Params(jen.Id("e").Id(enumName)).Id("Value").Params().Params(
		jen.Qual("database/sql/driver", "Value"),
		jen.Error(),
	).Block(
		jen.Return(jen.String().Call(jen.Id("e")), jen.Nil()),
	)

	// MarshalGQL method (for graphql.Marshaler interface)
	f.Comment("MarshalGQL implements graphql.Marshaler interface.")
	f.Func().Params(jen.Id("e").Id(enumName)).Id("MarshalGQL").Params(
		jen.Id("w").Qual("io", "Writer"),
	).Block(
		jen.Qual("io", "WriteString").Call(
			jen.Id("w"),
			jen.Qual("strconv", "Quote").Call(
				jen.Qual("strings", "ToUpper").Call(jen.Id("e").Dot("String").Call()),
			),
		),
	)

	// UnmarshalGQL method (for graphql.Unmarshaler interface)
	f.Comment("UnmarshalGQL implements graphql.Unmarshaler interface.")
	f.Func().Params(jen.Id("e").Op("*").Id(enumName)).Id("UnmarshalGQL").Params(
		jen.Id("val").Any(),
	).Error().Block(
		jen.List(jen.Id("str"), jen.Id("ok")).Op(":=").Id("val").Assert(jen.String()),
		jen.If(jen.Op("!").Id("ok")).Block(
			jen.Return(jen.Qual("fmt", "Errorf").Call(
				jen.Lit("enum %T must be a string"),
				jen.Id("val"),
			)),
		),
		// Try as-is first (for NamedValues with uppercase DB values)
		jen.Op("*").Id("e").Op("=").Id(enumName).Call(jen.Id("str")),
		jen.If(jen.Id("e").Dot("IsValid").Call()).Block(
			jen.Return(jen.Nil()),
		),
		// Try lowercase (for Values with lowercase DB values)
		jen.Op("*").Id("e").Op("=").Id(enumName).Call(
			jen.Qual("strings", "ToLower").Call(jen.Id("str")),
		),
		jen.If(jen.Op("!").Id("e").Dot("IsValid").Call()).Block(
			jen.Return(jen.Qual("fmt", "Errorf").Call(
				jen.Lit("%s is not a valid "+enumName),
				jen.Id("str"),
			)),
		),
		jen.Return(jen.Nil()),
	)
}

// =============================================================================
// Builder Interfaces (Creator, Updater, UpdateOner, Deleter, DeleteOner, Clienter)
// =============================================================================

// Builder interfaces (Creator, Updater, etc.) and Clienter have been removed.
// Root Client now imports entity sub-packages directly and uses concrete types.
// Only Querier, Selector, and GroupByer survive as interfaces in entity/.
