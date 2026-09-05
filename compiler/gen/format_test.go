package gen

import (
	"strings"
	"testing"
)

// TestFormatGoBytes_DoesNotResolveImports pins that the generator's
// formatting pass is format-only: it must not add or remove imports.
//
// Jennifer already tracks every import a generated file needs, so the
// only job left for x/tools/imports is layout — grouping stdlib imports
// apart from third-party ones. With import resolution enabled,
// imports.Process spawned one `go env` subprocess per generated file
// (a fresh ProcessEnv each call), which made generation time scale with
// process-spawn latency and made the cmd/velox check tests time out
// under CPU contention. An unused import surviving the pass is the
// observable proof that resolution is off.
func TestFormatGoBytes_DoesNotResolveImports(t *testing.T) {
	src := []byte(`package x

import (
	"fmt"
	"os"
)

func F() { fmt.Println() }
`)
	out, err := FormatGoBytes("x.go", src)
	if err != nil {
		t.Fatalf("FormatGoBytes: %v", err)
	}
	if !strings.Contains(string(out), `"os"`) {
		t.Fatalf("unused import was removed — FormatGoBytes is resolving imports (spawns `go env` per file):\n%s", out)
	}
}

// TestFormatGoBytes_GroupsStdlibSeparately pins the layout job the pass
// still performs: stdlib imports in their own block above third-party
// imports, matching Ent's assets.format() output.
func TestFormatGoBytes_GroupsStdlibSeparately(t *testing.T) {
	src := []byte(`package x

import (
	"context"
	"github.com/google/uuid"
	"fmt"
)

var _ = context.Background
var _ = uuid.New
var _ = fmt.Sprint
`)
	out, err := FormatGoBytes("x.go", src)
	if err != nil {
		t.Fatalf("FormatGoBytes: %v", err)
	}
	want := "import (\n\t\"context\"\n\t\"fmt\"\n\n\t\"github.com/google/uuid\"\n)"
	if !strings.Contains(string(out), want) {
		t.Fatalf("stdlib/third-party import grouping lost:\n%s", out)
	}
}

// TestFormatGoBytes_SingleImportUnchanged pins that source with no
// parenthesised import block passes through untouched: Jennifer renders a
// lone import on one line, and there is nothing to group.
func TestFormatGoBytes_SingleImportUnchanged(t *testing.T) {
	src := []byte("package x\n\nimport \"fmt\"\n\nvar _ = fmt.Sprint\n")
	out, err := FormatGoBytes("x.go", src)
	if err != nil {
		t.Fatalf("FormatGoBytes: %v", err)
	}
	if string(out) != string(src) {
		t.Fatalf("single-line import was rewritten:\n%s", out)
	}
}

// TestFormatGoBytes_FallsBackOnUnrecognisedBlock pins the safety net: an
// import block the textual regroup does not understand (here, a comment
// inside the block) is handed to the parser-backed format-only pass, which
// still groups stdlib apart from third-party imports.
func TestFormatGoBytes_FallsBackOnUnrecognisedBlock(t *testing.T) {
	src := []byte(`package x

import (
	"github.com/google/uuid" // third-party
	"fmt"
)

var _ = uuid.New
var _ = fmt.Sprint
`)
	if _, ok := regroupImports(src); ok {
		t.Fatal("regroupImports accepted a block with a trailing comment; it must defer to the parser")
	}
	out, err := FormatGoBytes("x.go", src)
	if err != nil {
		t.Fatalf("FormatGoBytes: %v", err)
	}
	want := "import (\n\t\"fmt\"\n\n\t\"github.com/google/uuid\" // third-party\n)"
	if !strings.Contains(string(out), want) {
		t.Fatalf("fallback path lost grouping:\n%s", out)
	}
}

// TestFormatGoBytes_NamedImportsSortByPath pins goimports' ordering rule for
// the textual pass: specs sort by import path, and the alias does not move a
// spec ahead of an unaliased one with a lexically smaller path.
func TestFormatGoBytes_NamedImportsSortByPath(t *testing.T) {
	src := []byte(`package x

import (
	zz "github.com/b/pkg"
	"github.com/a/pkg"
	"os"
	_ "embed"
)
`)
	out, err := FormatGoBytes("x.go", src)
	if err != nil {
		t.Fatalf("FormatGoBytes: %v", err)
	}
	want := "import (\n\t_ \"embed\"\n\t\"os\"\n\n\t\"github.com/a/pkg\"\n\tzz \"github.com/b/pkg\"\n)"
	if !strings.Contains(string(out), want) {
		t.Fatalf("unexpected ordering:\n%s", out)
	}
}
