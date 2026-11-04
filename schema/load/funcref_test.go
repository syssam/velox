package load

import (
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox/internal/codegen/loadjson"
)

type stamp struct{}

func (stamp) Now() time.Time { return time.Time{} }

func identity[T any](v T) T { return v }

func unexportedNow() time.Time { return time.Time{} }

// ExportedCheck is a validator declared at package level.
func ExportedCheck(string) error { return nil }

// funcRef names an exported package-level function and says why it does not
// name any other function value.
func TestFuncRef(t *testing.T) {
	closure := func() time.Time { return time.Now() }
	cases := []struct {
		name string
		f    any
		want loadjson.FuncRef
	}{
		{"stdlib", time.Now, loadjson.FuncRef{Pkg: "time", Name: "Now", Type: "func() time.Time"}},
		{"exported", ExportedCheck, loadjson.FuncRef{Pkg: "github.com/syssam/velox/schema/load", Name: "ExportedCheck", Type: "func(string) error"}},
		{"closure", closure, loadjson.FuncRef{Type: "func() time.Time", Why: "a closure"}},
		{"method value", stamp{}.Now, loadjson.FuncRef{Type: "func() time.Time", Why: "a method value"}},
		{"method expression", stamp.Now, loadjson.FuncRef{Type: "func(load.stamp) time.Time", Why: "a method"}},
		{"generic", identity[string], loadjson.FuncRef{Type: "func(string) string", Why: "an instance of a generic function"}},
		{"unexported", unexportedNow, loadjson.FuncRef{Type: "func() time.Time", Why: "unexported"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := funcRef(c.f); got != c.want {
				t.Errorf("funcRef = %+v, want %+v", got, c.want)
			}
		})
	}
}

// splitFuncName reads the linker's names, which escape a dot of a path's last
// element, and refuses every name that is not pkg.Exported.
func TestSplitFuncName(t *testing.T) {
	cases := []struct{ sym, pkg, name, why string }{
		{"time.Now", "time", "Now", ""},
		{"github.com/google/uuid.New", "github.com/google/uuid", "New", ""},
		{"gopkg.in/yaml%2ev3.Marshal", "gopkg.in/yaml.v3", "Marshal", ""},
		{"example.com/app/schema.NewSlug", "example.com/app/schema", "NewSlug", ""},
		{"example.com/app/schema.Post.Fields.func1", "", "", "a closure"},
		{"example.com/app/schema.init.func2", "", "", "a closure"},
		{"example.com/app/schema.glob..func1", "", "", "a closure"},
		{"example.com/app/schema.(*Clock).Now-fm", "", "", "a method value"},
		{"example.com/app/schema.Clock.Now-fm", "", "", "a method value"},
		{"example.com/app/schema.Clock.Now", "", "", "a method"},
		{"example.com/app/schema.Make[...]", "", "", "an instance of a generic function"},
		{"example.com/app/schema.newSlug", "", "", "unexported"},
		{"main.NewSlug", "", "", "declared in package main, which no package imports"},
		{"vendor/golang.org/x/net/idna.ToASCII", "", "", "declared in a vendored package"},
		{"noPackage", "", "", "its name noPackage has no package"},
	}
	for _, c := range cases {
		pkg, name, why := splitFuncName(c.sym)
		if pkg != c.pkg || name != c.name || !strings.HasPrefix(why, c.why) || (c.why == "") != (why == "") {
			t.Errorf("splitFuncName(%q) = %q, %q, %q; want %q, %q, %q", c.sym, pkg, name, why, c.pkg, c.name, c.why)
		}
	}
}
