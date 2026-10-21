// Package gqlimpl is for generated code. It holds what the Go that
// velox-graphql writes calls: the where filter and its helpers, the
// conversions between a where input's Go types and a column's, the
// connection with its paging, the runtime of the mutations (Atomic, which
// runs one in the operation's transaction or in one of its own, the
// decoding of input IDs, Exist, Matched, Repointed, Disjoint and
// NotFound, WriteError and FromJSON), and InputErrorf, the coded error of a
// client's input. An application does not call it.
//
// Compatibility is by veloxgql.GeneratedCodeIsVersion1, which every
// generated file references, and not by semantic versioning, as protobuf's
// protoimpl is: a name here may change or go when the generated code
// changes, and then the version constant is replaced, so that generated code
// and this module fail to compile together when they disagree.
//
// What an application sees stays in package veloxgql: the IDs, the cursor
// scalar, the limits, the errors and the extension. See its documentation for
// how a where input, a connection and a cursor behave.
package gqlimpl
