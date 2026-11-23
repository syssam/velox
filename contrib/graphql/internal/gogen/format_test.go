package gogen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/syssam/velox/plugin"
)

// TestFormatFiles: formatFiles gofmts every file in place, and its error is
// that of the first file, in order, that does not parse, as formatting the
// files one by one returns.
func TestFormatFiles(t *testing.T) {
	var files []plugin.File
	for i := range 50 {
		src := fmt.Sprintf("package p\nvar  x%d =  %d\n", i, i)
		if i == 7 || i == 30 || i == 49 {
			src = "package p\nfunc {\n"
		}
		files = append(files, plugin.File{Path: fmt.Sprintf("f%d.go", i), Content: src})
	}
	err := formatFiles(files)
	if err == nil || !strings.Contains(err.Error(), "does not parse, which is a bug: f7.go: ") {
		t.Fatalf("got %v, want f7.go's error", err)
	}
	if files[0].Content != "package p\n\nvar x0 = 0\n" || files[48].Content != "package p\n\nvar x48 = 48\n" {
		t.Errorf("not formatted: %q %q", files[0].Content, files[48].Content)
	}
	if err := formatFiles(nil); err != nil {
		t.Errorf("no files: %v", err)
	}
}
