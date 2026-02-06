package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genEntQL generates the querylanguage.go file with runtime filtering capabilities.
// This is part of the entql feature.
func genEntQL(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile(h.Pkg())
	graph := h.Graph()

	f.ImportName("fmt", "fmt")
	f.ImportName(h.SQLPkg(), "sql")
	f.ImportName(h.PredicatePkg(), "predicate")

	// SchemaConfig type for runtime schema access
	f.Comment("SchemaConfig represents a schema configuration for runtime filtering.")
	f.Type().Id("SchemaConfig").Struct(
		jen.Id("Fields").Index().Id("FieldConfig").Tag(map[string]string{"json": "fields"}),
		jen.Id("Edges").Index().Id("EdgeConfig").Tag(map[string]string{"json": "edges"}),
	)

	// FieldConfig type
	f.Comment("FieldConfig describes a field for runtime filtering.")
	f.Type().Id("FieldConfig").Struct(
		jen.Id("Name").String().Tag(map[string]string{"json": "name"}),
		jen.Id("Column").String().Tag(map[string]string{"json": "column"}),
		jen.Id("Type").String().Tag(map[string]string{"json": "type"}),
	)

	// EdgeConfig type
	f.Comment("EdgeConfig describes an edge for runtime filtering.")
	f.Type().Id("EdgeConfig").Struct(
		jen.Id("Name").String().Tag(map[string]string{"json": "name"}),
		jen.Id("Type").String().Tag(map[string]string{"json": "type"}),
		jen.Id("Table").String().Tag(map[string]string{"json": "table"}),
		jen.Id("Columns").Index().String().Tag(map[string]string{"json": "columns"}),
		jen.Id("Inverse").Bool().Tag(map[string]string{"json": "inverse"}),
	)

	// Generate per-entity schema descriptors
	for _, t := range graph.Nodes {
		genEntQLSchemaDescriptor(h, f, t)
	}

	// TypeSchemas map
	f.Comment("TypeSchemas maps entity type names to their schema configurations.")
	f.Var().Id("TypeSchemas").Op("=").Map(jen.String()).Op("*").Id("SchemaConfig").ValuesFunc(func(vals *jen.Group) {
		for _, t := range graph.Nodes {
			vals.Lit(t.Name).Op(":").Op("&").Id(t.Name + "Schema")
		}
	})

	// GetSchema function
	f.Comment("GetSchema returns the schema configuration for a given type name.")
	f.Func().Id("GetSchema").Params(jen.Id("typeName").String()).Params(jen.Op("*").Id("SchemaConfig"), jen.Bool()).Block(
		jen.List(jen.Id("s"), jen.Id("ok")).Op(":=").Id("TypeSchemas").Index(jen.Id("typeName")),
		jen.Return(jen.Id("s"), jen.Id("ok")),
	)

	// RuntimeFilter type
	f.Comment("RuntimeFilter represents a filter that can be applied at runtime.")
	f.Type().Id("RuntimeFilter").Struct(
		jen.Id("Field").String().Tag(map[string]string{"json": "field"}),
		jen.Id("Op").String().Tag(map[string]string{"json": "op"}),
		jen.Id("Value").Any().Tag(map[string]string{"json": "value"}),
	)

	// CompositeFilter type for AND/OR
	f.Comment("CompositeFilter represents a composite filter with AND/OR logic.")
	f.Type().Id("CompositeFilter").Struct(
		jen.Id("And").Index().Op("*").Id("RuntimeFilter").Tag(map[string]string{"json": "and,omitempty"}),
		jen.Id("Or").Index().Op("*").Id("RuntimeFilter").Tag(map[string]string{"json": "or,omitempty"}),
		jen.Id("Not").Op("*").Id("RuntimeFilter").Tag(map[string]string{"json": "not,omitempty"}),
	)

	// Supported operators
	f.Comment("Supported filter operators.")
	f.Const().Defs(
		jen.Id("OpEQ").Op("=").Lit("eq"),
		jen.Id("OpNEQ").Op("=").Lit("neq"),
		jen.Id("OpGT").Op("=").Lit("gt"),
		jen.Id("OpGTE").Op("=").Lit("gte"),
		jen.Id("OpLT").Op("=").Lit("lt"),
		jen.Id("OpLTE").Op("=").Lit("lte"),
		jen.Id("OpIn").Op("=").Lit("in"),
		jen.Id("OpNotIn").Op("=").Lit("not_in"),
		jen.Id("OpIsNull").Op("=").Lit("is_null"),
		jen.Id("OpIsNotNull").Op("=").Lit("is_not_null"),
		jen.Id("OpContains").Op("=").Lit("contains"),
		jen.Id("OpHasPrefix").Op("=").Lit("has_prefix"),
		jen.Id("OpHasSuffix").Op("=").Lit("has_suffix"),
	)

	// ApplyFilter helper
	f.Comment("ApplyFilter applies a RuntimeFilter to a SQL Selector.")
	f.Func().Id("ApplyFilter").Params(
		jen.Id("s").Op("*").Qual(h.SQLPkg(), "Selector"),
		jen.Id("filter").Op("*").Id("RuntimeFilter"),
		jen.Id("column").String(),
	).Block(
		jen.If(jen.Id("filter").Op("==").Nil()).Block(
			jen.Return(),
		),
		jen.Switch(jen.Id("filter").Dot("Op")).BlockFunc(func(grp *jen.Group) {
			grp.Case(jen.Id("OpEQ")).Block(
				jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "EQ").Call(jen.Id("column"), jen.Id("filter").Dot("Value"))),
			)
			grp.Case(jen.Id("OpNEQ")).Block(
				jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "NEQ").Call(jen.Id("column"), jen.Id("filter").Dot("Value"))),
			)
			grp.Case(jen.Id("OpGT")).Block(
				jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "GT").Call(jen.Id("column"), jen.Id("filter").Dot("Value"))),
			)
			grp.Case(jen.Id("OpGTE")).Block(
				jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "GTE").Call(jen.Id("column"), jen.Id("filter").Dot("Value"))),
			)
			grp.Case(jen.Id("OpLT")).Block(
				jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "LT").Call(jen.Id("column"), jen.Id("filter").Dot("Value"))),
			)
			grp.Case(jen.Id("OpLTE")).Block(
				jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "LTE").Call(jen.Id("column"), jen.Id("filter").Dot("Value"))),
			)
			grp.Case(jen.Id("OpIsNull")).Block(
				jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "IsNull").Call(jen.Id("column"))),
			)
			grp.Case(jen.Id("OpIsNotNull")).Block(
				jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "NotNull").Call(jen.Id("column"))),
			)
			grp.Case(jen.Id("OpContains")).Block(
				jen.If(jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Id("filter").Dot("Value").Op(".").Parens(jen.String()), jen.Id("ok")).Block(
					jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "Contains").Call(jen.Id("column"), jen.Id("v"))),
				),
			)
			grp.Case(jen.Id("OpHasPrefix")).Block(
				jen.If(jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Id("filter").Dot("Value").Op(".").Parens(jen.String()), jen.Id("ok")).Block(
					jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "HasPrefix").Call(jen.Id("column"), jen.Id("v"))),
				),
			)
			grp.Case(jen.Id("OpHasSuffix")).Block(
				jen.If(jen.List(jen.Id("v"), jen.Id("ok")).Op(":=").Id("filter").Dot("Value").Op(".").Parens(jen.String()), jen.Id("ok")).Block(
					jen.Id("s").Dot("Where").Call(jen.Qual(h.SQLPkg(), "HasSuffix").Call(jen.Id("column"), jen.Id("v"))),
				),
			)
		}),
	)

	return f
}

// genEntQLSchemaDescriptor generates the schema descriptor for an entity.
func genEntQLSchemaDescriptor(h gen.GeneratorHelper, f *jen.File, t *gen.Type) {
	schemaName := t.Name + "Schema"

	// Schema variable
	f.Commentf("%s is the schema configuration for %s.", schemaName, t.Name)
	f.Var().Id(schemaName).Op("=").Id("SchemaConfig").Values(jen.Dict{
		jen.Id("Fields"): jen.Index().Id("FieldConfig").ValuesFunc(func(vals *jen.Group) {
			// ID field
			if t.ID != nil {
				vals.Values(jen.Dict{
					jen.Id("Name"):   jen.Lit("id"),
					jen.Id("Column"): jen.Lit(t.ID.Name),
					jen.Id("Type"):   jen.Lit(h.FieldTypeConstant(t.ID)),
				})
			}
			// Regular fields
			for _, field := range t.Fields {
				vals.Values(jen.Dict{
					jen.Id("Name"):   jen.Lit(field.Name),
					jen.Id("Column"): jen.Lit(field.Name),
					jen.Id("Type"):   jen.Lit(h.FieldTypeConstant(field)),
				})
			}
		}),
		jen.Id("Edges"): jen.Index().Id("EdgeConfig").ValuesFunc(func(vals *jen.Group) {
			for _, edge := range t.Edges {
				vals.Values(jen.Dict{
					jen.Id("Name"):  jen.Lit(edge.Name),
					jen.Id("Type"):  jen.Lit(edge.Type.Name),
					jen.Id("Table"): jen.Lit(edge.Type.Table()),
					jen.Id("Columns"): jen.Index().String().ValuesFunc(func(cols *jen.Group) {
						for _, col := range edge.Rel.Columns {
							cols.Lit(col)
						}
					}),
					jen.Id("Inverse"): jen.Lit(edge.IsInverse()),
				})
			}
		}),
	})
}
