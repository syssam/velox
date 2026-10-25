package velox_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/syssam/velox2"
)

// dAuthor is tenant-scoped, has an immutable creation time, a validated name
// and a sensitive email.
type dAuthor struct {
	ID        int64
	OrgID     int64
	Name      string
	Email     *string
	CreatedAt time.Time
	Books     velox.Many[dBook]
}

var dAuthors = struct {
	ID        velox.NumberCol[dAuthor, int64]
	OrgID     velox.NumberCol[dAuthor, int64]
	Name      velox.StringCol[dAuthor, string]
	Email     velox.NullStringCol[dAuthor, string]
	CreatedAt velox.OrderedCol[dAuthor, time.Time]
	Books     velox.Rel[dAuthor, dBook]
}{
	velox.NumberCol[dAuthor, int64]{I: 0}, velox.NumberCol[dAuthor, int64]{I: 1},
	velox.StringCol[dAuthor, string]{I: 2}, velox.NullStringCol[dAuthor, string]{I: 3},
	velox.OrderedCol[dAuthor, time.Time]{I: 4}, velox.Rel[dAuthor, dBook]{I: 0},
}

var dAuthorMeta velox.MetaOnce[dAuthor]

func (*dAuthor) VeloxMeta() *velox.Meta[dAuthor] { return dAuthorMeta.Load(dAuthorDesc) }

var errEmptyName = errors.New("must not be empty")

func dAuthorDesc() velox.Desc[dAuthor] {
	return velox.Desc[dAuthor]{
		Policy: func() velox.BoundPolicy { return velox.BindPolicy((*dAuthor)(nil).Policy()) },
		Hooks:  func() velox.BoundHooks { return velox.BindHooks((*dAuthor)(nil).Hooks()) },
		Table:  "authors",
		Columns: []velox.ColumnDesc{
			{Field: "ID", Name: "id"},
			{Field: "OrgID", Name: "org_id"},
			{Field: "Name", Name: "name"},
			{Field: "Email", Name: "email", Sensitive: true},
			{Field: "CreatedAt", Name: "created_at", DefaultFunc: func() any { return time.Now() }, Immutable: true},
		},
		PrimaryKey: []int{0},
		Tenant:     1,
		Edges:      []velox.EdgeDesc{velox.EdgeTo[dBook]("Books", 0, 1)},
		Addr: func(x *dAuthor, i int) any {
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
		EdgeAddr: func(x *dAuthor, i int) any {
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
					return errEmptyName
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

// dBook belongs to an author, and has tags and picks through join entities.
type dBook struct {
	ID       int64
	AuthorID int64
	Title    string
	Author   velox.One[dAuthor]
	Tags     velox.Many[dTag]
	Picks    velox.Many[dTag]
}

var dBooks = struct {
	ID       velox.NumberCol[dBook, int64]
	AuthorID velox.NumberCol[dBook, int64]
	Title    velox.StringCol[dBook, string]
	Author   velox.Rel[dBook, dAuthor]
	Tags     velox.Rel[dBook, dTag]
	Picks    velox.Rel[dBook, dTag]
}{
	velox.NumberCol[dBook, int64]{I: 0}, velox.NumberCol[dBook, int64]{I: 1}, velox.StringCol[dBook, string]{I: 2},
	velox.Rel[dBook, dAuthor]{I: 0}, velox.Rel[dBook, dTag]{I: 1}, velox.Rel[dBook, dTag]{I: 2},
}

var dBookMeta velox.MetaOnce[dBook]

func (*dBook) VeloxMeta() *velox.Meta[dBook] { return dBookMeta.Load(dBookDesc) }

func dBookDesc() velox.Desc[dBook] {
	return velox.Desc[dBook]{
		Hooks:      func() velox.BoundHooks { return velox.BindHooks((*dBook)(nil).Hooks()) },
		Table:      "books",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "AuthorID", Name: "author_id"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Edges: []velox.EdgeDesc{
			velox.EdgeTo[dAuthor]("Author", 1, 0),
			velox.EdgeThrough[dBookTag, dTag]("Tags", 0, 0, 1, 0),
			velox.EdgeThrough[dPick, dTag]("Picks", 0, 0, 1, 0),
		},
		Addr: func(x *dBook, i int) any {
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
		EdgeAddr: func(x *dBook, i int) any {
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

// dTag is tenant-scoped, and has books through dBookTag.
type dTag struct {
	ID    int64
	OrgID int64
	Name  string
	Books velox.Many[dBook]
}

var dTags = struct {
	ID    velox.NumberCol[dTag, int64]
	OrgID velox.NumberCol[dTag, int64]
	Name  velox.StringCol[dTag, string]
	Books velox.Rel[dTag, dBook]
}{velox.NumberCol[dTag, int64]{I: 0}, velox.NumberCol[dTag, int64]{I: 1}, velox.StringCol[dTag, string]{I: 2}, velox.Rel[dTag, dBook]{I: 0}}

var dTagMeta velox.MetaOnce[dTag]

func (*dTag) VeloxMeta() *velox.Meta[dTag] { return dTagMeta.Load(dTagDesc) }

func dTagDesc() velox.Desc[dTag] {
	return velox.Desc[dTag]{
		Table:      "tags",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		Tenant:     1,
		Edges:      []velox.EdgeDesc{velox.EdgeThrough[dBookTag, dBook]("Books", 0, 1, 0, 0)},
		Addr: func(x *dTag, i int) any {
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
		EdgeAddr: func(x *dTag, i int) any {
			if i == 0 {
				return &x.Books
			}
			return nil
		},
	}
}

// dBookTag is the join entity of dBook.Tags and dTag.Books: a row per pair.
type dBookTag struct {
	BookID int64
	TagID  int64
}

var dBookTags = struct {
	BookID velox.NumberCol[dBookTag, int64]
	TagID  velox.NumberCol[dBookTag, int64]
}{velox.NumberCol[dBookTag, int64]{I: 0}, velox.NumberCol[dBookTag, int64]{I: 1}}

var dBookTagMeta velox.MetaOnce[dBookTag]

func (*dBookTag) VeloxMeta() *velox.Meta[dBookTag] { return dBookTagMeta.Load(dBookTagDesc) }

func dBookTagDesc() velox.Desc[dBookTag] {
	return velox.Desc[dBookTag]{
		Table:      "book_tags",
		Columns:    []velox.ColumnDesc{{Field: "BookID", Name: "book_id"}, {Field: "TagID", Name: "tag_id"}},
		PrimaryKey: []int{0, 1},
		Tenant:     -1,
		Addr: func(x *dBookTag, i int) any {
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

// dPick is a second join entity between books and tags, so that dBook has
// two many-to-many relations whose SQL the render cache must keep apart.
type dPick struct {
	BookID int64
	TagID  int64
}

var dPickMeta velox.MetaOnce[dPick]

func (*dPick) VeloxMeta() *velox.Meta[dPick] { return dPickMeta.Load(dPickDesc) }

func dPickDesc() velox.Desc[dPick] {
	return velox.Desc[dPick]{
		Table:      "picks",
		Columns:    []velox.ColumnDesc{{Field: "BookID", Name: "book_id"}, {Field: "TagID", Name: "tag_id"}},
		PrimaryKey: []int{0, 1},
		Tenant:     -1,
		Addr: func(x *dPick, i int) any {
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
