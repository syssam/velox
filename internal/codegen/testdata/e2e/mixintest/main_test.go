package gen_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"

	"example.com/tmp/gen"
	_ "example.com/tmp/gen/runtime" // binds the schema
	"example.com/tmp/schema"
)

// A mixin's Policy, Hooks and Interceptors, bound by package runtime, run
// for the entity that lists it: the hooks of the mixins first, in Mixin
// order, the configured one with its value, then the entity's own; the
// policy confines reads to the organization; the interceptor hides rows.
func TestMixinBehaviourRuns(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(`CREATE TABLE tickets (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, hidden BOOLEAN NOT NULL, title TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, raw)
	org1, org2 := schema.WithOrg(context.Background(), 1), schema.WithOrg(context.Background(), 2)
	for _, c := range []struct {
		ctx context.Context
		t   gen.Ticket
	}{{org1, gen.Ticket{Title: "a"}}, {org1, gen.Ticket{Title: "b", Hidden: true}}, {org2, gen.Ticket{Title: "c"}}} {
		if err := db.Insert(&c.t).Exec(c.ctx); err != nil {
			t.Fatal(err)
		}
		if c.t.OrgID == 0 {
			t.Fatalf("%s: org_id not filled", c.t.Title)
		}
	}
	if want := []string{"tenant", "stamp", "own"}; !slices.Equal(schema.Trace[:3], want) {
		t.Errorf("hooks ran %v, want %v first", schema.Trace, want)
	}
	got, err := db.Query[gen.Ticket]().Unbounded().All(org1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "a" {
		t.Errorf("org 1 reads %+v, want ticket a alone", got)
	}
	if n, err := db.Query[gen.Ticket]().Count(org2); err != nil || n != 1 {
		t.Errorf("org 2 counts %d, %v", n, err)
	}
}
