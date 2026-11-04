package codegen

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func methodsDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// SchemaMethods finds an entity's Hooks and Policy on the schema package's
// type of its name, with a value or a pointer receiver: package runtime calls
// them on a new *E.
func TestSchemaMethodsFindsMethods(t *testing.T) {
	dir := methodsDir(t, map[string]string{
		"user_policy.go": "package schema\n\nfunc (User) Policy() velox.Policy[gen.User] { return velox.Policy[gen.User]{} }\n",
		"post.go":        "package schema\n\nfunc (p *Post) Hooks() []velox.Hook[gen.Post] { return nil }\n\nfunc (p Post) Other() {}\n",
		"both.go":        "package schema\n\nfunc (t Tag) Policy() velox.Policy[gen.Tag] { return velox.Policy[gen.Tag]{} }\nfunc (t Tag) Hooks() []vx.Hook[g.Tag] { return nil }\n",
		"other.go":       "package schema\n\nfunc (Comment) Policies() {}\nfunc Policy() {}\n",
	})
	got, err := SchemaMethods(dir, "", []string{"User", "Post", "Tag", "Comment"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Bound{"User": {Policy: true}, "Post": {Hooks: true, HooksPtr: true}, "Tag": {Policy: true, Hooks: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A Hooks or Policy method of another shape than package entity binds is
// refused at its line, as is one on a type that is no entity's.
func TestSchemaMethodsRefusesAWrongShape(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"policy result", "package schema\n\nfunc (User) Policy() int { return 0 }\n", "User.Policy must take nothing and return velox.Policy[entity.User] (func (User) Policy() velox.Policy[entity.User])"},
		{"hooks result", "package schema\n\nfunc (u User) Hooks() velox.Hook[gen.User] { return nil }\n", "User.Hooks must take nothing and return []velox.Hook[entity.User] (func (User) Hooks() []velox.Hook[entity.User])"},
		{"another entity", "package schema\n\nfunc (User) Hooks() []velox.Hook[gen.Post] { return nil }\n", "User.Hooks must take nothing and return []velox.Hook[entity.User] (func (User) Hooks() []velox.Hook[entity.User])"},
		{"unqualified", "package schema\n\nfunc (User) Policy() velox.Policy[User] { return velox.Policy[User]{} }\n", "User.Policy must take nothing and return velox.Policy[entity.User] (func (User) Policy() velox.Policy[entity.User])"},
		{"a parameter", "package schema\n\nfunc (User) Hooks(int) []velox.Hook[gen.User] { return nil }\n", "User.Hooks must take nothing and return []velox.Hook[entity.User] (func (User) Hooks() []velox.Hook[entity.User])"},
		{"an array", "package schema\n\nfunc (User) Hooks() [2]velox.Hook[gen.User] { return nil }\n", "User.Hooks must take nothing and return []velox.Hook[entity.User] (func (User) Hooks() []velox.Hook[entity.User])"},
		{"not an entity", "package schema\n\nfunc (Mixin) Hooks() []velox.Hook[gen.User] { return nil }\n", "Mixin.Hooks: Mixin is not an entity; Hooks and Policy are declared on an entity's schema, or on a type named like a join entity"},
	} {
		dir := methodsDir(t, map[string]string{"mine.go": c.src})
		_, err := SchemaMethods(dir, "", []string{"User", "Post"})
		var fe *FileError
		if !errors.As(err, &fe) || fe.Path != filepath.Join(dir, "mine.go") || fe.Line != 3 || fe.Reason != c.want {
			t.Errorf("%s: err = %v; want a FileError at mine.go:3: %s", c.name, err, c.want)
		}
	}
}

// The generated package's own files may not declare an entity's Hooks or
// Policy: velox-gen binds them from the schema package, and says so.
func TestCheckUserFilesRefusesBehaviour(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"policy", "package gen\n\nfunc (*User) Policy() velox.Policy[User] { return velox.Policy[User]{} }\n", "declares User.Policy in the generated package; declare it in the schema package, on User: func (User) Policy() velox.Policy[entity.User]"},
		{"hooks", "package gen\n\nfunc (u User) Hooks() []velox.Hook[User] { return nil }\n", "declares User.Hooks in the generated package; declare it in the schema package, on User: func (User) Hooks() []velox.Hook[entity.User]"},
	} {
		dir := methodsDir(t, map[string]string{"mine.go": c.src})
		err := CheckUserFiles(dir, []string{"User"}, nil)
		var fe *FileError
		if !errors.As(err, &fe) || fe.Path != filepath.Join(dir, "mine.go") || fe.Line != 3 || fe.Reason != c.want {
			t.Errorf("%s: err = %v; want a FileError at mine.go:3: %s", c.name, err, c.want)
		}
	}
	// Another type's Hooks, and an entity's other methods, are the user's.
	dir := methodsDir(t, map[string]string{"mine.go": "package gen\n\nfunc (*Stranger) Policy() {}\nfunc (*User) Label() string { return \"\" }\n"})
	if err := CheckUserFiles(dir, []string{"User"}, nil); err != nil {
		t.Error(err)
	}
}

func TestSchemaMethodsIgnoresTestAndGeneratedFiles(t *testing.T) {
	dir := methodsDir(t, map[string]string{
		"user_test.go":  "package schema\n\nfunc (User) Policy() int { return 0 }\n",
		"user_velox.go": header + "package schema\n\nfunc (User) Hooks() {}\n",
		"notes.txt":     "func (User) Policy()",
	})
	if err := os.Mkdir(filepath.Join(dir, "dir.go"), 0o755); err != nil { // a directory is not a file
		t.Fatal(err)
	}
	got, err := SchemaMethods(dir, "", []string{"User", "Post"})
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want none", got, err)
	}
}

func TestCheckUserFilesRefusesARedeclaredType(t *testing.T) {
	for name, src := range map[string]string{
		"struct":  "package gen\n\ntype User struct{}\n",
		"grouped": "package gen\n\ntype (\n\tOther int\n\tUser  string\n)\n",
		"alias":   "package gen\n\ntype User = int\n",
	} {
		dir := methodsDir(t, map[string]string{"mine.go": src})
		err := CheckUserFiles(dir, []string{"User"}, nil)
		if err == nil || !strings.Contains(err.Error(), "mine.go") || !strings.Contains(err.Error(), "type User") {
			t.Errorf("%s: err = %v; want one naming mine.go and type User", name, err)
		}
	}
}

func TestMissingDirIsNoFiles(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent")
	if err := CheckUserFiles(absent, []string{"User"}, nil); err != nil {
		t.Error(err)
	}
	got, err := SchemaMethods(absent, "", []string{"User"})
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestReportsASyntaxError(t *testing.T) {
	dir := methodsDir(t, map[string]string{"bad.go": "package gen\n\nfunc (\n"})
	if err := CheckUserFiles(dir, []string{"User"}, nil); err == nil || !strings.Contains(err.Error(), "bad.go") {
		t.Errorf("CheckUserFiles: err = %v", err)
	}
	if _, err := SchemaMethods(dir, "", []string{"User"}); err == nil || !strings.Contains(err.Error(), "bad.go") {
		t.Errorf("SchemaMethods: err = %v", err)
	}
}

// HasPackageDoc looks only at the user's files: not at generated files, test
// files, files that no system builds, or a file whose comment is
// not the package's or holds only directives.
func TestHasPackageDoc(t *testing.T) {
	for _, c := range []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"none", nil, false},
		{"a user's file", map[string]string{"doc.go": "// Package gen is mine.\npackage gen\n"}, true},
		{"undocumented user's file", map[string]string{"mine.go": "package gen\n\n// X is mine.\nvar X int\n"}, false},
		{"detached comment", map[string]string{"mine.go": "// Not the package's.\n\npackage gen\n"}, false},
		{"generated file", map[string]string{"velox.go": "// Code generated by velox-gen. DO NOT EDIT.\n\n// Package gen is generated.\npackage gen\n"}, false},
		{"test file", map[string]string{"doc_test.go": "// Package gen is a test's.\npackage gen\n"}, false},
		{"directive only", map[string]string{"generate.go": "//go:generate go tool velox-gen ../schema\npackage gen\n"}, false},
		{"build ignore", map[string]string{"doc.go": "//go:build ignore\n\n// Package gen is ignored.\npackage gen\n"}, false},
		{"build ignore and a tag", map[string]string{"doc.go": "//go:build ignore && tools\n\n// Package gen is ignored.\npackage gen\n"}, false},
		{"build constraint every build meets", map[string]string{"doc.go": "//go:build go1.1 || tools\n\n// Package gen is mine.\npackage gen\n"}, true},
		{"directive and doc", map[string]string{"doc.go": "// Package gen is mine.\n//\n//go:generate go tool velox-gen ../schema\npackage gen\n"}, true},
	} {
		got, err := HasPackageDoc(methodsDir(t, c.files))
		if err != nil || got != c.want {
			t.Errorf("%s: got %v, %v; want %v", c.name, got, err, c.want)
		}
	}
	if got, err := HasPackageDoc(filepath.Join(t.TempDir(), "missing")); err != nil || got {
		t.Errorf("missing dir: got %v, %v", got, err)
	}
}

// A file go build leaves out is not in the package: its methods do not
// bind, and its types do not clash with the generated ones.
func TestSchemaMethodsSkipsIgnoredFiles(t *testing.T) {
	const policy = "package schema\n\nfunc (User) Policy() velox.Policy[gen.User] { return velox.Policy[gen.User]{} }\n"
	for name, files := range map[string]map[string]string{
		"go:build ignore":   {"policy.go": "//go:build ignore\n\n" + policy},
		"never satisfied":   {"policy.go": "//go:build linux && !linux\n\n" + policy},
		"+build ignore":     {"policy.go": "// +build ignore\n\n" + policy},
		"underscore prefix": {"_policy.go": policy},
		"dot prefix":        {".policy.go": policy},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := SchemaMethods(methodsDir(t, files), "", []string{"User"})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 {
				t.Errorf("got %v, want none", got)
			}
		})
	}
	// Nor does a type clash with the generated ones.
	if err := CheckUserFiles(methodsDir(t, map[string]string{"types.go": "//go:build ignore\n\npackage gen\n\ntype User struct{}\n"}), []string{"User"}, nil); err != nil {
		t.Error(err)
	}
}

// A file only some systems build is refused, at the line that says so,
// whatever system velox-gen runs on: generated on one and built on another,
// the code would bind a method the package there does not have.
func TestSchemaMethodsRefusesSystemFiles(t *testing.T) {
	const policy = "package schema\n\nfunc (User) Policy() velox.Policy[gen.User] { return velox.Policy[gen.User]{} }\n"
	for _, c := range []struct {
		name, file, src string
		line            int
	}{
		{"name", "policy_linux.go", policy, 0},
		{"constraint", "policy.go", "//go:build darwin\n\n" + policy, 1},
		{"not a system", "policy.go", "// Policies.\n\n//go:build !windows\n\n" + policy, 3},
		// A custom tag is some builds' too: a build with -tags prod has the
		// policy, so the generated code must not leave it unbound.
		{"custom tag", "policy.go", "//go:build prod\n\n" + policy, 1},
		{"tools", "policy.go", "//go:build tools\n\n" + policy, 1},
		{"goexperiment", "policy.go", "//go:build goexperiment.jsonv2\n\n" + policy, 1},
		{"architecture level", "policy.go", "//go:build amd64.v3\n\n" + policy, 1},
		{"go version above the module's", "policy.go", "//go:build go1.28\n\n" + policy, 1},
		{"cgo", "policy.go", "package schema\n\nimport \"C\"\n\nfunc (User) Policy() velox.Policy[gen.User] { return velox.Policy[gen.User]{} }\n", 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := methodsDir(t, map[string]string{c.file: c.src})
			_, err := SchemaMethods(dir, "", []string{"User"})
			var fe *FileError
			if !errors.As(err, &fe) || fe.Path != filepath.Join(dir, c.file) || fe.Line != c.line ||
				!strings.Contains(fe.Reason, "velox-gen reads hooks and policies only from files that every build includes") {
				t.Fatalf("got %v", err)
			}
		})
	}
	// An ordinary file, and one whose constraint every system meets, bind.
	dir := methodsDir(t, map[string]string{"go.mod": "module m\n\ngo 1.27.0\n", "policy.go": policy, "hooks.go": "//go:build go1.27 && (prod || !prod)\n\npackage schema\n\nfunc (User) Hooks() []velox.Hook[gen.User] { return nil }\n"})
	got, err := SchemaMethods(dir, "", []string{"User"})
	if err != nil || !reflect.DeepEqual(got, map[string]Bound{"User": {Policy: true, Hooks: true}}) {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestGoFileSystems(t *testing.T) {
	for _, c := range []struct {
		name, src string
		want      Systems
		line      int
	}{
		{"x.go", "package p\n", EverySystem, 0},
		{"linux.go", "package p\n", EverySystem, 0},
		{"x_unix.go", "package p\n", EverySystem, 0},
		{"x.go", "not go", EverySystem, 0},
		{"x.go", "//go:build go1.1\n\npackage p\n", EverySystem, 0},
		{"x.go", "//go:build linux || !linux\n\npackage p\n", EverySystem, 0},
		{"_x.go", "package p\n", NoSystem, 0},
		{".x.go", "package p\n", NoSystem, 0},
		{"x_test.go", "package p\n", NoSystem, 0},
		{"x.go", "//go:build ignore\n\npackage p\n", NoSystem, 1},
		{"x.go", "//go:build ignore && tools\n\npackage p\n", NoSystem, 1},
		{"x.go", "//go:build linux && !linux\n\npackage p\n", NoSystem, 1},
		{"x.go", "// +build ignore\n\npackage p\n", NoSystem, 1},
		{"x_linux.go", "package p\n", SomeSystems, 0},
		{"x_arm64.go", "package p\n", SomeSystems, 0},
		{"x_linux_arm64.go", "package p\n", SomeSystems, 0},
		{"x.go", "//go:build darwin\n\npackage p\n", SomeSystems, 1},
		{"x.go", "//go:build !windows\n\npackage p\n", SomeSystems, 1},
		{"x.go", "//go:build unix\n\npackage p\n", SomeSystems, 1},
		{"x.go", "//go:build cgo\n\npackage p\n", SomeSystems, 1},
		{"x.go", "// +build linux darwin\n\npackage p\n", SomeSystems, 1},
		// Every tag but ignore is free: a build may set it or not.
		{"x.go", "//go:build tools\n\npackage p\n", SomeSystems, 1},
		{"x.go", "//go:build prod\n\npackage p\n", SomeSystems, 1},
		{"x.go", "//go:build enterprise\n\npackage p\n", SomeSystems, 1},
		{"x.go", "//go:build !prod\n\npackage p\n", SomeSystems, 1},
		{"x.go", "//go:build goexperiment.jsonv2\n\npackage p\n", SomeSystems, 1},
		{"x.go", "//go:build amd64.v3\n\npackage p\n", SomeSystems, 1},
		{"x.go", "//go:build arm64.v8.1\n\npackage p\n", SomeSystems, 1},
		{"x.go", "//go:build go1.999\n\npackage p\n", SomeSystems, 1},
		{"x.go", "// +build prod\n\npackage p\n", SomeSystems, 1},
		{"x.go", "package p\n\nimport \"C\"\n", SomeSystems, 3},
		// A //go:build line in the package's doc counts; a // +build line
		// there does not, as for go/build.
		{"x.go", "// Doc.\n//go:build ignore\npackage p\n", NoSystem, 2},
		{"x.go", "// +build ignore\npackage p\n", EverySystem, 0},
		// Nor does one after a /* */ comment: go/build stops reading // +build
		// lines at the first line that is not a line comment.
		{"x.go", "/* License */\n\n// +build ignore\n\npackage p\n", EverySystem, 0},
		{"x.go", "// +build ignore\n/* License */\n\npackage p\n", EverySystem, 0},
		{"x.go", "// +build ignore\n\n/* License */\n\npackage p\n", NoSystem, 1},
		{"x.go", "/* License */\n\n//go:build ignore\n\npackage p\n", NoSystem, 3},
	} {
		if got, line := GoFileSystems(c.name, []byte(c.src), "1.27.0"); got != c.want || line != c.line {
			t.Errorf("GoFileSystems(%q, %q) = %d, %d; want %d, %d", c.name, c.src, got, line, c.want, c.line)
		}
	}
}

// A go1.N tag holds when the module's go version is go1.N or later, and is
// free above it, whatever toolchain velox-gen runs on.
func TestGoFileSystemsGoVersion(t *testing.T) {
	for _, c := range []struct {
		tag, goVersion string
		want           Systems
	}{
		{"go1.21", "1.27.0", EverySystem},
		{"go1.27", "1.27.0", EverySystem},
		{"go1.27", "1.27", EverySystem},
		{"go1.28", "1.27.0", SomeSystems},
		{"go1.28", "1.30", EverySystem},
		{"!go1.28", "1.27.0", SomeSystems},
		{"!go1.21", "1.27.0", NoSystem},
		{"go1.1", "", EverySystem},
		{"go1.999", "", SomeSystems},
		// Not release tags: free, so the file is refused, not skipped.
		{"go1.0", "1.27.0", SomeSystems},
		{"!go1.0", "1.27.0", SomeSystems},
		{"!go1.00", "1.27.0", SomeSystems},
		{"!go1.027", "1.27.0", SomeSystems},
	} {
		src := "//go:build " + c.tag + "\n\npackage p\n"
		if got, _ := GoFileSystems("x.go", []byte(src), c.goVersion); got != c.want {
			t.Errorf("%s at go %q: got %d, want %d", c.tag, c.goVersion, got, c.want)
		}
	}
}

func TestModuleGoVersion(t *testing.T) {
	dir := methodsDir(t, map[string]string{"go.mod": "module m\n\ngo 1.27.0\n\ntoolchain go1.28.1\n"})
	for _, d := range []string{dir, filepath.Join(dir, "gen", "missing")} {
		if got := ModuleGoVersion(d); got != "1.27.0" {
			t.Errorf("ModuleGoVersion(%s) = %q", d, got)
		}
	}
	if got := ModuleGoVersion(methodsDir(t, map[string]string{"go.mod": "module m\n"})); got != "" {
		t.Errorf("no go line: got %q", got)
	}
}

// SchemaMethods records the first Read or Write key, not nil, of the entity's
// Policy literal in its Policy method, where velox-gen can see it.
func TestSchemaMethodsFindsPolicyConds(t *testing.T) {
	dir := methodsDir(t, map[string]string{
		// Read is nil, and Other's literal is another entity's: the Write
		// key is the first that counts.
		"friend.go": "package schema\n\nimport \"github.com/syssam/velox\"\n\nfunc (UserFriend) Policy() velox.Policy[gen.UserFriend] {\n\t_ = velox.Policy[gen.Other]{Read: r}\n\treturn velox.Policy[gen.UserFriend]{\n\t\tInsert: ins,\n\t\tRead:   nil,\n\t\tWrite:  w,\n\t\tRead:   r,\n\t}\n}\n",
		"group.go":  "package schema\n\nimport vx \"github.com/syssam/velox\"\n\nfunc (*UserGroup) Policy() vx.Policy[g.UserGroup] {\n\treturn vx.Policy[g.UserGroup]{Insert: ins, Write: nil}\n}\n",
		// A positional literal is left to the runtime's check.
		"pair.go": "package schema\n\nimport \"github.com/syssam/velox\"\n\nfunc (UserPair) Policy() velox.Policy[gen.UserPair] {\n\treturn velox.Policy[gen.UserPair]{r, w, nil}\n}\n",
	})
	got, err := SchemaMethods(dir, "", []string{"UserFriend", "UserGroup", "UserPair"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Bound{
		"UserFriend": {Policy: true, PolicyConds: filepath.Join(dir, "friend.go") + ":10"},
		"UserGroup":  {Policy: true, PolicyPtr: true},
		"UserPair":   {Policy: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Generating the blog model, whose UserFriend is the join entity of a
// bidirectional edge, with a Policy that sets Write fails at its line.
func TestGenerateRefusesABidiPolicy(t *testing.T) {
	dir := methodsDir(t, map[string]string{
		"friend.go": "package schema\n\nimport \"github.com/syssam/velox\"\n\ntype UserFriend struct{}\n\nfunc (UserFriend) Policy() velox.Policy[gen.UserFriend] {\n\treturn velox.Policy[gen.UserFriend]{Write: w}\n}\n",
	})
	bound, err := SchemaMethods(dir, "", []string{"UserFriend"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Generate(goldenGraph(t, "blog"), Config{Package: "gen", Bound: bound, Split: true, GenPath: "m/gen"})
	var fe *FileError
	if !errors.As(err, &fe) || fe.Path != filepath.Join(dir, "friend.go") || fe.Line != 8 ||
		fe.Reason != "UserFriend.Policy sets Read or Write: the join entity of a bidirectional edge allows only Insert, as a condition would show or delete one row of a link" {
		t.Fatalf("err = %v; want a FileError at friend.go:8", err)
	}
}

// A name velox-gen generates for a scope, a key or an entity's scope, may not
// be declared by a file of the user's: the generated file would not compile.
func TestCheckUserFilesReservedName(t *testing.T) {
	reserved := []string{"OrgKey", "noteScope"}
	for name, src := range map[string]string{
		"var":    "package gen\n\nvar OrgKey = 1\n",
		"const":  "package gen\n\nconst (\n\tx = 1\n\tnoteScope = 2\n)\n",
		"type":   "package gen\n\ntype noteScope int\n",
		"func":   "package gen\n\nfunc OrgKey() {}\n",
		"method": "package gen\n\ntype T int\n\nfunc (T) OrgKey() {}\n", // not a package-level name: allowed
	} {
		err := CheckUserFiles(methodsDir(t, map[string]string{"mine.go": src}), []string{"User"}, reserved)
		var fe *FileError
		switch {
		case name == "method":
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
		case !errors.As(err, &fe) || !strings.Contains(fe.Reason, "a name velox-gen generates") ||
			!strings.Contains(fe.Reason, "OrgKey") && !strings.Contains(fe.Reason, "noteScope"):
			t.Errorf("%s: got %v, want a *FileError naming the identifier", name, err)
		}
	}
}
