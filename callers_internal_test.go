package velox

import (
	"runtime"
	"testing"
)

func TestRelFile(t *testing.T) {
	mods := []string{"github.com/syssam/velox2/example", "github.com/syssam/velox2", "gopkg.in/yaml.v3"}
	for _, c := range []struct{ file, fn, mainPkg, want string }{
		{"/src/velox2/db.go", "github.com/syssam/velox2.(*DB).plain", "", "db.go"},
		{"/src/velox2/internal/testrepo/testrepo.go", "github.com/syssam/velox2/internal/testrepo.FirstInvoice", "", "internal/testrepo/testrepo.go"},
		// The longer module wins.
		{"/src/velox2/example/billing/invoice.go", "github.com/syssam/velox2/example/billing.(*Invoice).Schema", "", "billing/invoice.go"},
		{"/src/velox2/example/main.go", "main.run.func1", "github.com/syssam/velox2/example", "main.go"},
		{"/src/velox2/example/tool/main.go", "main.run", "github.com/syssam/velox2/example/tool", "tool/main.go"},
		{"/src/velox2/callers_test.go", "github.com/syssam/velox2_test.TestX", "", "callers_test.go"},
		{"/src/velox2/internal/testrepo/x_test.go", "github.com/syssam/velox2/internal/testrepo_test.TestX", "", "internal/testrepo/x_test.go"},
		// -trimpath file names.
		{"github.com/syssam/velox2/example/billing/invoice.go", "github.com/syssam/velox2/example/billing.F", "", "billing/invoice.go"},
		{"gopkg.in/yaml.v3@v3.0.1/decode.go", "gopkg.in/yaml%2ev3.(*decoder).unmarshal", "", "decode.go"},
		{"/src/yaml/decode.go", "gopkg.in/yaml%2ev3.Unmarshal", "", "decode.go"},
		// No module: the base name.
		{"/x/other/f.go", "example.com/other.F", "", "f.go"},
	} {
		if got := relFile(c.file, c.fn, c.mainPkg, mods); got != c.want {
			t.Errorf("relFile(%q, %q, %q) = %q, want %q", c.file, c.fn, c.mainPkg, got, c.want)
		}
	}
}

func TestPkgOf(t *testing.T) {
	for fn, want := range map[string]string{
		"github.com/syssam/velox2.(*DB).plain": "github.com/syssam/velox2",
		"github.com/syssam/velox2.Query[go.shape.struct { github.com/syssam/velox2/internal/testmodel.ID int64 }].All": "github.com/syssam/velox2",
		"github.com/syssam/velox2/example.run":      "github.com/syssam/velox2/example",
		"github.com/syssam/velox2_test.TestX.func1": "github.com/syssam/velox2_test",
		"gopkg.in/yaml%2ev3.Marshal":                "gopkg.in/yaml.v3",
		"main.run.func1":                            "main",
	} {
		if got := pkgOf(fn); got != want {
			t.Errorf("pkgOf(%q) = %q, want %q", fn, got, want)
		}
	}
}

func TestInPkg(t *testing.T) {
	const velox = "github.com/syssam/velox2"
	for _, c := range []struct {
		fn, pkg string
		want    bool
	}{
		{"github.com/syssam/velox2.Query[go.shape.struct { github.com/syssam/velox2/internal/testmodel.ID int64 }].All", velox, true},
		{"github.com/syssam/velox2/example.run", velox, false},
		{"github.com/syssam/velox2_test.TestX", velox, false},
		{"gopkg.in/yaml%2ev3.Marshal", "gopkg.in/yaml.v3", true},
	} {
		if got := inPkg(c.fn, c.pkg); got != c.want {
			t.Errorf("inPkg(%q, %q) = %v", c.fn, c.pkg, got)
		}
	}
}

// thisLine returns the line it is called from.
func thisLine() int { _, _, line, _ := runtime.Caller(1); return line }

func TestPCCacheIsBounded(t *testing.T) {
	defer func(n int) { maxPCs = n }(maxPCs)
	maxPCs = 2
	// This test is in velox's own package: load a self that is not, so that
	// its frames are the application's. Without module paths, File is the
	// base name.
	c := &callers{}
	c.once.Do(func() { c.self = "velox.invalid/none" })
	want := func(line int) Caller {
		return Caller{File: "callers_internal_test.go", Line: line, Func: "github.com/syssam/velox2.TestPCCacheIsBounded"}
	}
	for i := range 3 {
		if got, line := c.capture(), thisLine(); got != want(line) {
			t.Errorf("round %d, first site: %+v, want line %d", i, got, line)
		}
		if got, line := c.capture(), thisLine(); got != want(line) {
			t.Errorf("round %d, second site: %+v, want line %d", i, got, line)
		}
		if got, line := c.capture(), thisLine(); got != want(line) {
			t.Errorf("round %d, third site: %+v, want line %d", i, got, line)
		}
		if len(c.pcs) > 2 {
			t.Fatalf("round %d: %d PCs cached, at most 2 allowed", i, len(c.pcs))
		}
	}
	if len(c.pcs) != 2 {
		t.Errorf("%d PCs cached, want 2", len(c.pcs))
	}
}
