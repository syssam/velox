package gqlrelay

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	velox "github.com/syssam/velox"
)

func TestOrderDirection_Validate(t *testing.T) {
	tests := []struct {
		name    string
		dir     OrderDirection
		wantErr bool
	}{
		{"ASC valid", OrderDirectionAsc, false},
		{"DESC valid", OrderDirectionDesc, false},
		{"INVALID errors", OrderDirection("INVALID"), true},
		{"empty errors", OrderDirection(""), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.dir.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestOrderDirection_Reverse(t *testing.T) {
	assert.Equal(t, OrderDirectionDesc, OrderDirectionAsc.Reverse())
	assert.Equal(t, OrderDirectionAsc, OrderDirectionDesc.Reverse())
}

func TestOrderDirection_String(t *testing.T) {
	assert.Equal(t, "ASC", OrderDirectionAsc.String())
	assert.Equal(t, "DESC", OrderDirectionDesc.String())
}

func TestCursor_MarshalUnmarshalGQL(t *testing.T) {
	original := Cursor{
		ID:    42,
		Value: "test-value",
	}

	// Marshal to string.
	var buf bytes.Buffer
	original.MarshalGQL(&buf)
	marshaled := buf.String()

	// The marshaled value should be a quoted base64 string.
	assert.NotEmpty(t, marshaled)

	// Strip quotes for unmarshaling.
	unquoted := marshaled[1 : len(marshaled)-1]

	// Unmarshal back.
	var decoded Cursor
	err := decoded.UnmarshalGQL(unquoted)
	require.NoError(t, err)

	// The ID comes back as an interface from msgpack, so compare via type assertion.
	// msgpack decodes integers as int8/int16/int32/int64 depending on size.
	assert.EqualValues(t, 42, decoded.ID)
	assert.Equal(t, "test-value", decoded.Value)
}

func TestCursor_UnmarshalGQL_InvalidInput(t *testing.T) {
	t.Run("non-string errors", func(t *testing.T) {
		var c Cursor
		err := c.UnmarshalGQL(123)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cursor must be a string")
	})

	t.Run("bad base64 errors", func(t *testing.T) {
		var c Cursor
		err := c.UnmarshalGQL("not-valid-base64!!!")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot decode cursor")
	})
}

func TestCursor_UnmarshalGQL_EmptyString(t *testing.T) {
	var c Cursor
	err := c.UnmarshalGQL("")
	assert.NoError(t, err)
	assert.Nil(t, c.ID)
	assert.Nil(t, c.Value)
}

func TestOrderDirection_OrderTermOption(t *testing.T) {
	// Verify OrderTermOption returns a non-nil option for both directions.
	ascOpt := OrderDirectionAsc.OrderTermOption()
	assert.NotNil(t, ascOpt)

	descOpt := OrderDirectionDesc.OrderTermOption()
	assert.NotNil(t, descOpt)
}

func TestOrderDirection_MarshalGQL(t *testing.T) {
	var buf bytes.Buffer
	OrderDirectionAsc.MarshalGQL(&buf)
	assert.Equal(t, "ASC", buf.String())

	buf.Reset()
	OrderDirectionDesc.MarshalGQL(&buf)
	assert.Equal(t, "DESC", buf.String())
}

func TestOrderDirection_UnmarshalGQL(t *testing.T) {
	var dir OrderDirection

	err := dir.UnmarshalGQL("ASC")
	require.NoError(t, err)
	assert.Equal(t, OrderDirectionAsc, dir)

	err = dir.UnmarshalGQL("DESC")
	require.NoError(t, err)
	assert.Equal(t, OrderDirectionDesc, dir)
}

func TestOrderDirection_UnmarshalGQL_Invalid(t *testing.T) {
	var dir OrderDirection
	err := dir.UnmarshalGQL("INVALID")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid order direction")
}

func TestOrderDirection_UnmarshalGQL_NonString(t *testing.T) {
	var dir OrderDirection
	err := dir.UnmarshalGQL(123)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "order direction must be a string")
}

func TestCursor_MarshalGQL_EmptyCursor(t *testing.T) {
	c := Cursor{}
	var buf bytes.Buffer
	c.MarshalGQL(&buf)
	// Should produce a valid base64-encoded quoted string (not empty).
	assert.NotEmpty(t, buf.String())
	assert.True(t, buf.Len() > 2, "expected quoted string, got: %s", buf.String())
}

func TestCursor_UnmarshalGQL_InvalidMsgpack(t *testing.T) {
	// Valid base64 but invalid msgpack content.
	badData := base64.RawStdEncoding.EncodeToString([]byte{0xff, 0xfe, 0xfd})
	var c Cursor
	err := c.UnmarshalGQL(badData)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot decode cursor")
}

func TestCursor_MarshalUnmarshalGQL_MultiValues(t *testing.T) {
	original := Cursor{
		ID:     99,
		Values: []velox.Value{"alice", 25, true},
	}

	var buf bytes.Buffer
	original.MarshalGQL(&buf)
	marshaled := buf.String()
	require.NotEmpty(t, marshaled)

	// Strip quotes for unmarshaling.
	unquoted := marshaled[1 : len(marshaled)-1]

	var decoded Cursor
	err := decoded.UnmarshalGQL(unquoted)
	require.NoError(t, err)

	assert.EqualValues(t, 99, decoded.ID)
	assert.Nil(t, decoded.Value, "single Value should be nil when only Values is set")
	require.Len(t, decoded.Values, 3)
	assert.Equal(t, "alice", decoded.Values[0])
	assert.EqualValues(t, 25, decoded.Values[1])
	assert.Equal(t, true, decoded.Values[2])
}

func TestCursor_MarshalUnmarshalGQL_BackwardCompat(t *testing.T) {
	// Cursor with only single Value (no Values) should still round-trip.
	original := Cursor{
		ID:    7,
		Value: "legacy",
	}

	var buf bytes.Buffer
	original.MarshalGQL(&buf)
	unquoted := buf.String()[1 : buf.Len()-1]

	var decoded Cursor
	err := decoded.UnmarshalGQL(unquoted)
	require.NoError(t, err)

	assert.EqualValues(t, 7, decoded.ID)
	assert.Equal(t, "legacy", decoded.Value)
	assert.Nil(t, decoded.Values)
}

// Cursor.MarshalGQL error path (line 78-79): The error fallback writes `""` when
// msgpack.Marshal fails. However, msgpack can marshal virtually any Go value
// (ints, strings, nil, structs, maps, slices), so this error path is effectively
// unreachable in practice. The Cursor struct only contains ID (any) and Value
// (velox.Value = any), both of which msgpack handles. Creating a value that
// fails msgpack serialization would require a channel or func type, which are
// not valid cursor values. This is a defensive fallback for safety.
