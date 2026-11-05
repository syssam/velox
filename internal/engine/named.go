package engine

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unsafe"
)

// anyEntity is the entity of a Cond: none, since a condition by name is
// resolved against the entity of the statement that holds it.
type anyEntity struct{}

// Cond is a condition that names its columns by name. It is resolved
// against the statement's entity when the statement runs. Or, And and Not
// combine it as they combine a Pred of one entity.
//
// Its zero value fails the statement that holds it, as the zero Pred does.
type Cond = Pred[anyEntity]

// Named is a column named by its name, as the schema declares it (C). Its
// methods make conditions on it. Its fields are not exported.
type Named struct{ name string }

// NamedEdge is a relation named by its name, as the schema declares it
// (Edge). Its fields are not exported.
type NamedEdge struct{ name string }

// C returns the column of the given name, as the schema declares it. A
// condition on it is resolved when the statement that holds it runs: a name
// the entity has no column of fails the statement, and so does a value of
// another kind than the column's.
func C(name string) Named { return Named{name} }

// Edge returns the relation of the given name, as the schema declares the
// edge. A condition on it is resolved when the statement that holds it
// runs, and only on a relation whose target the generated code links.
func Edge(name string) NamedEdge { return NamedEdge{name} }

// cond is the condition op on c with the value v.
func (c Named) cond(op PredOp, v any) Cond {
	return Cond{Node{Op: opNamed, Col: int32(op) | int32(len(c.name))<<8, V: v, W: nameData(c.name)}}
}

// nameData is the bytes of name, as a node's W holds a name: a string in an
// any would cost an allocation for each condition, and an unsafe.Pointer
// costs none. The node's Col holds the length, above its low byte.
func nameData(name string) any { return unsafe.Pointer(unsafe.StringData(name)) }

// nodeName is the name a node by name holds (nameData).
func nodeName(n Node) string {
	return unsafe.String((*byte)(n.W.(unsafe.Pointer)), int(n.Col>>8))
}

// EQ is column = v. v is converted to the column's type: a value of
// another type of the same kind (an int for an int64 column, a string for
// a string enum) is converted, and a value of another kind fails the
// statement. EQ(nil) fails: IsNull says that. On a nullable column, a
// value compares the value.
func (c Named) EQ(v any) Cond { return c.cond(PredEQ, v) }

// NEQ is column <> v, with v converted as EQ converts it.
func (c Named) NEQ(v any) Cond { return c.cond(PredNEQ, v) }

// GT is column > v, with v converted as EQ converts it.
func (c Named) GT(v any) Cond { return c.cond(PredGT, v) }

// GTE is column >= v, with v converted as EQ converts it.
func (c Named) GTE(v any) Cond { return c.cond(PredGTE, v) }

// LT is column < v, with v converted as EQ converts it.
func (c Named) LT(v any) Cond { return c.cond(PredLT, v) }

// LTE is column <= v, with v converted as EQ converts it.
func (c Named) LTE(v any) Cond { return c.cond(PredLTE, v) }

// In is column IN (vs...), each converted as EQ converts it. With no
// values it is false.
func (c Named) In(vs ...any) Cond { return c.cond(PredIn, nonNil(vs)) }

// NotIn is column NOT IN (vs...), each converted as EQ converts it. With
// no values it is true.
func (c Named) NotIn(vs ...any) Cond { return c.cond(PredNotIn, nonNil(vs)) }

// Contains is column LIKE %s%, with s's LIKE characters escaped. The column
// must be text.
func (c Named) Contains(s string) Cond { return c.cond(PredContains, s) }

// HasPrefix is column LIKE s%. The column must be text.
func (c Named) HasPrefix(s string) Cond { return c.cond(PredHasPrefix, s) }

// HasSuffix is column LIKE %s. The column must be text.
func (c Named) HasSuffix(s string) Cond { return c.cond(PredHasSuffix, s) }

// IsNull is column IS NULL. The column must be nullable.
func (c Named) IsNull() Cond { return c.cond(PredIsNull, nil) }

// NotNull is column IS NOT NULL. The column must be nullable.
func (c Named) NotNull() Cond { return c.cond(PredNotNull, nil) }

// nonNil is vs, or an empty slice for none.
func nonNil(vs []any) []any {
	if vs == nil {
		return []any{}
	}
	return vs
}

// Has is true when the relation holds at least one row. The target's
// interceptors and Read policy apply inside it, as they apply to a typed
// relation's Has.
func (e NamedEdge) Has() Cond { return e.HasWhere() }

// HasWhere is true when the relation holds at least one row that meets
// every condition of cs, which name the target's columns.
func (e NamedEdge) HasWhere(cs ...Cond) Cond {
	kids := make([]Node, len(cs))
	for i, c := range cs {
		kids[i] = c.n
	}
	return Cond{Node{Op: opNamedEdge, Col: int32(len(e.name)) << 8, W: nameData(e.name), Kids: kids}}
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
	where, err := resolveNamedPreds(st.Entity, st.Where)
	if err != nil {
		return err
	}
	st.Where = where
	if cf := st.Conflict; cf != nil && len(cf.Where) > 0 {
		cw, err := resolveNamedPreds(st.Entity, cf.Where)
		if err != nil {
			return err
		}
		if &cw[0] != &cf.Where[0] {
			c := *cf // the caller's clause may be shared with other runs
			c.Where = cw
			st.Conflict = &c
		}
	}
	return nil
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
// those Table.Pred makes and an Edge's EXISTS that of a typed Has, so the
// SQL and the render cache's key are a typed condition's.
func resolveNamed(in *EntityInfo, n Node) (Node, error) {
	switch n.Op {
	case opNamed:
		return namedLeaf(in, n)
	case opNamedEdge:
		return namedEdge(in, n)
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
				return n, fmt.Errorf("In or NotIn of nil on %s: use IsNull or NotNull", name)
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
	if r.target == nil {
		return n, fmt.Errorf("relation %s's target is not linked: velox-gen links it when the schema names it with velox.Edge", name)
	}
	l, err := r.Resolve(in, RelTarget{Get: r.target, One: r.typ, Many: r.typ})
	if err != nil {
		return n, err
	}
	kids, _, err := resolveKids(l.Target, n.Kids)
	if err != nil {
		return n, fmt.Errorf("relation %s: %w", name, err)
	}
	return Node{Op: OpExists, Col: int32(i), V: l, Kids: kids}, nil
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
