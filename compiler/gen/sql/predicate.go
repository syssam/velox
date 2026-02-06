package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// reservedPredicateNames are names that conflict with package-level declarations
// and should not be used for shorthand field predicate functions.
var reservedPredicateNames = map[string]bool{
	"Label":       true,
	"OrderOption": true,
	"Hooks":       true,
	"Policy":      true,
	"Table":       true,
	"FieldID":     true,
	"Columns":     true,
	"ForeignKeys": true,
}

// genPredicate generates the predicate file ({entity}/where.go).
func genPredicate(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	// Default: generic predicates (compact, ~90% less code)
	// If FeatureEntPredicates is enabled: Ent-compatible verbose functions
	if h.FeatureEnabled("sql/entpredicates") {
		return genVerbosePredicate(h, t)
	}
	return genGenericPredicate(h, t)
}

// genGenericPredicate generates compact predicate code using Go generics.
// This dramatically reduces generated code size by ~97%.
// Fields are exposed directly at package level for clean Ent-like API:
//
//	user.ID.EQ("123")
//	user.Email.EQ("test@example.com")
//	user.StatusField.EQ(StatusActive)  // Enum fields use Field suffix to avoid type conflict
func genGenericPredicate(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	f := h.NewFile(t.PackageDir())

	// Generate package-level predicate variables for all fields
	genPredicateVars(h, f, t)

	// Generate edge predicates (these still need functions for HasEdge queries)
	for _, edge := range t.Edges {
		genEdgePredicates(h, f, t, edge)
	}

	// And combinator
	f.Comment("And groups predicates with the AND operator between them.")
	f.Func().Id("And").Params(
		jen.Id("predicates").Op("...").Add(h.PredicateType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "AndPredicates").Call(jen.Id("predicates").Op("...")),
		),
	)

	// Or combinator
	f.Comment("Or groups predicates with the OR operator between them.")
	f.Func().Id("Or").Params(
		jen.Id("predicates").Op("...").Add(h.PredicateType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "OrPredicates").Call(jen.Id("predicates").Op("...")),
		),
	)

	// Not negates a predicate
	f.Comment("Not applies the not operator on the given predicate.")
	f.Func().Id("Not").Params(
		jen.Id("p").Add(h.PredicateType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "NotPredicates").Call(jen.Id("p")),
		),
	)

	return f
}

// genPredicateVars generates package-level predicate variables for all fields.
// All fields use Field suffix for consistency and to avoid conflicts:
//
//	user.IDField.EQ("123")
//	user.EmailField.EQ("test@example.com")
//	user.StatusField.EQ(StatusActive)
func genPredicateVars(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	// Collect enum values to detect conflicts (e.g., WorkModeField enum value)
	enumValues := make(map[string]bool)
	for _, field := range t.Fields {
		if field.IsEnum() {
			for _, v := range field.EnumValues() {
				enumValues[field.StructField()+gen.Pascal(v)] = true
			}
		}
	}

	// Generate ID field predicate
	if t.ID != nil {
		idGenericType := "StringField"
		var typeParams []jen.Code
		switch {
		case t.ID.IsInt():
			idGenericType = "IntField"
			typeParams = []jen.Code{h.PredicateType(t)}
		case t.ID.IsInt64():
			idGenericType = "Int64Field"
			typeParams = []jen.Code{h.PredicateType(t)}
		case t.ID.IsUUID():
			idGenericType = "UUIDField"
			uuidType := subpkgBaseType(h, t.ID)
			typeParams = []jen.Code{h.PredicateType(t), uuidType}
		default:
			typeParams = []jen.Code{h.PredicateType(t)}
		}
		f.Comment("IDField is the predicate for the id field.")
		f.Var().Id("IDField").Op("=").Qual(h.SQLPkg(), idGenericType).Types(typeParams...).Call(jen.Id(t.ID.Constant()))
	}

	// Generate regular field predicates
	for _, field := range t.Fields {
		if field.Type == nil {
			continue
		}
		info := getGenericFieldInfo(h, t, field)
		if info.genericType == "" {
			continue // Skip unsupported types (e.g., JSON)
		}

		// Use Field suffix, but if that conflicts with enum value, use Pred suffix
		varName := info.name + "Field"
		if enumValues[varName] {
			varName = info.name + "Pred"
		}

		f.Commentf("%s is the predicate for the %q field.", varName, field.Name)
		f.Var().Id(varName).Op("=").Qual(h.SQLPkg(), info.genericType).Types(info.typeParams...).Call(jen.Id(info.fieldConst))
	}
}

// getGenericFieldInfo returns the generic field type info for a field.
func getGenericFieldInfo(h gen.GeneratorHelper, t *gen.Type, field *gen.Field) fieldInfo {
	structField := field.StructField()
	fieldConst := field.Constant()

	var genericType string
	var typeParams []jen.Code

	switch {
	case field.IsString():
		genericType = "StringField"
		typeParams = []jen.Code{h.PredicateType(t)}
	case field.IsInt():
		genericType = "IntField"
		typeParams = []jen.Code{h.PredicateType(t)}
	case field.IsInt64():
		genericType = "Int64Field"
		typeParams = []jen.Code{h.PredicateType(t)}
	case field.Type != nil && field.Type.Type.Float():
		genericType = "Float64Field"
		typeParams = []jen.Code{h.PredicateType(t)}
	case field.IsBool():
		genericType = "BoolField"
		typeParams = []jen.Code{h.PredicateType(t)}
	case field.IsTime():
		genericType = "TimeField"
		typeParams = []jen.Code{h.PredicateType(t), jen.Qual("time", "Time")}
	case field.IsEnum():
		genericType = "EnumField"
		enumType := subpkgBaseType(h, field)
		typeParams = []jen.Code{h.PredicateType(t), enumType}
	case field.IsUUID():
		genericType = "UUIDField"
		uuidType := subpkgBaseType(h, field)
		typeParams = []jen.Code{h.PredicateType(t), uuidType}
	case field.IsJSON():
		// Skip JSON fields - they have special handling
		return fieldInfo{}
	default:
		genericType = "OtherField"
		otherType := subpkgBaseType(h, field)
		typeParams = []jen.Code{h.PredicateType(t), otherType}
	}

	return fieldInfo{
		name:        structField,
		fieldConst:  fieldConst,
		genericType: genericType,
		typeParams:  typeParams,
	}
}

// fieldInfo holds information about a generic field.
type fieldInfo struct {
	name        string
	fieldConst  string
	genericType string
	typeParams  []jen.Code
}

// genVerbosePredicate generates the traditional verbose predicate code (Ent-compatible).
func genVerbosePredicate(h gen.GeneratorHelper, t *gen.Type) *jen.File {
	f := h.NewFile(t.PackageDir())

	// Generate ID predicate
	if t.ID != nil {
		// Simple ID function: ID(id) returns a predicate
		f.Comment("ID filters vertices based on their ID field.")
		f.Func().Id("ID").Params(
			jen.Id("id").Add(h.IDType(t)),
		).Add(h.PredicateType(t)).Block(
			jen.Return(h.PredicateType(t)).Call(
				jen.Qual(h.SQLPkg(), "FieldEQ").Call(jen.Id(t.ID.Constant()), jen.Id("id")),
			),
		)

		// Generate other ID predicates (IDEQ, IDNEQ, etc.)
		genIDPredicates(h, f, t)
	}

	// Generate shorthand equality functions for comparable fields
	for _, field := range t.Fields {
		// Skip auto-generated edge fields (FK columns), but include user-defined edge fields
		if field.IsEdgeField() && !field.UserDefined {
			continue
		}
		// Generate shorthand only for non-JSON, non-enum comparable fields
		if !field.IsJSON() && !field.IsEnum() && field.Type != nil {
			genFieldShorthand(h, f, t, field)
		}
	}

	// Generate predicates for each field
	// Note: Edge fields (FK columns) also get predicates for filtering by FK values
	for _, field := range t.Fields {
		genFieldPredicates(h, f, t, field)
	}

	// Generate edge predicates
	for _, edge := range t.Edges {
		genEdgePredicates(h, f, t, edge)
	}

	// And combinator
	f.Comment("And groups predicates with the AND operator between them.")
	f.Func().Id("And").Params(
		jen.Id("predicates").Op("...").Add(h.PredicateType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "AndPredicates").Call(jen.Id("predicates").Op("...")),
		),
	)

	// Or combinator
	f.Comment("Or groups predicates with the OR operator between them.")
	f.Func().Id("Or").Params(
		jen.Id("predicates").Op("...").Add(h.PredicateType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "OrPredicates").Call(jen.Id("predicates").Op("...")),
		),
	)

	// Not negates a predicate
	f.Comment("Not applies the not operator on the given predicate.")
	f.Func().Id("Not").Params(
		jen.Id("p").Add(h.PredicateType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "NotPredicates").Call(jen.Id("p")),
		),
	)

	return f
}

// genIDPredicates generates predicates for the ID field.
func genIDPredicates(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	idConst := t.ID.Constant()

	// IDEQ
	f.Comment("IDEQ applies the EQ predicate on the ID field.")
	f.Func().Id("IDEQ").Params(
		jen.Id("id").Add(h.IDType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "FieldEQ").Call(jen.Id(idConst), jen.Id("id")),
		),
	)

	// IDNEQ
	f.Comment("IDNEQ applies the NEQ predicate on the ID field.")
	f.Func().Id("IDNEQ").Params(
		jen.Id("id").Add(h.IDType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "FieldNEQ").Call(jen.Id(idConst), jen.Id("id")),
		),
	)

	// IDIn
	f.Comment("IDIn applies the In predicate on the ID field.")
	f.Func().Id("IDIn").Params(
		jen.Id("ids").Op("...").Add(h.IDType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "FieldIn").Call(jen.Id(idConst), jen.Id("ids").Op("...")),
		),
	)

	// IDNotIn
	f.Comment("IDNotIn applies the NotIn predicate on the ID field.")
	f.Func().Id("IDNotIn").Params(
		jen.Id("ids").Op("...").Add(h.IDType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "FieldNotIn").Call(jen.Id(idConst), jen.Id("ids").Op("...")),
		),
	)

	// IDGT
	f.Comment("IDGT applies the GT predicate on the ID field.")
	f.Func().Id("IDGT").Params(
		jen.Id("id").Add(h.IDType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "FieldGT").Call(jen.Id(idConst), jen.Id("id")),
		),
	)

	// IDGTE
	f.Comment("IDGTE applies the GTE predicate on the ID field.")
	f.Func().Id("IDGTE").Params(
		jen.Id("id").Add(h.IDType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "FieldGTE").Call(jen.Id(idConst), jen.Id("id")),
		),
	)

	// IDLT
	f.Comment("IDLT applies the LT predicate on the ID field.")
	f.Func().Id("IDLT").Params(
		jen.Id("id").Add(h.IDType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "FieldLT").Call(jen.Id(idConst), jen.Id("id")),
		),
	)

	// IDLTE
	f.Comment("IDLTE applies the LTE predicate on the ID field.")
	f.Func().Id("IDLTE").Params(
		jen.Id("id").Add(h.IDType(t)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "FieldLTE").Call(jen.Id(idConst), jen.Id("id")),
		),
	)
}

// genFieldShorthand generates the shorthand equality function for a field.
func genFieldShorthand(h gen.GeneratorHelper, f *jen.File, t *gen.Type, field *gen.Field) {
	structField := field.StructField()

	// Skip reserved names to avoid conflicts with package-level declarations
	if reservedPredicateNames[structField] {
		return
	}

	fieldConst := field.Constant()

	f.Commentf("%s applies equality check predicate on the %q field. It's identical to %sEQ.", structField, field.Name, structField)
	f.Func().Id(structField).Params(
		jen.Id("v").Add(subpkgBaseType(h, field)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Qual(h.SQLPkg(), "FieldEQ").Call(jen.Id(fieldConst), jen.Id("v")),
		),
	)
}

// genFieldPredicates generates predicates for a field using Field.Ops().
// This matches Ent's template approach where each field type has a specific set of operations.
func genFieldPredicates(h gen.GeneratorHelper, f *jen.File, t *gen.Type, field *gen.Field) {
	// Skip fields with nil Type - they can't have predicates generated
	if field.Type == nil {
		return
	}

	structField := field.StructField()
	fieldConst := field.Constant()

	// Iterate through the field's supported operations
	for _, op := range field.Ops() {
		funcName := structField + op.Name()
		arg := "v"
		if op.Variadic() {
			arg = "vs"
		}

		f.Commentf("%s applies the %s predicate on the %q field.", funcName, op.Name(), field.Name)

		// Build the function signature based on operation type
		if op.Niladic() {
			// Niladic operations (IsNil, NotNil) take no arguments
			// Map IsNil -> FieldIsNull, NotNil -> FieldNotNull
			sqlFunc := "Field" + op.Name()
			if op == gen.IsNil {
				sqlFunc = "FieldIsNull"
			} else if op == gen.NotNil {
				sqlFunc = "FieldNotNull"
			}
			f.Func().Id(funcName).Params().Add(h.PredicateType(t)).Block(
				jen.Return(h.PredicateType(t)).Call(
					jen.Qual(h.SQLPkg(), sqlFunc).Call(jen.Id(fieldConst)),
				),
			)
		} else if op.Variadic() {
			// Variadic operations (In, NotIn) take multiple arguments
			f.Func().Id(funcName).Params(
				jen.Id(arg).Op("...").Add(subpkgBaseType(h, field)),
			).Add(h.PredicateType(t)).Block(
				jen.Return(h.PredicateType(t)).Call(
					jen.Qual(h.SQLPkg(), "Field"+op.Name()).Call(jen.Id(fieldConst), jen.Id(arg).Op("...")),
				),
			)
		} else {
			// Regular operations take a single argument
			// String operations (Contains, HasPrefix, HasSuffix, EqualFold, ContainsFold) always take string
			paramType := subpkgBaseType(h, field)
			if isStringOp(op) {
				paramType = jen.String()
			}
			f.Func().Id(funcName).Params(
				jen.Id(arg).Add(paramType),
			).Add(h.PredicateType(t)).Block(
				jen.Return(h.PredicateType(t)).Call(
					jen.Qual(h.SQLPkg(), "Field"+op.Name()).Call(jen.Id(fieldConst), jen.Id(arg)),
				),
			)
		}
	}
}

// isStringOp returns true if the operation is a string-specific operation.
func isStringOp(op gen.Op) bool {
	switch op {
	case gen.Contains, gen.HasPrefix, gen.HasSuffix, gen.EqualFold, gen.ContainsFold:
		return true
	}
	return false
}

// genEdgePredicates generates Has predicates for edges.
func genEdgePredicates(h gen.GeneratorHelper, f *jen.File, t *gen.Type, edge *gen.Edge) {
	structField := edge.StructField()
	schemaConfigEnabled := h.FeatureEnabled("sql/schemaconfig")

	// edgeColumns returns the column specification for the edge
	edgeColumns := func() jen.Code {
		if edge.M2M() {
			return jen.Id(edge.PKConstant()).Op("...")
		}
		return jen.Id(edge.ColumnConstant())
	}

	// edgeSchemaName returns the schema config field name for the edge.
	// This follows Ent's logic for determining which schema the edge relation table is in.
	edgeSchemaName := func() string {
		if edge.OwnFK() {
			return t.Name
		}
		if edge.M2M() {
			if edge.Through != nil {
				return edge.Through.Name
			}
			if edge.IsInverse() {
				return edge.Type.Name + gen.Pascal(edge.Inverse)
			}
			return t.Name + structField
		}
		return edge.Type.Name
	}

	// schemaConfigStatements returns the schema config setup statements when the feature is enabled
	schemaConfigStatements := func() []jen.Code {
		if !schemaConfigEnabled {
			return nil
		}
		return []jen.Code{
			jen.Id("schemaConfig").Op(":=").Qual(h.InternalPkg(), "SchemaConfigFromContext").Call(
				jen.Id("s").Dot("Context").Call(),
			),
			jen.Id("step").Dot("To").Dot("Schema").Op("=").Id("schemaConfig").Dot(edge.Type.Name),
			jen.Id("step").Dot("Edge").Dot("Schema").Op("=").Id("schemaConfig").Dot(edgeSchemaName()),
		}
	}

	// Build the Has predicate function body
	hasBody := []jen.Code{
		jen.Id("step").Op(":=").Qual(h.SQLGraphPkg(), "NewStep").Call(
			jen.Qual(h.SQLGraphPkg(), "From").Call(
				jen.Id("Table"),
				jen.Id(t.ID.Constant()),
			),
			jen.Qual(h.SQLGraphPkg(), "Edge").Call(
				jen.Qual(h.SQLGraphPkg(), h.EdgeRelType(edge)),
				jen.Lit(edge.IsInverse()),
				jen.Id(edge.TableConstant()),
				edgeColumns(),
			),
		),
	}
	hasBody = append(hasBody, schemaConfigStatements()...)
	hasBody = append(hasBody, jen.Qual(h.SQLGraphPkg(), "HasNeighbors").Call(jen.Id("s"), jen.Id("step")))

	// Has predicate
	f.Commentf("Has%s applies the HasEdge predicate on the %q edge.", structField, edge.Name)
	f.Func().Id("Has" + structField).Params().Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(hasBody...),
		),
	)

	// Build the HasWith predicate function body
	hasWithBody := []jen.Code{
		jen.Id("step").Op(":=").Id("new" + structField + "Step").Call(),
	}
	hasWithBody = append(hasWithBody, schemaConfigStatements()...)
	hasWithBody = append(hasWithBody,
		jen.Qual(h.SQLGraphPkg(), "HasNeighborsWith").Call(
			jen.Id("s"),
			jen.Id("step"),
			jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(
				jen.For(jen.List(jen.Id("_"), jen.Id("p")).Op(":=").Range().Id("preds")).Block(
					jen.Id("p").Call(jen.Id("s")),
				),
			),
		),
	)

	// HasWith predicate - uses new*Step() function and predicate.EdgeType
	f.Commentf("Has%sWith applies the HasEdge predicate on the %q edge with a given conditions (other predicates).", structField, edge.Name)
	f.Func().Id("Has" + structField + "With").Params(
		jen.Id("preds").Op("...").Add(h.EdgePredicateType(edge)),
	).Add(h.PredicateType(t)).Block(
		jen.Return(h.PredicateType(t)).Call(
			jen.Func().Params(jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector")).Block(hasWithBody...),
		),
	)
}
