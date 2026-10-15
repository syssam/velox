// Package model holds the start example's one entity. user_velox.go beside
// this file is written by go tool velox-gen.
package model

import "github.com/syssam/velox2"

// User is a plain struct: every exported field is a column, ID is the
// primary key, and the column names are the snake_case of the field names.
type User struct {
	ID    int64
	Name  string
	Email string
	Age   int64
}

// Schema declares what the struct cannot say: the table's name and that no
// two users share an email.
func (x *User) Schema(s *velox.Schema) {
	s.Table("users")
	s.Unique(&x.Email)
}
