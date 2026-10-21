package desc

import (
	"fmt"
	"net/http"
	"reflect"
	"runtime"
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
		{time.Time{}, GoType{Ident: "time.Time", PkgPath: "time", Kind: "struct", Imports: []string{"time"}}},
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
