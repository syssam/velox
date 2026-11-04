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

// archiveKey marks a context that sees archived posts.
type archiveKey struct{}

// WithArchived returns ctx under which reads see archived posts.
func WithArchived(ctx context.Context) context.Context {
	return context.WithValue(ctx, archiveKey{}, true)
}

// Interceptors hides the post titled "archived", unless the context is
// WithArchived's.
func (Post) Interceptors() []velox.Interceptor[gen.Post] {
	return []velox.Interceptor[gen.Post]{{Filter: func(ctx context.Context) (velox.Pred[gen.Post], error) {
		if v, _ := ctx.Value(archiveKey{}).(bool); v {
			return velox.Allow[gen.Post](), nil
		}
		return gen.Posts.Title.NEQ("archived"), nil
	}}}
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
