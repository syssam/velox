package gogen

import (
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
)

// TestFiltersOption: a gqlgraph.Type has VeloxeFilters as sdl.Config's
// Filters gives it, apart from Veloxe: compat=veloxe sets both, an explicit
// filters=strict leaves VeloxeFilters out, and filters=veloxe sets it alone.
func TestFiltersOption(t *testing.T) {
	for _, c := range []struct {
		name            string
		cfg             sdl.Config
		veloxe, filters bool
	}{
		{"default", sdl.Config{}, false, false},
		{"compat=veloxe", sdl.Config{Veloxe: true, Package: outPath}, true, true},
		{"compat=veloxe,filters=strict", sdl.Config{Veloxe: true, Package: outPath, Filters: "strict"}, true, false},
		{"filters=veloxe", sdl.Config{Filters: "veloxe"}, false, true},
	} {
		files, err := RenderConfig(modeltest.Model(t), outPath, "gen", c.cfg)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var post string
		for _, f := range files {
			if f.Path == "post_gql.go" {
				post = strings.Join(strings.Fields(f.Content), " ")
			}
		}
		if got := strings.Contains(post, " Veloxe: true,"); got != c.veloxe {
			t.Errorf("%s: Veloxe is %v, want %v", c.name, got, c.veloxe)
		}
		if got := strings.Contains(post, " VeloxeFilters: true,"); got != c.filters {
			t.Errorf("%s: VeloxeFilters is %v, want %v", c.name, got, c.filters)
		}
	}
}
