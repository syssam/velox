package graphql

import (
	"context"

	gqlgenGraphql "github.com/99designs/gqlgen/graphql"
	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/runtime"
)

// RegisterFieldCollector registers the gqlgen-backed field collection function
// with the Velox runtime. Called by NewExtension() — not registered at init time
// to avoid side effects when importing the package only for annotations.
func RegisterFieldCollector() {
	runtime.SetFieldCollector(gqlCollectFields)
}

// gqlCollectFields is the gqlgen-backed implementation of runtime.DefaultFieldCollector.
// It inspects the GraphQL field selections and configures the query for optimal
// eager loading — selecting only requested columns and pre-loading requested edges.
//
// This is a generic implementation that replaces per-entity generated CollectFields
// methods, eliminating cross-entity imports and reducing generated code by ~220K lines
// for large schemas.
func gqlCollectFields(ctx context.Context, q runtime.FieldCollectable, fields map[string]string, edges map[string]runtime.EdgeMeta, satisfies []string) error {
	fc := gqlgenGraphql.GetFieldContext(ctx)
	if fc == nil {
		return nil
	}
	// GetOperationContext panics if not set, but if FieldContext exists,
	// OperationContext is always present (gqlgen sets both before resolvers).
	opCtx := gqlgenGraphql.GetOperationContext(ctx)
	return gqlCollectField(q, fields, edges, opCtx, fc.Field, satisfies)
}

// gqlCollectField processes GraphQL field selections and configures the query.
// The satisfies parameter lists additional GraphQL interfaces for union/interface resolution.
func gqlCollectField(
	q runtime.FieldCollectable,
	fields map[string]string,
	edges map[string]runtime.EdgeMeta,
	opCtx *gqlgenGraphql.OperationContext,
	collected gqlgenGraphql.CollectedField,
	satisfies []string,
) error {
	unknownSeen := false
	selectedFields := make([]string, 0, len(fields))

	// Always include the ID column.
	selectedFields = append(selectedFields, q.GetIDColumn())

	for _, field := range gqlgenGraphql.CollectFields(opCtx, collected.Selections, satisfies) {
		switch field.Name {
		case "id", "__typename":
			// Skip introspection fields.

		default:
			// Check if it's a scalar field.
			if col, ok := fields[field.Name]; ok {
				selectedFields = append(selectedFields, col)
				continue
			}

			// Check if it's an edge.
			if edge, ok := edges[field.Name]; ok {
				var opts []runtime.LoadOption

				// For Relay connection edges, handle pagination args.
				// When cursor args (after/before) are present, skip eager-loading
				// entirely — the resolver's Paginate call will handle cursors correctly.
				if edge.Relay {
					var hasCursors bool
					opts, hasCursors = gqlCollectRelayEdgeOpts(opCtx, field)
					if hasCursors {
						// Still add FK columns so the parent query includes them.
						selectedFields = append(selectedFields, edge.FKColumns...)
						continue
					}
				}

				// Add FK columns to parent selection.
				selectedFields = append(selectedFields, edge.FKColumns...)

				q.WithEdgeLoad(edge.Name, opts...)
				continue
			}

			// Unknown field (custom resolver, etc.) — fall back to SELECT *.
			unknownSeen = true
		}
	}

	// Only apply column projection if all fields are known.
	// Unknown fields might be resolved by custom resolvers that need all columns.
	if !unknownSeen && len(selectedFields) > 0 {
		for _, col := range selectedFields {
			q.GetCtx().AppendFieldOnce(col)
		}
	}

	return nil
}

// gqlCollectRelayEdgeOpts extracts Relay pagination options from GraphQL arguments.
// Returns the options and whether cursor args (after/before) are present.
// When cursors are present, eager-loading should be skipped in favor of Paginate.
func gqlCollectRelayEdgeOpts(
	opCtx *gqlgenGraphql.OperationContext,
	field gqlgenGraphql.CollectedField,
) ([]runtime.LoadOption, bool) {
	var opts []runtime.LoadOption

	// Extract pagination args from GraphQL field arguments.
	args := field.ArgumentMap(opCtx.Variables)

	// Cursor args mean the resolver's Paginate call must handle this edge.
	_, hasAfter := args["after"]
	_, hasBefore := args["before"]
	if hasAfter || hasBefore {
		return nil, true
	}

	if first, ok := args["first"]; ok {
		if n, ok := gqlToInt(first); ok {
			// Request one extra for hasNextPage probe.
			opts = append(opts, runtime.Limit(n+1))
		}
	} else if last, ok := args["last"]; ok {
		if n, ok := gqlToInt(last); ok {
			opts = append(opts, runtime.Limit(n+1))
		}
	}

	return opts, false
}

// gqlToInt converts a GraphQL argument value to int.
// AST-parsed IntValue yields int64, while gqlgen resolvers may pass int.
// JSON-parsed numbers may arrive as float64.
func gqlToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		if n > int64(maxInt) || n < int64(minInt) {
			return 0, false
		}
		return int(n), true
	case int32:
		return int(n), true
	case float64:
		// JSON-parsed integers arrive as float64; accept only whole numbers in int range.
		if n != float64(int64(n)) {
			return 0, false
		}
		i := int64(n)
		if i > int64(maxInt) || i < int64(minInt) {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

// maxInt and minInt are the platform-specific int bounds.
const (
	maxInt = int(^uint(0) >> 1)
	minInt = -maxInt - 1
)

// genEntityCollection generates a per-entity collection file for the entity sub-package.
// Contains the CollectMeta variable with FieldColumns and Edges, populated in init().
// The CollectFields method on *{Entity}Query is generated separately in query/ via genCollectionQueries.
func (g *Generator) genEntityCollection(t *gen.Type) *jen.File {
	if g.config.ORMPackage == "" {
		return nil
	}
	f := jen.NewFilePath(g.entityPkgPath(t))
	f.HeaderComment("Code generated by velox. DO NOT EDIT.")

	f.ImportName(runtimePkgPath, "runtime")

	metaVar := t.Name + "CollectMeta"

	// Package-level CollectMeta variable (exported for query/ package access).
	f.Commentf("%s holds GraphQL field collection metadata for %s.", metaVar, t.Name)
	f.Var().Id(metaVar).Qual(runtimePkgPath, "CollectMeta")

	// Generate init() to populate CollectMeta.
	g.genEntityCollectionInit(f, t, metaVar)

	return f
}

// genCollectionQueries generates a single gql_collection.go in the query/ package
// containing CollectFields methods for all entity query types.
func (g *Generator) genCollectionQueries(nodes []*gen.Type) *jen.File {
	if g.config.ORMPackage == "" || len(nodes) == 0 {
		return nil
	}
	queryPkg := g.config.ORMPackage + "/query"
	f := jen.NewFilePathName(queryPkg, "query")
	f.HeaderComment("Code generated by velox. DO NOT EDIT.")

	f.ImportName("context", "context")
	f.ImportName(runtimePkgPath, "runtime")

	for _, t := range nodes {
		entityPkg := g.entityPkgPath(t)
		f.ImportName(entityPkg, g.entityPkgName(t))

		queryType := t.QueryName()
		metaVar := t.Name + "CollectMeta"

		// CollectFields method on *{Entity}Query — local type in query/ package.
		f.Commentf("CollectFields inspects GraphQL field selections and configures")
		f.Comment("the query for optimal eager loading using entity metadata.")
		f.Func().Params(
			jen.Id("q").Op("*").Id(queryType),
		).Id("CollectFields").Params(
			jen.Id("ctx").Qual("context", "Context"),
			jen.Id("satisfies").Op("...").String(),
		).Params(
			jen.Op("*").Id(queryType),
			jen.Error(),
		).Block(
			jen.Return(
				jen.Id("q"),
				jen.Qual(runtimePkgPath, "CollectFields").Call(
					jen.Id("ctx"),
					jen.Id("q"),
					jen.Qual(entityPkg, metaVar).Dot("FieldColumns"),
					jen.Qual(entityPkg, metaVar).Dot("Edges"),
					jen.Id("satisfies").Op("..."),
				),
			),
		)
	}

	return f
}

// genEntityCollectionInit generates an init() function that populates
// CollectMeta.FieldColumns and CollectMeta.Edges for GraphQL field collection.
// The generated code lives in the entity sub-package, so field constants use
// local (unqualified) references.
func (g *Generator) genEntityCollectionInit(f *jen.File, t *gen.Type, metaVar string) {
	// Collect scalar fields (GraphQL name → DB column).
	filteredFields := g.filterFields(t.Fields, SkipType)

	// Collect edges.
	filteredEdges := g.filterEdges(t.Edges, SkipType)

	f.Func().Id("init").Params().BlockFunc(func(grp *jen.Group) {
		// FieldColumns: map GraphQL field name → DB column name.
		grp.Id(metaVar).Dot("FieldColumns").Op("=").Map(jen.String()).String().ValuesFunc(func(d *jen.Group) {
			for _, fld := range filteredFields {
				gqlName := g.graphqlFieldName(fld)
				// Field constants are local to the entity sub-package.
				d.Lit(gqlName).Op(":").Id("Field" + pascal(fld.Name))
			}
		})

		// Edges: map GraphQL edge name → EdgeMeta.
		if len(filteredEdges) > 0 {
			grp.Id(metaVar).Dot("Edges").Op("=").Map(jen.String()).Qual(runtimePkgPath, "EdgeMeta").ValuesFunc(func(d *jen.Group) {
				for _, e := range filteredEdges {
					edgeName := camel(e.Name)
					relay := g.config.RelayConnection && g.hasRelayConnection(e.Type)

					// FK columns needed for this edge's eager loading.
					fkCols := make([]jen.Code, 0, len(e.Rel.Columns))
					for _, col := range e.Rel.Columns {
						fkCols = append(fkCols, jen.Lit(col))
					}

					d.Lit(edgeName).Op(":").Qual(runtimePkgPath, "EdgeMeta").Values(jen.Dict{
						jen.Id("Name"):      jen.Lit(e.Name),
						jen.Id("Target"):    jen.Lit(e.Type.Table()),
						jen.Id("Unique"):    jen.Lit(e.Unique),
						jen.Id("Relay"):     jen.Lit(relay),
						jen.Id("FKColumns"): jen.Index().String().Values(fkCols...),
						jen.Id("Inverse"):   jen.Lit(e.Inverse),
					})
				}
			})
		}
	})
}
