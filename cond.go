package velox

import (
	"github.com/syssam/velox/internal/engine"
)

// anyEntity is the entity of a Predicate: none.
type anyEntity = engine.AnyEntity

// Predicate is a condition that names no entity's type: what a Policy's
// Read and Write and an Interceptor's Filter return, what a hook's
// Mutation.Where takes, and what the untyped surface (TableQuery,
// TableUpdate, TableDelete) filters by. It is Pred of no entity, so And, Or
// and Not combine predicates as they combine a Pred of one entity, though
// not one of each: velox.Or(velox.FieldEQ("a", 1), veloxgen.Users.ID.EQ(2))
// does not compile. Predicate(p) converts a typed p.
//
// FieldEQ and the other Field functions name a column by its name, as the
// schema's field names it, and HasEdge and HasEdgeWith a relation by its
// edge's name, as ent's sql.FieldEQ and generated HasX and HasXWith do,
// since the schema package cannot name the generated entities:
//
//	velox.FieldEQ("org_id", org)
//	velox.Or(velox.FieldEQ("author_id", u), velox.FieldIsNull("deleted_at"))
//	velox.HasEdgeWith("doc", velox.FieldEQ("public", true))
//
// A condition by name is resolved against the entity of the statement it is
// used in, when the statement runs: a name the entity has no column of, a
// value of another kind, a text operator on a column that is not text, or
// FieldIsNull on a NOT NULL column fails the statement before anything is
// sent. A value is converted as Table.Convert converts it: the column's own
// type as it is, another type of the same kind (an int for an int64 column,
// a string for a column of a named string type) converted and
// range-checked. In a schema's behaviour, velox-gen refuses a constant name
// the entity lacks, and a constant value that does not fit the column,
// before any of it runs.
//
// The zero Predicate fails the statement that holds it, so that a policy
// that forgot to return a condition lets nothing through: a policy that
// denies returns velox.Predicate{} with its error. Allow is the condition
// that matches every row.
type Predicate = Pred[anyEntity]

// FieldEQ is the column of the given name = v. FieldEQ(name, nil) fails
// the statement: FieldIsNull says that. On a nullable column, a value
// compares the value.
func FieldEQ(name string, v any) Predicate { return engine.FieldEQ(name, v) }

// FieldNEQ is the column of the given name <> v.
func FieldNEQ(name string, v any) Predicate { return engine.FieldNEQ(name, v) }

// FieldGT is the column of the given name > v.
func FieldGT(name string, v any) Predicate { return engine.FieldGT(name, v) }

// FieldGTE is the column of the given name >= v.
func FieldGTE(name string, v any) Predicate { return engine.FieldGTE(name, v) }

// FieldLT is the column of the given name < v.
func FieldLT(name string, v any) Predicate { return engine.FieldLT(name, v) }

// FieldLTE is the column of the given name <= v.
func FieldLTE(name string, v any) Predicate { return engine.FieldLTE(name, v) }

// FieldIn is the column of the given name IN (vs...). With no values it is
// false.
func FieldIn(name string, vs ...any) Predicate { return engine.FieldIn(name, vs...) }

// FieldNotIn is the column of the given name NOT IN (vs...). With no
// values it is true.
func FieldNotIn(name string, vs ...any) Predicate { return engine.FieldNotIn(name, vs...) }

// FieldContains is true when the text column of the given name contains s,
// taken literally (LIKE %s%, with s's LIKE characters escaped).
func FieldContains(name, s string) Predicate { return engine.FieldContains(name, s) }

// FieldHasPrefix is true when the text column of the given name begins
// with s, taken literally.
func FieldHasPrefix(name, s string) Predicate { return engine.FieldHasPrefix(name, s) }

// FieldHasSuffix is true when the text column of the given name ends with
// s, taken literally.
func FieldHasSuffix(name, s string) Predicate { return engine.FieldHasSuffix(name, s) }

// FieldEqualFold is LOWER(column) = LOWER(s), on the text column of the
// given name. SQLite's LOWER folds ASCII letters only.
func FieldEqualFold(name, s string) Predicate { return engine.FieldEqualFold(name, s) }

// FieldContainsFold is true when the text column of the given name contains
// s, taken literally, whatever the case of either: LOWER(column) LIKE
// LOWER(pattern). SQLite's LOWER folds ASCII letters only.
func FieldContainsFold(name, s string) Predicate { return engine.FieldContainsFold(name, s) }

// FieldInSelect is true when this row's column is among the key values of the
// rows of entity that meet every condition of ps, which name that entity's
// columns. Read the arguments in that order: this entity's column, the other
// entity, its key column, its conditions:
//
//	// a post is visible when its conversation_id is the conversation_id of a
//	// Participant row whose user_id is uid
//	velox.FieldInSelect("conversation_id", "Participant", "conversation_id",
//		velox.FieldEQ("user_id", uid))
//
// It is SQL's column IN (SELECT key FROM entity WHERE ps), written as an EXISTS
// that the database plans like a join on key, which is fast when key is indexed
// (usually the primary key).
//
// It is the condition a column handle's InSelect makes, for a policy, a hook or
// an interceptor in a package that cannot name the entity's type. The entity's
// Read policy and interceptors apply inside the subquery, so a row the caller
// may not read does not widen the condition. When the subquery reaches back
// to an entity whose condition is being made, as a rule on participants that
// reads participants does, that inner subquery gets none of that entity's
// conditions. A NULL column is in no subquery.
// Negate it with Not: NOT EXISTS, which is true for a NULL column.
//
// entity is the schema's name for it or its table. velox finds it in the
// generated package's EntityTable when the statement runs, so the names may
// come from a helper package or configuration; velox-gen checks the names it
// can read as constants. A name that is no entity or table, or that is one
// entity's name and another's table, a column that is not there, or two
// columns of different types fail the statement that holds it. An integer
// and a text column are the exception, for a column or a key that holds
// ids as text, and they match as the integer's text would. An integer
// column against a text key is cast to text (CONCAT on MySQL, so the text
// takes the key's collation), and the key stays bare. A text column
// against an integer key is the integer it spells, so the key stays bare
// and its index serves: a text that is not an int64 as Go's strconv
// writes it ("07", " 7", "+7", "7.0", "", one past int64) matches nothing
// and fails nothing, as a guard keeps it from the cast (a regular
// expression on PostgreSQL and MySQL, a round trip on SQLite). An
// unsigned 64-bit key is cast to text instead. The column
// must not be an encoded (JSON) column. On MySQL a subquery that reads the table
// an UPDATE or DELETE changes fails with error 1093.
func FieldInSelect(column, entity, key string, ps ...Predicate) Predicate {
	return engine.FieldInSelect(column, entity, key, ps...)
}

// FieldInSelectRaw is FieldInSelect with no policy inside the subquery:
// only ps apply there. The entity's Read policy, the DB's WithFilter
// filters and its interceptors, soft delete among them, do not, so a row
// the caller may not read, or one soft-deleted, still matches:
//
//	// an activity row's entity_id, text, names a lead by its integer id;
//	// a deleted lead keeps its history
//	velox.FieldInSelectRaw("activity trail: a deleted lead keeps its history",
//		"entity_id", "Lead", "lead_id", velox.FieldIn("org_id", orgs...))
//
// The names resolve as FieldInSelect's, and the columns compare as its do:
// the text entity_id is cast to lead_id's type when it spells an int64,
// so lead_id's index serves each row. A relation or FieldInSelect among ps keeps its own target's policies.
// The statement around it keeps its own Read or Write policy, filters and
// interceptors, on its own rows. It works in Or, And and Not, and in an
// update's or a delete's Where.
//
// reason says why the subquery reads past the policies; empty or white
// space fails the statement. Statement.RawReasons returns it, as it
// returns ExprRaw's, so an Around hook can record it. The render cache
// keys neither the reason nor the values.
func FieldInSelectRaw(reason, column, entity, key string, ps ...Predicate) Predicate {
	return engine.FieldInSelectRaw(reason, column, entity, key, ps...)
}

// FieldIsNull is true when the nullable column of the given name is NULL.
func FieldIsNull(name string) Predicate { return engine.FieldIsNull(name) }

// FieldNotNull is true when the nullable column of the given name is not
// NULL.
func FieldNotNull(name string) Predicate { return engine.FieldNotNull(name) }

// HasEdge is true when the relation of the given name, as the schema's
// edge names it, holds at least one row:
//
//	velox.HasEdge("doc")
//
// It is an EXISTS on the relation's target, which applies the target's
// Read policy and interceptors for the statement's context. velox finds
// the target in the generated package's EntityTable when the statement
// runs, so the name may come from a helper package or configuration.
// velox-gen checks a constant name; a name that is not a relation fails
// the statement.
func HasEdge(name string) Predicate { return engine.HasEdge(name) }

// HasEdgeWith is true when the relation of the given name holds at least
// one row that meets every condition of ps, which name the target's
// columns, as HasEdge applies the target's policy.
func HasEdgeWith(name string, ps ...Predicate) Predicate { return engine.HasEdgeWith(name, ps...) }

// Allow is the condition that matches every row: what a policy's Read or
// Write, an interceptor's Filter or a WithFilter filter returns for a
// caller or an entity it does not restrict. In a policy it adds nothing to the SQL; in a Where it is 1 = 1,
// which an Update or Delete's guard does not count as a condition. A policy
// that returns the zero Predicate instead fails the statement, so that no
// restriction is said, never implied.
//
// Allow composes as its meaning says: And drops it (And(Allow(), x) is x),
// Or(Allow(), x) matches every row (see Or), and Not(Allow()) matches no
// row. Typed code that wants a condition that matches every row writes
// And[E]().
func Allow() Predicate { return engine.NewPredicate(engine.Node{Op: engine.OpAllow}) }

// None is the condition that matches no row: what a policy's Read or Write,
// an interceptor's Filter or a WithFilter filter returns to hide every row
// of an entity, as ent's sql.False() does. It renders as 1 = 0 on every
// dialect, as an Or of nothing does; Not(Allow()) means the same and
// renders as NOT (1 = 1). A read under it returns no row, and an update or
// delete changes none and reports 0, as for any row a filter or a policy
// hides. Typed code that wants a condition that matches no row writes
// Or[E]().
func None() Predicate { return engine.NewPredicate(engine.Group(engine.OpOr, nil)) }
