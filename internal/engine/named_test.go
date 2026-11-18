package engine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/untyped"
)

// The entities of the conditions and mutations by name: nmOrg, and nmDoc,
// whose relation org is linked (SetEdgeTarget) and whose relation peer is
// not.

type nmOrg struct {
	ID   int64
	Name string
}

var nmOrgMeta velox.MetaOnce[nmOrg]

func (*nmOrg) VeloxMeta() *velox.Meta[nmOrg] { return nmOrgMeta.Load(nmOrgDescriptor) }

func nmOrgDescriptor() velox.Descriptor[nmOrg] {
	return velox.Descriptor[nmOrg]{
		Table:      "nm_orgs",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *nmOrg, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Name
			}
			return nil
		},
	}
}

// nmStatus is a string enum.
type nmStatus string

type nmDoc struct {
	ID      int64
	OrgID   int64
	Title   string
	Note    *string
	Rank    int8
	Status  nmStatus
	At      time.Time
	Score   *float64
	Org     velox.One[nmOrg]
	Peer    velox.One[nmDoc]
	PeerID  *int64
	Visible bool
}

var nmDocMeta velox.MetaOnce[nmDoc]

func (*nmDoc) VeloxMeta() *velox.Meta[nmDoc] { return nmDocMeta.Load(nmDocDescriptor) }

// The column indexes of nmDoc.
const (
	nmID = iota
	nmOrgID
	nmTitle
	nmNote
	nmRank
	nmStatusCol
	nmAt
	nmScore
	nmPeerID
	nmVisible
)

func nmDocDescriptor() velox.Descriptor[nmDoc] {
	org := velox.EdgeTo[nmOrg]("org", nmOrgID, 0)
	engine.SetEdgeTarget(&org, func() (*engine.EntityInfo, error) { return relRef[nmOrg, nmOrg]().Owner() })
	return velox.Descriptor[nmDoc]{
		Table: "nm_docs",
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true}, {Field: "Title", Name: "title"},
			{Field: "Note", Name: "note"}, {Field: "Rank", Name: "rank"}, {Field: "Status", Name: "status"},
			{Field: "At", Name: "at"}, {Field: "Score", Name: "score"}, {Field: "PeerID", Name: "peer_id"},
			{Field: "Visible", Name: "visible"},
		},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{org, velox.EdgeTo[nmDoc]("peer", nmPeerID, nmID)},
		FieldPtr: func(x *nmDoc, i int) any {
			return [...]any{&x.ID, &x.OrgID, &x.Title, &x.Note, &x.Rank, &x.Status, &x.At, &x.Score, &x.PeerID, &x.Visible}[i]
		},
		EdgeFieldPtr: func(x *nmDoc, i int) any {
			if i == 0 {
				return &x.Org
			}
			return &x.Peer
		},
	}
}

// nmSQL renders a SELECT of nmDoc with the conditions ps.
func nmSQL(t *testing.T, ps ...velox.Predicate) (string, []any, error) {
	t.Helper()
	db := velox.OpenConn(velox.Postgres, fakeConn(nil))
	return untyped.Query(db, untyped.TableOf[nmDoc]()).Where(ps...).Limit(1).SQL(ctx)
}

// cond is c as the untyped surface's Predicate.
func cond(c engine.Predicate) velox.Predicate { return c }

// TestResolveByName: a condition by name resolves against the statement's
// entity: its value converted to the column's type, and every misfit failing
// the statement before anything is sent.
func TestResolveByName(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    engine.Predicate
		sql  string
		args []any
		err  string
	}{
		{"own type", engine.FieldEQ("org_id", int64(7)), `"org_id" = $1`, []any{int64(7)}, ""},
		{"converted", engine.FieldGT("org_id", 7), `"org_id" > $1`, []any{int64(7)}, ""},
		{"enum", engine.FieldNEQ("status", "draft"), `"status" <> $1`, []any{nmStatus("draft")}, ""},
		{"nullable value", engine.FieldEQ("note", "x"), `"note" = $1`, []any{"x"}, ""},
		{"in", engine.FieldIn("rank", 1, int64(2)), `"rank" IN ($1, $2)`, []any{int8(1), int8(2)}, ""},
		{"in none", engine.FieldIn("rank"), `1 = 0`, nil, ""},
		{"not in", engine.FieldNotIn("rank", 3), `"rank" NOT IN ($1)`, []any{int8(3)}, ""},
		{"lte", engine.FieldLTE("score", 1.5), `"score" <= $1`, []any{1.5}, ""},
		{"gte", engine.FieldGTE("at", t0), `"at" >= $1`, []any{t0}, ""},
		{"lt", engine.FieldLT("rank", int8(3)), `"rank" < $1`, []any{int8(3)}, ""},
		{"contains", engine.FieldContains("title", "a%"), `"title" LIKE $1 ESCAPE '!'`, []any{"%a!%%"}, ""},
		{"prefix", engine.FieldHasPrefix("title", "a"), `"title" LIKE $1 ESCAPE '!'`, []any{"a%"}, ""},
		{"suffix", engine.FieldHasSuffix("status", "t"), `"status" LIKE $1 ESCAPE '!'`, []any{"%t"}, ""},
		{"is null", engine.FieldIsNull("note"), `"note" IS NULL`, nil, ""},
		{"not null", engine.FieldNotNull("peer_id"), `"peer_id" IS NOT NULL`, nil, ""},
		{"unknown column", engine.FieldEQ("authr_id", 1), "", nil, `velox: engine_test.nmDoc: no column "authr_id"`},
		{"out of range", engine.FieldEQ("rank", 300), "", nil, "300 for rank, an int8: value out of"},
		{"wrong kind", engine.FieldEQ("org_id", "7"), "", nil, "velox: engine_test.nmDoc: org_id is an int64, not a string"},
		{"wrong kind in", engine.FieldIn("org_id", 1, "7"), "", nil, "org_id is an int64, not a string"},
		{"nil in", engine.FieldIn("org_id", nil), "", nil, "a FieldIn or FieldNotIn of nil on org_id: use FieldIsNull or FieldNotNull"},
		{"eq nil", engine.FieldEQ("note", nil), "", nil, "a comparison of note with nil: use IsNull or NotNull"},
		{"is null on not null", engine.FieldIsNull("title"), "", nil, "title is not nullable"},
		{"text on a number", engine.FieldContains("rank", "1"), "", nil, "rank is not a text column"},
		{"fold on a number", engine.FieldContainsFold("rank", "1"), "", nil, "rank is not a text column"},
		{"in a group", velox.And(engine.FieldEQ("rank", 1), velox.Or(engine.FieldEQ("title", "a"), engine.FieldEQ("nope", 1))), "", nil, `no column "nope"`},
		{"not", velox.Not(engine.FieldEQ("visible", true)), `NOT ("visible" = $1)`, []any{true}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sql, args, err := nmSQL(t, cond(tc.c))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(sql, "WHERE "+tc.sql) && !strings.Contains(sql, tc.sql) {
				t.Errorf("SQL %s, want %s", sql, tc.sql)
			}
			if len(args) < len(tc.args) || fmt.Sprint(args[:len(tc.args)]) != fmt.Sprint(tc.args) {
				t.Errorf("args %#v, want %#v", args, tc.args)
			}
			for i, a := range tc.args {
				if fmt.Sprintf("%T", args[i]) != fmt.Sprintf("%T", a) {
					t.Errorf("arg %d is a %T, want a %T", i, args[i], a)
				}
			}
		})
	}
}

// TestResolveOutOfRangeIs: an out-of-range value matches ErrOutOfRange.
func TestResolveOutOfRangeIs(t *testing.T) {
	_, _, err := nmSQL(t, cond(engine.FieldEQ("rank", -200)))
	if !errors.Is(err, velox.ErrOutOfRange) {
		t.Fatalf("err %v, want ErrOutOfRange", err)
	}
}

// TestResolveRendersAsTyped: a condition by name renders the SQL, the
// arguments and the render cache's key of the same condition by index.
func TestResolveRendersAsTyped(t *testing.T) {
	tbl := untyped.TableOf[nmDoc]()
	pairs := []struct{ named, typed velox.Predicate }{
		{cond(engine.FieldEQ("org_id", int64(3))), tbl.Pred(nmOrgID, untyped.PredEQ, int64(3))},
		{cond(engine.FieldHasPrefix("title", "x")), tbl.Pred(nmTitle, untyped.PredHasPrefix, "x")},
		{cond(engine.FieldIsNull("note")), tbl.Pred(nmNote, untyped.PredIsNull, nil)},
		{cond(engine.FieldIn("rank", int8(1), int8(2))), tbl.Pred(nmRank, untyped.PredIn, []any{int8(1), int8(2)})},
	}
	db := velox.OpenConn(velox.Postgres, fakeConn(nil))
	for _, p := range pairs {
		ns, na, err := nmSQL(t, p.named)
		if err != nil {
			t.Fatal(err)
		}
		ts, ta, err := nmSQL(t, p.typed)
		if err != nil {
			t.Fatal(err)
		}
		if ns != ts || fmt.Sprint(na) != fmt.Sprint(ta) {
			t.Errorf("by name %s %v, by index %s %v", ns, na, ts, ta)
		}
		// One shape in the cache for both.
		before := cacheSize(db)
		q := untyped.Query(db, tbl).Limit(1)
		if _, _, err := q.Where(p.typed).SQL(ctx); err != nil {
			t.Fatal(err)
		}
		mid := cacheSize(db)
		if _, _, err := q.Where(p.named).SQL(ctx); err != nil {
			t.Fatal(err)
		}
		if after := cacheSize(db); after != mid || mid != before+1 {
			t.Errorf("cache sizes %d, %d, %d: by name is another shape", before, mid, after)
		}
	}
}

// TestResolveEdgeNeedsLink: an EXISTS by a relation's name resolves on a
// linked relation, with its conditions on the target, and fails the
// statement on a relation that is not linked or not there.
func TestResolveEdgeNeedsLink(t *testing.T) {
	sql, args, err := nmSQL(t, cond(engine.HasEdgeWith("org", engine.FieldEQ("name", "acme"))))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `EXISTS (SELECT 1 FROM "nm_orgs"`) || !strings.Contains(sql, `"name" = $1`) || fmt.Sprint(args[:1]) != "[acme]" {
		t.Errorf("SQL %s %v", sql, args)
	}
	typed, _, err := nmSQL(t, velox.Predicate(velox.Rel[nmDoc, nmOrg]{I: 0}.HasWith(velox.Pred[nmOrg](untyped.TableOf[nmOrg]().Pred(1, untyped.PredEQ, "acme")))))
	if err != nil {
		t.Fatal(err)
	}
	if typed != sql {
		t.Errorf("by name %s, typed %s", sql, typed)
	}
	if _, _, err := nmSQL(t, cond(velox.Or(engine.FieldEQ("visible", true), engine.HasEdge("org")))); err != nil {
		t.Errorf("an Edge in an Or: %v", err)
	}
	for _, tc := range []struct {
		c   engine.Predicate
		err string
	}{
		{engine.HasEdge("peer"), "relation peer's target is not linked: velox-gen links it when the schema names it with velox.HasEdge or velox.HasEdgeWith"},
		{engine.HasEdge("nope"), `no relation "nope"`},
		{engine.HasEdgeWith("org", engine.FieldEQ("title", "x")), `relation org: no column "title"`},
	} {
		if _, _, err := nmSQL(t, cond(tc.c)); err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("err %v, want %q", err, tc.err)
		}
	}
}

// TestZeroCondFails: the zero Cond fails the statement, alone or inside a
// condition.
func TestZeroCondFails(t *testing.T) {
	for _, c := range []engine.Predicate{{}, velox.And(engine.FieldEQ("rank", 1), engine.Predicate{}), engine.HasEdgeWith("org", engine.Predicate{})} {
		if _, _, err := nmSQL(t, cond(c)); err == nil {
			t.Errorf("%v: no error", c)
		}
	}
}

// nmMutation is a mutation of nmDoc: an insert of rows, or the update or
// delete st.
func nmMutation(op velox.Op, rows ...*nmDoc) *engine.MutationState {
	info := infoOf[nmDoc]()
	mu := &engine.MutationState{In: info}
	if op == velox.OpInsert {
		mu.Op = op
		for _, r := range rows {
			mu.Rows = append(mu.Rows, unsafe.Pointer(r))
		}
		return mu
	}
	mu.St = &engine.Statement{Op: op, Entity: info}
	return mu
}

// TestMutationSetByName: Set sets every row of an insert, replaces an
// update's assignment, and is a misuse on a delete; a wrong name or kind is
// a misuse, which the mutation records.
func TestMutationSetByName(t *testing.T) {
	a, b := &nmDoc{Title: "a"}, &nmDoc{Title: "b", OrgID: 2}
	mu := nmMutation(velox.OpInsert, a, b)
	for _, s := range []struct {
		name string
		v    any
	}{{"org_id", 7}, {"note", "n"}, {"status", "open"}, {"rank", int64(4)}, {"at", t0}, {"score", 2.5}, {"visible", true}, {"title", "t"}} {
		if err := mu.SetByName(s.name, s.v); err != nil {
			t.Fatalf("Set(%q): %v", s.name, err)
		}
	}
	for _, x := range []*nmDoc{a, b} {
		if x.OrgID != 7 || x.Note == nil || *x.Note != "n" || x.Status != "open" || x.Rank != 4 || !x.At.Equal(t0) ||
			x.Score == nil || *x.Score != 2.5 || !x.Visible || x.Title != "t" {
			t.Errorf("row %+v", x)
		}
	}
	if a.Note == b.Note {
		t.Error("the rows share one pointer")
	}
	if err := mu.SetByName("note", nil); err != nil || a.Note != nil {
		t.Errorf("Set(note, nil): %v, %v", err, a.Note)
	}
	if mu.Err != nil {
		t.Fatalf("misuse %v", mu.Err)
	}
	for _, tc := range []struct {
		name string
		v    any
		err  string
	}{
		{"orgid", 1, `Set("orgid"): no column "orgid"`},
		{"org_id", "7", `Set("org_id", 7): org_id is an int64, not a string`},
		{"title", nil, "title is not nullable"},
		{"rank", 300, "300 for rank, an int8"},
	} {
		mu := nmMutation(velox.OpInsert, &nmDoc{})
		err := mu.SetByName(tc.name, tc.v)
		if err == nil || !strings.Contains(err.Error(), tc.err) || mu.Err == nil {
			t.Errorf("Set(%q, %v): %v (recorded %v), want %q", tc.name, tc.v, err, mu.Err, tc.err)
		}
	}

	up := nmMutation(velox.OpUpdate)
	up.St.Set = []engine.Assignment{{Column: nmTitle, Op: engine.AssignSet, Value: "old"}}
	if err := up.SetByName("title", "new"); err != nil {
		t.Fatal(err)
	}
	if err := up.SetByName("rank", 2); err != nil {
		t.Fatal(err)
	}
	if err := up.SetByName("note", nil); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprint([]engine.Assignment{{Column: nmTitle, Op: engine.AssignSet, Value: "new"}, {Column: nmRank, Op: engine.AssignSet, Value: int8(2)}, {Column: nmNote, Op: engine.AssignSet}})
	if got := fmt.Sprint(up.St.Set); got != want {
		t.Errorf("Set %s, want %s", got, want)
	}
	for _, tc := range []struct {
		name string
		v    any
		err  string
	}{
		{"title", 1, "title is a string, not an int"},
		{"title", nil, "title is not nullable"},
	} {
		up := nmMutation(velox.OpUpdate)
		if err := up.SetByName(tc.name, tc.v); err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("update Set(%q, %v): %v, want %q", tc.name, tc.v, err, tc.err)
		}
	}

	del := nmMutation(velox.OpDelete)
	if err := del.SetByName("title", "x"); err == nil || del.Err == nil || !strings.Contains(err.Error(), "a delete assigns no columns") {
		t.Errorf("delete Set: %v", err)
	}
}

// TestMutationFillByName: Fill sets the rows whose column is zero, and is a
// misuse on an update or a delete.
func TestMutationFillByName(t *testing.T) {
	note := "kept"
	a, b := &nmDoc{OrgID: 3, Note: &note}, &nmDoc{}
	mu := nmMutation(velox.OpInsert, a, b)
	if err := mu.FillByName("org_id", int64(9)); err != nil {
		t.Fatal(err)
	}
	if err := mu.FillByName("note", "filled"); err != nil {
		t.Fatal(err)
	}
	if a.OrgID != 3 || b.OrgID != 9 || *a.Note != "kept" || b.Note == nil || *b.Note != "filled" {
		t.Errorf("rows %+v %+v", a, b)
	}
	if err := mu.FillByName("org_id", "x"); err == nil {
		t.Error("Fill of a string into an int64: no error")
	}
	for _, op := range []velox.Op{velox.OpUpdate, velox.OpDelete} {
		mu := nmMutation(op)
		if err := mu.FillByName("org_id", 1); err == nil || mu.Err == nil {
			t.Errorf("%v Fill: %v", op, err)
		}
	}
	if err := nmMutation(velox.OpInsert).FillByName("nope", 1); err == nil {
		t.Error("Fill of no column: no error")
	}
}

// TestMutationValueByName: Value returns what an update's Set assigns, the
// value of a nullable column's pointer, and nothing for Add or a column not
// assigned; it is a misuse on an insert.
func TestMutationValueByName(t *testing.T) {
	n := "n"
	up := nmMutation(velox.OpUpdate)
	up.St.Set = []engine.Assignment{
		{Column: nmTitle, Op: engine.AssignSet, Value: "t"},
		{Column: nmNote, Op: engine.AssignSet, Value: &n},
		{Column: nmScore, Op: engine.AssignSet, Value: (*float64)(nil)},
		{Column: nmRank, Op: engine.AssignAdd, Value: int8(1)},
		{Column: nmPeerID, Op: engine.AssignSet},
	}
	for _, tc := range []struct {
		name string
		v    any
		ok   bool
	}{{"title", "t", true}, {"note", "n", true}, {"score", nil, true}, {"rank", nil, false}, {"org_id", nil, false}, {"peer_id", nil, true}} {
		v, ok := up.ValueByName(tc.name)
		if v != tc.v || ok != tc.ok {
			t.Errorf("Value(%q) = %v, %v; want %v, %v", tc.name, v, ok, tc.v, tc.ok)
		}
	}
	if up.Err != nil {
		t.Fatal(up.Err)
	}
	if _, ok := up.ValueByName("nope"); ok || up.Err == nil {
		t.Error("Value of no column: no misuse")
	}
	ins := nmMutation(velox.OpInsert, &nmDoc{})
	if _, ok := ins.ValueByName("title"); ok || ins.Err == nil {
		t.Error("Value on an insert: no misuse")
	}
	if v, ok := nmMutation(velox.OpDelete).ValueByName("title"); ok || v != nil {
		t.Errorf("Value on a delete: %v, %v", v, ok)
	}
}

// TestRowsByName: an insert's rows read and set their columns by name; a
// read-only row refuses Set; a wrong name is recorded.
func TestRowsByName(t *testing.T) {
	note, score := "n", 1.5
	a := &nmDoc{ID: 1, OrgID: 2, Title: "a", Note: &note, Rank: 3, Status: "s", At: t0, Score: &score, Visible: true}
	mu := nmMutation(velox.OpInsert, a, &nmDoc{})
	var rows []engine.Row
	for r := range mu.RowsByName() {
		rows = append(rows, r)
	}
	if len(rows) != 2 {
		t.Fatalf("%d rows", len(rows))
	}
	r := rows[0]
	for name, want := range map[string]any{"id": int64(1), "org_id": int64(2), "title": "a", "note": "n", "rank": int8(3),
		"status": nmStatus("s"), "at": t0, "score": 1.5, "peer_id": nil, "visible": true} {
		if got := r.Value(name); fmt.Sprint(got) != fmt.Sprint(want) || fmt.Sprintf("%T", got) != fmt.Sprintf("%T", want) {
			t.Errorf("Value(%q) = %#v, want %#v", name, got, want)
		}
	}
	for name, want := range map[string]bool{"id": false, "peer_id": true, "status": false, "rank": false} {
		if r.IsZero(name) != want {
			t.Errorf("IsZero(%q) = %v", name, !want)
		}
	}
	z := rows[1]
	for _, name := range []string{"id", "org_id", "title", "note", "rank", "status", "at", "score", "peer_id", "visible"} {
		if !z.IsZero(name) {
			t.Errorf("zero row: IsZero(%q) false", name)
		}
	}
	if err := z.Set("status", "x"); err != nil || z.Value("status") != nmStatus("x") {
		t.Errorf("Set(status): %v, %v", err, z.Value("status"))
	}
	if err := z.Set("title", 1); err == nil || mu.Err == nil {
		t.Errorf("Set(title, 1): %v", err)
	}
	if r.Entity().(*nmDoc) != a {
		t.Error("Entity is not the row")
	}
	var rec error
	ro := engine.NewRow(infoOf[nmDoc](), unsafe.Pointer(a), &rec, true)
	if err := ro.Set("title", "x"); err == nil || rec == nil || a.Title != "a" {
		t.Errorf("Set on a read-only row: %v, %v", err, rec)
	}
	rec = nil
	rw := engine.NewRow(infoOf[nmDoc](), unsafe.Pointer(a), &rec, false)
	if rw.Value("nope") != nil || rec == nil {
		t.Errorf("Value of no column: %v", rec)
	}
	rec = nil
	if rw.IsZero("nope") || rec == nil {
		t.Errorf("IsZero of no column: %v", rec)
	}
	rec = nil
	if err := rw.Set("nope", 1); err == nil || rec == nil {
		t.Errorf("Set of no column: %v", err)
	}
	var zero engine.Row
	if zero.Value("id") != nil || zero.Entity() != nil {
		t.Error("the zero Row has values")
	}
}

// TestRewriteByName: a Rewrite hook's conditions by name resolve too, and a
// wrong one fails the statement.
func TestRewriteByName(t *testing.T) {
	add := func(c engine.Predicate) velox.Option {
		return velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			st.Where = append(st.Where, cond(c))
			return nil
		})
	}
	db := velox.OpenConn(velox.Postgres, fakeConn(nil), add(engine.FieldEQ("title", "x")))
	sql, args, err := untyped.Query(db, untyped.TableOf[nmDoc]()).Limit(1).SQL(ctx)
	if err != nil || !strings.Contains(sql, `WHERE "title" = $1`) || args[0] != "x" {
		t.Errorf("%s %v %v", sql, args, err)
	}
	db = velox.OpenConn(velox.Postgres, fakeConn(nil), add(engine.FieldEQ("nope", "x")))
	if _, _, err := untyped.Query(db, untyped.TableOf[nmDoc]()).Limit(1).SQL(ctx); err == nil || !strings.Contains(err.Error(), `no column "nope"`) {
		t.Errorf("err %v", err)
	}
}

// TestTableHasByName: Table.Has takes conditions by name on its target.
func TestTableHasByName(t *testing.T) {
	docs, orgs := untyped.TableOf[nmDoc](), untyped.TableOf[nmOrg]()
	sql, args, err := nmSQL(t, docs.Has(0, orgs, cond(engine.FieldEQ("name", "acme"))))
	if err != nil || !strings.Contains(sql, `"name" = $1`) || args[0] != "acme" {
		t.Errorf("%s %v %v", sql, args, err)
	}
	if _, _, err := nmSQL(t, docs.Has(0, orgs, cond(engine.FieldEQ("title", "acme")))); err == nil {
		t.Error("a condition on no column of the target: no error")
	}
	if _, _, err := nmSQL(t, docs.Has(5, orgs, cond(engine.FieldEQ("name", "acme")))); err == nil {
		t.Error("no relation 5: no error")
	}
}

// TestFieldConditionsRenderAsTyped: each of the root's conditions by name
// renders as the typed condition of the same operator. FieldEqualFold has
// no typed operator; it renders LOWER on both sides.
func TestFieldConditionsRenderAsTyped(t *testing.T) {
	tbl := untyped.TableOf[nmDoc]()
	pairs := []struct {
		name         string
		named, typed velox.Predicate
	}{
		{"FieldEQ", velox.FieldEQ("rank", int8(1)), tbl.Pred(nmRank, untyped.PredEQ, int8(1))},
		{"FieldNEQ", velox.FieldNEQ("rank", int8(1)), tbl.Pred(nmRank, untyped.PredNEQ, int8(1))},
		{"FieldGT", velox.FieldGT("rank", int8(1)), tbl.Pred(nmRank, untyped.PredGT, int8(1))},
		{"FieldGTE", velox.FieldGTE("rank", int8(1)), tbl.Pred(nmRank, untyped.PredGTE, int8(1))},
		{"FieldLT", velox.FieldLT("rank", int8(1)), tbl.Pred(nmRank, untyped.PredLT, int8(1))},
		{"FieldLTE", velox.FieldLTE("rank", int8(1)), tbl.Pred(nmRank, untyped.PredLTE, int8(1))},
		{"FieldIn", velox.FieldIn("rank", int8(1), int8(2)), tbl.Pred(nmRank, untyped.PredIn, []any{int8(1), int8(2)})},
		{"FieldNotIn", velox.FieldNotIn("rank", int8(1), int8(2)), tbl.Pred(nmRank, untyped.PredNotIn, []any{int8(1), int8(2)})},
		{"FieldContains", velox.FieldContains("title", "a%b"), tbl.Pred(nmTitle, untyped.PredContains, "a%b")},
		{"FieldHasPrefix", velox.FieldHasPrefix("title", "x"), tbl.Pred(nmTitle, untyped.PredHasPrefix, "x")},
		{"FieldHasSuffix", velox.FieldHasSuffix("title", "x"), tbl.Pred(nmTitle, untyped.PredHasSuffix, "x")},
		{"FieldEqualFold", velox.FieldEqualFold("title", "Ab"), tbl.Pred(nmTitle, untyped.PredEqualFold, "Ab")},
		{"FieldContainsFold", velox.FieldContainsFold("title", "a%b"), tbl.Pred(nmTitle, untyped.PredContainsFold, "a%b")},
		{"FieldIsNull", velox.FieldIsNull("note"), tbl.Pred(nmNote, untyped.PredIsNull, nil)},
		{"FieldNotNull", velox.FieldNotNull("note"), tbl.Pred(nmNote, untyped.PredNotNull, nil)},
	}
	for _, p := range pairs {
		ns, na, err := nmSQL(t, p.named)
		if err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		ts, ta, err := nmSQL(t, p.typed)
		if err != nil {
			t.Fatalf("%s typed: %v", p.name, err)
		}
		if ns != ts || fmt.Sprint(na) != fmt.Sprint(ta) {
			t.Errorf("%s: by name %s %v, typed %s %v", p.name, ns, na, ts, ta)
		}
	}
	s, args, err := nmSQL(t, velox.FieldEqualFold("title", "Ab"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `LOWER("title") = LOWER(`) || len(args) == 0 || args[0] != "Ab" {
		t.Errorf("FieldEqualFold renders %s %v", s, args)
	}
	s, args, err = nmSQL(t, velox.FieldContainsFold("title", "a%!"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `LOWER("title") LIKE LOWER(`) || !strings.Contains(s, `ESCAPE '!'`) || len(args) == 0 || args[0] != "%a!%!!%" {
		t.Errorf("FieldContainsFold renders %s %v", s, args)
	}
}
