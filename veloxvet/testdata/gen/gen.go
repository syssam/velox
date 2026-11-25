// Package gen is a hand-written entity in velox-gen's shape, whose columns
// have the defaults the zerodefault analyzer looks for.
package gen

import "github.com/syssam/velox"

// User is an entity with a bool, a number and a string default.
type User struct { // want User:"Defaults"
	ID     int64
	Active bool
	Seats  int
	Plan   string
	Token  string
	Note   string
	Nick   *string
	Name   string
	Rate   float64
	Big    float64
}

// Label is a method of the value, which cannot change it.
func (u User) Label() string { return u.Name }

// UserHandles is the type of Users.
type UserHandles struct {
	Active velox.Col[User, bool]
}

// Users holds User's handles.
var Users = UserHandles{Active: velox.Col[User, bool]{I: 1}}

var userMeta velox.MetaOnce[User]

// VeloxMeta is called by velox.
func (*User) VeloxMeta() *velox.Meta[User] { return userMeta.Load(userDescriptor) }

func userDescriptor() velox.Descriptor[User] {
	return velox.Descriptor[User]{
		Table: "users",
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"},
			{Field: "Active", Name: "active", Default: true},
			{Field: "Seats", Name: "seats", Default: 100},
			{Field: "Plan", Name: "plan", Default: "free"},
			{Field: "Token", Name: "token", DefaultFunc: func() any { return "t" }},
			{Field: "Note", Name: "note", Default: ""},
			{Field: "Nick", Name: "nick", Default: "anon"},
			{Field: "Name", Name: "name"},
			{Field: "Rate", Name: "rate", Default: 1.5},
			{Field: "Big", Name: "big", Default: 2e30},
		},
		PrimaryKey: []int{0},
	}
}

// Plain has no defaults, so it gets no fact.
type Plain struct {
	ID int64
}

var plainMeta velox.MetaOnce[Plain]

// VeloxMeta is called by velox.
func (*Plain) VeloxMeta() *velox.Meta[Plain] { return plainMeta.Load(plainDescriptor) }

func plainDescriptor() velox.Descriptor[Plain] {
	return velox.Descriptor[Plain]{Table: "plains", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}}
}
