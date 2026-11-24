package gogen

import (
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
)

// TestFiltersOption: a gqlgraph.Type has LenientFilters as sdl.Config's
// Filters gives it.
func TestFiltersOption(t *testing.T) {
	for _, c := range []struct {
		name    string
		cfg     sdl.Config
		filters bool
	}{
		{"default", sdl.Config{}, false},
		{"filters=strict", sdl.Config{Filters: "strict"}, false},
		{"filters=lenient", sdl.Config{Filters: "veloxe"}, true},
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
		if got := strings.Contains(post, " LenientFilters: true,"); got != c.filters {
			t.Errorf("%s: LenientFilters is %v, want %v", c.name, got, c.filters)
		}
	}
}
