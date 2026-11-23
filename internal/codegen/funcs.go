package codegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/syssam/velox/internal/codegen/loadjson"
)

// FuncPkg is a package that declares a function a schema names as a
// default or a validator, as Lower reads it to call the function by name.
type FuncPkg struct {
	Name string // the package's name
	// Gen says that the generated package can import it.
	Gen bool
}

// FuncPackages reports, by import path, the packages that declare a
// function the schemas of g name as a default or a validator, as the
// generated code for cfg sees them. go list runs in dir, with env. A package
// go list cannot load, or one of package main, is left out, and so is one
// whose function the loader found no name for: Lower then reads each such
// function from the schema.
//
// A package is importable from the generated package when Go's rule for
// internal packages allows it and it does not import, directly or through
// others, the generated package.
func FuncPackages(dir string, g *Graph, cfg Config, env []string) (map[string]FuncPkg, error) {
	return NewCache().FuncPackages(dir, g, cfg, env)
}

// FuncPackages is the package's FuncPackages, whose go commands c keeps.
func (c *Cache) FuncPackages(dir string, g *Graph, cfg Config, env []string) (map[string]FuncPkg, error) {
	seen := map[string]bool{}
	for _, e := range g.Entities {
		for _, f := range e.Fields {
			for _, r := range fieldRefs(f) {
				if r.Pkg != "" {
					seen[r.Pkg] = true
				}
			}
		}
	}
	if len(seen) == 0 {
		return nil, nil
	}
	pkgs := slices.Sorted(maps.Keys(seen))
	out, stderr, err := c.run(dir, env, append([]string{"list", "-e", "-deps", "-json=ImportPath,Name,Imports,Error"}, pkgs...)...)
	if err != nil {
		return nil, fmt.Errorf("go list %s: %w\n%s", strings.Join(pkgs, " "), err, strings.TrimSpace(stderr))
	}
	type pkg struct {
		ImportPath, Name string
		Imports          []string
		Error            *struct{ Err string }
	}
	listed := map[string]*pkg{}
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var p pkg
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("reading go list output: %w", err)
		}
		listed[p.ImportPath] = &p
	}
	genPath := cfg.GenPath
	forbidden := map[string]bool{genPath: true}
	// reaches reports whether p imports a forbidden package, or is one.
	memo := map[string]bool{}
	var reaches func(p string) bool
	reaches = func(p string) bool {
		if v, ok := memo[p]; ok {
			return v
		}
		memo[p] = false // an import cycle ends here
		v := forbidden[p]
		if q := listed[p]; q != nil {
			for _, i := range q.Imports {
				v = v || reaches(i)
			}
		}
		memo[p] = v
		return v
	}
	res := map[string]FuncPkg{}
	for _, p := range pkgs {
		q := listed[p]
		if q == nil || q.Error != nil || q.Name == "" || q.Name == "main" {
			continue
		}
		res[p] = FuncPkg{Name: q.Name, Gen: genPath != "" && internalOK(genPath, p) && !reaches(p)}
	}
	return res, nil
}

// fieldRefs returns the function references of f's defaults and validators.
func fieldRefs(f *Field) []loadjson.FuncRef {
	var out []loadjson.FuncRef
	if f.DefaultRef != nil {
		out = append(out, *f.DefaultRef)
	}
	if f.UpdateDefaultRef != nil {
		out = append(out, *f.UpdateDefaultRef)
	}
	return append(out, f.ValidatorRefs...)
}

// internalOK reports whether the package at importer may import the package
// at p by Go's rule for internal packages: p's path holds no element
// internal, or importer is in the tree rooted at the element's parent.
func internalOK(importer, p string) bool {
	var parent string
	switch {
	case p == "internal" || strings.HasPrefix(p, "internal/"):
		return false // the standard library's own
	case strings.HasSuffix(p, "/internal"):
		parent = strings.TrimSuffix(p, "/internal")
	case strings.Contains(p, "/internal/"):
		parent = p[:strings.LastIndex(p, "/internal/")]
	default:
		return true
	}
	return importer == parent || strings.HasPrefix(importer, parent+"/")
}
