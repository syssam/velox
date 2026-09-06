package load

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox/schema/field"

	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	cfg := &Config{Path: "./testdata/valid"}
	spec, err := cfg.Load()
	require.NoError(t, err)
	require.Len(t, spec.Schemas, 3)
	require.Equal(t, "github.com/syssam/velox/compiler/load/testdata/valid", spec.PkgPath)

	require.Equal(t, "Group", spec.Schemas[0].Name, "ordered alphabetically")
	require.Equal(t, "Tag", spec.Schemas[1].Name)
	require.Equal(t, "User", spec.Schemas[2].Name)
}

func TestLoadWrongPath(t *testing.T) {
	cfg := &Config{Path: "./boring"}
	plg, err := cfg.Load()
	require.Error(t, err)
	require.Nil(t, plg)
}

func TestLoadSpecific(t *testing.T) {
	cfg := &Config{Path: "./testdata/valid", Names: []string{"User"}}
	spec, err := cfg.Load()
	require.NoError(t, err)
	require.Len(t, spec.Schemas, 1)
	require.Equal(t, "User", spec.Schemas[0].Name)
	require.Equal(t, "github.com/syssam/velox/compiler/load/testdata/valid", spec.PkgPath)
}

func TestLoadNoSchema(t *testing.T) {
	cfg := &Config{Path: "./testdata/invalid"}
	schemas, err := cfg.Load()
	require.Error(t, err)
	require.Empty(t, schemas)
}

func TestLoadSchemaFailure(t *testing.T) {
	cfg := &Config{Path: "./testdata/failure"}
	spec, err := cfg.Load()
	require.Error(t, err)
	require.Nil(t, spec)
}

func TestLoadBaseSchema(t *testing.T) {
	cfg := &Config{Path: "./testdata/base"}
	spec, err := cfg.Load()
	require.NoError(t, err)
	require.Len(t, spec.Schemas, 1)
	require.Len(t, spec.Schemas[0].Fields, 2, "embedded base schema")
	f1 := spec.Schemas[0].Fields[0]
	require.Equal(t, "base_field", f1.Name)
	require.Equal(t, field.TypeInt, f1.Info.Type)
	f2 := spec.Schemas[0].Fields[1]
	require.Equal(t, "user_field", f2.Name)
	require.Equal(t, field.TypeString, f2.Info.Type)
}

func TestLoadTags(t *testing.T) {
	all, err := (&Config{
		Path: "./testdata/buildflags",
	}).Load()
	require.NoError(t, err)

	require.Len(t, all.Schemas, 2)
	require.Equal(t, "Group", all.Schemas[0].Name, "ordered alphabetically")
	require.Equal(t, "User", all.Schemas[1].Name)

	notags, err := (&Config{
		Path:       "./testdata/buildflags",
		BuildFlags: []string{"-tags", "hidegroups"},
	}).Load()
	require.NoError(t, err)

	require.Len(t, notags.Schemas, 1)
	require.Equal(t, "User", notags.Schemas[0].Name)

	require.Equal(t, all.Schemas[1], notags.Schemas[0])
}

func TestLoadCycleError(t *testing.T) {
	cfg := &Config{Path: "./testdata/cycle"}
	spec, err := cfg.Load()
	require.Nil(t, spec)
	require.EqualError(t, err, `velox/load: parse schema dir: import cycle not allowed: import stack: [github.com/syssam/velox/compiler/load/testdata/cycle github.com/syssam/velox/compiler/load/testdata/cycle/fakevelox github.com/syssam/velox/compiler/load/testdata/cycle]
To resolve this issue, move the custom types used by the generated code to a separate package: "Enum", "Used"`)
}

// TestLoad_CacheDirPersistsAndReuses pins the loader cache: after Load the
// .velox/ dir holds the loader source, its binary and a self-ignoring
// .gitignore, and a second Load of the same schema leaves the binary
// untouched (same mtime) — that is what lets `go build -o` skip the link
// and lets macOS skip validating a fresh executable on first exec. It also
// pins that a stale loader for the same package is pruned.
func TestLoad_CacheDirPersistsAndReuses(t *testing.T) {
	// The cache dir must sit inside the module (internal/ visibility), so
	// the test runs in the package dir like every other loader test and
	// removes what it created afterwards.
	t.Cleanup(func() { _ = os.RemoveAll(cacheDir) })
	cfg := &Config{Path: "./testdata/valid"}
	spec, err := cfg.Load()
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("cache dir missing after Load: %v", err)
	}
	var src, bin string
	for _, e := range entries {
		switch {
		case e.Name() == ".gitignore":
		case strings.HasSuffix(e.Name(), ".go"):
			src = e.Name()
		case strings.HasSuffix(e.Name(), ".bin"):
			bin = e.Name()
		default:
			t.Errorf("unexpected file in cache dir: %s", e.Name())
		}
	}
	if src == "" || bin == "" {
		t.Fatalf("cache dir must hold loader source and binary, got %v", entries)
	}
	if !strings.HasPrefix(src, loaderPrefix(spec.PkgPath)) {
		t.Errorf("loader %q is not prefixed by its package %q", src, loaderPrefix(spec.PkgPath))
	}
	gi, err := os.ReadFile(filepath.Join(cacheDir, ".gitignore"))
	if err != nil || string(gi) != "*\n" {
		t.Errorf("cache dir must ignore itself, got %q, %v", gi, err)
	}
	before, err := os.Stat(filepath.Join(cacheDir, bin))
	if err != nil {
		t.Fatal(err)
	}
	// A stale loader for the same package must be pruned; one for another
	// package must survive.
	stale := filepath.Join(cacheDir, loaderPrefix(spec.PkgPath)+"deadbeef0000.go")
	other := filepath.Join(cacheDir, loaderPrefix("example.com/other")+"cafe.go")
	for _, p := range []string{stale, other} {
		if err := os.WriteFile(p, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := cfg.Load(); err != nil {
		t.Fatalf("second Load: %v", err)
	}
	after, err := os.Stat(filepath.Join(cacheDir, bin))
	if err != nil {
		t.Fatalf("binary was removed by the second Load: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("binary was rewritten on an unchanged schema (mtime %v -> %v); the up-to-date fast path is lost", before.ModTime(), after.ModTime())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale loader for the same package was not pruned: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("loader for another package must be left alone: %v", err)
	}
}

// TestLoad_SecondLoadSkipsLink pins that an unchanged schema does not
// relink: the go tool's own staleness answer (needsLink) is false for the
// cached binary, and the binary's inode survives the second Load untouched.
func TestLoad_SecondLoadSkipsLink(t *testing.T) {
	t.Cleanup(func() { _ = os.RemoveAll(cacheDir) })
	cfg := &Config{Path: "./testdata/valid"}
	if _, err := cfg.Load(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(cacheDir)
	var src string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") {
			src = filepath.Join(cacheDir, e.Name())
		}
	}
	if src == "" {
		t.Fatal("loader source missing")
	}
	if needsLink(src+".bin", src, []string{"-ldflags=-s -w"}) {
		t.Fatal("go build -n reports a link for a binary that was just built; the cache skip is dead")
	}
	if err := os.WriteFile(src, append([]byte("// stale\n"), mustRead(t, src)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if !needsLink(src+".bin", src, []string{"-ldflags=-s -w"}) {
		t.Fatal("go build -n must report a link after the source changed")
	}
}

// TestPruneStaleLoaders_Anchored pins that pruning only removes this
// package's own stale loaders: a nested package, a `_`-suffixed sibling,
// and an in-flight temp binary must all survive.
func TestPruneStaleLoaders_Anchored(t *testing.T) {
	dir := t.TempDir()
	pkg := "example.com/a/schema"
	keep := filename(pkg, []byte("current"))
	touch := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	stale := touch(filename(pkg, []byte("old")) + ".go")
	staleBin := touch(filename(pkg, []byte("old")) + ".go.bin")
	current := touch(keep + ".go")
	currentBin := touch(keep + ".go.bin")
	inflight := touch(keep + ".go.bin.123456.tmp")
	nested := touch(filename(pkg+"/v2", []byte("v2")) + ".go")
	sibling := touch(filename(pkg+"_legacy", []byte("l")) + ".go")

	pruneStaleLoaders(dir, pkg, keep)

	for _, p := range []string{stale, staleBin} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("stale loader %s must be pruned", filepath.Base(p))
		}
	}
	for _, p := range []string{current, currentBin, inflight, nested, sibling} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must survive pruning: %v", filepath.Base(p), err)
		}
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
