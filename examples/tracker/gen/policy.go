package gen

import (
	"context"

	"github.com/syssam/velox"
)

// The scope that Scoped generates confines every statement to the caller's Org,
// and is bound around each policy here. These policies decide what a member may do within the Org.
// velox-gen binds each Policy method into its entity's metadata. Read and Write
// return a condition ANDed into every statement, so a row the caller may not
// change is one the statement does not touch; an error denies the statement.

// Policy lets anyone in the Org read Projects and only an admin change them.
func (*Project) Policy() velox.Policy[Project] {
	return velox.Policy[Project]{
		Read: func(ctx context.Context) (velox.Pred[Project], error) {
			if _, ok := PrincipalFromContext(ctx); !ok {
				return velox.Pred[Project]{}, velox.Deny("not logged in")
			}
			return velox.Allow[Project](), nil
		},
		Write: func(ctx context.Context) (velox.Pred[Project], error) {
			if p, ok := PrincipalFromContext(ctx); !ok || !p.Admin {
				return velox.Pred[Project]{}, velox.Deny("only an admin can change a project")
			}
			return velox.Allow[Project](), nil
		},
		Insert: func(ctx context.Context, _ *Project) error {
			if p, ok := PrincipalFromContext(ctx); !ok || !p.Admin {
				return velox.Deny("only an admin can create a project")
			}
			return nil
		},
	}
}

// Policy lets anyone in the Org read Issues; an admin, the author and the
// assignee can change one. A new Issue must name its caller as its author.
func (*Issue) Policy() velox.Policy[Issue] {
	return velox.Policy[Issue]{
		Read: func(ctx context.Context) (velox.Pred[Issue], error) {
			if _, ok := PrincipalFromContext(ctx); !ok {
				return velox.Pred[Issue]{}, velox.Deny("not logged in")
			}
			return velox.Allow[Issue](), nil
		},
		Write: func(ctx context.Context) (velox.Pred[Issue], error) {
			p, ok := PrincipalFromContext(ctx)
			switch {
			case !ok:
				return velox.Pred[Issue]{}, velox.Deny("not logged in")
			case p.Admin:
				return velox.Allow[Issue](), nil
			}
			return velox.Or(Issues.AuthorID.EQ(p.UserID), Issues.AssigneeID.EQ(p.UserID)), nil
		},
		Insert: func(ctx context.Context, x *Issue) error {
			if p, ok := PrincipalFromContext(ctx); !ok || p.UserID != x.AuthorID {
				return velox.Deny("an issue must be created by its author")
			}
			return nil
		},
	}
}

// Policy lets anyone in the Org read Comments; an admin and the author can change
// one. A new Comment must name its caller as its author.
func (*Comment) Policy() velox.Policy[Comment] {
	return velox.Policy[Comment]{
		Read: func(ctx context.Context) (velox.Pred[Comment], error) {
			if _, ok := PrincipalFromContext(ctx); !ok {
				return velox.Pred[Comment]{}, velox.Deny("not logged in")
			}
			return velox.Allow[Comment](), nil
		},
		Write: func(ctx context.Context) (velox.Pred[Comment], error) {
			p, ok := PrincipalFromContext(ctx)
			switch {
			case !ok:
				return velox.Pred[Comment]{}, velox.Deny("not logged in")
			case p.Admin:
				return velox.Allow[Comment](), nil
			}
			return Comments.AuthorID.EQ(p.UserID), nil
		},
		Insert: func(ctx context.Context, x *Comment) error {
			if p, ok := PrincipalFromContext(ctx); !ok || p.UserID != x.AuthorID {
				return velox.Deny("a comment must be created by its author")
			}
			return nil
		},
	}
}
