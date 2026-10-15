package gen

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
	_, err = discover(map[string][]byte{"c.go": []byte("package s\nimport \"github.com/syssam/velox2/schema\"\ntype user struct{ schema.Schema }\n")})
	if err == nil || !strings.Contains(err.Error(), "schema type user is unexported") {
		t.Fatalf("got %v", err)
	}
}
