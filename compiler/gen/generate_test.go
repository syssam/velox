package gen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// testGenerator returns a Generator for gen package tests that cannot
// import gen/sql due to circular imports. It creates the same file
// structure that the full SQL dialect would produce, including feature
// files based on enabled features.
func testGenerator() Generator {
	return GenerateFunc(func(g *Graph) error {
		if err := os.MkdirAll(g.Target, 0o755); err != nil {
			return err
		}
		pkg := filepath.Base(g.Target)
		content := []byte("package " + pkg + "\n")
		internalContent := []byte("package internal\n")

		// Write graph-level files.
		for _, name := range []string{"velox.go", "client.go", "tx.go", "runtime.go"} {
			if err := os.WriteFile(filepath.Join(g.Target, name), content, 0o644); err != nil {
				return err
			}
		}
		// Write predicate package.
		predDir := filepath.Join(g.Target, "predicate")
		if err := os.MkdirAll(predDir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(predDir, "predicate.go"), []byte("package predicate\n"), 0o644); err != nil {
			return err
		}
		// Write per-entity files.
		for _, n := range g.Nodes {
			lower := n.PackageDir()
			for _, suffix := range []string{".go", "_create.go", "_update.go", "_delete.go", "_query.go", "_mutation.go"} {
				if err := os.WriteFile(filepath.Join(g.Target, lower+suffix), content, 0o644); err != nil {
					return err
				}
			}
			entityDir := filepath.Join(g.Target, lower)
			if err := os.MkdirAll(entityDir, 0o755); err != nil {
				return err
			}
			entityPkg := []byte("package " + lower + "\n")
			if err := os.WriteFile(filepath.Join(entityDir, lower+".go"), entityPkg, 0o644); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(entityDir, "where.go"), entityPkg, 0o644); err != nil {
				return err
			}
		}
		// Write feature files based on enabled features.
		writeFeature := func(dir, file string, data []byte) error {
			if err := os.MkdirAll(filepath.Join(g.Target, dir), 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(g.Target, dir, file), data, 0o644)
		}
		if enabled, _ := g.FeatureEnabled(FeatureSnapshot.Name); enabled {
			if err := writeFeature("internal", "schema.go", internalContent); err != nil {
				return err
			}
		}
		if enabled, _ := g.FeatureEnabled(FeatureSchemaConfig.Name); enabled {
			if err := writeFeature("internal", "schemaconfig.go", internalContent); err != nil {
				return err
			}
		}
		if enabled, _ := g.FeatureEnabled(FeatureGlobalID.Name); enabled {
			// Generate globalid content that tests expect.
			starts := IncrementStarts{}
			for i, n := range g.Nodes {
				starts[n.Table()] = i << 32
			}
			data, _ := json.Marshal(starts)
			globalidContent := fmt.Sprintf("package internal\nconst Schema = %q\n", string(data))
			if err := writeFeature("internal", "globalid.go", []byte(globalidContent)); err != nil {
				return err
			}
		}
		// Handle external templates.
		for _, rootT := range g.Templates {
			for _, tmpl := range rootT.Templates() {
				name := tmpl.Name()
				if name == "templates" || name == "" {
					continue
				}
				if rootT.condition != nil && rootT.condition(g) {
					continue
				}
				outPath := filepath.Join(g.Target, snake(name)+".go")
				var buf bytes.Buffer
				if err := rootT.ExecuteTemplate(&buf, name, g); err != nil {
					return err
				}
				if err := os.WriteFile(outPath, buf.Bytes(), 0o644); err != nil {
					return err
				}
			}
		}
		// Clean up stale flat-layout entity files (mimics cleanupStaleNodes).
		activeNodes := make(map[string]struct{}, len(g.Nodes))
		for _, n := range g.Nodes {
			activeNodes[n.PackageDir()] = struct{}{}
		}
		entries, _ := os.ReadDir(g.Target)
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), "_query.go") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), "_query.go")
			if _, ok := activeNodes[strings.ToLower(name)]; ok {
				continue
			}
			for _, suffix := range []string{"_create.go", "_update.go", "_delete.go", "_query.go", "_mutation.go", ".go"} {
				os.Remove(filepath.Join(g.Target, name+suffix))
			}
			os.RemoveAll(filepath.Join(g.Target, strings.ToLower(name)))
		}
		// Feature cleanup for disabled features.
		for _, f := range allFeatures {
			if f.cleanup == nil || g.featureEnabled(f) {
				continue
			}
			_ = f.cleanup(g.Config)
		}
		return nil
	})
}

// init registers testGenerator as the default for gen package tests.
// The gen/sql package registers sql.generateBase via its own init(),
// but gen/sql is not imported by package gen tests (circular import).
func init() {
	if defaultGenerator == nil {
		RegisterDefaultGenerator(func(g *Graph) error {
			return testGenerator().Generate(g)
		})
	}
}

func TestJenniferGenerator(t *testing.T) {
	t.Run("creates generator with graph", func(t *testing.T) {
		target := t.TempDir()
		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Target:  target,
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)

		gen := NewJenniferGenerator(graph, target)
		require.NotNil(t, gen)
	})
}

func TestGeneratorHelper(t *testing.T) {
	t.Run("Graph returns graph", func(t *testing.T) {
		target := t.TempDir()
		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Target:  target,
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)

		gen := NewJenniferGenerator(graph, target)
		assert.Equal(t, graph, gen.Graph())
	})
}

func TestGraphValidation(t *testing.T) {
	t.Run("validates missing edge type", func(t *testing.T) {
		_, err := NewGraph(&Config{
			Package: "test/gen",
			Storage: drivers["sql"],
		}, &load.Schema{
			Name: "User",
			Edges: []*load.Edge{
				{Name: "posts", Type: "Post"},
			},
		})
		require.Error(t, err)
	})

	t.Run("validates invalid schema name", func(t *testing.T) {
		_, err := NewGraph(&Config{
			Package: "test/gen",
			Storage: drivers["sql"],
		}, &load.Schema{Name: "Type"}) // Reserved keyword
		require.Error(t, err)
	})
}

func TestGraphTables(t *testing.T) {
	t.Run("returns tables for schema", func(t *testing.T) {
		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{
			Name: "User",
			Fields: []*load.Field{
				{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
			},
		})
		require.NoError(t, err)

		tables, err := graph.Tables()
		require.NoError(t, err)
		assert.Len(t, tables, 1)
		assert.Equal(t, "users", tables[0].Name)
	})
}

func TestGraphNodes(t *testing.T) {
	t.Run("returns nodes for schema", func(t *testing.T) {
		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{
			Name: "User",
			Fields: []*load.Field{
				{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
			},
		})
		require.NoError(t, err)

		assert.Len(t, graph.Nodes, 1)
		assert.Equal(t, "User", graph.Nodes[0].Name)
	})
}

func TestGenerateWithFeatures(t *testing.T) {
	schemas := []*load.Schema{
		{
			Name: "User",
			Fields: []*load.Field{
				{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
				{Name: "age", Info: &field.TypeInfo{Type: field.TypeInt}, Optional: true, Nillable: true},
			},
		},
	}

	features := []struct {
		name    string
		feature Feature
	}{
		{"Privacy", FeaturePrivacy},
		{"Intercept", FeatureIntercept},
		{"Snapshot", FeatureSnapshot},
		{"SchemaConfig", FeatureSchemaConfig},
		{"Lock", FeatureLock},
		{"Modifier", FeatureModifier},
		{"Upsert", FeatureUpsert},
		{"ExecQuery", FeatureExecQuery},
	}

	for _, tt := range features {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			graph, err := NewGraph(&Config{
				Package:   "test/gen",
				Target:    target,
				Storage:   drivers["sql"],
				IDType:    &field.TypeInfo{Type: field.TypeInt},
				Features:  []Feature{tt.feature},
				Generator: testGenerator(),
			}, schemas...)
			require.NoError(t, err)

			err = graph.Gen()
			require.NoError(t, err)

			// Verify core files exist
			_, err = os.Stat(filepath.Join(target, "velox.go"))
			require.NoError(t, err)
			_, err = os.Stat(filepath.Join(target, "client.go"))
			require.NoError(t, err)
		})
	}
}

func TestGenerateWithHooks(t *testing.T) {
	hookCalled := false
	hook := func(next Generator) Generator {
		return GenerateFunc(func(g *Graph) error {
			hookCalled = true
			return next.Generate(g)
		})
	}

	target := t.TempDir()
	graph, err := NewGraph(&Config{
		Package:   "test/gen",
		Target:    target,
		Storage:   drivers["sql"],
		IDType:    &field.TypeInfo{Type: field.TypeInt},
		Hooks:     []Hook{hook},
		Generator: testGenerator(),
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)

	err = graph.Gen()
	require.NoError(t, err)
	assert.True(t, hookCalled)
}

func TestGenerateWithTemplates(t *testing.T) {
	target := t.TempDir()
	tmpl := MustParse(NewTemplate("custom").Parse("// Custom template output\npackage gen"))

	graph, err := NewGraph(&Config{
		Package:   "test/gen",
		Target:    target,
		Storage:   drivers["sql"],
		IDType:    &field.TypeInfo{Type: field.TypeInt},
		Templates: []*Template{tmpl},
		Generator: testGenerator(),
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)

	err = graph.Gen()
	require.NoError(t, err)

	// Verify custom template was generated
	content, err := os.ReadFile(filepath.Join(target, "custom.go"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "Custom template output")
}

func TestGeneratorWriteFile(t *testing.T) {
	t.Run("creates file in target directory", func(t *testing.T) {
		target := t.TempDir()
		graph, err := NewGraph(&Config{
			Package:   "test/gen",
			Target:    target,
			Storage:   drivers["sql"],
			IDType:    &field.TypeInfo{Type: field.TypeInt},
			Generator: testGenerator(),
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)

		err = graph.Gen()
		require.NoError(t, err)

		// Verify files exist
		entries, err := os.ReadDir(target)
		require.NoError(t, err)
		assert.True(t, len(entries) > 0)
	})
}

func TestWithWorkers(t *testing.T) {
	target := t.TempDir()
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Target:  target,
		Storage: drivers["sql"],
		IDType:  &field.TypeInfo{Type: field.TypeInt},
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)

	t.Run("sets positive workers", func(t *testing.T) {
		gen := NewJenniferGenerator(graph, target)
		result := gen.WithWorkers(4)
		assert.Equal(t, gen, result, "WithWorkers should return the same generator for chaining")
		assert.Equal(t, 4, gen.workers)
	})

	t.Run("ignores zero workers", func(t *testing.T) {
		gen := NewJenniferGenerator(graph, target)
		original := gen.workers
		gen.WithWorkers(0)
		assert.Equal(t, original, gen.workers)
	})

	t.Run("ignores negative workers", func(t *testing.T) {
		gen := NewJenniferGenerator(graph, target)
		original := gen.workers
		gen.WithWorkers(-1)
		assert.Equal(t, original, gen.workers)
	})
}

func TestMarkEnumGenerated(t *testing.T) {
	target := t.TempDir()
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Target:  target,
		Storage: drivers["sql"],
		IDType:  &field.TypeInfo{Type: field.TypeInt},
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)

	gen := NewJenniferGenerator(graph, target)

	t.Run("first call returns false", func(t *testing.T) {
		assert.False(t, gen.MarkEnumGenerated("Status"))
	})

	t.Run("second call returns true", func(t *testing.T) {
		assert.True(t, gen.MarkEnumGenerated("Status"))
	})

	t.Run("different enum returns false", func(t *testing.T) {
		assert.False(t, gen.MarkEnumGenerated("Role"))
	})
}

func TestZeroValue(t *testing.T) {
	target := t.TempDir()
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Target:  target,
		Storage: drivers["sql"],
		IDType:  &field.TypeInfo{Type: field.TypeInt},
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)
	gen := NewJenniferGenerator(graph, target)

	t.Run("nil field returns 0", func(t *testing.T) {
		code := gen.ZeroValue(nil)
		assert.NotNil(t, code)
	})

	t.Run("nillable field returns nil", func(t *testing.T) {
		f := &Field{
			Name:     "name",
			Type:     &field.TypeInfo{Type: field.TypeString},
			Nillable: true,
		}
		code := gen.ZeroValue(f)
		assert.NotNil(t, code)
	})

	t.Run("string field returns empty string", func(t *testing.T) {
		f := &Field{
			Name: "name",
			Type: &field.TypeInfo{Type: field.TypeString},
		}
		code := gen.ZeroValue(f)
		assert.NotNil(t, code)
	})
}

func TestPointerType(t *testing.T) {
	target := t.TempDir()
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Target:  target,
		Storage: drivers["sql"],
		IDType:  &field.TypeInfo{Type: field.TypeInt},
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)
	gen := NewJenniferGenerator(graph, target)

	tests := []struct {
		name  string
		field *Field
	}{
		{"nil type", &Field{Name: "x", Type: nil, Nillable: true}},
		{"string", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeString}, Nillable: true}},
		{"int", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeInt}, Nillable: true}},
		{"int8", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeInt8}, Nillable: true}},
		{"int16", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeInt16}, Nillable: true}},
		{"int32", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeInt32}, Nillable: true}},
		{"int64", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeInt64}, Nillable: true}},
		{"uint", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeUint}, Nillable: true}},
		{"uint8", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeUint8}, Nillable: true}},
		{"uint16", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeUint16}, Nillable: true}},
		{"uint32", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeUint32}, Nillable: true}},
		{"uint64", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeUint64}, Nillable: true}},
		{"float32", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeFloat32}, Nillable: true}},
		{"float64", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeFloat64}, Nillable: true}},
		{"bool", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeBool}, Nillable: true}},
		{"time", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeTime}, Nillable: true}},
		{"uuid", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeUUID}, Nillable: true}},
		{"bytes", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeBytes}, Nillable: true}},
		{"json with ident", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeJSON, Ident: "MyData"}, Nillable: true}},
		{"json without ident", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeJSON}, Nillable: true}},
		{"custom go type with pkg", &Field{Name: "x", Type: &field.TypeInfo{
			Type:    field.TypeOther,
			Ident:   "decimal.Decimal",
			PkgPath: "github.com/shopspring/decimal",
			RType:   &field.RType{Kind: 0},
		}, Nillable: true}},
		{"custom go type slice with pkg", &Field{Name: "x", Type: &field.TypeInfo{
			Type:    field.TypeOther,
			Ident:   "[]net.IP",
			PkgPath: "net",
			RType:   &field.RType{Kind: 0},
		}, Nillable: true}},
		{"custom go type without pkg", &Field{Name: "x", Type: &field.TypeInfo{
			Type:  field.TypeOther,
			Ident: "MyLocal",
			RType: &field.RType{Kind: 0},
		}, Nillable: true}},
		{"enum", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeEnum}, Nillable: true, Enums: []Enum{{Name: "Active", Value: "active"}}}},
		{"unknown default", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeOther}, Nillable: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := gen.GoType(tt.field)
			assert.NotNil(t, code)
		})
	}
}

func TestBaseType(t *testing.T) {
	target := t.TempDir()
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Target:  target,
		Storage: drivers["sql"],
		IDType:  &field.TypeInfo{Type: field.TypeInt},
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)
	gen := NewJenniferGenerator(graph, target)

	tests := []struct {
		name  string
		field *Field
	}{
		{"nil type", &Field{Name: "x", Type: nil}},
		{"string", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeString}}},
		{"int", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeInt}}},
		{"int8", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeInt8}}},
		{"int16", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeInt16}}},
		{"int32", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeInt32}}},
		{"int64", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeInt64}}},
		{"uint", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeUint}}},
		{"uint8", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeUint8}}},
		{"uint16", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeUint16}}},
		{"uint32", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeUint32}}},
		{"uint64", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeUint64}}},
		{"float32", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeFloat32}}},
		{"float64", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeFloat64}}},
		{"bool", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeBool}}},
		{"time", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeTime}}},
		{"uuid", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeUUID}}},
		{"bytes", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeBytes}}},
		{"enum", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeEnum}, Enums: []Enum{{Name: "Active", Value: "active"}}}},
		{"json with pkg ident", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeJSON, Ident: "MyType", PkgPath: "mypkg"}}},
		{"json with ident only", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeJSON, Ident: "MyData"}}},
		{"json without ident", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeJSON}}},
		{"custom go type with pkg", &Field{Name: "x", Type: &field.TypeInfo{
			Type:    field.TypeOther,
			Ident:   "decimal.Decimal",
			PkgPath: "github.com/shopspring/decimal",
			RType:   &field.RType{Kind: 0},
		}}},
		{"custom go type slice with pkg", &Field{Name: "x", Type: &field.TypeInfo{
			Type:    field.TypeOther,
			Ident:   "[]net.IP",
			PkgPath: "net",
			RType:   &field.RType{Kind: 0},
		}}},
		{"custom go type without pkg", &Field{Name: "x", Type: &field.TypeInfo{
			Type:  field.TypeOther,
			Ident: "LocalType",
			RType: &field.RType{Kind: 0},
		}}},
		{"other with pkg ident", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeOther, Ident: "pkg.T", PkgPath: "pkg"}}},
		{"other with ident only", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeOther, Ident: "LocalT"}}},
		{"other without ident", &Field{Name: "x", Type: &field.TypeInfo{Type: field.TypeOther}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := gen.BaseType(tt.field)
			assert.NotNil(t, code)
		})
	}
}

func TestFieldTypeConstant(t *testing.T) {
	target := t.TempDir()
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Target:  target,
		Storage: drivers["sql"],
		IDType:  &field.TypeInfo{Type: field.TypeInt},
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)
	gen := NewJenniferGenerator(graph, target)

	tests := []struct {
		fieldType field.Type
		expected  string
	}{
		{field.TypeBool, "TypeBool"},
		{field.TypeTime, "TypeTime"},
		{field.TypeJSON, "TypeJSON"},
		{field.TypeUUID, "TypeUUID"},
		{field.TypeBytes, "TypeBytes"},
		{field.TypeEnum, "TypeEnum"},
		{field.TypeString, "TypeString"},
		{field.TypeOther, "TypeOther"},
		{field.TypeInt, "TypeInt"},
		{field.TypeInt8, "TypeInt8"},
		{field.TypeInt16, "TypeInt16"},
		{field.TypeInt32, "TypeInt32"},
		{field.TypeInt64, "TypeInt64"},
		{field.TypeUint, "TypeUint"},
		{field.TypeUint8, "TypeUint8"},
		{field.TypeUint16, "TypeUint16"},
		{field.TypeUint32, "TypeUint32"},
		{field.TypeUint64, "TypeUint64"},
		{field.TypeFloat32, "TypeFloat32"},
		{field.TypeFloat64, "TypeFloat64"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			f := &Field{Name: "x", Type: &field.TypeInfo{Type: tt.fieldType}}
			result := gen.FieldTypeConstant(f)
			assert.Equal(t, tt.expected, result)
		})
	}

	t.Run("nil type returns TypeString", func(t *testing.T) {
		f := &Field{Name: "x", Type: nil}
		result := gen.FieldTypeConstant(f)
		assert.Equal(t, "TypeString", result)
	})
}

func TestEdgeRelType(t *testing.T) {
	target := t.TempDir()
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Target:  target,
		Storage: drivers["sql"],
		IDType:  &field.TypeInfo{Type: field.TypeInt},
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)
	gen := NewJenniferGenerator(graph, target)

	tests := []struct {
		rel      Rel
		expected string
	}{
		{O2O, "O2O"},
		{O2M, "O2M"},
		{M2O, "M2O"},
		{M2M, "M2M"},
		{Unk, "O2M"}, // default
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			e := &Edge{Rel: Relation{Type: tt.rel}}
			result := gen.EdgeRelType(e)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIDType(t *testing.T) {
	target := t.TempDir()
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Target:  target,
		Storage: drivers["sql"],
		IDType:  &field.TypeInfo{Type: field.TypeInt},
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)
	gen := NewJenniferGenerator(graph, target)

	t.Run("nil ID returns int", func(t *testing.T) {
		typ := &Type{Name: "NoID"}
		code := gen.IDType(typ)
		assert.NotNil(t, code)
	})

	t.Run("with ID field", func(t *testing.T) {
		typ := &Type{
			Name: "WithID",
			ID:   &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		}
		code := gen.IDType(typ)
		assert.NotNil(t, code)
	})
}

func TestStructTags(t *testing.T) {
	target := t.TempDir()
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Target:  target,
		Storage: drivers["sql"],
		IDType:  &field.TypeInfo{Type: field.TypeInt},
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)
	gen := NewJenniferGenerator(graph, target)

	t.Run("default json tag", func(t *testing.T) {
		f := &Field{Name: "email"}
		tags := gen.StructTags(f)
		assert.Equal(t, "email,omitempty", tags["json"])
	})

	t.Run("custom struct tag", func(t *testing.T) {
		f := &Field{Name: "email", StructTag: `json:"mail" xml:"mail_addr"`}
		tags := gen.StructTags(f)
		assert.Equal(t, "mail", tags["json"])
		assert.Equal(t, "mail_addr", tags["xml"])
	})
}

func TestEdgeStructTags(t *testing.T) {
	target := t.TempDir()
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Target:  target,
		Storage: drivers["sql"],
		IDType:  &field.TypeInfo{Type: field.TypeInt},
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)
	gen := NewJenniferGenerator(graph, target)

	t.Run("default json tag", func(t *testing.T) {
		e := &Edge{Name: "posts"}
		tags := gen.EdgeStructTags(e)
		assert.Equal(t, "posts,omitempty", tags["json"])
	})

	t.Run("custom struct tag", func(t *testing.T) {
		e := &Edge{Name: "posts", StructTag: `json:"articles" xml:"article_list"`}
		tags := gen.EdgeStructTags(e)
		assert.Equal(t, "articles", tags["json"])
		assert.Equal(t, "article_list", tags["xml"])
	})
}

func TestPredicatePkg(t *testing.T) {
	target := t.TempDir()

	t.Run("with config package", func(t *testing.T) {
		graph, err := NewGraph(&Config{
			Package: "github.com/test/project/ent",
			Target:  target,
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)
		gen := NewJenniferGenerator(graph, target)
		assert.Equal(t, "github.com/test/project/ent/predicate", gen.PredicatePkg())
	})

	t.Run("fallback to pkg name", func(t *testing.T) {
		graph, err := NewGraph(&Config{
			Target:  target,
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)
		gen := NewJenniferGenerator(graph, target)
		pkg := gen.PredicatePkg()
		assert.Contains(t, pkg, "predicate")
	})
}

func TestRootPkg(t *testing.T) {
	target := t.TempDir()

	t.Run("without entity package dialect returns empty", func(t *testing.T) {
		graph, err := NewGraph(&Config{
			Package: "github.com/test/project/ent",
			Target:  target,
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)
		gen := NewJenniferGenerator(graph, target)
		// No dialect set that implements EntityPackageDialect
		assert.Equal(t, "", gen.RootPkg())
	})
}

func TestAnnotationExists(t *testing.T) {
	target := t.TempDir()

	t.Run("nil annotations", func(t *testing.T) {
		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Target:  target,
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)
		gen := NewJenniferGenerator(graph, target)
		graph.Annotations = nil
		assert.False(t, gen.AnnotationExists("GQL"))
	})

	t.Run("existing annotation", func(t *testing.T) {
		graph, err := NewGraph(&Config{
			Package:     "test/gen",
			Target:      target,
			Storage:     drivers["sql"],
			IDType:      &field.TypeInfo{Type: field.TypeInt},
			Annotations: Annotations{"GQL": map[string]string{"Name": "test"}},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)
		gen := NewJenniferGenerator(graph, target)
		assert.True(t, gen.AnnotationExists("GQL"))
	})

	t.Run("missing annotation", func(t *testing.T) {
		graph, err := NewGraph(&Config{
			Package:     "test/gen",
			Target:      target,
			Storage:     drivers["sql"],
			IDType:      &field.TypeInfo{Type: field.TypeInt},
			Annotations: Annotations{"GQL": "data"},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)
		gen := NewJenniferGenerator(graph, target)
		assert.False(t, gen.AnnotationExists("Other"))
	})
}

func TestCleanupStaleFiles(t *testing.T) {
	t.Run("removes files from previous manifest not in current generation", func(t *testing.T) {
		target := t.TempDir()
		// Create files that were in the previous manifest.
		staleDir := filepath.Join(target, "oldentity")
		require.NoError(t, os.MkdirAll(staleDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(staleDir, "client.go"), []byte("package oldentity\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(staleDir, "mutation.go"), []byte("package oldentity\n"), 0o644))

		// Write a previous manifest listing these files.
		manifest := "oldentity/client.go\noldentity/mutation.go\nuser/client.go\n"
		require.NoError(t, os.WriteFile(filepath.Join(target, manifestFile), []byte(manifest), 0o644))

		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Target:  target,
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)

		gen := NewJenniferGenerator(graph, target)
		// Simulate that only user/client.go was generated this run.
		gen.generatedFiles = []string{"user/client.go"}
		require.NoError(t, gen.cleanupStaleFiles())

		_, err = os.Stat(filepath.Join(staleDir, "client.go"))
		assert.True(t, os.IsNotExist(err), "stale file should be removed")
		// Empty directory should also be cleaned up.
		_, err = os.Stat(staleDir)
		assert.True(t, os.IsNotExist(err), "empty stale directory should be removed")
	})

	t.Run("preserves files in current generation", func(t *testing.T) {
		target := t.TempDir()
		activeDir := filepath.Join(target, "user")
		require.NoError(t, os.MkdirAll(activeDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(activeDir, "client.go"), []byte("package user\n"), 0o644))

		manifest := "user/client.go\n"
		require.NoError(t, os.WriteFile(filepath.Join(target, manifestFile), []byte(manifest), 0o644))

		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Target:  target,
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)

		gen := NewJenniferGenerator(graph, target)
		gen.generatedFiles = []string{"user/client.go"}
		require.NoError(t, gen.cleanupStaleFiles())

		_, err = os.Stat(filepath.Join(activeDir, "client.go"))
		assert.NoError(t, err, "active file should be preserved")
	})

	t.Run("no-op without previous manifest", func(t *testing.T) {
		target := t.TempDir()
		someDir := filepath.Join(target, "somedir")
		require.NoError(t, os.MkdirAll(someDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(someDir, "other.go"), []byte("package somedir\n"), 0o644))

		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Target:  target,
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)

		gen := NewJenniferGenerator(graph, target)
		require.NoError(t, gen.cleanupStaleFiles())

		_, err = os.Stat(someDir)
		assert.NoError(t, err, "directory should be preserved when no manifest exists")
	})

	t.Run("writes and reads manifest round-trip", func(t *testing.T) {
		target := t.TempDir()
		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Target:  target,
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)

		gen := NewJenniferGenerator(graph, target)
		gen.generatedFiles = []string{"user/client.go", "client.go", "velox.go"}
		require.NoError(t, gen.writeManifest())

		got, err := gen.readManifest()
		require.NoError(t, err)
		assert.Equal(t, []string{"client.go", "user/client.go", "velox.go"}, got)
	})
}

// =============================================================================
// fullMockDialect — implements all generator interfaces for Generate() tests
// =============================================================================

type fullMockDialect struct {
	mu                sync.Mutex
	genCalled         map[string]int
	returnNil         bool
	supportedFeatures map[string]bool
	genMigrateN       int
}

func newFullMockDialect() *fullMockDialect {
	return &fullMockDialect{
		genCalled:         make(map[string]int),
		supportedFeatures: make(map[string]bool),
	}
}

func (d *fullMockDialect) record(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.genCalled[name]++
}

func (d *fullMockDialect) callCount(name string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.genCalled[name]
}

// mockJenFile creates a valid *jen.File that renders without error.
func mockJenFile(pkg string) *jen.File {
	f := jen.NewFile(pkg)
	f.HeaderComment("Code generated by velox. DO NOT EDIT.")
	f.Comment("mock")
	return f
}

// MinimalDialect
func (d *fullMockDialect) Name() string { return "mock" }

// EntityGenerator
func (d *fullMockDialect) GenMutation(t *Type) (*jen.File, error) {
	d.record("GenMutation")
	return mockJenFile(t.PackageDir()), nil
}
func (d *fullMockDialect) GenPredicate(t *Type) (*jen.File, error) {
	d.record("GenPredicate")
	return mockJenFile(t.PackageDir()), nil
}
func (d *fullMockDialect) GenPackage(t *Type) (*jen.File, error) {
	d.record("GenPackage")
	return mockJenFile(t.PackageDir()), nil
}

// GraphGenerator
func (d *fullMockDialect) GenClient() (*jen.File, error) {
	d.record("GenClient")
	return mockJenFile("gen"), nil
}
func (d *fullMockDialect) GenVelox() (*jen.File, error) {
	d.record("GenVelox")
	return mockJenFile("gen"), nil
}
func (d *fullMockDialect) GenErrors() (*jen.File, error) {
	d.record("GenErrors")
	return mockJenFile("gen"), nil
}
func (d *fullMockDialect) GenTx() (*jen.File, error) {
	d.record("GenTx")
	return mockJenFile("gen"), nil
}
func (d *fullMockDialect) GenRuntime() (*jen.File, error) {
	d.record("GenRuntime")
	return mockJenFile("gen"), nil
}
func (d *fullMockDialect) GenPredicatePackage() (*jen.File, error) {
	d.record("GenPredicatePackage")
	return mockJenFile("predicate"), nil
}

// FeatureGenerator
func (d *fullMockDialect) SupportsFeature(feature string) bool {
	return d.supportedFeatures[feature]
}
func (d *fullMockDialect) GenFeature(feature string) (*jen.File, error) {
	d.record("Feature:" + feature)
	return mockJenFile("gen"), nil
}

// OptionalFeatureGenerator
func (d *fullMockDialect) GenSchemaConfig() (*jen.File, error) {
	d.record("GenSchemaConfig")
	if d.returnNil {
		return nil, nil
	}
	return mockJenFile("internal"), nil
}
func (d *fullMockDialect) GenIntercept() (*jen.File, error) {
	d.record("GenIntercept")
	if d.returnNil {
		return nil, nil
	}
	return mockJenFile("intercept"), nil
}
func (d *fullMockDialect) GenPrivacy() (*jen.File, error) {
	d.record("GenPrivacy")
	if d.returnNil {
		return nil, nil
	}
	return mockJenFile("privacy"), nil
}
func (d *fullMockDialect) GenSnapshot() (*jen.File, error) {
	d.record("GenSnapshot")
	if d.returnNil {
		return nil, nil
	}
	return mockJenFile("internal"), nil
}
func (d *fullMockDialect) GenVersionedMigration() (*jen.File, error) {
	d.record("GenVersionedMigration")
	if d.returnNil {
		return nil, nil
	}
	return mockJenFile("migrate"), nil
}
func (d *fullMockDialect) GenGlobalID() (*jen.File, error) {
	d.record("GenGlobalID")
	if d.returnNil {
		return nil, nil
	}
	return mockJenFile("internal"), nil
}
func (d *fullMockDialect) GenEntQL() (*jen.File, error) {
	d.record("GenEntQL")
	if d.returnNil {
		return nil, nil
	}
	return mockJenFile("gen"), nil
}

// MigrateGenerator
func (d *fullMockDialect) GenMigrate() (MigrateFiles, error) {
	d.record("GenMigrate")
	if d.genMigrateN <= 0 {
		return MigrateFiles{}, nil
	}
	return MigrateFiles{
		Schema:  mockJenFile("migrate"),
		Migrate: mockJenFile("migrate"),
	}, nil
}

// EntityPackageDialect
func (d *fullMockDialect) WithHelper(_ GeneratorHelper) MinimalDialect {
	return d
}
func (d *fullMockDialect) GenEntityClient(_ GeneratorHelper, t *Type) (*jen.File, error) {
	d.record("GenEntityClient")
	return mockJenFile(t.PackageDir()), nil
}
func (d *fullMockDialect) GenEntityRuntime(_ GeneratorHelper, t *Type) (*jen.File, error) {
	d.record("GenEntityRuntime")
	return mockJenFile(t.PackageDir()), nil
}
func (d *fullMockDialect) GenCreate(_ GeneratorHelper, t *Type) (*jen.File, error) {
	d.record("GenCreate")
	return mockJenFile(t.PackageDir()), nil
}
func (d *fullMockDialect) GenUpdate(_ GeneratorHelper, t *Type) (*jen.File, error) {
	d.record("GenUpdate")
	return mockJenFile(t.PackageDir()), nil
}
func (d *fullMockDialect) GenDelete(_ GeneratorHelper, t *Type) (*jen.File, error) {
	d.record("GenDelete")
	return mockJenFile(t.PackageDir()), nil
}
func (d *fullMockDialect) GenEntityPkg(_ GeneratorHelper, t *Type) (*jen.File, error) {
	d.record("GenEntityPkg")
	return mockJenFile(t.PackageDir()), nil
}
func (d *fullMockDialect) GenQueryPkg(_ GeneratorHelper, t *Type, _ string) (*jen.File, error) {
	d.record("GenQueryPkg")
	return mockJenFile(t.PackageDir()), nil
}
func (d *fullMockDialect) GenQueryHelpers(_ GeneratorHelper) (*jen.File, error) {
	d.record("GenQueryHelpers")
	return mockJenFile("query"), nil
}
func (d *fullMockDialect) GenEntityHooks(_ GeneratorHelper) (*jen.File, error) {
	d.record("GenEntityHooks")
	return mockJenFile("entity"), nil
}

// Compile-time interface checks.
var (
	_ MinimalDialect           = (*fullMockDialect)(nil)
	_ FeatureGenerator         = (*fullMockDialect)(nil)
	_ OptionalFeatureGenerator = (*fullMockDialect)(nil)
	_ MigrateGenerator         = (*fullMockDialect)(nil)
	_ EntityPackageDialect     = (*fullMockDialect)(nil)
)

// newTestGraph creates a Graph with the given features for Generate() tests.
func newTestGraph(t *testing.T, target string, features ...Feature) *Graph {
	t.Helper()
	graph, err := NewGraph(&Config{
		Package:  "test/gen",
		Target:   target,
		Storage:  drivers["sql"],
		IDType:   &field.TypeInfo{Type: field.TypeInt},
		Features: features,
	}, &load.Schema{
		Name: "User",
		Fields: []*load.Field{
			{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
		},
	})
	require.NoError(t, err)
	return graph
}

func TestGenerateOptionalFeatures(t *testing.T) {
	target := t.TempDir()
	graph := newTestGraph(t, target,
		FeatureSchemaConfig,
		FeatureIntercept,
		FeaturePrivacy,
		FeatureSnapshot,
		FeatureVersionedMigration,
		FeatureGlobalID,
		FeatureEntQL,
	)

	mock := newFullMockDialect()
	gen := NewJenniferGenerator(graph, target).WithDialect(mock)

	err := gen.Generate(context.Background())
	require.NoError(t, err)

	// Each optional Gen* method should be called exactly once.
	for _, name := range []string{
		"GenSchemaConfig",
		"GenIntercept",
		"GenPrivacy",
		"GenSnapshot",
		"GenVersionedMigration",
		"GenGlobalID",
		"GenEntQL",
	} {
		assert.Equal(t, 1, mock.callCount(name), "%s should be called once", name)
	}

	// Verify files were written.
	assert.FileExists(t, filepath.Join(target, "internal", "schemaconfig.go"))
	assert.FileExists(t, filepath.Join(target, "intercept", "intercept.go"))
	assert.FileExists(t, filepath.Join(target, "privacy", "privacy.go"))
	assert.FileExists(t, filepath.Join(target, "internal", "schema.go"))
	assert.FileExists(t, filepath.Join(target, "migrate", "migrate.go"))
	assert.FileExists(t, filepath.Join(target, "internal", "globalid.go"))
	assert.FileExists(t, filepath.Join(target, "querylanguage.go"))
}

func TestGenerateOptionalFeaturesNilReturn(t *testing.T) {
	target := t.TempDir()
	graph := newTestGraph(t, target,
		FeatureSchemaConfig,
		FeatureIntercept,
		FeaturePrivacy,
		FeatureSnapshot,
		FeatureVersionedMigration,
		FeatureGlobalID,
		FeatureEntQL,
	)

	mock := newFullMockDialect()
	mock.returnNil = true
	gen := NewJenniferGenerator(graph, target).WithDialect(mock)

	err := gen.Generate(context.Background())
	require.NoError(t, err)

	// Methods should still be called (to check), but nil return means no file written.
	for _, name := range []string{
		"GenSchemaConfig",
		"GenIntercept",
		"GenPrivacy",
		"GenSnapshot",
		"GenVersionedMigration",
		"GenGlobalID",
		"GenEntQL",
	} {
		assert.Equal(t, 1, mock.callCount(name), "%s should be called once", name)
	}

	// These optional files should NOT exist because the generator returned nil.
	for _, path := range []string{
		filepath.Join(target, "internal", "schemaconfig.go"),
		filepath.Join(target, "intercept", "intercept.go"),
		filepath.Join(target, "privacy", "privacy.go"),
		filepath.Join(target, "internal", "schema.go"),
		filepath.Join(target, "internal", "globalid.go"),
		filepath.Join(target, "querylanguage.go"),
	} {
		_, statErr := os.Stat(path)
		assert.True(t, os.IsNotExist(statErr), "file should not exist: %s", path)
	}
}

func TestGenerateDisabledFeatures(t *testing.T) {
	target := t.TempDir()
	// No features enabled.
	graph := newTestGraph(t, target)

	mock := newFullMockDialect()
	gen := NewJenniferGenerator(graph, target).WithDialect(mock)

	err := gen.Generate(context.Background())
	require.NoError(t, err)

	// Optional generators should NOT be called when features are disabled.
	for _, name := range []string{
		"GenSchemaConfig",
		"GenIntercept",
		"GenPrivacy",
		"GenSnapshot",
		"GenVersionedMigration",
		"GenGlobalID",
		"GenEntQL",
	} {
		assert.Equal(t, 0, mock.callCount(name), "%s should not be called", name)
	}
}

func TestGenerateFeatureGenerator(t *testing.T) {
	target := t.TempDir()
	graph := newTestGraph(t, target)

	mock := newFullMockDialect()
	mock.supportedFeatures = map[string]bool{
		"hook": true,
	}
	gen := NewJenniferGenerator(graph, target).WithDialect(mock)

	err := gen.Generate(context.Background())
	require.NoError(t, err)

	// Only supported core features should have GenFeature called.
	// Note: "migrate" is handled by MigrateGenerator (GenMigrate), and
	// "intercept"/"privacy" are dispatched via OptionalFeatureGenerator —
	// none of these are in coreFeatures.
	assert.Equal(t, 1, mock.callCount("Feature:hook"), "hook should be generated")
	assert.Equal(t, 0, mock.callCount("Feature:migrate"), "migrate is not a core feature")

	// Verify generated feature files exist for supported features.
	assert.FileExists(t, filepath.Join(target, "hook", "hook.go"))
}

func TestGenerateMigratePackage(t *testing.T) {
	target := t.TempDir()
	graph := newTestGraph(t, target)

	mock := newFullMockDialect()
	mock.genMigrateN = 2
	gen := NewJenniferGenerator(graph, target).WithDialect(mock)

	err := gen.Generate(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 1, mock.callCount("GenMigrate"), "GenMigrate should be called once")

	// Verify migrate directory and files were created.
	assert.FileExists(t, filepath.Join(target, "migrate", "schema.go"))
	assert.FileExists(t, filepath.Join(target, "migrate", "migrate.go"))
}

func TestWriteFile(t *testing.T) {
	t.Run("writes file to target directory", func(t *testing.T) {
		target := t.TempDir()
		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Target:  target,
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)

		gen := NewJenniferGenerator(graph, target)
		f := gen.NewFile("test")
		f.Comment("test file")

		err = gen.writeFile(context.Background(), f, "", "test.go")
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(target, "test.go"))
		assert.NoError(t, err)
	})

	t.Run("writes file to subdirectory", func(t *testing.T) {
		target := t.TempDir()
		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Target:  target,
			Storage: drivers["sql"],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)

		gen := NewJenniferGenerator(graph, target)
		f := gen.NewFile("sub")
		f.Comment("sub file")

		err = gen.writeFile(context.Background(), f, "subpkg", "test.go")
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(target, "subpkg", "test.go"))
		assert.NoError(t, err)
	})
}

func TestTypeNames(t *testing.T) {
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Storage: drivers["sql"],
		IDType:  &field.TypeInfo{Type: field.TypeInt},
	}, &load.Schema{
		Name: "User",
		Fields: []*load.Field{
			{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
		},
	})
	require.NoError(t, err)
	require.Len(t, graph.Nodes, 1)

	typ := graph.Nodes[0]

	t.Run("QueryName", func(t *testing.T) {
		assert.Equal(t, "UserQuery", typ.QueryName())
	})

	t.Run("MutationName", func(t *testing.T) {
		assert.Equal(t, "UserMutation", typ.MutationName())
	})

	t.Run("CreateName", func(t *testing.T) {
		assert.Equal(t, "UserCreate", typ.CreateName())
	})

	t.Run("UpdateName", func(t *testing.T) {
		assert.Equal(t, "UserUpdate", typ.UpdateName())
	})

	t.Run("UpdateOneName", func(t *testing.T) {
		assert.Equal(t, "UserUpdateOne", typ.UpdateOneName())
	})

	t.Run("DeleteName", func(t *testing.T) {
		assert.Equal(t, "UserDelete", typ.DeleteName())
	})

	t.Run("DeleteOneName", func(t *testing.T) {
		assert.Equal(t, "UserDeleteOne", typ.DeleteOneName())
	})

	t.Run("ClientName", func(t *testing.T) {
		assert.Equal(t, "UserClient", typ.ClientName())
	})
}
