package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// esUser is in groups through esMember, an edge schema with a key of its own
// and a note Add cannot fill; and in clubs through esSecret, an edge schema
// with a Read policy; in teams through a descriptor whose unfilled column
// is out of range; and in crews through esMember with two unfilled columns.
type esUser struct {
	ID     int64
	Groups velox.Many[esGroup]
	Clubs  velox.Many[esGroup]
	Teams  velox.Many[esGroup]
	Crews  velox.Many[esGroup]
}

var esUsers = struct {
	ID     velox.NumberCol[esUser, int64]
	Groups velox.Rel[esUser, esGroup]
	Clubs  velox.Rel[esUser, esGroup]
	Teams  velox.Rel[esUser, esGroup]
	Crews  velox.Rel[esUser, esGroup]
}{velox.NumberCol[esUser, int64]{I: 0}, velox.Rel[esUser, esGroup]{I: 0}, velox.Rel[esUser, esGroup]{I: 1}, velox.Rel[esUser, esGroup]{I: 2}, velox.Rel[esUser, esGroup]{I: 3}}

var esUserMeta velox.MetaOnce[esUser]

func (*esUser) VeloxMeta() *velox.Meta[esUser] {
	return esUserMeta.Load(func() velox.Descriptor[esUser] {
		return velox.Descriptor[esUser]{
			Table:      "es_users",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0},
			Edges: []velox.EdgeDescriptor{
				velox.EdgeThrough[esMember, esGroup]("groups", 0, 2, 3, 0, 1),
				velox.EdgeThrough[esSecret, esGroup]("clubs", 0, 1, 2, 0),
				velox.EdgeThrough[esMember, esGroup]("teams", 0, 2, 3, 0, 9),
				velox.EdgeThrough[esMember, esGroup]("crews", 0, 2, 3, 0, 0, 1),
			},
			FieldPtr:     func(x *esUser, i int) any { return &x.ID },
			EdgeFieldPtr: func(x *esUser, i int) any { return [...]any{&x.Groups, &x.Clubs, &x.Teams, &x.Crews}[i] },
		}
	})
}

type esGroup struct{ ID int64 }

var esGroupMeta velox.MetaOnce[esGroup]

func (*esGroup) VeloxMeta() *velox.Meta[esGroup] {
	return esGroupMeta.Load(func() velox.Descriptor[esGroup] {
		return velox.Descriptor[esGroup]{
			Table:      "es_groups",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *esGroup, i int) any { return &x.ID },
		}
	})
}

// esMember is the edge schema of esUser.Groups: its note has no default.
type esMember struct {
	ID              int64
	Note            string
	UserID, GroupID int64
}

var esMemberMeta velox.MetaOnce[esMember]

func (*esMember) VeloxMeta() *velox.Meta[esMember] {
	return esMemberMeta.Load(func() velox.Descriptor[esMember] {
		return velox.Descriptor[esMember]{
			Table: "es_members",
			Columns: []velox.ColumnDescriptor{
				{Field: "ID", Name: "id"}, {Field: "Note", Name: "note"}, {Field: "UserID", Name: "user_id"}, {Field: "GroupID", Name: "group_id"},
			},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *esMember, i int) any { return [...]any{&x.ID, &x.Note, &x.UserID, &x.GroupID}[i] },
		}
	})
}

// esSecret is an edge schema with a Read policy, which velox-gen refuses
// (rule 70) and the relation refuses when it resolves.
type esSecret struct{ ID, UserID, GroupID int64 }

func (*esSecret) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Predicate, error) { return velox.FieldEQ("user_id", 1), nil }}
}

var esSecretMeta velox.MetaOnce[esSecret]

func (*esSecret) VeloxMeta() *velox.Meta[esSecret] {
	return esSecretMeta.Load(func() velox.Descriptor[esSecret] {
		return velox.Descriptor[esSecret]{
			Schema:     (*esSecret)(nil),
			Table:      "es_secrets",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "UserID", Name: "user_id"}, {Field: "GroupID", Name: "group_id"}},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *esSecret, i int) any { return [...]any{&x.ID, &x.UserID, &x.GroupID}[i] },
		}
	})
}

// Add of a relation whose edge schema has a column it cannot fill fails
// before any statement, the owner's insert or update included; Remove and
// Clear still run.
func TestEdgeSchemaAddUnfilled(t *testing.T) {
	const want = "velox: engine_test.esUser: edge groups: Add cannot fill engine_test.esMember.Note, which has no default; insert engine_test.esMember rows, or give it a Default or Optional"
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	if err := db.Insert(&esUser{ID: 1}).Set(esUsers.Groups.Add(1)).Exec(ctx); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("insert: %v", err)
	}
	if _, err := db.Update[esUser]().Set(esUsers.Groups.Add(1)).Where(esUsers.ID.EQ(1)).Exec(ctx); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("update: %v", err)
	}
	const two = "Add cannot fill engine_test.esMember.ID, engine_test.esMember.Note, which have no default; insert engine_test.esMember rows, or give them a Default or Optional"
	if err := db.Insert(&esUser{ID: 1}).Set(esUsers.Crews.Add(1)).Exec(ctx); err == nil || !strings.Contains(err.Error(), two) {
		t.Errorf("two columns: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent: %s", e.Calls, e.SQL)
	}
	if _, err := db.Update[esUser]().Set(esUsers.Groups.Clear()).Where(esUsers.ID.EQ(1)).Exec(ctx); err != nil {
		t.Errorf("clear: %v", err)
	}
	if e.Calls == 0 {
		t.Error("clear sent no statement")
	}
}

// A relation through an edge schema with a Read policy, and one whose
// unfilled column is out of range, are refused when they resolve.
func TestEdgeSchemaRefusedAtResolve(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	for _, c := range []struct {
		rel  velox.Rel[esUser, esGroup]
		want string
	}{
		{esUsers.Clubs, "its join entity engine_test.esSecret has a Read policy; a join entity is read through its two sides"},
		{esUsers.Teams, "it names unfilled column 9 of engine_test.esMember, which has 4"},
	} {
		if _, err := db.Query[esUser]().Where(c.rel.Has()).Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Has: %v, want %s", err, c.want)
		}
		if _, err := db.Update[esUser]().Set(c.rel.Add(1)).Where(esUsers.ID.EQ(1)).Exec(ctx); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Add: %v, want %s", err, c.want)
		}
	}
}
