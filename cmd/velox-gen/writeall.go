package main

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
)

// writeTemp, renameFile, linkFile and mkdirAll are how writeAll writes its
// files and makes its directories; tests replace them.
var (
	writeTemp  = writeTempFile
	renameFile = os.Rename
	linkFile   = os.Link
	mkdirAll   = os.MkdirAll
)

// writeTempFile writes data to a new file in dir named by pattern, as
// os.CreateTemp names it, with mode 0o644, and returns its path.
func writeTempFile(dir, pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// leftover matches the names writeAll gives the entries it writes or sets
// aside, ".<name>.<kind>-velox-<n>": a file written under a temporary name
// (tmp), a file it replaces (old) and an entry it removes (gone). They are
// left only when the process is killed while it writes.
var leftover = regexp.MustCompile(`^\.(.+)\.(tmp|old|gone)-velox-[0-9]+$`)

// writeAll writes files and removes remove, all or nothing. Both are slash
// paths relative to dir, in any directory below it. It first sets aside each
// file it removes, by renaming it within its directory, so that a file whose
// new name differs only in case can take its place. A path of remove may be
// a directory, which is set aside whole, with what it holds, so that a
// directory whose new name differs only in case can take its place too. Then
// it writes each file under a temporary name in its own directory, making the
// directories it lacks, and renames it into place, velox.sum last; a file it
// writes has mode 0644, also one it replaces. A file it replaces is kept
// aside first, by a hard link (else a rename). Once every file is in place it
// deletes what it set aside: the paths of remove, then each directory of
// them, which it deletes only when it is empty. On a failure it removes what
// it wrote and the directories it made, and renames back what it set aside.
// The error names what it could not undo.
func writeAll(dir string, files map[string][]byte, remove []string) error {
	paths := slices.Sorted(maps.Keys(files))
	if i := slices.Index(paths, "velox.sum"); i >= 0 {
		paths = append(append(paths[:i:i], paths[i+1:]...), "velox.sum")
	}
	type swapped struct{ target, aside string } // aside "": the target was new
	type setAside struct{ rel, target, aside string }
	var (
		gone    []setAside        // what remove names, set aside
		unmake  []func() []string // each removes the directories one makeDirs made
		temps   = map[string]string{}
		done    []swapped
		stray   []string // what a failed step left beside its target
		removed = map[string]bool{}
	)
	undo := func(err error) error {
		left := slices.Clone(stray)
		for _, t := range temps {
			if rerr := os.Remove(t); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
				left = append(left, t)
			}
		}
		for i := len(done) - 1; i >= 0; i-- {
			s := done[i]
			if s.aside == "" {
				if rerr := os.Remove(s.target); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
					left = append(left, s.target)
				}
			} else if rerr := renameFile(s.aside, s.target); rerr != nil {
				left = append(left, s.target+" (what it held is in "+s.aside+")")
			}
		}
		for i := len(unmake) - 1; i >= 0; i-- {
			left = append(left, unmake[i]()...)
		}
		for i := len(gone) - 1; i >= 0; i-- {
			s := gone[i]
			if rerr := renameFile(s.aside, s.target); rerr != nil {
				left = append(left, s.target+" (what it held is in "+s.aside+")")
			}
		}
		if len(left) > 0 {
			slices.Sort(left)
			return fmt.Errorf("%w; undoing the write failed, which left %s", err, strings.Join(left, ", "))
		}
		return err
	}
	// aside is a new name beside target, which nothing holds.
	aside := func(target, kind string) (string, error) {
		a, err := writeTemp(filepath.Dir(target), "."+filepath.Base(target)+"."+kind+"-velox-*", nil) // a name of its own
		if err != nil {
			return "", err
		}
		if err := os.Remove(a); err != nil {
			stray = append(stray, a)
			return "", err
		}
		return a, nil
	}
	for _, r := range remove {
		removed[r] = true
	}
	for _, r := range slices.Sorted(slices.Values(remove)) {
		under := false
		for d := path.Dir(r); d != "." && d != "/" && !under; d = path.Dir(d) {
			under = removed[d] // it goes with its directory
		}
		if under {
			continue
		}
		s := setAside{rel: r, target: filepath.Join(dir, filepath.FromSlash(r))}
		a, err := aside(s.target, "gone")
		if err != nil {
			return undo(err)
		}
		if err := renameFile(s.target, a); err != nil {
			return undo(err)
		}
		s.aside = a
		gone = append(gone, s)
	}
	for _, p := range paths {
		target := filepath.Join(dir, filepath.FromSlash(p))
		rm, err := makeDirs(filepath.Dir(target))
		if err != nil {
			return undo(err)
		}
		unmake = append(unmake, rm)
		t, err := writeTemp(filepath.Dir(target), "."+filepath.Base(target)+".tmp-velox-*", files[p])
		if err != nil {
			return undo(err)
		}
		temps[p] = t
	}
	for _, p := range paths {
		s := swapped{target: filepath.Join(dir, filepath.FromSlash(p))}
		linked := false
		if fi, err := os.Lstat(s.target); err == nil {
			a, err := aside(s.target, "old")
			if err != nil {
				return undo(err)
			}
			// A hard link keeps a regular file at its path until the rename
			// replaces it; a symbolic link, or where links fail, is renamed
			// aside.
			linked = fi.Mode().IsRegular() && linkFile(s.target, a) == nil
			if !linked {
				if err := renameFile(s.target, a); err != nil {
					return undo(err)
				}
			}
			s.aside = a
		} else if !errors.Is(err, fs.ErrNotExist) {
			return undo(err)
		}
		if err := renameFile(temps[p], s.target); err != nil {
			if !linked {
				done = append(done, s)
			} else if rerr := os.Remove(s.aside); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
				stray = append(stray, s.aside) // the original is at its path still
			}
			return undo(err)
		}
		done = append(done, s)
		delete(temps, p)
	}
	var left []string
	for _, s := range done {
		if s.aside != "" {
			if err := os.Remove(s.aside); err != nil && !errors.Is(err, fs.ErrNotExist) {
				left = append(left, s.aside)
			}
		}
	}
	// where is the path at which the path r of remove is now.
	where := func(r string) string {
		for _, s := range gone {
			if r == s.rel {
				return s.aside
			}
			if rest, ok := strings.CutPrefix(r, s.rel+"/"); ok {
				return filepath.Join(s.aside, filepath.FromSlash(rest))
			}
		}
		return filepath.Join(dir, filepath.FromSlash(r))
	}
	var dirs []string
	for _, r := range slices.Sorted(slices.Values(remove)) {
		p := where(r)
		fi, err := os.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			left = append(left, p)
		case fi.IsDir():
			dirs = append(dirs, p)
		default:
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				left = append(left, p)
			}
		}
	}
	slices.SortFunc(dirs, func(a, b string) int { // deepest first
		return cmp.Or(cmp.Compare(strings.Count(b, string(filepath.Separator)), strings.Count(a, string(filepath.Separator))), cmp.Compare(a, b))
	})
	for _, d := range dirs {
		if err := os.Remove(d); err != nil && !errors.Is(err, fs.ErrNotExist) {
			left = append(left, d)
		}
	}
	if len(left) > 0 {
		return fmt.Errorf("the files are written, but removing the old ones failed, which left %s", strings.Join(left, ", "))
	}
	return nil
}

// holdSignals holds SIGINT and SIGTERM until release. A write that holds
// them finishes its undo or its commit, and its command then exits 130. A
// signal the process ignores, as one started with nohup or in the background
// of a shell ignores SIGINT, stays ignored: holding it would turn it on.
func holdSignals() *held {
	h := &held{ch: make(chan os.Signal, 1)}
	var sigs []os.Signal
	for _, s := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		if !signal.Ignored(s) {
			sigs = append(sigs, s)
		}
	}
	if len(sigs) > 0 {
		signal.Notify(h.ch, sigs...)
	}
	return h
}

// held is the signals holdSignals holds.
type held struct{ ch chan os.Signal }

// release stops holding the signals and reports whether one arrived.
func (h *held) release() bool {
	signal.Stop(h.ch)
	select {
	case <-h.ch:
		return true
	default:
		return false
	}
}

// writeAllHeld is writeAll with SIGINT and SIGTERM held, so that its undo or
// its commit finishes; it reports whether one arrived meanwhile.
func writeAllHeld(dir string, files map[string][]byte, remove []string) (bool, error) {
	h := holdSignals()
	err := writeAll(dir, files, remove)
	return h.release(), err
}

// interruptedCode is the exit code of a command interrupted while it wrote.
const interruptedCode = 130

// swept is one entry an interrupted writeAll left, which sweepLeftovers
// handled: removed, or renamed back to restored. kept are the files of yours
// in a directory set aside, which it left there, and with them the
// directory.
type swept struct {
	path, restored string
	kept           []string
}

// sweepLeftovers handles the entries an interrupted writeAll left in d, of
// those in des. A file written under a temporary name is removed. A file or
// directory set aside is renamed back when its name is missing, and removed
// otherwise: a directory entry by entry (removeGone), never whole, so that a
// file of yours someone put in it stays. It reports what it handled.
func sweepLeftovers(d string, des []fs.DirEntry) ([]swept, error) {
	var out []swept
	for _, de := range des {
		m := leftover.FindStringSubmatch(de.Name())
		if m == nil {
			continue
		}
		p, orig := filepath.Join(d, de.Name()), filepath.Join(d, m[1])
		if m[2] != "tmp" {
			if _, err := os.Lstat(orig); errors.Is(err, fs.ErrNotExist) {
				if err := os.Rename(p, orig); err != nil {
					return out, err
				}
				out = append(out, swept{path: p, restored: orig})
				continue
			} else if err != nil {
				return out, err
			}
		}
		if de.IsDir() {
			kept, err := removeGone(p)
			if err != nil {
				return out, err
			}
			out = append(out, swept{path: p, kept: kept})
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return out, err
		}
		out = append(out, swept{path: p})
	}
	return out, nil
}

// removeGone removes the directory d, which writeAll set aside, as writeAll
// removes what it set aside: each generated file in it (isGenerated), then
// each directory, deepest first, when it is empty. It keeps, and returns, the
// other entries, which are not velox-gen's, with the directories that hold
// them.
func removeGone(d string) (kept []string, err error) {
	var files, dirs []string
	err = filepath.WalkDir(d, func(p string, de fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case de.IsDir():
			dirs = append(dirs, p)
		case de.Type().IsRegular() && isGenerated(p):
			files = append(files, p)
		default:
			kept = append(kept, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, p := range files {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	slices.SortFunc(dirs, func(a, b string) int { // deepest first
		return cmp.Or(cmp.Compare(strings.Count(b, string(filepath.Separator)), strings.Count(a, string(filepath.Separator))), cmp.Compare(a, b))
	})
	for _, p := range dirs {
		holds := slices.ContainsFunc(kept, func(k string) bool { return strings.HasPrefix(k, p+string(filepath.Separator)) })
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) && !holds {
			return nil, err
		}
	}
	return kept, nil
}

// makeDirs makes dir and the parents it lacks, and returns what removes
// those it made, when they are empty, and names those it could not remove.
func makeDirs(dir string) (remove func() []string, err error) {
	var made []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil || !errors.Is(err, fs.ErrNotExist) {
			break
		}
		made = append(made, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	remove = func() []string {
		var left []string
		for _, d := range made { // deepest first
			if err := os.Remove(d); err != nil && !errors.Is(err, fs.ErrNotExist) {
				left = append(left, d)
			}
		}
		return left
	}
	if err := mkdirAll(dir, 0o755); err != nil {
		if left := remove(); len(left) > 0 {
			slices.Sort(left)
			return nil, fmt.Errorf("%w; removing the directories it made failed, which left %s", err, strings.Join(left, ", "))
		}
		return nil, err
	}
	return remove, nil
}
