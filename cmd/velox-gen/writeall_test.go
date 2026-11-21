package main

import (
	"errors"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/syssam/velox/internal/genrun"
)

// TestWriteDirHeld: a signal that arrives while migrate writes waits until
// the write is undone, the directories it made among the undo.
func TestWriteDirHeld(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "migrations")
	injected := errors.New("injected failure")
	genrun.WriteTemp = func(string, string, []byte) (string, error) {
		c := make(chan os.Signal, 1) // a second holder, to see the signal arrive
		signal.Notify(c, syscall.SIGTERM)
		defer signal.Stop(c)
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			return "", err
		}
		select {
		case <-c:
		case <-time.After(10 * time.Second):
		}
		return "", injected
	}
	defer func() { genrun.WriteTemp = genrun.WriteTempFile }()
	interrupted, err := writeDirHeld(dir, map[string][]byte{"1_x.sql": []byte("x")})
	if !interrupted || !errors.Is(err, injected) {
		t.Fatalf("%v %v", interrupted, err)
	}
	if _, err := os.Stat(filepath.Dir(dir)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the directories made are left: %v", err)
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
