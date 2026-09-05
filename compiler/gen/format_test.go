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
