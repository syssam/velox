package gen

// Tests for the external-template loader (template.go).

import (
	"os"
	"testing"
	"text/template/parse"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// template.go — ParseFiles, ParseGlob, AddParseTree, Dependencies
// =============================================================================

func TestTemplate_ParseFiles_Error(t *testing.T) {
	tmpl := NewTemplate("test")
	// Non-existent file → error.
	_, err := tmpl.ParseFiles("/nonexistent/file.tmpl")
	assert.Error(t, err)
}

func TestTemplate_ParseGlob_Error(t *testing.T) {
	tmpl := NewTemplate("test")
	// No matching files → error (ParseGlob fails with no files).
	_, err := tmpl.ParseGlob("/nonexistent/*.tmpl")
	assert.Error(t, err)
}

func TestTemplate_AddParseTree(t *testing.T) {
	tmpl := NewTemplate("test")
	tree := &parse.Tree{Name: "child"}
	result, err := tmpl.AddParseTree("child", tree)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestDependencies_Name(t *testing.T) {
	d := Dependencies{}
	assert.Equal(t, "Dependencies", d.Name())
}

func TestDependencies_Merge(t *testing.T) {
	d1 := Dependencies{&Dependency{Field: "HTTPClient"}}
	d2 := Dependencies{&Dependency{Field: "Logger"}}
	merged := d1.Merge(d2)
	deps, ok := merged.(Dependencies)
	require.True(t, ok)
	assert.Len(t, deps, 2)
}

func TestDependencies_Merge_NonDeps(t *testing.T) {
	d := Dependencies{&Dependency{Field: "HTTPClient"}}
	// Merge with non-Dependencies annotation → returns original.
	result := d.Merge(nil)
	assert.Equal(t, d, result)
}

// =============================================================================
// template.go — ParseDir
// =============================================================================

func TestTemplate_ParseDir_Empty(t *testing.T) {
	dir := t.TempDir()
	tmpl := NewTemplate("test")
	result, err := tmpl.ParseDir(dir)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestTemplate_ParseDir_WithFile(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(dir+"/my.tmpl", []byte("{{.Name}}"), 0o644)
	require.NoError(t, err)

	tmpl := NewTemplate("test")
	result, err := tmpl.ParseDir(dir)
	require.NoError(t, err)
	assert.NotNil(t, result)
}
