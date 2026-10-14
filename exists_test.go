package velox

import "testing"

// TestRenderExists is Review Focus 3: the subquery's table is aliased, the
// correlation names the outer table (or the outer alias when nested), and the
// inner conditions are unqualified, so they bind to the innermost table.
func TestRenderExists(t *testing.T) {
	em := (*employee)(nil).VeloxMeta().info
	co := (*company)(nil).VeloxMeta().info
	staff, err := co.rels[0].resolve(co)
	if err != nil {
		t.Fatal(err)
	}
	boss, err := em.rels[0].resolve(em)
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
	boss, _ := em.rels[0].resolve(em)
	comp, _ := em.rels[1].resolve(em)
	var c renderCache
	a, _ := c.render(Postgres, &Statement{Op: OpSelect, Entity: em, Where: []Predicate{{node{op: opExists, col: 0, v: boss}}}})
	b, _ := c.render(Postgres, &Statement{Op: OpSelect, Entity: em, Where: []Predicate{{node{op: opExists, col: 1, v: comp}}}})
	if a == b {
		t.Errorf("two relations share SQL: %s", a)
	}
}
