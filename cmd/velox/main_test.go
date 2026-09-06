package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_NoArgs(t *testing.T) {
	err := run(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no command specified")
}

func TestRun_Help(t *testing.T) {
	err := run([]string{"help"})
	assert.NoError(t, err)
}

func TestRun_DashH(t *testing.T) {
	err := run([]string{"-h"})
	assert.NoError(t, err)
}

func TestRun_DashDashHelp(t *testing.T) {
	err := run([]string{"--help"})
	assert.NoError(t, err)
}

func TestRun_UnknownCommand(t *testing.T) {
	err := run([]string{"foobar"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command: foobar")
}

func TestRun_Version(t *testing.T) {
	err := run([]string{"version"})
	assert.NoError(t, err)
}

func TestRun_GenerateMissingPath(t *testing.T) {
	err := run([]string{"generate"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing schema path")
}

func TestRun_GenerateInvalidPath(t *testing.T) {
	err := run([]string{"generate", "/nonexistent/path/to/schema"})
	require.Error(t, err)
}

func TestRun_GenerateWithTargetFlag(t *testing.T) {
	// Flag parsing should work even if the path doesn't exist
	err := run([]string{"generate", "--target", "./output", "/nonexistent/schema"})
	require.Error(t, err)
	// Error should be about the invalid schema path, not flag parsing
	assert.NotContains(t, err.Error(), "flag")
}

func TestRun_GenerateWithPackageFlag(t *testing.T) {
	err := run([]string{"generate", "--package", "mymodule/velox", "/nonexistent/schema"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "flag")
}

func TestRun_GenerateWithBothFlags(t *testing.T) {
	err := run([]string{"generate", "--target", "./output", "--package", "pkg/velox", "/nonexistent/schema"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "flag")
}

func TestRun_GenerateInvalidFlag(t *testing.T) {
	err := run([]string{"generate", "--invalid-flag"})
	require.Error(t, err)
}

func TestUsage_WritesToWriter(t *testing.T) {
	var buf bytes.Buffer
	usage(&buf)
	output := buf.String()
	assert.Contains(t, output, "Velox")
	assert.Contains(t, output, "generate")
	assert.Contains(t, output, "version")
	assert.Contains(t, output, "help")
	assert.Contains(t, output, "Usage:")
	assert.Contains(t, output, "Commands:")
	assert.Contains(t, output, "Examples:")
}

func TestVersionCmd(t *testing.T) {
	// Save and restore version/commit/date
	oldVersion, oldCommit, oldDate := version, commit, date
	defer func() { version, commit, date = oldVersion, oldCommit, oldDate }()

	version = "dev"
	commit = "none"
	date = "unknown"

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := versionCmd()

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	assert.NoError(t, err)
	assert.Equal(t, "velox version dev (commit: none, built: unknown)\n", buf.String())
}

func TestGenerateCmd_NoArgs(t *testing.T) {
	err := generateCmd(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing schema path")
}

func TestGenerateCmd_FlagParsing(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no flags", []string{"/nonexistent"}},
		{"with target", []string{"--target", "./velox", "/nonexistent"}},
		{"with package", []string{"--package", "mymodule/velox", "/nonexistent"}},
		{"with both flags", []string{"--target", "./output", "--package", "pkg/velox", "/nonexistent"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := generateCmd(tt.args)
			require.Error(t, err)
			// Should fail on schema loading, not flag parsing
			assert.NotContains(t, err.Error(), "flag provided but not defined")
		})
	}
}

func TestCommandParsing(t *testing.T) {
	// Commands that can be tested without side effects.
	validCommands := []string{"generate", "validate", "watch", "version", "help", "-h", "--help"}
	invalidCommands := []string{"unknown", "status", "migrate"}

	for _, cmd := range validCommands {
		t.Run("valid_"+cmd, func(t *testing.T) {
			err := run([]string{cmd})
			// Valid commands either succeed or fail for reasons other than "unknown command"
			if err != nil {
				assert.NotContains(t, err.Error(), "unknown command")
			}
		})
	}

	// init creates files, so run it in a temp dir.
	t.Run("valid_init", func(t *testing.T) {
		dir := t.TempDir()
		origDir, _ := os.Getwd()
		defer func() { _ = os.Chdir(origDir) }()
		require.NoError(t, os.Chdir(dir))

		err := run([]string{"init"})
		if err != nil {
			assert.NotContains(t, err.Error(), "unknown command")
		}
	})

	for _, cmd := range invalidCommands {
		t.Run("invalid_"+cmd, func(t *testing.T) {
			err := run([]string{cmd})
			require.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), "unknown command"),
				"expected 'unknown command' error for %q, got: %v", cmd, err)
		})
	}
}

func BenchmarkUsage(b *testing.B) {
	var buf bytes.Buffer
	for b.Loop() {
		buf.Reset()
		usage(&buf)
	}
}

// --- Validate command tests ---

func TestValidateCmd_MissingPath(t *testing.T) {
	err := run([]string{"validate"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing schema path")
}

func TestValidateCmd_InvalidPath(t *testing.T) {
	err := run([]string{"validate", "/nonexistent/path/to/schema"})
	require.Error(t, err)
}

// --- Config file tests ---

func TestLoadConfig_FromFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".velox.yml")
	err := os.WriteFile(cfgPath, []byte("schema: ./schema\ntarget: ./velox\nfeatures:\n  - privacy\n"), 0o644)
	require.NoError(t, err)

	cfg, err := loadConfigFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "./schema", cfg.Schema)
	assert.Equal(t, "./velox", cfg.Target)
	assert.Equal(t, []string{"privacy"}, cfg.Features)
}

func TestLoadConfig_NotFound(t *testing.T) {
	cfg, err := loadConfigFile("/nonexistent/.velox.yml")
	assert.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".velox.yml")
	err := os.WriteFile(cfgPath, []byte("schema:\n  - nested: [broken\n"), 0o644)
	require.NoError(t, err)

	_, err = loadConfigFile(cfgPath)
	assert.Error(t, err)
}

func TestFindConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".velox.yml")
	err := os.WriteFile(cfgPath, []byte("schema: ./schema\n"), 0o644)
	require.NoError(t, err)

	// Create a subdirectory and search from there.
	subDir := filepath.Join(dir, "sub", "deep")
	require.NoError(t, os.MkdirAll(subDir, 0o755))

	found := findConfigFile(subDir)
	assert.Equal(t, cfgPath, found)
}

func TestFindConfigFile_NotFound(t *testing.T) {
	dir := t.TempDir()
	found := findConfigFile(dir)
	assert.Empty(t, found)
}

// --- Init command tests ---

func TestInitCmd_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	err := run([]string{"init"})
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dir, "schema", "user.go"))
	assert.FileExists(t, filepath.Join(dir, ".velox.yml"))
	assert.FileExists(t, filepath.Join(dir, "generate.go"))
}

// --- Init command: writeIfNotExists tests ---

func TestWriteIfNotExists_NewFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.txt")
	written, err := writeIfNotExists(p, []byte("hello"))
	require.NoError(t, err)
	assert.True(t, written)

	data, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
}

func TestWriteIfNotExists_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(p, []byte("original"), 0o644))

	written, err := writeIfNotExists(p, []byte("new content"))
	require.NoError(t, err)
	assert.False(t, written)

	// Content should be unchanged.
	data, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, "original", string(data))
}

func TestWriteIfNotExists_BadDir(t *testing.T) {
	// Writing to a path whose parent dir doesn't exist.
	p := filepath.Join(t.TempDir(), "nonexistent", "sub", "file.txt")
	written, err := writeIfNotExists(p, []byte("data"))
	assert.Error(t, err)
	assert.False(t, written)
	assert.Contains(t, err.Error(), "failed to write")
}

func TestInitCmd_SkipsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	// Pre-create one of the files.
	require.NoError(t, os.MkdirAll("schema", 0o755))
	require.NoError(t, os.WriteFile(filepath.Join("schema", "user.go"), []byte("// custom"), 0o644))

	err := initCmd(nil)
	require.NoError(t, err)

	// The pre-existing file should NOT be overwritten.
	data, err := os.ReadFile(filepath.Join("schema", "user.go"))
	require.NoError(t, err)
	assert.Equal(t, "// custom", string(data))

	// Other files should still be created.
	assert.FileExists(t, filepath.Join(dir, ".velox.yml"))
	assert.FileExists(t, filepath.Join(dir, "generate.go"))
}

func TestInitCmd_IdempotentDoubleRun(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	// Running init twice should succeed both times.
	require.NoError(t, initCmd(nil))
	require.NoError(t, initCmd(nil))
}

// --- Verbose flag tests ---

func TestGenerateCmd_VerboseFlag(t *testing.T) {
	err := generateCmd([]string{"--verbose", "/nonexistent/schema"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "flag provided but not defined")
}

func TestWatchCmd_VerboseFlag(t *testing.T) {
	err := watchCmd([]string{"--verbose", "--debounce", "1s", "/nonexistent/schema"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "flag provided but not defined")
}

func TestValidateCmd_VerboseFlag(t *testing.T) {
	err := validateCmd([]string{"--verbose", "/nonexistent/schema"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "flag provided but not defined")
}

// --- resolveConfig tests ---

func TestResolveConfig_NoConfigFile(t *testing.T) {
	// Run in a temp dir with no .velox.yml.
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	res, err := resolveConfig([]string{"./schema"}, "", "")
	require.NoError(t, err)
	assert.Equal(t, "./schema", res.schemaPath)
	assert.Empty(t, res.opts)
}

func TestResolveConfig_SchemaFromConfigFile(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	// Create a config file with schema path.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".velox.yml"),
		[]byte("schema: ./my-schema\ntarget: ./out\npackage: mymod/out\n"), 0o644))

	// No CLI args — should use config file values.
	res, err := resolveConfig(nil, "", "")
	require.NoError(t, err)
	assert.Equal(t, "./my-schema", res.schemaPath)
	assert.NotEmpty(t, res.opts)
}

func TestResolveConfig_CLIArgOverridesConfigSchema(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".velox.yml"),
		[]byte("schema: ./config-schema\n"), 0o644))

	// CLI arg should take priority over config file.
	res, err := resolveConfig([]string{"./cli-schema"}, "", "")
	require.NoError(t, err)
	assert.Equal(t, "./cli-schema", res.schemaPath)
}

func TestResolveConfig_CLIFlagsOverrideConfig(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".velox.yml"),
		[]byte("schema: ./schema\ntarget: ./config-target\npackage: config/pkg\n"), 0o644))

	// CLI flags override config file target and package.
	res, err := resolveConfig([]string{"./schema"}, "./cli-target", "cli/pkg")
	require.NoError(t, err)
	assert.Equal(t, "./schema", res.schemaPath)
	// Both config file and CLI values produce opts; CLI opts come last and win.
	assert.True(t, len(res.opts) >= 4, "expected at least 4 options (2 config + 2 CLI)")
}

func TestResolveConfig_WithFeatures(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".velox.yml"),
		[]byte("schema: ./schema\nfeatures:\n  - privacy\n  - intercept\n"), 0o644))

	res, err := resolveConfig(nil, "", "")
	require.NoError(t, err)
	assert.Equal(t, "./schema", res.schemaPath)
	assert.NotEmpty(t, res.opts)
}

func TestResolveConfig_UnknownFeature(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".velox.yml"),
		[]byte("schema: ./schema\nfeatures:\n  - nonexistent_feature\n"), 0o644))

	_, err := resolveConfig(nil, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown feature")
	assert.Contains(t, err.Error(), "nonexistent_feature")
}

func TestResolveConfig_InvalidConfigFile(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".velox.yml"),
		[]byte("schema:\n  - nested: [broken\n"), 0o644))

	_, err := resolveConfig(nil, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load config file")
}

func TestResolveConfig_EmptySchemaPath(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	// No config file, no CLI arg — schema path should be empty.
	res, err := resolveConfig(nil, "", "")
	require.NoError(t, err)
	assert.Empty(t, res.schemaPath)
}

// --- generateCmd: dry-run tests ---

func TestGenerateCmd_DryRunNoSchemaPath(t *testing.T) {
	err := generateCmd([]string{"--dry-run"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing schema path")
}

func TestGenerateCmd_DryRunValidConfig(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	// dry-run only validates config, doesn't need a real schema directory.
	err := generateCmd([]string{"--dry-run", "./schema"})
	require.NoError(t, err)
}

func TestGenerateCmd_DryRunWithFlags(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	err := generateCmd([]string{"--dry-run", "--target", "./out", "--package", "mymod/out", "./schema"})
	require.NoError(t, err)
}

func TestGenerateCmd_DryRunWithConfigFile(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".velox.yml"),
		[]byte("schema: ./schema\ntarget: ./velox\nfeatures:\n  - privacy\n"), 0o644))

	err := generateCmd([]string{"--dry-run"})
	require.NoError(t, err)
}

func TestGenerateCmd_DryRunUnknownFeature(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".velox.yml"),
		[]byte("schema: ./schema\nfeatures:\n  - bogus_feature\n"), 0o644))

	err := generateCmd([]string{"--dry-run"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown feature")
}

// --- validate command additional tests ---

func TestValidateCmd_FlagParsing(t *testing.T) {
	err := validateCmd([]string{"--help"})
	// flag.ContinueOnError causes --help to return flag.ErrHelp.
	require.Error(t, err)
}

func TestValidateCmd_NoArgs(t *testing.T) {
	err := validateCmd(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing schema path")
}

// --- generate function tests ---

func TestGenerate_InvalidConfig(t *testing.T) {
	// Empty schema path should fail in the compiler.
	err := generate(context.TODO(), "/nonexistent/schema", nil)
	require.Error(t, err)
}

// --- Watch command tests ---

func TestWatchCmd_MissingPath(t *testing.T) {
	err := run([]string{"watch"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing schema path")
}

func TestWatchCmd_InvalidFlag(t *testing.T) {
	err := run([]string{"watch", "--invalid-flag"})
	require.Error(t, err)
}

func TestWatchCmd_FlagParsing(t *testing.T) {
	// Verify that --debounce flag is accepted (even though the command will fail
	// because the schema path is non-existent).
	err := watchCmd([]string{"--target", "./out", "--debounce", "1s", "/nonexistent/schema"})
	require.Error(t, err)
	// Error should be about the schema path, not flag parsing.
	assert.NotContains(t, err.Error(), "flag provided but not defined")
}

func TestWatchCmd_MissingPathFromConfig(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	// Config file without schema path, no CLI arg.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".velox.yml"),
		[]byte("target: ./out\n"), 0o644))

	err := watchCmd(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing schema path")
}

// --- findConfigFile edge cases ---

func TestFindConfigFile_InCurrentDir(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".velox.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("schema: ./s\n"), 0o644))

	found := findConfigFile(dir)
	assert.Equal(t, cfgPath, found)
}

// --- Config file with all fields ---

func TestLoadConfigFile_AllFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".velox.yml")
	content := `schema: ./schema
target: ./output
package: mymod/output
features:
  - privacy
  - intercept
  - entql
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0o644))

	cfg, err := loadConfigFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "./schema", cfg.Schema)
	assert.Equal(t, "./output", cfg.Target)
	assert.Equal(t, "mymod/output", cfg.Package)
	assert.Equal(t, []string{"privacy", "intercept", "entql"}, cfg.Features)
}

func TestLoadConfigFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".velox.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(""), 0o644))

	cfg, err := loadConfigFile(cfgPath)
	require.NoError(t, err)
	// Empty YAML yields nil struct fields / zero values.
	assert.NotNil(t, cfg)
	assert.Empty(t, cfg.Schema)
	assert.Empty(t, cfg.Target)
	assert.Empty(t, cfg.Features)
}

// --- Version with custom values ---

func TestVersionCmd_CustomValues(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	defer func() { version, commit, date = oldVersion, oldCommit, oldDate }()

	version = "1.2.3"
	commit = "abc1234"
	date = "2026-01-15"

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := versionCmd()

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)

	assert.NoError(t, err)
	output := buf.String()
	assert.Contains(t, output, "1.2.3")
	assert.Contains(t, output, "abc1234")
	assert.Contains(t, output, "2026-01-15")
}

// --- resolveConfig: config file with target only, package only ---

func TestResolveConfig_ConfigTargetOnly(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".velox.yml"),
		[]byte("target: ./out\n"), 0o644))

	res, err := resolveConfig([]string{"./schema"}, "", "")
	require.NoError(t, err)
	assert.Equal(t, "./schema", res.schemaPath)
	// Should have 1 option from config target.
	assert.Len(t, res.opts, 1)
}

func TestResolveConfig_ConfigPackageOnly(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(origDir) }()
	require.NoError(t, os.Chdir(dir))

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".velox.yml"),
		[]byte("package: mymod/velox\n"), 0o644))

	res, err := resolveConfig([]string{"./schema"}, "", "")
	require.NoError(t, err)
	assert.Len(t, res.opts, 1)
}
