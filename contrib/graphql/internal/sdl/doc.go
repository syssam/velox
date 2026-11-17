// Package sdl renders the GraphQL schema, velox.graphql, of a velox model.
//
// Build makes the GraphQL surface of a model once, and Render writes the SDL
// from it; gogen writes the Go from the same Surface, so they cannot drift.
// The files follow the steps:
//
//   - graph.go: the graph of objects, built from the model, and type names;
//   - fields.go: an object's fields and edges, their names and types;
//   - directive.go: graphql.Directives, checked and rendered;
//   - rules.go: which annotations apply to which element, and their checks;
//   - names.go: GraphQL names, and the type names each element reserves;
//   - enums.go: Enums, the enum types velox-graphql:only=enums binds;
//   - order.go: the XOrder input and XOrderField enum;
//   - where.go: the XWhereInput's predicates;
//   - input.go: the CreateX and UpdateX inputs;
//   - surface.go: Build, and the Surface it gives gogen;
//   - render.go: Render, and the writer of the SDL;
//   - check.go: Check, the SDL validated with gqlparser.
package sdl
