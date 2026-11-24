// Package handlegen writes velox's handles.go: the column handle types and
// their methods. go generate runs it in the module root, and a test in package
// velox fails when handles.go differs from its output.
package handlegen

import (
	"bytes"
	"fmt"
	"go/format"
	"strings"
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
		{"ContainsFold", "is true when the column contains s, taken literally, whatever the case of either: LOWER(column) LIKE LOWER(pattern). SQLite's LOWER folds ASCII letters only, so on SQLite it ignores the case of those alone.", "(s V) Pred[E]",
			`return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpLikeFold, Col: c.I, V: "%" + engine.EscapeLike(string(s)) + "%"}))`},
	}
	set = []method{
		{"Set", "sets the column to v.", "(v V) Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignSet, Value: v}}"},
		setPtr(""),
	}
	nullSet = []method{
		set[0],
		setPtr(" A nil p does not write NULL: SetNull does."),
	}
	values = []method{
		{"Old", "returns the column's value in ch's old row: the zero value for an insert, or when ch is of another entity.", "(ch Change) V",
			"return changeValue[E, V](ch, ch.row.Old, c.I)"},
		newValue,
	}
	nullValues = []method{
		{"Old", "returns the column's value in ch's old row: the field's element, the zero value for NULL, for an insert, or when ch is of another entity; OldPtr tells NULL apart.", "(ch Change) V",
			"return changeValue[E, V](ch, ch.row.Old, c.I)"},
		newValue,
	}
	ptrs = []method{
		{"OldPtr", "returns a fresh pointer to the column's value in ch's old row (slice and map contents are shared with the snapshot); nil for NULL, an insert, or ch of another entity.", "(ch Change) *V",
			"return changePtr[E, V](ch, ch.row.Old, c.I)"},
		{"NewPtr", "returns a fresh pointer to the column's value in ch's new row (slice and map contents are shared with the snapshot); nil for NULL, a delete, or ch of another entity.", "(ch Change) *V",
			"return changePtr[E, V](ch, ch.row.New, c.I)"},
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
		{"Has", "is true where the column's JSON array holds the element v, a string, a number or a bool: false where the column holds no array, NULL where it is NULL. Any other v fails the statement.", "(v any) Pred[E]",
			"return jsonHas[E](c.I, v)"},
		{"HasSome", "is true where the column's JSON array holds at least one of vs. With no values it is false.", "(vs ...any) Pred[E]",
			"return jsonHasAny[E](c.I, vs)"},
		{"HasEvery", "is true where the column's JSON array holds every one of vs. With no values it is true.", "(vs ...any) Pred[E]",
			"return jsonHasAll[E](c.I, vs)"},
		{"Contains", "is true where the column's JSON document contains doc, as the database's JSON containment defines it: PostgreSQL's @> on a jsonb column, MySQL's JSON_CONTAINS. An object contains an object whose every key and value it contains, recursively, and a scalar an equal scalar; arrays follow the database's rule. SQLite has no JSON containment, so there the statement fails. doc is encoded with encoding/json once, when Contains is called, and bound as one argument; a doc that does not encode fails the statement. On PostgreSQL a GIN index on the column serves it.", "(doc any) Pred[E]",
			"return jsonContains[E](c.I, doc)"},
		{"IsEmpty", "is true where the column's JSON array has no element, []: false for an array with one and for a document that is no array, NULL where the column is.", "() Pred[E]",
			"return jsonIsEmpty[E](c.I)"},
		{"Len", "is the number of elements of the JSON array at the path of object keys in the column, or of the column's own array when no key is given, for its conditions and an ordering: NULL where the path is missing or holds no array (an object has no length).", "(keys ...string) JSONNumber[E]",
			"return JSONNumber[E]{c.I, jsonPath(engine.JSONKindLen, keys)}"},
		{"Array", "is the JSON array at the path of object keys in the column, or the column's own array when no key is given, for the conditions on its elements: Has, HasSome, HasEvery and IsEmpty, false where the path is missing or holds no array.", "(keys ...string) JSONArray[E]",
			"return jsonArray[E](c.I, keys)"},
	}
	appendJSON = []method{
		{"Append", "appends the elements of v, a slice, to the column's JSON array, as ent's AppendX does: PostgreSQL's ||, MySQL's JSON_MERGE_PRESERVE and SQLite's json_insert. A NULL column, or one holding the JSON null, becomes the elements; a document that is no array becomes the first element. v must encode as a JSON array; an insert refuses Append, as it refuses Add. PostgreSQL's column must be jsonb, as velox-gen writes it.", "(v V) Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignAppend, Value: v}}"},
	}
	null = []method{
		{"SetNull", "sets the column to NULL.", "() Setter[E]",
			"return Setter[E]{Assignment{Column: int(c.I), Op: AssignSet}}"},
		{"IsNull", "is column IS NULL.", "() Pred[E]", "return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpIsNull, Col: c.I}))"},
		{"NotNull", "is column IS NOT NULL.", "() Pred[E]", "return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpNotNull, Col: c.I}))"},
	}
)

// hook are the methods by which a hook reads and writes the column of a
// Mutation or a Row by its type, for every handle kind: Mutation.Value,
// Set and Fill, and Row.Value and Set, by name, checked at compile time.
var (
	hook = []method{
		{"Value", "returns the value the update m assigns to the column, as Mutation.Value does: false when it does not assign it with Set, and on a delete; on an insert, read the rows with Get. A mutation of another entity is a misuse, which fails the write.", "(m *Mutation) (V, bool)",
			"return mutValue[E, V](m, c.I)"},
		{"Assign", "sets the column to v, as Mutation.Set does: in every row of an insert, or in an update's assignment, which it replaces. On a delete, or on a mutation of another entity, it is a misuse, which is recorded and returned.", "(m *Mutation, v V) error",
			`return mutSet[E]("Assign", m, c.I, v)`},
		hookFill,
		hookGet,
		{"Put", "sets the column of the insert's row r to v, as Row.Set does. In an Insert policy, or on a row of another entity, it is a misuse, which is recorded and returned.", "(r Row, v V) error",
			`return rowSet[E]("Put", r, c.I, v)`},
	}
	nullHook = []method{
		{"Value", "returns the value the update m assigns to the column, as Mutation.Value does: the zero value for NULL, and false when it does not assign it with Set, and on a delete; ValuePtr tells NULL apart. On an insert, read the rows with Get. A mutation of another entity is a misuse, which fails the write.", "(m *Mutation) (V, bool)",
			"return mutValue[E, V](m, c.I)"},
		{"ValuePtr", "is Value as a fresh pointer: nil for NULL.", "(m *Mutation) (*V, bool)",
			"return mutValuePtr[E, V](m, c.I)"},
		{"Assign", "sets the column to v, as Mutation.Set does: in every row of an insert, or in an update's assignment, which it replaces; AssignNull sets NULL. On a delete, or on a mutation of another entity, it is a misuse, which is recorded and returned.", "(m *Mutation, v V) error",
			`return mutSet[E]("Assign", m, c.I, v)`},
		{"AssignNull", "sets the column to NULL, as Assign sets a value.", "(m *Mutation) error",
			`return mutSet[E]("AssignNull", m, c.I, nil)`},
		hookFill,
		{"Get", "returns the column's value in the insert's row r, as Row.Value does: the zero value for NULL; GetPtr tells NULL apart. A row of another entity is a misuse, which fails the write.", "(r Row) V",
			"return rowValue[E, V](r, c.I)"},
		{"GetPtr", "is Get as a fresh pointer: nil for NULL.", "(r Row) *V",
			"return rowPtr[E, V](r, c.I)"},
		{"Put", "sets the column of the insert's row r to v, as Row.Set does; PutNull sets NULL. In an Insert policy, or on a row of another entity, it is a misuse, which is recorded and returned.", "(r Row, v V) error",
			`return rowSet[E]("Put", r, c.I, v)`},
		{"PutNull", "sets the column of the insert's row r to NULL, as Put sets a value.", "(r Row) error",
			`return rowSet[E]("PutNull", r, c.I, nil)`},
	}
	hookFill = method{"Fill", "sets the column to v in each row of an insert that leaves it at its Go zero value (nil, for a nullable column), as Mutation.Fill does. On an update, a delete or a mutation of another entity it is a misuse, which is recorded and returned.", "(m *Mutation, v V) error",
		"return mutFill[E](m, c.I, v)"}
	hookGet = method{"Get", "returns the column's value in the insert's row r, as Row.Value does. A row of another entity is a misuse, which fails the write.", "(r Row) V",
		"return rowValue[E, V](r, c.I)"}
)

// setPtr is the SetPtr method of every handle kind, its doc ending with more.
func setPtr(more string) method {
	return method{"SetPtr", "sets the column to *p, read when SetPtr is called, as ent's SetNillable does: a nil p gives a setter that Set and SetInput skip, so the column is not written. An update whose every setter is skipped has no Set; an insert leaves the field as it is, so a zero field whose column has a declared database default gets that default; a column an upsert's Update names is not updated unless another setter sets it." + more, "(p *V) Setter[E]",
		"if p == nil { return Setter[E]{Assignment{Column: int(c.I), Op: engine.AssignSkip}} }; return Setter[E]{Assignment{Column: int(c.I), Op: AssignSet, Value: *p}}"}
}

// newValue is the New method of every handle kind.
var newValue = method{"New", "returns the column's value in ch's new row, as Old does: the zero value for a delete. An insert's new row is the row as stored, read back with the insert: a key, a default, or a value the column rounds is as the database holds it.", "(ch Change) V",
	"return changeValue[E, V](ch, ch.row.New, c.I)"}

type kind struct {
	name, tparams, doc string
	methods            [][]method
}

var kinds = []kind{
	{"Col", "[E, V any]", "is the handle of a column of entity E whose values, of type V, are neither numbers, times nor free text: bool, an enum, []byte and other types.", [][]method{base, key, count, set, values, hook}},
	{"TimeCol", "[E, V any]", "is the handle of a time column: a time field, of time.Time or a type defined on it.", [][]method{base, key, ordered, count, extremes, set, values, hook}},
	{"OtherCol", "[E, V any]", "is the handle of an Other field's column that holds no decimal: its SQL type, such as a range or a point, compares and orders in the database.", [][]method{base, key, ordered, count, extremes, set, values, hook}},
	{"NumberCol", "[E, V any]", "is the handle of a numeric column: a Go number, or an Other field whose SQL type is a decimal (numeric or decimal) on every dialect it names, such as a decimal.Decimal.", [][]method{base, key, ordered, count, extremes, sums, set, arith, values, hook}},
	{"StringCol", "[E any, V ~string]", "is the handle of a text column.", [][]method{base, key, ordered, count, extremes, text, set, values, hook}},
	{"NullCol", "[E, V any]", "is Col for a column that allows NULL: a field of type *V.", [][]method{base, nullKey, count, nullSet, null, nullValues, ptrs, nullHook}},
	{"NullTimeCol", "[E, V any]", "is TimeCol for a column that allows NULL: a field of type *V.", [][]method{base, nullKey, ordered, count, extremes, nullSet, null, nullValues, ptrs, nullHook}},
	{"NullOtherCol", "[E, V any]", "is OtherCol for a column that allows NULL: a field of type *V.", [][]method{base, nullKey, ordered, count, extremes, nullSet, null, nullValues, ptrs, nullHook}},
	{"NullNumberCol", "[E, V any]", "is NumberCol for a column that allows NULL: a field of type *V.", [][]method{base, nullKey, ordered, count, extremes, sums, nullSet, arith, null, nullValues, ptrs, nullHook}},
	{"NullStringCol", "[E any, V ~string]", "is StringCol for a column that allows NULL: a field of type *V.", [][]method{base, nullKey, ordered, count, extremes, text, nullSet, null, nullValues, ptrs, nullHook}},
	{"JSONCol", "[E, V any]", "is the handle of an encoded column: a json field, whose value is stored as JSON. It sets the column and Appends to its array, its Text, Number, Bool and HasKey test a value inside it, and its Has, HasSome, HasEvery, IsEmpty and Len test the elements of an array, and Array those of an array at a path; it neither compares nor orders by the whole document, as equality and order of JSON differ by dialect.", [][]method{set, appendJSON, jsonPaths, values, hook}},
	{"NullJSONCol", "[E, V any]", "is JSONCol for a column that allows NULL: a field of type *V.", [][]method{nullSet, appendJSON, jsonPaths, null, nullValues, ptrs, nullHook}},
}

// Generate returns the source of handles.go.
func Generate() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("// Code generated by internal/handlegen. DO NOT EDIT.\n\npackage velox\n\nimport \"github.com/syssam/velox/internal/engine\"\n")
	for _, k := range kinds {
		// A handle that Query.Values reads, one with a Key, names the type
		// of the values it reads in a field that is always nil: *V, or **V
		// for a nullable column. Values infers V from the struct.
		fields := "{ I int32 }"
		for _, ms := range k.methods {
			if len(ms) > 0 && ms[0].name == "Key" {
				v := strings.TrimSuffix(strings.TrimPrefix(ms[0].sig, "() GroupKey[E, "), "]")
				fields = "{\n\t_ *" + v + "\n\tI int32\n}"
			}
		}
		fmt.Fprintf(&b, "\n// %s %s I is the column's index; velox-gen sets it. I is for generated code and extensions; outside the 1.0 compatibility promise.\ntype %s%s struct%s\n", k.name, k.doc, k.name, k.tparams, fields)
		fmt.Fprintf(&b, "\nfunc (c %s[E, V]) column(*E) int32 { return c.I }\n", k.name)
		for _, ms := range k.methods {
			for _, m := range ms {
				fmt.Fprintf(&b, "\n// %s %s\nfunc (c %s[E, V]) %s[_ Lazy[V]]%s { %s }\n", m.name, m.doc, k.name, m.name, m.sig, m.body)
			}
		}
	}
	return format.Source(b.Bytes())
}
