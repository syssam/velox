package gqlplugin

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/plugin"
)

var update = flag.Bool("update", false, "rewrite testdata/oldvelox")

// TestOldVeloxGolden pins what velox-graphql writes under the options of an
// application that moved from the old velox and keeps its GraphQL API
// (oldVelox and where=whitelist), for graphql-go (engine=graphql-go) and
// for gqlgen, over models as the old velox's schemas annotate them: no
// RelayConnection, Go names that start with initialisms, a JSON field a
// GQL method binds, a Bytes field. The files and the warnings are those
// compat=veloxe,where=whitelist wrote with the same engine before these
// options replaced it, but for the version line, comments, the Veloxe
// field and the VeloxeFilters rename (ADR 0156). Run it with -update to
// rewrite testdata/oldvelox.
func TestOldVeloxGolden(t *testing.T) {
	for name, c := range map[string]struct {
		m        *plugin.Model
		apptypes string
		engine   string
	}{
		"graphqlgo":          {modeltest.NoRelay(t, modeltest.GraphQLGo(t)), "JSON+UUID+Uint64", "graphql-go"},
		"edgeschema":         {modeltest.NoRelay(t, modeltest.EdgeSchema(t)), "JSON+UUID+Uint64+Bytes", "graphql-go"},
		"salesorder":         {modeltest.SalesOrder(t), "JSON+UUID+Uint64+Bytes", "graphql-go"},
		"initialisms":        {modeltest.Initialisms(t), "JSON+UUID+Uint64", "graphql-go"},
		"salesorder-gqlgen":  {modeltest.SalesOrder(t), "JSON+UUID+Uint64+Bytes", "gqlgen"},
		"initialisms-gqlgen": {modeltest.Initialisms(t), "JSON+UUID+Uint64", "gqlgen"},
	} {
		t.Run(name, func(t *testing.T) {
			opts := map[string]string{"where": "whitelist", "engine": c.engine}
			for kv := range strings.SplitSeq(oldVelox, ",") {
				k, v, _ := strings.Cut(kv, "=")
				opts[k] = v
			}
			opts["apptypes"] = c.apptypes
			files, warnings, err := Generate(&plugin.Request{OutPath: "example.com/app/gen", OutPackage: "gen", Options: opts, Model: c.m})
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, plugin.File{Path: "warnings.txt", Content: strings.Join(warnings, "\n") + "\n"})
			dir := filepath.Join("testdata", "oldvelox", name)
			if *update {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			var paths []string
			for _, f := range files {
				paths = append(paths, f.Path)
				path := filepath.Join(dir, f.Path)
				if *update {
					if err := os.WriteFile(path, []byte(f.Content), 0o644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if f.Content != string(want) {
					t.Errorf("%s differs (run go test -update to accept)", path)
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if !slices.Contains(paths, e.Name()) {
					t.Errorf("%s is not written", filepath.Join(dir, e.Name()))
				}
			}
		})
	}
}
