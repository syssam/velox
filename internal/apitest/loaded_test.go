package apitest

import (
	"testing"

	"github.com/syssam/velox"
)

type loadedThing struct{ Name string }

func TestLoadedRelations(t *testing.T) {
	var zero velox.One[loadedThing]
	if _, ok := zero.Get(); ok {
		t.Fatal("the zero One is loaded")
	}
	one := velox.LoadedOne(&loadedThing{Name: "a"})
	if p, ok := one.Get(); !ok || p == nil || p.Name != "a" {
		t.Fatalf("LoadedOne.Get() = %v, %t; want the row, loaded", p, ok)
	}
	none := velox.LoadedOne[loadedThing](nil)
	if p, ok := none.Get(); !ok || p != nil {
		t.Fatalf("LoadedOne(nil).Get() = %v, %t; want nil, loaded", p, ok)
	}
	many := velox.LoadedMany([]*loadedThing{{Name: "a"}, {Name: "b"}})
	if s, ok := many.Get(); !ok || len(s) != 2 {
		t.Fatalf("LoadedMany.Get() = %v, %t; want 2 rows, loaded", s, ok)
	}
	empty := velox.LoadedMany[loadedThing](nil)
	if s, ok := empty.Get(); !ok || len(s) != 0 {
		t.Fatalf("LoadedMany(nil).Get() = %v, %t; want none, loaded", s, ok)
	}
	if b, err := empty.MarshalJSON(); err != nil || string(b) != "[]" {
		t.Fatalf("LoadedMany(nil).MarshalJSON() = %s, %v; want []", b, err)
	}
}
