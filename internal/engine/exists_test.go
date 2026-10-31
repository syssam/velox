package engine_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestRenderExists is Review Focus 3: the subquery's table is aliased, the
// correlation names the outer table (or the outer alias when nested), and the
// inner conditions are unqualified, so they bind to the innermost table.
func TestRenderExists(t *testing.T) {
	em := infoOf[employee]()
	co := infoOf[company]()
	staff, err := engine.InfoRels(co)[0].Resolve(co, targetOf[employee]())
	if err != nil {
		t.Fatal(err)
	}
	boss, err := engine.InfoRels(em)[0].Resolve(em, targetOf[employee]())
	if err != nil {
		t.Fatal(err)
	}
	hasBoss := engine.Node{Op: engine.OpExists, Col: 0, V: boss}
	st := &velox.Statement{Op: velox.OpSelect, Entity: co, Columns: []int{0}, Where: []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpExists, Col: 0, V: staff,
		Kids: []engine.Node{{Op: engine.OpEQ, Col: 1, V: int64(7)}, hasBoss}})}}
	for d, want := range map[velox.Dialect]string{
		velox.Postgres: `SELECT "id" FROM "company" WHERE EXISTS (SELECT 1 FROM "employee" AS "vx1" WHERE "vx1"."company_id" = "company"."id" AND "company_id" = $1 AND EXISTS (SELECT 1 FROM "employee" AS "vx2" WHERE "vx2"."id" = "vx1"."boss_id"))`,
		velox.MySQL:    "SELECT `id` FROM `company` WHERE EXISTS (SELECT 1 FROM `employee` AS `vx1` WHERE `vx1`.`company_id` = `company`.`id` AND `company_id` = ? AND EXISTS (SELECT 1 FROM `employee` AS `vx2` WHERE `vx2`.`id` = `vx1`.`boss_id`))",
	} {
		got, args := render(d, st)
		if got != want || len(args) != 1 {
			t.Errorf("%v:\n got %s\nwant %s", d, got, want)
		}
		var c engine.RenderCache
		cachedRender(&c, d, st)
		if cached, _ := cachedRender(&c, d, st); cached != want {
			t.Errorf("%v cached: %s", d, cached)
		}
	}
	self := &velox.Statement{Op: velox.OpDelete, Entity: em, Where: []velox.Predicate{engine.NewPredicate(hasBoss)}}
	if got, _ := render(velox.SQLite, self); got != `DELETE FROM "employee" WHERE EXISTS (SELECT 1 FROM "employee" AS "vx1" WHERE "vx1"."id" = "employee"."boss_id")` {
		t.Errorf("self relation: %s", got)
	}
}

func TestCacheKeepsRelationsApart(t *testing.T) {
	em := infoOf[employee]()
	boss, _ := engine.InfoRels(em)[0].Resolve(em, targetOf[employee]())
	comp, _ := engine.InfoRels(em)[1].Resolve(em, targetOf[company]())
	var c engine.RenderCache
	a, _ := cachedRender(&c, velox.Postgres, &velox.Statement{Op: velox.OpSelect, Entity: em, Where: []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpExists, Col: 0, V: boss})}})
	b, _ := cachedRender(&c, velox.Postgres, &velox.Statement{Op: velox.OpSelect, Entity: em, Where: []velox.Predicate{engine.NewPredicate(engine.Node{Op: engine.OpExists, Col: 1, V: comp})}})
	if a == b {
		t.Errorf("two relations share SQL: %s", a)
	}
}

// TestCachedExistsRendersLikeRender renders a statement with a nested EXISTS,
// a trailing condition and a Limit through the cache twice (a miss, then a
// hit on another statement of the same shape), and compares the hit's SQL and
// arguments with render's.
func TestCachedExistsRendersLikeRender(t *testing.T) {
	em := infoOf[employee]()
	co := infoOf[company]()
	staff, err := engine.InfoRels(co)[0].Resolve(co, targetOf[employee]())
	if err != nil {
		t.Fatal(err)
	}
	boss, err := engine.InfoRels(em)[0].Resolve(em, targetOf[employee]())
	if err != nil {
		t.Fatal(err)
	}
	mk := func(inner, id, limit int64) *velox.Statement {
		return &velox.Statement{Op: velox.OpSelect, Entity: co, Limit: int(limit), Where: []velox.Predicate{
			engine.NewPredicate(engine.Node{Op: engine.OpExists, Col: 0, V: staff, Kids: []engine.Node{
				{Op: engine.OpEQ, Col: 1, V: inner},
				{Op: engine.OpExists, Col: 0, V: boss},
			}}),
			engine.NewPredicate(engine.Node{Op: engine.OpEQ, Col: 0, V: id}),
		}}
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		var c engine.RenderCache
		cachedRender(&c, d, mk(7, 3, 5)) // miss
		hit, hitArgs := cachedRender(&c, d, mk(8, 4, 6))
		want, wantArgs := render(d, mk(8, 4, 6))
		if hit != want || !reflect.DeepEqual(hitArgs, wantArgs) || len(wantArgs) != 3 {
			t.Errorf("%v:\n got %s %v\nwant %s %v", d, hit, hitArgs, want, wantArgs)
		}
		if c.Size() != 1 {
			t.Errorf("%v: %d shapes", d, c.Size())
		}
	}
}

// TestRewriteHookMayAddAnUnresolvedRelation: a Rewrite hook's condition made
// by HasWhere is a RelRef, which CheckColumns and the scope resolve; the
// caller's own conditions were resolved before the hooks ran.
func TestRewriteHookMayAddAnUnresolvedRelation(t *testing.T) {
	co := infoOf[company]()
	ref := func(i int32) engine.Node {
		return engine.Node{Op: engine.OpExists, Col: i, V: engine.RelRef{Owner: func() (*velox.EntityInfo, error) { return co, nil }, Target: targetOf[employee](), I: i}}
	}
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Where = append(st.Where, engine.NewPredicate(ref(0)))
		return nil
	}))
	st := velox.Statement{Op: velox.OpSelect, Entity: co, Columns: []int{0}, Limit: 1, Where: []velox.Predicate{engine.NewPredicate(ref(0))}}
	if err := engineOf(db).Run(context.Background(), &st, func(engine.Rows) error { return nil }, nil); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(st.SQL, "EXISTS"); n != 2 {
		t.Errorf("%d EXISTS in %s, want 2", n, st.SQL)
	}
	// An unresolvable one fails the statement, and sends nothing.
	e.Calls = 0
	bad := velox.Statement{Op: velox.OpSelect, Entity: co, Columns: []int{0}, Limit: 1, Where: []velox.Predicate{engine.NewPredicate(ref(9))}}
	if err := engineOf(db).Run(context.Background(), &bad, func(engine.Rows) error { return nil }, nil); err == nil || !strings.Contains(err.Error(), "has no relation 9") || e.Calls != 0 {
		t.Errorf("%v (calls %d)", err, e.Calls)
	}
}
