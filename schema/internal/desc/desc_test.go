package desc

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

type local struct{}

func TestGoTypeOf(t *testing.T) {
	for _, c := range []struct {
		v    any
		want GoType
	}{
		{"", GoType{Ident: "string", Kind: "string"}},
		{[]string{}, GoType{Ident: "[]string", Kind: "slice"}},
		{time.Time{}, GoType{Ident: "time.Time", PkgPath: "time", Kind: "struct", Imports: []string{"time"}, Marshaler: true}},
		{map[string]*http.Request{}, GoType{Ident: "map[string]*http.Request", Kind: "map", Imports: []string{"net/http"}}},
		{&local{}, GoType{Ident: "*desc.local", PkgPath: "github.com/syssam/velox2/schema/internal/desc", Kind: "ptr",
			Imports: []string{"github.com/syssam/velox2/schema/internal/desc"}}},
	} {
		got := GoTypeOf(reflect.TypeOf(c.v))
		if !reflect.DeepEqual(*got, c.want) {
			t.Errorf("GoTypeOf(%T) = %+v, want %+v", c.v, *got, c.want)
		}
	}
}

type box[T any] struct{ V T }

func TestUnwritable(t *testing.T) {
	for _, c := range []struct {
		v    any
		want string // a word the reason holds; "" for none
	}{
		{0, ""},
		{[]string{}, ""},
		{map[string]any{}, ""},
		{map[string]struct{}{}, ""},
		{&url.URL{}, ""},
		{[16]byte{}, ""},
		{local{}, ""},
		{box[int]{}, "generic"},
		{[]box[int]{}, "generic"},
		{map[string]*box[int]{}, "generic"},
		{struct{ A int }{}, "unnamed struct"},
		{[]struct{ A int }{}, "unnamed struct"},
		{func() {}, "unnamed func"},
		{(*interface{ M() })(nil), "unnamed interface"},
	} {
		got := Unwritable(reflect.TypeOf(c.v))
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("Unwritable(%T) = %q, want %q", c.v, got, c.want)
		}
	}
}

// ctor stands for a DSL constructor small enough to be inlined into its
// caller, which go build -gcflags=-m reports for it.
func ctor() uintptr { return Caller() }

// The counter Caller captures is the call of the constructor, also when the
// constructor is inlined; go test -gcflags=all=-l checks the other case.
func TestCaller(t *testing.T) {
	_, file, line, _ := runtime.Caller(0)
	pc := ctor()
	if got, want := PosOf(pc), fmt.Sprintf("%s:%d", file, line+1); got != want {
		t.Fatalf("PosOf = %q, want %q", got, want)
	}
	if PosOf(0) != "" {
		t.Fatal("PosOf(0) is not empty")
	}
}
