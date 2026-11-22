package edge_test

import (
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/internal/desc"
)

type User struct{ schema.Schema }
type Post struct{ schema.Schema }

func TestTo(t *testing.T) {
	d := edge.To("posts", Post.Type).Required().StorageKey(edge.Column("author_id"), edge.Symbol("posts_author")).Comment("c").Descriptor()
	if len(d.Errs) != 0 || d.Name != "posts" || d.Type != "Post" || d.TypePkg != "github.com/syssam/velox/schema/edge_test" ||
		d.Inverse || !d.Required || d.Columns[0] != "author_id" || d.Symbols[0] != "posts_author" || d.Comment != "c" {
		t.Fatalf("%+v", d)
	}
}

func TestFrom(t *testing.T) {
	d := edge.From("author", User.Type).Ref("posts").Unique().Field("author_id").Descriptor()
	if len(d.Errs) != 0 || !d.Inverse || d.RefName != "posts" || !d.Unique || d.Field != "author_id" || d.Type != "User" {
		t.Fatalf("%+v", d)
	}
}

func TestInlineInverse(t *testing.T) {
	d := edge.To("children", User.Type).From("parent").Unique().Descriptor()
	if len(d.Errs) != 0 || d.Name != "children" || d.Ref == nil || d.Ref.Name != "parent" || !d.Ref.Unique || !d.Ref.Inverse || d.Ref.Type != "User" {
		t.Fatalf("%+v ref %+v", d, d.Ref)
	}
}

func TestBadType(t *testing.T) {
	for _, v := range []any{nil, "User", func() {}, func(int) {}} {
		if d := edge.To("x", v).Descriptor(); len(d.Errs) == 0 {
			t.Errorf("edge.To with %T: no error", v)
		}
		if d := edge.From("x", v).Descriptor(); len(d.Errs) == 0 {
			t.Errorf("edge.From with %T: no error", v)
		}
	}
}

func TestM2MStorage(t *testing.T) {
	d := edge.To("groups", User.Type).StorageKey(edge.Table("memberships"), edge.Columns("user_id", "group_id"), edge.Symbols("fk_user", "fk_group")).Descriptor()
	if d.Table != "memberships" || len(d.Columns) != 2 || d.Columns[1] != "group_id" || len(d.Symbols) != 2 || d.Symbols[1] != "fk_group" {
		t.Fatalf("%+v", d)
	}
}

func TestPos(t *testing.T) {
	_, file, line, _ := runtime.Caller(0)
	to := edge.To("a", Post.Type)
	from := edge.From("b", User.Type)
	inline := edge.To("c", User.Type).From("d")
	if want := fmt.Sprintf("%s:%d", file, line+1); desc.PosOf(to.Descriptor().PC) != want {
		t.Errorf("To: %q, want %q", desc.PosOf(to.Descriptor().PC), want)
	}
	if want := fmt.Sprintf("%s:%d", file, line+2); desc.PosOf(from.Descriptor().PC) != want {
		t.Errorf("From: %q, want %q", desc.PosOf(from.Descriptor().PC), want)
	}
	if d, want := inline.Descriptor(), fmt.Sprintf("%s:%d", file, line+3); desc.PosOf(d.PC) != want || desc.PosOf(d.Ref.PC) != want {
		t.Errorf("inline: %q %q, want %q", desc.PosOf(d.PC), desc.PosOf(d.Ref.PC), want)
	}
}

// Each exported builder lists its options in the package's documentation.
func TestBuildersAreDocumented(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var parsed []*ast.File
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, f)
	}
	pkg, err := doc.NewFromFiles(fset, parsed, "github.com/syssam/velox/schema/edge")
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string][]string{}
	for _, typ := range pkg.Types {
		for _, m := range typ.Methods {
			methods[typ.Name] = append(methods[typ.Name], m.Name)
		}
	}
	for _, b := range []string{"AssocBuilder", "InverseBuilder"} {
		got, ok := methods[b]
		if !ok {
			t.Errorf("%s is not documented", b)
			continue
		}
		for _, m := range []string{"Unique", "Required", "Field", "StorageKey"} {
			if !slices.Contains(got, m) {
				t.Errorf("%s: the doc lacks %s; it lists %v", b, m, got)
			}
		}
	}
}

func TestOnDelete(t *testing.T) {
	d := edge.To("posts", Post.Type).OnDelete(edge.Cascade).Descriptor()
	if len(d.Errs) != 0 || d.OnDelete != "CASCADE" {
		t.Fatalf("assoc %+v", d)
	}
	d = edge.From("author", User.Type).Ref("posts").OnDelete(edge.SetNull).Descriptor()
	if len(d.Errs) != 0 || d.OnDelete != "SET NULL" {
		t.Fatalf("inverse %+v", d)
	}
	d = edge.To("children", User.Type).From("parent").OnDelete(edge.Restrict).Descriptor()
	if d.OnDelete != "" || d.Ref == nil || d.Ref.OnDelete != "RESTRICT" {
		t.Fatalf("inline: the inverse's side is set, not the assoc's: %+v", d)
	}
	d = edge.To("posts", Post.Type).OnDelete(edge.NoAction).Descriptor()
	if len(d.Errs) != 0 || d.OnDelete != "NO ACTION" {
		t.Fatalf("NoAction %+v", d)
	}
	for _, d := range []*desc.Edge{
		edge.To("x", Post.Type).OnDelete("SET DEFAULT").Descriptor(),
		edge.From("x", User.Type).OnDelete("").Descriptor(),
	} {
		if len(d.Errs) != 1 || d.OnDelete != "" {
			t.Errorf("an action outside the four is a builder error: %+v", d)
		}
	}
}

// TestBuildersSetTheirFields covers each builder's setters, which velox-gen reads.
func TestBuildersSetTheirFields(t *testing.T) {
	type T = schema.Schema
	to := edge.To("a", T.Type).Unique().Immutable().Field("a_id").Comment("c").Annotations(schema.Comment("x")).Descriptor()
	if !to.Unique || !to.Immutable || to.Field != "a_id" || to.Comment != "c" || len(to.Annotations) != 1 {
		t.Errorf("To: %+v", to)
	}
	from := edge.From("b", T.Type).Ref("a").Required().Immutable().StorageKey(edge.Column("b_id")).Comment("c").Annotations(schema.Comment("x")).Descriptor()
	if !from.Required || !from.Immutable || len(from.Columns) != 1 || from.Columns[0] != "b_id" || from.Comment != "c" || len(from.Annotations) != 1 {
		t.Errorf("From: %+v", from)
	}
}

type Group struct{ schema.Schema }
type Membership struct{ schema.Schema }

func TestThrough(t *testing.T) {
	to := edge.To("groups", User.Type).Through("memberships", Membership.Type).Descriptor()
	if len(to.Errs) != 0 || to.Through == nil || *to.Through != (desc.Through{Name: "memberships", Type: "Membership", TypePkg: "github.com/syssam/velox/schema/edge_test"}) {
		t.Fatalf("To: %+v through %+v", to, to.Through)
	}
	from := edge.From("users", User.Type).Ref("groups").Through("memberships", Membership.Type).Descriptor()
	if len(from.Errs) != 0 || from.Through == nil || from.Through.Name != "memberships" || from.Through.Type != "Membership" {
		t.Fatalf("From: %+v through %+v", from, from.Through)
	}
	inline := edge.To("following", User.Type).Through("follows", Membership.Type).From("followers").Through("follows", Membership.Type).Descriptor()
	if inline.Through == nil || inline.Ref == nil || inline.Ref.Through == nil || inline.Ref.Through.Name != "follows" {
		t.Fatalf("inline: the inverse's Through is the inverse's: %+v ref %+v", inline, inline.Ref)
	}
	if d := edge.To("groups", User.Type).Descriptor(); d.Through != nil {
		t.Fatalf("an edge without Through has none: %+v", d.Through)
	}
}

func TestThroughMisuse(t *testing.T) {
	for _, d := range []*desc.Edge{
		edge.To("groups", User.Type).Through("", Membership.Type).Descriptor(),
		edge.From("users", User.Type).Through("", Membership.Type).Descriptor(),
	} {
		if len(d.Errs) != 1 || d.Errs[0].Error() != "Through needs the name of the edge to the edge schema" {
			t.Errorf("empty name: %v", d.Errs)
		}
	}
	for _, v := range []any{nil, "Membership", func() {}} {
		for _, d := range []*desc.Edge{
			edge.To("groups", User.Type).Through("memberships", v).Descriptor(),
			edge.From("users", User.Type).Through("memberships", v).Descriptor(),
		} {
			if len(d.Errs) != 1 || d.Errs[0].Error() != "the type of Through must be a schema's Type method expression, such as Membership.Type" || d.Through != nil {
				t.Errorf("Through with %T: %v %+v", v, d.Errs, d.Through)
			}
		}
	}
}
