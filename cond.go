package velox

import (
	"github.com/syssam/velox/internal/engine"
)

// Cond is a condition that names its columns by name, as the schema declares
// them: what a Policy, an Interceptor and a hook's Where use, since the
// schema package cannot name the generated entities. It is an instance of
// Pred, so And, Or and Not combine conditions of both forms, though not one
// of each: velox.Or(velox.C("a").EQ(1), gen.Users.ID.EQ(2)) does not
// compile.
//
// A Cond is resolved against the entity of the statement it is used in, when
// the statement runs: a name the entity has no column of, a value of another
// kind, a text operator on a column that is not text, or IsNull on a NOT NULL
// column fails the statement before anything is sent. Its zero value fails
// the statement too, so that a policy that forgot to return a condition lets
// nothing through. In a schema's behaviour, velox-gen refuses a constant
// name the entity lacks, and a constant value that does not fit the column,
// before any of it runs.
type Cond = engine.Cond

// Named is a column by its name, made by C. Its operators, EQ, NEQ, GT, GTE,
// LT, LTE, In, NotIn, Contains, HasPrefix, HasSuffix, IsNull and NotNull,
// return a Cond.
// A value is converted as Table.Convert converts it: the column's own type
// as it is, another type of the same kind (an int for an int64 column, a
// string for a column of a named string type) converted and range-checked.
// A value of another kind fails the statement. EQ(nil) fails: IsNull says
// that. On a nullable column a value compares the value.
type Named = engine.Named

// NamedEdge is a relation by its name, made by Edge. Has and HasWhere
// return a Cond.
type NamedEdge = engine.NamedEdge

// C returns the column of the given name, as the schema's field names it:
//
//	velox.C("org_id").EQ(org)
func C(name string) Named { return engine.C(name) }

// Edge returns the relation of the given name, as the schema's edge names it:
//
//	velox.Edge("doc").Has()
//
// Has and HasWhere are an EXISTS on the relation's target, which applies the
// target's Read policy and interceptors for the statement's context. The
// conditions given HasWhere name the target's columns. velox-gen links the
// target of a relation an entity's behaviour names with a constant name; a
// statement that names one it did not link fails.
func Edge(name string) NamedEdge { return engine.Edge(name) }

// Allow is the condition that matches every row: what a policy's Read or
// Write, or an interceptor's Filter, returns for a caller it does not
// restrict. In a policy it adds nothing to the SQL; in a Where it is 1 = 1,
// which an Update or Delete's guard does not count as a condition. A policy
// that returns the zero Cond instead fails the statement, so that no
// restriction is said, never implied.
//
// Allow composes as its meaning says: And drops it (And(Allow(), x) is x),
// Or(Allow(), x) matches every row (see Or), and Not(Allow()) matches no
// row. Typed code that wants a condition that matches every row writes
// And[E]().
func Allow() Cond { return Cond(engine.NewPredicate(engine.Node{Op: engine.OpAllow})) }

// Predicates returns cs for the untyped surface: a TableQuery's,
// TableUpdate's or TableDelete's Where. A condition by name resolves
// against the statement's entity when the statement runs.
func Predicates(cs []Cond) []Predicate {
	out := make([]Predicate, len(cs))
	for i, c := range cs {
		out[i] = Predicate(c)
	}
	return out
}
