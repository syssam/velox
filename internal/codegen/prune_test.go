package codegen

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// A schema package with no Hooks or Policy is compiled as it is.
func TestPruneNoBehaviour(t *testing.T) {
	got, err := prune(map[string][]byte{"user.go": []byte("package schema\n\nimport \"example.com/gen\"\n\ntype User struct{}\n\nfunc helper() gen.T { return gen.T{} }\n")}, []string{"User"}, nil)
	if err != nil || got != nil {
		t.Fatalf("got %v, %v; want nil", got, err)
	}
}

// prune leaves out Hooks and Policy, what only they reach, and the imports
// only those use, in place: every line keeps its number. It keeps what the
// schema types reach, the methods of kept types (generic ones too), the init
// functions, and a blank or dot import.
func TestPrune(t *testing.T) {
	user := `package schema

import (
	"context"
	"strings"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	_ "embed"
	yaml "gopkg.in/yaml.v3"

	"example.com/app/auth"
	"example.com/app/gen"
)

// User is a schema.
type User struct{ schema.Schema }

// Fields uses a mixin's helper and a constant.
func (User) Fields() []schema.Field {
	return []schema.Field{field.String(strings.ToLower(nameCol)).Default(def())}
}

const nameCol = "NAME"

func def() string { return set[string]{}.first() }

type set[T comparable] map[T]struct{}

func (s set[T]) first() T { var z T; return z }

// Hooks is typed with gen.
func (User) Hooks() []velox.Hook[gen.User] {
	return []velox.Hook[gen.User]{velox.On(stamp, velox.OpInsert)}
}

func stamp(next velox.Mutator[gen.User]) velox.Mutator[gen.User] {
	return velox.MutateFunc[gen.User](func(ctx context.Context, m *velox.Mutation[gen.User]) error {
		_ = auth.Who(ctx)
		_ = yaml.Marshal
		return next.Mutate(ctx, m)
	})
}

type hookState struct{ u *gen.User }

func (h hookState) row() *gen.User { return h.u }

var _ velox.Hook[gen.User] = stamp

func init() { _ = nameCol }
`
	policy := `package schema

import "github.com/syssam/velox"
import "example.com/app/gen"

func (User) Policy() velox.Policy[gen.User] { return velox.Policy[gen.User]{} }
`
	files := map[string][]byte{"user.go": []byte(user), "policy.go": []byte(policy)}
	got, err := prune(files, []string{"User"}, map[string]string{"gopkg.in/yaml.v3": "yaml"})
	if err != nil {
		t.Fatal(err)
	}
	for name, src := range files {
		out, ok := got[name]
		if !ok {
			t.Fatalf("%s is unchanged", name)
		}
		if strings.Count(string(out), "\n") != strings.Count(string(src), "\n") || len(out) != len(src) {
			t.Errorf("%s: lines or offsets moved", name)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), name, out, 0); err != nil {
			t.Errorf("%s does not parse: %v\n%s", name, err, out)
		}
	}
	u := string(got["user.go"])
	for _, kept := range []string{`"strings"`, `"github.com/syssam/velox/schema"`, `"github.com/syssam/velox/schema/field"`, `_ "embed"`,
		"type User struct", "func (User) Fields()", "const nameCol", "func def()", "type set[T comparable]", "func (s set[T]) first()", "func init()"} {
		if !strings.Contains(u, kept) {
			t.Errorf("user.go lost %s:\n%s", kept, u)
		}
	}
	for _, gone := range []string{`"context"`, `"github.com/syssam/velox"`, "yaml", `"example.com/app/auth"`, `"example.com/app/gen"`,
		"func (User) Hooks", "func stamp", "hookState", "func (h hookState)", "var _ velox.Hook"} {
		if strings.Contains(u, gone) {
			t.Errorf("user.go kept %s:\n%s", gone, u)
		}
	}
	if p := strings.TrimSpace(string(got["policy.go"])); p != "package schema" {
		t.Errorf("policy.go = %q, want only its package clause", p)
	}
}

func TestGuessName(t *testing.T) {
	for p, want := range map[string]string{
		"example.com/x":         "x",
		"example.com/x/v2":      "x",
		"gopkg.in/yaml.v3":      "yaml",
		"github.com/a/go-thing": "thing",
		"example.com/a-b":       "a_b",
		"v2":                    "v2",
	} {
		if got := guessName(p); got != want {
			t.Errorf("guessName(%q) = %q, want %q", p, got, want)
		}
	}
}
