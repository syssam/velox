// Package handlegen writes velox's handles.go: the column handle types and
// their methods. go generate runs it in the module root, and a test in package
// velox fails when handles.go differs from its output.
package handlegen

import (
	"bytes"
	"fmt"
	"go/format"
)

type method struct{ name, doc, sig, body string }

func cmp(name, op, doc string) method {
	return method{name, doc, "(v V) Pred[E]", "return Pred[E](engine.NewPredicate(engine.Node{Op: " + op + ", Col: c.I, V: v}))"}
}

const caseNote = " Whether case is ignored follows the database:" +
	" PostgreSQL's LIKE does not ignore it; MySQL's default collations and SQLite (for ASCII) do."

var (
	base = []method{
		cmp("EQ", "engine.OpEQ", "is column = v."),
		cmp("NEQ", "engine.OpNEQ", "is column <> v."),
		{"In", "is column IN (vs...). With no values it is false.", "(vs ...V) Pred[E]",
			"return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpIn, Col: c.I, V: list(vs)}))"},
		{"NotIn", "is column NOT IN (vs...). With no values it is true.", "(vs ...V) Pred[E]",
			"return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpNotIn, Col: c.I, V: list(vs)}))"},
		{"Asc", "orders by the column, ascending.", "() Order[E]",
			"return Order[E]{OrderTerm{Column: int(c.I)}}"},
		{"Desc", "orders by the column, descending.", "() Order[E]",
			"return Order[E]{OrderTerm{Column: int(c.I), Desc: true}}"},
	}
	ordered = []method{
		cmp("GT", "engine.OpGT", "is column > v."),
		cmp("GTE", "engine.OpGTE", "is column >= v."),
		cmp("LT", "engine.OpLT", "is column < v."),
		cmp("LTE", "engine.OpLTE", "is column <= v."),
		{"Between", "is column BETWEEN lo AND hi.", "(lo, hi V) Pred[E]",
			"return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpBetween, Col: c.I, V: lo, W: hi}))"},
	}
	text = []method{
		{"Like", "is column LIKE pattern: % and _ are wildcards, and ! escapes them and itself (\\ is an ordinary character: a \\ in a pattern matches a backslash)." + caseNote +
			" It takes a string, not the column's type, because a pattern is not a value of the column.", "(pattern string) Pred[E]",
			"return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpLike, Col: c.I, V: pattern}))"},
		{"HasPrefix", "is true when the column starts with s, taken literally." + caseNote, "(s V) Pred[E]",
			`return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpLike, Col: c.I, V: engine.EscapeLike(string(s)) + "%"}))`},
		{"HasSuffix", "is true when the column ends with s, taken literally." + caseNote, "(s V) Pred[E]",
			`return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpLike, Col: c.I, V: "%" + engine.EscapeLike(string(s))}))`},
		{"Contains", "is true when the column contains s, taken literally." + caseNote, "(s V) Pred[E]",
			`return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpLike, Col: c.I, V: "%" + engine.EscapeLike(string(s)) + "%"}))`},
		{"EqualFold", "is LOWER(column) = LOWER(s). SQLite's LOWER folds ASCII letters only, so on SQLite it ignores the case of those alone.", "(s V) Pred[E]",
			"return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpFold, Col: c.I, V: string(s)}))"},
	}
	set = []method{
		{"Set", "sets the column to v.", "(v V) Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignSet, Value: v}}"},
	}
	valueIn          = []method{valueInMethod("")}
	valueInArith     = []method{valueInMethod(addNote)}
	nullValueIn      = []method{nullValueInMethod("")}
	nullValueInArith = []method{nullValueInMethod(addNote)}
	arith            = []method{
		{"Add", "adds v to the column: column = column + v.", "(v V) Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignAdd, Value: v}}"},
		{"Sub", "subtracts v from the column: column = column - v.", "(v V) Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignSub, Value: v}}"},
	}
	null = []method{
		{"SetNull", "sets the column to NULL.", "() Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignSet}}"},
		{"IsNull", "is column IS NULL.", "() Pred[E]", "return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpIsNull, Col: c.I}))"},
		{"NotNull", "is column IS NOT NULL.", "() Pred[E]", "return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpNotNull, Col: c.I}))"},
	}
)

const (
	addNote    = "; an Add or Sub assigns no value"
	insertNote = ". A delete assigns none. Once next has run it also sees the UpdateDefault assignments." +
		" On an insert it records an error, as Mutation's Set does: read Rows." +
		" A value of another type (a Rewrite hook's, an untyped UpdateDefault's) it records as an error too, and reports none." +
		" A slice or map it returns is shared with the statement, and often with the caller: change the assignment with Mutation.Set, not in place."
)

func valueInMethod(add string) method {
	return method{"ValueIn", "returns the value m's update assigns to the column with Set, and whether it assigns one" + add + insertNote,
		"(m *Mutation[E]) (V, bool)", "return valueIn[E, V](m, c.I)"}
}

func nullValueInMethod(add string) method {
	return method{"ValueIn", "returns a pointer to a copy of the value m's update assigns to the column with Set, nil for SetNull, and whether it assigns one" + add + insertNote,
		"(m *Mutation[E]) (*V, bool)", "return nullValueIn[E, V](m, c.I)"}
}

type kind struct {
	name, tparams, doc string
	methods            [][]method
}

var kinds = []kind{
	{"Col", "[E, V any]", "is the handle of a column of entity E whose values, of type V, are neither ordered nor text: bool, []byte and other types.", [][]method{base, set, valueIn}},
	{"OrderedCol", "[E, V any]", "is the handle of an ordered column: time.Time and types registered as ordered.", [][]method{base, ordered, set, valueIn}},
	{"NumberCol", "[E, V any]", "is the handle of a numeric column.", [][]method{base, ordered, set, valueInArith, arith}},
	{"StringCol", "[E any, V ~string]", "is the handle of a text column.", [][]method{base, ordered, text, set, valueIn}},
	{"NullCol", "[E, V any]", "is Col for a column that allows NULL: a field of type *V.", [][]method{base, set, nullValueIn, null}},
	{"NullOrderedCol", "[E, V any]", "is OrderedCol for a column that allows NULL: a field of type *V.", [][]method{base, ordered, set, nullValueIn, null}},
	{"NullNumberCol", "[E, V any]", "is NumberCol for a column that allows NULL: a field of type *V.", [][]method{base, ordered, set, nullValueInArith, arith, null}},
	{"NullStringCol", "[E any, V ~string]", "is StringCol for a column that allows NULL: a field of type *V.", [][]method{base, ordered, text, set, nullValueIn, null}},
	{"JSONCol", "[E, V any]", "is the handle of an encoded column: a json field, whose value is stored as JSON. It sets the column and neither compares nor orders by it, as equality and order of JSON differ by dialect.", [][]method{set, valueIn}},
	{"NullJSONCol", "[E, V any]", "is JSONCol for a column that allows NULL: a field of type *V.", [][]method{set, nullValueIn, null}},
}

// Generate returns the source of handles.go.
func Generate() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// Code generated by internal/handlegen. DO NOT EDIT.\n\npackage velox\n\nimport \"github.com/syssam/velox2/internal/engine\"\n")
	for _, k := range kinds {
		fmt.Fprintf(&b, "\n// %s %s I is the column's index; velox-gen sets it.\ntype %s%s struct{ I int32 }\n", k.name, k.doc, k.name, k.tparams)
		fmt.Fprintf(&b, "\nfunc (c %s[E, V]) column(*E) int32 { return c.I }\n", k.name)
		for _, ms := range k.methods {
			for _, m := range ms {
				fmt.Fprintf(&b, "\n// %s %s\nfunc (c %s[E, V]) %s[_ Lazy[V]]%s { %s }\n", m.name, m.doc, k.name, m.name, m.sig, m.body)
			}
		}
	}
	return format.Source(b.Bytes())
}
