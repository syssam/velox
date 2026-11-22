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
	ciChanges  = "scripts/ci-changes.sh"
)

// levels are the levels of the two scripts' checks. gate runs in every run,
// merge in the merge gate only (ci-local.sh's default, a pull request), full in
// --full only (a push), and each group in --full and when a change touches it
// (scripts/ci-changes.sh prints the groups).
var (
	levels = []string{"gate", "merge", "full", "graphql", "otel", "bench"}
	groups = levels[3:]
)

// scriptLine is one line of a CI script, without its comment.
type scriptLine struct {
	where  string // the script and line, for a message
	dir    string // the directory it runs in, relative to the repository's root
	text   string
	level  string // when it runs: one of levels
	scope  string // its ci.yml job, or its ci-local.sh lane or final ("" for the serial start)
	matrix bool   // in ci-local.sh's VELOX_CI_MATRIX=1 block, its PostgreSQL 14 run
}

var (
	workingDir   = regexp.MustCompile(`^working-directory: ([\w./-]+)$`)
	changeDir    = regexp.MustCompile(`^cd ([\w./-]+)$`)
	subshellLine = regexp.MustCompile(`^\(cd ([\w./-]+) && (.*)\)$`)

	// ci-local.sh's lanes and finals: the lane_x and final_x functions, and
	// the lists that give each its level.
	localFunc  = regexp.MustCompile(`^(lane|final)_(\w+)\(\) [({]$`)
	localList  = regexp.MustCompile(`(?m)^(lanes|finals)=\(([\w: ]+)\)$`)
	localGroup = regexp.MustCompile(`^if selected (\w+); then$`)
	// ci.yml's jobs and steps, and the if that gives one its level.
	ciJob    = regexp.MustCompile(`^  ([\w-]+):$`)
	ciJobIf  = regexp.MustCompile(`^    if: (.+)$`)
	ciStepIf = regexp.MustCompile(`^        if: (.+)$`)
	ciGroup  = regexp.MustCompile(`^needs\.changes\.outputs\.(\w+) == 'true'$`)
)

// localLevels returns the level of each of ci-local.sh's lane_ and final_
// functions, by function name, and the names in each list, in order.
func localLevels(t *testing.T, data string) (map[string]string, map[string][]string) {
	t.Helper()
	out, lists := map[string]string{}, map[string][]string{}
	for _, m := range localList.FindAllStringSubmatch(data, -1) {
		kind := strings.TrimSuffix(m[1], "s")
		if _, dup := lists[m[1]]; dup {
			t.Errorf("%s: two %s=(…) lines", ciLocal, m[1])
		}
		lists[m[1]] = []string{}
		for f := range strings.FieldsSeq(m[2]) {
			name, level, ok := strings.Cut(f, ":")
			if !ok || !slices.Contains(levels, level) {
				t.Errorf("%s: %s: %q has no level of %v", ciLocal, m[1], f, levels)
			}
			if _, dup := out[kind+"_"+name]; dup {
				t.Errorf("%s: %s names %s twice", ciLocal, m[1], name)
			}
			out[kind+"_"+name] = level
			lists[m[1]] = append(lists[m[1]], name)
		}
	}
	return out, lists
}

// ciLevel returns the level of a job or a step of ci.yml from its if.
func ciLevel(t *testing.T, where, expr string) string {
	t.Helper()
	switch expr {
	case "github.event_name == 'push'":
		return "full"
	case "github.event_name == 'pull_request'":
		return "merge"
	}
	if m := ciGroup.FindStringSubmatch(expr); m != nil && slices.Contains(groups, m[1]) {
		return m[1]
	}
	t.Errorf("%s: an if this test cannot read as a level: %q", where, expr)
	return "?"
}

// scriptLines returns the lines of a script, each with its directory, its
// level and its scope. CI's steps name their directory with
// working-directory, and ci-local.sh's subshells with cd. In ci.yml a job's
// or a step's if gives the level; in ci-local.sh, the entry of the line's
// lane or final in the lists, and in the matrix block an if selected.
func scriptLines(t *testing.T, script string) []scriptLine {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(), script))
	if err != nil {
		t.Fatal(err)
	}
	var local map[string]string
	if script == ciLocal {
		local, _ = localLevels(t, string(data))
	}
	var out []scriptLine
	wd, matrix := ".", false
	scope, scopeLevel := "", "gate"
	stepLevel, stepRun := "", false // ci.yml's step
	group := ""                     // ci-local.sh's if selected, in the matrix block
	for i, raw := range strings.Split(string(data), "\n") {
		where := fmt.Sprintf("%s:%d", script, i+1)
		l := strings.TrimSpace(raw)
		if strings.HasPrefix(l, "#") {
			continue
		}
		if before, _, ok := strings.Cut(l, " # "); ok {
			l = strings.TrimSpace(before)
		}
		if strings.HasPrefix(raw, "      - ") || raw == ")" {
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

		var level string
		if local != nil {
			if m := localFunc.FindStringSubmatch(raw); m != nil {
				scope = m[1] + "_" + m[2]
				var ok bool
				if scopeLevel, ok = local[scope]; !ok {
					t.Errorf("%s: %s is in neither list", where, scope)
				}
			} else if raw == ")" || raw == "}" {
				scope, scopeLevel = "", "gate"
			}
			if m := localGroup.FindStringSubmatch(l); m != nil && matrix {
				group = m[1]
			} else if l == "fi" {
				group = ""
			}
			level = scopeLevel
			if group != "" {
				level = group
			}
		} else {
			if m := ciJob.FindStringSubmatch(raw); m != nil {
				scope, scopeLevel, stepLevel = m[1], "gate", ""
			}
			if m := ciJobIf.FindStringSubmatch(raw); m != nil {
				scopeLevel = ciLevel(t, where, m[1])
			}
			if strings.HasPrefix(raw, "      - ") {
				stepLevel, stepRun = "", false
			}
			if strings.HasPrefix(l, "run:") || strings.HasPrefix(l, "- run:") {
				stepRun = true
			}
			if m := ciStepIf.FindStringSubmatch(raw); m != nil {
				if stepRun {
					t.Errorf("%s: a step's if after its run, which this test would read as the next step's", where)
				}
				stepLevel = ciLevel(t, where, m[1])
				if scopeLevel != "gate" && stepLevel != scopeLevel {
					t.Errorf("%s: a step of level %s in a job of level %s", where, stepLevel, scopeLevel)
				}
			}
			level = scopeLevel
			if stepLevel != "" {
				level = stepLevel
			}
		}

		l = strings.TrimPrefix(strings.TrimPrefix(l, "- "), "run: ")
		dir := wd
		if m := subshellLine.FindStringSubmatch(l); m != nil {
			dir, l = m[1], m[2]
		}
		out = append(out, scriptLine{where, dir, l, level, scope, matrix})
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

// command is a command of a script, by directory and text, with its level.
type command struct{ dir, text, level string }

func (c command) String() string { return c.dir + " [" + c.level + "]: " + c.text }

// TestCILocalMirrorsCI: scripts/ci-local.sh runs the commands of ci.yml, each in the
// same directory and at the same level, so that the merge gate is a pull
// request's run and --full a push's. A command of the gate level, which runs in
// every run, stands in for the same command at any level. Its
// VELOX_CI_MATRIX=1 run tests what CI's PostgreSQL 14 leg tests, at the same
// levels, with that leg's timeout. Each published module and examples are
// scanned for vulnerabilities, and every tidied module is checked for tidiness
// before go mod download all, which adds to go.sum what tidy leaves out, can
// make the check fail.
func TestCILocalMirrorsCI(t *testing.T) {
	t.Parallel()
	timeouts := ciTimeouts(t)
	commands := func(script string) map[command]scriptLine {
		out := map[command]scriptLine{}
		for _, l := range scriptLines(t, script) {
			if ciCommand.MatchString(l.text) && !l.matrix {
				out[command{l.dir, normalizeCommand(l.text, timeouts["18"]), l.level}] = l
			}
		}
		if len(out) < 30 {
			t.Fatalf("%s: read only %d commands: the test no longer reads the script", script, len(out))
		}
		return out
	}
	ci, local := commands(ciWorkflow), commands(ciLocal)
	mirrored := func(other map[command]scriptLine, c command) bool {
		_, same := other[c]
		_, always := other[command{c.dir, c.text, "gate"}]
		return same || always
	}
	for c, l := range ci {
		if !mirrored(local, c) {
			t.Errorf("%s: not in %s: %s", l.where, ciLocal, c)
		}
	}
	for c, l := range local {
		if !mirrored(ci, c) {
			t.Errorf("%s: not in %s: %s", l.where, ciWorkflow, c)
		}
	}

	// The PostgreSQL 14 run, of what CI's test job runs on both versions. The
	// root's tests run in it whatever the level, and the merge and full forms
	// of a test are the gate's there.
	var matrix []command
	for _, l := range scriptLines(t, ciLocal) {
		if l.matrix && strings.HasPrefix(l.text, "go test ") {
			matrix = append(matrix, command{l.dir, l.text, l.level})
		}
	}
	// The root's: the merge gate's command, with the 14 leg's timeout.
	rootTests := 0
	for c, l := range ci {
		if c.dir == "." && c.level == "merge" && l.scope == "test" && strings.HasPrefix(c.text, "go test -race -count=1 ") {
			rootTests++
			if want := (command{".", normalizeCommand(l.text, timeouts["14"]), "gate"}); !slices.Contains(matrix, want) {
				t.Errorf("%s's PostgreSQL 14 run has %q, want %q", ciLocal, matrix, want)
			}
		}
	}
	if rootTests != 1 {
		t.Errorf("%s's test job has %d merge-level go test lines in the root, want 1", ciWorkflow, rootTests)
	}
	for c, l := range ci {
		if l.scope != "test" || !strings.HasPrefix(c.text, "go test -race -count=1 ") || strings.Contains(c.text, " -short ") {
			continue
		}
		level := c.level
		if level == "merge" || level == "full" {
			level = "gate"
		}
		if !slices.ContainsFunc(matrix, func(m command) bool { return m.dir == c.dir && m.level == level }) {
			t.Errorf("%s: CI tests %s on PostgreSQL 14 at level %s, and %s's PostgreSQL 14 run does not", l.where, c.dir, level, ciLocal)
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
			if has(dir, "go mod tidy -diff") < 0 {
				t.Errorf("%s runs no go mod tidy -diff in %s", script, dir)
			}
		}
		// In a job, or in ci-local.sh's run, whose lanes share the tree, a
		// module's tidy check comes before its go mod download all.
		for i, l := range lines {
			if l.text != "go mod download all" {
				continue
			}
			for _, d := range lines[i+1:] {
				if d.dir == l.dir && d.text == "go mod tidy -diff" && (script == ciLocal || d.scope == l.scope) {
					t.Errorf("%s: go mod tidy -diff after %s's go mod download all in %s, which makes it fail", d.where, l.where, l.dir)
				}
			}
		}
		if script == ciLocal {
			for _, l := range lines {
				if l.text != "go mod download all" {
					continue
				}
				if i := slices.IndexFunc(lines, func(d scriptLine) bool { return d.dir == l.dir && d.text == "go mod tidy -diff" }); i >= 0 && lines[i].scope != l.scope {
					t.Errorf("%s: go mod download all in %s in another lane than its tidy check (%s), which could run first", l.where, l.dir, lines[i].where)
				}
			}
		}
	}
}

// TestCIGateIsTheORM: the merge gate, the gate and merge levels of both
// scripts, runs the ORM's checks: the root's vet, lint and race tests, the
// examples' tests, velox-gen's check of its test models and examples, the
// root's fuzz targets and the scale check. --full measures the root's
// coverage and checks the floors.
func TestCIGateIsTheORM(t *testing.T) {
	t.Parallel()
	gate := []struct{ dir, prefix string }{
		{".", "go vet ./..."},
		{".", "go run github.com/golangci/golangci-lint/"},
		{".", "go test -race -count=1 -timeout "},
		{".", "go test -run XXX -fuzz "},
		{".", "bin/velox-gen -check "},
		{".", "bin/velox-gen verify "},
		{".", "scripts/scale-check.sh -n 500 -s 100"},
		{"examples", "go test -race -count=1 "},
		{"examples", "go tool velox-gen -check "},
		{"examples", "go tool velox-gen migrate apply "},
	}
	full := []struct{ dir, prefix string }{
		{".", "go test -race -count=1 -timeout 20m -coverpkg=./... -coverprofile=cover-root.out "},
		{".", "go run ./scripts/covfloor "},
	}
	for _, script := range []string{ciWorkflow, ciLocal} {
		lines := scriptLines(t, script)
		has := func(dir, prefix string, at ...string) bool {
			return slices.ContainsFunc(lines, func(l scriptLine) bool {
				return l.dir == dir && strings.HasPrefix(normalizeCommand(l.text, "20m"), prefix) && slices.Contains(at, l.level) && !l.matrix
			})
		}
		for _, c := range gate {
			if !has(c.dir, c.prefix, "gate", "merge") {
				t.Errorf("%s's merge gate runs no %q in %s", script, c.prefix, c.dir)
			}
		}
		for _, c := range full {
			if !has(c.dir, c.prefix, "gate", "full") {
				t.Errorf("%s's full run runs no %q in %s", script, c.prefix, c.dir)
			}
		}
		// Every test of contrib/ is a group's, not the gate's.
		for _, l := range lines {
			if strings.HasPrefix(l.dir, "contrib/") && strings.HasPrefix(l.text, "go test ") && !slices.Contains(groups, l.level) {
				t.Errorf("%s: %s's tests at level %s, not a group's", l.where, l.dir, l.level)
			}
		}
	}
}

// TestCIGroupsAreCIChanges: the groups the two scripts name are the ones
// scripts/ci-changes.sh prints, and ci.yml's changes job has an output for
// each. ci-changes.sh derives each group's files from go list in every
// module a lane of the group runs in, but the root's, whose group files it
// names by path.
func TestCIGroupsAreCIChanges(t *testing.T) {
	t.Parallel()
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(repoRoot(), name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	var printed []string
	for _, m := range regexp.MustCompile(`(?m)^if \[ -n "\$\w+" \]; then echo (\w+); fi$`).FindAllStringSubmatch(read(ciChanges), -1) {
		printed = append(printed, m[1])
	}
	if !slices.Equal(printed, groups) {
		t.Errorf("%s prints %v, want %v", ciChanges, printed, groups)
	}
	derived := map[string]bool{} // group and module directory
	for _, m := range regexp.MustCompile(`(?m)^\s*(\w+)_deps\+=\$\(deps ([\w./-]+) `).FindAllStringSubmatch(read(ciChanges), -1) {
		derived[m[1]+" "+m[2]] = true
	}
	for _, l := range scriptLines(t, ciWorkflow) {
		if slices.Contains(groups, l.level) && l.dir != "." && ciCommand.MatchString(l.text) && !derived[l.level+" "+l.dir] {
			t.Errorf("%s: a %s lane runs in %s, from which %s derives no files for %s", l.where, l.level, l.dir, ciChanges, l.level)
		}
	}
	workflow := read(ciWorkflow)
	for _, g := range groups {
		if !strings.Contains(workflow, "\n      "+g+": ${{ steps.groups.outputs."+g+" }}\n") {
			t.Errorf("%s's changes job has no output %s", ciWorkflow, g)
		}
	}
	for _, script := range []string{ciWorkflow, ciLocal} {
		seen := map[string]bool{}
		for _, l := range scriptLines(t, script) {
			seen[l.level] = true
		}
		for _, g := range groups {
			if !seen[g] {
				t.Errorf("%s runs nothing at level %s", script, g)
			}
		}
	}
}

// TestCILocalRunsEveryLane: scripts/ci-local.sh runs its checks as lane and
// final functions, started from its lanes and finals lists. One left out of
// its list would still hold its commands, which TestCILocalMirrorsCI reads, and
// never run. Every lane_ and final_ function is in its list once, and each
// list names no other.
func TestCILocalRunsEveryLane(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join(repoRoot(), ciLocal))
	if err != nil {
		t.Fatal(err)
	}
	_, lists := localLevels(t, string(data))
	for kind, min := range map[string]int{"lane": 10, "final": 2} {
		var defined []string
		for _, m := range regexp.MustCompile(`(?m)^`+kind+`_(\w+)\(\) [({]$`).FindAllStringSubmatch(string(data), -1) {
			defined = append(defined, m[1])
		}
		if len(defined) < min {
			t.Fatalf("%s: read only %d %s functions: the test no longer reads the script", ciLocal, len(defined), kind)
		}
		listed, ok := lists[kind+"s"]
		if !ok {
			t.Fatalf("%s: no %ss=(…) line", ciLocal, kind)
		}
		slices.Sort(defined)
		if sorted := slices.Sorted(slices.Values(listed)); !slices.Equal(defined, sorted) {
			t.Errorf("%s: the %s functions are %v, the %ss list %v", ciLocal, kind, defined, kind, sorted)
		}
	}
}
