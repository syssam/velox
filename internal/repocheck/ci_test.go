package repocheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	ciWorkflow = ".github/workflows/ci.yml"
	ciLocal    = "scripts/ci-local.sh"
)

// scriptLine is one line of a CI script, without its comment.
type scriptLine struct {
	where  string // the script and line, for a message
	dir    string // the directory it runs in, relative to the repository's root
	text   string
	matrix bool // in ci-local.sh's VELOX_CI_MATRIX=1 block, its PostgreSQL 14 run
}

var (
	workingDir   = regexp.MustCompile(`^working-directory: ([\w./-]+)$`)
	changeDir    = regexp.MustCompile(`^cd ([\w./-]+)$`)
	subshellLine = regexp.MustCompile(`^\(cd ([\w./-]+) && (.*)\)$`)
)

// scriptLines returns the lines of a script, each with its directory: CI's steps
// name theirs with working-directory, and ci-local.sh's subshells with cd.
func scriptLines(t *testing.T, script string) []scriptLine {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(), script))
	if err != nil {
		t.Fatal(err)
	}
	var out []scriptLine
	wd, matrix := ".", false
	for i, raw := range strings.Split(string(data), "\n") {
		l := strings.TrimSpace(raw)
		if strings.HasPrefix(l, "#") {
			continue
		}
		if before, _, ok := strings.Cut(l, " # "); ok {
			l = strings.TrimSpace(before)
		}
		if strings.HasPrefix(l, "- name:") || strings.HasPrefix(l, "- run:") || raw == ")" {
			wd = "." // a new step, or the end of a subshell
		}
		if m := workingDir.FindStringSubmatch(l); m != nil {
			wd = m[1]
		}
		if m := changeDir.FindStringSubmatch(l); m != nil {
			wd = m[1]
		}
		switch {
		case strings.HasPrefix(l, `if [ "${VELOX_CI_MATRIX:-}" = 1 ]`):
			matrix = true
		case raw == "fi":
			matrix = false
		}
		l = strings.TrimPrefix(strings.TrimPrefix(l, "- "), "run: ")
		dir := wd
		if m := subshellLine.FindStringSubmatch(l); m != nil {
			dir, l = m[1], m[2]
		}
		out = append(out, scriptLine{fmt.Sprintf("%s:%d", script, i+1), dir, l, matrix})
	}
	return out
}

// ciTimeouts is the Go test timeout of each PostgreSQL version of CI's matrix.
func ciTimeouts(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(), ciWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`pg: "(\d+)"\n\s+postgres-image: \S+\n\s+test-timeout: (\S+)`).FindAllStringSubmatch(string(data), -1) {
		out[m[1]] = m[2]
	}
	if out["14"] == "" || out["18"] == "" {
		t.Fatalf("%s: the matrix has no test-timeout for PostgreSQL 14 and 18: %v", ciWorkflow, out)
	}
	return out
}

var (
	// ciCommand is a line that runs a tool: what the two scripts must share.
	ciCommand = regexp.MustCompile(`^(go |bin/velox-gen |scripts/)`)
	// outputPath is a path the script spells through its root's variable.
	outputPath = regexp.MustCompile(`"\$(?:root|GITHUB_WORKSPACE)/([^"]+)"`)
)

// normalizeCommand removes what may differ between the two scripts: the paths of
// the coverage profiles, golangci-lint's wait for another run on a local machine,
// and the matrix's timeout, which is that of PostgreSQL 18, the version the local
// script's main run uses.
func normalizeCommand(text, timeout18 string) string {
	text = outputPath.ReplaceAllString(text, "$1")
	text = strings.ReplaceAll(text, " --allow-serial-runners", "")
	return strings.ReplaceAll(text, "${{ matrix.test-timeout }}", timeout18)
}

// TestCILocalMirrorsCI: scripts/ci-local.sh runs the commands of ci.yml, each in the
// same directory. Its VELOX_CI_MATRIX=1 run tests what CI's PostgreSQL 14 leg tests,
// with that leg's timeout. Each published module and examples are scanned for
// vulnerabilities, and every tidied module is checked for tidiness before go mod
// download all, which adds to go.sum what tidy leaves out, can make the check fail.
func TestCILocalMirrorsCI(t *testing.T) {
	timeouts := ciTimeouts(t)
	commands := func(script string) map[string]scriptLine {
		out := map[string]scriptLine{}
		for _, l := range scriptLines(t, script) {
			if ciCommand.MatchString(l.text) && !l.matrix {
				out[l.dir+": "+normalizeCommand(l.text, timeouts["18"])] = l
			}
		}
		if len(out) < 30 {
			t.Fatalf("%s: read only %d commands: the test no longer reads the script", script, len(out))
		}
		return out
	}
	ci, local := commands(ciWorkflow), commands(ciLocal)
	for k, l := range ci {
		if _, ok := local[k]; !ok {
			t.Errorf("%s: not in %s: %s", l.where, ciLocal, k)
		}
	}
	for k, l := range local {
		if _, ok := ci[k]; !ok {
			t.Errorf("%s: not in %s: %s", l.where, ciWorkflow, k)
		}
	}

	// The PostgreSQL 14 run.
	var matrix []string
	for _, l := range scriptLines(t, ciLocal) {
		if l.matrix && strings.HasPrefix(l.text, "go test ") {
			matrix = append(matrix, l.dir+": "+l.text)
		}
	}
	if want := ".: go test -race -count=1 -timeout " + timeouts["14"] + " ./..."; !slices.Contains(matrix, want) {
		t.Errorf("%s's PostgreSQL 14 run has %q, want %q", ciLocal, matrix, want)
	}
	for k, l := range ci {
		if !strings.Contains(k, ": go test -race -count=1 ") || strings.Contains(k, " -short ") {
			continue
		}
		if !slices.ContainsFunc(matrix, func(m string) bool { return strings.HasPrefix(m, l.dir+": ") }) {
			t.Errorf("%s: CI tests %s on PostgreSQL 14, and %s's PostgreSQL 14 run does not", l.where, l.dir, ciLocal)
		}
	}

	// What neither script may lose.
	for _, script := range []string{ciWorkflow, ciLocal} {
		lines := scriptLines(t, script)
		has := func(dir, prefix string) int {
			return slices.IndexFunc(lines, func(l scriptLine) bool { return l.dir == dir && strings.HasPrefix(l.text, prefix) })
		}
		for _, dir := range []string{".", "contrib/graphql", "contrib/veloxotel", "examples"} {
			if has(dir, "go run golang.org/x/vuln/cmd/govulncheck@") < 0 {
				t.Errorf("%s runs no govulncheck in %s", script, dir)
			}
		}
		for _, dir := range []string{".", "contrib/graphql", "contrib/veloxotel", "examples", "bench/compare"} {
			diff := has(dir, "go mod tidy -diff")
			if diff < 0 {
				t.Errorf("%s runs no go mod tidy -diff in %s", script, dir)
			}
			if download := has(dir, "go mod download all"); download >= 0 && download < diff {
				t.Errorf("%s runs go mod tidy -diff after go mod download all in %s, which makes it fail", script, dir)
			}
		}
	}
}
