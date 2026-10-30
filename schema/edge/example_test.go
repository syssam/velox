package edge_test

import (
	"fmt"

	"github.com/syssam/velox2/schema/edge"
)

// An edge names its target by a method expression, and its inverse by the name of
// the edge it answers.
func ExampleFrom() {
	owner := edge.From("owner", Post.Type).Ref("posts").Unique().Required().Descriptor()
	fmt.Println(owner.Name, owner.Type, owner.RefName, owner.Unique, owner.Required)
	// Output:
	// owner Post posts true true
}

func ExampleTo() {
	posts := edge.To("posts", Post.Type).OnDelete(edge.Cascade).Descriptor()
	fmt.Println(posts.Name, posts.Type, posts.OnDelete)
	// Output:
	// posts Post CASCADE
}
