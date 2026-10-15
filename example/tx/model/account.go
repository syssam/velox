// Package model holds the tx example's entity. account_velox.go beside this
// file is written by go tool velox-gen.
package model

import "github.com/syssam/velox2"

// Account holds a balance in cents. No two accounts share a name.
type Account struct {
	ID      int64
	Name    string
	Balance int64
}

// Schema names the table and the unique key.
func (x *Account) Schema(s *velox.Schema) {
	s.Table("accounts")
	s.Unique(&x.Name)
}
