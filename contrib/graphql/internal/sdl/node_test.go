package sdl

import (
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
)

// Under Config.Veloxe the SDL declares Node as veloxe did: the interface,
// bound to the generated package's Noder, each type with an ID implementing
// it, and node and nodes first in the root query, which is there even with
// no QueryField. Without Veloxe there is none of it (ADR 0061).
func TestVeloxeNode(t *testing.T) {
	post, tag := entity("Post", field("title", "string")), entity("Tag", field("name", "string"))
	post.Annotations = ann(t, graphql.QueryField("posts"), graphql.RelayConnection())
	sdl, err := RenderConfig(model(post, tag), Config{Veloxe: true, Package: "example.com/app/gen"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(sdl); err != nil {
		t.Fatalf("%v\n%s", err, sdl)
	}
	for _, want := range []string{
		"interface Node @goModel(model: \"example.com/app/gen.Noder\") {\n  \"\"\"The id of the object.\"\"\"\n  id: ID!\n}\n",
		"type Post implements Node {\n",
		"type Tag implements Node {\n",
		"type Query {\n  \"\"\"Fetches an object given its ID.\"\"\"\n  node(id: ID!): Node\n  \"\"\"Lookup nodes by a list of IDs.\"\"\"\n  nodes(ids: [ID!]!): [Node]!\n  \"\"\"The connection to the Post nodes",
	} {
		if !strings.Contains(sdl, want) {
			t.Errorf("SDL lacks %q:\n%s", want, sdl)
		}
	}

	sdl, err = RenderConfig(model(entity("Tag", field("name", "string"))), Config{Veloxe: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sdl, "interface Node {\n") || !strings.Contains(sdl, "type Query {\n  \"\"\"Fetches an object given its ID.\"\"\"\n  node(id: ID!): Node\n") {
		t.Errorf("with no QueryField and no Package, no Query of node or no unbound Node:\n%s", sdl)
	}

	sdl, err = RenderConfig(model(post, tag), Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, not := range []string{"Node", "node(", "nodes(", "implements"} {
		if strings.Contains(sdl, not) {
			t.Errorf("velox's own SDL holds %q:\n%s", not, sdl)
		}
	}
}
