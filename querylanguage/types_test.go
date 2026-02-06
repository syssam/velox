package querylanguage

import (
	"database/sql/driver"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFielder(t *testing.T) {
	tests := []struct {
		input    Fielder
		expected string
	}{
		{
			input:    StringEQ("value"),
			expected: `field == "value"`,
		},
		{
			input: StringOr(
				StringEQ("a"),
				StringEQ("b"),
				StringEQ("c"),
			),
			expected: `(field == "a" || field == "b" || field == "c")`,
		},
		{
			input: StringAnd(
				StringEQ("a"),
				StringNot(
					StringOr(
						StringEQ("b"),
						StringGT("c"),
						StringNEQ("d"),
					),
				),
			),
			expected: `field == "a" && !((field == "b" || field > "c" || field != "d"))`,
		},
		{
			input:    IntGT(1),
			expected: `field > 1`,
		},
		{
			input:    IntGTE(1),
			expected: `field >= 1`,
		},
		{
			input:    IntLT(1),
			expected: `field < 1`,
		},
		{
			input:    IntLTE(1),
			expected: `field <= 1`,
		},
		{
			input:    IntGT(1),
			expected: `field > 1`,
		},
		{
			input:    IntNot(IntGTE(1)),
			expected: `!(field >= 1)`,
		},
		{
			input: BoolNot(
				BoolOr(
					BoolEQ(true),
					BoolEQ(false),
				),
			),
			expected: `!(field == true || field == false)`,
		},
	}
	for i := range tests {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			p := tests[i].input.Field("field")
			assert.Equal(t, tests[i].expected, p.String())
		})
	}
}

func TestBoolPredicates(t *testing.T) {
	tests := []struct {
		name     string
		input    BoolP
		expected string
	}{
		{"BoolNil", BoolNil(), `field == nil`},
		{"BoolNotNil", BoolNotNil(), `field != nil`},
		{"BoolEQ_true", BoolEQ(true), `field == true`},
		{"BoolEQ_false", BoolEQ(false), `field == false`},
		{"BoolNEQ", BoolNEQ(true), `field != true`},
		{"BoolAnd", BoolAnd(BoolEQ(true), BoolEQ(false)), `field == true && field == false`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestBytesPredicates(t *testing.T) {
	tests := []struct {
		name     string
		input    BytesP
		expected string
	}{
		{"BytesNil", BytesNil(), `field == nil`},
		{"BytesNotNil", BytesNotNil(), `field != nil`},
		{"BytesEQ", BytesEQ([]byte("test")), `field == "dGVzdA=="`},
		{"BytesNEQ", BytesNEQ([]byte("test")), `field != "dGVzdA=="`},
		{"BytesOr", BytesOr(BytesNil(), BytesNotNil()), `field == nil || field != nil`},
		{"BytesAnd", BytesAnd(BytesNil(), BytesNotNil()), `field == nil && field != nil`},
		{"BytesNot", BytesNot(BytesNil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestTimePredicates(t *testing.T) {
	testTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		input    TimeP
		expected string
	}{
		{"TimeNil", TimeNil(), `field == nil`},
		{"TimeNotNil", TimeNotNil(), `field != nil`},
		{"TimeEQ", TimeEQ(testTime), `field == "2024-01-01T00:00:00Z"`},
		{"TimeNEQ", TimeNEQ(testTime), `field != "2024-01-01T00:00:00Z"`},
		{"TimeLT", TimeLT(testTime), `field < "2024-01-01T00:00:00Z"`},
		{"TimeLTE", TimeLTE(testTime), `field <= "2024-01-01T00:00:00Z"`},
		{"TimeGT", TimeGT(testTime), `field > "2024-01-01T00:00:00Z"`},
		{"TimeGTE", TimeGTE(testTime), `field >= "2024-01-01T00:00:00Z"`},
		{"TimeOr", TimeOr(TimeNil(), TimeNotNil()), `field == nil || field != nil`},
		{"TimeAnd", TimeAnd(TimeNil(), TimeNotNil()), `field == nil && field != nil`},
		{"TimeNot", TimeNot(TimeNil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestUintPredicates(t *testing.T) {
	tests := []struct {
		name     string
		input    UintP
		expected string
	}{
		{"UintNil", UintNil(), `field == nil`},
		{"UintNotNil", UintNotNil(), `field != nil`},
		{"UintEQ", UintEQ(42), `field == 42`},
		{"UintNEQ", UintNEQ(42), `field != 42`},
		{"UintLT", UintLT(100), `field < 100`},
		{"UintLTE", UintLTE(100), `field <= 100`},
		{"UintGT", UintGT(0), `field > 0`},
		{"UintGTE", UintGTE(0), `field >= 0`},
		{"UintOr", UintOr(UintNil(), UintNotNil()), `field == nil || field != nil`},
		{"UintAnd", UintAnd(UintNil(), UintNotNil()), `field == nil && field != nil`},
		{"UintNot", UintNot(UintNil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestUint8Predicates(t *testing.T) {
	tests := []struct {
		name     string
		input    Uint8P
		expected string
	}{
		{"Uint8Nil", Uint8Nil(), `field == nil`},
		{"Uint8NotNil", Uint8NotNil(), `field != nil`},
		{"Uint8EQ", Uint8EQ(42), `field == 42`},
		{"Uint8NEQ", Uint8NEQ(42), `field != 42`},
		{"Uint8LT", Uint8LT(100), `field < 100`},
		{"Uint8LTE", Uint8LTE(100), `field <= 100`},
		{"Uint8GT", Uint8GT(0), `field > 0`},
		{"Uint8GTE", Uint8GTE(0), `field >= 0`},
		{"Uint8Or", Uint8Or(Uint8Nil(), Uint8NotNil()), `field == nil || field != nil`},
		{"Uint8And", Uint8And(Uint8Nil(), Uint8NotNil()), `field == nil && field != nil`},
		{"Uint8Not", Uint8Not(Uint8Nil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestUint16Predicates(t *testing.T) {
	tests := []struct {
		name     string
		input    Uint16P
		expected string
	}{
		{"Uint16Nil", Uint16Nil(), `field == nil`},
		{"Uint16NotNil", Uint16NotNil(), `field != nil`},
		{"Uint16EQ", Uint16EQ(1000), `field == 1000`},
		{"Uint16NEQ", Uint16NEQ(1000), `field != 1000`},
		{"Uint16LT", Uint16LT(65535), `field < 65535`},
		{"Uint16LTE", Uint16LTE(65535), `field <= 65535`},
		{"Uint16GT", Uint16GT(0), `field > 0`},
		{"Uint16GTE", Uint16GTE(0), `field >= 0`},
		{"Uint16Or", Uint16Or(Uint16Nil(), Uint16NotNil()), `field == nil || field != nil`},
		{"Uint16And", Uint16And(Uint16Nil(), Uint16NotNil()), `field == nil && field != nil`},
		{"Uint16Not", Uint16Not(Uint16Nil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestUint32Predicates(t *testing.T) {
	tests := []struct {
		name     string
		input    Uint32P
		expected string
	}{
		{"Uint32Nil", Uint32Nil(), `field == nil`},
		{"Uint32NotNil", Uint32NotNil(), `field != nil`},
		{"Uint32EQ", Uint32EQ(100000), `field == 100000`},
		{"Uint32NEQ", Uint32NEQ(100000), `field != 100000`},
		{"Uint32LT", Uint32LT(4294967295), `field < 4294967295`},
		{"Uint32LTE", Uint32LTE(4294967295), `field <= 4294967295`},
		{"Uint32GT", Uint32GT(0), `field > 0`},
		{"Uint32GTE", Uint32GTE(0), `field >= 0`},
		{"Uint32Or", Uint32Or(Uint32Nil(), Uint32NotNil()), `field == nil || field != nil`},
		{"Uint32And", Uint32And(Uint32Nil(), Uint32NotNil()), `field == nil && field != nil`},
		{"Uint32Not", Uint32Not(Uint32Nil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestUint64Predicates(t *testing.T) {
	tests := []struct {
		name     string
		input    Uint64P
		expected string
	}{
		{"Uint64Nil", Uint64Nil(), `field == nil`},
		{"Uint64NotNil", Uint64NotNil(), `field != nil`},
		{"Uint64EQ", Uint64EQ(1000000000), `field == 1000000000`},
		{"Uint64NEQ", Uint64NEQ(1000000000), `field != 1000000000`},
		{"Uint64LT", Uint64LT(18446744073709551615), `field < 18446744073709551615`},
		{"Uint64LTE", Uint64LTE(18446744073709551615), `field <= 18446744073709551615`},
		{"Uint64GT", Uint64GT(0), `field > 0`},
		{"Uint64GTE", Uint64GTE(0), `field >= 0`},
		{"Uint64Or", Uint64Or(Uint64Nil(), Uint64NotNil()), `field == nil || field != nil`},
		{"Uint64And", Uint64And(Uint64Nil(), Uint64NotNil()), `field == nil && field != nil`},
		{"Uint64Not", Uint64Not(Uint64Nil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestIntPredicates(t *testing.T) {
	tests := []struct {
		name     string
		input    IntP
		expected string
	}{
		{"IntNil", IntNil(), `field == nil`},
		{"IntNotNil", IntNotNil(), `field != nil`},
		{"IntEQ", IntEQ(42), `field == 42`},
		{"IntNEQ", IntNEQ(42), `field != 42`},
		{"IntOr", IntOr(IntNil(), IntNotNil()), `field == nil || field != nil`},
		{"IntAnd", IntAnd(IntNil(), IntNotNil()), `field == nil && field != nil`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestInt8Predicates(t *testing.T) {
	tests := []struct {
		name     string
		input    Int8P
		expected string
	}{
		{"Int8Nil", Int8Nil(), `field == nil`},
		{"Int8NotNil", Int8NotNil(), `field != nil`},
		{"Int8EQ", Int8EQ(42), `field == 42`},
		{"Int8NEQ", Int8NEQ(42), `field != 42`},
		{"Int8LT", Int8LT(127), `field < 127`},
		{"Int8LTE", Int8LTE(127), `field <= 127`},
		{"Int8GT", Int8GT(-128), `field > -128`},
		{"Int8GTE", Int8GTE(-128), `field >= -128`},
		{"Int8Or", Int8Or(Int8Nil(), Int8NotNil()), `field == nil || field != nil`},
		{"Int8And", Int8And(Int8Nil(), Int8NotNil()), `field == nil && field != nil`},
		{"Int8Not", Int8Not(Int8Nil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestInt16Predicates(t *testing.T) {
	tests := []struct {
		name     string
		input    Int16P
		expected string
	}{
		{"Int16Nil", Int16Nil(), `field == nil`},
		{"Int16NotNil", Int16NotNil(), `field != nil`},
		{"Int16EQ", Int16EQ(1000), `field == 1000`},
		{"Int16NEQ", Int16NEQ(1000), `field != 1000`},
		{"Int16LT", Int16LT(32767), `field < 32767`},
		{"Int16LTE", Int16LTE(32767), `field <= 32767`},
		{"Int16GT", Int16GT(-32768), `field > -32768`},
		{"Int16GTE", Int16GTE(-32768), `field >= -32768`},
		{"Int16Or", Int16Or(Int16Nil(), Int16NotNil()), `field == nil || field != nil`},
		{"Int16And", Int16And(Int16Nil(), Int16NotNil()), `field == nil && field != nil`},
		{"Int16Not", Int16Not(Int16Nil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestInt32Predicates(t *testing.T) {
	tests := []struct {
		name     string
		input    Int32P
		expected string
	}{
		{"Int32Nil", Int32Nil(), `field == nil`},
		{"Int32NotNil", Int32NotNil(), `field != nil`},
		{"Int32EQ", Int32EQ(100000), `field == 100000`},
		{"Int32NEQ", Int32NEQ(100000), `field != 100000`},
		{"Int32LT", Int32LT(2147483647), `field < 2147483647`},
		{"Int32LTE", Int32LTE(2147483647), `field <= 2147483647`},
		{"Int32GT", Int32GT(-2147483648), `field > -2147483648`},
		{"Int32GTE", Int32GTE(-2147483648), `field >= -2147483648`},
		{"Int32Or", Int32Or(Int32Nil(), Int32NotNil()), `field == nil || field != nil`},
		{"Int32And", Int32And(Int32Nil(), Int32NotNil()), `field == nil && field != nil`},
		{"Int32Not", Int32Not(Int32Nil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestInt64Predicates(t *testing.T) {
	tests := []struct {
		name     string
		input    Int64P
		expected string
	}{
		{"Int64Nil", Int64Nil(), `field == nil`},
		{"Int64NotNil", Int64NotNil(), `field != nil`},
		{"Int64EQ", Int64EQ(1000000000), `field == 1000000000`},
		{"Int64NEQ", Int64NEQ(1000000000), `field != 1000000000`},
		{"Int64LT", Int64LT(9223372036854775807), `field < 9223372036854775807`},
		{"Int64LTE", Int64LTE(9223372036854775807), `field <= 9223372036854775807`},
		{"Int64GT", Int64GT(-9223372036854775808), `field > -9223372036854775808`},
		{"Int64GTE", Int64GTE(-9223372036854775808), `field >= -9223372036854775808`},
		{"Int64Or", Int64Or(Int64Nil(), Int64NotNil()), `field == nil || field != nil`},
		{"Int64And", Int64And(Int64Nil(), Int64NotNil()), `field == nil && field != nil`},
		{"Int64Not", Int64Not(Int64Nil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestFloat32Predicates(t *testing.T) {
	tests := []struct {
		name     string
		input    Float32P
		expected string
	}{
		{"Float32Nil", Float32Nil(), `field == nil`},
		{"Float32NotNil", Float32NotNil(), `field != nil`},
		{"Float32EQ", Float32EQ(3.14), `field == 3.14`},
		{"Float32NEQ", Float32NEQ(3.14), `field != 3.14`},
		{"Float32LT", Float32LT(100.5), `field < 100.5`},
		{"Float32LTE", Float32LTE(100.5), `field <= 100.5`},
		{"Float32GT", Float32GT(0.0), `field > 0`},
		{"Float32GTE", Float32GTE(0.0), `field >= 0`},
		{"Float32Or", Float32Or(Float32Nil(), Float32NotNil()), `field == nil || field != nil`},
		{"Float32And", Float32And(Float32Nil(), Float32NotNil()), `field == nil && field != nil`},
		{"Float32Not", Float32Not(Float32Nil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestFloat64Predicates(t *testing.T) {
	tests := []struct {
		name     string
		input    Float64P
		expected string
	}{
		{"Float64Nil", Float64Nil(), `field == nil`},
		{"Float64NotNil", Float64NotNil(), `field != nil`},
		{"Float64EQ", Float64EQ(3.14159265359), `field == 3.14159265359`},
		{"Float64NEQ", Float64NEQ(3.14159265359), `field != 3.14159265359`},
		{"Float64LT", Float64LT(1e10), `field < 10000000000`},
		{"Float64LTE", Float64LTE(1e10), `field <= 10000000000`},
		{"Float64GT", Float64GT(-1e10), `field > -10000000000`},
		{"Float64GTE", Float64GTE(-1e10), `field >= -10000000000`},
		{"Float64Or", Float64Or(Float64Nil(), Float64NotNil()), `field == nil || field != nil`},
		{"Float64And", Float64And(Float64Nil(), Float64NotNil()), `field == nil && field != nil`},
		{"Float64Not", Float64Not(Float64Nil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestStringPredicates(t *testing.T) {
	tests := []struct {
		name     string
		input    StringP
		expected string
	}{
		{"StringNil", StringNil(), `field == nil`},
		{"StringNotNil", StringNotNil(), `field != nil`},
		{"StringLT", StringLT("b"), `field < "b"`},
		{"StringLTE", StringLTE("b"), `field <= "b"`},
		{"StringGT", StringGT("a"), `field > "a"`},
		{"StringGTE", StringGTE("a"), `field >= "a"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

// mockValuer implements driver.Valuer for testing
type mockValuer struct {
	val any
}

func (m mockValuer) Value() (driver.Value, error) {
	return m.val, nil
}

func TestValuePredicates(t *testing.T) {
	mv := mockValuer{val: "test"}
	tests := []struct {
		name     string
		input    ValueP
		expected string
	}{
		{"ValueNil", ValueNil(), `field == nil`},
		{"ValueNotNil", ValueNotNil(), `field != nil`},
		{"ValueEQ", ValueEQ(mv), `field == {}`},
		{"ValueNEQ", ValueNEQ(mv), `field != {}`},
		{"ValueOr", ValueOr(ValueNil(), ValueNotNil()), `field == nil || field != nil`},
		{"ValueAnd", ValueAnd(ValueNil(), ValueNotNil()), `field == nil && field != nil`},
		{"ValueNot", ValueNot(ValueNil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestOtherPredicates(t *testing.T) {
	mv := mockValuer{val: "test"}
	tests := []struct {
		name     string
		input    OtherP
		expected string
	}{
		{"OtherNil", OtherNil(), `field == nil`},
		{"OtherNotNil", OtherNotNil(), `field != nil`},
		{"OtherEQ", OtherEQ(mv), `field == {}`},
		{"OtherNEQ", OtherNEQ(mv), `field != {}`},
		{"OtherOr", OtherOr(OtherNil(), OtherNotNil()), `field == nil || field != nil`},
		{"OtherAnd", OtherAnd(OtherNil(), OtherNotNil()), `field == nil && field != nil`},
		{"OtherNot", OtherNot(OtherNil()), `!(field == nil)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}

func TestNaryComposedPredicates(t *testing.T) {
	// Test with 3+ predicates which triggers NaryExpr
	tests := []struct {
		name     string
		input    Fielder
		expected string
	}{
		{
			name:     "StringOr_3",
			input:    StringOr(StringEQ("a"), StringEQ("b"), StringEQ("c")),
			expected: `(field == "a" || field == "b" || field == "c")`,
		},
		{
			name:     "StringAnd_3",
			input:    StringAnd(StringEQ("a"), StringEQ("b"), StringEQ("c")),
			expected: `(field == "a" && field == "b" && field == "c")`,
		},
		{
			name:     "IntOr_3",
			input:    IntOr(IntEQ(1), IntEQ(2), IntEQ(3)),
			expected: `(field == 1 || field == 2 || field == 3)`,
		},
		{
			name:     "IntAnd_3",
			input:    IntAnd(IntEQ(1), IntEQ(2), IntEQ(3)),
			expected: `(field == 1 && field == 2 && field == 3)`,
		},
		{
			name:     "BoolOr_3",
			input:    BoolOr(BoolEQ(true), BoolEQ(false), BoolNil()),
			expected: `(field == true || field == false || field == nil)`,
		},
		{
			name:     "Float64Or_3",
			input:    Float64Or(Float64EQ(1.0), Float64EQ(2.0), Float64EQ(3.0)),
			expected: `(field == 1 || field == 2 || field == 3)`,
		},
		{
			name:     "TimeOr_3",
			input:    TimeOr(TimeNil(), TimeNotNil(), TimeNil()),
			expected: `(field == nil || field != nil || field == nil)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.input.Field("field")
			assert.Equal(t, tt.expected, p.String())
		})
	}
}
