package velox

import (
	"testing"
	"time"

	"github.com/syssam/velox/internal/engine"
)

// TestSetPtrOnEveryHandle: every handle kind with Set has SetPtr. A nil
// pointer gives a setter the builders skip, of the column's index; a
// non-nil one gives Set of what it points to, a Null kind's included.
func TestSetPtrOnEveryHandle(t *testing.T) {
	b, n, s, tm, js := true, int64(4), "s", t1, []string{"a"}
	skip := Assignment{Column: 1, Op: engine.AssignSkip}
	cases := []handleCase{
		{"Col nil", Col[ent, bool]{I: 1}.SetPtr(nil).a, skip},
		{"Col", Col[ent, bool]{I: 1}.SetPtr(&b).a, Assignment{Column: 1, Op: AssignSet, Value: b}},
		{"TimeCol nil", TimeCol[ent, time.Time]{I: 1}.SetPtr(nil).a, skip},
		{"TimeCol", TimeCol[ent, time.Time]{I: 1}.SetPtr(&tm).a, Assignment{Column: 1, Op: AssignSet, Value: tm}},
		{"OtherCol nil", OtherCol[ent, rdec]{I: 1}.SetPtr(nil).a, skip},
		{"NumberCol nil", NumberCol[ent, int64]{I: 1}.SetPtr(nil).a, skip},
		{"NumberCol", NumberCol[ent, int64]{I: 1}.SetPtr(&n).a, Assignment{Column: 1, Op: AssignSet, Value: n}},
		{"StringCol nil", StringCol[ent, string]{I: 1}.SetPtr(nil).a, skip},
		{"StringCol", StringCol[ent, string]{I: 1}.SetPtr(&s).a, Assignment{Column: 1, Op: AssignSet, Value: s}},
		{"NullCol nil", NullCol[ent, bool]{I: 1}.SetPtr(nil).a, skip},
		{"NullCol", NullCol[ent, bool]{I: 1}.SetPtr(&b).a, Assignment{Column: 1, Op: AssignSet, Value: b}},
		{"NullTimeCol nil", NullTimeCol[ent, time.Time]{I: 1}.SetPtr(nil).a, skip},
		{"NullOtherCol nil", NullOtherCol[ent, rdec]{I: 1}.SetPtr(nil).a, skip},
		{"NullNumberCol nil", NullNumberCol[ent, int64]{I: 1}.SetPtr(nil).a, skip},
		{"NullNumberCol", NullNumberCol[ent, int64]{I: 1}.SetPtr(&n).a, Assignment{Column: 1, Op: AssignSet, Value: n}},
		{"NullStringCol nil", NullStringCol[ent, string]{I: 1}.SetPtr(nil).a, skip},
		{"JSONCol nil", JSONCol[ent, []string]{I: 1}.SetPtr(nil).a, skip},
		{"JSONCol", JSONCol[ent, []string]{I: 1}.SetPtr(&js).a, Assignment{Column: 1, Op: AssignSet, Value: js}},
		{"NullJSONCol nil", NullJSONCol[ent, []string]{I: 1}.SetPtr(nil).a, skip},
		{"NullJSONCol", NullJSONCol[ent, []string]{I: 1}.SetPtr(&js).a, Assignment{Column: 1, Op: AssignSet, Value: js}},
		{"SetNull still writes NULL", NullCol[ent, bool]{I: 1}.SetNull().a, Assignment{Column: 1, Op: AssignSet}},
		{"Column of a skipped setter", NumberCol[ent, int64]{I: 1}.SetPtr(nil).Column(), 1},
	}
	checkCases(t, cases)
}

// TestSetPtrCopiesTheValue: SetPtr reads *p when it is called; a later
// change to *p does not reach the setter.
func TestSetPtrCopiesTheValue(t *testing.T) {
	n := int64(1)
	s := NumberCol[ent, int64]{I: 1}.SetPtr(&n)
	n = 2
	if s.a.Value != int64(1) {
		t.Errorf("value %v, want 1", s.a.Value)
	}
}
