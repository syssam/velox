// Package gogen writes the Go code velox-graphql generates: the Go types
// gqlgen's autobind finds in the generated package, named after the SDL types
// they bind to, and the methods and functions their fields resolve through.
// It writes <label>_gql.go for each entity with a GraphQL type (its types,
// its edge methods, its PaginateQuery and Paginate options, and its mutation inputs
// with their method Setters, which input.go writes).
//
// Every name comes from sdl.Build, which the SDL is rendered from too, so the
// Go and the SDL cannot drift.
//
// The files follow what they write:
//
//   - render.go: Render and RenderEnums, the renderer, and an entity's file;
//   - file.go: one Go file being written, its imports and its layout;
//   - names.go: the Go names of receivers and locals;
//   - enum.go: an enum's GraphQL marshalers;
//   - conn.go: a connection, its edge type and PaginateQuery;
//   - edge.go: the edge methods (post.Author(ctx)), batched per page;
//   - order.go: the XOrder input and XOrderField enum;
//   - where.go: the XWhereInput and its Pred;
//   - input.go: the CreateX and UpdateX inputs and their Setters;
//   - inputtype.go: the Go types of input fields and their conversions.
package gogen
