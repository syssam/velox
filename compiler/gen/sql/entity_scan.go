package sql

import (
	"strings"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genFieldAssignment generates the assignment code for a field in assignValues.
func genFieldAssignment(h gen.GeneratorHelper, grp *jen.Group, t *gen.Type, field *gen.Field, idx string, receiver string, structField string, localEnums ...bool) {
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
					validGrp.Op("*").Id(receiver).Dot(structField).Op("=").Add(genScanTypeFieldExpr(field, len(localEnums) > 0 && localEnums[0]))
				} else {
					validGrp.Id(receiver).Dot(structField).Op("=").Add(genScanTypeFieldExpr(field, len(localEnums) > 0 && localEnums[0]))
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
// When localEnums is true, enum types are in the current package (entity_model.go) so use jen.Id.
func genScanTypeFieldExpr(f *gen.Field, localEnums bool) jen.Code {
	// Handle enum fields specially because:
	// - If HasGoType() is true: use f.Type.String() which gives the custom type (e.g., schematype.Currency)
	// - If HasGoType() is false: the enum is defined in the entity's subpackage (e.g., abtesting.Type)
	if f.IsEnum() && !f.HasGoType() {
		if localEnums {
			// Same package: enum type is local (e.g., Role(value.String))
			return jen.Id(f.SubpackageEnumTypeName()).Call(jen.Id("value").Dot("String"))
		}
		// Subpackage enum type: abtesting.Type(value.String)
		return jen.Qual(f.EnumPkgPath(), f.SubpackageEnumTypeName()).Call(jen.Id("value").Dot("String"))
	}
	// For all other cases (including enums with custom Go types), use the standard method
	return jen.Id(f.ScanTypeField("value"))
}
