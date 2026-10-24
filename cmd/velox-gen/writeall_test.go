package main

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
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
			err := writeAll(dir, map[string][]byte{"0002_x.sql": []byte("new"), "schema.json": []byte("new schema"), "velox.sum": []byte("new sum")})
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
	if err := writeAll(dir, map[string][]byte{"0002_x.sql": []byte("new"), "schema.json": []byte("new schema"), "velox.sum": []byte("new sum")}); err != nil {
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
	if err := writeAll(dir, map[string][]byte{"schema.json": []byte("newer"), "velox.sum": []byte("newer")}); !errors.Is(err, injected) || err.Error() != "injected failure" {
		t.Fatalf("%v", err)
	}
	if after := dirFiles(t, dir); !reflect.DeepEqual(after, before) {
		t.Errorf("without links the directory changed:\n%v", after)
	}
}
