package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// subpkgGoType returns the Go type for a field in the subpackage context.
// For enums without custom GoType, this returns the local subpackage type (e.g., "Type")
// instead of the main package type (e.g., "ABTestingType").
func subpkgGoType(h gen.GeneratorHelper, f *gen.Field) jen.Code {
	if f.Nillable {
		return jen.Op("*").Add(subpkgBaseType(h, f))
	}
	return subpkgBaseType(h, f)
}

// subpkgBaseType returns the base type for a field in the subpackage context.
func subpkgBaseType(h gen.GeneratorHelper, f *gen.Field) jen.Code {
	if f.IsEnum() && !f.HasGoType() {
		// Use local subpackage enum type name
		return jen.Id(f.SubpackageEnumTypeName())
	}
	// For all other types, use the standard helper
	return h.BaseType(f)
}

// genPackage generates the per-entity constant package ({entity}/{entity}.go).
// Follows Ent's meta.tmpl pattern: single const block with all constants.
func genPackage(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	f := h.NewFile(t.PackageDir())
	graph := h.Graph()

	// Generate enum types for enum fields (before constants)
	// In the subpackage, enum types use just the field name (e.g., "Type", "Status")
	for _, field := range t.Fields {
		if field.IsEnum() && !field.HasGoType() {
			genSubpackageEnumType(h, f, t, field)
		}
	}

	// Single const block with all constants (following Ent meta.tmpl pattern)
	f.Const().DefsFunc(func(defs *jen.Group) {
		// Label constant
		defs.Commentf("Label holds the string label denoting the %s type in the database.", t.Name)
		defs.Id("Label").Op("=").Lit(t.Name)

		// ID field constant (if HasOneFieldID)
		if t.ID != nil {
			defs.Commentf("%s holds the string denoting the id field in the database.", t.ID.Constant())
			defs.Id(t.ID.Constant()).Op("=").Lit(t.ID.StorageKey())
		}

		// Field constants (including edge FK fields that are user-defined)
		for _, field := range t.Fields {
			defs.Commentf("%s holds the string denoting the %s field in the database.", field.Constant(), field.Name)
			defs.Id(field.Constant()).Op("=").Lit(field.StorageKey())
		}

		// Edge constants
		for _, edge := range t.Edges {
			defs.Commentf("%s holds the string denoting the %s edge name in mutations.", edge.Constant(), edge.Name)
			defs.Id(edge.Constant()).Op("=").Lit(edge.Name)
		}

		// Related type ID constants (following Ent meta.tmpl lines 11-20)
		// Generate {TypeName}FieldID constant when related type's ID differs from current type's ID
		for _, relType := range t.RelatedTypes() {
			if relType.ID != nil && t.ID != nil && relType.ID.StorageKey() != t.ID.StorageKey() {
				defs.Commentf("%sFieldID holds the string denoting the ID field of the %s.", relType.Name, relType.Name)
				defs.Id(relType.Name + "FieldID").Op("=").Lit(relType.ID.StorageKey())
			}
		}

		// Table constant
		defs.Commentf("Table holds the table name of the %s in the database.", t.Name)
		defs.Id("Table").Op("=").Lit(t.Table())

		// Edge table/inverse/column constants
		for _, edge := range t.Edges {
			// Table constant
			if edge.M2M() {
				defs.Commentf("%s is the table that holds the %s relation/edge. The primary key declared below.", edge.TableConstant(), edge.Name)
			} else {
				defs.Commentf("%s is the table that holds the %s relation/edge.", edge.TableConstant(), edge.Name)
			}
			defs.Id(edge.TableConstant()).Op("=").Lit(edge.Rel.Table)

			// Inverse table constant (only if different from current table)
			if t.Table() != edge.Type.Table() {
				defs.Commentf("%s is the table name for the %s entity.", edge.InverseTableConstant(), edge.Type.Name)
				defs.Comment("It exists in this package in order to avoid circular dependency with the \"" + edge.Type.PackageDir() + "\" package.")
				defs.Id(edge.InverseTableConstant()).Op("=").Lit(edge.Type.Table())
			}

			// Column constant (only for non-M2M edges)
			if !edge.M2M() {
				defs.Commentf("%s is the table column denoting the %s relation/edge.", edge.ColumnConstant(), edge.Name)
				defs.Id(edge.ColumnConstant()).Op("=").Lit(edge.Rel.Column())
			}
		}
	})

	// Columns variable using constant references (following Ent pattern)
	// Includes all fields except deprecated ones (following meta.tmpl line 52)
	f.Commentf("Columns holds all SQL columns for %s fields.", t.Name)
	f.Var().Id("Columns").Op("=").Index().String().ValuesFunc(func(vals *jen.Group) {
		if t.ID != nil {
			vals.Id(t.ID.Constant())
		}
		for _, field := range t.Fields {
			if !field.IsDeprecated() {
				vals.Id(field.Constant())
			}
		}
	})

	// ForeignKeys variable (only for unexported foreign keys - not defined as standalone fields)
	unexportedFKs := t.UnexportedForeignKeys()
	if len(unexportedFKs) > 0 {
		f.Comment("ForeignKeys holds the SQL foreign-keys that are owned by the \"" + t.Table() + "\"")
		f.Comment("table and are not defined as standalone fields in the schema.")
		f.Var().Id("ForeignKeys").Op("=").Index().String().ValuesFunc(func(vals *jen.Group) {
			for _, fk := range unexportedFKs {
				vals.Lit(fk.Field.StorageKey())
			}
		})
	}

	// M2M primary key variables
	hasM2M := false
	for _, edge := range t.Edges {
		if edge.M2M() {
			hasM2M = true
			break
		}
	}
	if hasM2M {
		f.Var().DefsFunc(func(defs *jen.Group) {
			for _, edge := range t.Edges {
				if edge.M2M() && len(edge.Rel.Columns) >= 2 {
					defs.Commentf("%s and %s2 are the table columns denoting the", edge.PKConstant(), edge.ColumnConstant())
					defs.Comment("primary key for the " + edge.Name + " relation (M2M).")
					defs.Id(edge.PKConstant()).Op("=").Index().String().Values(
						jen.Lit(edge.Rel.Columns[0]),
						jen.Lit(edge.Rel.Columns[1]),
					)
				}
			}
		})
	}

	// ValidColumn function (checks both Columns and ForeignKeys)
	f.Comment("ValidColumn reports if the column name is valid (part of the table columns).")
	f.Func().Id("ValidColumn").Params(jen.Id("column").String()).Bool().BlockFunc(func(body *jen.Group) {
		body.For(jen.Id("i").Op(":=").Range().Id("Columns")).Block(
			jen.If(jen.Id("column").Op("==").Id("Columns").Index(jen.Id("i"))).Block(
				jen.Return(jen.True()),
			),
		)
		// Check ForeignKeys if present (only unexported ones)
		if len(unexportedFKs) > 0 {
			body.For(jen.Id("i").Op(":=").Range().Id("ForeignKeys")).Block(
				jen.If(jen.Id("column").Op("==").Id("ForeignKeys").Index(jen.Id("i"))).Block(
					jen.Return(jen.True()),
				),
			)
		}
		body.Return(jen.False())
	})

	// Calculate hooks count (NumHooks + 1 if there's a Policy)
	numHooks := t.NumHooks()
	if t.NumPolicy() > 0 {
		numHooks++
	}
	numInterceptors := t.NumInterceptors()
	numPolicy := t.NumPolicy()

	// Generate runtime comment if hooks or interceptors are present
	if numHooks > 0 || numInterceptors > 0 {
		f.Comment("Note that the variables below are initialized by the runtime")
		f.Comment("package on the initialization of the application. Therefore,")
		f.Comment("it should be imported in the main as follows:")
		f.Comment("")
		// Use full config package path (following Ent meta.tmpl line 63)
		pkgPath := h.Pkg()
		if graph.Config != nil && graph.Config.Package != "" {
			pkgPath = graph.Config.Package
		}
		f.Commentf("	import _ \"%s/runtime\"", pkgPath)
		f.Comment("")
	}

	// Hooks, Interceptors, Policy, validators and defaults (following Ent meta.tmpl lines 52-109)
	// Build list of fields including ID if user-defined (Ent meta.tmpl line 76)
	fields := t.Fields
	idUserDefined := t.ID != nil && t.ID.UserDefined

	hasDefaults := false
	hasValidators := false
	validatorsEnabled, _ := h.Graph().Config.FeatureEnabled(gen.FeatureValidator.Name)
	for _, field := range fields {
		if field.Default || (validatorsEnabled && (field.Validators > 0 || field.IsEnum())) {
			hasDefaults = hasDefaults || field.Default
			hasValidators = hasValidators || (validatorsEnabled && (field.Validators > 0 || field.IsEnum()))
		}
	}
	// Check if ID has a default or validator
	if idUserDefined {
		if t.ID.Default {
			hasDefaults = true
		}
		if validatorsEnabled && t.ID.Validators > 0 {
			hasValidators = true
		}
	}

	if numHooks > 0 || numInterceptors > 0 || numPolicy > 0 || hasDefaults || hasValidators {
		f.Var().DefsFunc(func(defs *jen.Group) {
			// Hooks array
			if numHooks > 0 {
				defs.Id("Hooks").Index(jen.Lit(numHooks)).Qual(h.VeloxPkg(), "Hook")
			}
			// Interceptors array
			if numInterceptors > 0 {
				defs.Id("Interceptors").Index(jen.Lit(numInterceptors)).Qual(h.VeloxPkg(), "Interceptor")
			}
			// Policy
			if numPolicy > 0 {
				defs.Id("Policy").Qual(h.VeloxPkg(), "Policy")
			}

			// Generate per-field: Default → UpdateDefault → Validator (following Ent meta.tmpl lines 77-96)
			// Note: Unlike Ent which handles enum defaults as const values, Velox uses var for compatibility
			// Use subpkg* helpers for enum types to generate local type names (e.g., "Type" not "ABTestingType")
			for _, field := range fields {
				// Default value declaration
				if field.Default {
					defs.Commentf("%s holds the default value on creation for the %q field.", field.DefaultName(), field.Name)
					if field.DefaultFunc() {
						defs.Id(field.DefaultName()).Func().Params().Add(subpkgBaseType(h, field))
					} else {
						defs.Id(field.DefaultName()).Add(subpkgGoType(h, field))
					}
				}
				// UpdateDefault value declaration
				if field.UpdateDefault {
					defs.Commentf("%s holds the default value on update for the %q field.", field.UpdateDefaultName(), field.Name)
					defs.Id(field.UpdateDefaultName()).Func().Params().Add(subpkgBaseType(h, field))
				}
				// Validator declaration (includes enums which get auto-validators)
				if validatorsEnabled && (field.Validators > 0 || field.IsEnum()) {
					defs.Commentf("%s is a validator for the %q field. It is called by the builders before save.", field.Validator(), field.Name)
					defs.Id(field.Validator()).Func().Params(subpkgBaseType(h, field)).Error()
				}
			}

			// ID default and validator (if user-defined)
			if idUserDefined {
				if t.ID.Default {
					defs.Commentf("DefaultID holds the default value on creation for the \"id\" field.")
					if t.ID.DefaultFunc() {
						defs.Id("DefaultID").Func().Params().Add(h.BaseType(t.ID))
					} else {
						defs.Id("DefaultID").Add(h.IDType(t))
					}
				}
				if validatorsEnabled && t.ID.Validators > 0 {
					defs.Commentf("IDValidator is a validator for the \"id\" field. It is called by the builders before save.")
					defs.Id("IDValidator").Func().Params(h.BaseType(t.ID)).Error()
				}
			}
		})
	}

	// OrderOption type (following Ent pattern)
	f.Commentf("OrderOption defines the ordering options for the %s queries.", t.Name)
	f.Type().Id("OrderOption").Func().Params(jen.Op("*").Qual(h.SQLPkg(), "Selector"))

	// ByID ordering function (before other fields, following Ent pattern)
	if t.ID != nil {
		genFieldOrderOption(h, f, t, t.ID)
	}

	// ByXxx ordering functions for fields (including edge FK fields)
	// Only generate for comparable types (following Ent pattern - meta.tmpl line 117)
	for _, field := range t.Fields {
		if field.Type != nil && field.Type.Comparable() {
			genFieldOrderOption(h, f, t, field)
		}
	}

	// Edge ordering functions
	for _, edge := range t.Edges {
		genEdgeOrderOptions(h, f, t, edge)
	}

	// Generate new*Step() helper functions for edges (used by predicates and ordering)
	for _, edge := range t.Edges {
		genEdgeStepFunction(h, f, t, edge)
	}

	return f
}

// genFieldOrderOption generates ordering function for a field.
func genFieldOrderOption(h gen.GeneratorHelper, f *jen.File, t *gen.Type, field *gen.Field) {
	orderName := field.OrderName()

	f.Commentf("%s orders the results by the %s field.", orderName, field.Name)
	f.Func().Id(orderName).Params(
		jen.Id("opts").Op("...").Qual(h.SQLPkg(), "OrderTermOption"),
	).Id("OrderOption").Block(
		jen.Return(jen.Qual(h.SQLPkg(), "OrderByField").Call(
			jen.Id(field.Constant()),
			jen.Id("opts").Op("..."),
		).Dot("ToFunc").Call()),
	)
}

// genEdgeOrderOptions generates ordering functions for an edge.
func genEdgeOrderOptions(h gen.GeneratorHelper, f *jen.File, t *gen.Type, edge *gen.Edge) {
	stepFuncName := "new" + edge.StructField() + "Step"

	if !edge.Unique {
		// ByXxxCount for non-unique edges
		countName, err := edge.OrderCountName()
		if err == nil {
			f.Commentf("%s orders the results by %s count.", countName, edge.Name)
			f.Func().Id(countName).Params(
				jen.Id("opts").Op("...").Qual(h.SQLPkg(), "OrderTermOption"),
			).Id("OrderOption").Block(
				jen.Return(jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
					jen.Qual(h.SQLGraphPkg(), "OrderByNeighborsCount").Call(
						jen.Id("s"),
						jen.Id(stepFuncName).Call(),
						jen.Id("opts").Op("..."),
					),
				)),
			)
		}

		// ByXxx for non-unique edges (order by terms)
		termsName, err := edge.OrderTermsName()
		if err == nil {
			f.Commentf("%s orders the results by %s terms.", termsName, edge.Name)
			f.Func().Id(termsName).Params(
				jen.Id("term").Qual(h.SQLPkg(), "OrderTerm"),
				jen.Id("terms").Op("...").Qual(h.SQLPkg(), "OrderTerm"),
			).Id("OrderOption").Block(
				jen.Return(jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
					jen.Qual(h.SQLGraphPkg(), "OrderByNeighborTerms").Call(
						jen.Id("s"),
						jen.Id(stepFuncName).Call(),
						jen.Append(
							jen.Index().Qual(h.SQLPkg(), "OrderTerm").Values(jen.Id("term")),
							jen.Id("terms").Op("..."),
						).Op("..."),
					),
				)),
			)
		}
	} else {
		// ByXxxField for unique edges (ordering by a field in the related entity)
		fieldName, err := edge.OrderFieldName()
		if err == nil {
			f.Commentf("%s orders the results by %s field.", fieldName, edge.Name)
			f.Func().Id(fieldName).Params(
				jen.Id("field").String(),
				jen.Id("opts").Op("...").Qual(h.SQLPkg(), "OrderTermOption"),
			).Id("OrderOption").Block(
				jen.Return(jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
					jen.Qual(h.SQLGraphPkg(), "OrderByNeighborTerms").Call(
						jen.Id("s"),
						jen.Id(stepFuncName).Call(),
						jen.Qual(h.SQLPkg(), "OrderByField").Call(jen.Id("field"), jen.Id("opts").Op("...")),
					),
				)),
			)
		}
	}
}

// genEdgeStepFunction generates the new*Step() helper function for an edge.
// This function is used by both edge predicates (Has*With) and edge ordering functions.
func genEdgeStepFunction(h gen.GeneratorHelper, f *jen.File, t *gen.Type, edge *gen.Edge) {
	stepFuncName := "new" + edge.StructField() + "Step"

	// edgeColumns returns the column specification for the edge
	edgeColumns := func() jen.Code {
		if edge.M2M() {
			return jen.Id(edge.PKConstant()).Op("...")
		}
		return jen.Id(edge.ColumnConstant())
	}

	// Determine the inverse table constant - use InverseTableConstant if different from current table
	toTable := func() jen.Code {
		if t.Table() != edge.Type.Table() {
			return jen.Id(edge.InverseTableConstant())
		}
		return jen.Id("Table")
	}

	// Determine the target ID field constant (following Ent meta.tmpl lines 157-159)
	toFieldID := func() jen.Code {
		if edge.Type.ID != nil && edge.Type.ID.StorageKey() != t.ID.StorageKey() {
			// Use the type-specific FieldID constant (e.g., ABTestingFieldID)
			return jen.Id(edge.Type.Name + "FieldID")
		}
		return jen.Id(t.ID.Constant())
	}

	f.Func().Id(stepFuncName).Params().Op("*").Qual(h.SQLGraphPkg(), "Step").Block(
		jen.Return(jen.Qual(h.SQLGraphPkg(), "NewStep").Call(
			jen.Qual(h.SQLGraphPkg(), "From").Call(
				jen.Id("Table"),
				jen.Id(t.ID.Constant()),
			),
			jen.Qual(h.SQLGraphPkg(), "To").Call(
				toTable(),
				toFieldID(),
			),
			jen.Qual(h.SQLGraphPkg(), "Edge").Call(
				jen.Qual(h.SQLGraphPkg(), h.EdgeRelType(edge)),
				jen.Lit(edge.IsInverse()),
				jen.Id(edge.TableConstant()),
				edgeColumns(),
			),
		)),
	)
}

// genEnumValidator generates a validator function for an enum field.
func genEnumValidator(h gen.GeneratorHelper, f *jen.File, t *gen.Type, field *gen.Field) {
	validatorName := field.Validator()
	enumName := t.Name + field.StructField()
	graph := h.Graph()
	enumPkg := graph.Config.Package

	f.Commentf("%s validates the %q field value.", validatorName, field.Name)
	f.Func().Id(validatorName).Params(
		jen.Id("v").Qual(enumPkg, enumName),
	).Error().Block(
		jen.If(jen.Op("!").Id("v").Dot("IsValid").Call()).Block(
			jen.Return(jen.Qual("fmt", "Errorf").Call(
				jen.Lit("invalid enum value for "+field.Name+": %v"),
				jen.Id("v"),
			)),
		),
		jen.Return(jen.Nil()),
	)
}

// genSubpackageEnumType generates an enum type in the subpackage.
// In the subpackage, enum types use just the field struct name (e.g., "Type", "Status").
func genSubpackageEnumType(h gen.GeneratorHelper, f *jen.File, t *gen.Type, field *gen.Field) {
	enumName := field.StructField() // Just the field name for subpackage (e.g., "Type")

	// Type definition
	f.Commentf("%s defines the type for the %q enum field.", enumName, field.Name)
	f.Type().Id(enumName).String()

	// Enum constants
	f.Const().DefsFunc(func(defs *jen.Group) {
		for _, e := range field.Enums {
			constName := field.EnumName(e.Value) // e.g., "TypeFEATURE" for value "FEATURE"
			defs.Id(constName).Id(enumName).Op("=").Lit(e.Value)
		}
	})

	// String method
	f.Func().Params(jen.Id("e").Id(enumName)).Id("String").Params().String().Block(
		jen.Return(jen.String().Call(jen.Id("e"))),
	)

	// IsValid method
	f.Func().Params(jen.Id("e").Id(enumName)).Id("IsValid").Params().Bool().BlockFunc(func(body *jen.Group) {
		body.Switch(jen.Id("e")).BlockFunc(func(sw *jen.Group) {
			// Case for all valid enum values
			caseValues := make([]jen.Code, 0, len(field.Enums))
			for _, e := range field.Enums {
				caseValues = append(caseValues, jen.Id(field.EnumName(e.Value)))
			}
			sw.Case(caseValues...).Block(jen.Return(jen.True()))
			sw.Default().Block(jen.Return(jen.False()))
		})
	})

	// Values function
	f.Commentf("%sValues returns all valid values for %s.", enumName, enumName)
	f.Func().Id(enumName + "Values").Params().Index().Id(enumName).BlockFunc(func(body *jen.Group) {
		body.Return(jen.Index().Id(enumName).ValuesFunc(func(vals *jen.Group) {
			for _, e := range field.Enums {
				vals.Id(field.EnumName(e.Value))
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
	// Auto-converts DB value (lowercase) to GraphQL value (UPPERCASE)
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
	// Handles both NamedValues (uppercase DB) and Values (lowercase DB)
	f.Comment("UnmarshalGQL implements graphql.Unmarshaler interface.")
	f.Func().Params(jen.Id("e").Op("*").Id(enumName)).Id("UnmarshalGQL").Params(
		jen.Id("val").Interface(),
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
