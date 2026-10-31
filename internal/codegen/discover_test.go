package codegen

import (
	"strings"
	"testing"
)

func TestDiscover(t *testing.T) {
	files := map[string][]byte{
		"a.go": []byte(`package s
import vs "github.com/syssam/velox2/schema"
type User struct{ vs.Schema }
type notASchema struct{ Name string }
`),
		"b.go": []byte(`package s
import "github.com/syssam/velox2/schema"
type Post struct {
	schema.Schema
}
type Alias = Post
`),
	}
	got, err := discover(files)
	if err != nil || strings.Join(got, ",") != "Post,User" {
		t.Fatalf("got %v %v", got, err)
	}
	// Only a direct embedding is found: Derived embeds Base, which embeds
	// schema.Schema, and is not a schema.
	got, err = discover(map[string][]byte{"d.go": []byte("package s\nimport \"github.com/syssam/velox2/schema\"\ntype Base struct{ schema.Schema }\ntype Derived struct{ Base }\n")})
	if err != nil || strings.Join(got, ",") != "Base" {
		t.Fatalf("got %v %v", got, err)
	}
	_, err = discover(map[string][]byte{"c.go": []byte("package s\nimport \"github.com/syssam/velox2/schema\"\ntype user struct{ schema.Schema }\n")})
	if err == nil || !strings.Contains(err.Error(), "schema type user is unexported") {
		t.Fatalf("got %v", err)
	}
	_, err = discover(map[string][]byte{"d.go": []byte("package s\nimport \"github.com/syssam/velox2/schema\"\ntype Ok struct{ schema.Schema }\ntype Ptr struct{ *schema.Schema }\n")})
	if err == nil || !strings.Contains(err.Error(), "schema type Ptr must embed schema.Schema by value, not a pointer") {
		t.Fatalf("got %v", err)
	}
}

func TestDecodeModel(t *testing.T) {
	m, err := decodeModel([]byte(`{"version":3,"package":"p","schemas":[]}`))
	if err != nil || m.Package != "p" {
		t.Fatalf("got %+v %v", m, err)
	}
	// A model of another version whose shape differs reports the version.
	_, err = decodeModel([]byte(`{"version":1,"package":["p"],"schemas":{}}`))
	if err == nil || !strings.Contains(err.Error(), "the loader wrote model version 1; velox-gen reads version 3: update velox-gen and the velox2 module together") {
		t.Fatalf("got %v", err)
	}
}
