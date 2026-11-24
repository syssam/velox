package sdl

import (
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
)

// Under node=on the SDL declares Node as entgql does: the interface, bound
// to the generated package's Noder, each type with an ID implementing it,
// and node and nodes first in the root query, which is there even with no
// QueryField. By default there is none of it (ADR 0061).
func TestNode(t *testing.T) {
	post, tag := entity("Post", field("title", "string")), entity("Tag", field("name", "string"))
	post.Annotations = ann(t, graphql.QueryField("posts"), graphql.RelayConnection())
	sdl, err := RenderConfig(model(post, tag), Config{Node: "on", Package: "example.com/app/gen"})
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

	sdl, err = RenderConfig(model(entity("Tag", field("name", "string"))), Config{Node: "on"})
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

// node=on declares Node with velox's other defaults: names stay Go's. type
// Query is written for node and nodes with no QueryField. Under entities=
// only the chosen types implement Node (ADR 0125). node=off and the default
// write none of it (ADR 0152).
func TestNodeOption(t *testing.T) {
	img := field("image_url", "string")
	img.GoName = "ImageURL"
	tag := entity("Tag", img)
	sdl, err := RenderConfig(model(tag), Config{Node: "on", Package: "example.com/app/gen"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(sdl); err != nil {
		t.Fatalf("%v\n%s", err, sdl)
	}
	for _, want := range []string{
		"interface Node @goModel(model: \"example.com/app/gen.Noder\") {\n",
		"type Tag implements Node {\n",
		"  imageURL: String!\n",
		"type Query {\n  \"\"\"Fetches an object given its ID.\"\"\"\n  node(id: ID!): Node\n  \"\"\"Lookup nodes by a list of IDs.\"\"\"\n  nodes(ids: [ID!]!): [Node]!\n}\n",
	} {
		if !strings.Contains(sdl, want) {
			t.Errorf("node=on: SDL lacks %q:\n%s", want, sdl)
		}
	}

	post := entity("Post", field("title", "string"))
	sdl, err = RenderConfig(model(post, tag), Config{Node: "on", Entities: []string{"Post"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sdl, "type Post implements Node {\n") || strings.Contains(sdl, "type Tag") || !strings.Contains(sdl, "  node(id: ID!): Node\n") {
		t.Errorf("node=on, entities=Post: Post does not implement Node, or Tag is written:\n%s", sdl)
	}
	sdl, err = RenderConfig(model(post, tag), Config{Node: "on", Entities: []string{"Post"}, PerEntity: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sdl, "interface Node {\n") || !strings.Contains(sdl, "type Post implements Node {\n") {
		t.Errorf("node=on, entities=Post, sdlfiles=entity: no Node:\n%s", sdl)
	}

	for _, cfg := range []Config{func() Config { c := wideConfig(); c.Node = "off"; return c }(), {Node: "off"}, {}} {
		sdl, err := RenderConfig(model(post, tag), cfg)
		if err != nil {
			t.Fatal(err)
		}
		for _, not := range []string{"Node", "node(", "nodes(", "implements"} {
			if strings.Contains(sdl, not) {
				t.Errorf("%+v: SDL holds %q:\n%s", cfg, not, sdl)
			}
		}
	}

	if _, err := RenderConfig(model(tag), Config{Node: "yes"}); err == nil || err.Error() != "velox-graphql: node=yes: node is off, no Node; or on, Relay's Node and node(id:)" {
		t.Errorf("node=yes: %v", err)
	}
}
