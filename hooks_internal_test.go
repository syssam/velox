package velox

import (
	"context"
	"errors"
	"testing"
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
	if err := db.Save(ctx, &x, hn.Title); err != nil {
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
	if err := db.Save(ctx, &x, hn.Title); err == nil {
		t.Fatal("no error")
	}
	e.Affected = 0
	if err := db.Save(ctx, &x, hn.Title); !errors.Is(err, ErrNotFound) {
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
	if err := db.Save(ctx, &x, hn.Title); err != nil {
		t.Fatal(err)
	}
	if x.Note != "old" || x.Due != nil {
		t.Errorf("Note %q, Due %v", x.Note, x.Due)
	}
	ctx = withHook(func(m *Mutation[hnote]) error {
		m.Set(hn.Due.Set(7))
		return nil
	})
	if err := db.Save(ctx, &x, hn.Title); err != nil {
		t.Fatal(err)
	}
	if x.Due == nil || *x.Due != 7 || five != 5 {
		t.Errorf("Due %v, five %d", x.Due, five)
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

func TestMutationValue(t *testing.T) {
	e := &FakeExec{Affected: 1}
	db := OpenFake(Postgres, e)
	type seen struct {
		op           Op
		title, note  any
		okT, okN, ok bool
	}
	var s seen
	ctx := withHook(func(m *Mutation[hnote]) error {
		s.op = m.Op()
		s.title, s.okT = m.Value(hn.Title)
		s.note, s.okN = m.Value(hn.Note)
		_, s.ok = m.Value(hn.N) // an Add assigns no value
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
	if s.op != OpDelete || s.okT {
		t.Errorf("on a delete the hook saw %+v", s)
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
