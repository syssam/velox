package compiler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"

	"golang.org/x/tools/go/packages"
)

// testAnnotation is a minimal schema.Annotation implementation for tests.
type testAnnotation struct{ name string }

func (a *testAnnotation) Name() string { return a.name }

// =============================================================================
// Group 1: Option functions
// =============================================================================

func TestStorage(t *testing.T) {
	cfg := &gen.Config{}
	opt := Storage("sql")
	require.NoError(t, opt(cfg))
	require.NotNil(t, cfg.Storage)
	assert.Equal(t, "sql", cfg.Storage.Name)
}

func TestStorage_InvalidDriver(t *testing.T) {
	cfg := &gen.Config{}
	opt := Storage("nosql")
	err := opt(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nosql")
}

func TestFeatureNames(t *testing.T) {
	cfg := &gen.Config{}
	opt := FeatureNames("intercept", "privacy")
	require.NoError(t, opt(cfg))
	names := make([]string, 0, len(cfg.Features))
	for _, f := range cfg.Features {
		names = append(names, f.Name)
	}
	assert.Contains(t, names, "intercept")
	assert.Contains(t, names, "privacy")
	assert.Len(t, cfg.Features, 2)
}

func TestFeatureNames_Unknown(t *testing.T) {
	cfg := &gen.Config{}
	opt := FeatureNames("nonexistent")
	err := opt(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown feature name")
}

func TestBuildFlags(t *testing.T) {
	cfg := &gen.Config{}
	opt := BuildFlags("-mod=vendor", "-v")
	require.NoError(t, opt(cfg))
	assert.Equal(t, []string{"-mod=vendor", "-v"}, cfg.BuildFlags)
}

func TestBuildTags(t *testing.T) {
	cfg := &gen.Config{}
	opt := BuildTags("integration", "postgres")
	require.NoError(t, opt(cfg))
	// BuildTags converts to "-tags" flag
	require.Len(t, cfg.BuildFlags, 2)
	assert.Equal(t, "-tags", cfg.BuildFlags[0])
	assert.Equal(t, "integration,postgres", cfg.BuildFlags[1])
}

func TestAnnotations(t *testing.T) {
	cfg := &gen.Config{}
	ant := &testAnnotation{name: "TestAnt"}
	opt := Annotations(ant)
	require.NoError(t, opt(cfg))
	require.NotNil(t, cfg.Annotations)
	assert.Equal(t, ant, cfg.Annotations["TestAnt"])
}

func TestAnnotations_Duplicate(t *testing.T) {
	cfg := &gen.Config{}
	ant := &testAnnotation{name: "DupAnt"}
	opt := Annotations(ant)
	require.NoError(t, opt(cfg))
	// Second application with same name and no Merger should fail.
	err := opt(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate annotations")
}

// =============================================================================
// Group 2: Dependency options
// =============================================================================

func TestDependencyType(t *testing.T) {
	d := &gen.Dependency{}
	opt := DependencyType(&testing.T{})
	require.NoError(t, opt(d))
	require.NotNil(t, d.Type)
	require.NotNil(t, d.Type.RType)
	// The pointer type should be represented.
	assert.Contains(t, d.Type.RType.Ident, "testing.T")
}

func TestDependencyType_Nil(t *testing.T) {
	d := &gen.Dependency{}
	opt := DependencyType(nil)
	err := opt(d)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil dependency type")
}

func TestDependencyTypeInfo(t *testing.T) {
	d := &gen.Dependency{}
	info := &field.TypeInfo{Ident: "http.Client"}
	opt := DependencyTypeInfo(info)
	require.NoError(t, opt(d))
	assert.Equal(t, info, d.Type)
}

func TestDependencyTypeInfo_Nil(t *testing.T) {
	d := &gen.Dependency{}
	opt := DependencyTypeInfo(nil)
	err := opt(d)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil dependency type info")
}

func TestDependencyName(t *testing.T) {
	d := &gen.Dependency{}
	opt := DependencyName("DB")
	require.NoError(t, opt(d))
	assert.Equal(t, "DB", d.Field)
	assert.Equal(t, "DB", d.Option)
}

func TestDefaultExtension(t *testing.T) {
	ext := DefaultExtension{}
	assert.Nil(t, ext.Hooks())
	assert.Nil(t, ext.Annotations())
	assert.Nil(t, ext.Templates())
	assert.Nil(t, ext.Options())
}

func TestExtensions(t *testing.T) {
	cfg := &gen.Config{}
	ext := DefaultExtension{}
	opt := Extensions(ext)
	require.NoError(t, opt(cfg))
}

// =============================================================================
// Group 3: Validation helpers
// =============================================================================

func TestNormalizePkg(t *testing.T) {
	tests := []struct {
		name    string
		pkg     string
		wantErr bool
	}{
		{
			name:    "valid package",
			pkg:     "github.com/org/project/velox",
			wantErr: false,
		},
		{
			name:    "hyphenated path component",
			pkg:     "github.com/org/my-project/velox",
			wantErr: false,
		},
		{
			name:    "invalid identifier starting with digit",
			pkg:     "github.com/org/123invalid",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &gen.Config{Package: tc.pkg}
			err := normalizePkg(cfg)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid package identifier")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateTarget_ModuleRoot(t *testing.T) {
	dir := t.TempDir()
	// Create a go.mod to make this look like a module root.
	gomod := filepath.Join(dir, "go.mod")
	require.NoError(t, os.WriteFile(gomod, []byte("module example.com/test\n\ngo 1.21\n"), 0o644))

	err := validateTarget(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Go module root")
}

func TestValidateTarget_ValidSubdir(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "velox")
	require.NoError(t, os.MkdirAll(subdir, 0o755))

	// No go.mod in subdir — should pass.
	err := validateTarget(subdir)
	require.NoError(t, err)
}

func TestDefaultTarget(t *testing.T) {
	// Simulate: schema at <dir>/velox/schema
	// Expected target: <dir>/velox
	base := t.TempDir()
	schemaDir := filepath.Join(base, "velox", "schema")
	require.NoError(t, os.MkdirAll(schemaDir, 0o755))

	cfg := &gen.Config{}
	err := defaultTarget(schemaDir, cfg)
	require.NoError(t, err)

	expectedTarget, err := filepath.Abs(filepath.Join(base, "velox"))
	require.NoError(t, err)
	assert.Equal(t, expectedTarget, cfg.Target)
}

func TestDefaultTarget_ExplicitTarget(t *testing.T) {
	base := t.TempDir()
	explicitTarget := filepath.Join(base, "custom")
	require.NoError(t, os.MkdirAll(explicitTarget, 0o755))

	cfg := &gen.Config{Target: explicitTarget}
	schemaDir := filepath.Join(base, "schema")
	err := defaultTarget(schemaDir, cfg)
	require.NoError(t, err)
	// Target should not be changed.
	assert.Equal(t, explicitTarget, cfg.Target)
}

// =============================================================================
// Group 4: Generate error paths
// =============================================================================

func TestGenerate_InvalidSchemaPath(t *testing.T) {
	cfg, err := gen.NewConfig(gen.WithTarget(t.TempDir()))
	require.NoError(t, err)

	err = Generate("/nonexistent/path/to/schema", cfg)
	require.Error(t, err)
}

func TestGenerateContext_OptionError(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "velox")
	require.NoError(t, os.MkdirAll(targetDir, 0o755))

	cfg, err := gen.NewConfig(gen.WithTarget(targetDir))
	require.NoError(t, err)

	failingOpt := Option(func(_ *gen.Config) error {
		return fmt.Errorf("intentional option failure")
	})

	err = GenerateContext(context.Background(), filepath.Join(dir, "schema"), cfg, failingOpt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "intentional option failure")
}

func TestGenerateContext_InvalidStorage(t *testing.T) {
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "velox")
	require.NoError(t, os.MkdirAll(targetDir, 0o755))

	cfg, err := gen.NewConfig(gen.WithTarget(targetDir))
	require.NoError(t, err)

	err = GenerateContext(context.Background(), filepath.Join(dir, "schema"), cfg, Storage("invalid"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestGenerateContext_TargetIsModuleRoot(t *testing.T) {
	dir := t.TempDir()
	// Make the target a module root.
	gomod := filepath.Join(dir, "go.mod")
	require.NoError(t, os.WriteFile(gomod, []byte("module example.com/test\n\ngo 1.21\n"), 0o644))

	cfg := &gen.Config{Target: dir}
	err := GenerateContext(context.Background(), filepath.Join(dir, "schema"), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Go module root")
}

// =============================================================================
// Group 5: Template options
// =============================================================================

func TestTemplateDir_NonExistent(t *testing.T) {
	cfg := &gen.Config{}
	opt := TemplateDir("/nonexistent/template/dir")
	err := opt(cfg)
	// ParseDir on a nonexistent dir returns an error.
	require.Error(t, err)
}

func TestTemplateDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	cfg := &gen.Config{}
	opt := TemplateDir(dir)
	// Empty dir with no *.tmpl files is valid.
	err := opt(cfg)
	require.NoError(t, err)
	// One Template was appended.
	assert.Len(t, cfg.Templates, 1)
}

func TestTemplateFiles_NonExistent(t *testing.T) {
	cfg := &gen.Config{}
	opt := TemplateFiles("/nonexistent/file.tmpl")
	err := opt(cfg)
	require.Error(t, err)
}

func TestTemplateGlob_NoMatch(t *testing.T) {
	dir := t.TempDir()
	cfg := &gen.Config{}
	// Pattern that matches nothing — ParseGlob returns an error.
	opt := TemplateGlob(filepath.Join(dir, "*.tmpl"))
	err := opt(cfg)
	require.Error(t, err)
}

// =============================================================================
// Group 6: Dependency compiler option (end-to-end)
// =============================================================================

func TestDependency_ValidType(t *testing.T) {
	cfg := &gen.Config{}
	opt := Dependency(
		DependencyName("Logger"),
		DependencyType(&testing.T{}),
	)
	require.NoError(t, opt(cfg))
	// The dependency should have been added as an annotation.
	require.NotNil(t, cfg.Annotations)
	assert.NotNil(t, cfg.Annotations["Dependencies"])
}

func TestDependency_NilType(t *testing.T) {
	cfg := &gen.Config{}
	opt := Dependency(DependencyType(nil))
	err := opt(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil dependency type")
}

// =============================================================================
// Group 7: Extensions with hooks and annotations
// =============================================================================

// testExtension is a full Extension implementation for testing.
type testExtension struct {
	DefaultExtension
	hooks   []gen.Hook
	annots  []Annotation
	options []Option
}

func (e *testExtension) Hooks() []gen.Hook         { return e.hooks }
func (e *testExtension) Annotations() []Annotation { return e.annots }
func (e *testExtension) Options() []Option         { return e.options }

func TestExtensions_WithHooksAndAnnotations(t *testing.T) {
	var hookCalled bool
	ext := &testExtension{
		hooks: []gen.Hook{
			func(next gen.Generator) gen.Generator {
				hookCalled = true
				return next
			},
		},
		annots: []Annotation{&testAnnotation{name: "ExtAnt"}},
	}

	cfg := &gen.Config{}
	opt := Extensions(ext)
	require.NoError(t, opt(cfg))

	// Hook should be appended.
	require.Len(t, cfg.Hooks, 1)
	// Trigger the hook to confirm it's the right one.
	cfg.Hooks[0](nil)
	assert.True(t, hookCalled)

	// Annotation should be present.
	require.NotNil(t, cfg.Annotations)
	assert.Equal(t, &testAnnotation{name: "ExtAnt"}, cfg.Annotations["ExtAnt"])
}

func TestExtensions_OptionError(t *testing.T) {
	ext := &testExtension{
		options: []Option{func(_ *gen.Config) error {
			return fmt.Errorf("option failure in extension")
		}},
	}

	cfg := &gen.Config{}
	opt := Extensions(ext)
	err := opt(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "option failure in extension")
}

func TestExtensions_DuplicateAnnotation(t *testing.T) {
	ant := &testAnnotation{name: "ConflictAnt"}

	ext1 := &testExtension{annots: []Annotation{ant}}
	ext2 := &testExtension{annots: []Annotation{ant}}

	cfg := &gen.Config{}
	// First extension succeeds.
	require.NoError(t, Extensions(ext1)(cfg))
	// Second extension with same annotation name and no merger should fail.
	err := Extensions(ext2)(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate annotations")
}

// =============================================================================
// Group 8: Annotations with Merger
// =============================================================================

// mergerAnnotation implements schema.Annotation and schema.Merger.
// schema.Merger requires: Merge(schema.Annotation) schema.Annotation
type mergerAnnotation struct {
	name   string
	values []string
}

func (a *mergerAnnotation) Name() string { return a.name }

// Merge implements schema.Merger.
func (a *mergerAnnotation) Merge(other Annotation) Annotation {
	if o, ok := other.(*mergerAnnotation); ok {
		combined := make([]string, len(a.values)+len(o.values))
		copy(combined, a.values)
		copy(combined[len(a.values):], o.values)
		return &mergerAnnotation{name: a.name, values: combined}
	}
	return a
}

func TestAnnotations_WithMerger(t *testing.T) {
	cfg := &gen.Config{}

	first := &mergerAnnotation{name: "MergeAnt", values: []string{"a"}}
	second := &mergerAnnotation{name: "MergeAnt", values: []string{"b"}}

	require.NoError(t, Annotations(first)(cfg))
	require.NoError(t, Annotations(second)(cfg))

	merged, ok := cfg.Annotations["MergeAnt"].(*mergerAnnotation)
	require.True(t, ok)
	assert.Equal(t, []string{"a", "b"}, merged.values)
}

// =============================================================================
// Group 9: mayRecover — snapshot recovery path
// =============================================================================

func TestMayRecover_NoSnapshotFeature(t *testing.T) {
	// Without the snapshot feature enabled, mayRecover returns the original error.
	cfg := &gen.Config{Target: t.TempDir()}
	origErr := errors.New("some build error")
	err := mayRecover(origErr, "/fake/schema", cfg)
	assert.Equal(t, origErr, err)
}

func TestMayRecover_NonBuildError(t *testing.T) {
	// With snapshot enabled but a non-build error, mayRecover returns the original error.
	cfg := &gen.Config{
		Target:   t.TempDir(),
		Features: []gen.Feature{gen.FeatureSnapshot},
	}
	origErr := errors.New("not a build error")
	err := mayRecover(origErr, "/fake/schema", cfg)
	assert.Equal(t, origErr, err)
}

func TestMayRecover_BuildError_InvalidSchemaDir(t *testing.T) {
	// With snapshot enabled and a packages.Error, mayRecover checks the schema dir.
	// If the schema dir is invalid, it wraps the error.
	cfg := &gen.Config{
		Target:   t.TempDir(),
		Features: []gen.Feature{gen.FeatureSnapshot},
	}
	pkgErr := fmt.Errorf("velox/load: parse schema dir: %w", &packages.Error{
		Pos: "", Msg: "syntax error", Kind: packages.ListError,
	})
	err := mayRecover(pkgErr, "/nonexistent/schema/path", cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema failure")
}

func TestMayRecover_BuildError_NoSnapshotFile(t *testing.T) {
	// With snapshot enabled, a valid schema dir, but no snapshot file,
	// Restore() should fail because internal/schema.go doesn't exist.
	dir := t.TempDir()
	schemaDir := filepath.Join(dir, "schema")
	require.NoError(t, os.MkdirAll(schemaDir, 0o755))
	// Create a minimal Go file in schema dir so CheckDir passes.
	require.NoError(t, os.WriteFile(
		filepath.Join(schemaDir, "schema.go"),
		[]byte("package schema\n"),
		0o644,
	))

	targetDir := filepath.Join(dir, "velox")
	require.NoError(t, os.MkdirAll(targetDir, 0o755))

	cfg := &gen.Config{
		Target:   targetDir,
		Features: []gen.Feature{gen.FeatureSnapshot},
	}
	// Use IsBuildError-matching error string.
	buildErr := errors.New("velox/load: # some build error")
	err := mayRecover(buildErr, schemaDir, cfg)
	// Should fail because there's no internal/schema.go snapshot to restore from.
	require.Error(t, err)
}
