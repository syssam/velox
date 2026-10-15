package desc

import (
	"net/http"
	"reflect"
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
