package velox_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestReviveConfigsAgree pins revive.toml, which CI runs on generated code, to
// the revive rules of .golangci.yml: the same rules, in the same order, with
// the same arguments.
func TestReviveConfigsAgree(t *testing.T) {
	yml, err := os.ReadFile(".golangci.yml")
	if err != nil {
		t.Fatal(err)
	}
	toml, err := os.ReadFile("revive.toml")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(yml), "\n    revive:\n")
	if !ok {
		t.Fatal(".golangci.yml has no revive settings")
	}
	var fromYML []string
	for _, l := range strings.Split(rest, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "- name: "):
			fromYML = append(fromYML, strings.TrimPrefix(l, "- name: "))
		case strings.HasPrefix(l, "arguments: "):
			fromYML[len(fromYML)-1] += " " + strings.TrimPrefix(l, "arguments: ")
		case l == "rules:" || l == "" || strings.HasPrefix(l, "#"):
		default:
			if !strings.HasPrefix(l, "issues:") && len(fromYML) > 0 {
				t.Fatalf(".golangci.yml: unexpected line in the revive rules: %q", l)
			}
		}
		if strings.HasPrefix(l, "issues:") {
			break
		}
	}
	rule := regexp.MustCompile(`^\[rule\.([a-z-]+)\]$`)
	var fromTOML []string
	for _, l := range strings.Split(string(toml), "\n") {
		l = strings.TrimSpace(l)
		if m := rule.FindStringSubmatch(l); m != nil {
			fromTOML = append(fromTOML, m[1])
		} else if a, ok := strings.CutPrefix(l, "arguments = "); ok && len(fromTOML) > 0 {
			fromTOML[len(fromTOML)-1] += " " + strings.ReplaceAll(a, " = ", ": ")
		}
	}
	if len(fromYML) == 0 || !slices.Equal(fromYML, fromTOML) {
		t.Errorf("revive rules differ:\n.golangci.yml: %q\nrevive.toml:   %q", fromYML, fromTOML)
	}
	if !strings.Contains(string(toml), "\nignoreGeneratedHeader = true\n") {
		t.Error("revive.toml does not lint generated files")
	}
}
