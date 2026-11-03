package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tracker/gen/entity"
	"github.com/syssam/velox/examples/tracker/principal"
)

// The scope that Scoped generates confines every statement to the caller's Org,
// and is bound around each policy here. These policies decide what a member may do within the Org.
// velox-gen binds each Policy method from gen. Read and Write
// return a condition ANDed into every statement, so a row the caller may not
// change is one the statement does not touch; an error denies the statement.

// Policy lets anyone in the Org read Projects and only an admin change them.
func (Project) Policy() velox.Policy[entity.Project] {
	return velox.Policy[entity.Project]{
		Read: func(ctx context.Context) (velox.Pred[entity.Project], error) {
			if _, ok := principal.FromContext(ctx); !ok {
				return velox.Pred[entity.Project]{}, velox.Deny("not logged in")
			}
			return velox.Allow[entity.Project](), nil
		},
		Write: func(ctx context.Context) (velox.Pred[entity.Project], error) {
			if p, ok := principal.FromContext(ctx); !ok || !p.Admin {
				return velox.Pred[entity.Project]{}, velox.Deny("only an admin can change a project")
			}
			return velox.Allow[entity.Project](), nil
		},
		Insert: func(ctx context.Context, _ *entity.Project) error {
			if p, ok := principal.FromContext(ctx); !ok || !p.Admin {
				return velox.Deny("only an admin can create a project")
			}
			return nil
		},
	}
}

// Policy lets anyone in the Org read Issues; an admin, the author and the
// assignee can change one. A new Issue must name its caller as its author.
func (Issue) Policy() velox.Policy[entity.Issue] {
	return velox.Policy[entity.Issue]{
		Read: func(ctx context.Context) (velox.Pred[entity.Issue], error) {
			if _, ok := principal.FromContext(ctx); !ok {
				return velox.Pred[entity.Issue]{}, velox.Deny("not logged in")
			}
			return velox.Allow[entity.Issue](), nil
		},
		Write: func(ctx context.Context) (velox.Pred[entity.Issue], error) {
			p, ok := principal.FromContext(ctx)
			switch {
			case !ok:
				return velox.Pred[entity.Issue]{}, velox.Deny("not logged in")
			case p.Admin:
				return velox.Allow[entity.Issue](), nil
			}
			return velox.Or(entity.Issues.AuthorID.EQ(p.UserID), entity.Issues.AssigneeID.EQ(p.UserID)), nil
		},
		Insert: func(ctx context.Context, x *entity.Issue) error {
			if p, ok := principal.FromContext(ctx); !ok || p.UserID != x.AuthorID {
				return velox.Deny("an issue must be created by its author")
			}
			return nil
		},
	}
}

// Policy lets anyone in the Org read Comments; an admin and the author can change
// one. A new Comment must name its caller as its author.
func (Comment) Policy() velox.Policy[entity.Comment] {
	return velox.Policy[entity.Comment]{
		Read: func(ctx context.Context) (velox.Pred[entity.Comment], error) {
			if _, ok := principal.FromContext(ctx); !ok {
				return velox.Pred[entity.Comment]{}, velox.Deny("not logged in")
			}
			return velox.Allow[entity.Comment](), nil
		},
		Write: func(ctx context.Context) (velox.Pred[entity.Comment], error) {
			p, ok := principal.FromContext(ctx)
			switch {
			case !ok:
				return velox.Pred[entity.Comment]{}, velox.Deny("not logged in")
			case p.Admin:
				return velox.Allow[entity.Comment](), nil
			}
			return entity.Comments.AuthorID.EQ(p.UserID), nil
		},
		Insert: func(ctx context.Context, x *entity.Comment) error {
			if p, ok := principal.FromContext(ctx); !ok || p.UserID != x.AuthorID {
				return velox.Deny("a comment must be created by its author")
			}
			return nil
		},
	}
}
