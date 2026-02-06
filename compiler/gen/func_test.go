package gen

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/schema/field"
)

func TestSnake(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Username", "username"},
		{"FullName", "full_name"},
		{"HTTPCode", "http_code"},
		{"UserID", "user_id"},
		{"XMLParser", "xml_parser"},
		{"getHTTPResponse", "get_http_response"},
		{"already_snake", "already_snake"},
		{"A", "a"},
		{"AB", "ab"},
		{"ABC", "abc"},
		{"", ""},
		{"userInfo", "user_info"},
		{"PHBOrg", "phb_org"},
		{"UserIDs", "user_ids"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := snake(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPascal(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user_info", "UserInfo"},
		{"full_name", "FullName"},
		{"user_id", "UserID"},
		{"http_code", "HTTPCode"},
		{"full-admin", "FullAdmin"},
		{"already", "Already"},
		{"a", "A"},
		{"ab", "Ab"},
		{"a_b", "AB"},
		{"xml_parser", "XMLParser"},
		{"api_url", "APIURL"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := pascal(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCamel(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"user_info", "userInfo"},
		{"full_name", "fullName"},
		{"user_id", "userID"},
		{"http_code", "httpCode"},
		{"full-admin", "fullAdmin"},
		{"already", "already"},
		{"a", "a"},
		{"user", "user"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := camel(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestReceiver(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"User", "u"},
		{"UserQuery", "uq"},
		{"[]User", "u"},
		{"[1]User", "u"},
		{"*User", "u"},
		{"HTTPClient", "hc"},
		{"A", "a"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := receiver(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPlural(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"User", "Users"},
		{"Category", "Categories"},
		{"Person", "Persons"},
		{"Child", "Childs"},
		{"Data", "DataSlice"}, // Already plural or uncountable
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := plural(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestXrange(t *testing.T) {
	tests := []struct {
		n        int
		expected []int
	}{
		{0, nil},
		{1, []int{0}},
		{3, []int{0, 1, 2}},
		{5, []int{0, 1, 2, 3, 4}},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := xrange(tt.n)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAdd(t *testing.T) {
	tests := []struct {
		input    []int
		expected int
	}{
		{[]int{}, 0},
		{[]int{1}, 1},
		{[]int{1, 2, 3}, 6},
		{[]int{-1, 1}, 0},
		{[]int{10, 20, 30, 40}, 100},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := add(tt.input...)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestQuote(t *testing.T) {
	tests := []struct {
		input    any
		expected any
	}{
		{"hello", `"hello"`},
		{"hello\nworld", `"hello\nworld"`},
		{123, 123},
		{true, true},
		{nil, nil},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := quote(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIndexOf(t *testing.T) {
	slice := []string{"a", "b", "c", "d"}

	tests := []struct {
		value    string
		expected int
	}{
		{"a", 0},
		{"b", 1},
		{"c", 2},
		{"d", 3},
		{"e", -1},
		{"", -1},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			result := indexOf(slice, tt.value)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestJoinWords(t *testing.T) {
	tests := []struct {
		words    []string
		maxSize  int
		expected string
	}{
		{[]string{}, 10, ""},
		{[]string{"hello"}, 10, "hello"},
		{[]string{"hello", "world"}, 20, "hello world"},
		{[]string{"hello", "world"}, 5, "hello\n world"},
		{[]string{"a", "b", "c"}, 3, "a b\n c"},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := joinWords(tt.words, tt.maxSize)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtend(t *testing.T) {
	t.Run("extends Type", func(t *testing.T) {
		typ := &Type{Name: "User"}
		result, err := extend(typ, "key1", "value1", "key2", "value2")

		require.NoError(t, err)
		scope, ok := result.(*typeScope)
		require.True(t, ok)
		assert.Equal(t, "User", scope.Type.Name)
		assert.Equal(t, "value1", scope.Scope["key1"])
		assert.Equal(t, "value2", scope.Scope["key2"])
	})

	t.Run("extends Graph", func(t *testing.T) {
		graph := &Graph{}
		result, err := extend(graph, "key", "value")

		require.NoError(t, err)
		scope, ok := result.(*graphScope)
		require.True(t, ok)
		assert.Equal(t, "value", scope.Scope["key"])
	})

	t.Run("extends existing typeScope", func(t *testing.T) {
		typ := &Type{Name: "User"}
		scope1, _ := extend(typ, "key1", "value1")
		result, err := extend(scope1, "key2", "value2")

		require.NoError(t, err)
		scope, ok := result.(*typeScope)
		require.True(t, ok)
		assert.Equal(t, "value1", scope.Scope["key1"])
		assert.Equal(t, "value2", scope.Scope["key2"])
	})

	t.Run("returns error for odd number of parameters", func(t *testing.T) {
		typ := &Type{Name: "User"}
		_, err := extend(typ, "key1")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid number of parameters")
	})

	t.Run("returns error for invalid type", func(t *testing.T) {
		_, err := extend("invalid", "key", "value")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid type for extend")
	})
}

func TestDict(t *testing.T) {
	t.Run("creates dictionary", func(t *testing.T) {
		result := dict("key1", "value1", "key2", 123)

		assert.Equal(t, "value1", result["key1"])
		assert.Equal(t, 123, result["key2"])
	})

	t.Run("handles odd number of arguments", func(t *testing.T) {
		result := dict("key1", "value1", "key2")

		assert.Equal(t, "value1", result["key1"])
		assert.Equal(t, "", result["key2"])
	})

	t.Run("empty dict", func(t *testing.T) {
		result := dict()

		assert.Empty(t, result)
	})
}

func TestGetSetUnset(t *testing.T) {
	t.Run("get existing key", func(t *testing.T) {
		d := map[string]any{"key": "value"}
		result := get(d, "key")
		assert.Equal(t, "value", result)
	})

	t.Run("get missing key", func(t *testing.T) {
		d := map[string]any{}
		result := get(d, "missing")
		assert.Equal(t, "", result)
	})

	t.Run("set key", func(t *testing.T) {
		d := map[string]any{}
		result := set(d, "key", "value")
		assert.Equal(t, "value", result["key"])
	})

	t.Run("unset key", func(t *testing.T) {
		d := map[string]any{"key": "value"}
		result := unset(d, "key")
		_, ok := result["key"]
		assert.False(t, ok)
	})
}

func TestHasKey(t *testing.T) {
	d := map[string]any{"key": "value"}

	assert.True(t, hasKey(d, "key"))
	assert.False(t, hasKey(d, "missing"))
}

func TestList(t *testing.T) {
	t.Run("any list", func(t *testing.T) {
		result := list[any](1, "two", true)
		assert.Equal(t, []any{1, "two", true}, result)
	})

	t.Run("string list", func(t *testing.T) {
		result := list[string]("a", "b", "c")
		assert.Equal(t, []string{"a", "b", "c"}, result)
	})

	t.Run("empty list", func(t *testing.T) {
		result := list[int]()
		assert.Empty(t, result)
	})
}

func TestFail(t *testing.T) {
	result, err := fail("test error")

	assert.Equal(t, "", result)
	require.Error(t, err)
	assert.Equal(t, "test error", err.Error())
}

func TestJSONString(t *testing.T) {
	t.Run("valid JSON", func(t *testing.T) {
		result, err := jsonString(map[string]int{"a": 1, "b": 2})

		require.NoError(t, err)
		assert.Contains(t, result, `"a":1`)
		assert.Contains(t, result, `"b":2`)
	})

	t.Run("string value", func(t *testing.T) {
		result, err := jsonString("hello")

		require.NoError(t, err)
		assert.Equal(t, `"hello"`, result)
	})
}

func TestAllZero(t *testing.T) {
	tests := []struct {
		name     string
		values   []any
		expected bool
	}{
		{"all zeros", []any{0, "", false}, true},
		{"one non-zero int", []any{0, 1}, false},
		{"one non-zero string", []any{"", "hello"}, false},
		{"empty", []any{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := allZero(tt.values...)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsNil(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected bool
	}{
		{"nil", nil, true},
		{"nil slice", []string(nil), true},
		{"nil map", map[string]int(nil), true},
		{"nil pointer", (*int)(nil), true},
		{"non-nil int", 1, false},
		{"non-nil string", "hello", false},
		{"non-nil slice", []string{"a"}, false},
		{"empty slice", []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNil(tt.value)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHasField(t *testing.T) {
	type testStruct struct {
		Name  string
		Value int
	}

	v := testStruct{Name: "test", Value: 123}

	assert.True(t, hasField(v, "Name"))
	assert.True(t, hasField(v, "Value"))
	assert.False(t, hasField(v, "Missing"))
	assert.True(t, hasField(&v, "Name"))
}

func TestHasImport(t *testing.T) {
	// Test that common imports are recognized
	assert.True(t, hasImport("context"))
	assert.True(t, hasImport("fmt"))
	assert.True(t, hasImport("errors"))
	assert.False(t, hasImport("nonexistent_package"))
}

func TestTrimPackage(t *testing.T) {
	tests := []struct {
		ident    string
		pkg      string
		expected string
	}{
		{"pkg.Type", "pkg", "Type"},
		{"other.Type", "pkg", "other.Type"},
		{"Type", "pkg", "Type"},
		{"pkg.sub.Type", "pkg", "sub.Type"},
	}

	for _, tt := range tests {
		t.Run(tt.ident, func(t *testing.T) {
			result := trimPackage(tt.ident, tt.pkg)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTagLookup(t *testing.T) {
	tag := `json:"name,omitempty" sql:"column_name"`

	assert.Equal(t, "name,omitempty", tagLookup(tag, "json"))
	assert.Equal(t, "column_name", tagLookup(tag, "sql"))
	assert.Equal(t, "", tagLookup(tag, "missing"))
}

func TestToString(t *testing.T) {
	tests := []struct {
		value    any
		expected string
	}{
		{"hello", "hello"},
		{[]byte("bytes"), "bytes"},
		{123, "123"},
		{true, "true"},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := toString(tt.value)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestKeys(t *testing.T) {
	t.Run("string keys", func(t *testing.T) {
		m := map[string]int{"b": 2, "a": 1, "c": 3}
		result, err := keys(reflect.ValueOf(m))

		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, result) // sorted
	})

	t.Run("non-map returns error", func(t *testing.T) {
		_, err := keys(reflect.ValueOf([]string{"a", "b"}))

		require.Error(t, err)
		assert.Contains(t, err.Error(), "expect map")
	})
}

func TestOrder(t *testing.T) {
	result := order()

	assert.Equal(t, "incr", result["asc"])
	assert.Equal(t, "decr", result["desc"])
}

func TestAggregate(t *testing.T) {
	result := aggregate()

	assert.True(t, result["min"])
	assert.True(t, result["max"])
	assert.True(t, result["sum"])
	assert.True(t, result["mean"])
	assert.False(t, result["count"])
}

func TestPrimitives(t *testing.T) {
	result := primitives()

	assert.Contains(t, result, "string")
	assert.Contains(t, result, "int")
	assert.Contains(t, result, "float64")
	assert.Contains(t, result, "bool")
}

func TestJoin(t *testing.T) {
	result := join([]string{"c", "a", "b"}, ",")
	assert.Equal(t, "a,b,c", result) // sorted then joined
}

func TestAddAcronym(t *testing.T) {
	// Add a custom acronym
	AddAcronym("VELOX")

	// Now pascal should treat VELOX as an acronym
	result := pascal("velox_test")
	assert.Equal(t, "VELOXTest", result)
}

func TestFieldOps(t *testing.T) {
	t.Run("nil type returns nil", func(t *testing.T) {
		f := &Field{Type: nil}
		ops := fieldOps(f)
		assert.Nil(t, ops)
	})

	t.Run("bool field", func(t *testing.T) {
		f := &Field{Type: &field.TypeInfo{Type: field.TypeBool}}
		ops := fieldOps(f)
		assert.NotNil(t, ops)
		assert.True(t, len(ops) > 0)
	})

	t.Run("string field", func(t *testing.T) {
		f := &Field{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}}
		ops := fieldOps(f)
		assert.NotNil(t, ops)
	})

	t.Run("enum field", func(t *testing.T) {
		f := &Field{Type: &field.TypeInfo{Type: field.TypeEnum}}
		ops := fieldOps(f)
		assert.NotNil(t, ops)
	})

	t.Run("int field", func(t *testing.T) {
		f := &Field{Type: &field.TypeInfo{Type: field.TypeInt}}
		ops := fieldOps(f)
		assert.NotNil(t, ops)
	})

	t.Run("optional field adds nillable ops", func(t *testing.T) {
		f := &Field{Type: &field.TypeInfo{Type: field.TypeInt}, Optional: true}
		ops := fieldOps(f)
		assert.NotNil(t, ops)
		// Optional fields should have additional nillable ops
	})

	t.Run("JSON field returns nil ops", func(t *testing.T) {
		f := &Field{Type: &field.TypeInfo{Type: field.TypeJSON}}
		ops := fieldOps(f)
		assert.Nil(t, ops)
	})
}

func TestIsSeparator(t *testing.T) {
	assert.True(t, isSeparator('_'))
	assert.True(t, isSeparator('-'))
	assert.True(t, isSeparator(' '))
	assert.True(t, isSeparator('\t'))
	assert.False(t, isSeparator('a'))
	assert.False(t, isSeparator('1'))
}
