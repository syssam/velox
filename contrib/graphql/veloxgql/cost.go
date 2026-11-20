package veloxgql

import (
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/cost"
)

// Cost is what op costs under l, with vars its variables. Two parts add up:
//
// The rows part is what the response holds:
//   - each field costs 1, plus its children's cost times its multiplier;
//   - the edges of a connection (a field whose type has edges and pageInfo)
//     multiply by its first or last, or by l.MaxPage when neither is given or
//     the field has no such arguments, the safer count; its totalCount and
//     pageInfo are read once;
//   - any other field of a list of objects multiplies by l.MaxList, or by the
//     edge's graphql.MaxList when that is lower;
//   - every branch of a fragment counts, but a selection that @skip(if: true)
//     or @include(if: false) leaves out costs nothing, its condition a
//     literal or a variable's value (a missing variable's default, when it
//     has one); one whose condition is not a boolean (a variable that is
//     missing with no default, or null) counts, as the cost is an upper
//     bound. The value above it is read all the same: an object whose every
//     selection is left out costs what one that selects __typename does;
//   - __schema and __type cost what they select, counted over the schema
//     itself: one for each field of the response. A walk that passes
//     l.MaxIntrospectionCost stops, saturated, so it does no more work than the
//     limit.
//
// The statements part is what the request sends, and takes no multiplier, as
// the rows of a batch share one statement however many parents they have:
//   - a field that runs a statement adds 100: a root field, a connection, its
//     totalCount (a COUNT of its own) and any other field of an object or a list
//     of objects. first: 0 still counts: a COUNT or a filter still runs;
//   - an input object argument (a where, an orderBy, a mutation's input) adds
//     its size at each use: one for each value in it, nested inputs and lists
//     included, read from the variables; a string one more for each 128 of its
//     bytes, and an object one and one for each 16 of its type's fields, as gqlgen
//     does work for each. A variable used twice counts twice;
//   - in a <T>WhereInput, a predicate no index serves adds 2000, 20 statements,
//     at each use: each <field>Contains, <field>HasPrefix, <field>HasSuffix,
//     <field>EqualFold and <field>ContainsFold given, as a LIKE or a LOWER
//     comparison reads the whole table, and each <field>Has, <field>HasSome,
//     <field>HasEvery and <field>IsEmpty of a JSON array, whose containment
//     test only an index of the database's own kind could serve, and each
//     non-empty has<Edge>With,
//     whose subquery runs for each row it reads. The default MaxCost allows about
//     70 of them;
//   - on a mutation, an input object adds two statements, for the row it reads
//     back and for the read of the row's key that an update's
//     UpdateQuery.One sends first unless its conditions pin the key, or the
//     read that tells a skipped write from a missing row; two more for a
//     non-empty <edge>IDs, add<Edge>IDs or remove<Edge>IDs, five for an
//     <edge>ID, a one-to-one edge's re-point, and three for a clear<Edge>:
//     true, a page of its links, the targets and the delete, a page being
//     velox.EdgePage links. A list of input objects, as a bulk mutation of the
//     application's takes, counts so for each element. A delete<T>, which the
//     application writes, of a type with connections or lists adds a statement
//     for a check of its row and one for each, the links it deletes. Any
//     mutation of the application's that names its fields so counts the same.
//
// Sums and products saturate at 1<<62 (math.MaxInt where int is smaller),
// checked before they are computed, so huge arguments and many aliases cannot
// wrap the cost to a small or negative number. A named
// fragment is costed once for each page size it is spread under, so the walk is
// linear in the document. schema is the server's; op is a parsed and validated
// operation. Cost is the sum of what Extension holds to two limits, before any
// statement runs: the introspection part to Limits.MaxIntrospectionCost, and the
// rest to Limits.MaxCost.
func Cost(schema *ast.Schema, op *ast.OperationDefinition, vars map[string]any, l Limits) int {
	return cost.Total(schema, op, vars, l.cost())
}

// cost is what the cost model reads of l.
func (l Limits) cost() cost.Limits {
	return cost.Limits{MaxPage: l.MaxPage, MaxList: l.MaxList, MaxIntrospectionCost: l.MaxIntrospectionCost}
}
