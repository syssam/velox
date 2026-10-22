package veloxotel

import (
	"runtime/debug"
	"testing"
)

// TestModuleVersion pins that the scope's version is the module's as the build
// records it, and "" where the build has none: "(devel)", or a replacement by a
// directory. Never a placeholder.
func TestModuleVersion(t *testing.T) {
	dep := func(v string, r *debug.Module) *debug.BuildInfo {
		return &debug.BuildInfo{
			Main: debug.Module{Path: "example.com/app", Version: "(devel)"},
			Deps: []*debug.Module{
				{Path: "github.com/syssam/velox2", Version: "v0.0.0-00010101000000-000000000000", Replace: &debug.Module{Path: "../.."}},
				{Path: modulePath, Version: v, Replace: r},
			},
		}
	}
	for _, c := range []struct {
		name string
		bi   *debug.BuildInfo
		ok   bool
		want string
	}{
		{"no build info", nil, false, ""},
		{"main module", &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v1.2.0"}}, true, "v1.2.0"},
		{"main module, devel", &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "(devel)"}}, true, ""},
		{"dependency", dep("v1.2.0", nil), true, "v1.2.0"},
		{"replaced by a directory", dep("v0.0.0-00010101000000-000000000000", &debug.Module{Path: "../veloxotel"}), true, ""},
		{"replaced by a module", dep("v1.2.0", &debug.Module{Path: "example.com/fork", Version: "v1.2.1"}), true, "v1.2.1"},
		{"no entry", &debug.BuildInfo{Main: debug.Module{Path: "example.com/app", Version: "v3.0.0"}}, true, ""},
	} {
		if got := moduleVersion(c.bi, c.ok); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
