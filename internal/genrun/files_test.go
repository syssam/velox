package genrun

import (
	"path/filepath"
	"strings"
	"testing"
)

// FilesDir needs -out in a module; no -files, or -files equal to -out, is
// "", as without the flag.
func TestFilesDirOutsideAModule(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if moduleRoot(dir) != "" {
		t.Skip("the temporary directory is in a module")
	}
	out := filepath.Join(dir, "gen")
	if _, err := FilesDir(dir, out, "schema"); err == nil || !strings.Contains(err.Error(), "-files schema: -out gen is not in a module") {
		t.Errorf("%v", err)
	}
	for _, files := range []string{"", "gen", "./gen/"} {
		if fd, err := FilesDir(dir, out, files); fd != "" || err != nil {
			t.Errorf("-files %q: %q %v", files, fd, err)
		}
	}
}

func TestCommonDir(t *testing.T) {
	for _, c := range [][3]string{
		{"/m/gen", "/m/schema", "/m"},
		{"/m/a/gen", "/m/a/gen/x", "/m/a/gen"},
		{"/a", "/b", "/"},
	} {
		if got := commonDir(filepath.FromSlash(c[0]), filepath.FromSlash(c[1])); got != filepath.FromSlash(c[2]) {
			t.Errorf("commonDir(%s, %s) = %s, want %s", c[0], c[1], got, c[2])
		}
	}
}
