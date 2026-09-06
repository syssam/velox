package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// writeIfNotExists writes content to path only if the file does not already exist.
// Returns true if the file was written, false if it was skipped.
func writeIfNotExists(path string, content []byte) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil // file exists, skip
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return false, fmt.Errorf("failed to write %s: %w", path, err)
	}
	return true, nil
}

func initCmd(_ []string) error {
	// Create schema directory.
	if err := os.MkdirAll("schema", 0o755); err != nil {
		return fmt.Errorf("failed to create schema directory: %w", err)
	}

	type fileEntry struct {
		path    string
		content string
		label   string
	}
	files := []fileEntry{
		{filepath.Join("schema", "user.go"), exampleSchema, "example schema"},
		{"generate.go", generateFile, "code generation entrypoint"},
		{".velox.yml", defaultConfig, "configuration"},
	}

	slog.Info("velox: project initialized")
	for _, f := range files {
		written, err := writeIfNotExists(f.path, []byte(f.content))
		if err != nil {
			return fmt.Errorf("write %s: %w", f.path, err)
		}
		if written {
			slog.Info("created file", "path", f.path, "description", f.label)
		} else {
			slog.Info("file already exists, skipping", "path", f.path)
		}
	}
	slog.Info("next steps: 1) edit schema/user.go to define your entities, 2) run: go generate ./...")
	return nil
}

const exampleSchema = `package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// User holds the schema definition for the User entity.
type User struct {
	velox.Schema
}

// Mixin of the User.
func (User) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the User.
func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			NotEmpty(),
		field.String("email").
			Unique(),
	}
}

// Edges of the User.
func (User) Edges() []velox.Edge {
	return nil
}
`

const generateFile = `//go:build ignore

package main

import (
	"log/slog"
	"os"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
)

func main() {
	cfg, err := gen.NewConfig(
		gen.WithTarget("./velox"),
	)
	if err != nil {
		slog.Error("creating config", "error", err)
		os.Exit(1)
	}
	if err := compiler.Generate("./schema", cfg); err != nil {
		slog.Error("running velox codegen", "error", err)
		os.Exit(1)
	}
	slog.Info("Code generation completed successfully!")
}
`

const defaultConfig = `# Velox ORM configuration
schema: ./schema
target: ./velox
# package: mymodule/velox
# features:
#   - privacy
#   - intercept
`
