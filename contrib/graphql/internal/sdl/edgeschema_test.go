package sdl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/plugin"
)

// TestEdgeSchemaGolden pins the SDL of the edge schema model for gqlgen and
// graphql-go. Run it with -update to rewrite testdata/edgeschema*.graphql.
// Membership and Follow are types like any; User.groups keeps every input,
// and User.following and followers, whose edge schema Follow has a note Add
// cannot fill, have no input that adds links.
func TestEdgeSchemaGolden(t *testing.T) {
	// The through edge's Ref names the key edge, which names none back:
	// nothing in velox-graphql may pair edges by Ref both ways.
	byName := map[string]*plugin.Entity{}
	for _, e := range modeltest.EdgeSchema(t).Entities {
		byName[e.Name] = e
	}
	if ms, key := edgeOf(t, byName["User"], "memberships"), edgeOf(t, byName["Membership"], "user"); ms.Ref != "user" || key.Ref != "" {
		t.Fatalf("the model's Ref pair is not one way: %q, %q", ms.Ref, key.Ref)
	}
	for name, cfg := range map[string]Config{"edgeschema.graphql": {}, "edgeschema_graphqlgo.graphql": graphqlGo(false)} {
		t.Run(name, func(t *testing.T) {
			got, err := RenderConfig(modeltest.EdgeSchema(t), cfg)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", name)
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if want := readFile(t, path); got != want {
				t.Fatalf("differs from %s (run go test -update to accept)", path)
			}
			if !cfg.GraphQLGo {
				if _, err := Check(got, ""); err != nil {
					t.Fatal(err)
				}
			}
			for _, want := range []string{"type Membership ", "type Follow ", "memberships(", "groupIDs: [ID!]", "addGroupIDs: [ID!]", "removeFollowingIDs: [ID!]", "clearFollowing: Boolean"} {
				if !strings.Contains(got, want) {
					t.Errorf("no %q", want)
				}
			}
			for _, absent := range []string{"followingIDs", "addFollowingIDs", "followerIDs", "addFollowerIDs", "addMembershipIDs", "membershipIDs"} {
				if strings.Contains(got, absent) {
					t.Errorf("%q is written", absent)
				}
			}
		})
	}
}

// edgeOf is e's edge of the given name.
func edgeOf(t *testing.T, e *plugin.Entity, name string) *plugin.Edge {
	t.Helper()
	for _, ed := range e.Edges {
		if ed.Name == name {
			return ed
		}
	}
	t.Fatalf("%s has no edge %s", e.Name, name)
	return nil
}
