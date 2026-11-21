package genrun

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/syssam/velox/internal/codegen"
)

// RelPath shows a path relative to dir when it is inside dir.
func RelPath(dir, path string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return path
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	if r, err := filepath.Rel(abs, path); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return path
}

// RelErrors shows the positions of err's *codegen.Error and *codegen.FileError
// values, as errors.Join joined them, relative to dir when their files are
// inside it. That covers
// the positions inside a Reason too.
func RelErrors(dir string, err error) error {
	errs := []error{err}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		errs = j.Unwrap()
	}
	inside := relInside(dir)
	out := make([]error, len(errs))
	changed := false
	for i, e := range errs {
		out[i] = e
		if fe, ok := e.(*codegen.FileError); ok {
			c := *fe
			c.Path = RelPath(dir, fe.Path)
			out[i], changed = &c, c != *fe
			continue
		}
		if ge, ok := e.(*codegen.Error); ok {
			c := *ge
			if ge.Pos != "" {
				c.Pos = RelPos(dir, ge.Pos)
			}
			if inside != nil {
				c.Reason = inside.ReplaceAllString(ge.Reason, "${1}")
			}
			if c != *ge {
				out[i], changed = &c, true
			}
		}
	}
	if !changed {
		return err
	}
	return errors.Join(out...)
}

// relInside matches the working directory, and its symlink-free form when
// that differs, as the start of a path: the directory and its separator,
// after anything but a path character. Its first group is what comes before.
// It is nil when dir has no absolute form, and for the filesystem root, which
// every path is inside: cutting it would cut every slash.
func relInside(dir string) *regexp.Regexp {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil
	}
	cands := []string{abs}
	if r, err := filepath.EvalSymlinks(abs); err == nil && r != abs {
		cands = append(cands, r)
	}
	var prefixes []string
	for _, p := range cands {
		if filepath.Dir(p) != p {
			prefixes = append(prefixes, regexp.QuoteMeta(strings.TrimRight(p, string(filepath.Separator))+string(filepath.Separator)))
		}
	}
	if len(prefixes) == 0 {
		return nil
	}
	return regexp.MustCompile(`(^|[^\w./\\-])(?:` + strings.Join(prefixes, "|") + `)`)
}

// RelPos shows a file:line position, or a file alone, relative to dir when
// the file is inside it.
func RelPos(dir, pos string) string {
	file, line := pos, ""
	if i := strings.LastIndexByte(pos, ':'); i >= 0 && pos[i+1:] != "" && strings.Trim(pos[i+1:], "0123456789") == "" {
		file, line = pos[:i], pos[i:]
	}
	file = filepath.FromSlash(file)
	if !filepath.IsAbs(file) {
		return pos
	}
	if r, err := filepath.EvalSymlinks(file); err == nil {
		file = r
	}
	return RelPath(dir, file) + line
}

// RelWarning is the warning w, "file:line: text", with the file relative
// to dir, as RelPos makes it.
func RelWarning(dir, w string) string {
	if i := strings.Index(w, ": "); i > 0 {
		return RelPos(dir, w[:i]) + w[i:]
	}
	return w
}
