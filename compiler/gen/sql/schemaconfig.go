package sql

import (
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genSchemaConfig generates the internal/schemaconfig.go file when the sql/schemaconfig feature is enabled.
func genSchemaConfig(h gen.GeneratorHelper, graph *gen.Graph) *jen.File {
	f := h.NewFile("internal")

	f.ImportName("context", "context")

	// SchemaConfig struct
	f.Comment("SchemaConfig represents alternative schema names for all tables")
	f.Comment("that can be passed at runtime.")
	f.Type().Id("SchemaConfig").StructFunc(func(g *jen.Group) {
		for _, n := range graph.Nodes {
			g.Id(n.Name).String().Comment(n.Name + " table.")
			// Add M2M join table fields (non-inverse only)
			for _, e := range n.Edges {
				if e.M2M() && !e.IsInverse() && e.Through == nil {
					fieldName := n.Name + e.StructField()
					comment := n.Name + "-" + e.Name + "->" + e.Type.Name + " table."
					g.Id(fieldName).String().Comment(comment)
				}
			}
		}
	})

	f.Line()

	// schemaCtxKey type
	f.Type().Id("schemaCtxKey").Struct()

	f.Line()

	// SchemaConfigFromContext function
	f.Comment("SchemaConfigFromContext returns a SchemaConfig stored inside a context, or empty if there isn't one.")
	f.Func().Id("SchemaConfigFromContext").Params(
		jen.Id("ctx").Qual("context", "Context"),
	).Id("SchemaConfig").Block(
		jen.List(jen.Id("config"), jen.Id("_")).Op(":=").Id("ctx").Dot("Value").Call(
			jen.Id("schemaCtxKey").Values(),
		).Assert(jen.Id("SchemaConfig")),
		jen.Return(jen.Id("config")),
	)

	f.Line()

	// NewSchemaConfigContext function
	f.Comment("NewSchemaConfigContext returns a new context with the given SchemaConfig attached.")
	f.Func().Id("NewSchemaConfigContext").Params(
		jen.Id("parent").Qual("context", "Context"),
		jen.Id("config").Id("SchemaConfig"),
	).Qual("context", "Context").Block(
		jen.Return(jen.Qual("context", "WithValue").Call(
			jen.Id("parent"),
			jen.Id("schemaCtxKey").Values(),
			jen.Id("config"),
		)),
	)

	f.Line()

	// SchemaConfigFromRuntime function
	f.Comment("SchemaConfigFromRuntime returns the SchemaConfig the client was configured with")
	f.Comment("(AlternateSchema), carried by runtime.Config, or an empty one if there isn't one.")
	f.Comment("Every generated builder reads it once in its constructor.")
	f.Func().Id("SchemaConfigFromRuntime").Params(
		jen.Id("c").Qual(runtimePkg, "Config"),
	).Id("SchemaConfig").Block(
		jen.List(jen.Id("config"), jen.Id("_")).Op(":=").Id("c").Dot("SchemaConfig").Assert(jen.Id("SchemaConfig")),
		jen.Return(jen.Id("config")),
	)

	return f
}
