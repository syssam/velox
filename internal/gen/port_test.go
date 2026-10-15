package gen

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestPortEntSchema loads a representative ent schema, with ent's import paths
// changed to velox2's, and checks the relations of its graph.
func TestPortEntSchema(t *testing.T) {
	files := map[string]string{}
	matches, err := filepath.Glob("testdata/port/schema/*.go")
	if err != nil || len(matches) == 0 {
		t.Fatalf("no port fixture: %v", err)
	}
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		// The first line is the //go:build ignore that keeps go vet ./...
		// from type-checking the fixture as a package of velox2.
		_, src, _ := bytes.Cut(b, []byte("\n"))
		files["schema/"+filepath.Base(p)] = string(src)
	}
	dir := module(t, files)
	m, err := Load(dir, "./schema", offline())
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	u, c := g.Entity("User"), g.Entity("Card")
	if e := u.Edge("card"); e.Rel.Kind != "O2O" || e.Rel.Table != "cards" || e.Rel.Columns[0] != "owner_id" || e.Field != "owner_id" {
		t.Fatalf("card %+v", e)
	}
	if c.Field("owner_id").Implicit != "" || !u.Field("active").Pointer || g.Entity("Pet").Field("user_pets") == nil {
		t.Fatal("bound and implicit foreign keys, and the default bool")
	}
	if e := u.Edge("groups"); e.Rel.Kind != "M2M" || e.Rel.Table != "user_groups" || g.Entity("Group").Table != "teams" {
		t.Fatalf("groups %+v", e)
	}
	if e := u.Edge("pets"); e.Rel.Kind != "O2M" || e.Rel.Table != "pets" || e.Rel.Columns[0] != "user_pets" {
		t.Fatalf("pets %+v", e)
	}
	n := g.Entity("Node")
	if e := n.Edge("children"); e.Rel.Kind != "O2M" || n.Edge("parent").Rel.Kind != "M2O" || n.Edge("friends").Rel.Kind != "M2M" {
		t.Fatalf("tree %+v", n.Edges)
	}
}
