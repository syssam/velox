package velox_test

import (
	"errors"
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
		Table: "authors",
		Columns: []velox.ColumnDesc{
			{Field: "ID", Name: "id"},
			{Field: "OrgID", Name: "org_id"},
			{Field: "Name", Name: "name"},
			{Field: "Email", Name: "email", Sensitive: true},
			{Field: "CreatedAt", Name: "created_at", DefaultFunc: func() any { return time.Now() }, Immutable: true},
		},
		PrimaryKey: []int{0},
		Tenant:     1,
		Relations:  []velox.Relation{velox.EdgeTo[dBook]("Books", 0, 1)},
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
		RelAddr: func(x *dAuthor, i int) any {
			if i == 0 {
				return &x.Books
			}
			return nil
		},
		Check: func(col int, v any) error {
			if col == 2 {
				if s := v.(string); s == "" {
					return errEmptyName
				} else if len(s) > 20 {
					return errors.New("longer than 20")
				}
			}
			return nil
		},
	}
}

// dBook belongs to an author.
type dBook struct {
	ID       int64
	AuthorID int64
	Title    string
	Author   velox.One[dAuthor]
}

var dBooks = struct {
	ID       velox.NumberCol[dBook, int64]
	AuthorID velox.NumberCol[dBook, int64]
	Title    velox.StringCol[dBook, string]
	Author   velox.Rel[dBook, dAuthor]
}{velox.NumberCol[dBook, int64]{I: 0}, velox.NumberCol[dBook, int64]{I: 1}, velox.StringCol[dBook, string]{I: 2}, velox.Rel[dBook, dAuthor]{I: 0}}

var dBookMeta velox.MetaOnce[dBook]

func (*dBook) VeloxMeta() *velox.Meta[dBook] { return dBookMeta.Load(dBookDesc) }

func dBookDesc() velox.Desc[dBook] {
	return velox.Desc[dBook]{
		Table:      "books",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "AuthorID", Name: "author_id"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Relations:  []velox.Relation{velox.EdgeTo[dAuthor]("Author", 1, 0)},
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
		RelAddr: func(x *dBook, i int) any {
			if i == 0 {
				return &x.Author
			}
			return nil
		},
	}
}
