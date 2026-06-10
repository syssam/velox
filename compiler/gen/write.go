package gen

import (
	"bytes"
	"os"
	"path/filepath"
)

// WriteFileIfChanged writes data to path atomically (temp file + rename), but
// skips the write entirely when the file already holds exactly data. It
// reports whether the file was written.
//
// The skip is the load-bearing part: it preserves the file's mtime across
// no-op regenerations, which keeps mtime-based toolchains quiet — make rules
// keyed on generated files, file watchers, and editor/gopls indexers all
// re-fire on mtime alone. Go's own build cache is content-addressed and never
// needed this; the skip is for everything that isn't the Go compiler.
//
// Every generated-artifact writer must go through this helper (Jennifer
// files, external templates, the manifest, GraphQL SDL/config, globalid).
// Pinned by write_test.go::TestGen_NoopRegen_PreservesMtimes.
func WriteFileIfChanged(path string, data []byte, perm os.FileMode) (bool, error) {
	if prev, err := os.ReadFile(path); err == nil && bytes.Equal(prev, data) {
		return false, nil
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return false, err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return false, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return false, err
	}
	// CreateTemp creates 0600 files; normalize to the requested mode so fresh
	// generated sources don't end up unreadable to group/other.
	if err := os.Chmod(tmpPath, perm); err != nil {
		os.Remove(tmpPath)
		return false, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return false, err
	}
	return true, nil
}
