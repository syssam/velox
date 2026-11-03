package codegen

import (
	"bytes"
	"flag"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "write the golden want/ files")

// goldenCases are the cases of testdata/golden, with the Config each is
// generated with.
var goldenCases = []struct {
	name string
	cfg  Config
}{
	{"minimal", Config{Package: "gen"}},
	{"blog", Config{Package: "gen", Split: true, GenPath: "example.com/blog/gen", Bound: map[string]Bound{"Post": {Policy: true}}}},
	{"scoped", Config{Package: "gen", Split: true, GenPath: "example.com/scoped/gen", Bound: map[string]Bound{
		"Task": {Policy: true, Hooks: true}, "Memo": {Policy: true}, "Tag": {Hooks: true},
	}}},
}

// TestGolden loads each case's schema package, generates it and compares every
// file with want/. Run it with -update to write want/.
func TestGolden(t *testing.T) {
	for _, c := range goldenCases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join("testdata", "golden", c.name)
			got, err := Generate(goldenGraph(t, c.name), c.cfg)
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(dir, "want")
			if *update {
				if err := os.RemoveAll(want); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(want, 0o755); err != nil {
					t.Fatal(err)
				}
				for _, f := range got {
					p := filepath.Join(want, f.Name)
					if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, f.Data, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return
			}
			var names, wanted []string
			for _, f := range got {
				names = append(names, f.Name)
			}
			err = filepath.WalkDir(want, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				rel, err := filepath.Rel(want, p)
				wanted = append(wanted, filepath.ToSlash(rel))
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(names)
			slices.Sort(wanted)
			if !slices.Equal(names, wanted) {
				t.Fatalf("files %v, want %v", names, wanted)
			}
			for _, f := range got {
				w, err := os.ReadFile(filepath.Join(want, f.Name))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(f.Data, w) {
					t.Errorf("%s differs from want/%s at line %d", f.Name, f.Name, firstDiff(string(f.Data), string(w)))
				}
			}
		})
	}
}

// goldenGraph loads and builds the schema package of the golden case name.
func goldenGraph(t *testing.T, name string) *Graph {
	t.Helper()
	files := map[string]string{}
	matches, err := filepath.Glob(filepath.Join("testdata", "golden", name, "schema", "*.go"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no schema files: %v", err)
	}
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		// The first line is the //go:build ignore that keeps go vet ./...
		// from type-checking the fixture as a package of velox.
		_, src, _ := bytes.Cut(b, []byte("\n"))
		files["schema/"+filepath.Base(p)] = string(src)
	}
	m, err := Load(module(t, files), "./schema", offline())
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// The blog golden's bidirectional friends edge marks its join entity Bidi,
// and the users' groups do not mark theirs.
func TestGoldenBidi(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", "golden", "blog", "want", EntityDir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if !regexp.MustCompile(`\n\t\tBidi: +true,\n`).MatchString(read("user_friend_velox.go")) {
		t.Error("user_friend_velox.go's Descriptor has no Bidi: true")
	}
	if strings.Contains(read("user_group_velox.go"), "Bidi:") {
		t.Error("user_group_velox.go's Descriptor says Bidi")
	}
	// The users' spouse edge is a bidirectional one-to-one relation.
	if u := read("user_velox.go"); !strings.Contains(u, `velox.EdgeBidi[User]("Spouse", `) || strings.Contains(u, `EdgeTo[User]("Spouse"`) {
		t.Error(`user_velox.go's Descriptor does not relate Spouse with velox.EdgeBidi[User]`)
	}
}

// The blog golden's entity.go gives each enum Values, String and IsValid, all
// with the receiver e.
func TestGoldenEnumMethods(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "golden", "blog", "want", EntityDir, "entity.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := `// Values returns the values of PostStatus in declaration order.
func (e PostStatus) Values() []string { return []string{"draft", "published"} }

// String returns the stored value of e.
func (e PostStatus) String() string { return string(e) }

// IsValid reports whether e is one of the values of PostStatus.
func (e PostStatus) IsValid() bool {
	switch e {
	case PostStatusDraft, PostStatusPublished:
		return true
	}
	return false
}
`
	if !strings.Contains(string(b), want) {
		t.Errorf("entity.go lacks PostStatus's methods:\n%s", b)
	}
}

// firstDiff is the 1-based number of the first line where a and b differ.
func firstDiff(a, b string) int {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range min(len(x), len(y)) {
		if x[i] != y[i] {
			return i + 1
		}
	}
	return min(len(x), len(y)) + 1
}

// Generate gives the same bytes for the same Graph, and for a Graph built
// from the same schemas listed in another order.
func TestRenderDeterministic(t *testing.T) {
	cfg := Config{GenPath: "example.com/blog/gen", Bound: map[string]Bound{"User": {Policy: true, Hooks: true}}}
	var runs [][]File
	for _, g := range []*Graph{
		mustBuild(t, User{}, Post{}, Group{}),
		mustBuild(t, User{}, Post{}, Group{}),
		mustBuild(t, Group{}, Post{}, User{}),
		mustBuild(t, Post{}, Group{}, User{}),
	} {
		files, err := Generate(g, cfg)
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, files)
	}
	a := runs[0]
	if len(a) == 0 {
		t.Fatal("no files")
	}
	for r, b := range runs[1:] {
		if len(a) != len(b) {
			t.Fatalf("run %d: %d files, then %d", r+1, len(a), len(b))
		}
		for i := range a {
			if a[i].Name != b[i].Name || !bytes.Equal(a[i].Data, b[i].Data) {
				t.Fatalf("run %d: file %s differs", r+1, a[i].Name)
			}
		}
	}
}

// Every file starts with the header, is gofmt-clean, and the names are the
// labels of the entities, the join entity included, and velox.go.
func TestRenderFiles(t *testing.T) {
	files, err := Generate(mustBuild(t, User{}, Post{}, Group{}), Config{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
		if !bytes.HasPrefix(f.Data, []byte("// Code generated by velox-gen. DO NOT EDIT.\n\n")) {
			t.Errorf("%s has no header", f.Name)
		}
		if out, err := format.Source(f.Data); err != nil || !bytes.Equal(out, f.Data) {
			t.Errorf("%s is not gofmt-clean: %v", f.Name, err)
		}
	}
	want := []string{"group_velox.go", "post_velox.go", "user_group_velox.go", "user_velox.go", "velox.go"}
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Fatalf("files %v, want %v", names, want)
	}
}

// Generate reports what Lower does.
func TestGenerateLowerError(t *testing.T) {
	g := mustBuild(t, User{}, Post{}, Group{})
	g.Entities[0].Fields = append(g.Entities[0].Fields, &Field{GoName: "String", Column: "s", Field: g.Entities[0].Fields[0].Field})
	if _, err := Generate(g, Config{}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("got %v", err)
	}
}

func TestCamel(t *testing.T) {
	for in, want := range map[string]string{"user": "user", "user_group": "userGroup", "http_log": "httpLog", "log_id": "logID"} {
		if got := camel(in); got != want {
			t.Errorf("camel(%q) = %q, want %q", in, got, want)
		}
	}
}

// velox.go has the package comment unless a user's file has one, and each
// enum's const block has a doc.
func TestRenderPackageDoc(t *testing.T) {
	o := &Out{Package: "gen", SchemaPath: "example.com/m/schema",
		Enums: []*OutEnum{{Type: "Status", Consts: []string{"StatusDraft"}, Values: []string{"draft"}}}}
	doc := "// Package gen holds the entities of example.com/m/schema, generated by velox-gen.\npackage gen\n"
	consts := "// The values of Status.\nconst (\n"
	if src := renderPackage(o); !strings.Contains(src, doc) || !strings.Contains(src, consts) {
		t.Errorf("velox.go has no package comment or no const doc:\n%s", src)
	}
	if src := renderPackage(&Out{Package: "gen", SchemaPath: "example.com/m/schema", UserDoc: true}); strings.Contains(src, "// Package") {
		t.Errorf("velox.go has a package comment beside the user's:\n%s", src)
	}
	files, err := Generate(mustBuild(t, User{}, Post{}, Group{}), Config{UserDoc: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if bytes.Contains(f.Data, []byte("// Package")) {
			t.Errorf("%s has a package comment beside the user's", f.Name)
		}
	}
	// In split mode, gen's doc and entity's each give way to the user's own.
	for _, c := range []struct {
		cfg  Config
		want map[string]bool // file: has a package comment
	}{
		{Config{Split: true, GenPath: "m/gen"}, map[string]bool{"velox.go": true, "entity/entity.go": true}},
		{Config{Split: true, GenPath: "m/gen", UserDoc: true}, map[string]bool{"velox.go": false, "entity/entity.go": true}},
		{Config{Split: true, GenPath: "m/gen", EntityDoc: true}, map[string]bool{"velox.go": true, "entity/entity.go": false}},
	} {
		files, err := Generate(mustBuild(t, User{}, Post{}, Group{}), c.cfg)
		if err != nil {
			t.Fatal(err)
		}
		for name, want := range c.want {
			if got := bytes.Contains(fileNamed(t, files, name), []byte("// Package")); got != want {
				t.Errorf("%+v: %s has a package comment: %v, want %v", c.cfg, name, got, want)
			}
		}
	}
}

// TestRenderScopeFile: scope_velox.go holds each key once and each scoped
// entity's scope, and exists only when some entity is scoped.
func TestRenderScopeFile(t *testing.T) {
	files, err := Generate(goldenGraph(t, "scoped"), goldenCases[2].cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := string(fileNamed(t, files, "entity/scope_velox.go"))
	for _, want := range []string{
		`var OrgKey = scope.NewKey[int64]("org")`,
		`var RegionKey = scope.NewKey[string]("region")`,
		`noteScope = scope.By(OrgKey, Notes.OrgID.EQ, func(x *Note) *int64 { return &x.OrgID })`,
		`homeScope = scope.By(RegionKey, Homes.Region.EQ, func(x *Home) *string { return &x.Region })`,
		`"github.com/syssam/velox/scope"`,
		`"github.com/syssam/velox"`,
		"Code generated by velox-gen. DO NOT EDIT.",
		"GeneratedCodeIsVersion",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("scope_velox.go lacks %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "scope.NewKey") != 2 {
		t.Errorf("want two keys, org once and region once:\n%s", got)
	}
	if strings.Contains(got, "ownerScope") {
		t.Errorf("an unscoped entity has a scope variable:\n%s", got)
	}
	plain, err := Generate(goldenGraph(t, "minimal"), Config{Package: "gen"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range plain {
		if f.Name == "scope_velox.go" {
			t.Errorf("a model with no scoped entity has scope_velox.go")
		}
	}
}

// TestRenderScopedBinding: a scoped entity's descriptor wraps the user's Policy
// in the scope and puts the scope's Fill before the user's Hooks, for each
// combination of the methods the user declares; an unscoped entity's lines are
// as they were.
func TestRenderScopedBinding(t *testing.T) {
	files, err := Generate(goldenGraph(t, "scoped"), goldenCases[2].cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ file, want string }{
		{"note_velox.go", "velox.BindPolicy(noteScope.Policy())"},
		{"note_velox.go", "velox.BindHooks([]velox.Hook[Note]{noteScope.Fill()})"},
		{"task_velox.go", `policy := veloxPolicy[Task](rt.Task, "Task")`},
		{"task_velox.go", `hooks := veloxHooks[Task](rt.Task, "Task")`},
		{"task_velox.go", "velox.BindPolicy(taskScope.Wrap(policy()))"},
		{"task_velox.go", "velox.BindHooksAfter(taskScope.Fill(), hooks())"},
		{"memo_velox.go", "velox.BindPolicy(memoScope.Wrap(policy()))"},
		{"memo_velox.go", "velox.BindHooks([]velox.Hook[Memo]{memoScope.Fill()})"},
		{"tag_velox.go", "velox.BindPolicy(tagScope.Policy())"},
		{"tag_velox.go", "velox.BindHooksAfter(tagScope.Fill(), hooks())"},
	} {
		if got := string(fileNamed(t, files, EntityDir+"/"+c.file)); !strings.Contains(got, c.want) {
			t.Errorf("%s lacks %s", c.file, c.want)
		}
	}
	if got := string(fileNamed(t, files, EntityDir+"/owner_velox.go")); strings.Contains(got, "Scope") || strings.Contains(got, "Policy:") || strings.Contains(got, "Hooks:") {
		t.Errorf("an unscoped entity without methods binds something:\n%s", got)
	}
}

func fileNamed(t *testing.T, files []File, name string) []byte {
	t.Helper()
	for _, f := range files {
		if f.Name == name {
			return f.Data
		}
	}
	t.Fatalf("no file %s", name)
	return nil
}
