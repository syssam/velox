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
// the path is missing or holds a value that is not a number. Its conditions
// read alike on PostgreSQL (jsonb), MySQL and SQLite.
type JSONNumber[E any] struct {
	col  int32
	path engine.JSONPath
}

func jsonPath(kind engine.JSONKind, keys []string) engine.JSONPath {
	return engine.JSONPath{Kind: kind, Keys: slices.Clone(keys)}
}

func jsonPred[E any](col int32, p engine.JSONPath, leaf engine.Node) Pred[E] {
	return Pred[E](engine.NewPredicate(engine.JSONNode(col, p, leaf)))
}

func hasKey[E any](col int32, keys []string) Pred[E] {
	return jsonPred[E](col, jsonPath(engine.JSONKindAny, keys), engine.Node{Op: engine.OpNotNull})
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

// IsNull is true where the path is missing or holds no number.
func (n JSONNumber[E]) IsNull[_ Lazy[E]]() Pred[E] {
	return jsonPred[E](n.col, n.path, engine.Node{Op: engine.OpIsNull})
}

// NotNull is true where the path holds a number.
func (n JSONNumber[E]) NotNull[_ Lazy[E]]() Pred[E] {
	return jsonPred[E](n.col, n.path, engine.Node{Op: engine.OpNotNull})
}
