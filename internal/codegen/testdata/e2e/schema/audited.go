//go:build ignore

package schema

import (
	"errors"
	"strings"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// Audited records who created a row; the name must be lower case.
type Audited struct{ mixin.Schema }

func (Audited) Fields() []schema.Field {
	return []schema.Field{
		field.String("created_by").MinLen(2).Validate(func(s string) error {
			if s != strings.ToLower(s) {
				return errors.New("must be lower case")
			}
			return nil
		}),
	}
}
