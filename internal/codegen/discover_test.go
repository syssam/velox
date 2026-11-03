package codegen

import (
	"strings"
	"testing"
)

// candidates keeps the types that may be schemas: a struct with an embedded
// field, at any depth or from another package, an alias, a defined type of
// another named type (Level too, which classify then passes over) and a
// type that declares Type. It refuses an unexported struct that embeds schema.Schema
// when no type embeds it, and passes an unexported base.
func TestCandidates(t *testing.T) {
	files := map[string][]byte{
		"a.go": []byte(`package s
import vs "github.com/syssam/velox/schema"
type User struct{ vs.Schema }
type notASchema struct{ Name string }
type base struct{ vs.Schema }
type Derived struct{ base }
type Other struct{ other.Base }
type Own struct{}
func (Own) Type() {}
type Plain struct{ Name string }
`),
		"b.go": []byte(`package s
import "github.com/syssam/velox/schema"
type Post struct {
	schema.Schema
}
type Alias = Post
type Ptr struct{ *schema.Schema }
type Gen[T any] struct{ schema.Schema }
type Release Post
type Draft other.Base
type Level int
type ids []int
`),
	}
	got, err := candidates(files)
	if want := "Alias,Derived,Draft,Gen,Level,Other,Own,Post,Ptr,Release,User"; err != nil || strings.Join(got, ",") != want {
		t.Fatalf("got %v %v, want %s", got, err, want)
	}
	_, err = candidates(map[string][]byte{"c.go": []byte("package s\nimport \"github.com/syssam/velox/schema\"\ntype user struct{ schema.Schema }\ntype deeper struct{ user }\n")})
	if want := "c.go:4: type deeper embeds schema.Schema and is unexported: a schema must be exported, and a shared base is embedded by one"; err == nil || err.Error() != want {
		t.Fatalf("got %v, want %s", err, want)
	}
}

func TestDecodeModel(t *testing.T) {
	m, err := decodeModel([]byte(`{"version":4,"package":"p","schemas":[]}`))
	if err != nil || m.Package != "p" {
		t.Fatalf("got %+v %v", m, err)
	}
	// A model of another version whose shape differs reports the version.
	_, err = decodeModel([]byte(`{"version":1,"package":["p"],"schemas":{}}`))
	if err == nil || !strings.Contains(err.Error(), "the loader wrote model version 1; velox-gen reads version 4: update velox-gen and the velox module together") {
		t.Fatalf("got %v", err)
	}
}
