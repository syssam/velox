package schema

import (
	"errors"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"

	"example.com/matrix/types"
)

// EnumShape is shape 6.
type EnumShape struct{ velox.Schema }

// Fields of the EnumShape.
func (EnumShape) Fields() []velox.Field {
	return []velox.Field{
		field.Enum("state").Values("draft", "live", "gone").Default("draft"),
		field.Enum("mood").Values("calm", "angry").Optional().Nillable(),
		field.Enum("tier").GoType(types.Tier("")).Default(string(types.TierFree)),
		field.Enum("grade").GoType(types.Tier("")).Optional().Nillable(),
	}
}

// GoTypeShape is shape 7.
type GoTypeShape struct{ velox.Schema }

// Fields of the GoTypeShape.
func (GoTypeShape) Fields() []velox.Field {
	return []velox.Field{
		field.String("label").GoType(types.Label("")).NotEmpty().MaxLen(8).Default("lbl"),
		field.String("alias").GoType(types.Label("")).Optional().Nillable().NotEmpty(),
		field.Int("weight").GoType(types.Weight(0)).Positive().Default(1),
		field.Int("bonus").GoType(types.Weight(0)).Range(0, 10).Default(0),
	}
}

// UUIDKey is shape 8a: a UUID id generated in Go.
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
	return "sk-" + strings.Repeat("0", 3) + string(rune('a'+StringKeySeq.Add(1)-1))
}

// StringKey is shape 8b: a string id with a default and validators.
type StringKey struct{ velox.Schema }

// Fields of the StringKey.
func (StringKey) Fields() []velox.Field {
	return []velox.Field{
		field.String("id").DefaultFunc(NextStringKey).NotEmpty().MaxLen(12).Immutable(),
		field.String("name"),
	}
}

// IntKey is shape 8c: the implicit auto-increment int id.
type IntKey struct{ velox.Schema }

// Fields of the IntKey.
func (IntKey) Fields() []velox.Field {
	return []velox.Field{field.String("name")}
}

// DefaultShapeRevision is the value revision's UpdateDefault returns.
const DefaultShapeRevision = 100

// DefaultShape is shape 9.
type DefaultShape struct{ velox.Schema }

// Fields of the DefaultShape.
func (DefaultShape) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.Int("revision").Default(1).UpdateDefault(func() int { return DefaultShapeRevision }),
		field.String("note").Default("n/a"),
		field.Bool("active").Default(true),
		field.Float("ratio").Default(0.5),
		field.String("code").Immutable(),
	}
}

var codeRE = regexp.MustCompile(`^[a-z]+$`)

// ErrOdd is returned by Validated.even's custom validator.
var ErrOdd = errors.New("matrix: value must be even")

// Validated is shape 10: fields with one validator and with several.
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
	}
}

// ErrField is shape 10's awkward case: a field called err with two
// validators, the name the generated validator wiring uses for its own local.
type ErrField struct{ velox.Schema }

// Fields of the ErrField.
func (ErrField) Fields() []velox.Field {
	return []velox.Field{
		field.String("err").NotEmpty().MaxLen(5),
		field.Int("errs").Min(1).Max(9).Default(1),
	}
}

// Blob is shape 11: bytes and JSON columns in every nullability.
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
	}
}
