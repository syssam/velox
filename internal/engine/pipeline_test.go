package engine_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/engine"
	"github.com/syssam/velox2/internal/fakeconn"
)

func TestWritesGoThroughExec(t *testing.T) {
	e := &fakeconn.Exec{Affected: 3, LastID: 9, HasLastID: true}
	db := openFake(velox.Postgres, e)
	st := velox.Statement{Op: velox.OpUpdate, Entity: invInfo, AllRows: true, Set: []velox.Assignment{{Column: 3, Op: velox.AssignSet, Value: 1.5}}}
	res, err := engine.ExecWrite(context.Background(), engineOf(db), &st)
	if err != nil || res.Affected != 3 || res.LastID != 9 || !res.HasLastID {
		t.Fatalf("%+v, %v", res, err)
	}
	if e.SQL != `UPDATE "invoices" SET "total" = $1` || !reflect.DeepEqual(e.Args, []any{1.5}) {
		t.Errorf("%s %v", e.SQL, e.Args)
	}
}

// TestHooksOnWrites is Review Focus 4.
func TestHooksOnWrites(t *testing.T) {
	ctx := context.Background()
	update := func() velox.Statement {
		return velox.Statement{Op: velox.OpUpdate, Entity: invInfo, AllRows: true, Set: []velox.Assignment{{Column: 3, Op: velox.AssignSet, Value: 1.5}}}
	}
	t.Run("a hook's edits do not reach the caller", func(t *testing.T) {
		edit := func(_ context.Context, st *velox.Statement) error {
			if len(st.Set) > 0 {
				st.Set[0].Value = 99.0
			}
			if st.Rows != nil {
				st.Rows[0][0] = "changed"
			}
			if st.Conflict != nil {
				st.Conflict.Update[0] = 1
			}
			return nil
		}
		db := openFake(velox.Postgres, &fakeconn.Exec{}, velox.WithRewrite(edit))
		st := update()
		set := st.Set
		if _, err := engine.ExecWrite(ctx, engineOf(db), &st); err != nil {
			t.Fatal(err)
		}
		if set[0].Value != 1.5 {
			t.Errorf("the hook changed the caller's Set: %v", set[0].Value)
		}
		row := []any{"orig"}
		cf := &velox.ConflictClause{Columns: []int{1}, Update: []int{2}}
		ins := velox.Statement{Op: velox.OpInsert, Entity: invInfo, Columns: []int{2}, Rows: [][]any{row}, Conflict: cf}
		if _, err := engine.ExecWrite(ctx, engineOf(db), &ins); err != nil {
			t.Fatal(err)
		}
		if row[0] != "orig" || cf.Update[0] != 2 {
			t.Errorf("the hook changed the caller's Rows or Conflict: %v %v", row, cf.Update)
		}
	})
	for name, c := range map[string]struct {
		hook velox.Rewrite
		st   velox.Statement
		want string
	}{
		"kind": {func(_ context.Context, st *velox.Statement) error { st.Op = velox.OpDelete; return nil }, update(), "changed the statement's kind"},
		"set column": {func(_ context.Context, st *velox.Statement) error {
			st.Set = append(st.Set, velox.Assignment{Column: 99, Op: velox.AssignSet})
			return nil
		}, update(), "set column 99"},
		"returning column": {func(_ context.Context, st *velox.Statement) error { st.Returning = []int{42}; return nil },
			velox.Statement{Op: velox.OpInsert, Entity: invInfo, Columns: []int{1}, Rows: [][]any{{int64(1)}}}, "set column 42"},
		"row width": {func(_ context.Context, st *velox.Statement) error { st.Rows[0] = append(st.Rows[0], 2); return nil },
			velox.Statement{Op: velox.OpInsert, Entity: invInfo, Columns: []int{1}, Rows: [][]any{{int64(1)}}}, "a row of 2 values for 1 columns"},
		"empty conflict": {func(_ context.Context, st *velox.Statement) error { st.Conflict.Columns = nil; return nil },
			velox.Statement{Op: velox.OpInsert, Entity: invInfo, Columns: []int{1}, Rows: [][]any{{int64(1)}}, Conflict: &velox.ConflictClause{Columns: []int{1}, DoNothing: true}}, "no conflict columns"},
	} {
		t.Run(name, func(t *testing.T) {
			e := &fakeconn.Exec{}
			db := openFake(velox.Postgres, e, velox.WithRewrite(c.hook))
			st := c.st
			if _, err := engine.ExecWrite(ctx, engineOf(db), &st); err == nil || !strings.Contains(err.Error(), c.want) || e.Calls != 0 {
				t.Errorf("%v (calls %d), want an error containing %q", err, e.Calls, c.want)
			}
		})
	}
}

// TestHooksAndConditionsOfRelations: a rewrite hook that points the statement
// at an entity other than a copy of its own, or appends a condition that carries an
// error, gets an error, not a panic or a malformed statement.
func TestHooksAndConditionsOfRelations(t *testing.T) {
	ctx := context.Background()
	co := infoOf[company]()
	staff, err := engine.InfoRels(co)[0].Resolve(co, targetOf[employee]())
	if err != nil {
		t.Fatal(err)
	}
	broken := errors.New("velox: the captured condition is invalid")
	for name, c := range map[string]struct {
		hook velox.Rewrite
		want string
	}{
		"smaller entity": {func(_ context.Context, st *velox.Statement) error {
			st.Entity = &velox.EntityInfo{Name: "tiny"}
			return nil
		}, "not a copy of it"},
		"copy with another policy": {func(_ context.Context, st *velox.Statement) error {
			info := *st.Entity
			engine.SetInfoPolicy(&info, &engine.EntityPolicy{})
			st.Entity = &info
			return nil
		}, "a copy whose tenant column or policy differs"},
		"invalid predicate": {func(_ context.Context, st *velox.Statement) error {
			st.Where = append(st.Where, engine.NewPredicate(engine.Node{Op: engine.OpInvalid, V: broken}))
			return nil
		}, broken.Error()},
	} {
		t.Run(name, func(t *testing.T) {
			e := &fakeconn.Exec{}
			db := openFake(velox.Postgres, e, velox.WithRewrite(c.hook))
			st := velox.Statement{Op: velox.OpDelete, Entity: co, Where: []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpExists, Col: 0, V: staff})}}
			if _, err := engine.ExecWrite(ctx, engineOf(db), &st); err == nil || !strings.Contains(err.Error(), c.want) || e.Calls != 0 {
				t.Errorf("%v (calls %d), want an error containing %q", err, e.Calls, c.want)
			}
		})
	}
}
