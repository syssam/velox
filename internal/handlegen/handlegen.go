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
		{"Select", "is the subquery of the column's values in the rows that meet every p, for another handle's InSelect or NotInSelect: SELECT column FROM table WHERE ps…. It reads no metadata, so it may be called anywhere, an entity's Policy and Hooks bodies included.", "(ps ...Pred[E]) Subquery[V]",
			"return selectOf[E, V](c.I, ps)"},
		{"InSelect", "is true when the column equals a value of s: column IN (SELECT …). It is written as an EXISTS correlated on the two columns, and the subquery's entity gets its Read policy and interceptors' conditions, as HasWith's target does. NULL is never among the values, and a NULL column is not in them. On MySQL, an UPDATE or DELETE whose subquery reads the table being changed fails with error 1093.", "(s Subquery[V]) Pred[E]",
			"return inSelect[E](c.I, s)"},
		{"NotInSelect", "is Not(InSelect(s)): true when the column equals no value of s, and for a NULL column. Unlike SQL's NOT IN, a NULL among the subquery's values does not make it false for every row.", "(s Subquery[V]) Pred[E]",
			"return Not(inSelect[E](c.I, s))"},
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
	key = []method{
		{"Key", "is the column as the key of a grouped aggregate: CountBy, SumBy, AvgBy, MinBy or MaxBy.", "() GroupKey[E, V]",
			"return GroupKey[E, V]{c.I}"},
	}
	nullKey = []method{
		{"Key", "is the column as the key of a grouped aggregate: CountBy, SumBy, AvgBy, MinBy or MaxBy. NULL is one group, whose Key is nil.", "() GroupKey[E, *V]",
			"return GroupKey[E, *V]{c.I}"},
	}
	count = []method{
		{"Count", "is COUNT of the column, as a Term for Select, Having and OrderBy: the rows where it is not NULL.", "() Term[E]",
			"return Term[E]{engine.AggTerm(engine.TermCount, engine.ColumnTerm(c.I))}"},
		{"CountDistinct", "is COUNT(DISTINCT column), as a Term.", "() Term[E]",
			"return Term[E]{engine.AggTerm(engine.TermCountDistinct, engine.ColumnTerm(c.I))}"},
	}
	extremes = []method{
		{"Min", "is MIN of the column, as a Term. NULL when no row has a value.", "() Term[E]",
			"return Term[E]{engine.AggTerm(engine.TermMin, engine.ColumnTerm(c.I))}"},
		{"Max", "is MAX of the column, as a Term. NULL when no row has a value.", "() Term[E]",
			"return Term[E]{engine.AggTerm(engine.TermMax, engine.ColumnTerm(c.I))}"},
	}
	sums = []method{
		{"Sum", "is SUM of the column, as a Term. NULL when no row has a value.", "() Term[E]",
			"return Term[E]{engine.AggTerm(engine.TermSum, engine.ColumnTerm(c.I))}"},
		{"Avg", "is AVG of the column, as a Term. NULL when no row has a value.", "() Term[E]",
			"return Term[E]{engine.AggTerm(engine.TermAvg, engine.ColumnTerm(c.I))}"},
	}
	arith = []method{
		{"Add", "adds v to the column: column = column + v.", "(v V) Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignAdd, Value: v}}"},
		{"Sub", "subtracts v from the column: column = column - v.", "(v V) Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignSub, Value: v}}"},
	}
	jsonPaths = []method{
		{"Text", "is the string at the path of object keys in the column, for its conditions: NULL where the path is missing or holds no string. A key may not be empty or hold a double quote, a backslash or a control character.", "(keys ...string) JSONText[E]",
			"return JSONText[E]{c.I, jsonPath(engine.JSONKindText, keys)}"},
		{"Number", "is the number at the path of object keys in the column, for its conditions: NULL where the path is missing or holds no number.", "(keys ...string) JSONNumber[E]",
			"return JSONNumber[E]{c.I, jsonPath(engine.JSONKindNumber, keys)}"},
		{"Bool", "is the boolean at the path of object keys in the column, for its conditions: NULL where the path is missing or holds no boolean.", "(keys ...string) JSONBool[E]",
			"return JSONBool[E]{c.I, jsonPath(engine.JSONKindBool, keys)}"},
		{"HasKey", "is true where the path of object keys is in the column's document, whatever its value, a JSON null included.", "(keys ...string) Pred[E]",
			"return hasKey[E](c.I, keys)"},
	}
	null = []method{
		{"SetNull", "sets the column to NULL.", "() Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignSet}}"},
		{"IsNull", "is column IS NULL.", "() Pred[E]", "return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpIsNull, Col: c.I}))"},
		{"NotNull", "is column IS NOT NULL.", "() Pred[E]", "return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpNotNull, Col: c.I}))"},
	}
)

type kind struct {
	name, tparams, doc string
	methods            [][]method
}

var kinds = []kind{
	{"Col", "[E, V any]", "is the handle of a column of entity E whose values, of type V, are neither numbers, times nor free text: bool, an enum, []byte and other types.", [][]method{base, key, count, set}},
	{"OrderedCol", "[E, V any]", "is the handle of an ordered column: time.Time and types registered as ordered.", [][]method{base, key, ordered, count, extremes, set}},
	{"NumberCol", "[E, V any]", "is the handle of a numeric column.", [][]method{base, key, ordered, count, extremes, sums, set, arith}},
	{"StringCol", "[E any, V ~string]", "is the handle of a text column.", [][]method{base, key, ordered, count, extremes, text, set}},
	{"NullCol", "[E, V any]", "is Col for a column that allows NULL: a field of type *V.", [][]method{base, nullKey, count, set, null}},
	{"NullOrderedCol", "[E, V any]", "is OrderedCol for a column that allows NULL: a field of type *V.", [][]method{base, nullKey, ordered, count, extremes, set, null}},
	{"NullNumberCol", "[E, V any]", "is NumberCol for a column that allows NULL: a field of type *V.", [][]method{base, nullKey, ordered, count, extremes, sums, set, arith, null}},
	{"NullStringCol", "[E any, V ~string]", "is StringCol for a column that allows NULL: a field of type *V.", [][]method{base, nullKey, ordered, count, extremes, text, set, null}},
	{"JSONCol", "[E, V any]", "is the handle of an encoded column: a json field, whose value is stored as JSON. It sets the column, and its Text, Number, Bool and HasKey test a value inside it; it neither compares nor orders by the whole document, as equality and order of JSON differ by dialect.", [][]method{set, jsonPaths}},
	{"NullJSONCol", "[E, V any]", "is JSONCol for a column that allows NULL: a field of type *V.", [][]method{set, jsonPaths, null}},
}

// Generate returns the source of handles.go.
func Generate() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// Code generated by internal/handlegen. DO NOT EDIT.\n\npackage velox\n\nimport \"github.com/syssam/velox/internal/engine\"\n")
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
