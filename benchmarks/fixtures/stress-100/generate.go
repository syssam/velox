//go:build ignore

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
)

func main() {
	// Generate 100 schema files
	schemaDir := filepath.Join(".", "schema")
	os.MkdirAll(schemaDir, 0o755)

	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("Entity%03d", i)
		next := fmt.Sprintf("Entity%03d", (i+1)%100)
		code := fmt.Sprintf(`package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type %s struct{ velox.Schema }

func (%s) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (%s) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.String("description").Optional().Nillable(),
		field.Int("count").Default(0),
		field.String("status").Optional().Nillable(),
	}
}

func (%s) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("children", %s.Type),
	}
}
`, name, name, name, name, next)
		os.WriteFile(filepath.Join(schemaDir, fmt.Sprintf("entity%03d.go", i)), []byte(code), 0o644)
	}

	config, err := gen.NewConfig(
		gen.WithTarget("./velox"),
		gen.WithFeatures(gen.FeatureIntercept),
	)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	start := time.Now()
	if err := compiler.Generate("./schema", config); err != nil {
		log.Fatalf("generate: %v", err)
	}
	fmt.Printf("Generated 100 entities in %v\n", time.Since(start))
}
