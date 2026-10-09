// Package schema is the vx schema-shape matrix: one small entity (or a few)
// per shape the generator and the runtime must handle. It is adapted from
// velox's own matrix (tests/matrix/schema), without the shapes vx does not
// cover yet (hooks, policies, composite ids).
package schema

import (
	"errors"
	"net"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"

	"github.com/syssam/velox/vx/tests/shapes/types"
)

// Scalar holds every scalar field type in the three nullable spellings:
// _opt is Optional (NULL in the database, zero value in Go), _nil is
// Optional+Nillable (NULL is a nil pointer) and _ptr is Nillable alone
// (required, but a pointer in Go). The required spelling of each type is on
// Wide.
type Scalar struct{ velox.Schema }

// Fields of the Scalar.
func (Scalar) Fields() []velox.Field {
	return []velox.Field{
		field.Int("seq").Unique(),

		field.String("string_opt").Optional(),
		field.String("string_nil").Optional().Nillable(),
		field.String("string_ptr").Nillable(),
		field.Text("text_opt").Optional(),
		field.Text("text_nil").Optional().Nillable(),

		field.Int("int_opt").Optional(),
		field.Int("int_nil").Optional().Nillable(),
		field.Int("int_ptr").Nillable(),
		field.Int8("int8_opt").Optional(),
		field.Int8("int8_nil").Optional().Nillable(),
		field.Int16("int16_opt").Optional(),
		field.Int16("int16_nil").Optional().Nillable(),
		field.Int32("int32_opt").Optional(),
		field.Int32("int32_nil").Optional().Nillable(),
		field.Int64("int64_opt").Optional(),
		field.Int64("int64_nil").Optional().Nillable(),

		field.Uint("uint_opt").Optional(),
		field.Uint("uint_nil").Optional().Nillable(),
		field.Uint8("uint8_opt").Optional(),
		field.Uint8("uint8_nil").Optional().Nillable(),
		field.Uint16("uint16_opt").Optional(),
		field.Uint16("uint16_nil").Optional().Nillable(),
		field.Uint32("uint32_opt").Optional(),
		field.Uint32("uint32_nil").Optional().Nillable(),
		field.Uint64("uint64_opt").Optional(),
		field.Uint64("uint64_nil").Optional().Nillable(),

		field.Float("float_opt").Optional(),
		field.Float("float_nil").Optional().Nillable(),
		field.Float("float_ptr").Nillable(),
		field.Float32("float32_opt").Optional(),
		field.Float32("float32_nil").Optional().Nillable(),

		field.Bool("bool_opt").Optional(),
		field.Bool("bool_nil").Optional().Nillable(),
		field.Bool("bool_ptr").Nillable(),

		field.Time("time_opt").Optional(),
		field.Time("time_nil").Optional().Nillable(),
		field.Time("time_ptr").Nillable(),

		field.UUID("uuid_opt", uuid.UUID{}).Optional(),
		field.UUID("uuid_nil", uuid.UUID{}).Optional().Nillable(),
		field.UUID("uuid_ptr", uuid.UUID{}).Nillable(),

		field.Bytes("bytes_opt").Optional(),
		field.Bytes("bytes_nil").Optional().Nillable(),
		field.Bytes("bytes_ptr").Nillable(),
	}
}

// EnumShape holds enums: generated type and Go type, each with a default and
// each nillable. Two enums share a value name on purpose.
type EnumShape struct{ velox.Schema }

// Fields of the EnumShape.
func (EnumShape) Fields() []velox.Field {
	return []velox.Field{
		field.Enum("state").Values("draft", "live", "gone").Default("draft"),
		field.Enum("mood").Values("calm", "angry").Optional().Nillable(),
		field.Enum("phase").Values("draft", "in-progress", "on_hold").Default("in-progress"),
		field.Enum("size").NamedValues("Small", "S", "Large", "L"),
		field.Enum("tier").GoType(types.Tier("")).Default(string(types.TierFree)),
		field.Enum("grade").GoType(types.Tier("")).Optional().Nillable(),
	}
}

// GoTypeShape holds fields with custom Go types: named basic types, a struct
// that reaches the database through its own Valuer/Scanner, and an Other.
type GoTypeShape struct{ velox.Schema }

// Fields of the GoTypeShape.
func (GoTypeShape) Fields() []velox.Field {
	return []velox.Field{
		field.String("label").GoType(types.Label("")).NotEmpty().MaxLen(8).Default("lbl"),
		field.String("alias").GoType(types.Label("")).Optional().Nillable().NotEmpty(),
		field.String("nick").GoType(types.Label("")).Optional(),
		field.Int("weight").GoType(types.Weight(0)).Positive().Default(1),
		field.Int("bonus").GoType(types.Weight(0)).Range(0, 10).Default(0),
		field.Int("extra").GoType(types.Weight(0)).Optional(),
		field.Float("ratio").GoType(types.Ratio(0)).Default(0.5),
		field.String("price").GoType(types.Decimal{}),
		field.String("cost").GoType(types.Decimal{}).Optional(),
		field.String("fee").GoType(types.Decimal{}).Optional().Nillable(),
		field.String("tax").GoType(types.Decimal{}).DefaultFunc(func() types.Decimal { return types.Decimal{Cents: 150} }),
		field.Other("pair", types.Pair{}).SchemaType(map[string]string{dialect.SQLite: "text", dialect.Postgres: "text", dialect.MySQL: "varchar(64)"}),
		field.Other("pair_nil", types.Pair{}).SchemaType(map[string]string{dialect.SQLite: "text", dialect.Postgres: "text", dialect.MySQL: "varchar(64)"}).Optional().Nillable(),
	}
}

// NamedBool holds bool fields with a named Go type, in every nullability.
// It is an entity of its own because database/sql cannot scan into a named
// bool, which fails every query of the entity; see known_gaps_test.go.
type NamedBool struct{ velox.Schema }

// Fields of the NamedBool.
func (NamedBool) Fields() []velox.Field {
	return []velox.Field{
		field.String("label"),
		field.Bool("flag").GoType(types.Flag(false)).Default(true),
	}
}

// NamedBoolNil is NamedBool with the nillable spelling.
type NamedBoolNil struct{ velox.Schema }

// Fields of the NamedBoolNil.
func (NamedBoolNil) Fields() []velox.Field {
	return []velox.Field{
		field.String("label"),
		field.Bool("flag").GoType(types.Flag(false)).Optional().Nillable(),
	}
}

// NamedBytes holds bytes fields with a named Go type.
type NamedBytes struct{ velox.Schema }

// Fields of the NamedBytes.
func (NamedBytes) Fields() []velox.Field {
	return []velox.Field{
		field.Bytes("ip").GoType(net.IP{}),
		field.Bytes("ip_opt").GoType(net.IP{}).Optional(),
		field.Bytes("ip_nil").GoType(net.IP{}).Optional().Nillable(),
	}
}

// UUIDKey has a UUID id generated in Go.
type UUIDKey struct{ velox.Schema }

// Fields of the UUIDKey.
func (UUIDKey) Fields() []velox.Field {
	return []velox.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.String("name"),
	}
}

// StringKeySeq feeds StringKey's default id.
var StringKeySeq atomic.Int64

// NextStringKey is StringKey's id default.
func NextStringKey() string {
	return "sk-" + strconv.FormatInt(StringKeySeq.Add(1), 10)
}

// StringKey has a string id with a user-defined default func and validators.
type StringKey struct{ velox.Schema }

// Fields of the StringKey.
func (StringKey) Fields() []velox.Field {
	return []velox.Field{
		field.String("id").DefaultFunc(NextStringKey).NotEmpty().MaxLen(12).Immutable(),
		field.String("name"),
	}
}

// IntKey has the implicit auto-increment int id.
type IntKey struct{ velox.Schema }

// Fields of the IntKey.
func (IntKey) Fields() []velox.Field {
	return []velox.Field{field.String("name")}
}

// Int64Key has an explicit int64 id.
type Int64Key struct{ velox.Schema }

// Fields of the Int64Key.
func (Int64Key) Fields() []velox.Field {
	return []velox.Field{
		field.Int64("id"),
		field.String("name"),
	}
}

// Uint16Key has a small unsigned id, assigned by the database.
type Uint16Key struct{ velox.Schema }

// Fields of the Uint16Key.
func (Uint16Key) Fields() []velox.Field {
	return []velox.Field{
		field.Uint16("id"),
		field.String("name"),
	}
}

// ManualKey has a string id without a default: the caller must set it.
type ManualKey struct{ velox.Schema }

// Fields of the ManualKey.
func (ManualKey) Fields() []velox.Field {
	return []velox.Field{
		field.String("id").MaxLen(8),
		field.String("name"),
	}
}

// DefaultShapeRevision is the value revision's UpdateDefault returns.
const DefaultShapeRevision = 100

// DefaultCalls counts calls of DefaultShape's token default func.
var DefaultCalls atomic.Int64

// DefaultShape holds defaults, default funcs, update defaults and immutables.
type DefaultShape struct{ velox.Schema }

// Fields of the DefaultShape.
func (DefaultShape) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.Int("revision").Default(1).UpdateDefault(func() int { return DefaultShapeRevision }),
		field.String("note").Default("n/a"),
		field.String("opt_note").Optional().Default("opt"),
		field.String("nil_note").Optional().Nillable().Default("nil"),
		field.Bool("active").Default(true),
		field.Float("ratio").Default(0.5),
		field.Float32("ratio32").Default(0.25),
		field.Int8("tiny").Default(7),
		field.Int64("big").Default(1 << 40),
		field.Uint32("mask").Default(0xff),
		field.Bytes("blob").Default([]byte("dflt")),
		field.UUID("uid", uuid.UUID{}).Default(uuid.New),
		field.String("token").DefaultFunc(func() string {
			return "tok-" + strconv.FormatInt(DefaultCalls.Add(1), 10)
		}),
		field.Int("counter").DefaultFunc(func() int { return 42 }),
		field.JSON("tags", []string{}).Default([]string{"a", "b"}),
		field.String("code").Immutable(),
		field.Int64("touched").Optional().UpdateDefault(func() int64 { return 77 }),
		field.Time("seen").Optional().Nillable().UpdateDefault(time.Now),
	}
}

var codeRE = regexp.MustCompile(`^[a-z]+$`)

// ErrOdd is returned by Validated.even's custom validator.
var ErrOdd = errors.New("shapes: value must be even")

// Validated holds validators of every basic type, one and several per field.
type Validated struct{ velox.Schema }

// Fields of the Validated.
func (Validated) Fields() []velox.Field {
	return []velox.Field{
		field.String("one").NotEmpty(),
		field.String("two").NotEmpty().MaxLen(5),
		field.String("three").MinLen(2).MaxLen(6).Match(codeRE),
		field.Int("score").Range(0, 100),
		field.Int("rank").Min(1).Max(10),
		field.Int("even").Validate(func(v int) error {
			if v%2 != 0 {
				return ErrOdd
			}
			return nil
		}),
		field.Float("ratio").Min(0).Max(1).Optional(),
		field.Int8("i8").Positive().Default(1),
		field.Int16("i16").Min(-5).Default(0),
		field.Int32("i32").NonNegative().Default(0),
		field.Int64("i64").Max(1000).Default(0),
		field.Uint("u").Max(9).Default(0),
		field.Uint8("u8").Min(1).Default(1),
		field.Uint64("u64").Range(1, 5).Default(1),
		field.Float32("f32").Positive().Default(1),
		field.Bytes("raw").NotEmpty().MaxLen(4).Default([]byte("r")),
		field.String("opt").Optional().Nillable().MinLen(3),
		// Optional and not Nillable, with a validator its zero value fails:
		// unset, the zero value is stored without being validated.
		field.Int("opt_pos").Optional().Positive(),
		field.String("opt_ne").Optional().NotEmpty(),
		field.Bytes("opt_raw").Optional().NotEmpty(),
	}
}

// ErrField has fields named like the locals generated validator wiring uses.
type ErrField struct{ velox.Schema }

// Fields of the ErrField.
func (ErrField) Fields() []velox.Field {
	return []velox.Field{
		field.String("err").NotEmpty().MaxLen(5),
		field.Int("errs").Min(1).Max(9).Default(1),
		field.String("fn").NotEmpty().MaxLen(5).Default("fn"),
		field.String("fns").NotEmpty().MaxLen(5).Default("fns"),
		field.String("def").NotEmpty().Default("def"),
		field.String("checks").NotEmpty().Default("c"),
		field.String("m").NotEmpty().Default("m"),
		field.String("d").NotEmpty().Default("d"),
		field.String("f").NotEmpty().Default("f"),
		field.String("v").NotEmpty().Default("v"),
		field.String("fields").NotEmpty().Default("fs"),
		field.String("mixin").NotEmpty().Default("mx"),
	}
}

// Blob holds bytes and JSON columns in every nullability.
type Blob struct{ velox.Schema }

// Fields of the Blob.
func (Blob) Fields() []velox.Field {
	return []velox.Field{
		field.Int("seq").Unique(),
		field.Bytes("data"),
		field.Bytes("opt").Optional(),
		field.Bytes("optd").Optional().Default([]byte("dflt")),
		field.Bytes("nilb").Optional().Nillable(),
		field.String("nils").Optional().Nillable(),
		field.JSON("addr", types.Address{}).Optional(),
		field.JSON("addr_ptr", &types.Address{}).Optional(),
		field.JSON("tags", []string{}).Optional(),
		field.JSON("attrs", map[string]int{}).Optional(),
		field.JSON("req", types.Address{}),
		field.Strings("strs").Optional(),
		field.Ints("ints").Optional(),
		field.Floats("floats").Optional(),
		field.Any("anything").Optional(),
		field.JSON("free", map[string]any{}).Optional(),
	}
}

// Renamed holds fields whose column differs from their schema name, on a
// table that differs from the entity name.
type Renamed struct{ velox.Schema }

// Fields of the Renamed.
func (Renamed) Fields() []velox.Field {
	return []velox.Field{
		field.String("title").StorageKey("ttl").NotEmpty().Default("untitled"),
		field.Int("count").StorageKey("cnt").Optional().Nillable(),
		field.Time("seen_at").StorageKey("seen").Optional().UpdateDefault(time.Now),
		field.Enum("kind").Values("a", "b").StorageKey("knd").Default("a"),
		field.String("secret").Sensitive().Optional(),
		field.String("alias").Optional().StructTag(`json:"aka,omitempty" xml:"aka,attr"`),
	}
}

// Edges of the Renamed: to-many edges that are immutable (set on create,
// never changed).
func (Renamed) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("frozen", Note.Type).Immutable(),
		edge.To("frozen_topics", Topic.Type).Immutable(),
	}
}

// Bare has no field but its id.
type Bare struct{ velox.Schema }
