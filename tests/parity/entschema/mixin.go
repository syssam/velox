package entschema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

// timeMixin mirrors velox's mixin.Time column names (created_at / updated_at)
// so the two parity schemas stay field-name-identical for differential testing.
// Ent's built-in mixin.Time would emit create_time / update_time, which would
// read as mismatched fields against the velox side.
type timeMixin struct{ mixin.Schema }

func (timeMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
