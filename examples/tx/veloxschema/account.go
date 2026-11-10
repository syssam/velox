// Package veloxschema declares the tx example's entity. go tool velox-gen reads it
// and writes the veloxgen package beside it.
package veloxschema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// Account holds a balance in cents. No two accounts share a name.
type Account struct{ schema.Schema }

// Fields returns the columns after the ID.
func (Account) Fields() []schema.Field {
	return []schema.Field{
		field.String("name").Unique(),
		field.Int64("balance"),
	}
}
