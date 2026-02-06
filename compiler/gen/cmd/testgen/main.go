// testgen is a simple test program to demonstrate the Jennifer-based code generator.
// Run: go run ./compiler/gen/cmd/testgen
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/gen/sql"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

func main() {
	// Create a temp directory for output
	outDir, err := os.MkdirTemp("", "velox-jennifer-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Output directory: %s\n", outDir)

	// Define test schemas (similar to ent-master/examples/start)
	schemas := []*load.Schema{
		{
			Name: "User",
			Fields: []*load.Field{
				{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
				{Name: "age", Info: &field.TypeInfo{Type: field.TypeInt}, Optional: true},
			},
			Edges: []*load.Edge{
				{Name: "cars", Type: "Car"},
				{Name: "groups", Type: "Group", Inverse: true, RefName: "users"},
			},
		},
		{
			Name: "Car",
			Fields: []*load.Field{
				{Name: "model", Info: &field.TypeInfo{Type: field.TypeString}},
				{Name: "registered_at", Info: &field.TypeInfo{Type: field.TypeTime}},
			},
			Edges: []*load.Edge{
				{Name: "owner", Type: "User", Unique: true, Inverse: true, RefName: "cars"},
			},
		},
		{
			Name: "Group",
			Fields: []*load.Field{
				{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
			},
			Edges: []*load.Edge{
				{Name: "users", Type: "User"},
			},
		},
	}

	// Create config with functional options
	config, err := gen.NewConfig(
		gen.WithPackage("example.com/test/velox"),
		gen.WithTarget(outDir),
		gen.WithIDTypeInfo(&field.TypeInfo{Type: field.TypeInt}),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create config: %v\n", err)
		os.Exit(1)
	}

	// Create the graph
	graph, err := gen.NewGraph(config, schemas...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create graph: %v\n", err)
		os.Exit(1)
	}

	// Generate using Jennifer with SQL dialect
	fmt.Println("Generating code with Jennifer (SQL dialect)...")
	if err = sql.Generate(graph); err != nil {
		fmt.Fprintf(os.Stderr, "generation failed: %v\n", err)
		os.Exit(1)
	}

	// List generated files
	fmt.Println("\nGenerated files:")
	err = filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			relPath, _ := filepath.Rel(outDir, path)
			fmt.Printf("  %s (%d bytes)\n", relPath, info.Size())
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list files: %v\n", err)
	}

	// Show sample output
	fmt.Println("\n--- Sample: user.go ---")
	content, err := os.ReadFile(filepath.Join(outDir, "user.go"))
	if err == nil {
		// Show first 80 lines
		lines := 0
		for i, c := range content {
			fmt.Print(string(c))
			if c == '\n' {
				lines++
				if lines >= 80 {
					fmt.Println("... (truncated)")
					break
				}
			}
			if i >= 4000 {
				fmt.Println("... (truncated)")
				break
			}
		}
	}

	fmt.Printf("\n\nTo inspect generated code: ls -la %s\n", outDir)
	fmt.Println("To verify compilation: go build " + outDir + "/...")

	fmt.Println("Done!")
}
