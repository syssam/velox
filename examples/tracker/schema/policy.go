package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tracker/gen"
	"github.com/syssam/velox/examples/tracker/principal"
)

// InOrg's policy confines every statement to the caller's Org, and velox ANDs
// it with each policy here, which decides what a member may do within the Org.
// gen/runtime binds each Policy method, the mixin's first. Read and Write
// return a condition ANDed into every statement, so a row the caller may not
// change is one the statement does not touch; an error denies the statement.

// Policy lets anyone in the Org read Projects and only an admin change them.
func (Project) Policy() velox.Policy[gen.Project] {
	return velox.Policy[gen.Project]{
		Read: func(ctx context.Context) (velox.Pred[gen.Project], error) {
			if _, ok := principal.FromContext(ctx); !ok {
				return velox.Pred[gen.Project]{}, velox.Deny("not logged in")
			}
			return velox.Allow[gen.Project](), nil
		},
		Write: func(ctx context.Context) (velox.Pred[gen.Project], error) {
			if p, ok := principal.FromContext(ctx); !ok || !p.Admin {
				return velox.Pred[gen.Project]{}, velox.Deny("only an admin can change a project")
			}
			return velox.Allow[gen.Project](), nil
		},
		Insert: func(ctx context.Context, _ *gen.Project) error {
			if p, ok := principal.FromContext(ctx); !ok || !p.Admin {
				return velox.Deny("only an admin can create a project")
			}
			return nil
		},
	}
}

// Policy lets anyone in the Org read Issues; an admin, the author and the
// assignee can change one. A new Issue must name its caller as its author.
func (Issue) Policy() velox.Policy[gen.Issue] {
	return velox.Policy[gen.Issue]{
		Read: func(ctx context.Context) (velox.Pred[gen.Issue], error) {
			if _, ok := principal.FromContext(ctx); !ok {
				return velox.Pred[gen.Issue]{}, velox.Deny("not logged in")
			}
			return velox.Allow[gen.Issue](), nil
		},
		Write: func(ctx context.Context) (velox.Pred[gen.Issue], error) {
			p, ok := principal.FromContext(ctx)
			switch {
			case !ok:
				return velox.Pred[gen.Issue]{}, velox.Deny("not logged in")
			case p.Admin:
				return velox.Allow[gen.Issue](), nil
			}
			return velox.Or(gen.Issues.AuthorID.EQ(p.UserID), gen.Issues.AssigneeID.EQ(p.UserID)), nil
		},
		Insert: func(ctx context.Context, x *gen.Issue) error {
			if p, ok := principal.FromContext(ctx); !ok || p.UserID != x.AuthorID {
				return velox.Deny("an issue must be created by its author")
			}
			return nil
		},
	}
}

// Policy lets anyone in the Org read Comments; an admin and the author can change
// one. A new Comment must name its caller as its author.
func (Comment) Policy() velox.Policy[gen.Comment] {
	return velox.Policy[gen.Comment]{
		Read: func(ctx context.Context) (velox.Pred[gen.Comment], error) {
			if _, ok := principal.FromContext(ctx); !ok {
				return velox.Pred[gen.Comment]{}, velox.Deny("not logged in")
			}
			return velox.Allow[gen.Comment](), nil
		},
		Write: func(ctx context.Context) (velox.Pred[gen.Comment], error) {
			p, ok := principal.FromContext(ctx)
			switch {
			case !ok:
				return velox.Pred[gen.Comment]{}, velox.Deny("not logged in")
			case p.Admin:
				return velox.Allow[gen.Comment](), nil
			}
			return gen.Comments.AuthorID.EQ(p.UserID), nil
		},
		Insert: func(ctx context.Context, x *gen.Comment) error {
			if p, ok := principal.FromContext(ctx); !ok || p.UserID != x.AuthorID {
				return velox.Deny("a comment must be created by its author")
			}
			return nil
		},
	}
}
