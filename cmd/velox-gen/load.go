package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"

	"golang.org/x/tools/go/packages"
)

const loadMode = packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
	packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule

// load loads the packages the patterns name. Type errors are tolerated: a
// generated file that is out of date may not compile, and regenerating it is
// the point. A pattern that matches no files, and a file that does not parse,
// are errors.
func load(cfg config, patterns []string) ([]*packages.Package, error) {
	ps, err := packages.Load(&packages.Config{Mode: loadMode, Dir: cfg.dir, Env: cfg.env}, patterns...)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, fmt.Errorf("no packages match %v", patterns)
	}
	for _, p := range ps {
		for _, e := range p.Errors {
			if e.Kind == packages.ParseError || (e.Kind == packages.ListError && len(p.GoFiles) == 0) {
				return nil, fmt.Errorf("%s: %s", p.PkgPath, e.Msg)
			}
		}
	}
	return ps, nil
}

// isGenerated reports whether the file at path starts with velox-gen's header.
func isGenerated(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	line, _ := bufio.NewReader(f).ReadString('\n')
	return line == header+"\n"
}

// isOldGenerated reports whether the file at path is one the old mode wrote:
// it starts with velox-gen's header and does not refer to
// GeneratedCodeIsVersion3, which the new mode's files do. The old mode removes
// only these, so that it leaves the new mode's files in the same module alone.
// Plan 4 removes this with the old mode.
func isOldGenerated(path string) bool {
	if !isGenerated(path) {
		return false
	}
	src, err := os.ReadFile(path)
	return err == nil && !bytes.Contains(src, []byte("GeneratedCodeIsVersion3"))
}
