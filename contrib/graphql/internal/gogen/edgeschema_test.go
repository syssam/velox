package gogen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
)

// TestEdgeSchemaGolden pins the Go of the edge schema model for gqlgen and
// graphql-go. Run it with -update to rewrite testdata/edgeschema and
// testdata/edgeschema_graphqlgo. Membership and Follow have Go files like
// any schema; User's update input has no field that adds to following.
func TestEdgeSchemaGolden(t *testing.T) {
	for dir, cfg := range map[string]sdl.Config{"edgeschema": {}, "edgeschema_graphqlgo": graphqlGoConfig, "edgeschema_schemanames": {Names: "schema"}} {
		t.Run(dir, func(t *testing.T) {
			files, err := RenderConfig(modeltest.EdgeSchema(t), outPath, "gen", cfg)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join("testdata", dir)
			if *update {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				for _, f := range files {
					if err := os.WriteFile(filepath.Join(dir, f.Path), []byte(f.Content), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != len(files) {
				t.Fatalf("%d files, want %d (run go test -update to accept)", len(files), len(entries))
			}
			byPath := map[string]string{}
			for _, f := range files {
				byPath[f.Path] = f.Content
				if w := readFile(t, filepath.Join(dir, f.Path)); f.Content != w {
					t.Fatalf("%s differs (run go test -update to accept)", f.Path)
				}
			}
			for _, name := range []string{"membership_gql.go", "follow_gql.go"} {
				if byPath[name] == "" {
					t.Errorf("no %s", name)
				}
			}
			user := byPath["user_gql.go"]
			if !strings.Contains(user, "AddGroupIDs") || !strings.Contains(user, "RemoveFollowingIDs") || strings.Contains(user, "AddFollowingIDs") || strings.Contains(user, "\tFollowingIDs ") {
				t.Errorf("User's inputs:\n%s", user)
			}
		})
	}
}
