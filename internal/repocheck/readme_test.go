package repocheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/tempmod"
)

// readmeBlock is one fenced block of the README's quick start, without the
// indentation of its list item.
type readmeBlock struct {
	lang string // sh, go or text
	text string
}

var (
	readmeFence  = regexp.MustCompile("^( *)```(\\w*)$")
	readmeMkdir  = regexp.MustCompile(`^mkdir (\S+) && cd (\S+)$`)
	readmeGoFile = regexp.MustCompile(`^// (\S+\.go)\n`)
)

// quickStartBlocks returns the fenced blocks of the section "Quick start" of a
// README, in order.
func quickStartBlocks(t *testing.T, readme string) []readmeBlock {
	t.Helper()
	_, section, ok := strings.Cut(readme, "\n## Quick start\n")
	if !ok {
		t.Fatal("the README has no Quick start section")
	}
	section, _, _ = strings.Cut(section, "\n## ")
	var (
		out    []readmeBlock
		lines  []string
		lang   string
		indent = -1 // the open fence's indentation, or -1 outside a block
	)
	for l := range strings.SplitSeq(section, "\n") {
		m := readmeFence.FindStringSubmatch(l)
		switch {
		case indent < 0 && m != nil:
			indent, lang, lines = len(m[1]), m[2], nil
		case indent >= 0 && m != nil:
			out = append(out, readmeBlock{lang, strings.Join(lines, "\n") + "\n"})
			indent = -1
		case indent >= 0:
			lines = append(lines, l[min(indent, len(l)):])
		}
	}
	if indent >= 0 {
		t.Fatal("the quick start has a fence that does not close")
	}
	return out
}

// readmeGet is the go get command of a README line, with the version it takes
// offline. The test module has no proxy, so a get without a version, which asks
// for the latest, cannot run: velox is the replaced checkout's placeholder, and
// any other module is at the version the root module requires, which is in the
// module cache.
func readmeGet(t *testing.T, args []string, rootMod string) []string {
	t.Helper()
	path := args[len(args)-1]
	version := ""
	if strings.HasPrefix(path, "github.com/syssam/velox") {
		version = "v0.0.0"
	} else if m := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(path) + ` (v\S+)`).FindStringSubmatch(rootMod); m != nil {
		version = m[1]
	} else {
		t.Fatalf("the README gets %s, which the root module does not require", path)
	}
	return append(args[:len(args)-1:len(args)-1], path+"@"+version)
}

// TestReadmeQuickStart follows the README's quick start as written: it runs each
// command in order, writes each Go block to the path its first line names, and
// compares the last command's output with the text block after it. It departs
// from the text in two ways, both because it works offline: a replace points at
// this checkout straight after go mod init, and a go get names a version.
func TestReadmeQuickStart(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module; run without -short")
	}
	root := repoRoot()
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	rootMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var last string // the output of the last command
	counts := map[string]int{}
	for _, b := range quickStartBlocks(t, string(readme)) {
		counts[b.lang]++
		switch b.lang {
		case "sh":
			for line := range strings.Lines(b.text) {
				line = strings.TrimSpace(line)
				if m := readmeMkdir.FindStringSubmatch(line); m != nil && m[1] == m[2] {
					dir = filepath.Join(dir, m[1])
					if err := os.Mkdir(dir, 0o755); err != nil {
						t.Fatal(err)
					}
					continue
				}
				args := strings.Fields(line)
				if len(args) < 2 || args[0] != "go" {
					t.Fatalf("the quick start has a command this test cannot run: %q", line)
				}
				args = args[1:]
				if args[0] == "get" {
					args = readmeGet(t, args, string(rootMod))
				}
				t.Logf("go %s", strings.Join(args, " "))
				if last, err = tempmod.Go(dir, args...); err != nil {
					t.Fatalf("the quick start's %q: %v", line, err)
				}
				if args[0] == "mod" && args[1] == "init" {
					if _, err := tempmod.Go(dir, "mod", "edit", "-replace", "github.com/syssam/velox="+root); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, "go.sum"), sums, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
		case "go":
			m := readmeGoFile.FindStringSubmatch(b.text)
			if m == nil {
				t.Fatalf("a Go block does not open with a comment naming its file:\n%s", b.text)
			}
			p := filepath.Join(dir, filepath.FromSlash(m[1]))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(b.text), 0o644); err != nil {
				t.Fatal(err)
			}
		case "text":
			if last != b.text {
				t.Errorf("the quick start ends with output\n%s\nthe README says\n%s", last, b.text)
			}
		default:
			t.Fatalf("a block of language %q in the quick start", b.lang)
		}
	}
	if got, want := fmt.Sprint(counts), "map[go:2 sh:4 text:1]"; got != want {
		t.Errorf("the quick start's blocks are %s, want %s: it no longer reads as this test expects", got, want)
	}
}
