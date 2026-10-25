package itest

import "github.com/syssam/velox2"

// The many-to-many model: books and tags through bookTag, written by hand as
// velox-gen writes a Desc, like the root package's test model. Tags are
// tenant-scoped, so that a relation's target scope is tested on a real
// database.

type book struct {
	ID    int64
	Title string
	Tags  velox.Many[tag]
}

var Books = struct {
	ID    velox.NumberCol[book, int64]
	Title velox.StringCol[book, string]
	Tags  velox.Rel[book, tag]
}{velox.NumberCol[book, int64]{I: 0}, velox.StringCol[book, string]{I: 1}, velox.Rel[book, tag]{I: 0}}

var bookMeta velox.MetaOnce[book]

func (*book) VeloxMeta() *velox.Meta[book] { return bookMeta.Load(bookDesc) }

func bookDesc() velox.Desc[book] {
	return velox.Desc[book]{
		Table:      "books",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Edges:      []velox.EdgeDesc{velox.EdgeThrough[bookTag, tag]("Tags", 0, 0, 1, 0)},
		Addr: func(x *book, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Title
			}
			return nil
		},
		EdgeAddr: func(x *book, i int) any {
			if i == 0 {
				return &x.Tags
			}
			return nil
		},
	}
}

type tag struct {
	ID    int64
	OrgID int64
	Name  string
	Books velox.Many[book]
}

var Tags = struct {
	ID    velox.NumberCol[tag, int64]
	OrgID velox.NumberCol[tag, int64]
	Name  velox.StringCol[tag, string]
	Books velox.Rel[tag, book]
}{velox.NumberCol[tag, int64]{I: 0}, velox.NumberCol[tag, int64]{I: 1}, velox.StringCol[tag, string]{I: 2}, velox.Rel[tag, book]{I: 0}}

var tagMeta velox.MetaOnce[tag]

func (*tag) VeloxMeta() *velox.Meta[tag] { return tagMeta.Load(tagDesc) }

func tagDesc() velox.Desc[tag] {
	return velox.Desc[tag]{
		Table:      "tags",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		Tenant:     1,
		Edges:      []velox.EdgeDesc{velox.EdgeThrough[bookTag, book]("Books", 0, 1, 0, 0)},
		Addr: func(x *tag, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.OrgID
			case 2:
				return &x.Name
			}
			return nil
		},
		EdgeAddr: func(x *tag, i int) any {
			if i == 0 {
				return &x.Books
			}
			return nil
		},
	}
}

// bookTag is the join entity: a row per book and tag.
type bookTag struct {
	BookID int64
	TagID  int64
}

var BookTags = struct {
	BookID velox.NumberCol[bookTag, int64]
	TagID  velox.NumberCol[bookTag, int64]
}{velox.NumberCol[bookTag, int64]{I: 0}, velox.NumberCol[bookTag, int64]{I: 1}}

var bookTagMeta velox.MetaOnce[bookTag]

func (*bookTag) VeloxMeta() *velox.Meta[bookTag] { return bookTagMeta.Load(bookTagDesc) }

func bookTagDesc() velox.Desc[bookTag] {
	return velox.Desc[bookTag]{
		Table:      "book_tags",
		Columns:    []velox.ColumnDesc{{Field: "BookID", Name: "book_id"}, {Field: "TagID", Name: "tag_id"}},
		PrimaryKey: []int{0, 1},
		Tenant:     -1,
		Addr: func(x *bookTag, i int) any {
			switch i {
			case 0:
				return &x.BookID
			case 1:
				return &x.TagID
			}
			return nil
		},
	}
}
