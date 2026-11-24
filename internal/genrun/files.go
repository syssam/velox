package genrun

import (
	"fmt"
	"os"
	"path/filepath"
)

// FilesDir is the directory where the plugins' files that are not Go go,
// for -files files, or "" when they go in out, as without it. files is
// resolved as -out is (OutDir), relative to dir, then through its symbolic
// links. It must name a directory strictly inside the module that holds out,
// not the module's root, not above out and not inside out's tree, since
// out's own sweep owns that tree, and below no directory that holds a go.mod
// or velox-gen's velox.go, the root of another module or another run's
// output. files equal to out is as without it. The path returned has its
// symbolic links resolved.
func FilesDir(dir, out, files string) (string, error) {
	if files == "" {
		return "", nil
	}
	bad := func(format string, args ...any) error {
		return fmt.Errorf("-files %s: %s", files, fmt.Sprintf(format, args...))
	}
	fd, err := resolveDir(dir, OutDir(dir, files))
	if err != nil {
		return "", bad("%v", err)
	}
	od, err := resolveDir(dir, out)
	if err != nil {
		return "", err
	}
	if fd == od {
		return "", nil
	}
	mod := moduleRoot(od)
	switch {
	case mod == "":
		return "", bad("-out %s is not in a module", RelPath(dir, out))
	case fd == mod:
		return "", bad("it is the module's root; name a directory inside it")
	case !within(mod, fd):
		return "", bad("it is outside the module %s; name a directory inside it", mod)
	case within(fd, od):
		return "", bad("it holds -out %s; name a directory beside it", RelPath(dir, out))
	case within(od, fd):
		return "", bad("it is inside -out %s, whose own files velox-gen sweeps; name a directory beside it", RelPath(dir, out))
	}
	for d := fd; d != mod; d = filepath.Dir(d) {
		if ownRoot(d) {
			return "", bad("it is in another module or velox-gen output: %s holds a go.mod or velox-gen's velox.go", RelPath(dir, d))
		}
	}
	return fd, nil
}

// resolveDir is the absolute path p with the symbolic links of its deepest
// existing directory resolved, and the rest, which does not exist yet,
// joined. What exists of p must be directories.
func resolveDir(dir, p string) (string, error) {
	base, rest := p, ""
	for {
		fi, err := os.Stat(base)
		if err == nil {
			if !fi.IsDir() {
				return "", fmt.Errorf("%s is not a directory", RelPath(dir, base))
			}
			break
		}
		// A missing entry, or one below a file, as afile/schema: the
		// entry above it decides.
		parent := filepath.Dir(base)
		if parent == base {
			return p, nil
		}
		rest, base = filepath.Join(filepath.Base(base), rest), parent
	}
	r, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", err
	}
	return filepath.Join(r, rest), nil
}

// moduleRoot is the nearest directory at or above d that holds a go.mod, or
// "" when none does.
func moduleRoot(d string) string {
	for {
		if fi, err := os.Stat(filepath.Join(d, "go.mod")); err == nil && !fi.IsDir() {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// splitWant splits want into the files below out and those below files,
// which is "" when every file is below out.
func splitWant(files string, want map[string][]byte) (inOut, inFiles map[string][]byte) {
	if files == "" {
		return want, nil
	}
	inOut, inFiles = map[string][]byte{}, map[string][]byte{}
	for p, data := range want {
		if within(files, p) {
			inFiles[p] = data
		} else {
			inOut[p] = data
		}
	}
	return inOut, inFiles
}

// commonDir is the deepest directory that holds both a and b, which are
// absolute.
func commonDir(a, b string) string {
	for !within(a, b) {
		parent := filepath.Dir(a)
		if parent == a {
			return a
		}
		a = parent
	}
	return a
}

// plan is staleFiles's sweep of out and, with -files, of files, merged.
func plan(out, files string, want map[string][]byte, owners map[string]string, tidy bool) (sweep, error) {
	inOut, inFiles := splitWant(files, want)
	sw, err := staleFiles(out, inOut, owners, tidy, false)
	if err != nil || files == "" {
		return sw, err
	}
	fsw, err := staleFiles(files, inFiles, owners, tidy, true)
	sw.swept = append(sw.swept, fsw.swept...)
	if err != nil {
		return sw, err
	}
	sw.write = append(sw.write, fsw.write...)
	sw.remove = append(sw.remove, fsw.remove...)
	sw.emptied = append(sw.emptied, fsw.emptied...)
	sw.leftovers = append(sw.leftovers, fsw.leftovers...)
	return sw, nil
}

// checkWant is checkWantPaths for out and, with -files, for files.
func checkWant(dir, out, files string, want map[string][]byte) error {
	inOut, inFiles := splitWant(files, want)
	if err := checkWantPaths(dir, out, inOut); err != nil {
		return err
	}
	if files == "" {
		return nil
	}
	return checkWantPaths(dir, files, inFiles)
}
