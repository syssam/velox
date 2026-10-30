package engine

import (
	"slices"
)

// EachPlan is what LoadEach and CountEach share: the relation, the owners'
// distinct keys and each owner's (nil for an owner whose key is NULL), and
// the statement every chunk starts from, with q's conditions, After's and
// r's.
type EachPlan struct {
	L        Link
	Keys     []any
	OwnerKey []any
	St       Statement
}

// EachRefused names what of st LoadEach and CountEach refuse, or is "".
func EachRefused(st *Statement) string {
	switch {
	case st.Limit > 0:
		return "Limit; Rel.LimitEach limits each owner's rows"
	case st.Offset > 0:
		return "Offset; page each owner's rows with After"
	case st.Lock == LockForUpdate:
		return "ForUpdate; a per-owner read locks nothing"
	case st.Lock == LockForShare:
		return "ForShare; a per-owner read locks nothing"
	case st.LockWait == LockSkipLocked:
		return "SkipLocked; a per-owner read locks nothing"
	case st.LockWait == LockNoWait:
		return "NoWait; a per-owner read locks nothing"
	}
	return ""
}

// MakeEachClause is the Each of a per-owner statement of l with per-owner limit
// n, without its keys.
func MakeEachClause(l Link, n int) EachClause {
	if l.Join != nil {
		return EachClause{Join: l.Join, JoinOwner: l.JoinOwner, JoinTarget: l.JoinTarget, Target: l.TargetCol, Limit: n}
	}
	return EachClause{Column: l.TargetCol, Limit: n}
}

// Chunk is st with Each ec holding keys, for one statement of a chunk; its
// slices are clipped, so that nothing appended to one chunk's reaches
// another's.
func Chunk(st Statement, ec EachClause, keys []any) Statement {
	ec.Keys = keys
	st.Each = &ec
	st.Where, st.OrderBy, st.Columns = slices.Clip(st.Where), slices.Clip(st.OrderBy), slices.Clip(st.Columns)
	return st
}
