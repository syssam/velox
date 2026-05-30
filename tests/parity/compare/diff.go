package compare

import "velox.test/parity/model"

// Mismatch is one observed difference between two executors' results at a given
// op (and, for row-level differences, a given row). Field names a structural
// slot: a column name, or one of the synthetic markers "<error>", "<scalar>",
// "<rowcount>", "<page>", "<resultcount>", "<hasNext>", "<hasPrev>",
// "<startHandle>", "<endHandle>".
type Mismatch struct {
	OpIndex  int
	RowIndex int
	Field    string
	A, B     any
}

// Diff compares two result slices pairwise and returns every Mismatch. It is
// total over the allowed Value dynamic types — an unrecognized dynamic type
// triggers a panic (a loud harness failure) rather than a silent pass.
func Diff(a, b []model.Result) []Mismatch {
	var out []Mismatch
	if len(a) != len(b) {
		out = append(out, Mismatch{OpIndex: -1, RowIndex: -1, Field: "<resultcount>", A: len(a), B: len(b)})
		return out
	}
	for i := range a {
		out = append(out, diffResult(i, a[i], b[i])...)
	}
	return out
}

// diffResult compares one op's two Results.
func diffResult(idx int, a, b model.Result) []Mismatch {
	var out []Mismatch

	if a.Err != b.Err {
		out = append(out, Mismatch{OpIndex: idx, RowIndex: -1, Field: "<error>", A: a.Err, B: b.Err})
	}

	out = append(out, diffScalar(idx, a.Scalar, b.Scalar)...)
	out = append(out, diffPage(idx, a.Page, b.Page)...)
	out = append(out, diffRows(idx, a.Rows, b.Rows)...)

	return out
}

func diffScalar(idx int, a, b *int) []Mismatch {
	switch {
	case a == nil && b == nil:
		return nil
	case a == nil || b == nil:
		return []Mismatch{{OpIndex: idx, RowIndex: -1, Field: "<scalar>", A: derefInt(a), B: derefInt(b)}}
	case *a != *b:
		return []Mismatch{{OpIndex: idx, RowIndex: -1, Field: "<scalar>", A: *a, B: *b}}
	default:
		return nil
	}
}

func diffPage(idx int, a, b *model.PageInfo) []Mismatch {
	switch {
	case a == nil && b == nil:
		return nil
	case a == nil || b == nil:
		return []Mismatch{{OpIndex: idx, RowIndex: -1, Field: "<page>", A: a, B: b}}
	}
	var out []Mismatch
	if a.HasNext != b.HasNext {
		out = append(out, Mismatch{OpIndex: idx, RowIndex: -1, Field: "<hasNext>", A: a.HasNext, B: b.HasNext})
	}
	if a.HasPrev != b.HasPrev {
		out = append(out, Mismatch{OpIndex: idx, RowIndex: -1, Field: "<hasPrev>", A: a.HasPrev, B: b.HasPrev})
	}
	if !eqIntPtr(a.StartHandle, b.StartHandle) {
		out = append(out, Mismatch{OpIndex: idx, RowIndex: -1, Field: "<startHandle>", A: a.StartHandle, B: b.StartHandle})
	}
	if !eqIntPtr(a.EndHandle, b.EndHandle) {
		out = append(out, Mismatch{OpIndex: idx, RowIndex: -1, Field: "<endHandle>", A: a.EndHandle, B: b.EndHandle})
	}
	return out
}

func diffRows(idx int, a, b []model.Row) []Mismatch {
	if len(a) != len(b) {
		return []Mismatch{{OpIndex: idx, RowIndex: -1, Field: "<rowcount>", A: len(a), B: len(b)}}
	}
	var out []Mismatch
	for r := range a {
		out = append(out, diffRow(idx, r, a[r], b[r])...)
	}
	return out
}

// diffRow compares two rows over the union of their keys.
func diffRow(idx, rowIdx int, a, b model.Row) []Mismatch {
	var out []Mismatch
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	for k := range seen {
		av, aok := a[k]
		bv, bok := b[k]
		var avAny, bvAny any
		if aok {
			avAny = av
		}
		if bok {
			bvAny = bv
		}
		if !valueEqual(avAny, bvAny) {
			out = append(out, Mismatch{OpIndex: idx, RowIndex: rowIdx, Field: k, A: avAny, B: bvAny})
		}
	}
	return out
}

// valueEqual is a TOTAL comparison over the allowed Value dynamic types: nil,
// int, string, bool, []string, model.Ref. Any other dynamic type on EITHER side
// is a harness contract violation and panics loudly (never a silent pass). Mixed
// allowed types (a is int, b is string) are unequal, not a panic.
func valueEqual(a, b any) bool {
	// Validate both sides loudly: an unrecognized dynamic type anywhere in the
	// results means the harness produced a value it cannot compare.
	assertAllowed(a)
	assertAllowed(b)
	switch av := a.(type) {
	case nil:
		return b == nil
	case int:
		bv, ok := b.(int)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case []string:
		bv, ok := b.([]string)
		if !ok {
			return false
		}
		return stringsEqual(av, bv)
	case model.Ref:
		bv, ok := b.(model.Ref)
		return ok && av == bv
	default:
		// Unreachable: assertAllowed already panicked.
		panic("compare: unhandled value type")
	}
}

// assertAllowed panics if v is not one of the allowed Value dynamic types.
func assertAllowed(v any) {
	switch v.(type) {
	case nil, int, string, bool, []string, model.Ref:
		return
	default:
		panic("compare: unhandled value type")
	}
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func eqIntPtr(a, b *int) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

func derefInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
