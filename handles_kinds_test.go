package velox

import (
	"reflect"
	"testing"
	"time"

	"github.com/syssam/velox/internal/engine"
)

// rdec stands for a decimal type: a field.Other column of it has an OtherCol.
type rdec string

var (
	t1 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	t2 = t1.Add(time.Hour)
)

// handleCase is one handle method's result and what the engine should read.
type handleCase struct {
	name      string
	got, want any
}

func checkCases(t *testing.T, cases []handleCase) {
	t.Helper()
	for _, c := range cases {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, c.got, c.want)
		}
	}
}

// pn is the node of p.
func pn[E any](p Pred[E]) engine.Node { return engine.PredNode(Predicate(p)) }

// refCols is the owner and target columns of an InSelect's EXISTS node;
// -9 for both when n is no such node.
func refCols(n engine.Node) [2]int32 {
	ref, ok := n.V.(engine.ColRef)
	if n.Op != engine.OpExists || !ok || ref.Owner == nil || ref.Target == nil {
		return [2]int32{-9, -9}
	}
	return [2]int32{ref.OwnerCol, ref.TargetCol}
}

// notKid is the one condition under a Not node; the zero node otherwise.
func notKid(n engine.Node) engine.Node {
	if n.Op != engine.OpNot || len(n.Kids) != 1 {
		return engine.Node{}
	}
	return n.Kids[0]
}

// TestTimeColHandle: every method of TimeCol builds the node, term, order or setter
// the engine reads for column 1.
func TestTimeColHandle(t *testing.T) {
	h := TimeCol[ent, time.Time]{I: 1}
	a, b := t1, t2
	cases := []handleCase{
		{"EQ", pn(h.EQ(a)), engine.Node{Op: engine.OpEQ, Col: 1, V: a}},
		{"NEQ", pn(h.NEQ(a)), engine.Node{Op: engine.OpNEQ, Col: 1, V: a}},
		{"In", pn(h.In(a, b)), engine.Node{Op: engine.OpIn, Col: 1, V: []any{a, b}}},
		{"NotIn", pn(h.NotIn(a, b)), engine.Node{Op: engine.OpNotIn, Col: 1, V: []any{a, b}}},
		{"GT", pn(h.GT(a)), engine.Node{Op: engine.OpGT, Col: 1, V: a}},
		{"GTE", pn(h.GTE(a)), engine.Node{Op: engine.OpGTE, Col: 1, V: a}},
		{"LT", pn(h.LT(a)), engine.Node{Op: engine.OpLT, Col: 1, V: a}},
		{"LTE", pn(h.LTE(a)), engine.Node{Op: engine.OpLTE, Col: 1, V: a}},
		{"Between", pn(h.Between(a, b)), engine.Node{Op: engine.OpBetween, Col: 1, V: a, W: b}},
		{"Asc", h.Asc().t, OrderTerm{Column: 1}},
		{"Desc", h.Desc().t, OrderTerm{Column: 1, Desc: true}},
		{"Count", h.Count().t, engine.AggTerm(engine.TermCount, engine.ColumnTerm(1))},
		{"CountDistinct", h.CountDistinct().t, engine.AggTerm(engine.TermCountDistinct, engine.ColumnTerm(1))},
		{"Min", h.Min().t, engine.AggTerm(engine.TermMin, engine.ColumnTerm(1))},
		{"Max", h.Max().t, engine.AggTerm(engine.TermMax, engine.ColumnTerm(1))},
		{"Set", h.Set(a).a, Assignment{Column: 1, Op: AssignSet, Value: a}},
		{"Key", h.Key().i, int32(1)},
		{"Select", h.Select().col, int32(1)},
		{"InSelect", refCols(pn(h.InSelect(h.Select()))), [2]int32{1, 1}},
		{"NotInSelect", refCols(notKid(pn(h.NotInSelect(h.Select())))), [2]int32{1, 1}},
	}
	checkCases(t, cases)
}

// TestOtherColHandle: every method of OtherCol builds the node, term, order or setter
// the engine reads for column 3.
func TestOtherColHandle(t *testing.T) {
	h := OtherCol[ent, rdec]{I: 3}
	a, b := rdec("1.5"), rdec("2.5")
	cases := []handleCase{
		{"EQ", pn(h.EQ(a)), engine.Node{Op: engine.OpEQ, Col: 3, V: a}},
		{"NEQ", pn(h.NEQ(a)), engine.Node{Op: engine.OpNEQ, Col: 3, V: a}},
		{"In", pn(h.In(a, b)), engine.Node{Op: engine.OpIn, Col: 3, V: []any{a, b}}},
		{"NotIn", pn(h.NotIn(a, b)), engine.Node{Op: engine.OpNotIn, Col: 3, V: []any{a, b}}},
		{"GT", pn(h.GT(a)), engine.Node{Op: engine.OpGT, Col: 3, V: a}},
		{"GTE", pn(h.GTE(a)), engine.Node{Op: engine.OpGTE, Col: 3, V: a}},
		{"LT", pn(h.LT(a)), engine.Node{Op: engine.OpLT, Col: 3, V: a}},
		{"LTE", pn(h.LTE(a)), engine.Node{Op: engine.OpLTE, Col: 3, V: a}},
		{"Between", pn(h.Between(a, b)), engine.Node{Op: engine.OpBetween, Col: 3, V: a, W: b}},
		{"Asc", h.Asc().t, OrderTerm{Column: 3}},
		{"Desc", h.Desc().t, OrderTerm{Column: 3, Desc: true}},
		{"Count", h.Count().t, engine.AggTerm(engine.TermCount, engine.ColumnTerm(3))},
		{"CountDistinct", h.CountDistinct().t, engine.AggTerm(engine.TermCountDistinct, engine.ColumnTerm(3))},
		{"Min", h.Min().t, engine.AggTerm(engine.TermMin, engine.ColumnTerm(3))},
		{"Max", h.Max().t, engine.AggTerm(engine.TermMax, engine.ColumnTerm(3))},
		{"Set", h.Set(a).a, Assignment{Column: 3, Op: AssignSet, Value: a}},
		{"Key", h.Key().i, int32(3)},
		{"Select", h.Select().col, int32(3)},
		{"InSelect", refCols(pn(h.InSelect(h.Select()))), [2]int32{3, 3}},
		{"NotInSelect", refCols(notKid(pn(h.NotInSelect(h.Select())))), [2]int32{3, 3}},
	}
	checkCases(t, cases)
}

// TestNullTimeColHandle: every method of NullTimeCol builds the node, term, order or setter
// the engine reads for column 2.
func TestNullTimeColHandle(t *testing.T) {
	h := NullTimeCol[ent, time.Time]{I: 2}
	a, b := t1, t2
	cases := []handleCase{
		{"EQ", pn(h.EQ(a)), engine.Node{Op: engine.OpEQ, Col: 2, V: a}},
		{"NEQ", pn(h.NEQ(a)), engine.Node{Op: engine.OpNEQ, Col: 2, V: a}},
		{"In", pn(h.In(a, b)), engine.Node{Op: engine.OpIn, Col: 2, V: []any{a, b}}},
		{"NotIn", pn(h.NotIn(a, b)), engine.Node{Op: engine.OpNotIn, Col: 2, V: []any{a, b}}},
		{"GT", pn(h.GT(a)), engine.Node{Op: engine.OpGT, Col: 2, V: a}},
		{"GTE", pn(h.GTE(a)), engine.Node{Op: engine.OpGTE, Col: 2, V: a}},
		{"LT", pn(h.LT(a)), engine.Node{Op: engine.OpLT, Col: 2, V: a}},
		{"LTE", pn(h.LTE(a)), engine.Node{Op: engine.OpLTE, Col: 2, V: a}},
		{"Between", pn(h.Between(a, b)), engine.Node{Op: engine.OpBetween, Col: 2, V: a, W: b}},
		{"Asc", h.Asc().t, OrderTerm{Column: 2}},
		{"Desc", h.Desc().t, OrderTerm{Column: 2, Desc: true}},
		{"Count", h.Count().t, engine.AggTerm(engine.TermCount, engine.ColumnTerm(2))},
		{"CountDistinct", h.CountDistinct().t, engine.AggTerm(engine.TermCountDistinct, engine.ColumnTerm(2))},
		{"Min", h.Min().t, engine.AggTerm(engine.TermMin, engine.ColumnTerm(2))},
		{"Max", h.Max().t, engine.AggTerm(engine.TermMax, engine.ColumnTerm(2))},
		{"Set", h.Set(a).a, Assignment{Column: 2, Op: AssignSet, Value: a}},
		{"Key", h.Key().i, int32(2)},
		{"Select", h.Select().col, int32(2)},
		{"InSelect", refCols(pn(h.InSelect(h.Select()))), [2]int32{2, 2}},
		{"NotInSelect", refCols(notKid(pn(h.NotInSelect(h.Select())))), [2]int32{2, 2}},
		{"SetNull", h.SetNull().a, Assignment{Column: 2, Op: AssignSet}},
		{"IsNull", pn(h.IsNull()), engine.Node{Op: engine.OpIsNull, Col: 2}},
		{"NotNull", pn(h.NotNull()), engine.Node{Op: engine.OpNotNull, Col: 2}},
	}
	checkCases(t, cases)
}

// TestNullOtherColHandle: every method of NullOtherCol builds the node, term, order or setter
// the engine reads for column 4.
func TestNullOtherColHandle(t *testing.T) {
	h := NullOtherCol[ent, rdec]{I: 4}
	a, b := rdec("1.5"), rdec("2.5")
	cases := []handleCase{
		{"EQ", pn(h.EQ(a)), engine.Node{Op: engine.OpEQ, Col: 4, V: a}},
		{"NEQ", pn(h.NEQ(a)), engine.Node{Op: engine.OpNEQ, Col: 4, V: a}},
		{"In", pn(h.In(a, b)), engine.Node{Op: engine.OpIn, Col: 4, V: []any{a, b}}},
		{"NotIn", pn(h.NotIn(a, b)), engine.Node{Op: engine.OpNotIn, Col: 4, V: []any{a, b}}},
		{"GT", pn(h.GT(a)), engine.Node{Op: engine.OpGT, Col: 4, V: a}},
		{"GTE", pn(h.GTE(a)), engine.Node{Op: engine.OpGTE, Col: 4, V: a}},
		{"LT", pn(h.LT(a)), engine.Node{Op: engine.OpLT, Col: 4, V: a}},
		{"LTE", pn(h.LTE(a)), engine.Node{Op: engine.OpLTE, Col: 4, V: a}},
		{"Between", pn(h.Between(a, b)), engine.Node{Op: engine.OpBetween, Col: 4, V: a, W: b}},
		{"Asc", h.Asc().t, OrderTerm{Column: 4}},
		{"Desc", h.Desc().t, OrderTerm{Column: 4, Desc: true}},
		{"Count", h.Count().t, engine.AggTerm(engine.TermCount, engine.ColumnTerm(4))},
		{"CountDistinct", h.CountDistinct().t, engine.AggTerm(engine.TermCountDistinct, engine.ColumnTerm(4))},
		{"Min", h.Min().t, engine.AggTerm(engine.TermMin, engine.ColumnTerm(4))},
		{"Max", h.Max().t, engine.AggTerm(engine.TermMax, engine.ColumnTerm(4))},
		{"Set", h.Set(a).a, Assignment{Column: 4, Op: AssignSet, Value: a}},
		{"Key", h.Key().i, int32(4)},
		{"Select", h.Select().col, int32(4)},
		{"InSelect", refCols(pn(h.InSelect(h.Select()))), [2]int32{4, 4}},
		{"NotInSelect", refCols(notKid(pn(h.NotInSelect(h.Select())))), [2]int32{4, 4}},
		{"SetNull", h.SetNull().a, Assignment{Column: 4, Op: AssignSet}},
		{"IsNull", pn(h.IsNull()), engine.Node{Op: engine.OpIsNull, Col: 4}},
		{"NotNull", pn(h.NotNull()), engine.Node{Op: engine.OpNotNull, Col: 4}},
	}
	checkCases(t, cases)
}

// TestNullStringColHandle: every method of NullStringCol builds the node, term, order or setter
// the engine reads for column 5.
func TestNullStringColHandle(t *testing.T) {
	h := NullStringCol[ent, string]{I: 5}
	a, b := "x", "y"
	cases := []handleCase{
		{"EQ", pn(h.EQ(a)), engine.Node{Op: engine.OpEQ, Col: 5, V: a}},
		{"NEQ", pn(h.NEQ(a)), engine.Node{Op: engine.OpNEQ, Col: 5, V: a}},
		{"In", pn(h.In(a, b)), engine.Node{Op: engine.OpIn, Col: 5, V: []any{a, b}}},
		{"NotIn", pn(h.NotIn(a, b)), engine.Node{Op: engine.OpNotIn, Col: 5, V: []any{a, b}}},
		{"GT", pn(h.GT(a)), engine.Node{Op: engine.OpGT, Col: 5, V: a}},
		{"GTE", pn(h.GTE(a)), engine.Node{Op: engine.OpGTE, Col: 5, V: a}},
		{"LT", pn(h.LT(a)), engine.Node{Op: engine.OpLT, Col: 5, V: a}},
		{"LTE", pn(h.LTE(a)), engine.Node{Op: engine.OpLTE, Col: 5, V: a}},
		{"Between", pn(h.Between(a, b)), engine.Node{Op: engine.OpBetween, Col: 5, V: a, W: b}},
		{"Asc", h.Asc().t, OrderTerm{Column: 5}},
		{"Desc", h.Desc().t, OrderTerm{Column: 5, Desc: true}},
		{"Count", h.Count().t, engine.AggTerm(engine.TermCount, engine.ColumnTerm(5))},
		{"CountDistinct", h.CountDistinct().t, engine.AggTerm(engine.TermCountDistinct, engine.ColumnTerm(5))},
		{"Min", h.Min().t, engine.AggTerm(engine.TermMin, engine.ColumnTerm(5))},
		{"Max", h.Max().t, engine.AggTerm(engine.TermMax, engine.ColumnTerm(5))},
		{"Set", h.Set(a).a, Assignment{Column: 5, Op: AssignSet, Value: a}},
		{"Key", h.Key().i, int32(5)},
		{"Select", h.Select().col, int32(5)},
		{"InSelect", refCols(pn(h.InSelect(h.Select()))), [2]int32{5, 5}},
		{"NotInSelect", refCols(notKid(pn(h.NotInSelect(h.Select())))), [2]int32{5, 5}},
		{"Like", pn(h.Like("a%")), engine.Node{Op: engine.OpLike, Col: 5, V: "a%"}},
		{"HasPrefix", pn(h.HasPrefix("5%")), engine.Node{Op: engine.OpLike, Col: 5, V: "5!%%"}},
		{"HasSuffix", pn(h.HasSuffix("_x")), engine.Node{Op: engine.OpLike, Col: 5, V: "%!_x"}},
		{"Contains", pn(h.Contains("a!")), engine.Node{Op: engine.OpLike, Col: 5, V: "%a!!%"}},
		{"EqualFold", pn(h.EqualFold("Ab")), engine.Node{Op: engine.OpFold, Col: 5, V: "Ab"}},
		{"ContainsFold", pn(h.ContainsFold("a%")), engine.Node{Op: engine.OpLikeFold, Col: 5, V: "%a!%%"}},
		{"SetNull", h.SetNull().a, Assignment{Column: 5, Op: AssignSet}},
		{"IsNull", pn(h.IsNull()), engine.Node{Op: engine.OpIsNull, Col: 5}},
		{"NotNull", pn(h.NotNull()), engine.Node{Op: engine.OpNotNull, Col: 5}},
	}
	checkCases(t, cases)
}

// TestNullNumberColHandle: every method of NullNumberCol builds the node, term, order or setter
// the engine reads for column 8.
func TestNullNumberColHandle(t *testing.T) {
	h := NullNumberCol[ent, int64]{I: 8}
	a, b := int64(1), int64(2)
	cases := []handleCase{
		{"EQ", pn(h.EQ(a)), engine.Node{Op: engine.OpEQ, Col: 8, V: a}},
		{"NEQ", pn(h.NEQ(a)), engine.Node{Op: engine.OpNEQ, Col: 8, V: a}},
		{"In", pn(h.In(a, b)), engine.Node{Op: engine.OpIn, Col: 8, V: []any{a, b}}},
		{"NotIn", pn(h.NotIn(a, b)), engine.Node{Op: engine.OpNotIn, Col: 8, V: []any{a, b}}},
		{"GT", pn(h.GT(a)), engine.Node{Op: engine.OpGT, Col: 8, V: a}},
		{"GTE", pn(h.GTE(a)), engine.Node{Op: engine.OpGTE, Col: 8, V: a}},
		{"LT", pn(h.LT(a)), engine.Node{Op: engine.OpLT, Col: 8, V: a}},
		{"LTE", pn(h.LTE(a)), engine.Node{Op: engine.OpLTE, Col: 8, V: a}},
		{"Between", pn(h.Between(a, b)), engine.Node{Op: engine.OpBetween, Col: 8, V: a, W: b}},
		{"Asc", h.Asc().t, OrderTerm{Column: 8}},
		{"Desc", h.Desc().t, OrderTerm{Column: 8, Desc: true}},
		{"Count", h.Count().t, engine.AggTerm(engine.TermCount, engine.ColumnTerm(8))},
		{"CountDistinct", h.CountDistinct().t, engine.AggTerm(engine.TermCountDistinct, engine.ColumnTerm(8))},
		{"Min", h.Min().t, engine.AggTerm(engine.TermMin, engine.ColumnTerm(8))},
		{"Max", h.Max().t, engine.AggTerm(engine.TermMax, engine.ColumnTerm(8))},
		{"Set", h.Set(a).a, Assignment{Column: 8, Op: AssignSet, Value: a}},
		{"Key", h.Key().i, int32(8)},
		{"Select", h.Select().col, int32(8)},
		{"InSelect", refCols(pn(h.InSelect(h.Select()))), [2]int32{8, 8}},
		{"NotInSelect", refCols(notKid(pn(h.NotInSelect(h.Select())))), [2]int32{8, 8}},
		{"Sum", h.Sum().t, engine.AggTerm(engine.TermSum, engine.ColumnTerm(8))},
		{"Avg", h.Avg().t, engine.AggTerm(engine.TermAvg, engine.ColumnTerm(8))},
		{"Add", h.Add(a).a, Assignment{Column: 8, Op: AssignAdd, Value: a}},
		{"Sub", h.Sub(a).a, Assignment{Column: 8, Op: AssignSub, Value: a}},
		{"SetNull", h.SetNull().a, Assignment{Column: 8, Op: AssignSet}},
		{"IsNull", pn(h.IsNull()), engine.Node{Op: engine.OpIsNull, Col: 8}},
		{"NotNull", pn(h.NotNull()), engine.Node{Op: engine.OpNotNull, Col: 8}},
	}
	checkCases(t, cases)
}

// TestNullColHandle: a nullable handle of a bool, which has no ordering.
func TestNullColHandle(t *testing.T) {
	h := NullCol[ent, bool]{I: 7}
	checkCases(t, []handleCase{
		{"EQ", pn(h.EQ(true)), engine.Node{Op: engine.OpEQ, Col: 7, V: true}},
		{"NEQ", pn(h.NEQ(true)), engine.Node{Op: engine.OpNEQ, Col: 7, V: true}},
		{"In", pn(h.In(true, false)), engine.Node{Op: engine.OpIn, Col: 7, V: []any{true, false}}},
		{"NotIn", pn(h.NotIn(true)), engine.Node{Op: engine.OpNotIn, Col: 7, V: []any{true}}},
		{"Asc", h.Asc().t, OrderTerm{Column: 7}},
		{"Desc", h.Desc().t, OrderTerm{Column: 7, Desc: true}},
		{"Key", h.Key().i, int32(7)},
		{"Select", h.Select().col, int32(7)},
		{"InSelect", refCols(pn(h.InSelect(h.Select()))), [2]int32{7, 7}},
		{"NotInSelect", refCols(notKid(pn(h.NotInSelect(h.Select())))), [2]int32{7, 7}},
		{"Count", h.Count().t, engine.AggTerm(engine.TermCount, engine.ColumnTerm(7))},
		{"CountDistinct", h.CountDistinct().t, engine.AggTerm(engine.TermCountDistinct, engine.ColumnTerm(7))},
		{"Set", h.Set(true).a, Assignment{Column: 7, Op: AssignSet, Value: true}},
		{"SetNull", h.SetNull().a, Assignment{Column: 7, Op: AssignSet}},
		{"IsNull", pn(h.IsNull()), engine.Node{Op: engine.OpIsNull, Col: 7}},
		{"NotNull", pn(h.NotNull()), engine.Node{Op: engine.OpNotNull, Col: 7}},
	})
}

// TestHandlesRest: the ordering, range and aggregate methods of the
// handles that earlier tests reach only in part.
func TestHandlesRest(t *testing.T) {
	s := StringCol[ent, string]{I: 4}
	n := NumberCol[ent, int64]{I: 3}
	c := Col[ent, bool]{I: 6}
	checkCases(t, []handleCase{
		{"String GT", pn(s.GT("a")), engine.Node{Op: engine.OpGT, Col: 4, V: "a"}},
		{"String GTE", pn(s.GTE("a")), engine.Node{Op: engine.OpGTE, Col: 4, V: "a"}},
		{"String LT", pn(s.LT("a")), engine.Node{Op: engine.OpLT, Col: 4, V: "a"}},
		{"String LTE", pn(s.LTE("a")), engine.Node{Op: engine.OpLTE, Col: 4, V: "a"}},
		{"String Between", pn(s.Between("a", "b")), engine.Node{Op: engine.OpBetween, Col: 4, V: "a", W: "b"}},
		{"String Min", s.Min().t, engine.AggTerm(engine.TermMin, engine.ColumnTerm(4))},
		{"Number Count", n.Count().t, engine.AggTerm(engine.TermCount, engine.ColumnTerm(3))},
		{"Col CountDistinct", c.CountDistinct().t, engine.AggTerm(engine.TermCountDistinct, engine.ColumnTerm(6))},
	})
}

// TestNullJSONColHandle: the path and array methods of a nullable JSON
// handle build what the JSON handle's do.
func TestNullJSONColHandle(t *testing.T) {
	h := NullJSONCol[ent, []string]{I: 9}
	j := JSONCol[ent, []string]{I: 9}
	keys := []string{"a", "b"}
	text, num, flag := h.Text("a", "b"), h.Number("a", "b"), h.Bool("a", "b")
	checkCases(t, []handleCase{
		{"Text", text, JSONText[ent]{9, engine.JSONPath{Kind: engine.JSONKindText, Keys: keys}}},
		{"Number", num, JSONNumber[ent]{9, engine.JSONPath{Kind: engine.JSONKindNumber, Keys: keys}}},
		{"Bool", flag, JSONBool[ent]{9, engine.JSONPath{Kind: engine.JSONKindBool, Keys: keys}}},
		{"Text as JSONCol's", text, j.Text("a", "b")},
		{"HasSome", pn(h.HasSome("x", 1)), pn(j.HasSome("x", 1))},
		{"HasEvery", pn(h.HasEvery("x", 1)), pn(j.HasEvery("x", 1))},
		{"HasKey", pn(h.HasKey("a")), pn(j.HasKey("a"))},
		{"Has", pn(h.Has("x")), pn(j.Has("x"))},
		{"IsEmpty", pn(h.IsEmpty()), pn(j.IsEmpty())},
		{"SetNull", h.SetNull().a, Assignment{Column: 9, Op: AssignSet}},
		{"IsNull", pn(h.IsNull()), engine.Node{Op: engine.OpIsNull, Col: 9}},
		{"NotNull", pn(h.NotNull()), engine.Node{Op: engine.OpNotNull, Col: 9}},
	})
}

// rkinds has a column for each handle kind whose Old, New, OldPtr and
// NewPtr are read from a Change.
type rkinds struct {
	ID       int64
	At       time.Time
	MaybeAt  *time.Time
	Dec      rdec
	MaybeDec *rdec
	Note     *string
	Flag     *bool
	Doc      *[]string
	N        *int64
}

var rkindsMeta MetaOnce[rkinds]

func (*rkinds) VeloxMeta() *Meta[rkinds] {
	return rkindsMeta.Load(func() Descriptor[rkinds] {
		return Descriptor[rkinds]{
			Table: "rkinds",
			Columns: []ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "At", Name: "at"},
				{Field: "MaybeAt", Name: "maybe_at"}, {Field: "Dec", Name: "dec"},
				{Field: "MaybeDec", Name: "maybe_dec"}, {Field: "Note", Name: "note"},
				{Field: "Flag", Name: "flag"}, {Field: "Doc", Name: "doc"}, {Field: "N", Name: "n"}},
			PrimaryKey: []int{0},
			FieldPtr: func(x *rkinds, i int) any {
				return [...]any{&x.ID, &x.At, &x.MaybeAt, &x.Dec, &x.MaybeDec, &x.Note, &x.Flag, &x.Doc, &x.N}[i]
			},
		}
	})
}

// TestHandleOldNew: every handle kind reads its column from a Change as its
// value type, a nullable one as the element (zero for NULL) with OldPtr and
// NewPtr telling NULL apart.
func TestHandleOldNew(t *testing.T) {
	d1, d2 := rdec("1.5"), rdec("2.5")
	note, flag, doc, n := "n", true, []string{"k"}, int64(4)
	old := &rkinds{ID: 1, At: t1, MaybeAt: &t1, Dec: d1, MaybeDec: &d1, Note: &note, Flag: &flag, Doc: &doc, N: &n}
	cur := &rkinds{ID: 1, At: t2, Dec: d2, MaybeDec: &d2}
	c := changeFor(t, OpUpdate, old, cur)

	at := TimeCol[rkinds, time.Time]{I: 1}
	maybeAt := NullTimeCol[rkinds, time.Time]{I: 2}
	dec := OtherCol[rkinds, rdec]{I: 3}
	maybeDec := NullOtherCol[rkinds, rdec]{I: 4}
	nnote := NullStringCol[rkinds, string]{I: 5}
	nflag := NullCol[rkinds, bool]{I: 6}
	ndoc := NullJSONCol[rkinds, []string]{I: 7}
	nn := NullNumberCol[rkinds, int64]{I: 8}
	checkCases(t, []handleCase{
		{"TimeCol Old", at.Old(c), t1},
		{"TimeCol New", at.New(c), t2},
		{"NullTimeCol Old", maybeAt.Old(c), t1},
		{"NullTimeCol New", maybeAt.New(c), time.Time{}},
		{"NullTimeCol OldPtr", maybeAt.OldPtr(c), &t1},
		{"NullTimeCol NewPtr", maybeAt.NewPtr(c), (*time.Time)(nil)},
		{"OtherCol Old", dec.Old(c), d1},
		{"OtherCol New", dec.New(c), d2},
		{"NullOtherCol Old", maybeDec.Old(c), d1},
		{"NullOtherCol New", maybeDec.New(c), d2},
		{"NullOtherCol OldPtr", maybeDec.OldPtr(c), &d1},
		{"NullOtherCol NewPtr", maybeDec.NewPtr(c), &d2},
		{"NullStringCol Old", nnote.Old(c), "n"},
		{"NullStringCol New", nnote.New(c), ""},
		{"NullStringCol OldPtr", nnote.OldPtr(c), &note},
		{"NullStringCol NewPtr", nnote.NewPtr(c), (*string)(nil)},
		{"NullCol Old", nflag.Old(c), true},
		{"NullCol New", nflag.New(c), false},
		{"NullCol OldPtr", nflag.OldPtr(c), &flag},
		{"NullCol NewPtr", nflag.NewPtr(c), (*bool)(nil)},
		{"NullJSONCol Old", ndoc.Old(c), doc},
		{"NullJSONCol New", ndoc.New(c), []string(nil)},
		{"NullJSONCol OldPtr", ndoc.OldPtr(c), &doc},
		{"NullJSONCol NewPtr", ndoc.NewPtr(c), (*[]string)(nil)},
		{"NullNumberCol Old", nn.Old(c), int64(4)},
		{"NullNumberCol New", nn.New(c), int64(0)},
		{"NullNumberCol OldPtr", nn.OldPtr(c), &n},
		{"NullNumberCol NewPtr", nn.NewPtr(c), (*int64)(nil)},
	})
	// Another entity's Change reads as the zero value and nil.
	other := changeFor(t, OpUpdate, &rticket{ID: 1}, &rticket{ID: 2})
	checkCases(t, []handleCase{
		{"OtherCol of another entity", dec.Old(other), rdec("")},
		{"NullOtherCol of another entity", maybeDec.NewPtr(other), (*rdec)(nil)},
		{"NullTimeCol of another entity", maybeAt.OldPtr(other), (*time.Time)(nil)},
	})
}
