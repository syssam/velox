package sql

import (
	"encoding/json"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genGlobalID generates the internal/globalid.go file with global ID utilities.
// This is part of the sql/globalid feature.
func genGlobalID(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile("internal")
	graph := h.Graph()

	f.ImportName("encoding/base64", "base64")
	f.ImportName("fmt", "fmt")
	f.ImportName("strconv", "strconv")
	f.ImportName("strings", "strings")

	// IncrementStarts constant - required for the globalid feature
	// This stores the auto-increment start values for each type
	if incrementStarts, ok := graph.Annotations["IncrementStarts"]; ok {
		jsonBytes, _ := json.Marshal(incrementStarts)
		f.Const().Id("IncrementStarts").Op("=").Lit(string(jsonBytes))
	} else {
		// Default empty map if not set
		f.Const().Id("IncrementStarts").Op("=").Lit("{}")
	}

	// GlobalID type
	f.Comment("GlobalID represents a globally unique identifier for any node.")
	f.Comment("The format is: Type:ID encoded in base64.")
	f.Type().Id("GlobalID").String()

	// NewGlobalID function
	f.Comment("NewGlobalID creates a new GlobalID from a type name and ID.")
	f.Func().Id("NewGlobalID").Params(
		jen.Id("typeName").String(),
		jen.Id("id").Any(),
	).Id("GlobalID").Block(
		jen.Id("raw").Op(":=").Qual("fmt", "Sprintf").Call(jen.Lit("%s:%v"), jen.Id("typeName"), jen.Id("id")),
		jen.Return(jen.Id("GlobalID").Call(
			jen.Qual("encoding/base64", "StdEncoding").Dot("EncodeToString").Call(
				jen.Index().Byte().Parens(jen.Id("raw")),
			),
		)),
	)

	// Decode method
	f.Comment("Decode decodes the GlobalID into its type name and ID.")
	f.Func().Params(jen.Id("g").Id("GlobalID")).Id("Decode").Params().Params(
		jen.Id("typeName").String(),
		jen.Id("id").String(),
		jen.Id("err").Error(),
	).Block(
		jen.List(jen.Id("decoded"), jen.Id("err")).Op(":=").Qual("encoding/base64", "StdEncoding").Dot("DecodeString").Call(
			jen.String().Call(jen.Id("g")),
		),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Lit(""), jen.Lit(""), jen.Qual("fmt", "Errorf").Call(jen.Lit("invalid global id: %w"), jen.Id("err"))),
		),
		jen.Id("parts").Op(":=").Qual("strings", "SplitN").Call(jen.String().Call(jen.Id("decoded")), jen.Lit(":"), jen.Lit(2)),
		jen.If(jen.Len(jen.Id("parts")).Op("!=").Lit(2)).Block(
			jen.Return(jen.Lit(""), jen.Lit(""), jen.Qual("fmt", "Errorf").Call(jen.Lit("invalid global id format"))),
		),
		jen.Return(jen.Id("parts").Index(jen.Lit(0)), jen.Id("parts").Index(jen.Lit(1)), jen.Nil()),
	)

	// Type method
	f.Comment("Type returns the type name from the GlobalID.")
	f.Func().Params(jen.Id("g").Id("GlobalID")).Id("Type").Params().Params(jen.String(), jen.Error()).Block(
		jen.List(jen.Id("typeName"), jen.Id("_"), jen.Id("err")).Op(":=").Id("g").Dot("Decode").Call(),
		jen.Return(jen.Id("typeName"), jen.Id("err")),
	)

	// ID method
	f.Comment("ID returns the ID from the GlobalID.")
	f.Func().Params(jen.Id("g").Id("GlobalID")).Id("ID").Params().Params(jen.String(), jen.Error()).Block(
		jen.List(jen.Id("_"), jen.Id("id"), jen.Id("err")).Op(":=").Id("g").Dot("Decode").Call(),
		jen.Return(jen.Id("id"), jen.Id("err")),
	)

	// String method
	f.Comment("String returns the string representation of the GlobalID.")
	f.Func().Params(jen.Id("g").Id("GlobalID")).Id("String").Params().String().Block(
		jen.Return(jen.String().Call(jen.Id("g"))),
	)

	// IntID helper
	f.Comment("IntID returns the ID as an int.")
	f.Func().Params(jen.Id("g").Id("GlobalID")).Id("IntID").Params().Params(jen.Int(), jen.Error()).Block(
		jen.List(jen.Id("_"), jen.Id("id"), jen.Id("err")).Op(":=").Id("g").Dot("Decode").Call(),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Lit(0), jen.Id("err")),
		),
		jen.Return(jen.Qual("strconv", "Atoi").Call(jen.Id("id"))),
	)

	// Int64ID helper
	f.Comment("Int64ID returns the ID as an int64.")
	f.Func().Params(jen.Id("g").Id("GlobalID")).Id("Int64ID").Params().Params(jen.Int64(), jen.Error()).Block(
		jen.List(jen.Id("_"), jen.Id("id"), jen.Id("err")).Op(":=").Id("g").Dot("Decode").Call(),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Lit(0), jen.Id("err")),
		),
		jen.Return(jen.Qual("strconv", "ParseInt").Call(jen.Id("id"), jen.Lit(10), jen.Lit(64))),
	)

	// Type constants map
	f.Comment("TypeMap maps entity names to their package type names.")
	f.Comment("This is used for resolving global IDs to their entity types.")
	f.Var().Id("TypeMap").Op("=").Map(jen.String()).String().ValuesFunc(func(vals *jen.Group) {
		for _, t := range graph.Nodes {
			vals.Lit(t.Name).Op(":").Lit(t.Name)
		}
	})

	// TypeNames list
	f.Comment("TypeNames returns a list of all entity type names.")
	f.Func().Id("TypeNames").Params().Index().String().Block(
		jen.Return(jen.Index().String().ValuesFunc(func(vals *jen.Group) {
			for _, t := range graph.Nodes {
				vals.Lit(t.Name)
			}
		})),
	)

	// ParseGlobalID helper
	f.Comment("ParseGlobalID parses a global ID string and returns the type and ID.")
	f.Func().Id("ParseGlobalID").Params(
		jen.Id("gid").String(),
	).Params(jen.String(), jen.String(), jen.Error()).Block(
		jen.Return(jen.Id("GlobalID").Call(jen.Id("gid")).Dot("Decode").Call()),
	)

	return f
}
