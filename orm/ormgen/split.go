package ormgen

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/scanner"
	"go/token"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// fileMarker starts a line of generated source that names the file the
// declarations after it belong to; see split.
const fileMarker = "//velox:file "

// fileName is the file of entity e in a package that has one file per entity.
// It is the name of the entity's own package, which no two entities share
// and which is not the name of the package's common file. A name with an
// underscore takes a suffix: user_linux.go and note_test.go would be left
// out of the build by their names.
func fileName(e *ent) string {
	if strings.Contains(e.Pkg, "_") {
		return e.Pkg + "_gen"
	}
	return e.Pkg
}

// each runs fn(0) ... fn(n-1) on every CPU and returns the error of the
// lowest index that has one. Formatting is two thirds of a generation and
// every file is formatted on its own: in turn, 450 entities took 4.0 s; at
// once, see benchmarks/fixtures/realistic/README.md.
func each(n int, fn func(i int) error) error {
	errs := make([]error, n)
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			errs[i] = fn(i)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// split cuts the source of a package, generated as one file and not yet
// formatted, into one formatted file per entity: the declarations after a
// marker line go to the file it names, the ones before any marker and after
// an empty marker to the common file, which keeps the package's
// documentation and its blank imports. Each file imports what its
// declarations use.
//
// One file was 10 MB and 285,000 lines on a 454-entity schema. The compiler
// does not mind (the package compiles in the same time either way), an
// editor does: asked for the outline of that file the language server
// answered in 3-5 s with 43,000 symbols and 1.3 GB, against 1.4-2 s and 0.3
// GB for the file of one entity, and it gave no semantic highlighting at all
// for a file that size (benchmarks/fixtures/realistic/README.md).
//
// The names an import is used under must not be shadowed by a variable in
// the generated code: a use is an identifier followed by a period.
func split(src []byte, common string) (map[string][]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		return nil, err
	}
	if len(f.Decls) != 1 {
		return nil, fmt.Errorf("velox: split: want one import declaration, found %d", len(f.Decls))
	}
	off := func(p token.Pos) int { return fset.Position(p).Offset }
	// spec is the import as the generator wrote it, with or without a name.
	type imp struct{ name, path, spec string }
	var imps []imp
	names := map[string]int{}
	for _, s := range f.Imports {
		path, perr := strconv.Unquote(s.Path.Value)
		if perr != nil {
			return nil, perr
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if s.Name != nil {
			name = s.Name.Name
		}
		if name != "_" {
			names[name] = len(imps)
		}
		spec := s.Path.Value
		if s.Name != nil {
			spec = s.Name.Name + " " + spec
		}
		imps = append(imps, imp{name, path, spec})
	}
	preamble := src[:off(f.Decls[0].Pos())]
	body := src[off(f.Decls[0].End()):]
	clause := "package " + f.Name.Name + "\n\n"

	// The text of each file, in the order the generator wrote it.
	text := map[string]*bytes.Buffer{}
	cur := common
	for _, line := range bytes.SplitAfter(body, []byte("\n")) {
		if name, ok := bytes.CutPrefix(line, []byte(fileMarker)); ok {
			if cur = string(bytes.TrimSpace(name)); cur == "" {
				cur = common
			}
			continue
		}
		b := text[cur]
		if b == nil {
			b = &bytes.Buffer{}
			text[cur] = b
		}
		b.Write(line)
	}
	if text[common] == nil {
		text[common] = &bytes.Buffer{}
	}

	names2 := make([]string, 0, len(text))
	for name := range text {
		names2 = append(names2, name)
	}
	sort.Strings(names2)
	sources := make([][]byte, len(names2))
	err = each(len(names2), func(i int) error {
		name, b := names2[i], text[names2[i]]
		used := make([]bool, len(imps))
		var s scanner.Scanner
		file := token.NewFileSet().AddFile("", -1, b.Len())
		s.Init(file, b.Bytes(), nil, 0)
		prev, pending := token.ILLEGAL, -1
		for {
			_, tok, lit := s.Scan()
			if tok == token.EOF {
				break
			}
			if tok == token.PERIOD && pending >= 0 {
				used[pending] = true
			}
			pending = -1
			if at, ok := names[lit]; ok && tok == token.IDENT && prev != token.PERIOD {
				pending = at
			}
			prev = tok
		}
		var need []imp
		for k, im := range imps {
			if used[k] || (im.name == "_" && name == common) {
				need = append(need, im)
			}
		}
		var w bytes.Buffer
		if name == common {
			w.Write(preamble)
		} else {
			w.WriteString(header)
			w.WriteString(clause)
		}
		if len(need) > 0 {
			specs := make(map[string]string, len(need))
			for _, im := range need {
				specs[im.path] = im.spec
			}
			w.WriteString(importDecl(specs))
			w.WriteString("\n")
		}
		w.Write(bytes.TrimLeft(b.Bytes(), "\n"))
		src, ferr := formatted(&w, f.Name.Name+"/"+name+".go")
		sources[i] = src
		return ferr
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(names2))
	for i, name := range names2 {
		out[name+".go"] = sources[i]
	}
	return out, nil
}
