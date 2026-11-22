package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestEdgesOf(t *testing.T) {
	const n = 20
	if got, want := edgesOf(0, n), []edgeSpec{
		{Name: "next", Target: 1},
		{Name: "links", Target: 5},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("edgesOf(0) = %+v, want %+v", got, want)
	}
	if got, want := edgesOf(5, n), []edgeSpec{
		{Name: "prev", Target: 4, Inverse: true, Ref: "next", Unique: true, Required: true, Field: "prev_id"},
		{Name: "next", Target: 6},
		{Name: "linked", Target: 0, Inverse: true, Ref: "links"},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("edgesOf(5) = %+v, want %+v", got, want)
	}
	if got, want := edgesOf(19, n), []edgeSpec{
		{Name: "prev", Target: 18, Inverse: true, Ref: "next", Unique: true, Required: true, Field: "prev_id"},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("edgesOf(19) = %+v, want %+v", got, want)
	}
	// The M2M pair exists only where i+5 < n: E10's partner E15 is past the end of 15 entities.
	if got := edgesOf(10, 15); len(got) != 2 || got[0].Name != "prev" || got[1].Name != "next" {
		t.Errorf("edgesOf(10, 15) = %+v, want prev and next only", got)
	}
}

func TestFieldLines(t *testing.T) {
	got := fieldLines(0, "Int64")
	want := []string{
		`field.String("name")`, `field.Int("count")`, `field.Bool("active")`, `field.Time("created_at")`,
		`field.String("note").Optional().Nillable()`, `field.Enum("status").Values("draft", "live", "gone")`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fieldLines(0) = %q, want %q", got, want)
	}
	if got := fieldLines(3, "Int"); len(got) != 7 || got[6] != `field.Int("prev_id")` {
		t.Errorf("fieldLines(3, Int) = %q", got)
	}
	if got := fieldLines(3, "Int64"); got[6] != `field.Int64("prev_id")` {
		t.Errorf("fieldLines(3, Int64) = %q", got)
	}
}

func TestSchemasParse(t *testing.T) {
	const n = 20
	for _, o := range orms {
		t.Run(o.Name, func(t *testing.T) {
			files := o.Schema(n)
			if len(files) != n {
				t.Fatalf("%d files, want %d", len(files), n)
			}
			var from, to int
			for i := range n {
				name := filepath.Join(o.SchemaDir, "e"+strconv.Itoa(i)+".go")
				src, ok := files[name]
				if !ok {
					t.Fatalf("no file %s", name)
				}
				if _, err := parser.ParseFile(token.NewFileSet(), name, src, parser.AllErrors); err != nil {
					t.Fatalf("%s does not parse: %v\n%s", name, err, src)
				}
				from += strings.Count(src, "edge.From(")
				to += strings.Count(src, "edge.To(")
				hasIndex := strings.Contains(src, `index.Fields("prev_id")`)
				switch {
				case o.dsl.IndexType == "" && strings.Contains(src, "index."):
					t.Errorf("%s: %s must write no index", o.Name, name)
				case o.dsl.IndexType != "" && (i > 0) != hasIndex:
					t.Errorf("%s: %s has index.Fields(prev_id) = %v, want %v", o.Name, name, hasIndex, i > 0)
				}
			}
			if from != 21 || to != 21 {
				t.Errorf("edge.From = %d, edge.To = %d, want 21 each", from, to)
			}
			if last := files[filepath.Join(o.SchemaDir, "e19.go")]; !strings.Contains(last, editMarker) {
				t.Errorf("E19 lacks the edit marker:\n%s", last)
			}
		})
	}
}

func TestServicesParse(t *testing.T) {
	got := serviceEntities(3, 100)
	if want := []int{10, 21, 32, 43, 54}; !reflect.DeepEqual(got, want) {
		t.Fatalf("serviceEntities(3, 100) = %v, want %v", got, want)
	}
	for _, o := range orms {
		t.Run(o.Name, func(t *testing.T) {
			src := o.Service(got)
			if _, err := parser.ParseFile(token.NewFileSet(), "main.go", src, parser.AllErrors); err != nil {
				t.Fatalf("does not parse: %v\n%s", err, src)
			}
			for _, e := range []string{"E10", "E21", "E32", "E43", "E54"} {
				if !strings.Contains(src, e) {
					t.Errorf("service does not name %s:\n%s", e, src)
				}
			}
		})
	}
}

func TestEditSchema(t *testing.T) {
	const n = 20
	for _, o := range orms {
		t.Run(o.Name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range o.Schema(n) {
				p := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := editSchema(dir, o, n); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(dir, o.SchemaDir, "e19.go")
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if c := strings.Count(string(b), editLine); c != 1 {
				t.Fatalf("%d copies of %s after the edit, want 1:\n%s", c, editLine, b)
			}
			if i, j := strings.Index(string(b), editMarker), strings.Index(string(b), editLine); i < 0 || j < i {
				t.Errorf("the edit line is not after the marker:\n%s", b)
			}
			if _, err := parser.ParseFile(token.NewFileSet(), p, b, parser.AllErrors); err != nil {
				t.Errorf("the edited file does not parse: %v", err)
			}
			// A file that has lost its marker cannot be edited.
			if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(b), editMarker, "")), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := editSchema(dir, o, n); err == nil {
				t.Error("editing a file without the marker must fail")
			}
		})
	}
}

func TestORMByName(t *testing.T) {
	for _, name := range []string{"velox", "veloxe", "ent"} {
		if o, err := ormByName(name); err != nil || o.Name != name {
			t.Errorf("ormByName(%q) = %q, %v", name, o.Name, err)
		}
	}
	if _, err := ormByName("gorm"); err == nil {
		t.Error("an unknown ORM must be an error")
	}
	for _, c := range []struct {
		name string
		argv []string
	}{
		{"velox", []string{"/t/velox-gen", "-out", "gen", "./schema"}},
		{"veloxe", []string{"/t/velox", "generate", "--target", "./velox", "./schema"}},
		{"ent", []string{"/t/ent", "generate", "./ent/schema"}},
	} {
		o, _ := ormByName(c.name)
		if got := o.Codegen("/t"); !reflect.DeepEqual(got, c.argv) {
			t.Errorf("%s: Codegen = %q, want %q", c.name, got, c.argv)
		}
	}
}

// TestSchemasEquivalent pins what the comparison rests on: after the DSLs'
// spellings are normalised, the three ORMs' schema files are the same, entity
// by entity. The index method is left out here; the test above checks that
// each ORM's writes index.Fields("prev_id").
func TestSchemasEquivalent(t *testing.T) {
	const n = 20
	importBlock := regexp.MustCompile(`(?s)import \(.*?\n\)\n`)
	indexMethod := regexp.MustCompile(`(?s)\nfunc \(E\d+\) Indexes\(\).*?\n}\n`)
	norm := func(o orm, src string) string {
		src = importBlock.ReplaceAllString(src, "")
		src = indexMethod.ReplaceAllString(src, "")
		src = strings.NewReplacer(o.dsl.SchemaType, "SCHEMA", o.dsl.FieldType, "FIELD", o.dsl.EdgeType, "EDGE",
			`field.`+o.dsl.FK+`("prev_id")`, `field.FK("prev_id")`).Replace(src)
		return src
	}
	ref := orms[0].Schema(n)
	for _, o := range orms[1:] {
		got := o.Schema(n)
		for i := range n {
			name := "e" + strconv.Itoa(i) + ".go"
			want := norm(orms[0], ref[filepath.Join(orms[0].SchemaDir, name)])
			if g := norm(o, got[filepath.Join(o.SchemaDir, name)]); g != want {
				t.Errorf("%s %s differs from velox's:\n%s\nvs\n%s", o.Name, name, g, want)
			}
		}
	}
}

// TestRuntimeSchemaIndexes checks that the runtime schema adds runtimeIndexes,
// spelled the same in every DSL, and that the build schema has none of them.
func TestRuntimeSchemaIndexes(t *testing.T) {
	indexMethod := regexp.MustCompile(`(?s)\nfunc \(E\d+\) Indexes\(\).*?\n}\n`)
	var ref map[string]string
	for _, o := range orms {
		rt, build := o.RuntimeSchema(), o.Schema(runtimeEntities)
		if len(rt) != runtimeEntities {
			t.Fatalf("%s: %d runtime files, want %d", o.Name, len(rt), runtimeEntities)
		}
		got := map[string]string{}
		for i := range runtimeEntities {
			name := filepath.Join(o.SchemaDir, "e"+strconv.Itoa(i)+".go")
			for _, x := range runtimeIndexes[i] {
				call := "index.Fields(" + x + ")"
				if !strings.Contains(rt[name], call) {
					t.Errorf("%s: runtime %s lacks %s:\n%s", o.Name, name, call, rt[name])
				}
				if strings.Contains(build[name], call) {
					t.Errorf("%s: build %s has %s", o.Name, name, call)
				}
			}
			if _, err := parser.ParseFile(token.NewFileSet(), name, rt[name], parser.AllErrors); err != nil {
				t.Fatalf("%s: runtime %s does not parse: %v", o.Name, name, err)
			}
			if len(runtimeIndexes[i]) == 0 && rt[name] != build[name] {
				t.Errorf("%s: runtime %s differs from the build's", o.Name, name)
			}
			got[strconv.Itoa(i)] = strings.ReplaceAll(indexMethod.FindString(rt[name]), o.dsl.IndexType, "INDEX")
		}
		if ref == nil {
			ref = got
			continue
		}
		for i, m := range got {
			if m != ref[i] {
				t.Errorf("%s: E%s's indexes differ from velox's:\n%s\nvs\n%s", o.Name, i, m, ref[i])
			}
		}
	}
}
