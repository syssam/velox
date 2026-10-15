// Package model holds the relations example's entities: an Author has many
// Posts, a Post belongs to its Author and has many Comments. The *_velox.go
// files beside this one are written by go tool velox-gen.
package model

import "github.com/syssam/velox2"

// Author writes posts.
type Author struct {
	ID    int64
	Name  string
	Posts velox.Many[Post] `json:",omitzero"` // a relation, never a column
}

// Schema names the table. Posts needs no declaration: by convention the
// target holds the key in a field named after the owner, Post.AuthorID.
func (x *Author) Schema(s *velox.Schema) { s.Table("authors") }

// Post belongs to an Author and has Comments.
type Post struct {
	ID       int64
	AuthorID int64
	Title    string
	Votes    int64
	Author   velox.One[Author]   `json:",omitzero"`
	Comments velox.Many[Comment] `json:",omitzero"`
}

// Schema names the table and declares the Comments key. Author needs no
// declaration: by convention a One named Author uses the owner's AuthorID.
func (x *Post) Schema(s *velox.Schema) {
	s.Table("posts")
	// The key the convention would find, written out. Declare it when the
	// field is not named after the owner; One's equivalent is References.
	s.Many(&x.Comments).ForeignKey(func(c *Comment) *int64 { return &c.PostID })
}

// Comment is a reply to a Post.
type Comment struct {
	ID     int64
	PostID int64
	Body   string
}

// Schema names the table.
func (x *Comment) Schema(s *velox.Schema) { s.Table("comments") }
