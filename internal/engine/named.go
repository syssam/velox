package engine

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unsafe"
)

// cond is the condition op on the column of the given name, with the
// value v.
func cond(name string, op PredOp, v any) Predicate {
	return Predicate{Node{Op: opNamed, Col: int32(op) | int32(len(name))<<8, V: v, W: nameData(name)}}
}

// nameData is the bytes of name, as a node's W holds a name: a string in an
// any would cost an allocation for each condition, and an unsafe.Pointer
// costs none. The node's Col holds the length, above its low byte.
func nameData(name string) any { return unsafe.Pointer(unsafe.StringData(name)) }

// nodeName is the name a node by name holds (nameData).
func nodeName(n Node) string {
	return unsafe.String((*byte)(n.W.(unsafe.Pointer)), int(n.Col>>8))
}

// FieldEQ is the column of the given name = v. A condition by name is
// resolved when the statement that holds it runs: a name the entity has no
// column of fails the statement. v is converted to the column's type: a
// value of another type of the same kind (an int for an int64 column, a
// string for a string enum) is converted, and a value of another kind fails
// the statement. FieldEQ(name, nil) fails: FieldIsNull says that. On a
// nullable column, a value compares the value.
func FieldEQ(name string, v any) Predicate { return cond(name, PredEQ, v) }

// FieldNEQ is column <> v, with v converted as FieldEQ converts it.
func FieldNEQ(name string, v any) Predicate { return cond(name, PredNEQ, v) }

// FieldGT is column > v, with v converted as FieldEQ converts it.
func FieldGT(name string, v any) Predicate { return cond(name, PredGT, v) }

// FieldGTE is column >= v, with v converted as FieldEQ converts it.
func FieldGTE(name string, v any) Predicate { return cond(name, PredGTE, v) }

// FieldLT is column < v, with v converted as FieldEQ converts it.
func FieldLT(name string, v any) Predicate { return cond(name, PredLT, v) }

// FieldLTE is column <= v, with v converted as FieldEQ converts it.
func FieldLTE(name string, v any) Predicate { return cond(name, PredLTE, v) }

// FieldIn is column IN (vs...), each converted as FieldEQ converts it.
// With no values it is false.
func FieldIn(name string, vs ...any) Predicate { return cond(name, PredIn, nonNil(vs)) }

// FieldNotIn is column NOT IN (vs...), each converted as FieldEQ converts
// it. With no values it is true.
func FieldNotIn(name string, vs ...any) Predicate { return cond(name, PredNotIn, nonNil(vs)) }

// FieldContains is column LIKE %s%, with s's LIKE characters escaped. The
// column must be text.
func FieldContains(name, s string) Predicate { return cond(name, PredContains, s) }

// FieldHasPrefix is column LIKE s%. The column must be text.
func FieldHasPrefix(name, s string) Predicate { return cond(name, PredHasPrefix, s) }

// FieldHasSuffix is column LIKE %s. The column must be text.
func FieldHasSuffix(name, s string) Predicate { return cond(name, PredHasSuffix, s) }

// FieldEqualFold is LOWER(column) = LOWER(s). The column must be text.
func FieldEqualFold(name, s string) Predicate { return cond(name, PredEqualFold, s) }

// FieldContainsFold is LOWER(column) LIKE LOWER(pattern), where the pattern
// holds s, taken literally. The column must be text.
func FieldContainsFold(name, s string) Predicate { return cond(name, PredContainsFold, s) }

// FieldIsNull is column IS NULL. The column must be nullable.
func FieldIsNull(name string) Predicate { return cond(name, PredIsNull, nil) }

// FieldNotNull is column IS NOT NULL. The column must be nullable.
func FieldNotNull(name string) Predicate { return cond(name, PredNotNull, nil) }

// nonNil is vs, or an empty slice for none.
func nonNil(vs []any) []any {
	if vs == nil {
		return []any{}
	}
	return vs
}

// HasEdge is true when the relation of the given name, as the schema
// declares the edge, holds at least one row. The target's interceptors and
// Read policy apply inside it, as they apply to a typed relation's Has. It
// is resolved when the statement that holds it runs, and only on a
// relation whose target the generated code links.
func HasEdge(name string) Predicate { return HasEdgeWith(name) }

// HasEdgeWith is true when the relation of the given name holds at least
// one row that meets every condition of ps, which name the target's
// columns.
func HasEdgeWith(name string, ps ...Predicate) Predicate {
	kids := make([]Node, len(ps))
	for i, p := range ps {
		kids[i] = p.n
	}
	return Predicate{Node{Op: opNamedEdge, Col: int32(len(name)) << 8, W: nameData(name), Kids: kids}}
}

// namedSub is what an opNamedSub holds: the owner's column, the target
// entity's name and the target's key column.
type namedSub struct{ column, entity, key string }

// FieldInSelect is true when the column of the given name is among the values
// of the key column of the entity named entity, in the rows that meet every
// condition of ps, which name the target's columns: column IN (SELECT key FROM
// entity WHERE ps), written as the EXISTS a column handle's InSelect writes. The
// target's Read policy applies inside it. entity is the target's name in the
// schema or its table, which the owner's EntityTable finds (namedSubquery).
func FieldInSelect(column, entity, key string, ps ...Predicate) Predicate {
	kids := make([]Node, len(ps))
	for i, p := range ps {
		kids[i] = p.n
	}
	return Predicate{Node{Op: opNamedSub, V: &namedSub{column, entity, key}, Kids: kids}}
}

// nameIndex is each column's index by its name.
func nameIndex(cols []ColumnInfo) map[string]int32 {
	m := make(map[string]int32, len(cols))
	for i := range cols {
		m[cols[i].Name] = int32(i)
	}
	return m
}

// valType is the Go type a condition or a mutation by name gives a column
// whose field is of type ft: ft, without the pointer of a nullable field.
func valType(ft reflect.Type) any {
	if ft.Kind() == reflect.Pointer {
		return ft.Elem()
	}
	return ft
}

// resolveStatementNames resolves the conditions by name in st's own
// conditions, against its entity.
func resolveStatementNames(st *Statement) error {
	return mapConds(st, func(ps []Predicate) ([]Predicate, error) {
		return resolveNamedPreds(st.Entity, ps)
	})
}

// resolveNamedPreds returns ps with every condition by name resolved
// against in, on a copy when any was by name: ps may be the query's, shared
// with other runs. A typed condition costs one test of its flags.
func resolveNamedPreds(in *EntityInfo, ps []Predicate) ([]Predicate, error) {
	out := ps
	for i, p := range ps {
		if !named(p.n) {
			continue
		}
		n, err := resolveNamed(in, p.n)
		if err != nil {
			return nil, fmt.Errorf("velox: %s: %w", in.Name, err)
		}
		if &out[0] == &ps[0] {
			out = slices.Clone(ps)
		}
		out[i] = Predicate{n}
	}
	return out, nil
}

// resolveNamed returns n with every condition by name in it resolved
// against in, the entity of the statement or the EXISTS that holds it. It
// never edits n: a changed node is a copy with new kids. The leaves become
// those Table.Pred makes and a HasEdge's EXISTS that of a typed Has, so the
// SQL and the render cache's key are a typed condition's.
func resolveNamed(in *EntityInfo, n Node) (Node, error) {
	switch n.Op {
	case opNamed:
		return namedLeaf(in, n)
	case opNamedEdge:
		return namedEdge(in, n)
	case opNamedSub:
		return namedSubquery(in, n)
	}
	if !n.ByName {
		return n, nil
	}
	if n.Op == OpExists {
		// Table.Has with conditions by name: they name the target's
		// columns.
		l, err := existsLink(n)
		if err != nil {
			return n, err
		}
		kids, _, err := resolveKids(l.Target, n.Kids)
		if err != nil {
			return n, err
		}
		n.V, n.Kids, n.ByName = l, kids, false
		return n, nil
	}
	kids, deep, err := resolveKids(in, n.Kids)
	if err != nil {
		return n, err
	}
	n.Kids, n.Deep, n.ByName = kids, deep, false
	return n, nil
}

// resolveKids returns kids resolved against in, in a new slice, and
// whether an EXISTS is among them at any depth.
func resolveKids(in *EntityInfo, kids []Node) ([]Node, bool, error) {
	out := make([]Node, len(kids))
	deep := false
	for i, k := range kids {
		r, err := resolveNamed(in, k)
		if err != nil {
			return nil, false, err
		}
		out[i] = r
		deep = deep || r.Op == OpExists || r.Op == OpThrough || r.Deep
	}
	return out, deep, nil
}

// namedLeaf resolves n, a condition on a column by name, against in.
func namedLeaf(in *EntityInfo, n Node) (Node, error) {
	name := nodeName(n)
	i, ok := in.names[name]
	if !ok {
		return n, fmt.Errorf("no column %q", name)
	}
	c := &in.Columns[i]
	op := PredOp(n.Col & 0xff)
	switch op {
	case PredEQ, PredNEQ, PredGT, PredGTE, PredLT, PredLTE:
		if n.V == nil {
			return n, fmt.Errorf("a comparison of %s with nil: use IsNull or NotNull", name)
		}
		v, err := namedValue(c, n.V)
		if err != nil {
			return n, err
		}
		r := predOps[op]
		r.Col, r.V, r.W = i, v, in
		return r, nil
	case PredIn, PredNotIn:
		vs := n.V.([]any)
		out := make([]any, len(vs))
		for k, x := range vs {
			if x == nil {
				return n, fmt.Errorf("a FieldIn or FieldNotIn of nil on %s: use FieldIsNull or FieldNotNull", name)
			}
			v, err := namedValue(c, x)
			if err != nil {
				return n, err
			}
			out[k] = v
		}
		r := predOps[op]
		r.Col, r.V, r.W = i, out, in
		return r, nil
	case PredContains, PredHasPrefix, PredHasSuffix:
		if rtype(c.val).Kind() != reflect.String {
			return n, fmt.Errorf("%s is not a text column", name)
		}
		s := EscapeLike(n.V.(string))
		switch op {
		case PredContains:
			s = "%" + s + "%"
		case PredHasPrefix:
			s += "%"
		default:
			s = "%" + s
		}
		return Node{Op: OpLike, Col: i, V: s, W: in}, nil
	case PredEqualFold:
		if rtype(c.val).Kind() != reflect.String {
			return n, fmt.Errorf("%s is not a text column", name)
		}
		return Node{Op: OpFold, Col: i, V: n.V, W: in}, nil
	case PredContainsFold:
		if rtype(c.val).Kind() != reflect.String {
			return n, fmt.Errorf("%s is not a text column", name)
		}
		return Node{Op: OpLikeFold, Col: i, V: "%" + EscapeLike(n.V.(string)) + "%", W: in}, nil
	case PredIsNull, PredNotNull:
		if !c.Nullable {
			return n, fmt.Errorf("%s is not nullable", name)
		}
		r := predOps[op]
		r.Col, r.W = i, in
		return r, nil
	}
	return n, fmt.Errorf("no condition operator %d", op)
}

// namedEdge resolves n, an EXISTS on a relation by name, against in: the
// relation's link, with its conditions resolved against its target.
func namedEdge(in *EntityInfo, n Node) (Node, error) {
	name := nodeName(n)
	i := slices.IndexFunc(in.rels, func(r *relation) bool { return r.Name == name })
	if i < 0 {
		return n, fmt.Errorf("no relation %q", name)
	}
	r := in.rels[i]
	t, err := in.entityTable()
	if err != nil {
		return n, fmt.Errorf("relation %s: %w", name, err)
	}
	target, ok, err := t.target(targetName(r.typ.(reflect.Type)))
	if err != nil {
		return n, err
	}
	if !ok {
		return n, fmt.Errorf("relation %s: its target %s is not in the entity table of %s", name, targetName(r.typ.(reflect.Type)), in.Name)
	}
	l, err := r.Resolve(in, RelTarget{Get: func() (*EntityInfo, error) { return target, nil }, One: r.typ, Many: r.typ})
	if err != nil {
		return n, err
	}
	kids, _, err := resolveKids(l.Target, n.Kids)
	if err != nil {
		return n, fmt.Errorf("relation %s: %w", name, err)
	}
	return Node{Op: OpExists, Col: int32(i), V: l, Kids: kids}, nil
}

// namedSubquery resolves n, an EXISTS on another entity by name, against in:
// the owner's column equals the target's key column, and its conditions are
// resolved against the target, so the target's Read policy and interceptors
// apply inside it, as they do inside a typed InSelect.
func namedSubquery(in *EntityInfo, n Node) (Node, error) {
	s := n.V.(*namedSub)
	oc, ok := in.names[s.column]
	if !ok {
		return n, fmt.Errorf("FieldInSelect: no column %q", s.column)
	}
	t, err := in.entityTable()
	if err != nil {
		return n, fmt.Errorf("FieldInSelect: %w", err)
	}
	target, err := t.entity(s.entity)
	if err != nil {
		return n, fmt.Errorf("FieldInSelect: %w", err)
	}
	tc, ok := target.names[s.key]
	if !ok {
		return n, fmt.Errorf("FieldInSelect: %s has no column %q", s.entity, s.key)
	}
	text, err := subKey(in, int(oc), target, int(tc))
	if err != nil {
		return n, fmt.Errorf("FieldInSelect: %w", err)
	}
	l, err := ColRef{Owner: func() (*EntityInfo, error) { return in, nil }, Target: func() (*EntityInfo, error) { return target, nil }, OwnerCol: oc, TargetCol: tc}.Link()
	if err != nil {
		return n, err
	}
	l.TextKey = text
	kids, _, err := resolveKids(l.Target, n.Kids)
	if err != nil {
		return n, fmt.Errorf("FieldInSelect %s: %w", s.entity, err)
	}
	return Node{Op: OpExists, Col: -1, V: l, Kids: kids}, nil
}

// subKey checks the owner's column oc against the target's key column tc,
// as a typed InSelect does (sameKey: one type, a nullable column keying as
// its type), with one exception: an integer column against a text key is
// compared as text, and subKey reports it. Such a key holds ids of several
// entities as text, as an approval's entity_id does (ADR 0091).
func subKey(in *EntityInfo, oc int, target *EntityInfo, tc int) (bool, error) {
	x, y := in.Columns[oc], target.Columns[tc]
	if !x.Codec && !y.Codec && isInteger(base(x.Type).Kind()) && base(y.Type).Kind() == reflect.String {
		return true, nil
	}
	return false, sameKey(in, oc, target, tc)
}

// isInteger reports whether k is a signed or unsigned integer kind.
func isInteger(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

// namedValue returns v as column c's value type, converted as
// Table.Convert converts: v of the type as it is, and v of another type of
// the same kind class converted and range-checked.
func namedValue(c *ColumnInfo, v any) (any, error) {
	want := rtype(c.val)
	got := reflect.TypeOf(v)
	if got == want {
		return v, nil
	}
	switch out, res := coerce(want, v); res {
	case coerced:
		return out, nil
	case overflow:
		return nil, fmt.Errorf("%v for %s, %s: %w", v, c.Name, article(want), ErrOutOfRange)
	}
	return nil, fmt.Errorf("%s is %s, not %s", c.Name, article(want), article(got))
}

// The results of coerce.
const (
	coerced  = iota // the value converted
	mismatch        // the value is of another kind class
	overflow        // the value's type cannot hold it
)

// coerce returns v converted to want, a type of the same kind class, and
// coerced; or mismatch, or overflow when want cannot hold the number.
func coerce(want reflect.Type, v any) (any, int) {
	got := reflect.TypeOf(v)
	if got == want {
		return v, coerced
	}
	if got == nil || want == nil || kindClass(got.Kind()) != kindClass(want.Kind()) || !got.ConvertibleTo(want) {
		return nil, mismatch
	}
	rv := reflect.ValueOf(v)
	if overflows(rv, want) {
		return nil, overflow
	}
	return rv.Convert(want).Interface(), coerced
}

// article is t's name with its indefinite article: "an int64", "a string".
func article(t reflect.Type) string {
	if t == nil {
		return "nil"
	}
	s := t.String()
	if s != "" && strings.ContainsRune("aeiouAEIOU", rune(s[0])) {
		return "an " + s
	}
	return "a " + s
}
