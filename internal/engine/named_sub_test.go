package engine_test

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
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
	// In an Or, beside a condition on the owner.
	s, _, err = nmSQL(t, velox.Or(velox.FieldEQ("rank", int8(1)), velox.FieldInSelect("org_id", "Org", "id", velox.FieldEQ("name", "x"))))
	if err != nil || !strings.Contains(s, " OR EXISTS (SELECT 1 FROM") {
		t.Errorf("Or: %s, %v", s, err)
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
		{"not linked", velox.FieldInSelect("org_id", "Doc", "id"), "entity Doc is not linked"},
		{"owner column", velox.FieldInSelect("nope", "Org", "id"), `no column "nope"`},
		{"key column", velox.FieldInSelect("org_id", "Org", "nope"), `Org has no column "nope"`},
		{"two types", velox.FieldInSelect("org_id", "Org", "name"), "a subquery compares columns of one type"},
		{"bad condition", velox.FieldInSelect("org_id", "Org", "id", velox.FieldEQ("nope", 1)), "no column"},
	} {
		_, _, err := nmSQL(t, c.p)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}
