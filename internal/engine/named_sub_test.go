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
		{"two types", velox.FieldInSelect("title", "Org", "id"), "the key types differ"},
		{"a float and text", velox.FieldInSelect("score", "Org", "name"), "the key types differ"},
		{"bad condition", velox.FieldInSelect("org_id", "Org", "id", velox.FieldEQ("nope", 1)), "no column"},
	} {
		_, _, err := nmSQL(t, c.p)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}
