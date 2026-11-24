package velox

import (
	"slices"

	"github.com/syssam/velox/internal/engine"
)

// JSONText is the string at a path of object keys in a JSON column of entity
// E, made by a JSON handle's Text. It is NULL where the path is missing or
// holds a value that is not a string, so a number 5 is not the string "5".
// Its conditions read alike on PostgreSQL (jsonb), MySQL and SQLite, and
// compare strings as written, with case, on all three.
type JSONText[E any] struct {
	col  int32
	path engine.JSONPath
}

// JSONNumber is the number at a path of object keys in a JSON column of
// entity E, made by a JSON handle's Number, as a float64. It is NULL where
// the path is missing or holds a value that is not a number. A JSON handle's
// Len is one too: the length of an array. Its conditions read alike on
// PostgreSQL (jsonb), MySQL and SQLite.
type JSONNumber[E any] struct {
	col  int32
	path engine.JSONPath
}

// JSONBool is the boolean at a path of object keys in a JSON column of
// entity E, made by a JSON handle's Bool. It is NULL where the path is
// missing or holds a value that is not true or false, so neither the string
// "true" nor the number 1 is true. Its conditions read alike on PostgreSQL
// (jsonb), MySQL and SQLite.
type JSONBool[E any] struct {
	col  int32
	path engine.JSONPath
}

// Term returns the value as a Term: a result column of Select, which
// Scan reads into a string field, a key of GroupBy, or an operand of Calc.
func (t JSONText[E]) Term[_ Lazy[E]]() Term[E] { return Term[E]{engine.JSONTerm(t.col, t.path)} }

// Term returns the value as a Term, as JSONText's Term does: Scan reads it
// into a number field, and Sum, Avg, Min and Max aggregate it.
func (n JSONNumber[E]) Term[_ Lazy[E]]() Term[E] { return Term[E]{engine.JSONTerm(n.col, n.path)} }

// Term returns the value as a Term, as JSONText's Term does: Scan reads it
// into a bool field.
func (b JSONBool[E]) Term[_ Lazy[E]]() Term[E] { return Term[E]{engine.JSONTerm(b.col, b.path)} }

func jsonPath(kind engine.JSONKind, keys []string) engine.JSONPath {
	return engine.JSONPath{Kind: kind, Keys: slices.Clone(keys)}
}

func jsonPred[E any](col int32, p engine.JSONPath, leaf engine.Node) Pred[E] {
	return Pred[E](engine.NewPredicate(engine.JSONNode(col, p, leaf)))
}

func hasKey[E any](col int32, keys []string) Pred[E] {
	return jsonPred[E](col, jsonPath(engine.JSONKindAny, keys), engine.Node{Op: engine.OpNotNull})
}

func jsonHas[E any](col int32, v any) Pred[E] {
	return Pred[E](engine.NewPredicate(engine.JSONHasNode(col, v)))
}

// jsonHasAny is true where the array holds one of vs: an Or of Has, false for
// none, as an In with no values is.
func jsonHasAny[E any](col int32, vs []any) Pred[E] {
	return jsonHasEach[E](false, col, vs)
}

// jsonHasAll is true where the array holds each of vs: an And of Has, true for
// none, as a NotIn with no values is.
func jsonHasAll[E any](col int32, vs []any) Pred[E] {
	return jsonHasEach[E](true, col, vs)
}

func jsonHasEach[E any](all bool, col int32, vs []any) Pred[E] {
	kids := make([]engine.Node, len(vs))
	for i, v := range vs {
		kids[i] = engine.JSONHasNode(col, v)
	}
	if all {
		return Pred[E](engine.NewPredicate(engine.Group(engine.OpAnd, kids)))
	}
	return Pred[E](engine.NewPredicate(engine.Group(engine.OpOr, kids)))
}

func jsonContains[E any](col int32, doc any) Pred[E] {
	return Pred[E](engine.NewPredicate(engine.JSONContainsNode(col, doc)))
}

func jsonIsEmpty[E any](col int32) Pred[E] {
	return Pred[E](engine.NewPredicate(engine.JSONEmptyNode(col)))
}

// EQ is true where the string at the path is v.
func (t JSONText[E]) EQ[_ Lazy[E]](v string) Pred[E] {
	return jsonPred[E](t.col, t.path, engine.Node{Op: engine.OpEQ, V: v})
}

// NEQ is true where the path holds a string that is not v. Like a nullable
// column's, it is false where the path holds no string.
func (t JSONText[E]) NEQ[_ Lazy[E]](v string) Pred[E] {
	return jsonPred[E](t.col, t.path, engine.Node{Op: engine.OpNEQ, V: v})
}

// In is true where the string at the path is one of vs. With no values it is
// false.
func (t JSONText[E]) In[_ Lazy[E]](vs ...string) Pred[E] {
	return jsonPred[E](t.col, t.path, engine.Node{Op: engine.OpIn, V: list(vs)})
}

// NotIn is true where the path holds a string that is none of vs. With no
// values it is true.
func (t JSONText[E]) NotIn[_ Lazy[E]](vs ...string) Pred[E] {
	return jsonPred[E](t.col, t.path, engine.Node{Op: engine.OpNotIn, V: list(vs)})
}

// Contains is true where the string at the path contains s, taken
// literally and with case on every dialect: SQLite's text is a GLOB, as its
// LIKE ignores the case of ASCII letters.
func (t JSONText[E]) Contains[_ Lazy[E]](s string) Pred[E] {
	return jsonPred[E](t.col, t.path, engine.JSONLikeLeaf(engine.JSONContains, s))
}

// HasPrefix is true where the string at the path begins with s, taken
// literally and with case, as Contains.
func (t JSONText[E]) HasPrefix[_ Lazy[E]](s string) Pred[E] {
	return jsonPred[E](t.col, t.path, engine.JSONLikeLeaf(engine.JSONHasPrefix, s))
}

// HasSuffix is true where the string at the path ends with s, taken
// literally and with case, as Contains.
func (t JSONText[E]) HasSuffix[_ Lazy[E]](s string) Pred[E] {
	return jsonPred[E](t.col, t.path, engine.JSONLikeLeaf(engine.JSONHasSuffix, s))
}

// Asc orders by the string at the path, ascending, in the database's order
// of text (its collation), NULL last: where the path is missing or holds no
// string. An entity query ordered so cannot be read After a row or by a
// cursor.
func (t JSONText[E]) Asc[_ Lazy[E]]() Order[E] {
	return Order[E]{OrderTerm{Expr: engine.JSONTerm(t.col, t.path)}}
}

// Desc orders by the string at the path, descending, NULL first.
func (t JSONText[E]) Desc[_ Lazy[E]]() Order[E] {
	return Order[E]{OrderTerm{Expr: engine.JSONTerm(t.col, t.path), Desc: true}}
}

// IsNull is true where the path is missing or holds no string.
func (t JSONText[E]) IsNull[_ Lazy[E]]() Pred[E] {
	return jsonPred[E](t.col, t.path, engine.Node{Op: engine.OpIsNull})
}

// NotNull is true where the path holds a string.
func (t JSONText[E]) NotNull[_ Lazy[E]]() Pred[E] {
	return jsonPred[E](t.col, t.path, engine.Node{Op: engine.OpNotNull})
}

// EQ is true where the number at the path is v.
func (n JSONNumber[E]) EQ[_ Lazy[E]](v float64) Pred[E] {
	return jsonPred[E](n.col, n.path, engine.Node{Op: engine.OpEQ, V: v})
}

// NEQ is true where the path holds a number that is not v.
func (n JSONNumber[E]) NEQ[_ Lazy[E]](v float64) Pred[E] {
	return jsonPred[E](n.col, n.path, engine.Node{Op: engine.OpNEQ, V: v})
}

// GT is true where the number at the path is greater than v.
func (n JSONNumber[E]) GT[_ Lazy[E]](v float64) Pred[E] {
	return jsonPred[E](n.col, n.path, engine.Node{Op: engine.OpGT, V: v})
}

// GTE is true where the number at the path is at least v.
func (n JSONNumber[E]) GTE[_ Lazy[E]](v float64) Pred[E] {
	return jsonPred[E](n.col, n.path, engine.Node{Op: engine.OpGTE, V: v})
}

// LT is true where the number at the path is less than v.
func (n JSONNumber[E]) LT[_ Lazy[E]](v float64) Pred[E] {
	return jsonPred[E](n.col, n.path, engine.Node{Op: engine.OpLT, V: v})
}

// LTE is true where the number at the path is at most v.
func (n JSONNumber[E]) LTE[_ Lazy[E]](v float64) Pred[E] {
	return jsonPred[E](n.col, n.path, engine.Node{Op: engine.OpLTE, V: v})
}

// Between is true where the number at the path is from lo to hi, both
// included.
func (n JSONNumber[E]) Between[_ Lazy[E]](lo, hi float64) Pred[E] {
	return jsonPred[E](n.col, n.path, engine.Node{Op: engine.OpBetween, V: lo, W: hi})
}

// Asc orders by the number at the path, ascending, NULL last: where the
// path is missing or holds no number. An entity query ordered so cannot be
// read After a row or by a cursor.
func (n JSONNumber[E]) Asc[_ Lazy[E]]() Order[E] {
	return Order[E]{OrderTerm{Expr: engine.JSONTerm(n.col, n.path)}}
}

// Desc orders by the number at the path, descending, NULL first.
func (n JSONNumber[E]) Desc[_ Lazy[E]]() Order[E] {
	return Order[E]{OrderTerm{Expr: engine.JSONTerm(n.col, n.path), Desc: true}}
}

// IsNull is true where the path is missing or holds no number.
func (n JSONNumber[E]) IsNull[_ Lazy[E]]() Pred[E] {
	return jsonPred[E](n.col, n.path, engine.Node{Op: engine.OpIsNull})
}

// NotNull is true where the path holds a number.
func (n JSONNumber[E]) NotNull[_ Lazy[E]]() Pred[E] {
	return jsonPred[E](n.col, n.path, engine.Node{Op: engine.OpNotNull})
}

// EQ is true where the boolean at the path is v.
func (b JSONBool[E]) EQ[_ Lazy[E]](v bool) Pred[E] {
	return jsonPred[E](b.col, b.path, engine.Node{Op: engine.OpEQ, V: v})
}

// IsNull is true where the path is missing or holds no boolean.
func (b JSONBool[E]) IsNull[_ Lazy[E]]() Pred[E] {
	return jsonPred[E](b.col, b.path, engine.Node{Op: engine.OpIsNull})
}

// NotNull is true where the path holds a boolean.
func (b JSONBool[E]) NotNull[_ Lazy[E]]() Pred[E] {
	return jsonPred[E](b.col, b.path, engine.Node{Op: engine.OpNotNull})
}

// JSONArray is the JSON array at a path of object keys in a JSON column of
// entity E, made by a JSON handle's Array, for the conditions on its
// elements that the handle's Has, HasSome, HasEvery and IsEmpty make on the
// column's own array, and HasField on the members of its objects. Array()
// with no key is the column's own array. Where the path is missing or holds no array they are
// false, and where the column is NULL they are NULL, as the handle's are.
// Its conditions read alike on PostgreSQL (jsonb), MySQL and SQLite.
type JSONArray[E any] struct {
	col  int32
	keys []string
}

func jsonArray[E any](col int32, keys []string) JSONArray[E] {
	return JSONArray[E]{col, slices.Clone(keys)}
}

// Has is true where the array holds the element v: a string, a number or a
// boolean, of any Go type of those kinds, as the handle's Has takes it.
// PostgreSQL's form keeps a bare @> of the column, which a GIN index on it
// serves.
func (a JSONArray[E]) Has[_ Lazy[E]](v any) Pred[E] {
	return Pred[E](engine.NewPredicate(engine.JSONAtPath(engine.JSONHasNode(a.col, v), a.keys)))
}

// HasField is true where the array holds an object whose member key is v: a
// string, a number or a boolean, compared as Has compares an element, so 7
// matches 7.0 and true is not 1. An element that is no object, or lacks the
// member, does not match. key is one member's name, which may not be empty
// or hold a double quote, a backslash or a control character. Not(HasField)
// matches an empty array and a path that is missing or holds no array, and
// no row whose column is NULL, as Not(Has) does.
//
// PostgreSQL writes a bare @> of the column against [{"key": v}], at the
// path, which a GIN index on it serves; MySQL writes JSON_CONTAINS, which
// also matches a member that is an array holding v; SQLite searches
// json_each.
func (a JSONArray[E]) HasField[_ Lazy[E]](key string, v any) Pred[E] {
	return Pred[E](engine.NewPredicate(engine.JSONAtPath(engine.JSONHasFieldNode(a.col, key, v), a.keys)))
}

// HasSome is true where the array holds one of vs. With no values it is
// false.
func (a JSONArray[E]) HasSome[_ Lazy[E]](vs ...any) Pred[E] { return a.each(false, vs) }

// HasEvery is true where the array holds every one of vs. With no values it
// is true.
func (a JSONArray[E]) HasEvery[_ Lazy[E]](vs ...any) Pred[E] { return a.each(true, vs) }

// IsEmpty is true where the array has no element, []: false for an array
// with one, for a document that is no array and where the path is missing.
func (a JSONArray[E]) IsEmpty[_ Lazy[E]]() Pred[E] {
	return Pred[E](engine.NewPredicate(engine.JSONAtPath(engine.JSONEmptyNode(a.col), a.keys)))
}

// each is HasSome's and HasEvery's work: an Or or an And of Has.
func (a JSONArray[E]) each[_ Lazy[E]](all bool, vs []any) Pred[E] {
	kids := make([]engine.Node, len(vs))
	for i, v := range vs {
		kids[i] = engine.JSONAtPath(engine.JSONHasNode(a.col, v), a.keys)
	}
	if all {
		return Pred[E](engine.NewPredicate(engine.Group(engine.OpAnd, kids)))
	}
	return Pred[E](engine.NewPredicate(engine.Group(engine.OpOr, kids)))
}
