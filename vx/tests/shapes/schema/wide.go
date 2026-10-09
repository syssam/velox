package schema

import (
	"time"

	"github.com/google/uuid"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"

	"github.com/syssam/velox/vx/tests/shapes/types"
)

// Wide has 40 columns of mixed types, every type in its required spelling,
// so a bulk insert or a scan that misaligns a column shows up as a wrong
// value somewhere.
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
		field.String("price").GoType(types.Decimal{}),
		field.Enum("tier").GoType(types.Tier("")),
		field.Int("n1").Default(1),
		field.Int("n2").Optional(),
		field.String("tail"),
	}
}

// AuditMixin contributes a field with a default and a validator, and a
// field with a default func.
type AuditMixin struct{ mixin.Schema }

// Fields of the AuditMixin.
func (AuditMixin) Fields() []velox.Field {
	return []velox.Field{
		field.String("created_by").NotEmpty().Default("system"),
		field.Int("version").Default(1).UpdateDefault(func() int { return 2 }),
	}
}

// FlagMixin contributes an optional field without a default and one with.
type FlagMixin struct{ mixin.Schema }

// Fields of the FlagMixin.
func (FlagMixin) Fields() []velox.Field {
	return []velox.Field{
		field.Bool("archived").Default(false),
		field.Time("archived_at").Optional().Nillable(),
		field.String("reason").Optional().MaxLen(4),
	}
}

// MixedIn takes fields from three mixins and has its own on top; the
// defaults and validators of each must come from the right descriptor.
type MixedIn struct{ velox.Schema }

// Mixin of the MixedIn.
func (MixedIn) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}, AuditMixin{}, FlagMixin{}}
}

// Fields of the MixedIn.
func (MixedIn) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty(),
		field.String("slug").Default("slug").MaxLen(6),
		field.Time("due").Default(func() time.Time { return time.Unix(1000, 0).UTC() }),
	}
}

// MixinOnly has no fields of its own.
type MixinOnly struct{ velox.Schema }

// Mixin of the MixinOnly.
func (MixinOnly) Mixin() []velox.Mixin {
	return []velox.Mixin{AuditMixin{}}
}
