package engine_test

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/untyped"
)

// FieldInSelect is column IN (SELECT key FROM entity WHERE ps), written as the
// EXISTS a typed InSelect writes: the conditions name the target's columns and
// nest, and a Not of it is NOT EXISTS.
func TestFieldInSelect(t *testing.T) {
	s, args, err := nmSQL(t, velox.FieldInSelect("org_id", "Org", "id", velox.FieldEQ("name", "acme")))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`EXISTS (SELECT 1 FROM "nm_orgs" AS`, `"id" = `, `"org_id"`, `"name" = `} {
		if !strings.Contains(s, want) {
			t.Errorf("SQL %s lacks %s", s, want)
		}
	}
	if len(args) == 0 || args[0] != "acme" { // the LIMIT follows
		t.Errorf("args %v", args)
	}
	s, _, err = nmSQL(t, velox.Not(velox.FieldInSelect("org_id", "Org", "id")))
	if err != nil || !strings.Contains(s, "NOT (EXISTS (SELECT 1 FROM") {
		t.Errorf("Not: %s, %v", s, err)
	}
	// The entity named by its table, as a name from configuration may: the
	// same statement.
	byName, _, _ := nmSQL(t, velox.FieldInSelect("org_id", "Org", "id", velox.FieldEQ("name", "acme")))
	byTable, _, err := nmSQL(t, velox.FieldInSelect("org_id", "nm_orgs", "id", velox.FieldEQ("name", "acme")))
	if err != nil || byTable != byName {
		t.Errorf("by table: %s, %v; by name %s", byTable, err, byName)
	}
	// A nullable column (*int64) keys against a not null one (int64), as a typed InSelect's.
	if s, _, err = nmSQL(t, velox.FieldInSelect("peer_id", "Org", "id")); err != nil || !strings.Contains(s, "EXISTS (SELECT 1 FROM") {
		t.Errorf("nullable key: %s, %v", s, err)
	}
	// In an Or, beside a condition on the owner.
	s, _, err = nmSQL(t, velox.Or(velox.FieldEQ("rank", int8(1)), velox.FieldInSelect("org_id", "Org", "id", velox.FieldEQ("name", "x"))))
	if err != nil || !strings.Contains(s, " OR EXISTS (SELECT 1 FROM") {
		t.Errorf("Or: %s, %v", s, err)
	}
}

// An integer column in a text key column is compared as text, the integer
// cast on the outer side (ADR 0091).
func TestFieldInSelectTextKey(t *testing.T) {
	s, _, err := nmSQL(t, velox.FieldInSelect("org_id", "Org", "name"))
	if want := `"vx1"."name" = CAST("nm_docs"."org_id" AS TEXT)`; err != nil || !strings.Contains(s, want) {
		t.Errorf("got %s, %v; want it to hold %s", s, err, want)
	}
	// A nullable integer too.
	s, _, err = nmSQL(t, velox.FieldInSelect("peer_id", "Org", "name"))
	if want := `"vx1"."name" = CAST("nm_docs"."peer_id" AS TEXT)`; err != nil || !strings.Contains(s, want) {
		t.Errorf("got %s, %v; want it to hold %s", s, err, want)
	}
	// MySQL's is CONCAT, whose text takes the key column's collation.
	db := velox.OpenConn(velox.MySQL, fakeConn(nil))
	s, _, err = untyped.Query(db, untyped.TableOf[nmDoc]()).Where(velox.FieldInSelect("org_id", "Org", "name")).Limit(1).SQL(ctx)
	if want := "`vx1`.`name` = CONCAT(`nm_docs`.`org_id`)"; err != nil || !strings.Contains(s, want) {
		t.Errorf("MySQL: got %s, %v; want it to hold %s", s, err, want)
	}
}

// nmInt64Text is the guard of a text column against an integer key, as
// rendered (ADR 0161).
const nmInt64Text = `^(0|-{0,1}[1-9][0-9]{0,17}|-{0,1}[1-8][0-9]{18}|-{0,1}9([01][0-9]{17}|2[01][0-9]{16}|22[0-2][0-9]{15}|223[0-2][0-9]{14}|2233[0-6][0-9]{13}|22337[01][0-9]{12}|2233720[0-2][0-9]{10}|22337203[0-5][0-9]{9}|223372036[0-7][0-9]{8}|2233720368[0-4][0-9]{7}|22337203685[0-3][0-9]{6}|223372036854[0-6][0-9]{5}|2233720368547[0-6][0-9]{4}|22337203685477[0-4][0-9]{3}|223372036854775[0-7][0-9]{2}|22337203685477580[0-7])|-9223372036854775808)$`

// A text column against an integer key keeps the key bare, so its index
// serves, and casts the column when it spells an int64, NULL otherwise
// (ADR 0161), by FieldInSelect and FieldInSelectRaw, and in a Not.
func TestFieldInSelectTextColumn(t *testing.T) {
	want := map[velox.Dialect]string{
		velox.Postgres: `SELECT "id", "org_id", "title", "note", "rank", "status", "at", "score", "peer_id", "visible" FROM "nm_docs" WHERE EXISTS (SELECT 1 FROM "nm_orgs" AS "vx1" WHERE "vx1"."id" = CASE WHEN "nm_docs"."title" COLLATE "C" ~ '` + nmInt64Text + `' THEN CAST("nm_docs"."title" AS BIGINT) END) LIMIT 1`,
		velox.MySQL:    "SELECT `id`, `org_id`, `title`, `note`, `rank`, `status`, `at`, `score`, `peer_id`, `visible` FROM `nm_docs` WHERE EXISTS (SELECT 1 FROM `nm_orgs` AS `vx1` WHERE `vx1`.`id` = CASE WHEN `nm_docs`.`title` REGEXP '" + nmInt64Text + "' AND `nm_docs`.`title` NOT REGEXP '[^-0-9]' THEN CAST(`nm_docs`.`title` AS SIGNED) END) LIMIT 1",
		velox.SQLite:   `SELECT "id", "org_id", "title", "note", "rank", "status", "at", "score", "peer_id", "visible" FROM "nm_docs" WHERE EXISTS (SELECT 1 FROM "nm_orgs" AS "vx1" WHERE "vx1"."id" = CASE WHEN CAST(CAST("nm_docs"."title" AS INTEGER) AS TEXT) = "nm_docs"."title" THEN CAST("nm_docs"."title" AS INTEGER) END) LIMIT 1`,
	}
	for _, p := range []velox.Predicate{
		velox.FieldInSelect("title", "Org", "id"),
		velox.FieldInSelectRaw("r", "title", "Org", "id"),
	} {
		for d, w := range want {
			s, args, err := nmSQLOn(t, d, p)
			if err != nil || s != w || len(args) != 0 {
				t.Errorf("%s:\n got %s, %v, %v\nwant %s", d, s, args, err, w)
			}
			s, _, err = nmSQLOn(t, d, velox.Not(p))
			if w := strings.Replace(strings.Replace(w, "WHERE EXISTS", "WHERE NOT (EXISTS", 1), ") LIMIT", ")) LIMIT", 1); err != nil || s != w {
				t.Errorf("%s Not:\n got %s, %v\nwant %s", d, s, err, w)
			}
		}
	}
	// A nullable text column too.
	s, _, err := nmSQL(t, velox.FieldInSelect("note", "Org", "id"))
	if want := `"vx1"."id" = CASE WHEN "nm_docs"."note" COLLATE "C" ~ '` + nmInt64Text + `' THEN CAST("nm_docs"."note" AS BIGINT) END`; err != nil || !strings.Contains(s, want) {
		t.Errorf("nullable: got %s, %v; want it to hold %s", s, err, want)
	}
}

// FieldInSelect fails the statement, naming what is wrong, for an entity that
// is not linked, a column or key that is not there, columns of two types, and
// a bad condition in the subquery.
func TestFieldInSelectRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		p    velox.Predicate
		want string
	}{
		{"unknown entity", velox.FieldInSelect("org_id", "Doc", "id"), `no entity or table "Doc"`},
		{"owner column", velox.FieldInSelect("nope", "Org", "id"), `no column "nope"`},
		{"key column", velox.FieldInSelect("org_id", "Org", "nope"), `Org has no column "nope"`},
		{"two types", velox.FieldInSelect("at", "Org", "id"), "the key types differ"},
		{"raw: no reason", velox.FieldInSelectRaw(" ", "org_id", "Org", "id"), "needs a reason"},
		{"raw: key column", velox.FieldInSelectRaw("r", "org_id", "Org", "nope"), `FieldInSelectRaw: Org has no column "nope"`},
		{"a float and text", velox.FieldInSelect("score", "Org", "name"), "the key types differ"},
		{"bad condition", velox.FieldInSelect("org_id", "Org", "id", velox.FieldEQ("nope", 1)), "no column"},
	} {
		_, _, err := nmSQL(t, c.p)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}
