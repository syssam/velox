package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox/internal/tempmod"
)

// internalOK follows Go's rule: a package below an element internal is
// importable only from the tree rooted at the element's parent.
func TestInternalOK(t *testing.T) {
	cases := []struct {
		importer, p string
		want        bool
	}{
		{"example.com/app/gen", "time", true},
		{"example.com/app/gen", "github.com/google/uuid", true},
		{"example.com/app/gen", "example.com/app/internal/ids", true},
		{"example.com/app/gen/entity", "example.com/app/internal/ids", true},
		{"example.com/app", "example.com/app/internal", true},
		{"example.com/app/gen", "example.com/app/schema/internal/clock", false},
		{"example.com/app/gen", "example.com/other/internal/ids", false},
		{"example.com/appx/gen", "example.com/app/internal/ids", false},
		{"example.com/app/gen", "example.com/app/internal/a/internal/b", false},
		{"example.com/app/gen", "internal/abi", false},
	}
	for _, c := range cases {
		if got := internalOK(c.importer, c.p); got != c.want {
			t.Errorf("internalOK(%q, %q) = %v, want %v", c.importer, c.p, got, c.want)
		}
	}
}

// The schema of TestFunctionDefaults: a default of each kind velox-gen tells
// apart, and two validators.
const funcsSchema = `package schema

import (
	"errors"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"

	"example.com/tmp/ids"
	"example.com/tmp/schema/internal/clock"
)

type Doc struct{ schema.Schema }

// Mixin adds created_at and updated_at, whose defaults are time.Now.
func (Doc) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Time{}} }

func (Doc) Fields() []schema.Field {
	return []schema.Field{
		field.String("code").DefaultFunc(ids.New),
		field.String("slug").DefaultFunc(NewSlug),
		field.String("token").DefaultFunc(func() string { return "tok" }),
		field.String("stamp").DefaultFunc(clock.Stamp),
		field.String("name").Validate(ids.Check).Validate(func(s string) error {
			if s == "bad" {
				return errors.New("bad name")
			}
			return nil
		}),
	}
}

// NewSlug is the schema package's own.
func NewSlug() string { return "slug" }
`

const funcsTest = `package gen_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"

	"example.com/tmp/gen"
)

func TestDefaults(t *testing.T) {
	ctx := context.Background()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	defer raw.Close()
	if _, err := raw.Exec("CREATE TABLE docs (id INTEGER PRIMARY KEY, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, code TEXT NOT NULL, slug TEXT NOT NULL, token TEXT NOT NULL, stamp TEXT NOT NULL, name TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, raw)
	d := gen.Doc{Name: "ok"}
	if err := db.Insert(&d).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if d.CreatedAt.IsZero() || d.UpdatedAt.IsZero() || d.Code != "id-1" || d.Slug != "slug" || d.Token != "tok" || d.Stamp != "stamp" {
		t.Errorf("defaults: %+v", d)
	}
	for name, want := range map[string]string{"bad": "bad name", "x": "too short"} {
		err := db.Insert(&gen.Doc{Name: name}).Exec(ctx)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Name %q: %v, want an error with %q", name, err, want)
		}
	}
}
`

// A default or validator that is a named, exported function of a package
// gen can import is called by its name, in the entities' package; the
// schema package's own is called from gen in split mode, and in the entity's
// file in plain mode. A closure, and a function of a package gen cannot
// import, is read from the field's descriptor, by the field's name. All of
// them run, in both modes.
func TestFunctionDefaults(t *testing.T) {
	for _, split := range []bool{false, true} {
		name := "plain"
		if split {
			name = "split"
		}
		t.Run(name, func(t *testing.T) {
			dir := module(t, map[string]string{
				"schema/doc.go": funcsSchema,
				"ids/ids.go": `package ids

import "errors"

func New() string { return "id-1" }

func Check(s string) error {
	if len(s) < 2 {
		return errors.New("too short")
	}
	return nil
}
`,
				"schema/internal/clock/clock.go": "package clock\n\nfunc Stamp() string { return \"stamp\" }\n",
			})
			m, err := Load(dir, "./schema", offline())
			if err != nil {
				t.Fatal(err)
			}
			g, err := Build(m)
			if err != nil {
				t.Fatal(err)
			}
			cfg := Config{Package: "gen", GenPath: "example.com/tmp/gen", Split: split}
			if cfg.Funcs, err = FuncPackages(dir, g, cfg, offline()); err != nil {
				t.Fatal(err)
			}
			if p := cfg.Funcs["example.com/tmp/schema/internal/clock"]; p.Entity {
				t.Errorf("an internal package of the schema is importable from gen: %+v", p)
			}
			files, err := Generate(g, cfg)
			if err != nil {
				t.Fatal(err)
			}
			src := map[string]string{}
			for _, f := range files {
				p := filepath.Join(dir, "gen", f.Name)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, f.Data, 0o644); err != nil {
					t.Fatal(err)
				}
				src[f.Name] = string(f.Data)
			}
			entity, facade := src["doc_velox.go"], src["velox.go"]
			want := []string{"return time.Now() }", "return ids.New() }", `schema.FieldDescriptor(&sch.Doc{}, "token")`,
				`schema.FieldDescriptor(&sch.Doc{}, "stamp")`, "ids.Check(x)", "return sch.NewSlug() }"}
			if split {
				entity = src["entity/doc_velox.go"]
				want = []string{"return time.Now() }", "return ids.New() }", "rt.DocTokenDefault()", "rt.DocStampDefault()",
					"ids.Check(x)", "rt.DocNameValidator2(x)", "rt.DocSlugDefault()"}
				for _, w := range []string{"sch.NewSlug,", `docToken := schema.FieldDescriptor(&sch.Doc{}, "token")`,
					"docToken.DefaultFunc.(func() string)", "docName.Validators[1].(func(string) error)"} {
					if !strings.Contains(facade, w) {
						t.Errorf("gen/velox.go has no %s:\n%s", w, facade)
					}
				}
			}
			for _, w := range want {
				if !strings.Contains(entity, w) {
					t.Errorf("the entity's file has no %s:\n%s", w, entity)
				}
			}
			for name, s := range src {
				if strings.Contains(s, "Mixin()[") || strings.Contains(s, "Fields()[") {
					t.Errorf("%s reads a field by its index:\n%s", name, s)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "gen", "main_test.go"), []byte(funcsTest), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := tempmod.Go(dir, "mod", "tidy"); err != nil {
				t.Fatal(err)
			}
			if out, err := tempmod.Go(dir, "vet", "./..."); err != nil {
				t.Fatalf("go vet:\n%v", out)
			}
			if out, err := tempmod.Go(dir, "test", "-count=1", "./gen"); err != nil {
				t.Fatalf("go test:\n%v", out)
			}
		})
	}
}
