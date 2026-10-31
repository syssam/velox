//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

type Item struct{ schema.Schema }

func (Item) Fields() []schema.Field {
	return []schema.Field{field.String("name"), field.Int("quantity")}
}
