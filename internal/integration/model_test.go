package integration

import "github.com/syssam/velox"

// The many-to-many model: books and tags through bookTag, written by hand as
// velox-gen writes a Descriptor, like the root package's test model. Tags are
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

func (*book) VeloxMeta() *velox.Meta[book] { return bookMeta.Load(bookDescriptor) }

func bookDescriptor() velox.Descriptor[book] {
	return velox.Descriptor[book]{
		Table:      "books",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Edges:      []velox.EdgeDescriptor{velox.EdgeThrough[bookTag, tag]("Tags", 0, 0, 1, 0)},
		FieldPtr: func(x *book, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Title
			}
			return nil
		},
		EdgeFieldPtr: func(x *book, i int) any {
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

func (*tag) VeloxMeta() *velox.Meta[tag] { return tagMeta.Load(tagDescriptor) }

func tagDescriptor() velox.Descriptor[tag] {
	return velox.Descriptor[tag]{
		Table:      "tags",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		Tenant:     1,
		Edges:      []velox.EdgeDescriptor{velox.EdgeThrough[bookTag, book]("Books", 0, 1, 0, 0)},
		FieldPtr: func(x *tag, i int) any {
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
		EdgeFieldPtr: func(x *tag, i int) any {
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

func (*bookTag) VeloxMeta() *velox.Meta[bookTag] { return bookTagMeta.Load(bookTagDescriptor) }

func bookTagDescriptor() velox.Descriptor[bookTag] {
	return velox.Descriptor[bookTag]{
		Table:      "book_tags",
		Columns:    []velox.ColumnDescriptor{{Field: "BookID", Name: "book_id"}, {Field: "TagID", Name: "tag_id"}},
		PrimaryKey: []int{0, 1},
		Tenant:     -1,
		FieldPtr: func(x *bookTag, i int) any {
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

// The bidirectional model: persons and their friends through personFriend, a
// self-referencing many-to-many edge with no inverse, so each link is two
// join rows; and each person's Spouse, a self-referencing unique edge with no
// inverse, keyed by SpouseID, which both partners' rows hold.

type person struct {
	ID       int64
	Name     string
	SpouseID *int64
	Friends  velox.Many[person]
	Spouse   velox.One[person]
}

var Persons = struct {
	ID       velox.NumberCol[person, int64]
	Name     velox.StringCol[person, string]
	SpouseID velox.NullNumberCol[person, int64]
	Friends  velox.Rel[person, person]
	Spouse   velox.Rel[person, person]
}{velox.NumberCol[person, int64]{I: 0}, velox.StringCol[person, string]{I: 1}, velox.NullNumberCol[person, int64]{I: 2},
	velox.Rel[person, person]{I: 0}, velox.Rel[person, person]{I: 1}}

var personMeta velox.MetaOnce[person]

func (*person) VeloxMeta() *velox.Meta[person] { return personMeta.Load(personDescriptor) }

func personDescriptor() velox.Descriptor[person] {
	return velox.Descriptor[person]{
		Table:      "persons",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}, {Field: "SpouseID", Name: "person_spouse"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Indexes:    []velox.IndexInfo{{Columns: []int{2}, Unique: true}},
		Edges:      []velox.EdgeDescriptor{velox.EdgeThrough[personFriend, person]("Friends", 0, 0, 1, 0), velox.EdgeBidi[person]("Spouse", 2, 0)},
		FieldPtr: func(x *person, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Name
			case 2:
				return &x.SpouseID
			}
			return nil
		},
		EdgeFieldPtr: func(x *person, i int) any {
			switch i {
			case 0:
				return &x.Friends
			case 1:
				return &x.Spouse
			}
			return nil
		},
	}
}

// personFriend is the join entity of the bidirectional edge Friends.
type personFriend struct {
	NodeID   int64
	FriendID int64
}

var PersonFriends = struct {
	NodeID   velox.NumberCol[personFriend, int64]
	FriendID velox.NumberCol[personFriend, int64]
}{velox.NumberCol[personFriend, int64]{I: 0}, velox.NumberCol[personFriend, int64]{I: 1}}

var personFriendMeta velox.MetaOnce[personFriend]

func (*personFriend) VeloxMeta() *velox.Meta[personFriend] {
	return personFriendMeta.Load(personFriendDescriptor)
}

func personFriendDescriptor() velox.Descriptor[personFriend] {
	return velox.Descriptor[personFriend]{
		Table:      "person_friends",
		Columns:    []velox.ColumnDescriptor{{Field: "NodeID", Name: "node_id"}, {Field: "FriendID", Name: "friend_id"}},
		PrimaryKey: []int{0, 1},
		Tenant:     -1,
		Bidi:       true,
		FieldPtr: func(x *personFriend, i int) any {
			switch i {
			case 0:
				return &x.NodeID
			case 1:
				return &x.FriendID
			}
			return nil
		},
	}
}
