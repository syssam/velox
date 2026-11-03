package engine

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"unsafe"
)

// JoinSet is how a query reads its joins out of a row; it holds nothing generic,
// so that the query's type does not reach a joined entity's: BindOwner and the
// fills' closures are made by Join, which only a package that joins compiles.
type JoinSet struct {
	Fills []JoinFill
	// BindOwner points dst at the owner's own columns, cols as in Statement.Columns.
	BindOwner func(cols []int, dst []any, owner unsafe.Pointer)
}

// Scanner is the JoinSet as the engine reads rows with it: only the joins' columns,
// the entity's own being scanned by the engine's kit.
func (s *JoinSet) Scanner() *JoinScan {
	if s == nil {
		return nil
	}
	return &JoinScan{Width: s.Width(), Bind: func(dst []any) func(unsafe.Pointer) {
		attach := make([]func(unsafe.Pointer), len(s.Fills))
		off := 0
		for i := range s.Fills {
			attach[i] = s.Fills[i].Bind(dst[off : off+s.Fills[i].Width])
			off += s.Fills[i].Width
		}
		return func(owner unsafe.Pointer) {
			for _, f := range attach {
				f(owner)
			}
		}
	}}
}

// JoinFill reads one join's columns out of a row: the columns follow the
// entity's own, in the order of the query's joins.
type JoinFill struct {
	Width int // the target's column count
	// bind points dst, width long, at the fields of a new target, and returns
	// the function that, once the row is scanned, stores the target in the
	// owner's relation field.
	Bind func(dst []any) func(owner unsafe.Pointer)
}

// Width is how many columns the joins add to a row; 0 for no joins.
func (s *JoinSet) Width() int {
	if s == nil {
		return 0
	}
	n := 0
	for i := range s.Fills {
		n += s.Fills[i].Width
	}
	return n
}

// Scan scans the current row: the entity's own columns (rootN of them) into
// the owner, then the columns of each join into a new target stored in the
// owner's relation field.
func (s *JoinSet) Scan(r Rows, cols []int, rootN int, dst []any, owner unsafe.Pointer) error {
	s.BindOwner(cols, dst[:rootN], owner)
	attach := make([]func(unsafe.Pointer), len(s.Fills))
	off := rootN
	for i := range s.Fills {
		attach[i] = s.Fills[i].Bind(dst[off : off+s.Fills[i].Width])
		off += s.Fills[i].Width
	}
	if err := r.Scan(dst...); err != nil {
		return err
	}
	for _, f := range attach {
		f(owner)
	}
	return nil
}

// NullProbe scans a joined column of a left join, counting NULLs: a row whose
// columns are all NULL has no related entity. A NULL leaves the field as it
// was, and any other value is read as database/sql would.
type NullProbe struct {
	Dst   any
	Nulls *int
}

func (p *NullProbe) Scan(src any) error {
	if src == nil {
		*p.Nulls++
	}
	if s, ok := p.Dst.(sql.Scanner); ok {
		return s.Scan(src)
	}
	if src == nil {
		return nil
	}
	return sql.ConvertAssign(driver.ScanContext{}, p.Dst, src)
}

// NilScanner reads a column of nilColumns into its field, dst a pointer to it: a NULL
// is nil, and any other value converts as database/sql does.
type NilScanner struct{ Dst any }

func (s *NilScanner) Scan(src any) error {
	if src == nil {
		reflect.ValueOf(s.Dst).Elem().SetZero()
		return nil
	}
	return sql.ConvertAssign(driver.ScanContext{}, s.Dst, src)
}
