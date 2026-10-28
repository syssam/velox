package velox

import (
	"runtime"
	"strings"
	"sync"
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

// TestPCCacheConcurrentAtBound: three call sites compete for two slots in the
// cache while goroutines capture them; every capture is right and the cache
// stays within its bound. The sites are literal, as in TestPCCacheIsBounded: a
// loop or descend is one site, so one pc.
func TestPCCacheConcurrentAtBound(t *testing.T) {
	defer func(n int) { maxPCs = n }(maxPCs)
	maxPCs = 2
	c := &callers{}
	c.once.Do(func() { c.self = "velox.invalid/none" })
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			check := func(site string, got Caller, line int) {
				if got.File != "callers_internal_test.go" || got.Line == 0 || got.Line != line {
					t.Errorf("goroutine %d, %s site: %+v, want line %d", g, site, got, line)
				}
			}
			for range 100 {
				got, line := c.capture(), thisLine()
				check("first", got, line)
				got, line = c.capture(), thisLine()
				check("second", got, line)
				got, line = c.capture(), thisLine()
				check("third", got, line)
			}
		})
	}
	wg.Wait()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.pcs) > 2 {
		t.Errorf("%d PCs cached, at most 2 allowed", len(c.pcs))
	}
}

// descend calls capture n frames down.
func descend(c *callers, n int) Caller {
	if n > 0 {
		return descend(c, n-1)
	}
	return c.capture()
}

// TestCaptureReadsDeepStacks: the first unwinding is sized by the last call
// site's depth, and the search goes on in batches to 64 frames, whatever the
// order of shallow and deep sites. Every frame of this test is velox's own, so
// the call site is testing's frame above it.
func TestCaptureReadsDeepStacks(t *testing.T) {
	c := &callers{}
	for _, n := range []int{0, 1, 5, 2, 7, 8, 9, 20, 3, 40, 0, 52, 4, 0} {
		if got := descend(c, n); got.Func != "testing.tRunner" {
			t.Errorf("%d frames down: %+v", n, got)
		}
	}
	// Past 64 frames there is no call site.
	if got := descend(c, 100); got != (Caller{}) {
		t.Errorf("100 frames down: %+v", got)
	}
}

// TestCaptureReadsAtMost64Frames: the search stops at the 64th frame, whatever
// size the first batch was. Every frame of this test is velox's own but
// strings.Map's, which calls descend's caller back: it is the call site when
// it is the 64th frame, and none is found when it is the 65th.
func TestCaptureReadsAtMost64Frames(t *testing.T) {
	// at returns the call site found with strings.Map frame k (from 1) down
	// from capture's caller, the first batch being hint frames.
	at := func(k, hint int) Caller {
		c := &callers{}
		c.depth.Store(int32(hint))
		var got Caller
		// descend's frames are 1 to k-2, the closure's k-1.
		if s := strings.Map(func(r rune) rune { got = descend(c, k-3); return r }, "x"); s != "x" {
			t.Fatalf("strings.Map returned %q", s)
		}
		return got
	}
	for _, hint := range []int{0, 4, 5, 7, 8} {
		if got := at(64, hint); got.Func != "strings.Map" {
			t.Errorf("first batch %d, strings.Map 64 frames down: %+v", hint, got)
		}
		if got := at(65, hint); got != (Caller{}) {
			t.Errorf("first batch %d, strings.Map 65 frames down: %+v", hint, got)
		}
	}
}

// TestDepthHintIsAtLeast4: a call site found in the first frames sizes the
// next first batch at 4, the smallest, as at the start.
func TestDepthHintIsAtLeast4(t *testing.T) {
	c := &callers{}
	c.once.Do(func() { c.self = "velox.invalid/none" })
	if got := c.capture(); got.Func != "github.com/syssam/velox2.TestDepthHintIsAtLeast4" {
		t.Fatalf("call site %+v", got)
	}
	if d := c.depth.Load(); d != 4 {
		t.Errorf("depth hint %d, want 4", d)
	}
}
