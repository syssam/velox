// Package model holds the policy example's entities: a Doc, which its owner
// may change and anyone may read once shared, and its Comments, which are
// visible exactly when their Doc is. The *_velox.go files beside this one are
// written by go tool velox-gen.
package model

import (
	"context"

	"github.com/syssam/velox2"
)

type userKey struct{}

// AsUser returns ctx acting as user. In a service, authentication middleware
// sets it. The user "admin" may read and change everything.
func AsUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

func userOf(ctx context.Context) string { u, _ := ctx.Value(userKey{}).(string); return u }

// Doc is a document with an owner.
type Doc struct {
	ID       int64
	Owner    string
	Title    string
	Shared   bool
	Comments velox.Many[Comment] `json:",omitzero"`
}

// Schema declares Doc's policy. Read and Write return a condition that velox
// ANDs into every statement; an error denies the statement. A zero Pred
// restricts nothing.
func (x *Doc) Schema(s *velox.Schema) {
	s.Table("docs")
	s.Policy(velox.Policy[Doc]{
		Read: func(ctx context.Context) (velox.Pred[Doc], error) {
			switch u := userOf(ctx); u {
			case "":
				return velox.Pred[Doc]{}, velox.Deny("no user")
			case "admin":
				return velox.Pred[Doc]{}, nil
			default:
				return velox.Or(Docs.Owner.EQ(u), Docs.Shared.EQ(true)), nil
			}
		},
		// Write filters which rows may change; Read applies too.
		Write: func(ctx context.Context) (velox.Pred[Doc], error) {
			switch u := userOf(ctx); u {
			case "":
				return velox.Pred[Doc]{}, velox.Deny("no user")
			case "admin":
				return velox.Pred[Doc]{}, nil
			default:
				return Docs.Owner.EQ(u), nil
			}
		},
		// Insert checks each new row.
		Insert: func(ctx context.Context, x *Doc) error {
			if u := userOf(ctx); u != "admin" && u != x.Owner {
				return velox.Deny("a doc must be created by its owner")
			}
			return nil
		},
	})
}

// Comment is a remark on a Doc.
type Comment struct {
	ID    int64
	DocID int64
	Body  string
	Doc   velox.One[Doc] `json:",omitzero"`
}

// Schema declares Comment's policy by inheritance: a comment is visible when
// its doc is. Has is an EXISTS on the Doc, and velox applies Doc's own Read
// policy inside it, for this context.
func (x *Comment) Schema(s *velox.Schema) {
	s.Table("comments")
	s.Policy(velox.Policy[Comment]{
		Read: func(context.Context) (velox.Pred[Comment], error) {
			return Comments.Doc.Has(), nil
		},
	})
}
