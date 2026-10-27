package velox

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// withHook returns a context whose hnote hooks run f.
func withHook(f func(*Mutation[hnote]) error) context.Context {
	return context.WithValue(context.Background(), hnoteKey{}, f)
}

func TestMutationSetReplacesAndAppends(t *testing.T) {
	e := &FakeExec{Affected: 1}
	db := OpenFake(Postgres, e)
	ctx := withHook(func(m *Mutation[hnote]) error {
		m.Set(hn.Title.Set("T"), hn.Note.Set("n"))
		return nil
	})
	x := hnote{ID: 1, Title: "t", Note: "old"}
	if err := db.Save(&x, hn.Title).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if e.SQL != `UPDATE "hnotes" SET "title" = $1, "note" = $2 WHERE "id" = $3` {
		t.Errorf("SQL %s", e.SQL)
	}
	if x.Title != "T" || x.Note != "n" {
		t.Errorf("Save stored Title %q, Note %q", x.Title, x.Note)
	}
	// After an error, and when no row matched, *x is as it was.
	x = hnote{ID: 1, Title: "t", Note: "old"}
	e.Errs = []error{errors.New("boom")}
	if err := db.Save(&x, hn.Title).Exec(ctx); err == nil {
		t.Fatal("no error")
	}
	e.Affected = 0
	if err := db.Save(&x, hn.Title).Exec(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if x.Title != "t" || x.Note != "old" {
		t.Errorf("a failed Save stored Title %q, Note %q", x.Title, x.Note)
	}
}

// TestSaveStoresWhatItSent: a NULL stores the zero value, a nullable
// column's value is stored through a new pointer, and a value the field cannot
// hold (here, from a Rewrite hook) is not stored.
func TestSaveStoresWhatItSent(t *testing.T) {
	e := &FakeExec{Affected: 1}
	rw := WithRewrite(func(_ context.Context, st *Statement) error {
		for i := range st.Set {
			if st.Set[i].Column == 2 {
				st.Set[i].Value = 5 // an int, for a string field
			}
		}
		return nil
	})
	db := OpenFake(Postgres, e, rw)
	five := int64(5)
	x := hnote{ID: 1, Note: "old", Due: &five}
	ctx := withHook(func(m *Mutation[hnote]) error {
		m.Set(hn.Note.Set("n"), hn.Due.SetNull())
		return nil
	})
	if err := db.Save(&x, hn.Title).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if x.Note != "old" || x.Due != nil {
		t.Errorf("Note %q, Due %v", x.Note, x.Due)
	}
	ctx = withHook(func(m *Mutation[hnote]) error {
		m.Set(hn.Due.Set(7))
		return nil
	})
	if err := db.Save(&x, hn.Title).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if x.Due == nil || *x.Due != 7 || five != 5 {
		t.Errorf("Due %v, five %d", x.Due, five)
	}
}

// TestMutateSetClonesOnce: the first assignment a hook appends copies the
// statement's Set, whose array may be the query's, once; later ones, an
// append or a replacement, write the copy in place.
func TestMutateSetClonesOnce(t *testing.T) {
	query := make([]Assignment, 1, 4) // spare capacity an append would write
	query[0] = Assignment{Column: 1, Op: AssignSet, Value: "a"}
	st := &Statement{Op: OpUpdate}
	mu := mutation{st: st}
	run := func() {
		st.Set, mu.ownSet = query, false
		mutateSet(&mu, Assignment{Column: 2, Op: AssignSet, Value: "b"}) // append: the one copy
		mutateSet(&mu, Assignment{Column: 2, Op: AssignSet, Value: "c"}) // in place
		mutateSet(&mu, Assignment{Column: 1, Op: AssignSet, Value: "d"}) // in place
	}
	if allocs := testing.AllocsPerRun(100, run); allocs != 1 {
		t.Errorf("%.0f allocations, want the one copy", allocs)
	}
	want := []Assignment{{Column: 1, Op: AssignSet, Value: "d"}, {Column: 2, Op: AssignSet, Value: "c"}}
	if !slices.Equal(st.Set, want) {
		t.Errorf("Set %v, want %v", st.Set, want)
	}
	if query[0].Value != "a" || query[:2][1] != (Assignment{}) {
		t.Errorf("the query's array was written: %v", query[:2])
	}
}

func TestMutationSetOnADelete(t *testing.T) {
	e := &FakeExec{Affected: 1}
	db := OpenFake(Postgres, e)
	ctx := withHook(func(m *Mutation[hnote]) error {
		m.Set(hn.Note.Set("n"))
		return nil
	})
	_, err := db.Delete[hnote]().Where(hn.ID.EQ(1)).Exec(ctx)
	const text = "velox: velox.hnote: hook: a delete assigns no columns: Set is for updates"
	if err == nil || err.Error() != text {
		t.Errorf("got %v, want %q", err, text)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// TestValueIn: a handle's ValueIn reads the value an update's Set assigns,
// typed to the handle's value; an Add assigns none. A nullable handle gives a
// copy: SetNull is (nil, true), and changing the pointee changes nothing sent.
// On an insert ValueIn fails the write, as Set does.
func TestValueIn(t *testing.T) {
	e := &FakeExec{Affected: 1}
	db := OpenFake(Postgres, e)
	type seen struct {
		op            Op
		title, note   string
		n             int64
		okT, okN, okX bool
	}
	var s seen
	ctx := withHook(func(m *Mutation[hnote]) error {
		s.op = m.Op()
		s.title, s.okT = hn.Title.ValueIn(m)
		s.note, s.okN = hn.Note.ValueIn(m)
		s.n, s.okX = hn.N.ValueIn(m) // an Add assigns no value
		return nil
	})
	if _, err := db.Update[hnote]().Set(hn.Title.Set("t"), hn.N.Add(1)).Where(hn.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if s != (seen{op: OpUpdate, title: "t", okT: true}) {
		t.Errorf("the hook saw %+v", s)
	}
	if _, err := db.Delete[hnote]().Where(hn.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if s != (seen{op: OpDelete}) {
		t.Errorf("on a delete the hook saw %+v", s)
	}

	// A nullable handle.
	var due *int64
	var ok bool
	ctx = withHook(func(m *Mutation[hnote]) error {
		due, ok = hn.Due.ValueIn(m)
		if due != nil {
			*due = 8 // a copy: the statement still sends 7
		}
		return nil
	})
	if _, err := db.Update[hnote]().Set(hn.Due.SetNull()).Where(hn.ID.EQ(1)).Exec(ctx); err != nil || due != nil || !ok {
		t.Errorf("SetNull: %v, %v, %v", err, due, ok)
	}
	if _, err := db.Update[hnote]().Set(hn.Due.Set(7)).Where(hn.ID.EQ(1)).Exec(ctx); err != nil || due == nil || !ok || e.Args[0] != int64(7) {
		t.Errorf("Set(7): %v, %v, %v, args %v", err, due, ok, e.Args)
	}
	if _, err := db.Update[hnote]().Set(hn.Title.Set("t")).Where(hn.ID.EQ(1)).Exec(ctx); err != nil || due != nil || ok {
		t.Errorf("no assignment: %v, %v, %v", err, due, ok)
	}
	// Save assigns the field's own value, a *int64 here.
	seven := int64(7)
	x := hnote{ID: 1, Due: &seven}
	if err := db.Save(&x, hn.Due).Exec(ctx); err != nil || due == nil || !ok || seven != 7 || *x.Due != 7 {
		t.Errorf("Save: %v, %v, %v, field %d", err, due, ok, seven)
	}
	x.Due = nil
	if err := db.Save(&x, hn.Due).Exec(ctx); err != nil || due != nil || !ok {
		t.Errorf("Save of a nil: %v, %v, %v", err, due, ok)
	}

	// On an insert.
	calls := e.Calls
	ctx = withHook(func(m *Mutation[hnote]) error {
		if v, ok := hn.Due.ValueIn(m); v != nil || ok {
			t.Errorf("on an insert: %v, %v", v, ok)
		}
		return nil
	})
	err := db.Insert(&hnote{ID: 2}).Exec(ctx)
	const text = "velox: velox.hnote: hook: Set and Where are for updates and deletes; change Rows"
	if err == nil || err.Error() != text || e.Calls != calls {
		t.Errorf("got %v, want %q; %d statements sent", err, text, e.Calls-calls)
	}
	ctx = withHook(func(m *Mutation[hnote]) error {
		hn.Title.ValueIn(m)
		return nil
	})
	if err := db.Insert(&hnote{ID: 2}).Exec(ctx); err == nil || err.Error() != text {
		t.Errorf("got %v, want %q", err, text)
	}
}

// TestValueInRecordsAnotherType: a value of another type gives no value and
// records an error naming the column and both types; the first one recorded
// wins, as for every misuse.
func TestValueInRecordsAnotherType(t *testing.T) {
	info, err := loadDesc(hnoteDesc())
	if err != nil {
		t.Fatal(err)
	}
	st := Statement{Op: OpUpdate, Entity: info, Set: []Assignment{{Column: 4, Op: AssignSet, Value: "x"}, {Column: 1, Op: AssignSet, Value: 5}}}
	m := &Mutation[hnote]{mu: mutation{st: &st}}
	if v, ok := hn.Due.ValueIn(m); v != nil || ok {
		t.Errorf("Due: %v, %v", v, ok)
	}
	const first = `column "due" is assigned a value of type string, not int64`
	if m.mu.err == nil || m.mu.err.Error() != first {
		t.Fatalf("recorded %v, want %q", m.mu.err, first)
	}
	if v, ok := hn.Title.ValueIn(m); v != "" || ok {
		t.Errorf("Title: %q, %v", v, ok)
	}
	if m.mu.err.Error() != first {
		t.Errorf("the second replaced the first: %v", m.mu.err)
	}
	m = &Mutation[hnote]{mu: mutation{st: &st}}
	hn.Title.ValueIn(m)
	if want := `column "title" is assigned a value of type int, not string`; m.mu.err == nil || m.mu.err.Error() != want {
		t.Errorf("recorded %v, want %q", m.mu.err, want)
	}
}

type hstatus string

// TestValueInEveryKind: each handle kind's ValueIn, over what its own Set
// and SetNull assign.
func TestValueInEveryKind(t *testing.T) {
	type doc struct{ A int }
	at := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	var (
		b  = Col[hnote, bool]{I: 0}
		o  = OrderedCol[hnote, time.Time]{I: 1}
		n  = NumberCol[hnote, int64]{I: 2}
		s  = StringCol[hnote, hstatus]{I: 3}
		j  = JSONCol[hnote, doc]{I: 4}
		nb = NullCol[hnote, bool]{I: 5}
		no = NullOrderedCol[hnote, time.Time]{I: 6}
		nn = NullNumberCol[hnote, int64]{I: 7}
		ns = NullStringCol[hnote, hstatus]{I: 8}
		nj = NullJSONCol[hnote, []string]{I: 9}
		nk = NullJSONCol[hnote, doc]{I: 10}
	)
	var set []Assignment
	for _, x := range []Setter[hnote]{b.Set(true), o.Set(at), n.Set(3), s.Set("open"), j.Set(doc{A: 1}),
		nb.Set(true), no.Set(at), nn.Set(4), ns.SetNull(), nj.Set([]string{"a"}), nk.SetNull()} {
		set = append(set, x.a)
	}
	m := &Mutation[hnote]{mu: mutation{st: &Statement{Op: OpUpdate, Set: set}}}
	if v, ok := b.ValueIn(m); !v || !ok {
		t.Errorf("Col: %v, %v", v, ok)
	}
	if v, ok := o.ValueIn(m); !v.Equal(at) || !ok {
		t.Errorf("OrderedCol: %v, %v", v, ok)
	}
	if v, ok := n.ValueIn(m); v != 3 || !ok {
		t.Errorf("NumberCol: %v, %v", v, ok)
	}
	if v, ok := s.ValueIn(m); v != "open" || !ok {
		t.Errorf("StringCol of an enum: %v, %v", v, ok)
	}
	if v, ok := j.ValueIn(m); v != (doc{A: 1}) || !ok {
		t.Errorf("JSONCol: %v, %v", v, ok)
	}
	if v, ok := nb.ValueIn(m); v == nil || !*v || !ok {
		t.Errorf("NullCol: %v, %v", v, ok)
	}
	if v, ok := no.ValueIn(m); v == nil || !v.Equal(at) || !ok {
		t.Errorf("NullOrderedCol: %v, %v", v, ok)
	}
	if v, ok := nn.ValueIn(m); v == nil || *v != 4 || !ok {
		t.Errorf("NullNumberCol: %v, %v", v, ok)
	}
	if v, ok := ns.ValueIn(m); v != nil || !ok {
		t.Errorf("NullStringCol after SetNull: %v, %v", v, ok)
	}
	if v, ok := nj.ValueIn(m); v == nil || !slices.Equal(*v, []string{"a"}) || !ok {
		t.Errorf("NullJSONCol: %v, %v", v, ok)
	}
	if v, ok := nk.ValueIn(m); v != nil || !ok {
		t.Errorf("NullJSONCol after SetNull: %v, %v", v, ok)
	}
	if m.mu.err != nil {
		t.Errorf("recorded %v", m.mu.err)
	}
	// Unassigned, and an Add.
	m = &Mutation[hnote]{mu: mutation{st: &Statement{Op: OpUpdate, Set: []Assignment{n.Add(1).a, nn.Add(1).a}}}}
	if v, ok := n.ValueIn(m); v != 0 || ok {
		t.Errorf("NumberCol after Add: %v, %v", v, ok)
	}
	if v, ok := nn.ValueIn(m); v != nil || ok {
		t.Errorf("NullNumberCol after Add: %v, %v", v, ok)
	}
	if v, ok := j.ValueIn(m); v != (doc{}) || ok {
		t.Errorf("JSONCol unassigned: %v, %v", v, ok)
	}
	if v, ok := nj.ValueIn(m); v != nil || ok {
		t.Errorf("NullJSONCol unassigned: %v, %v", v, ok)
	}
	// On an insert, each kind records the misuse.
	for _, f := range []func(*Mutation[hnote]){
		func(m *Mutation[hnote]) { b.ValueIn(m) }, func(m *Mutation[hnote]) { o.ValueIn(m) },
		func(m *Mutation[hnote]) { n.ValueIn(m) }, func(m *Mutation[hnote]) { s.ValueIn(m) },
		func(m *Mutation[hnote]) { j.ValueIn(m) }, func(m *Mutation[hnote]) { nb.ValueIn(m) },
		func(m *Mutation[hnote]) { no.ValueIn(m) }, func(m *Mutation[hnote]) { nn.ValueIn(m) },
		func(m *Mutation[hnote]) { ns.ValueIn(m) }, func(m *Mutation[hnote]) { nj.ValueIn(m) },
	} {
		m := &Mutation[hnote]{mu: mutation{op: OpInsert}}
		f(m)
		if m.mu.err != errInsertMutation {
			t.Errorf("on an insert recorded %v", m.mu.err)
		}
	}
}

// TestMutationErrors: a hook's error and a recorded one are wrapped once
// with the stage, and an error that names the entity does not name it twice.
func TestMutationErrors(t *testing.T) {
	e := &FakeExec{Affected: 1}
	db := OpenFake(Postgres, e)
	bad := errors.New("bad")
	cases := []struct {
		f    func(*Mutation[hnote]) error
		text string
	}{
		{func(*Mutation[hnote]) error { return bad }, "velox: velox.hnote: hook: bad"},
		{func(m *Mutation[hnote]) error { m.Where(Pred[hnote]{}); return nil },
			"velox: velox.hnote: hook: a condition is the zero Predicate; make conditions with column handles, And, Or and Not"},
		{func(m *Mutation[hnote]) error {
			m.Where(Pred[hnote]{node{op: opInvalid, v: errors.Join(errors.New("velox: velox.hnote: has no relation 3"), bad)}})
			return nil
		}, "velox: velox.hnote: hook: has no relation 3\nbad"},
		{func(*Mutation[hnote]) error { return errors.Join(errors.New("velox: testmodel.Other: x"), bad) },
			"velox: velox.hnote: hook: velox: testmodel.Other: x\nbad"},
	}
	for _, c := range cases {
		_, err := db.Update[hnote]().Set(hn.Title.Set("t")).Where(hn.ID.EQ(1)).Exec(withHook(c.f))
		if err == nil || err.Error() != c.text {
			t.Errorf("got %v, want %q", err, c.text)
		}
		if c.text != cases[1].text && !errors.Is(err, bad) {
			t.Errorf("%v does not wrap the hook's error", err)
		}
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// TestSaveWhereIsTheStatementsWhere: the conditions of Where are the
// statement's, so a hook's own come after them.
func TestSaveWhereIsTheStatementsWhere(t *testing.T) {
	e := &FakeExec{Affected: 1}
	db := OpenFake(Postgres, e)
	ctx := withHook(func(m *Mutation[hnote]) error {
		m.Where(hn.Note.EQ("hook"))
		return nil
	})
	x := hnote{ID: 1, Title: "t"}
	if err := db.Save(&x, hn.Title).Where(hn.Note.EQ("caller")).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `UPDATE "hnotes" SET "title" = $1 WHERE "id" = $2 AND "note" = $3 AND "note" = $4`; e.SQL != want {
		t.Errorf("\n got %s\nwant %s", e.SQL, want)
	}
	if e.Args[2] != "caller" || e.Args[3] != "hook" {
		t.Errorf("args %v", e.Args)
	}
}

// TestBidiDeleteKeepsTheStatement: the core sends a bidirectional delete's
// two-direction condition in a copy, so the hooks' statement keeps the Where
// they gave, and a second render gives the same SQL.
func TestBidiDeleteKeepsTheStatement(t *testing.T) {
	in := &EntityInfo{Name: "links", Table: "links", Columns: []ColumnInfo{{Name: "a"}, {Name: "b"}}, PrimaryKey: []int{0, 1}, Tenant: -1, bidi: true}
	st := &Statement{Op: OpDelete, Entity: in, Where: []Predicate{ColumnEQ(0, int64(1))}}
	db := OpenFake(Postgres, &FakeExec{})
	const want = `DELETE FROM "links" WHERE ("a" = $1 OR "b" = $2)`
	for i := range 2 {
		mu := &mutation{st: st, dry: true}
		if err := writeCore(context.Background(), db, mu); err != nil {
			t.Fatal(err)
		}
		if mu.sql != want {
			t.Errorf("render %d: %s, want %s", i, mu.sql, want)
		}
		if len(st.Where) != 1 || st.Where[0].n.op != opEQ || st.Where[0].n.col != 0 {
			t.Fatalf("render %d: the statement's Where became %+v", i, st.Where)
		}
	}
}

// TestMutationSetClonesOnce: a hook's Set copies the statement's assignments
// once, on its first change, and writes in place after.
func TestMutationSetClonesOnce(t *testing.T) {
	orig := []Assignment{{Column: 1, Op: AssignSet, Value: "a"}, {Column: 2, Op: AssignSet, Value: "b"}}
	want := slices.Clone(orig)
	set := func(st *Statement) {
		mu := &mutation{st: st}
		for i := range 3 {
			mutateSet(mu, Assignment{Column: orig[0].Column, Op: AssignSet, Value: i})
		}
	}
	st := &Statement{Op: OpUpdate, Set: orig}
	set(st)
	if !slices.Equal(orig, want) || st.Set[0].Value != 2 || len(st.Set) != 2 {
		t.Errorf("orig %v, Set %v", orig, st.Set)
	}
	n := testing.AllocsPerRun(100, func() {
		st := Statement{Op: OpUpdate, Set: orig}
		set(&st)
	})
	if n != 1 {
		t.Errorf("three Sets make %.0f allocations, want 1", n)
	}
}
