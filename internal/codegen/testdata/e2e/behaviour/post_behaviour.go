//go:build ignore

package schema

import (
	"context"

	"github.com/syssam/velox"
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
func (Post) Interceptors() []velox.Interceptor {
	return []velox.Interceptor{{Filter: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
		if v, _ := ctx.Value(archiveKey{}).(bool); v {
			return velox.Allow(), nil
		}
		return velox.FieldNEQ("title", "archived"), nil
	}}}
}

// Policy shows a viewer only published posts, and refuses a post titled
// "forbidden".
func (Post) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
			if v, _ := ctx.Value(viewerKey{}).(bool); v {
				return velox.FieldEQ("status", "published"), nil
			}
			return velox.Allow(), nil
		},
		Insert: func(ctx context.Context, _ *velox.DB, r velox.Row) error {
			if r.Value("title") == "forbidden" {
				return velox.Deny("that title is forbidden")
			}
			return nil
		},
	}
}
