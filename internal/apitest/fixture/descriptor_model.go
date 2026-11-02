package fixture

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/syssam/velox"
)

// DAuthor is confined to the context's org, has an immutable creation time, a validated name
// and a sensitive email.
type DAuthor struct {
	ID        int64
	OrgID     int64
	Name      string
	Email     *string
	CreatedAt time.Time
	Books     velox.Many[DBook]
}

var DAuthors = struct {
	ID        velox.NumberCol[DAuthor, int64]
	OrgID     velox.NumberCol[DAuthor, int64]
	Name      velox.StringCol[DAuthor, string]
	Email     velox.NullStringCol[DAuthor, string]
	CreatedAt velox.OrderedCol[DAuthor, time.Time]
	Books     velox.Rel[DAuthor, DBook]
}{
	velox.NumberCol[DAuthor, int64]{I: 0}, velox.NumberCol[DAuthor, int64]{I: 1},
	velox.StringCol[DAuthor, string]{I: 2}, velox.NullStringCol[DAuthor, string]{I: 3},
	velox.OrderedCol[DAuthor, time.Time]{I: 4}, velox.Rel[DAuthor, DBook]{I: 0},
}

var DAuthorMeta velox.MetaOnce[DAuthor]

func (*DAuthor) VeloxMeta() *velox.Meta[DAuthor] { return DAuthorMeta.Load(DAuthorDescriptor) }

var ErrEmptyName = errors.New("must not be empty")

func DAuthorDescriptor() velox.Descriptor[DAuthor] {
	return velox.Descriptor[DAuthor]{
		Policy: func() velox.BoundPolicy { return velox.BindPolicy((*DAuthor)(nil).Policy()) },
		Hooks:  func() velox.BoundHooks { return velox.BindHooks((*DAuthor)(nil).Hooks()) },
		Table:  "authors",
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"},
			{Field: "OrgID", Name: "org_id", Immutable: true},
			{Field: "Name", Name: "name"},
			{Field: "Email", Name: "email", Sensitive: true},
			{Field: "CreatedAt", Name: "created_at", DefaultFunc: func() any { return time.Now() }, Immutable: true},
		},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[DBook]("Books", 0, 1)},
		FieldPtr: func(x *DAuthor, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.OrgID
			case 2:
				return &x.Name
			case 3:
				return &x.Email
			case 4:
				return &x.CreatedAt
			}
			return nil
		},
		EdgeFieldPtr: func(x *DAuthor, i int) any {
			if i == 0 {
				return &x.Books
			}
			return nil
		},
		Check: func(col int, v any) error {
			// velox hands a validator the value, never nil or a pointer.
			if v == nil || reflect.TypeOf(v).Kind() == reflect.Pointer {
				return fmt.Errorf("check got %T", v)
			}
			switch col {
			case 2:
				if s := v.(string); s == "" {
					return ErrEmptyName
				} else if len(s) > 20 {
					return errors.New("longer than 20")
				}
			case 3:
				if !strings.Contains(v.(string), "@") {
					return errors.New("not an email")
				}
			}
			return nil
		},
	}
}

// DBook belongs to an author, and has tags and picks through join entities.
type DBook struct {
	ID       int64
	AuthorID int64
	Title    string
	Author   velox.One[DAuthor]
	Tags     velox.Many[DTag]
	Picks    velox.Many[DTag]
}

var DBooks = struct {
	ID       velox.NumberCol[DBook, int64]
	AuthorID velox.NumberCol[DBook, int64]
	Title    velox.StringCol[DBook, string]
	Author   velox.Rel[DBook, DAuthor]
	Tags     velox.Rel[DBook, DTag]
	Picks    velox.Rel[DBook, DTag]
}{
	velox.NumberCol[DBook, int64]{I: 0}, velox.NumberCol[DBook, int64]{I: 1}, velox.StringCol[DBook, string]{I: 2},
	velox.Rel[DBook, DAuthor]{I: 0}, velox.Rel[DBook, DTag]{I: 1}, velox.Rel[DBook, DTag]{I: 2},
}

var DBookMeta velox.MetaOnce[DBook]

func (*DBook) VeloxMeta() *velox.Meta[DBook] { return DBookMeta.Load(DBookDescriptor) }

func DBookDescriptor() velox.Descriptor[DBook] {
	return velox.Descriptor[DBook]{
		Hooks:      func() velox.BoundHooks { return velox.BindHooks((*DBook)(nil).Hooks()) },
		Table:      "books",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "AuthorID", Name: "author_id"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		Edges: []velox.EdgeDescriptor{
			velox.EdgeTo[DAuthor]("Author", 1, 0),
			velox.EdgeThrough[DBookTag, DTag]("Tags", 0, 0, 1, 0),
			velox.EdgeThrough[DPick, DTag]("Picks", 0, 0, 1, 0),
		},
		FieldPtr: func(x *DBook, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.AuthorID
			case 2:
				return &x.Title
			}
			return nil
		},
		EdgeFieldPtr: func(x *DBook, i int) any {
			switch i {
			case 0:
				return &x.Author
			case 1:
				return &x.Tags
			case 2:
				return &x.Picks
			}
			return nil
		},
	}
}

// DTag is confined to the context's org, and has books through dBookTag.
type DTag struct {
	ID    int64
	OrgID int64
	Name  string
	Books velox.Many[DBook]
}

var DTags = struct {
	ID    velox.NumberCol[DTag, int64]
	OrgID velox.NumberCol[DTag, int64]
	Name  velox.StringCol[DTag, string]
	Books velox.Rel[DTag, DBook]
}{velox.NumberCol[DTag, int64]{I: 0}, velox.NumberCol[DTag, int64]{I: 1}, velox.StringCol[DTag, string]{I: 2}, velox.Rel[DTag, DBook]{I: 0}}

var DTagMeta velox.MetaOnce[DTag]

func (*DTag) VeloxMeta() *velox.Meta[DTag] { return DTagMeta.Load(DTagDescriptor) }

func DTagDescriptor() velox.Descriptor[DTag] {
	return velox.Descriptor[DTag]{
		Policy:     func() velox.BoundPolicy { return velox.BindPolicy((*DTag)(nil).Policy()) },
		Hooks:      func() velox.BoundHooks { return velox.BindHooks((*DTag)(nil).Hooks()) },
		Table:      "tags",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeThrough[DBookTag, DBook]("Books", 0, 1, 0, 0)},
		FieldPtr: func(x *DTag, i int) any {
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
		EdgeFieldPtr: func(x *DTag, i int) any {
			if i == 0 {
				return &x.Books
			}
			return nil
		},
	}
}

// DBookTag is the join entity of dBook.Tags and dTag.Books: a row per pair.
type DBookTag struct {
	BookID int64
	TagID  int64
}

var DBookTagMeta velox.MetaOnce[DBookTag]

func (*DBookTag) VeloxMeta() *velox.Meta[DBookTag] { return DBookTagMeta.Load(DBookTagDescriptor) }

func DBookTagDescriptor() velox.Descriptor[DBookTag] {
	return velox.Descriptor[DBookTag]{
		Table:      "book_tags",
		Columns:    []velox.ColumnDescriptor{{Field: "BookID", Name: "book_id"}, {Field: "TagID", Name: "tag_id"}},
		PrimaryKey: []int{0, 1},
		FieldPtr: func(x *DBookTag, i int) any {
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

// DPick is a second join entity between books and tags, so that dBook has
// two many-to-many relations whose SQL the render cache must keep apart.
type DPick struct {
	BookID int64
	TagID  int64
}

var DPickMeta velox.MetaOnce[DPick]

func (*DPick) VeloxMeta() *velox.Meta[DPick] { return DPickMeta.Load(DPickDescriptor) }

func DPickDescriptor() velox.Descriptor[DPick] {
	return velox.Descriptor[DPick]{
		Table:      "picks",
		Columns:    []velox.ColumnDescriptor{{Field: "BookID", Name: "book_id"}, {Field: "TagID", Name: "tag_id"}},
		PrimaryKey: []int{0, 1},
		FieldPtr: func(x *DPick, i int) any {
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
