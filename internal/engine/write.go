package engine

import (
	"fmt"
	"reflect"
	"slices"
)

// DryRefused reports whether err, from the hooks of a statement under SQL(ctx),
// is a write a hook made that was refused with ErrDryRun, after the statement
// itself was rendered: SQL(ctx) then returns the statement, as the hook, not
// the dry run, is what asked for a write. A hook that wrote before next, and
// so before anything was rendered, gets the error, and so does one whose
// error joins another to ErrDryRun.
func DryRefused(mu *MutationState, err error) bool {
	return mu.Dry && mu.SQL != "" && onlyDryRun(err)
}

// onlyDryRun reports whether err is ErrDryRun, wrapped or joined, on every
// branch of its tree: errors.Join(ErrDryRun, other) is not.
func onlyDryRun(err error) bool {
	switch x := err.(type) {
	case nil:
		return false
	case interface{ Unwrap() []error }:
		errs := x.Unwrap()
		for _, e := range errs {
			if !onlyDryRun(e) {
				return false
			}
		}
		return len(errs) > 0
	case interface{ Unwrap() error }:
		return err == ErrDryRun || onlyDryRun(x.Unwrap())
	}
	return err == ErrDryRun
}

// BidiWhere is the condition that ps, a bidirectional join entity's
// conditions, hold for a row or for its mirror: Or(And(ps…), And(swap(ps)…)),
// or Or(p, swap(p)) for one p.
func BidiWhere(ps []Predicate) Predicate {
	if len(ps) == 1 {
		return Predicate{Group(OpOr, []Node{ps[0].n, swapCols(ps[0].n)})}
	}
	as, bs := make([]Node, len(ps)), make([]Node, len(ps))
	for i, p := range ps {
		as[i], bs[i] = p.n, swapCols(p.n)
	}
	return Predicate{Group(OpOr, []Node{Group(OpAnd, as), Group(OpAnd, bs)})}
}

// swapCols returns a copy of n, a condition on a bidirectional join entity,
// with its columns 0 and 1 exchanged, at every depth of And, Or and Not. The
// kids of an EXISTS (OpExists, OpThrough) hold another entity's columns: such
// a node is kept as it is.
func swapCols(n Node) Node {
	switch n.Op {
	case OpAnd, OpOr, OpNot:
		kids := make([]Node, len(n.Kids))
		for i, k := range n.Kids {
			kids[i] = swapCols(k)
		}
		m := n
		m.Kids = kids
		return m
	case OpExists, OpThrough, OpAllow, OpInvalid:
		return n
	}
	m := n
	switch n.Col {
	case 0:
		m.Col = 1
	case 1:
		m.Col = 0
	}
	return m
}

// CheckAssignedOnce refuses a column assigned twice: PostgreSQL rejects it
// while MySQL and SQLite accept it, so velox refuses it everywhere.
func CheckAssignedOnce(info *EntityInfo, set []Assignment) error {
	seen := make(map[int]bool, len(set))
	for _, a := range set {
		if seen[a.Column] {
			return fmt.Errorf("velox: %s: column %s is assigned twice", info.Name, info.Columns[a.Column].Name)
		}
		seen[a.Column] = true
	}
	return nil
}

// WithUpdateDefaults appends an assignment for every column with an
// UpdateDefault that set does not assign.
func WithUpdateDefaults(info *EntityInfo, set []Assignment) []Assignment {
	named := make(map[int]bool, len(set))
	for _, a := range set {
		named[a.Column] = true
	}
	out := slices.Clip(set)
	for i := range info.Columns {
		if f := info.Columns[i].UpdateDefault; f != nil && !named[i] {
			out = append(out, Assignment{Column: i, Op: AssignSet, Value: f()})
		}
	}
	return out
}

// SetElem stores v where p points: nil as the zero value, a value of the
// pointed-to type of a pointer field through a new pointer, and nothing when
// the field cannot hold v (a Rewrite hook may assign any value). Like elem it
// is not generic, so that the reflect.Value it uses is not compiled into every
// package that writes.
func SetElem(p, v any) {
	dst := reflect.ValueOf(p).Elem()
	if v == nil {
		dst.SetZero()
		return
	}
	val := reflect.ValueOf(v)
	switch t := dst.Type(); {
	case val.Type().AssignableTo(t):
		dst.Set(val)
	case t.Kind() == reflect.Pointer && val.Type().AssignableTo(t.Elem()):
		ptr := reflect.New(t.Elem())
		ptr.Elem().Set(val)
		dst.Set(ptr)
	}
}
