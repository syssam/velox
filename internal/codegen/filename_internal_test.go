package codegen

import (
	gobuild "go/build"
	"io"
	"strings"
	"testing"
)

// TestEntityFile: an entity's file is <label>.go, as ent names it, unless go
// build would leave that name out of a build, or it is the package's
// velox.go: then <label>_velox.go. Every name it gives builds on every
// system and is no test file.
func TestEntityFile(t *testing.T) {
	for label, want := range map[string]string{
		"post":              "post.go",
		"post_tag":          "post_tag.go",
		"linux":             "linux.go",
		"arm64":             "arm64.go",
		"test":              "test.go",
		"ab_test":           "ab_test_velox.go",
		"server_linux":      "server_linux_velox.go",
		"device_arm64":      "device_arm64_velox.go",
		"job_windows_amd64": "job_windows_amd64_velox.go",
		"velox":             "velox_velox.go",
	} {
		got := entityFile(label)
		if got != want {
			t.Errorf("entityFile(%q) = %s, want %s", label, got, want)
		}
		if strings.HasSuffix(got, "_test.go") {
			t.Errorf("%s is a test file", got)
		}
		for _, sys := range [][2]string{{"linux", "amd64"}, {"darwin", "arm64"}, {"windows", "386"}, {"plan9", "riscv64"}, {"js", "wasm"}} {
			ctx := gobuild.Context{GOOS: sys[0], GOARCH: sys[1], Compiler: "gc",
				OpenFile: func(string) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("package p\n")), nil }}
			if ok, err := ctx.MatchFile("/", got); err != nil || !ok {
				t.Errorf("%s/%s leaves out %s: %v", sys[0], sys[1], got, err)
			}
		}
	}
}
