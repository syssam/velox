// Command velox-graphql is the velox-gen plugin that writes the GraphQL
// schema of a velox schema package, veloxgen/velox.graphql, and the Go code in the
// generated package that gqlgen's autobind binds the schema's types to,
// veloxgen/*_gql.go. An application adds it as a tool of its module and names it
// to velox-gen:
//
//	go get -tool github.com/syssam/velox/contrib/graphql/cmd/velox-graphql
//	go tool velox-gen -out veloxgen -plugin velox-graphql ./veloxschema
//
// velox-gen runs it with the request, the resolved model as JSON, on its
// standard input, and it answers with the files on its standard output (see
// package github.com/syssam/velox/plugin). velox-gen writes them, with the
// generated-file header, and owns them from then on.
//
// A program that generates with package github.com/syssam/velox/generate
// runs it in the process, with no tool to build, as
// graphqlgen.Plugin(opts) from github.com/syssam/velox/contrib/graphql/graphqlgen.
//
// It takes these options, separated by commas. An application that writes
// its own SDL and binds it with gqlgen's autobind asks for only the
// marshalers of velox-gen's enum types, with no velox.graphql and no other
// Go:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:only=enums ./veloxschema
//
// A value is then written to GraphQL as the stored one upper-cased, or as
// graphql.EnumValues names it, and read back from it: open is OPEN.
//
// An application whose clients are built against the API veloxe's generator
// made asks for that API:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:compat=veloxe ./veloxschema
//
// A field or edge is then named from its schema name as veloxe named it
// (customer_id is customerId, image_url is imageUrl); a foreign key an edge
// is bound to is an ID in the type, beside its edge; and its where
// predicates are named from it (customerId, customerIdNEQ, customerIdIn,
// customerIdNotIn); and the rest of veloxe's surface follows, as
// docs/decisions/0087 lists it.
//
// Every type with an ID implements the interface Node, and the root query
// has node(id:) and nodes(ids:), which the application's resolvers answer
// with the generated ResolveNode and ResolveNodes (veloxgen/noder_gql.go). A
// where input reads as veloxe's did: an empty In, a false IsNil and an empty
// or add nothing, a not of nothing is an error, no width cap applies, and
// the node budget is 10 times MaxFilterNodes (gqlgraph.Type's Veloxe). A foreign key's predicates are the
// column's alone. A connection takes the cursors veloxe wrote, as well as
// its own, and reads their values as the current order's. An order of one
// term breaks ties by the key in its direction. A page after a cursor has
// a previous page, and a page before one a next page (docs/decisions/0108).
//
// An application whose filters were opt-in field by field, as a veloxe
// codegen hook made them, asks for where=whitelist: a field other than the
// key then has predicates only with graphql.WhereInput (the old velox's
// EnableWhereInputs(true)), whatever its WhereOps:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:compat=veloxe,where=whitelist ./veloxschema
//
// An application served by github.com/syssam/graphql-go, whose generator
// gqlc binds the schema, asks for engine=graphql-go; engine=gqlgen is the
// default:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:compat=veloxe,where=whitelist,engine=graphql-go ./veloxschema
//
// Every type of velox.graphql but Query then carries @goModel naming its Go
// type, an input's field whose name graphql-go would not derive has a graphql
// tag, and a JSON field, or one of the application's own Go type, is on the
// Resolver. A field or edge graphql.FieldName renames, a non-null bytes
// field and a key not named ID are refused, as gqlc does not read
// @goField(name:) yet. Package
// github.com/syssam/velox/contrib/graphql, "graphql-go and gqlc", has the
// gqlc.yaml it needs.
//
// sdlfiles=entity writes the SDL as velox.graphql, with the root Query, and
// one velox_<label>.graphql per entity, so that gqlc, which makes a Go
// package of each file, names Query's argument structs apart from the
// entities'. sdlfiles=one is the default, and entity is under compat=veloxe,
// whose files are named as veloxe's were (velox_salesorder.graphql).
// entities=A+B writes the SDL of the schemas A and B alone: a field whose
// type is a schema left out is left out, and each where input a written
// one nests is written, under sdlfiles=entity in velox.graphql; the Go is
// the whole schema's. apptypes=A+B names the application's types their SDL
// may name: a field naming another type velox-graphql does not declare is
// left out, and an input naming one is refused:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:engine=graphql-go,sdlfiles=entity,entities=SalesOrder+SalesOrderItem,apptypes=Decimal+Map ./veloxschema
//
// only=enums takes neither compat, where, sdlfiles, entities nor apptypes,
// and writes the same Go for either engine. Any other option fails the run.
package main

import (
	"github.com/syssam/velox/contrib/graphql/internal/gqlplugin"
	"github.com/syssam/velox/plugin"
)

func main() {
	plugin.Main(gqlplugin.Supports, gqlplugin.Generate)
}
