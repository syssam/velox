package engine

import (
	"reflect"
	"strings"
	"testing"
)

type etA struct{}
type etB struct{}

// TestEntityTableLookup: an entity is found by its name in the schema, else by
// its table; a name that is one entity's and another's table, or the table of
// two entities, is ambiguous, and an unknown name is refused. A relation's
// target is found by its Go type.
func TestEntityTableLookup(t *testing.T) {
	info := map[string]*EntityInfo{"A": {Name: "A"}, "B": {Name: "B"}, "C": {Name: "C"}, "D": {Name: "D"}, "E": {Name: "E"}}
	link := func(name, table, typ string) EntityLink {
		return EntityLink{Name: name, Table: table, Type: typ, Get: func() (*EntityInfo, error) { return info[name], nil }}
	}
	var tb EntityTable
	tb.Load(func() []EntityLink {
		return []EntityLink{
			link("A", "as", TypeKey(reflect.TypeFor[etA]())),
			link("B", "A", TypeKey(reflect.TypeFor[etB]())), // B's table is A's name
			link("C", "shared", ""),
			link("D", "shared", ""), // in another database schema
			link("E", "E", ""),      // its name and its table are one
		}
	})
	for name, want := range map[string]string{"as": "A", "C": "C", "E": "E"} {
		if in, err := tb.entity(name); err != nil || in.Name != want {
			t.Errorf("%q: %v, %v, want %s", name, in, err, want)
		}
	}
	for name, want := range map[string]string{
		"A":      "the name of entity A and the table of another",
		"shared": "the table of 2 entities",
		"nope":   `no entity or table "nope"`,
	} {
		if _, err := tb.entity(name); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", name, err, want)
		}
	}
	if in, ok, err := tb.target(TypeKey(reflect.TypeFor[etB]())); !ok || err != nil || in.Name != "B" {
		t.Errorf("target etB: %v %v %v", in, ok, err)
	}
	if _, ok, _ := tb.target("x.Nope"); ok {
		t.Error("an unknown target was found")
	}
	// Load builds once: a second build is not called.
	if tb.Load(func() []EntityLink { t.Error("built twice"); return nil }) != &tb {
		t.Error("Load returned another table")
	}
	// A descriptor with no table names it in the error.
	if _, err := (&EntityInfo{Name: "X"}).entityTable(); err == nil || !strings.Contains(err.Error(), "X has no entity table") {
		t.Errorf("no table: %v", err)
	}
}

// TestTypeKeyMatchesTargetName: the key of a relation's target, from its One[T]
// or Many[T], is the key of T, so the table finds it.
func TestTypeKeyMatchesTargetName(t *testing.T) {
	type holder[T any] struct{}
	if got, want := targetName(reflect.TypeFor[holder[etA]]()), TypeKey(reflect.TypeFor[etA]()); got != want {
		t.Errorf("targetName %q, TypeKey %q", got, want)
	}
}
