package tempmod

import (
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
