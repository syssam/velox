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
// The default surface is entgql's. Each option below shapes one part of
// it, and stands alone; a value the option does not know fails the run.
//
//   - names=schema names a field from its schema name (image_url is
//     imageUrl, customer_id is customerId); names=go, the default, from its
//     Go name (imageURL).
//   - filters=lenient reads a where input so that an empty In, a false
//     IsNil and an empty or add nothing, a not of nothing is an error, no
//     width cap applies and the node budget is 10 times MaxFilterNodes
//     (gqlgraph.Type's LenientFilters); filters=strict, the default, fails
//     closed: an empty idIn matches no node (docs/decisions/0150).
//   - node=on declares Relay's Node, node(id:) and nodes(ids:), for an
//     application whose IDs are unique across types, as snowflakes and
//     UUIDs are; node=off, the default, leaves them out
//     (docs/decisions/0152). The application's resolvers answer with the
//     generated ResolveNode and ResolveNodes (veloxgen/noder_gql.go), and
//     WithNodeType and WithFixedNodeType give ResolveNode an ID's type, so
//     it reads that type alone and runs no lookup.
//   - idops=equality gives equality alone (EQ, NEQ, In, NotIn, and IsNil
//     and NotNil when nullable) to the key, to any field named like a key
//     (id, *_id, *ID) and to an Other field; idops=kind, the default, gives
//     equality alone to a foreign key and its kind's operators to every
//     other field.
//   - orderfields=all lists every orderable field in the order enum;
//     orderfields=annotated, the default, those with graphql.OrderField.
//   - queryfields=all gives every type a root Query field;
//     queryfields=annotated, the default, those with graphql.QueryField.
//   - optional=nullable makes an Optional field nullable in its type;
//     optional=nonnull, the default, keeps it non-null, as its column is.
//   - requirededges=nonnull makes a Required unique edge non-null;
//     requirededges=nullable, the default, keeps it nullable, as a policy
//     may hide its node.
//   - inputkeys=column names an implicit foreign key's input field by its
//     column (userBestFriend); inputkeys=edge, the default, by its edge
//     (bestFriendID).
//   - jsontypes=named names a json field's type by its Go type's name
//     (Settings, [Assignee!]); jsontypes=json, the default, makes it JSON.
//
// A connection's edges and node are nullable, and an order of one term
// breaks ties by the key in its direction, as entgql's do
// (docs/decisions/0108). A type with a QueryField and no RelayConnection
// has a list as its root field, and no connection, as in entgql.
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:node=on ./veloxschema
//
// An application whose filters were opt-in field by field, as the old
// velox's codegen hook made them, asks for where=whitelist: a field other
// than the key then has predicates only with graphql.WhereInput (the old
// velox's EnableWhereInputs(true)), whatever its WhereOps. An application
// that moved from the old velox and keeps its GraphQL API gives these
// options together (docs/guide.md lists them):
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:names=schema,filters=lenient,node=on,sdlfiles=entity,orderfields=all,queryfields=all,optional=nullable,requirededges=nonnull,jsontypes=named,idops=equality,inputkeys=column,apptypes=JSON+UUID+Uint64,where=whitelist ./veloxschema
//
// An application served by github.com/syssam/graphql-go, whose generator
// gqlc binds the schema, asks for engine=graphql-go; engine=gqlgen is the
// default:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:where=whitelist,engine=graphql-go ./veloxschema
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
// entities'. Each file is named by the entity's name in lower case
// (velox_salesorder.graphql). sdlfiles=one is the default.
// entities=A+B writes the SDL of the schemas A and B alone: a field whose
// type is a schema left out is left out, and each where input a written
// one nests is written, under sdlfiles=entity in velox.graphql; the Go is
// the whole schema's. apptypes=A+B names the types the application
// declares. A scalar of velox-graphql's vocabulary it lists, JSON, UUID,
// Uint64, Bytes or Time, is left out of velox.graphql. Under entities= a
// field naming another type velox-graphql does not declare is left out,
// and an input naming one is refused:
//
//	go tool velox-gen -out veloxgen -plugin velox-graphql:engine=graphql-go,sdlfiles=entity,entities=SalesOrder+SalesOrderItem,apptypes=Decimal+Map ./veloxschema
//
// only=enums takes no other option but engine, and writes the same Go for
// either engine. Any other option fails the run, and compat=, which these
// options replace, is refused with them.
package main

import (
	"github.com/syssam/velox/contrib/graphql/internal/gqlplugin"
	"github.com/syssam/velox/plugin"
)

func main() {
	plugin.Main(gqlplugin.Supports, gqlplugin.Generate)
}
