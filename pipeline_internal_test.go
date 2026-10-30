package velox

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestWritesGoThroughExec(t *testing.T) {
	e := &FakeExec{Affected: 3, LastID: 9, HasLastID: true}
	db := OpenFake(Postgres, e)
	st := Statement{Op: OpUpdate, Entity: invInfo, AllRows: true, Set: []Assignment{{Column: 3, Op: AssignSet, Value: 1.5}}}
	res, err := execWrite(context.Background(), db, &st)
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
	update := func() Statement {
		return Statement{Op: OpUpdate, Entity: invInfo, AllRows: true, Set: []Assignment{{Column: 3, Op: AssignSet, Value: 1.5}}}
	}
	t.Run("a hook's edits do not reach the caller", func(t *testing.T) {
		edit := func(_ context.Context, st *Statement) error {
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
		db := OpenFake(Postgres, &FakeExec{}, WithRewrite(edit))
		st := update()
		set := st.Set
		if _, err := execWrite(ctx, db, &st); err != nil {
			t.Fatal(err)
		}
		if set[0].Value != 1.5 {
			t.Errorf("the hook changed the caller's Set: %v", set[0].Value)
		}
		row := []any{"orig"}
		cf := &ConflictClause{Columns: []int{1}, Update: []int{2}}
		ins := Statement{Op: OpInsert, Entity: invInfo, Columns: []int{2}, Rows: [][]any{row}, Conflict: cf}
		if _, err := execWrite(ctx, db, &ins); err != nil {
			t.Fatal(err)
		}
		if row[0] != "orig" || cf.Update[0] != 2 {
			t.Errorf("the hook changed the caller's Rows or Conflict: %v %v", row, cf.Update)
		}
	})
	for name, c := range map[string]struct {
		hook Rewrite
		st   Statement
		want string
	}{
		"kind": {func(_ context.Context, st *Statement) error { st.Op = OpDelete; return nil }, update(), "changed the statement's kind"},
		"set column": {func(_ context.Context, st *Statement) error {
			st.Set = append(st.Set, Assignment{Column: 99, Op: AssignSet})
			return nil
		}, update(), "set column 99"},
		"returning column": {func(_ context.Context, st *Statement) error { st.Returning = []int{42}; return nil },
			Statement{Op: OpInsert, Entity: invInfo, Columns: []int{1}, Rows: [][]any{{int64(1)}}}, "set column 42"},
		"row width": {func(_ context.Context, st *Statement) error { st.Rows[0] = append(st.Rows[0], 2); return nil },
			Statement{Op: OpInsert, Entity: invInfo, Columns: []int{1}, Rows: [][]any{{int64(1)}}}, "a row of 2 values for 1 columns"},
		"empty conflict": {func(_ context.Context, st *Statement) error { st.Conflict.Columns = nil; return nil },
			Statement{Op: OpInsert, Entity: invInfo, Columns: []int{1}, Rows: [][]any{{int64(1)}}, Conflict: &ConflictClause{Columns: []int{1}, DoNothing: true}}, "no conflict columns"},
	} {
		t.Run(name, func(t *testing.T) {
			e := &FakeExec{}
			db := OpenFake(Postgres, e, WithRewrite(c.hook))
			st := c.st
			if _, err := execWrite(ctx, db, &st); err == nil || !strings.Contains(err.Error(), c.want) || e.Calls != 0 {
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
	co := (*company)(nil).VeloxMeta().info
	staff, err := co.rels[0].resolve(co, targetOf[employee]())
	if err != nil {
		t.Fatal(err)
	}
	broken := errors.New("velox: the captured condition is invalid")
	for name, c := range map[string]struct {
		hook Rewrite
		want string
	}{
		"smaller entity": {func(_ context.Context, st *Statement) error {
			st.Entity = &EntityInfo{Name: "tiny"}
			return nil
		}, "not a copy of it"},
		"copy with another policy": {func(_ context.Context, st *Statement) error {
			info := *st.Entity
			info.policy = &policy{}
			st.Entity = &info
			return nil
		}, "a copy whose tenant column or policy differs"},
		"invalid predicate": {func(_ context.Context, st *Statement) error {
			st.Where = append(st.Where, Predicate{node{op: opInvalid, v: broken}})
			return nil
		}, broken.Error()},
	} {
		t.Run(name, func(t *testing.T) {
			e := &FakeExec{}
			db := OpenFake(Postgres, e, WithRewrite(c.hook))
			st := Statement{Op: OpDelete, Entity: co, Where: []Predicate{{node{op: opExists, col: 0, v: staff}}}}
			if _, err := execWrite(ctx, db, &st); err == nil || !strings.Contains(err.Error(), c.want) || e.Calls != 0 {
				t.Errorf("%v (calls %d), want an error containing %q", err, e.Calls, c.want)
			}
		})
	}
}
