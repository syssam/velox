package main

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestWriteAllRollback: a failed swap puts back the files writeAll replaced,
// by renaming them back, so their mode is kept; a failure to put one back
// names the paths left behind.
func TestWriteAllRollback(t *testing.T) {
	injected := errors.New("injected failure")
	for _, c := range []struct {
		name string
		fail func(from, to string) bool
		left []string // what the error names as left behind
	}{
		{"swap", func(from, to string) bool { return filepath.Base(to) == "velox.sum" && strings.Contains(from, ".tmp-") }, nil},
		{"undo", func(from, to string) bool {
			return filepath.Base(to) == "velox.sum" && strings.Contains(from, ".tmp-") ||
				filepath.Base(to) == "schema.json" && strings.HasPrefix(filepath.Base(from), ".schema.json.old-")
		}, []string{"schema.json"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "schema.json"), "old schema")
			writeFile(t, filepath.Join(dir, "velox.sum"), "old sum")
			if err := os.Chmod(filepath.Join(dir, "schema.json"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := dirFiles(t, dir)
			renameFile = func(from, to string) error {
				if c.fail(from, to) {
					return injected
				}
				return os.Rename(from, to)
			}
			defer func() { renameFile = os.Rename }()
			err := writeAll(dir, map[string][]byte{"0002_x.sql": []byte("new"), "schema.json": []byte("new schema"), "velox.sum": []byte("new sum")}, nil)
			if !errors.Is(err, injected) {
				t.Fatalf("%v", err)
			}
			if c.left == nil {
				if err.Error() != "injected failure" {
					t.Errorf("%v", err)
				}
				if after := dirFiles(t, dir); !reflect.DeepEqual(after, before) {
					t.Errorf("the directory changed:\n%v\nwas\n%v", slices.Sorted(maps.Keys(after)), slices.Sorted(maps.Keys(before)))
				}
				if fi, err := os.Stat(filepath.Join(dir, "schema.json")); err != nil || fi.Mode().Perm() != 0o600 {
					t.Errorf("schema.json: %v, %v; want mode 0600", fi.Mode(), err)
				}
				return
			}
			for _, p := range c.left {
				if !strings.Contains(err.Error(), filepath.Join(dir, p)) || !strings.Contains(err.Error(), "left") {
					t.Errorf("%v; want it to name %s as left behind", err, p)
				}
			}
		})
	}
}

// TestWriteAllKeepsTarget: a replaced file is at its path throughout the
// swap, the original kept by a hard link; where links fail, the old way
// still rolls back.
func TestWriteAllKeepsTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "schema.json"), "old schema")
	writeFile(t, filepath.Join(dir, "velox.sum"), "old sum")
	renameFile = func(from, to string) error {
		if strings.Contains(from, ".tmp-") {
			if _, err := os.Stat(to); err != nil && filepath.Base(to) != "0002_x.sql" {
				t.Errorf("%s is missing during its swap: %v", filepath.Base(to), err)
			}
		}
		return os.Rename(from, to)
	}
	defer func() { renameFile = os.Rename }()
	if err := writeAll(dir, map[string][]byte{"0002_x.sql": []byte("new"), "schema.json": []byte("new schema"), "velox.sum": []byte("new sum")}, nil); err != nil {
		t.Fatal(err)
	}
	if got := dirFiles(t, dir); !reflect.DeepEqual(got, map[string]string{"0002_x.sql": "new", "schema.json": "new schema", "velox.sum": "new sum"}) {
		t.Errorf("%v", got)
	}

	injected := errors.New("injected failure")
	linkFile = func(string, string) error { return errors.New("links unsupported") }
	defer func() { linkFile = os.Link }()
	before := dirFiles(t, dir)
	renameFile = func(from, to string) error {
		if filepath.Base(to) == "velox.sum" && strings.Contains(from, ".tmp-") {
			return injected
		}
		return os.Rename(from, to)
	}
	if err := writeAll(dir, map[string][]byte{"schema.json": []byte("newer"), "velox.sum": []byte("newer")}, nil); !errors.Is(err, injected) || err.Error() != "injected failure" {
		t.Fatalf("%v", err)
	}
	if after := dirFiles(t, dir); !reflect.DeepEqual(after, before) {
		t.Errorf("without links the directory changed:\n%v", after)
	}
}

// treeFiles is the files below dir, keyed by slash path, with their content;
// directories are not listed.
func treeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestWriteAllRemoves: writeAll removes what it is told to with the write,
// and a failure puts it back, with the directories it made gone.
func TestWriteAllRemoves(t *testing.T) {
	files := map[string][]byte{"a.go": []byte("A"), "pb/x.proto": []byte("X"), "old.go": []byte("new")}
	setup := func() string {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "old.go"), "old")
		writeFile(t, filepath.Join(dir, "gone.go"), "gone")
		return dir
	}
	dir := setup()
	if err := writeAll(dir, files, []string{"gone.go"}); err != nil {
		t.Fatal(err)
	}
	if got := treeFiles(t, dir); !reflect.DeepEqual(got, map[string]string{"a.go": "A", "old.go": "new", "pb/x.proto": "X"}) {
		t.Errorf("%v", got)
	}

	dir = setup()
	before := treeFiles(t, dir)
	injected := errors.New("injected failure")
	renameFile = func(from, to string) error {
		if to == filepath.Join(dir, "pb", "x.proto") {
			return injected
		}
		return os.Rename(from, to)
	}
	defer func() { renameFile = os.Rename }()
	if err := writeAll(dir, files, []string{"gone.go"}); !errors.Is(err, injected) || err.Error() != "injected failure" {
		t.Fatalf("%v", err)
	}
	if after := treeFiles(t, dir); !reflect.DeepEqual(after, before) {
		t.Errorf("the directory changed:\n%v\nwas\n%v", after, before)
	}
	if exists(filepath.Join(dir, "pb")) {
		t.Error("a failed write left the directory it made")
	}
}

// TestWriteAllCaseRename: a file, or a directory, whose new name differs
// only in case takes the old one's place, on a file system that folds case
// or not.
func TestWriteAllCaseRename(t *testing.T) {
	names := func(dir string) []string {
		t.Helper()
		des, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var ns []string
		for _, de := range des {
			ns = append(ns, de.Name())
		}
		return ns
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "user.go"), "a")
	if err := writeAll(dir, map[string][]byte{"User.go": []byte("b")}, []string{"user.go"}); err != nil {
		t.Fatal(err)
	}
	if got := treeFiles(t, dir); !reflect.DeepEqual(got, map[string]string{"User.go": "b"}) {
		t.Errorf("%v", got)
	}
	if got := names(dir); !slices.Equal(got, []string{"User.go"}) {
		t.Errorf("the directory lists %v", got)
	}

	dir = t.TempDir()
	writeFile(t, filepath.Join(dir, "Pb", "X.go"), "a")
	before := treeFiles(t, dir)
	remove := []string{"Pb/X.go", "Pb"}
	files := map[string][]byte{"pb/x.go": []byte("b")}
	injected := errors.New("injected failure")
	renameFile = func(from, to string) error {
		if strings.Contains(from, ".tmp-") {
			return injected
		}
		return os.Rename(from, to)
	}
	defer func() { renameFile = os.Rename }()
	if err := writeAll(dir, files, remove); !errors.Is(err, injected) {
		t.Fatalf("%v", err)
	}
	if after := treeFiles(t, dir); !reflect.DeepEqual(after, before) {
		t.Errorf("the directory changed:\n%v\nwas\n%v", after, before)
	}
	if got := names(dir); !slices.Equal(got, []string{"Pb"}) {
		t.Errorf("the directory lists %v", got)
	}
	renameFile = os.Rename
	if err := writeAll(dir, files, remove); err != nil {
		t.Fatal(err)
	}
	if got := treeFiles(t, dir); !reflect.DeepEqual(got, map[string]string{"pb/x.go": "b"}) {
		t.Errorf("%v", got)
	}
	if got := names(dir); !slices.Equal(got, []string{"pb"}) {
		t.Errorf("the directory lists %v", got)
	}
}

// TestWriteAllUndoSetAside: a failure while the removals are set aside puts
// back those set aside before it, and a failure to put one back names where
// what it held is.
func TestWriteAllUndoSetAside(t *testing.T) {
	injected := errors.New("injected failure")
	defer func() { renameFile = os.Rename }()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "a")
	writeFile(t, filepath.Join(dir, "b.go"), "b")
	before := treeFiles(t, dir)
	renameFile = func(from, to string) error {
		if from == filepath.Join(dir, "b.go") && strings.Contains(to, ".gone-velox-") {
			return injected
		}
		return os.Rename(from, to)
	}
	if err := writeAll(dir, map[string][]byte{"c.go": []byte("c")}, []string{"a.go", "b.go"}); !errors.Is(err, injected) || err.Error() != "injected failure" {
		t.Fatalf("%v", err)
	}
	if after := treeFiles(t, dir); !reflect.DeepEqual(after, before) {
		t.Errorf("the directory changed:\n%v\nwas\n%v", after, before)
	}

	dir = t.TempDir()
	writeFile(t, filepath.Join(dir, "gone.go"), "gone")
	renameFile = func(from, to string) error {
		if strings.Contains(from, ".tmp-velox-") || strings.Contains(from, ".gone-velox-") {
			return injected
		}
		return os.Rename(from, to)
	}
	err := writeAll(dir, map[string][]byte{"a.go": []byte("A")}, []string{"gone.go"})
	if !errors.Is(err, injected) || !strings.Contains(err.Error(), "undoing the write failed, which left "+filepath.Join(dir, "gone.go")+" (what it held is in "+filepath.Join(dir, ".gone.go.gone-velox-")) {
		t.Fatalf("%v", err)
	}
}

// TestWriteAllUndoNamesDirs: a directory it made that the undo cannot remove
// is named.
func TestWriteAllUndoNamesDirs(t *testing.T) {
	dir := t.TempDir()
	injected := errors.New("injected failure")
	renameFile = func(from, to string) error {
		if strings.Contains(from, ".tmp-velox-") {
			writeFile(t, filepath.Join(dir, "pb", "mine.txt"), "mine")
			return injected
		}
		return os.Rename(from, to)
	}
	defer func() { renameFile = os.Rename }()
	err := writeAll(dir, map[string][]byte{"pb/x.proto": []byte("X")}, nil)
	if !errors.Is(err, injected) || !strings.HasSuffix(err.Error(), "undoing the write failed, which left "+filepath.Join(dir, "pb")) {
		t.Fatalf("%v", err)
	}
}

// TestWriteAllKeepsANewFile: the commit deletes only the paths of remove and
// then their directories when they are empty, so a file added to a directory
// set aside is kept, and named.
func TestWriteAllKeepsANewFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pb", "x.go"), "x")
	var aside string
	renameFile = func(from, to string) error {
		err := os.Rename(from, to)
		if err == nil && from == filepath.Join(dir, "pb") {
			aside = to
			writeFile(t, filepath.Join(to, "mine.txt"), "mine")
		}
		return err
	}
	defer func() { renameFile = os.Rename }()
	err := writeAll(dir, map[string][]byte{"a.go": []byte("A")}, []string{"pb", "pb/x.go"})
	if err == nil || err.Error() != "the files are written, but removing the old ones failed, which left "+aside {
		t.Fatalf("%v", err)
	}
	rel, _ := filepath.Rel(dir, aside)
	if got := treeFiles(t, dir); !reflect.DeepEqual(got, map[string]string{"a.go": "A", rel + "/mine.txt": "mine"}) {
		t.Errorf("%v", got)
	}
}

// TestHoldSignals: a SIGINT that arrives while the signals are held does not
// end the process, and release reports it.
func TestHoldSignals(t *testing.T) {
	sig := syscall.SIGINT
	if signal.Ignored(sig) { // as in a shell's background job; it stays ignored
		sig = syscall.SIGTERM
	}
	h := holdSignals()
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		h.release()
		t.Fatal(err)
	}
	waitHeld(h)
	if !h.release() {
		t.Errorf("release does not report the %v", sig)
	}
	if holdSignals().release() {
		t.Error("release reports a signal that never came")
	}
}

// waitHeld waits up to 10s for h to hold a signal.
func waitHeld(h *held) {
	for deadline := time.Now().Add(10 * time.Second); len(h.ch) == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
}

// TestHoldSignalsKeepsIgnored: holding the signals does not turn on one the
// process ignores.
func TestHoldSignalsKeepsIgnored(t *testing.T) {
	if !signal.Ignored(syscall.SIGTERM) {
		signal.Ignore(syscall.SIGTERM)
		defer func() { // Reset does not undo Ignore; Notify does
			c := make(chan os.Signal, 1)
			signal.Notify(c, syscall.SIGTERM)
			signal.Stop(c)
		}()
	}
	h := holdSignals()
	if !signal.Ignored(syscall.SIGTERM) {
		h.release()
		t.Fatal("holdSignals turned on the ignored SIGTERM")
	}
	h.release()
	if !signal.Ignored(syscall.SIGTERM) {
		t.Error("release turned on the ignored SIGTERM")
	}
}

// TestWriteDirHeld: a signal that arrives while migrate writes waits until
// the write is undone, the directories it made among the undo.
func TestWriteDirHeld(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "migrations")
	injected := errors.New("injected failure")
	writeTemp = func(string, string, []byte) (string, error) {
		h := holdSignals() // a second holder, to see the signal arrive
		defer h.release()
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			return "", err
		}
		waitHeld(h)
		return "", injected
	}
	defer func() { writeTemp = writeTempFile }()
	interrupted, err := writeDirHeld(dir, map[string][]byte{"1_x.sql": []byte("x")})
	if !interrupted || !errors.Is(err, injected) {
		t.Fatalf("%v %v", interrupted, err)
	}
	if _, err := os.Stat(filepath.Dir(dir)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the directories made are left: %v", err)
	}
}

// TestMakeDirsNamesWhatItLeft: when MkdirAll fails part-way, makeDirs
// removes the directories it made, and the error names those it could not.
func TestMakeDirsNamesWhatItLeft(t *testing.T) {
	base := t.TempDir()
	a, b := filepath.Join(base, "a"), filepath.Join(base, "a", "b")
	injected := errors.New("injected failure")
	mkdirAll = func(string, os.FileMode) error {
		// a is made, and something lands in it, then b fails.
		writeFile(t, filepath.Join(a, "f"), "f")
		return injected
	}
	defer func() { mkdirAll = os.MkdirAll }()
	_, err := makeDirs(filepath.Join(b, "c"))
	if !errors.Is(err, injected) || err.Error() != "injected failure; removing the directories it made failed, which left "+a {
		t.Fatalf("%v", err)
	}
	// With nothing in the way, what it made is removed, and the error is
	// MkdirAll's alone.
	if err := os.RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	mkdirAll = func(dir string, _ os.FileMode) error {
		if err := os.Mkdir(a, 0o755); err != nil {
			return err
		}
		return injected
	}
	if _, err := makeDirs(filepath.Join(b, "c")); err == nil || err.Error() != "injected failure" {
		t.Fatalf("%v", err)
	}
	if _, err := os.Stat(a); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a is left: %v", err)
	}
}

// TestSweepLeftovers: what a killed write left is removed, or put back where
// its name is missing; a dot-file of yours stays, and so does a file of yours
// in a directory set aside, with the directory, never removed whole.
func TestSweepLeftovers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "new a")
	writeFile(t, filepath.Join(dir, ".a.go.old-velox-1"), "old a")
	writeFile(t, filepath.Join(dir, ".a.go.tmp-velox-2"), "newer a")
	writeFile(t, filepath.Join(dir, ".b.go.tmp-velox-3"), "new b")
	writeFile(t, filepath.Join(dir, ".c.go.old-velox-4"), "old c")
	writeFile(t, filepath.Join(dir, ".d.go.gone-velox-5"), "d")
	writeFile(t, filepath.Join(dir, ".e.go.gone-velox-6"), "old e")
	writeFile(t, filepath.Join(dir, "e.go"), "e")
	writeFile(t, filepath.Join(dir, ".pb.gone-velox-7", "x.go"), "x")
	writeFile(t, filepath.Join(dir, ".qb.gone-velox-8", "y.go"), header+"\n\ny")
	writeFile(t, filepath.Join(dir, ".qb.gone-velox-8", "sub", "z.go"), header+"\n\nz")
	writeFile(t, filepath.Join(dir, "qb", "y.go"), "new y")
	writeFile(t, filepath.Join(dir, ".rb.gone-velox-9", "sub", "z.go"), header+"\n\nz")
	writeFile(t, filepath.Join(dir, ".rb.gone-velox-9", "sub", "mine.go"), "mine")
	writeFile(t, filepath.Join(dir, ".rb.gone-velox-9", "notes.txt"), "mine")
	writeFile(t, filepath.Join(dir, "rb", "z.go"), "new z")
	mine := []string{".env", ".a.go.old-1", ".a.go.old-velox-", ".a.go.bak-velox-9", "a.go.old-velox-9"}
	for _, m := range mine {
		writeFile(t, filepath.Join(dir, m), "mine")
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := sweepLeftovers(dir, des)
	if err != nil {
		t.Fatal(err)
	}
	j := func(n string) string { return filepath.Join(dir, n) }
	if want := []swept{
		{path: j(".a.go.old-velox-1")},
		{path: j(".a.go.tmp-velox-2")},
		{path: j(".b.go.tmp-velox-3")},
		{path: j(".c.go.old-velox-4"), restored: j("c.go")},
		{path: j(".d.go.gone-velox-5"), restored: j("d.go")},
		{path: j(".e.go.gone-velox-6")},
		{path: j(".pb.gone-velox-7"), restored: j("pb")},
		{path: j(".qb.gone-velox-8")},
		{path: j(".rb.gone-velox-9"), kept: []string{j(".rb.gone-velox-9/notes.txt"), j(".rb.gone-velox-9/sub/mine.go")}},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("%v\nwant %v", got, want)
	}
	wantTree := map[string]string{
		"a.go": "new a", "c.go": "old c", "d.go": "d", "e.go": "e", "pb/x.go": "x", "qb/y.go": "new y", "rb/z.go": "new z",
		".rb.gone-velox-9/notes.txt": "mine", ".rb.gone-velox-9/sub/mine.go": "mine",
	}
	for _, m := range mine {
		wantTree[m] = "mine"
	}
	if tree := treeFiles(t, dir); !reflect.DeepEqual(tree, wantTree) {
		t.Errorf("%v", tree)
	}
}
