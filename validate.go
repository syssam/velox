package velox

import (
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// Validate runs the validators of the columns cs of x on the values x holds
// now, before defaults are filled and before any hook, and before any
// statement: velox-graphql's CreateX checks the fields its input sets this way,
// so that an invalid one costs no query. For generated code. The checks and the
// error are Insert's (see ValidationError); Insert validates again after its
// defaults and hooks.
func Validate[E any, PE Entity[E]](x PE, cs ...Column[E]) error {
	m := stateOf(x.VeloxMeta())
	if m.Err != nil {
		return m.Err
	}
	cols := make([]int, len(cs))
	for i, c := range cs {
		cols[i] = int(c.column(nil))
	}
	return engine.ValidateRow(m.Info, unsafe.Pointer(x), cols)
}

// ValidateSet runs the validators of the Set values of set, and refuses an
// Immutable column among them, on the values as given, before any hook or
// UpdateDefault and before any statement: velox-graphql's UpdateX checks the
// fields its input sets this way. For generated code. The checks and the error
// are Update's (see ValidationError); Update validates again after its hooks.
// A SetPtr given a nil pointer is passed over, an Immutable column's
// included: it writes nothing.
func ValidateSet[E any, PE Entity[E]](set ...Setter[E]) error {
	m := stateOf(PE(nil).VeloxMeta())
	if m.Err != nil {
		return m.Err
	}
	as := make([]Assignment, len(set))
	for i, s := range set {
		as[i] = s.a
	}
	return engine.ValidateSetters(m.Info, as)
}
