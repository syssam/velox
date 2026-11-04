package repocheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCIChangesGroups: scripts/ci-changes.sh prints the groups a change
// touches, from the files committed since the base and the ones not yet
// committed, and none for a change of the ORM's alone. It runs on a copy of
// the script in a repository of its own.
func TestCIChangesGroups(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	script, err := os.ReadFile(filepath.Join(repoRoot(), ciChanges))
	if err != nil {
		t.Fatal(err)
	}
	git := func(t *testing.T, dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(t *testing.T, dir, name, content string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The base: the script, and a file of each kind the cases change.
	base := map[string]string{
		"doc.go": "package velox\n\nconst GeneratedCodeIsVersion8 = true\n",
		"go.mod": "module m\n",
	}
	for _, c := range []struct {
		name      string
		committed map[string]string // committed after the base
		worktree  map[string]string // written and not committed
		want      string
	}{
		{"nothing", nil, nil, ""},
		{"the ORM", map[string]string{"internal/engine/x.go": "1", "migrate/x.go": "1", "cmd/velox-gen/x.go": "1"}, map[string]string{"examples/start/x.go": "1"}, ""},
		{"graphql", map[string]string{"contrib/graphql/x.go": "1"}, nil, "graphql"},
		{"examples/graphql", nil, map[string]string{"examples/graphql/x.go": "1"}, "graphql"},
		{"schema", map[string]string{"schema/field/x.go": "1"}, nil, "graphql"},
		{"otel, uncommitted", nil, map[string]string{"contrib/veloxotel/x.go": "1"}, "otel"},
		{"the test model", map[string]string{"internal/testmodel/x.go": "1"}, nil, "otel"},
		{"bench", map[string]string{"bench/compare/x.go": "1"}, nil, "bench"},
		{"bench/scale", map[string]string{"bench/scale/x.go": "1"}, nil, "graphql bench"},
		{"the API", map[string]string{"internal/repocheck/testdata/api.txt": "1"}, nil, "graphql otel"},
		{"the plugin protocol", map[string]string{"plugin/x.go": "1"}, nil, "graphql otel"},
		{"the generator", map[string]string{"internal/codegen/x.go": "1"}, nil, "graphql otel"},
		{"the root's go.mod", nil, map[string]string{"go.mod": "module m\n\ngo 1.27\n"}, "graphql otel"},
		{"the generated code's version", map[string]string{"doc.go": "package velox\n\nconst GeneratedCodeIsVersion11 = true\n"}, nil, "graphql otel"},
		{"another line of doc.go", map[string]string{"doc.go": "package velox\n\n// GeneratedCodeIsVersion8 ties the code.\nconst GeneratedCodeIsVersion8 = true\n"}, nil, ""},
		{"the CI", map[string]string{".github/workflows/ci.yml": "1"}, nil, "graphql otel bench"},
		{"an agent's worktree", nil, map[string]string{".claude/worktrees/a/contrib/graphql/x.go": "1"}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			git(t, dir, "init", "-q", "-b", "main")
			write(t, dir, ciChanges, string(script))
			for name, content := range base {
				write(t, dir, name, content)
			}
			git(t, dir, "add", "-A")
			git(t, dir, "commit", "-q", "-m", "base")
			head := git(t, dir, "rev-parse", "HEAD")
			for name, content := range c.committed {
				write(t, dir, name, content)
			}
			if len(c.committed) > 0 {
				git(t, dir, "add", "-A")
				git(t, dir, "commit", "-q", "-m", "change")
			}
			for name, content := range c.worktree {
				write(t, dir, name, content)
			}
			cmd := exec.Command("bash", filepath.Join(dir, ciChanges), head)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if got := strings.Join(strings.Fields(string(out)), " "); got != c.want {
				t.Errorf("groups %q, want %q", got, c.want)
			}
		})
	}
}
