package velox

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// TestRenderExists is Review Focus 3: the subquery's table is aliased, the
// correlation names the outer table (or the outer alias when nested), and the
// inner conditions are unqualified, so they bind to the innermost table.
func TestRenderExists(t *testing.T) {
	em := (*employee)(nil).VeloxMeta().info
	co := (*company)(nil).VeloxMeta().info
	staff, err := infoRels(co)[0].resolve(co, targetOf[employee]())
	if err != nil {
		t.Fatal(err)
	}
	boss, err := infoRels(em)[0].resolve(em, targetOf[employee]())
	if err != nil {
		t.Fatal(err)
	}
	hasBoss := node{op: opExists, col: 0, v: boss}
	st := &Statement{Op: OpSelect, Entity: co, Columns: []int{0}, Where: []Predicate{{node{op: opExists, col: 0, v: staff,
		kids: []node{{op: opEQ, col: 1, v: int64(7)}, hasBoss}}}}}
	for d, want := range map[Dialect]string{
		Postgres: `SELECT "id" FROM "company" WHERE EXISTS (SELECT 1 FROM "employee" AS "vx1" WHERE "vx1"."company_id" = "company"."id" AND "company_id" = $1 AND EXISTS (SELECT 1 FROM "employee" AS "vx2" WHERE "vx2"."id" = "vx1"."boss_id"))`,
		MySQL:    "SELECT `id` FROM `company` WHERE EXISTS (SELECT 1 FROM `employee` AS `vx1` WHERE `vx1`.`company_id` = `company`.`id` AND `company_id` = ? AND EXISTS (SELECT 1 FROM `employee` AS `vx2` WHERE `vx2`.`id` = `vx1`.`boss_id`))",
	} {
		got, args := render(d, st)
		if got != want || len(args) != 1 {
			t.Errorf("%v:\n got %s\nwant %s", d, got, want)
		}
		var c renderCache
		c.render(d, st)
		if cached, _ := c.render(d, st); cached != want {
			t.Errorf("%v cached: %s", d, cached)
		}
	}
	self := &Statement{Op: OpDelete, Entity: em, Where: []Predicate{{hasBoss}}}
	if got, _ := render(SQLite, self); got != `DELETE FROM "employee" WHERE EXISTS (SELECT 1 FROM "employee" AS "vx1" WHERE "vx1"."id" = "employee"."boss_id")` {
		t.Errorf("self relation: %s", got)
	}
}

func TestCacheKeepsRelationsApart(t *testing.T) {
	em := (*employee)(nil).VeloxMeta().info
	boss, _ := infoRels(em)[0].resolve(em, targetOf[employee]())
	comp, _ := infoRels(em)[1].resolve(em, targetOf[company]())
	var c renderCache
	a, _ := c.render(Postgres, &Statement{Op: OpSelect, Entity: em, Where: []Predicate{{node{op: opExists, col: 0, v: boss}}}})
	b, _ := c.render(Postgres, &Statement{Op: OpSelect, Entity: em, Where: []Predicate{{node{op: opExists, col: 1, v: comp}}}})
	if a == b {
		t.Errorf("two relations share SQL: %s", a)
	}
}

// TestCachedExistsRendersLikeRender renders a statement with a nested EXISTS,
// a trailing condition and a Limit through the cache twice (a miss, then a
// hit on another statement of the same shape), and compares the hit's SQL and
// arguments with render's.
func TestCachedExistsRendersLikeRender(t *testing.T) {
	em := (*employee)(nil).VeloxMeta().info
	co := (*company)(nil).VeloxMeta().info
	staff, err := infoRels(co)[0].resolve(co, targetOf[employee]())
	if err != nil {
		t.Fatal(err)
	}
	boss, err := infoRels(em)[0].resolve(em, targetOf[employee]())
	if err != nil {
		t.Fatal(err)
	}
	mk := func(inner, id, limit int64) *Statement {
		return &Statement{Op: OpSelect, Entity: co, Limit: int(limit), Where: []Predicate{
			{node{op: opExists, col: 0, v: staff, kids: []node{
				{op: opEQ, col: 1, v: inner},
				{op: opExists, col: 0, v: boss},
			}}},
			{node{op: opEQ, col: 0, v: id}},
		}}
	}
	for _, d := range []Dialect{Postgres, MySQL, SQLite} {
		var c renderCache
		c.render(d, mk(7, 3, 5)) // miss
		hit, hitArgs := c.render(d, mk(8, 4, 6))
		want, wantArgs := render(d, mk(8, 4, 6))
		if hit != want || !reflect.DeepEqual(hitArgs, wantArgs) || len(wantArgs) != 3 {
			t.Errorf("%v:\n got %s %v\nwant %s %v", d, hit, hitArgs, want, wantArgs)
		}
		if c.size() != 1 {
			t.Errorf("%v: %d shapes", d, c.size())
		}
	}
}

// TestRewriteHookMayAddAnUnresolvedRelation: a Rewrite hook's condition made
// by HasWhere is a relRef, which checkColumns and the scope resolve; the
// caller's own conditions were resolved before the hooks ran.
func TestRewriteHookMayAddAnUnresolvedRelation(t *testing.T) {
	co := (*company)(nil).VeloxMeta().info
	ref := func(i int32) node {
		return node{op: opExists, col: i, v: relRef{owner: func() (*EntityInfo, error) { return co, nil }, target: targetOf[employee](), i: i}}
	}
	e := &FakeExec{}
	db := OpenFake(Postgres, e, WithRewrite(func(_ context.Context, st *Statement) error {
		st.Where = append(st.Where, Predicate{ref(0)})
		return nil
	}))
	st := Statement{Op: OpSelect, Entity: co, Columns: []int{0}, Limit: 1, Where: []Predicate{{ref(0)}}}
	if err := db.run(context.Background(), &st, func(rows) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(st.SQL, "EXISTS"); n != 2 {
		t.Errorf("%d EXISTS in %s, want 2", n, st.SQL)
	}
	// An unresolvable one fails the statement, and sends nothing.
	e.Calls = 0
	bad := Statement{Op: OpSelect, Entity: co, Columns: []int{0}, Limit: 1, Where: []Predicate{{ref(9)}}}
	if err := db.run(context.Background(), &bad, func(rows) error { return nil }, nil); err == nil || !strings.Contains(err.Error(), "has no relation 9") || e.Calls != 0 {
		t.Errorf("%v (calls %d)", err, e.Calls)
	}
}
