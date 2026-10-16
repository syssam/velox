package gen

import (
	"context"

	"github.com/syssam/velox2"
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
func (*Post) Policy() velox.Policy[Post] {
	return velox.Policy[Post]{
		Read: func(ctx context.Context) (velox.Pred[Post], error) {
			if v, _ := ctx.Value(viewerKey{}).(bool); v {
				return Posts.Status.EQ(PostStatusPublished), nil
			}
			return velox.Pred[Post]{}, nil
		},
		Insert: func(ctx context.Context, x *Post) error {
			if x.Title == "forbidden" {
				return velox.Deny("that title is forbidden")
			}
			return nil
		},
	}
}
