package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/tracker/principal"
)

// InOrg's policy confines every statement to the caller's Org. velox ANDs it
// with each policy here, which decides what a member may do within the Org.
// Read and Write return a condition ANDed into every statement, so a row the
// caller may not change is one the statement does not touch; an error denies
// the statement.

// member lets anyone logged in read.
func member(ctx context.Context) (velox.Cond, error) {
	if _, ok := principal.FromContext(ctx); !ok {
		return velox.Cond{}, velox.Deny("not logged in")
	}
	return velox.Allow(), nil
}

// authoredBy refuses a new row whose author is not the caller.
func authoredBy(why string) func(context.Context, velox.Row) error {
	return func(ctx context.Context, r velox.Row) error {
		if p, ok := principal.FromContext(ctx); !ok || r.Value("author_id") != p.UserID {
			return velox.Deny(why)
		}
		return nil
	}
}

// Policy lets anyone in the Org read Projects and only an admin change them.
func (Project) Policy() velox.Policy {
	return velox.Policy{
		Read: member,
		Write: func(ctx context.Context) (velox.Cond, error) {
			if p, ok := principal.FromContext(ctx); !ok || !p.Admin {
				return velox.Cond{}, velox.Deny("only an admin can change a project")
			}
			return velox.Allow(), nil
		},
		Insert: func(ctx context.Context, _ velox.Row) error {
			if p, ok := principal.FromContext(ctx); !ok || !p.Admin {
				return velox.Deny("only an admin can create a project")
			}
			return nil
		},
	}
}

// Policy lets anyone in the Org read Issues; an admin, the author and the
// assignee can change one. A new Issue must name its caller as its author.
func (Issue) Policy() velox.Policy {
	return velox.Policy{
		Read: member,
		Write: func(ctx context.Context) (velox.Cond, error) {
			p, ok := principal.FromContext(ctx)
			switch {
			case !ok:
				return velox.Cond{}, velox.Deny("not logged in")
			case p.Admin:
				return velox.Allow(), nil
			}
			return velox.Or(velox.C("author_id").EQ(p.UserID), velox.C("assignee_id").EQ(p.UserID)), nil
		},
		Insert: authoredBy("an issue must be created by its author"),
	}
}

// Policy lets anyone in the Org read Comments; an admin and the author can
// change one. A new Comment must name its caller as its author.
func (Comment) Policy() velox.Policy {
	return velox.Policy{
		Read: member,
		Write: func(ctx context.Context) (velox.Cond, error) {
			p, ok := principal.FromContext(ctx)
			switch {
			case !ok:
				return velox.Cond{}, velox.Deny("not logged in")
			case p.Admin:
				return velox.Allow(), nil
			}
			return velox.C("author_id").EQ(p.UserID), nil
		},
		Insert: authoredBy("a comment must be created by its author"),
	}
}
