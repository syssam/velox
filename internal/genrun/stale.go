package genrun

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// warnOldDirs warns of each file of the user's in out/entity or out/runtime,
// where an older velox-gen wrote.
func warnOldDirs(dir, out, pkg string, log *Log) {
	// An older velox-gen wrote the entities in out/entity: the sweep removes
	// its files there, and the user's files stay, and are named.
	ed := filepath.Join(out, "entity")
	matches, _ := filepath.Glob(filepath.Join(ed, "*.go"))
	for _, p := range matches {
		if !IsGenerated(p) {
			log.warn(fmt.Sprintf("%s is yours, and stays; velox-gen writes every entity in %s and does not build %s: move the file to %[2]s, as package %[4]s, or delete it",
				RelPath(dir, p), RelPath(dir, out), RelPath(dir, ed), pkg))
		}
	}
	// An older velox-gen wrote package runtime in out/runtime, which the
	// sweep removes; the user's files stay, and are named.
	rd := filepath.Join(out, "runtime")
	matches, _ = filepath.Glob(filepath.Join(rd, "*.go"))
	for _, p := range matches {
		if !IsGenerated(p) {
			log.warn(fmt.Sprintf("%s is yours, and stays; velox-gen no longer writes package runtime: the generated package binds the schema itself, and no program imports %s: delete the file",
				RelPath(dir, p), RelPath(dir, rd)))
		}
	}
}

// checkWantPaths refuses a path of want, in order, that is not velox-gen's.
func checkWantPaths(dir, out string, want map[string][]byte) error {
	// Nothing is written before every path is known to be velox-gen's: a
	// regular file with its header, below real directories.
	for _, p := range slices.Sorted(maps.Keys(want)) {
		for d := filepath.Dir(p); d != out && within(out, d); d = filepath.Dir(d) {
			if err := checkEntry(dir, d, true); err != nil {
				return err
			}
		}
		if err := checkEntry(dir, p, false); err != nil {
			return err
		}
		if _, err := os.Lstat(p); err == nil && !IsGenerated(p) {
			return fmt.Errorf("%s was not written by velox-gen; move it", RelPath(dir, p))
		}
	}
	return nil
}

// reportSwept tells stderr what the sweep did with each entry an
// interrupted run left.
func reportSwept(dir string, swept []swept, stderr io.Writer) {
	for _, s := range swept {
		switch {
		case s.restored != "":
			_, _ = fmt.Fprintf(stderr, "velox-gen: restored %s from %s, which an interrupted run left\n", RelPath(dir, s.restored), RelPath(dir, s.path))
		case len(s.kept) > 0:
			for _, k := range s.kept {
				_, _ = fmt.Fprintf(stderr, "velox-gen: kept %s, which is not velox-gen's, in %s, which an interrupted run left; move it\n", RelPath(dir, k), RelPath(dir, s.path))
			}
		default:
			_, _ = fmt.Fprintf(stderr, "velox-gen: removed %s, which an interrupted run left\n", RelPath(dir, s.path))
		}
	}
}

// reportCheck names, for a check, each changed file and each leftover of an
// interrupted run, and returns their paths, relative to dir.
func reportCheck(dir string, changed, leftovers []string, stderr io.Writer) []string {
	var stale []string
	for _, p := range changed {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %s is out of date\n", RelPath(dir, p))
		stale = append(stale, RelPath(dir, p))
	}
	for _, p := range leftovers {
		_, _ = fmt.Fprintf(stderr, "velox-gen: %s is left over from an interrupted run\n", RelPath(dir, p))
		stale = append(stale, RelPath(dir, p))
	}
	return stale
}

// checkEntry refuses what is at p, when anything is, unless it is a directory
// (dir) or a regular file (!dir). A symbolic link is refused either way: what
// velox-gen writes or removes stays below -out.
func checkEntry(dir, p string, isDir bool) error {
	fi, err := os.Lstat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case fi.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("%s is a symbolic link; velox-gen writes only regular files below real directories; move it", RelPath(dir, p))
	case isDir && !fi.IsDir():
		return fmt.Errorf("%s is not a directory; move it", RelPath(dir, p))
	case !isDir && !fi.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file; move it", RelPath(dir, p))
	}
	return nil
}

// sweep is what staleFiles finds to do in the output directory.
type sweep struct {
	write     []string // the files of want that are missing or hold other content
	remove    []string // the generated files nothing produces now, under their spelling on disk
	emptied   []string // the directories the removals empty, deepest first
	swept     []swept  // what an interrupted run left, which tidy handled
	leftovers []string // what an interrupted run left, without tidy
}

// staleFiles compares want, the files that must be in out, with the disk.
//
// A generated file (.go, .graphql or .proto: a regular file that starts with
// the header of its kind) in out or below it that is not a path of want is
// removed. The walk skips the directories whose name starts with "." or "_"
// (.git, .velox-verify-*, testdata's kin), and those that hold a go.mod
// (another module) or a velox.go with the header (another run's output, such
// as -out ./gen/b beside -out ./gen). A directory is emptied only when
// it held something, everything it held is removed, and no file of want goes
// into it.
//
// Paths are matched to want by their spelling, and also folded, as a
// case-insensitive file system matches them. A generated file whose name
// differs from a path of want only in case is removed; when the file system
// takes the two for one file, that path of want is written again, after the
// removal, so that it ends up under its new spelling. A directory that differs
// from one of want only in case is removed when the removals empty it, and
// want's spelling is then created; any other such directory (one that holds a
// file of yours, or nothing) is an error, on every file system, because
// velox-gen never renames a directory it did not empty. owners names the
// plugin that wrote each path of want.
//
// With tidy, each directory the walk lists is first rid of what a killed run
// of writeAll left in it (sweepLeftovers), which sw.swept reports. Without
// it, as for -check, those entries stay, and sw.leftovers lists them. Either
// way they are never counted as a directory's entries.
func staleFiles(out string, want map[string][]byte, owners map[string]string, tidy bool) (sweep, error) {
	w := &staleWalk{
		out: out, want: want, tidy: tidy,
		write:    map[string]bool{},
		wantFold: make(map[string]string, len(want)),
		held:     map[string]int{},
		gone:     map[string]int{},
	}
	for p := range want {
		w.wantFold[fold(p)] = p
	}
	keep, keepBy := wantDirs(out, want)
	if err := w.walk(out); err != nil {
		return sweep{swept: w.sw.swept}, err
	}
	// After the walk, which may put back a file set aside.
	for p, data := range want {
		if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, data) {
			w.write[p] = true
		}
	}
	// Deepest first, so that a directory's emptied children count as gone.
	slices.SortFunc(w.dirs, func(a, b string) int {
		return cmp.Or(cmp.Compare(strings.Count(b, string(filepath.Separator)), strings.Count(a, string(filepath.Separator))), cmp.Compare(a, b))
	})
	for _, d := range w.dirs {
		k, kept := keep[fold(d)]
		exact := kept && k == d
		switch {
		case !exact && w.held[d] > 0 && w.gone[d] == w.held[d]:
			w.sw.emptied = append(w.sw.emptied, d)
			w.gone[filepath.Dir(d)]++
		case kept && !exact:
			p := keepBy[fold(d)]
			rel := func(q string) string {
				r, _ := filepath.Rel(out, q)
				return filepath.ToSlash(r)
			}
			return sweep{}, fmt.Errorf("plugin %s: path %q: the directory %q differs only in case; rename it", owners[p], rel(p), rel(d))
		}
	}
	w.sw.write = slices.Sorted(maps.Keys(w.write))
	slices.Sort(w.sw.remove)
	return w.sw, nil
}

// wantDirs returns, by folded path, each directory below out that a file of
// want goes into: its spelling, and the first path of want in it.
func wantDirs(out string, want map[string][]byte) (keep, keepBy map[string]string) {
	keep = map[string]string{}   // folded directory a file of want goes into → its spelling
	keepBy = map[string]string{} // folded directory → the first path of want in it
	for _, p := range slices.Sorted(maps.Keys(want)) {
		for d := filepath.Dir(p); within(out, d) && d != out; d = filepath.Dir(d) {
			if _, ok := keep[fold(d)]; !ok {
				keep[fold(d)], keepBy[fold(d)] = d, p
			}
		}
	}
	return keep, keepBy
}

// staleWalk is the state of staleFiles's walk of out.
type staleWalk struct {
	out      string
	want     map[string][]byte
	tidy     bool
	sw       sweep
	write    map[string]bool
	wantFold map[string]string // folded path → the path of want
	// held counts each directory's entries, gone those that go.
	held, gone map[string]int
	dirs       []string
}

// walk lists the directory d, which it reads through a symlink only when
// it is out itself, as os.ReadDir does.
func (w *staleWalk) walk(d string) error {
	des, err := w.readDir(d)
	if err != nil {
		return err
	}
	for _, de := range des {
		if err := w.entry(d, de); err != nil {
			return err
		}
	}
	return nil
}

// readDir returns the entries of d, a missing out having none. With tidy,
// it first sweeps what an interrupted run left in d.
func (w *staleWalk) readDir(d string) ([]fs.DirEntry, error) {
	des, err := os.ReadDir(d)
	if err != nil {
		if d == w.out && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !w.tidy {
		return des, nil
	}
	done, err := sweepLeftovers(d, des)
	w.sw.swept = append(w.sw.swept, done...)
	if err != nil {
		return nil, err
	}
	if len(done) > 0 {
		return os.ReadDir(d)
	}
	return des, nil
}

// entry counts the entry de of d, walks it when it is a directory, and
// marks it for removal when it is a generated file nothing produces now.
func (w *staleWalk) entry(d string, de fs.DirEntry) error {
	p := filepath.Join(d, de.Name())
	if leftover.MatchString(de.Name()) {
		if !w.tidy {
			w.sw.leftovers = append(w.sw.leftovers, p)
		}
		return nil // -check leaves them; they never hold a directory
	}
	w.held[d]++
	switch {
	case de.IsDir():
		if n := de.Name(); n[0] == '.' || n[0] == '_' || ownRoot(p) {
			return nil
		}
		w.dirs = append(w.dirs, p)
		return w.walk(p)
	case de.Type().IsRegular():
		if _, ok := w.want[p]; ok || headerFor(p) == "" || !IsGenerated(p) {
			return nil
		}
		w.sw.remove = append(w.sw.remove, p)
		w.gone[d]++
		if q, ok := w.wantFold[fold(p)]; ok && sameEntry(p, q) {
			w.write[q] = true
		}
	}
	return nil
}

// ownRoot reports whether the directory d is the root of output the sweep
// leaves alone: another generate run's, which holds velox-gen's own velox.go
// with the header, or another module's, which holds a go.mod.
func ownRoot(d string) bool {
	if fi, err := os.Lstat(filepath.Join(d, "go.mod")); err == nil && !fi.IsDir() {
		return true
	}
	v := filepath.Join(d, "velox.go")
	fi, err := os.Lstat(v)
	return err == nil && fi.Mode().IsRegular() && IsGenerated(v)
}

// sameEntry reports whether the paths a and b, which differ, name one entry of
// the file system: one that folds case.
func sameEntry(a, b string) bool {
	fa, err := os.Lstat(a)
	if err != nil {
		return false
	}
	fb, err := os.Lstat(b)
	return err == nil && os.SameFile(fa, fb)
}
