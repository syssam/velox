package schema

import (
	"time"

	"github.com/syssam/velox/internal/testmodel/kind"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// Event has a time GoType with a Default, once a value and once Nillable: the
// generated code converts what time.Now returns to kind.Moment.
type Event struct{ schema.Schema }

// Fields returns the columns after the ID.
func (Event) Fields() []schema.Field {
	return []schema.Field{
		field.Time("at").GoType(kind.Moment{}).Default(time.Now),
		field.Time("maybe_at").GoType(kind.Moment{}).Optional().Nillable().Default(time.Now),
	}
}
