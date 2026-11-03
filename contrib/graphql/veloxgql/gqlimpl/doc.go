// Package gqlimpl is for generated code. It holds what the Go that
// velox-graphql writes calls: the where filter and its helpers, the conversions
// between a where input's Go types and a column's, the connection with its
// paging (Conn, Col, Order, ConnectionBase and NewConnection: one runtime for
// every type, on velox's untyped TableQuery), the Node lookups (NewNode), the edges, which read in batches (the edge of every row of a page in
// one statement: EdgeConnection, EdgeOne and EdgeList), the fields a selection
// reads (Selected and Narrow), the runtime of the inputs' Create and Update
// methods (NewCreate and NewUpdate, whose Insert and Exec run one in Atomic, a
// savepoint inside the operation's transaction or a transaction of its own;
// the steps the generated methods give them: Key, Keys, AddRemove,
// DecodeJSON, NotZero, NotNull, Cleared, ClearEdge, Exists, Join, Repoint,
// Children, Child and Through, the last four with an edge's M2M or Owned,
// which NewM2M and NewOwned make; and what those run on: CheckExists, Repointed,
// StillLinked, Readable, UnlinkAll, NotFound, LockRow and WriteError), and
// InputErrorf, the coded error of a client's input. An
// application does not call this package.
//
// Compatibility is by veloxgql.GeneratedCodeIsVersion10, which every
// generated file references, and not by semantic versioning, as protobuf's
// protoimpl is: a name here may change or go when the generated code
// changes, and then the version constant is replaced, so that generated code
// and this module fail to compile together when they disagree.
//
// What an application sees stays in package veloxgql: the IDs, the cursor
// scalar, the limits, the errors and the extension. See its documentation for
// how a where input, a connection and a cursor behave.
package gqlimpl
