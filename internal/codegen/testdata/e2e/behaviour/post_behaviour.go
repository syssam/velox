//go:build ignore

package schema

import (
	"context"

	"github.com/syssam/velox"

	"example.com/tmp/gen"
)

// viewerKey marks a context as a viewer's: such a context reads only
// published posts.
type viewerKey struct{}

// AsViewer returns ctx acting as a viewer.
func AsViewer(ctx context.Context) context.Context {
	return context.WithValue(ctx, viewerKey{}, true)
}

// Policy shows a viewer only published posts, and refuses a post titled
// "forbidden".
func (Post) Policy() velox.Policy[gen.Post] {
	return velox.Policy[gen.Post]{
		Read: func(ctx context.Context) (velox.Pred[gen.Post], error) {
			if v, _ := ctx.Value(viewerKey{}).(bool); v {
				return gen.Posts.Status.EQ(gen.PostStatusPublished), nil
			}
			return velox.Allow[gen.Post](), nil
		},
		Insert: func(ctx context.Context, x *gen.Post) error {
			if x.Title == "forbidden" {
				return velox.Deny("that title is forbidden")
			}
			return nil
		},
	}
}
