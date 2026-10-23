package buildinfo_test

import (
	"strings"
	"testing"

	"github.com/syssam/velox2/internal/buildinfo"
)

func TestGenerator(t *testing.T) {
	if got := buildinfo.Generator(); !strings.HasPrefix(got, "velox-gen ") || len(got) == len("velox-gen ") {
		t.Errorf("Generator() = %q", got)
	}
}
