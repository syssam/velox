package engine

import (
	"encoding/binary"
	"fmt"
	"reflect"
	"slices"
)

// On PostgreSQL a projection's GROUP BY or ORDER BY term that repeats an
// earlier term with bound values is written with the earlier term's
// placeholders (decision 0128). PostgreSQL matches a grouped expression by
// its text, and $1 and $3 are two parameters to it, so a date bucket
// selected as date_trunc('month', x AT TIME ZONE $1) and grouped as
// date_trunc('month', x AT TIME ZONE $2) failed with 42803; written with $1
// both times, it is one expression, and PostgreSQL sends the value once.
//
// A term repeats an earlier one when sameTerm says so: the same text and
// columns and equal values, whatever its name. The earlier ones are the
// result terms, then, for an ordering, the GROUP BY keys, a Rollup's or
// Cube's keys among them. MySQL's and SQLite's ? are positional and cannot
// be repeated, so they bind the value at each place, as before. The render
// cache keys which earlier term each one repeats (repeatKey), since equal
// values decide it and the key holds no values.

// termSpan is a term the writer wrote on PostgreSQL and the arguments it
// bound: args[start:start+n].
type termSpan struct {
	t        Term
	start, n int
}

// repeated returns the index of the first term t repeats among p's result
// terms and its first groups GROUP BY keys (groupKeys' order), counted in
// that order, or -1.
func repeated(p *Projection, groups int, t Term) int {
	if !termArgs(t) {
		return -1
	}
	for i, e := range p.Terms {
		if sameTerm(e, t) {
			return i
		}
	}
	found := -1
	groupKeys(p, func(i int, e Term) bool {
		if i >= groups {
			return false
		}
		if sameTerm(e, t) {
			found = len(p.Terms) + i
			return false
		}
		return true
	})
	return found
}

// groupKeys calls f with each GROUP BY key of p and its index, a Rollup's
// or Cube's keys in its place, until f returns false.
func groupKeys(p *Projection, f func(int, Term) bool) {
	n := 0
	for _, g := range p.GroupBy {
		set := []Term{g}
		if g.kind == termGrouping {
			set = g.ext.set
		}
		for _, k := range set {
			if !f(n, k) {
				return
			}
			n++
		}
	}
}

// writeSpan writes t, as a term or, when result, as a result column, and
// records its arguments; when it repeats an earlier span it is written with
// that span's placeholders and binds nothing.
func (w *writer) writeSpan(t Term, result, distinct bool) {
	if w.d != Postgres {
		if result {
			w.resultTerm(t, distinct)
		} else {
			w.term(t)
		}
		return
	}
	if !result && w.repeatSpan(t) {
		return
	}
	start := len(w.args)
	if result {
		w.resultTerm(t, distinct)
	} else {
		w.term(t)
	}
	w.spans = append(w.spans, termSpan{t: t, start: start, n: len(w.args) - start})
}

// repeatSpan writes t with the placeholders of the first span it repeats,
// and reports whether there was one.
func (w *writer) repeatSpan(t Term) bool {
	if !termArgs(t) {
		return false
	}
	for _, s := range w.spans {
		if sameTerm(s.t, t) {
			w.reuse = s.start + 1
			w.term(unnamed(t))
			w.reuse = 0
			return true
		}
	}
	return false
}

// repeatKey appends to a PostgreSQL projection's key, for each GROUP BY
// term and each ordering by a term, 1 plus the index of the earlier term it
// repeats among the result terms and the GROUP BY terms, or 0.
func repeatKey(k []byte, p *Projection, order []OrderTerm) []byte {
	n := 0
	groupKeys(p, func(i int, g Term) bool {
		k = binary.AppendUvarint(k, uint64(repeated(p, i, g)+1))
		n = i + 1
		return true
	})
	for _, o := range order {
		if t, ok := o.Expr.(Term); ok {
			k = binary.AppendUvarint(k, uint64(repeated(p, n, t)+1))
		}
	}
	return k
}

// termArgs reports whether t binds a value: an expression's argument, or a
// JSON path's key.
func termArgs(t Term) bool {
	switch t.kind {
	case termExpr:
		if len(t.x.args) > 0 {
			return true
		}
		for _, s := range t.x.slots {
			if s.Term != nil && termArgs(*s.Term) {
				return true
			}
		}
	case termAgg, termDate:
		return t.of != nil && termArgs(*t.of)
	case termJSON:
		return len(t.path.Keys) > 0
	}
	return false
}

// SameTerm reports whether a and b are written alike and bind equal values,
// their names aside, as sameTerm says: Term.Equal in the root.
func SameTerm(a, b Term) bool { return sameTerm(a, b) }

// sameTerm reports whether a and b are written alike and bind equal values,
// their names aside. A relation's aggregate is never the same: GROUP BY
// refuses one, and its conditions are not compared.
func sameTerm(a, b Term) bool {
	if a.kind != b.kind || a.fn != b.fn || a.col != b.col || a.dec != b.dec {
		return false
	}
	switch a.kind {
	case termColumn, termExcluded:
		return true
	case termExpr:
		return sameExpr(a.x, b.x)
	case termAgg:
		if a.of == nil || b.of == nil {
			return a.of == b.of
		}
		return sameTerm(*a.of, *b.of)
	case termJSON:
		return a.path.Kind == b.path.Kind && slices.Equal(a.path.Keys, b.path.Keys)
	case termDate:
		x, y := a.ext, b.ext
		return x.unit == y.unit && x.zone == y.zone && x.offset == y.offset && x.fiscal == y.fiscal && sameTerm(*a.of, *b.of)
	}
	return false
}

// sameExpr reports whether the expressions a and b have the same text,
// slots and equal values.
func sameExpr(a, b *exprData) bool {
	if a == b {
		return true
	}
	if !slices.Equal(a.parts, b.parts) || len(a.slots) != len(b.slots) || len(a.args) != len(b.args) {
		return false
	}
	for i, s := range a.slots {
		o := b.slots[i]
		switch {
		case (s.Term == nil) != (o.Term == nil):
			return false
		case s.Term != nil:
			if !sameTerm(*s.Term, *o.Term) {
				return false
			}
		case s.Col != o.Col:
			return false
		}
	}
	for i, v := range a.args {
		if !sameValue(v, b.args[i]) {
			return false
		}
	}
	return true
}

// sameValue reports whether a and b are of one type and equal by ==. A value
// == cannot compare, such as a slice, is never the same as another.
func sameValue(a, b any) bool {
	if reflect.TypeOf(a) != reflect.TypeOf(b) {
		return false
	}
	if a == nil {
		return true
	}
	if !reflect.ValueOf(a).Comparable() || !reflect.ValueOf(b).Comparable() {
		return false
	}
	return a == b
}

// On MySQL a projection's result or ordering term with a bound value that
// repeats a GROUP BY key is written as ANY_VALUE of it (decision 0129).
// MySQL's ? cannot be repeated, so the copy binds its own parameter, and
// only_full_group_by, which matches a grouped expression as it is
// written, refused it with error 1055. Every row of a group has the key's
// value, so ANY_VALUE of the key is that value. Under WITH ROLLUP a
// subtotal row's key is NULL only where it is written as GROUP BY writes
// it; ANY_VALUE of a copy could be a row's value there, so such a statement
// fails. The render cache keys which terms are wrapped (keyRepeatKey).

// anyKey reports whether t, a result or ordering term of the grouped MySQL
// projection being written, is to be written as ANY_VALUE of it. Under a
// Rollup it fails the statement in place.
func (w *writer) anyKey(t Term) bool {
	if w.keys == nil || !keyRepeat(w.keys, t) {
		return false
	}
	if slices.ContainsFunc(w.keys.GroupBy, func(g Term) bool { return g.kind == termGrouping }) {
		w.fail(fmt.Errorf("velox: %s: on MySQL a term with a bound value cannot repeat a key of WITH ROLLUP in the select list or an ordering, as its ? is another parameter; write the value into the text, or use PostgreSQL", w.in.Name))
		return false
	}
	return true
}

// anyValue writes ANY_VALUE(t).
func (w *writer) anyValue(t Term) {
	w.str("ANY_VALUE(")
	w.term(t)
	w.str(")")
}

// keyRepeat reports whether t binds a value and is the same as a GROUP BY
// key of p, a Rollup's keys among them.
func keyRepeat(p *Projection, t Term) bool {
	if !termArgs(t) {
		return false
	}
	found := false
	groupKeys(p, func(_ int, k Term) bool {
		found = sameTerm(k, t)
		return !found
	})
	return found
}

// keyRepeatKey appends to a grouped MySQL projection's key, for each result
// term and each ordering by a term, whether it repeats a GROUP BY key.
func keyRepeatKey(k []byte, p *Projection, order []OrderTerm) []byte {
	if len(p.GroupBy) == 0 {
		return k
	}
	for _, t := range p.Terms {
		k = append(k, bit(keyRepeat(p, t)))
	}
	for _, o := range order {
		if t, ok := o.Expr.(Term); ok {
			k = append(k, bit(keyRepeat(p, t)))
		}
	}
	return k
}

// A HAVING term that repeats a GROUP BY key, outside any aggregate in it,
// is written as the key is (decision 0138). On PostgreSQL a key with a
// bound value is written with the key's placeholders, as an ordering is
// (decision 0128): with its own, it is another expression, refused with
// 42803. MySQL's HAVING names no column outside an aggregate unless it is
// a key column, so an expression key there failed with error 1054, bound
// or not, and ANY_VALUE does not lift that; the term is written as MIN of
// it, which over a group's rows is the key's value. Under WITH ROLLUP a
// subtotal row's key is NULL and MIN of a copy is not, so such a statement
// fails. The render cache keys what each term repeats (havingKey).
//
// On MySQL a DISTINCT projection's ordering by a term that is one of its
// result terms, not a column, is written as ANY_VALUE of it, in its
// IS NULL and in itself. MySQL refuses an ordering it does not find in the
// select list with error 3065, or 3066 for an aggregate: a copy with its
// own ? is not found, and neither is x IS NULL of an expression. The row's
// value of a result term is the value of each copy of it.

// havingTerm writes t, a term of a HAVING outside any aggregate, as the
// GROUP BY key it repeats is written, and reports whether it did.
func (w *writer) havingTerm(t Term) bool {
	switch w.d {
	case Postgres:
		if !termArgs(t) {
			return false
		}
		w.having = false
		ok := w.repeatSpan(t)
		w.having = true
		return ok
	case MySQL:
		if w.keys == nil || !havingKeyRepeat(w.keys, t) {
			return false
		}
		if slices.ContainsFunc(w.keys.GroupBy, func(g Term) bool { return g.kind == termGrouping }) {
			w.fail(fmt.Errorf("velox: %s: on MySQL HAVING cannot test an expression key of WITH ROLLUP outside an aggregate; test it in Where, or use PostgreSQL", w.in.Name))
			return false
		}
		w.having = false
		w.str("MIN(")
		w.term(t)
		w.str(")")
		w.having = true
		return true
	}
	return false
}

// havingKeyRepeat reports whether t, a term of a grouped MySQL
// projection's HAVING, is a GROUP BY key of p that is not a column.
func havingKeyRepeat(p *Projection, t Term) bool {
	if t.kind == termColumn {
		return false
	}
	found := false
	groupKeys(p, func(_ int, k Term) bool {
		found = sameTerm(k, t)
		return !found
	})
	return found
}

// distinctRepeat reports whether t, an ordering term of a DISTINCT MySQL
// projection p, is one of its result terms, as the guard finds it
// (resultTerm), and not a column.
func distinctRepeat(p *Projection, t Term) bool {
	return t.kind != termColumn && resultTerm(p, t)
}

// havingTerms calls f with each term of p's HAVING that the writer checks
// (havingTerm): each Expr's terms, and the terms inside them, but not
// inside an aggregate or a relation's term, in the order written.
func havingTerms(p *Projection, f func(Term)) {
	var term func(Term)
	term = func(t Term) {
		f(t)
		switch t.kind {
		case termExpr:
			for _, s := range t.x.slots {
				if s.Term != nil {
					term(*s.Term)
				}
			}
		case termDate:
			term(*t.of)
		}
	}
	var node func(Node)
	node = func(n Node) {
		if n.Op == OpExpr {
			for _, s := range n.V.(*exprData).slots {
				if s.Term != nil {
					term(*s.Term)
				}
			}
		}
		for _, k := range n.Kids {
			node(k)
		}
	}
	for _, h := range p.Having {
		node(h.n)
	}
}

// havingKey appends to a projection's key what its HAVING terms repeat:
// on PostgreSQL 1 plus the index of the result term or GROUP BY key each
// repeats (repeated), or 0; on MySQL whether each is a key (a term not
// written still adds its part, which keeps the key exact). A DISTINCT
// MySQL projection adds, for each ordering by a term, whether it is a
// result term.
func havingKey(k []byte, d Dialect, p *Projection, distinct bool, order []OrderTerm) []byte {
	switch d {
	case Postgres:
		groups := 0
		groupKeys(p, func(int, Term) bool { groups++; return true })
		havingTerms(p, func(t Term) { k = binary.AppendUvarint(k, uint64(repeated(p, groups, t)+1)) })
	case MySQL:
		if len(p.GroupBy) > 0 {
			havingTerms(p, func(t Term) { k = append(k, bit(havingKeyRepeat(p, t))) })
		}
		if distinct {
			for _, o := range order {
				if t, ok := o.Expr.(Term); ok {
					k = append(k, bit(distinctRepeat(p, t)))
				}
			}
		}
	}
	return k
}
