package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
)

// withHook returns a context whose hnote hooks run f.
func withHook(f func(*velox.Mutation[hnote]) error) context.Context {
	return context.WithValue(context.Background(), hnoteKey{}, f)
}

func TestMutationSetReplacesAndAppends(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	ctx := withHook(func(m *velox.Mutation[hnote]) error {
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
	boom := errors.New("boom")
	e.Errs = []error{boom}
	if err := db.Save(&x, hn.Title).Exec(ctx); !errors.Is(err, boom) {
		t.Fatalf("got %v, want boom", err)
	}
	e.Affected = 0
	if err := db.Save(&x, hn.Title).Exec(ctx); !errors.Is(err, velox.ErrNotFound) {
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
	e := &fakeconn.Exec{Affected: 1}
	rw := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		for i := range st.Set {
			if st.Set[i].Column == 2 {
				st.Set[i].Value = 5 // an int, for a string field
			}
		}
		return nil
	})
	db := openFake(velox.Postgres, e, rw)
	five := int64(5)
	x := hnote{ID: 1, Note: "old", Due: &five}
	ctx := withHook(func(m *velox.Mutation[hnote]) error {
		m.Set(hn.Note.Set("n"), hn.Due.SetNull())
		return nil
	})
	if err := db.Save(&x, hn.Title).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if x.Note != "old" || x.Due != nil {
		t.Errorf("Note %q, Due %v", x.Note, x.Due)
	}
	ctx = withHook(func(m *velox.Mutation[hnote]) error {
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

func TestMutationSetOnADelete(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	ctx := withHook(func(m *velox.Mutation[hnote]) error {
		m.Set(hn.Note.Set("n"))
		return nil
	})
	_, err := db.Delete[hnote]().Where(hn.ID.EQ(1)).Exec(ctx)
	const text = "velox: engine_test.hnote: hook: a delete assigns no columns: Set is for updates"
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
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	type seen struct {
		op            velox.Op
		title, note   string
		n             int64
		okT, okN, okX bool
	}
	var s seen
	ctx := withHook(func(m *velox.Mutation[hnote]) error {
		s.op = m.Op()
		s.title, s.okT = hn.Title.ValueIn(m)
		s.note, s.okN = hn.Note.ValueIn(m)
		s.n, s.okX = hn.N.ValueIn(m) // an Add assigns no value
		return nil
	})
	if _, err := db.Update[hnote]().Set(hn.Title.Set("t"), hn.N.Add(1)).Where(hn.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if s != (seen{op: velox.OpUpdate, title: "t", okT: true}) {
		t.Errorf("the hook saw %+v", s)
	}
	if _, err := db.Delete[hnote]().Where(hn.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if s != (seen{op: velox.OpDelete}) {
		t.Errorf("on a delete the hook saw %+v", s)
	}

	// A nullable handle.
	var due *int64
	var ok bool
	ctx = withHook(func(m *velox.Mutation[hnote]) error {
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
	ctx = withHook(func(m *velox.Mutation[hnote]) error {
		if v, ok := hn.Due.ValueIn(m); v != nil || ok {
			t.Errorf("on an insert: %v, %v", v, ok)
		}
		return nil
	})
	err := db.Insert(&hnote{ID: 2}).Exec(ctx)
	const text = "velox: engine_test.hnote: hook: Set and Where are for updates and deletes; change Rows"
	if err == nil || err.Error() != text || e.Calls != calls {
		t.Errorf("got %v, want %q; %d statements sent", err, text, e.Calls-calls)
	}
	ctx = withHook(func(m *velox.Mutation[hnote]) error {
		hn.Title.ValueIn(m)
		return nil
	})
	if err := db.Insert(&hnote{ID: 2}).Exec(ctx); err == nil || err.Error() != text {
		t.Errorf("got %v, want %q", err, text)
	}
}

// TestMutationErrors: a hook's error and a recorded one are wrapped once
// with the stage, and an error that names the entity does not name it twice.
func TestMutationErrors(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	bad := errors.New("bad")
	cases := []struct {
		f    func(*velox.Mutation[hnote]) error
		text string
	}{
		{func(*velox.Mutation[hnote]) error { return bad }, "velox: engine_test.hnote: hook: bad"},
		{func(m *velox.Mutation[hnote]) error { m.Where(velox.Pred[hnote]{}); return nil },
			"velox: engine_test.hnote: hook: a condition is the zero Predicate; make conditions with column handles, And, Or and Not"},
		{func(m *velox.Mutation[hnote]) error {
			m.Where(velox.Pred[hnote](engine.NewPredicate(engine.Node{Op: engine.OpInvalid, V: errors.Join(errors.New("velox: engine_test.hnote: has no relation 3"), bad)})))
			return nil
		}, "velox: engine_test.hnote: hook: has no relation 3\nbad"},
		{func(*velox.Mutation[hnote]) error { return errors.Join(errors.New("velox: testmodel.Other: x"), bad) },
			"velox: engine_test.hnote: hook: velox: testmodel.Other: x\nbad"},
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
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	ctx := withHook(func(m *velox.Mutation[hnote]) error {
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
