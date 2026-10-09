package schema

import (
	"github.com/google/uuid"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"

	"example.com/matrix/types"
)

// Wide is shape 12: 30+ columns of mixed types, so a bulk insert or an
// upsert that misaligns a column shows up as a wrong value somewhere.
type Wide struct{ velox.Schema }

// Fields of the Wide.
func (Wide) Fields() []velox.Field {
	return []velox.Field{
		field.String("key").Unique(),
		field.String("s1"),
		field.String("s2").Optional(),
		field.String("s3").Optional().Nillable(),
		field.Text("t1"),
		field.Bool("b1"),
		field.Bool("b2").Optional().Nillable(),
		field.Int("i1"),
		field.Int("i2").Optional().Nillable(),
		field.Int8("i8"),
		field.Int16("i16"),
		field.Int32("i32"),
		field.Int64("i64"),
		field.Uint("u1"),
		field.Uint8("u8"),
		field.Uint16("u16"),
		field.Uint32("u32"),
		field.Uint64("u64"),
		field.Float("f64"),
		field.Float32("f32"),
		field.Float("f3").Optional().Nillable(),
		field.Time("at"),
		field.Time("at2").Optional().Nillable(),
		field.UUID("uid", uuid.UUID{}),
		field.UUID("uid2", uuid.UUID{}).Optional().Nillable(),
		field.Bytes("raw"),
		field.Bytes("raw2").Optional().Nillable(),
		field.Enum("kind").Values("a", "b", "c"),
		field.Enum("kind2").Values("x", "y").Optional().Nillable(),
		field.JSON("list", []int{}),
		field.JSON("dict", map[string]string{}).Optional(),
		field.JSON("addr", types.Address{}).Optional(),
		field.String("label").GoType(types.Label("")),
		field.Int("weight").GoType(types.Weight(0)),
		field.String("dflt").Default("d"),
	}
}
