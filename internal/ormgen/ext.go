package ormgen

import (
	"bytes"
	"strings"

	vgen "github.com/syssam/velox/compiler/gen"
)

// This file is what a layer on top of the generated tree is written with:
// the Extension it implements, and what it may ask the generator. The one
// layer there is, the GraphQL layer, is contrib/graphql/ormgql; the package
// is internal so that none of this is a promise to anyone else.

// Extension adds a layer to a generation: declarations of its own in the
// packages of the tree, in files of its own, and whatever it writes beside
// the tree.
//
// What it declares must not move what the generator declares: a tree
// generated with an extension is the tree generated without it, plus the
// extension's files. So an extension writes no part of a file of the
// generator's, and the names it needs are taken before the generator names
// anything that could give way to them.
type Extension interface {
	// Begin is called once the entities, their packages and their edges are
	// known, and before any file is generated. The extension reads what it
	// needs and takes its names: Reserve for package entity, ReserveRoot
	// for the root package.
	Begin(g *Generator, graph *vgen.Graph) error
	// LeafNames takes the names the extension declares in the package of e.
	// It is called every time that package's names are handed out, from
	// several goroutines: it must give the same answer each time and write
	// to nothing but n.
	LeafNames(e *Entity, n *Namer)
	// Files returns the extension's files, by path under the target
	// directory. A path the generator writes itself is an error.
	Files() (map[string][]byte, error)
	// End is called after the tree is written.
	End() error
}

// The import paths generated code names.
const (
	ORMPath   = ormPath
	VeloxPath = veloxPath
	// Header is the first line of every generated file; a generation
	// removes the files that carry it and that it did not write.
	Header = header
	// FileMarker starts a line that names the file the declarations after
	// it belong to; see Split.
	FileMarker = fileMarker
)

// Package is the import path of the generated tree.
func (g *Generator) Package() string { return g.cfg.Package }

// Target is the directory the tree is written to.
func (g *Generator) Target() string { return g.cfg.Target }

// Entities returns the entities, in the order of the schema.
func (g *Generator) Entities() []*Entity { return g.ents }

// Entity returns the entity of the name, or nil.
func (g *Generator) Entity(name string) *Entity { return g.by[name] }

// Reserve takes an identifier of package entity, and reports whether it was
// free.
func (g *Generator) Reserve(name string) bool {
	if g.taken[name] {
		return false
	}
	g.taken[name] = true
	return true
}

// ReserveRoot takes identifiers of the root package: what the package gives
// of the library under the same name is left out.
func (g *Generator) ReserveRoot(names ...string) {
	for _, n := range names {
		g.rootTaken[n] = true
	}
}

// LeafNames returns the names of the package of e as they stand once the
// package's fields and the extensions have theirs, and the type of each enum
// field there.
func (g *Generator) LeafNames(e *Entity) (*Namer, map[*Field]string) {
	n, types, _, _ := g.enumNames(e)
	return n, types
}

// Names returns the names of e's types in package entity.
func (g *Generator) Names(e *Entity) Names { return g.names(e) }

// Predicate is the entity's predicate type, as written outside the predicate
// package.
func (e *Entity) Predicate() string { return e.pred() }

// OrderTerm is the type of the entity's order terms, as written outside the
// predicate package.
func (e *Entity) OrderTerm() string { return e.order() }

// Use records an import and returns the name to qualify identifiers with.
func (im *Imports) Use(path, name string) string { return im.use(path, name) }

// Block returns the import declaration.
func (im *Imports) Block() string { return im.block() }

// Unique takes name, with the suffix if another has it, and returns what was
// taken.
func (n *Namer) Unique(name, suffix string) string { return n.unique(name, suffix) }

// Take marks a name as given out.
func (n *Namer) Take(name string) { n.taken[name] = true }

// GoTypeOf returns the Go type of a field, without the pointer of a nillable
// field.
func GoTypeOf(f *vgen.Field) (TypeRef, error) { return goTypeOf(f) }

// FileName is the file of e in a package that has a file per entity, without
// the extension.
func FileName(e *Entity) string { return fileName(e) }

// Split cuts the source of a package, written as one file with FileMarker
// lines, into its formatted files; the declarations before the first marker
// go to the file called common.
func Split(src []byte, common string) (map[string][]byte, error) { return split(src, common) }

// Formatted returns a source file as gofmt writes it; what names the file in
// the error of a source that does not parse.
func Formatted(src []byte, what string) ([]byte, error) {
	return formatted(bytes.NewBuffer(src), what)
}

// NoInline marks the functions of a source of package entity //go:noinline:
// for the methods an extension adds to the entities, which every package
// of an application can call.
func NoInline(src []byte) []byte { return noInline(src) }

// RootPackage is the name of the package at the root of the generated tree.
func RootPackage(importPath string) string { return rootPackage(importPath) }

// locals are the names the generated functions of package entity give their
// variables and the packages they import: a package imported under one of
// them could not be named where the variable is in scope.
var locals = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range strings.Fields("e q b v i d s c r l p o ps os ls ss bs vs es id ids ctx ex err out rows subs dst conn where after first before last orderBy context schemafield orm slices predicate entity iter slots filter") {
		m[s] = true
	}
	return m
}()

// LocalName is the name package entity imports the package of an entity
// under: the package's own, unless a variable of the generated code has it.
func LocalName(pkg string) string {
	if locals[pkg] {
		return pkg + "pkg"
	}
	return pkg
}
