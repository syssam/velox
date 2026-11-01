package buildinfo

import (
	"runtime/debug"
	"testing"
)

// TestGeneratorVersions: a migration's header says the version the build holds. A
// directory in a replace has none, whatever go.mod requires.
func TestGeneratorVersions(t *testing.T) {
	dep := func(version string, replace *debug.Module) *debug.BuildInfo {
		return &debug.BuildInfo{Deps: []*debug.Module{
			{Path: "example.com/other", Version: "v9.9.9"},
			{Path: modulePath, Version: version, Replace: replace},
		}}
	}
	for name, c := range map[string]struct {
		bi   *debug.BuildInfo
		want string
	}{
		"no build info":           {nil, "velox-gen (devel)"},
		"the main module":         {&debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v1.2.3"}}, "velox-gen v1.2.3"},
		"a dependency":            {dep("v1.2.3", nil), "velox-gen v1.2.3"},
		"replaced by a version":   {dep("v0.3.0", &debug.Module{Path: "example.com/fork", Version: "v1.2.4"}), "velox-gen v1.2.4"},
		"replaced by a directory": {dep("v0.3.0", &debug.Module{Path: "../velox"}), "velox-gen (devel)"},
		"a placeholder replaced":  {dep("v0.0.0-00010101000000-000000000000", &debug.Module{Path: "../.."}), "velox-gen (devel)"},
	} {
		if got := generator(c.bi); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}
