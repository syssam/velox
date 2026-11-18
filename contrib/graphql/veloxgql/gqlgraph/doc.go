// Package gqlgraph is for generated code. As ent's sqlgraph is for ent's, it
// holds what the Go that velox-graphql writes calls, all of it one runtime
// for every type, on velox's untyped Table, TableQuery and table writes:
//   - the where filter and its helpers (Filter);
//   - the description of a GraphQL type (Type, Column), the connection with its
//     paging (Paginate, Args, Order, WhereOf);
//   - the edges, which read in batches, the edge of every row of a page in
//     one statement (Edge, EdgeOne, EdgeList and Paginate of an Edge);
//   - the fields a selection reads, which Paginate and the edges narrow their
//     queries to;
//   - what the inputs' Setters methods call: IDs for the keys a list field
//     gives, FromJSON for a JSON field, NotZero, and Validate,
//     which runs the validators of the columns an input sets;
//   - InputErrorf, the coded error of a client's input.
//
// An application does not call this package.
//
// Compatibility is by veloxgql.GeneratedCodeIsVersion23, which every
// generated file references, and not by semantic versioning, as protobuf's
// protoimpl is: a name here may change or go when the generated code
// changes, and then the version constant is replaced, so that generated code
// and this module fail to compile together when they disagree.
//
// What an application sees stays in package veloxgql: the ID and cursor
// scalars, the limits, the errors and the extension. See its documentation for
// how a where input, a connection and a cursor behave.
package gqlgraph
