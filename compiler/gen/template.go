// Package gen provides the code generation pipeline for Velox.
//
// This file provides legacy template support for external extensions.
// Core code generation uses Jennifer (see generate.go + generate_helper.go).
// The embedded template/ directory exists solely for backward compatibility
// with Ent-style extension templates registered via Config.Templates.
// The templates are loaded lazily by generateExternalTemplates() and are never
// executed by the built-in SQL dialect.
//
// If you are adding new generation logic, use Jennifer — do not add new .tmpl files.
package gen

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"text/template/parse"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// GraphTemplate specifies a template that is executed with the Graph object.
// Used for external template extensions registered via Config.Templates.
type GraphTemplate struct {
	Name           string            // template name.
	Skip           func(*Graph) bool // skip condition (storage constraints or gated by a feature-flag).
	Format         string            // file name format.
	ExtendPatterns []string          // extend patterns.
}

var (
	// templates holds the Go templates used as a base for external template extensions.
	templates     *Template
	templatesOnce sync.Once
	//go:embed template/*
	templateDir embed.FS
	// importPkg are the import packages used for code generation.
	// Extended by the function below on generation initialization.
	importPkg = map[string]string{
		"context": "context",
		"driver":  "database/sql/driver",
		"errors":  "errors",
		"fmt":     "fmt",
		"math":    "math",
		"strings": "strings",
		"time":    "time",
		"ent":     "github.com/syssam/velox",
		"dialect": "github.com/syssam/velox/dialect",
		"field":   "github.com/syssam/velox/schema/field",
	}
)

func initTemplates() {
	templatesOnce.Do(doInitTemplates)
}

func doInitTemplates() {
	// Collect every .tmpl file under the embedded template/ tree.
	// Walk-based rather than glob-based because the stdlib
	// template.ParseFS requires every supplied glob to match at least
	// one file; as orphan templates are pruned, fixed-depth globs
	// (template/*/*.tmpl etc.) start matching zero files and ParseFS
	// panics. Walking is robust to any surviving layout.
	var tmplFiles []string
	err := fs.WalkDir(templateDir, "template", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".tmpl") {
			tmplFiles = append(tmplFiles, path)
		}
		return nil
	})
	if err != nil {
		panic(fmt.Sprintf("velox/gen: walk templates: %v", err))
	}
	templates = MustParse(NewTemplate("templates").ParseFS(templateDir, tmplFiles...))
	b := bytes.NewBuffer([]byte("package main\n"))
	if execErr := templates.ExecuteTemplate(b, "import", Type{Config: &Config{}}); execErr != nil {
		panic(fmt.Sprintf("velox/gen: load imports: %v", execErr))
	}
	f, err := parser.ParseFile(token.NewFileSet(), "", b, parser.ImportsOnly)
	if err != nil {
		panic(fmt.Sprintf("velox/gen: parse imports: %v", err))
	}
	for _, spec := range f.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			panic(fmt.Sprintf("velox/gen: unquote import path: %v", err))
		}
		importPkg[filepath.Base(path)] = path
	}
	for _, s := range drivers {
		for _, path := range s.Imports {
			importPkg[filepath.Base(path)] = path
		}
	}
}

// Template wraps the standard template.Template to
// provide additional functionality for ent extensions.
type Template struct {
	*template.Template
	FuncMap   template.FuncMap
	condition func(*Graph) bool
}

// NewTemplate creates an empty template with the standard codegen functions.
func NewTemplate(name string) *Template {
	t := &Template{Template: template.New(name)}
	return t.Funcs(Funcs)
}

// Funcs merges the given funcMap with the template functions.
func (t *Template) Funcs(funcMap template.FuncMap) *Template {
	t.Template.Funcs(funcMap)
	if t.FuncMap == nil {
		t.FuncMap = template.FuncMap{}
	}
	for name, f := range funcMap {
		if _, ok := t.FuncMap[name]; !ok {
			t.FuncMap[name] = f
		}
	}
	return t
}

// SkipIf allows registering a function to determine if the template needs to be skipped or not.
func (t *Template) SkipIf(cond func(*Graph) bool) *Template {
	t.condition = cond
	return t
}

// Parse parses text as a template body for t.
func (t *Template) Parse(text string) (*Template, error) {
	if _, err := t.Template.Parse(text); err != nil {
		return nil, err
	}
	return t, nil
}

// ParseFiles parses a list of files as templates and associate them with t.
// Each file can be a standalone template.
func (t *Template) ParseFiles(filenames ...string) (*Template, error) {
	if _, err := t.Template.ParseFiles(filenames...); err != nil {
		return nil, err
	}
	return t, nil
}

// ParseGlob parses the files that match the given pattern as templates and
// associate them with t.
func (t *Template) ParseGlob(pattern string) (*Template, error) {
	if _, err := t.Template.ParseGlob(pattern); err != nil {
		return nil, err
	}
	return t, nil
}

// ParseDir walks on the given dir path and parses the given matches with aren't Go files.
func (t *Template) ParseDir(path string) (*Template, error) {
	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("walk path %s: %w", path, err)
		}
		if info.IsDir() || strings.HasSuffix(path, ".go") {
			return nil
		}
		_, err = t.ParseFiles(path)
		return err
	})
	return t, err
}

// ParseFS is like ParseFiles or ParseGlob but reads from the file system fsys
// instead of the host operating system's file system.
func (t *Template) ParseFS(fsys fs.FS, patterns ...string) (*Template, error) {
	if _, err := t.Template.ParseFS(fsys, patterns...); err != nil {
		return nil, err
	}
	return t, nil
}

// AddParseTree adds the given parse tree to the template.
func (t *Template) AddParseTree(name string, tree *parse.Tree) (*Template, error) {
	if _, err := t.Template.AddParseTree(name, tree); err != nil {
		return nil, err
	}
	return t, nil
}

// MustParse is a helper that wraps a call to a function returning (*Template, error)
// and panics if the error is non-nil.
func MustParse(t *Template, err error) *Template {
	if err != nil {
		panic(err)
	}
	return t
}

type (
	// Dependencies wraps a list of dependencies as codegen
	// annotation.
	Dependencies []*Dependency

	// Dependency allows configuring optional dependencies as struct fields on the
	// generated builders. For example:
	//
	//	DependencyAnnotation{
	//		Field:	"HTTPClient",
	//		Type:	"*http.Client",
	//		Option:	"WithClient",
	//	}
	//
	// Although the Dependency and the DependencyAnnotation are exported, used should
	// use the entc.Dependency option in order to build this annotation.
	Dependency struct {
		// Field defines the struct field name on the builders.
		// It defaults to the full type name. For example:
		//
		//	http.Client	=> HTTPClient
		//	net.Conn	=> NetConn
		//	url.URL		=> URL
		//
		Field string
		// Type defines the type identifier. For example, `*http.Client`.
		Type *field.TypeInfo
		// Option defines the name of the config option.
		// It defaults to the field name.
		Option string
	}
)

// Name describes the annotation name.
func (Dependencies) Name() string {
	return "Dependencies"
}

// Merge implements the schema.Merger interface.
func (d Dependencies) Merge(other schema.Annotation) schema.Annotation {
	if deps, ok := other.(Dependencies); ok {
		return append(d, deps...)
	}
	return d
}

var _ interface {
	schema.Annotation
	schema.Merger
} = (*Dependencies)(nil)

// Build builds the annotation and fails if it is invalid.
func (d *Dependency) Build() error {
	if d.Type == nil {
		return errors.New("velox/gen: missing dependency type")
	}
	if d.Field == "" {
		name, err := d.defaultName()
		if err != nil {
			return err
		}
		d.Field = name
	}
	if d.Option == "" {
		d.Option = d.Field
	}
	return nil
}

func (d *Dependency) defaultName() (string, error) {
	var pkg, name string
	switch parts := strings.Split(strings.TrimLeft(d.Type.Ident, "[]*"), "."); len(parts) {
	case 1:
		name = parts[0]
	case 2:
		name = parts[1]
		// Avoid stuttering.
		if !strings.EqualFold(parts[0], name) {
			pkg = parts[0]
		}
	default:
		return "", fmt.Errorf("velox/gen: unexpected number of parts: %q", parts)
	}
	if r := d.Type.RType; r != nil && (r.Kind == reflect.Array || r.Kind == reflect.Slice) {
		name = plural(name)
	}
	return pascal(pkg) + pascal(name), nil
}
