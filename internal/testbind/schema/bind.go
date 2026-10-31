// Package schema is the binding model's DSL schema: one entity with a field
// of every column type the planner writes.
package schema

import (
	"github.com/syssam/velox2/internal/testbind/bindtype"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

// Bind has one field of each type of the planner's type table.
type Bind struct{ schema.Schema }

// Fields returns the columns after the ID.
func (Bind) Fields() []schema.Field {
	return []schema.Field{
		field.Bool("flag"),
		field.Int8("i8"),
		field.Int16("i16"),
		field.Int32("i32"),
		field.Int64("i64"),
		field.Uint8("u8"),
		field.Uint16("u16"),
		field.Uint32("u32"),
		field.Uint64("u64"),
		field.Float32("f32"),
		field.Float("f64"),
		field.String("str"),
		field.String("short").MaxLen(20),
		field.Text("body"),
		field.Enum("kind").Values("a", "b"),
		field.EnumOf[bindtype.Level]("level"),
		field.Bytes("data"),
		field.Bytes("short_data").MaxLen(16),
		field.Time("at"),
		field.UUID("uid", bindtype.UUID{}),
		field.Strings("tags").Optional(),
		field.JSON("doc", bindtype.Doc{}),
		field.String("note").Optional().Nillable(),
	}
}
