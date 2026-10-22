package tempmod

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHint: a go command that failed on a module the offline cache lacks gets
// the command that fills the cache; any other output gets nothing.
func TestHint(t *testing.T) {
	out := "go: github.com/stretchr/testify@v1.11.1: module lookup disabled by GOPROXY=off\n"
	if h := Hint(out); !strings.Contains(h, "run go mod download") {
		t.Fatalf("Hint = %q", h)
	}
	if h := Hint("undefined: x\n"); h != "" {
		t.Fatalf("Hint of another failure = %q", h)
	}
}

// TestWriteGraphQL: the module requires contrib/graphql by replace, declares
// velox-gen and velox-graphql as tools, has the two go.sum files joined, and
// builds offline with a package that imports the runtime.
func TestWriteGraphQL(t *testing.T) {
	if testing.Short() {
		t.Skip("resolves a module offline; run without -short")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string]string{"use/use.go": "package use\n\nimport _ \"github.com/syssam/velox2/contrib/graphql/veloxgql\"\n"}
	if err := WriteGraphQL(dir, repo, files); err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Errorf("WriteGraphQL changed its files: %v", files)
	}
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"github.com/syssam/velox2/cmd/velox-gen",
		"github.com/syssam/velox2/contrib/graphql/cmd/velox-graphql",
		"github.com/syssam/velox2 => " + repo,
		"github.com/syssam/velox2/contrib/graphql => " + filepath.Join(repo, "contrib", "graphql"),
	} {
		if !strings.Contains(string(mod), want) {
			t.Errorf("go.mod lacks %q:\n%s", want, mod)
		}
	}
	sum, err := os.ReadFile(filepath.Join(dir, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"go.sum", filepath.Join("contrib", "graphql", "go.sum")} {
		b, err := os.ReadFile(filepath.Join(repo, p))
		if err != nil {
			t.Fatal(err)
		}
		// tidy may drop lines nothing needs, so one line of each file is enough.
		first, _, _ := strings.Cut(string(b), "\n")
		if !strings.Contains(string(sum), first) {
			t.Errorf("go.sum lacks %q of %s", first, p)
		}
	}
	if out, err := Go(dir, "build", "./..."); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	if out, err := Go(dir, "tool", "-n", "velox-graphql"); err != nil {
		t.Fatalf("velox-graphql is not a tool of the module: %v\n%s", err, out)
	}
}
