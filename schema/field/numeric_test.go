package field_test

import (
	"database/sql"
	"database/sql/driver"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/schema/field"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Numeric Builder Registry (Go 1.22+ - No loop variable capture needed)
// =============================================================================

// numericBuilder holds a numeric field builder with its metadata.
type numericBuilder struct {
	name         string
	fieldType    field.Type
	builder      func(string) velox.Field
	rangeBuilder func(string) *field.Descriptor
	minBuilder   func(string) *field.Descriptor
	maxBuilder   func(string) *field.Descriptor
}

// numericBuilders returns all numeric field builders for testing.
func numericBuilders() []numericBuilder {
	return []numericBuilder{
		{"Int8", field.TypeInt8, func(n string) velox.Field { return field.Int8(n) },
			func(n string) *field.Descriptor { return field.Int8(n).Range(-10, 10).Descriptor() },
			func(n string) *field.Descriptor { return field.Int8(n).Min(-5).Descriptor() },
			func(n string) *field.Descriptor { return field.Int8(n).Max(5).Descriptor() }},
		{"Int16", field.TypeInt16, func(n string) velox.Field { return field.Int16(n) },
			func(n string) *field.Descriptor { return field.Int16(n).Range(-1000, 1000).Descriptor() },
			func(n string) *field.Descriptor { return field.Int16(n).Min(-500).Descriptor() },
			func(n string) *field.Descriptor { return field.Int16(n).Max(500).Descriptor() }},
		{"Int32", field.TypeInt32, func(n string) velox.Field { return field.Int32(n) },
			func(n string) *field.Descriptor { return field.Int32(n).Range(-100000, 100000).Descriptor() },
			func(n string) *field.Descriptor { return field.Int32(n).Min(-50000).Descriptor() },
			func(n string) *field.Descriptor { return field.Int32(n).Max(50000).Descriptor() }},
		{"Int64", field.TypeInt64, func(n string) velox.Field { return field.Int64(n) },
			func(n string) *field.Descriptor { return field.Int64(n).Range(-1000000, 1000000).Descriptor() },
			func(n string) *field.Descriptor { return field.Int64(n).Min(-500000).Descriptor() },
			func(n string) *field.Descriptor { return field.Int64(n).Max(500000).Descriptor() }},
		{"Uint8", field.TypeUint8, func(n string) velox.Field { return field.Uint8(n) },
			func(n string) *field.Descriptor { return field.Uint8(n).Range(10, 200).Descriptor() },
			func(n string) *field.Descriptor { return field.Uint8(n).Min(10).Descriptor() },
			func(n string) *field.Descriptor { return field.Uint8(n).Max(200).Descriptor() }},
		{"Uint16", field.TypeUint16, func(n string) velox.Field { return field.Uint16(n) },
			func(n string) *field.Descriptor { return field.Uint16(n).Range(1024, 65535).Descriptor() },
			func(n string) *field.Descriptor { return field.Uint16(n).Min(1024).Descriptor() },
			func(n string) *field.Descriptor { return field.Uint16(n).Max(49151).Descriptor() }},
		{"Uint32", field.TypeUint32, func(n string) velox.Field { return field.Uint32(n) },
			func(n string) *field.Descriptor { return field.Uint32(n).Range(0, 1000000).Descriptor() },
			func(n string) *field.Descriptor { return field.Uint32(n).Min(100).Descriptor() },
			func(n string) *field.Descriptor { return field.Uint32(n).Max(999999).Descriptor() }},
		{"Uint64", field.TypeUint64, func(n string) velox.Field { return field.Uint64(n) },
			func(n string) *field.Descriptor { return field.Uint64(n).Range(0, 1000000000).Descriptor() },
			func(n string) *field.Descriptor { return field.Uint64(n).Min(100).Descriptor() },
			func(n string) *field.Descriptor { return field.Uint64(n).Max(999999).Descriptor() }},
	}
}

// =============================================================================
// Core Tests
// =============================================================================

// TestNumericBuildersUnified tests all numeric field builders using a unified approach.
func TestNumericBuildersUnified(t *testing.T) {
	t.Parallel()

	for _, nb := range numericBuilders() {
		t.Run(nb.name, func(t *testing.T) {
			t.Parallel()

			t.Run("Basic", func(t *testing.T) {
				t.Parallel()
				fd := nb.builder("test").Descriptor()
				assert.Equal(t, "test", fd.Name)
				assert.Equal(t, nb.fieldType, fd.Info.Type)
			})

			t.Run("Range", func(t *testing.T) {
				t.Parallel()
				fd := nb.rangeBuilder("test")
				require.Len(t, fd.Validators, 1)
			})

			t.Run("Min", func(t *testing.T) {
				t.Parallel()
				fd := nb.minBuilder("test")
				require.Len(t, fd.Validators, 1)
			})

			t.Run("Max", func(t *testing.T) {
				t.Parallel()
				fd := nb.maxBuilder("test")
				require.Len(t, fd.Validators, 1)
			})
		})
	}
}

// =============================================================================
// Boolean Property Tests (Consolidated)
// =============================================================================

// TestNumericFieldBooleanProperties tests Unique, Optional, Nillable, Immutable.
func TestNumericFieldBooleanProperties(t *testing.T) {
	t.Parallel()

	// Each test case produces a descriptor with a specific boolean property set
	tests := []struct {
		name    string
		builder func() *field.Descriptor
		check   func(*field.Descriptor) bool
	}{
		// Unique
		{"Int8/Unique", func() *field.Descriptor { return field.Int8("v").Unique().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Unique }},
		{"Int16/Unique", func() *field.Descriptor { return field.Int16("v").Unique().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Unique }},
		{"Int32/Unique", func() *field.Descriptor { return field.Int32("v").Unique().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Unique }},
		{"Int64/Unique", func() *field.Descriptor { return field.Int64("v").Unique().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Unique }},
		{"Uint8/Unique", func() *field.Descriptor { return field.Uint8("v").Unique().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Unique }},
		{"Uint16/Unique", func() *field.Descriptor { return field.Uint16("v").Unique().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Unique }},
		{"Uint32/Unique", func() *field.Descriptor { return field.Uint32("v").Unique().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Unique }},
		{"Uint64/Unique", func() *field.Descriptor { return field.Uint64("v").Unique().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Unique }},
		// Optional
		{"Int8/Optional", func() *field.Descriptor { return field.Int8("v").Optional().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Optional }},
		{"Int16/Optional", func() *field.Descriptor { return field.Int16("v").Optional().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Optional }},
		{"Int32/Optional", func() *field.Descriptor { return field.Int32("v").Optional().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Optional }},
		{"Int64/Optional", func() *field.Descriptor { return field.Int64("v").Optional().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Optional }},
		{"Uint8/Optional", func() *field.Descriptor { return field.Uint8("v").Optional().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Optional }},
		{"Uint16/Optional", func() *field.Descriptor { return field.Uint16("v").Optional().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Optional }},
		{"Uint32/Optional", func() *field.Descriptor { return field.Uint32("v").Optional().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Optional }},
		{"Uint64/Optional", func() *field.Descriptor { return field.Uint64("v").Optional().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Optional }},
		// Nillable
		{"Int8/Nillable", func() *field.Descriptor { return field.Int8("v").Nillable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Nillable }},
		{"Int16/Nillable", func() *field.Descriptor { return field.Int16("v").Nillable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Nillable }},
		{"Int32/Nillable", func() *field.Descriptor { return field.Int32("v").Nillable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Nillable }},
		{"Int64/Nillable", func() *field.Descriptor { return field.Int64("v").Nillable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Nillable }},
		{"Uint8/Nillable", func() *field.Descriptor { return field.Uint8("v").Nillable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Nillable }},
		{"Uint16/Nillable", func() *field.Descriptor { return field.Uint16("v").Nillable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Nillable }},
		{"Uint32/Nillable", func() *field.Descriptor { return field.Uint32("v").Nillable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Nillable }},
		{"Uint64/Nillable", func() *field.Descriptor { return field.Uint64("v").Nillable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Nillable }},
		// Immutable
		{"Int8/Immutable", func() *field.Descriptor { return field.Int8("v").Immutable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Immutable }},
		{"Int16/Immutable", func() *field.Descriptor { return field.Int16("v").Immutable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Immutable }},
		{"Int32/Immutable", func() *field.Descriptor { return field.Int32("v").Immutable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Immutable }},
		{"Int64/Immutable", func() *field.Descriptor { return field.Int64("v").Immutable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Immutable }},
		{"Uint8/Immutable", func() *field.Descriptor { return field.Uint8("v").Immutable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Immutable }},
		{"Uint16/Immutable", func() *field.Descriptor { return field.Uint16("v").Immutable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Immutable }},
		{"Uint32/Immutable", func() *field.Descriptor { return field.Uint32("v").Immutable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Immutable }},
		{"Uint64/Immutable", func() *field.Descriptor { return field.Uint64("v").Immutable().Descriptor() }, func(fd *field.Descriptor) bool { return fd.Immutable }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fd := tt.builder()
			assert.True(t, tt.check(fd))
		})
	}
}

// =============================================================================
// String Property Tests (Consolidated)
// =============================================================================

// TestNumericFieldStringProperties tests Comment, StructTag, StorageKey.
func TestNumericFieldStringProperties(t *testing.T) {
	t.Parallel()

	const (
		comment    = "test comment"
		tag        = `json:"value,omitempty"`
		storageKey = "custom_column"
	)

	tests := []struct {
		name     string
		builder  func() *field.Descriptor
		expected string
		getter   func(*field.Descriptor) string
	}{
		// Comment
		{"Int8/Comment", func() *field.Descriptor { return field.Int8("v").Comment(comment).Descriptor() }, comment, func(fd *field.Descriptor) string { return fd.Comment }},
		{"Int16/Comment", func() *field.Descriptor { return field.Int16("v").Comment(comment).Descriptor() }, comment, func(fd *field.Descriptor) string { return fd.Comment }},
		{"Int32/Comment", func() *field.Descriptor { return field.Int32("v").Comment(comment).Descriptor() }, comment, func(fd *field.Descriptor) string { return fd.Comment }},
		{"Int64/Comment", func() *field.Descriptor { return field.Int64("v").Comment(comment).Descriptor() }, comment, func(fd *field.Descriptor) string { return fd.Comment }},
		{"Uint8/Comment", func() *field.Descriptor { return field.Uint8("v").Comment(comment).Descriptor() }, comment, func(fd *field.Descriptor) string { return fd.Comment }},
		{"Uint16/Comment", func() *field.Descriptor { return field.Uint16("v").Comment(comment).Descriptor() }, comment, func(fd *field.Descriptor) string { return fd.Comment }},
		{"Uint32/Comment", func() *field.Descriptor { return field.Uint32("v").Comment(comment).Descriptor() }, comment, func(fd *field.Descriptor) string { return fd.Comment }},
		{"Uint64/Comment", func() *field.Descriptor { return field.Uint64("v").Comment(comment).Descriptor() }, comment, func(fd *field.Descriptor) string { return fd.Comment }},
		// StructTag
		{"Int8/StructTag", func() *field.Descriptor { return field.Int8("v").StructTag(tag).Descriptor() }, tag, func(fd *field.Descriptor) string { return fd.Tag }},
		{"Int16/StructTag", func() *field.Descriptor { return field.Int16("v").StructTag(tag).Descriptor() }, tag, func(fd *field.Descriptor) string { return fd.Tag }},
		{"Int32/StructTag", func() *field.Descriptor { return field.Int32("v").StructTag(tag).Descriptor() }, tag, func(fd *field.Descriptor) string { return fd.Tag }},
		{"Int64/StructTag", func() *field.Descriptor { return field.Int64("v").StructTag(tag).Descriptor() }, tag, func(fd *field.Descriptor) string { return fd.Tag }},
		{"Uint8/StructTag", func() *field.Descriptor { return field.Uint8("v").StructTag(tag).Descriptor() }, tag, func(fd *field.Descriptor) string { return fd.Tag }},
		{"Uint16/StructTag", func() *field.Descriptor { return field.Uint16("v").StructTag(tag).Descriptor() }, tag, func(fd *field.Descriptor) string { return fd.Tag }},
		{"Uint32/StructTag", func() *field.Descriptor { return field.Uint32("v").StructTag(tag).Descriptor() }, tag, func(fd *field.Descriptor) string { return fd.Tag }},
		{"Uint64/StructTag", func() *field.Descriptor { return field.Uint64("v").StructTag(tag).Descriptor() }, tag, func(fd *field.Descriptor) string { return fd.Tag }},
		// StorageKey
		{"Int8/StorageKey", func() *field.Descriptor { return field.Int8("v").StorageKey(storageKey).Descriptor() }, storageKey, func(fd *field.Descriptor) string { return fd.StorageKey }},
		{"Int16/StorageKey", func() *field.Descriptor { return field.Int16("v").StorageKey(storageKey).Descriptor() }, storageKey, func(fd *field.Descriptor) string { return fd.StorageKey }},
		{"Int32/StorageKey", func() *field.Descriptor { return field.Int32("v").StorageKey(storageKey).Descriptor() }, storageKey, func(fd *field.Descriptor) string { return fd.StorageKey }},
		{"Int64/StorageKey", func() *field.Descriptor { return field.Int64("v").StorageKey(storageKey).Descriptor() }, storageKey, func(fd *field.Descriptor) string { return fd.StorageKey }},
		{"Uint8/StorageKey", func() *field.Descriptor { return field.Uint8("v").StorageKey(storageKey).Descriptor() }, storageKey, func(fd *field.Descriptor) string { return fd.StorageKey }},
		{"Uint16/StorageKey", func() *field.Descriptor { return field.Uint16("v").StorageKey(storageKey).Descriptor() }, storageKey, func(fd *field.Descriptor) string { return fd.StorageKey }},
		{"Uint32/StorageKey", func() *field.Descriptor { return field.Uint32("v").StorageKey(storageKey).Descriptor() }, storageKey, func(fd *field.Descriptor) string { return fd.StorageKey }},
		{"Uint64/StorageKey", func() *field.Descriptor { return field.Uint64("v").StorageKey(storageKey).Descriptor() }, storageKey, func(fd *field.Descriptor) string { return fd.StorageKey }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fd := tt.builder()
			assert.Equal(t, tt.expected, tt.getter(fd))
		})
	}
}

// =============================================================================
// Validator Tests
// =============================================================================

// TestInt8ValidatorsComprehensive provides detailed validator testing for Int8.
func TestInt8ValidatorsComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("Range", func(t *testing.T) {
		t.Parallel()
		fd := field.Int8("val").Range(-10, 10).Descriptor()
		v := fd.Validators[0].(func(int8) error)
		assert.NoError(t, v(-10))
		assert.NoError(t, v(0))
		assert.NoError(t, v(10))
		assert.Error(t, v(-11))
		assert.Error(t, v(11))
	})

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()
		fd := field.Int8("val").Positive().Descriptor()
		v := fd.Validators[0].(func(int8) error)
		assert.NoError(t, v(1))
		assert.NoError(t, v(127))
		assert.Error(t, v(0))
		assert.Error(t, v(-1))
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()
		fd := field.Int8("val").Negative().Descriptor()
		v := fd.Validators[0].(func(int8) error)
		assert.NoError(t, v(-1))
		assert.NoError(t, v(-128))
		assert.Error(t, v(0))
		assert.Error(t, v(1))
	})

	t.Run("NonNegative", func(t *testing.T) {
		t.Parallel()
		fd := field.Int8("val").NonNegative().Descriptor()
		v := fd.Validators[0].(func(int8) error)
		assert.NoError(t, v(0))
		assert.NoError(t, v(127))
		assert.Error(t, v(-1))
	})
}

// TestUint8ValidatorsComprehensive provides detailed validator testing for Uint8.
func TestUint8ValidatorsComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("Range", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint8("val").Range(10, 200).Descriptor()
		v := fd.Validators[0].(func(uint8) error)
		assert.NoError(t, v(10))
		assert.NoError(t, v(100))
		assert.NoError(t, v(200))
		assert.Error(t, v(9))
		assert.Error(t, v(201))
	})

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint8("val").Positive().Descriptor()
		v := fd.Validators[0].(func(uint8) error)
		assert.NoError(t, v(1))
		assert.NoError(t, v(255))
		assert.Error(t, v(0))
	})
}

// =============================================================================
// Annotation, SchemaType, Deprecation Tests
// =============================================================================

// TestNumericFieldAnnotations tests annotation support across all numeric types.
func TestNumericFieldAnnotations(t *testing.T) {
	t.Parallel()

	ann := &field.Annotation{StructTag: map[string]string{"json": `"test"`}}

	tests := []struct {
		name    string
		builder func() *field.Descriptor
	}{
		{"Int8", func() *field.Descriptor { return field.Int8("v").Annotations(ann).Descriptor() }},
		{"Int16", func() *field.Descriptor { return field.Int16("v").Annotations(ann).Descriptor() }},
		{"Int32", func() *field.Descriptor { return field.Int32("v").Annotations(ann).Descriptor() }},
		{"Int64", func() *field.Descriptor { return field.Int64("v").Annotations(ann).Descriptor() }},
		{"Uint8", func() *field.Descriptor { return field.Uint8("v").Annotations(ann).Descriptor() }},
		{"Uint16", func() *field.Descriptor { return field.Uint16("v").Annotations(ann).Descriptor() }},
		{"Uint32", func() *field.Descriptor { return field.Uint32("v").Annotations(ann).Descriptor() }},
		{"Uint64", func() *field.Descriptor { return field.Uint64("v").Annotations(ann).Descriptor() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Len(t, tt.builder().Annotations, 1)
		})
	}
}

// TestNumericFieldSchemaTypes tests SchemaType support.
func TestNumericFieldSchemaTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, schemaType string
		builder          func() *field.Descriptor
	}{
		{"Int8", "smallint", func() *field.Descriptor {
			return field.Int8("v").SchemaType(map[string]string{dialect.Postgres: "smallint"}).Descriptor()
		}},
		{"Int16", "smallint", func() *field.Descriptor {
			return field.Int16("v").SchemaType(map[string]string{dialect.Postgres: "smallint"}).Descriptor()
		}},
		{"Int32", "integer", func() *field.Descriptor {
			return field.Int32("v").SchemaType(map[string]string{dialect.Postgres: "integer"}).Descriptor()
		}},
		{"Int64", "bigint", func() *field.Descriptor {
			return field.Int64("v").SchemaType(map[string]string{dialect.Postgres: "bigint"}).Descriptor()
		}},
		{"Uint8", "smallint", func() *field.Descriptor {
			return field.Uint8("v").SchemaType(map[string]string{dialect.Postgres: "smallint"}).Descriptor()
		}},
		{"Uint16", "integer", func() *field.Descriptor {
			return field.Uint16("v").SchemaType(map[string]string{dialect.Postgres: "integer"}).Descriptor()
		}},
		{"Uint32", "bigint", func() *field.Descriptor {
			return field.Uint32("v").SchemaType(map[string]string{dialect.Postgres: "bigint"}).Descriptor()
		}},
		{"Uint64", "bigint", func() *field.Descriptor {
			return field.Uint64("v").SchemaType(map[string]string{dialect.Postgres: "bigint"}).Descriptor()
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.schemaType, tt.builder().SchemaType[dialect.Postgres])
		})
	}
}

// TestNumericFieldDeprecation tests deprecation support.
func TestNumericFieldDeprecation(t *testing.T) {
	t.Parallel()

	const reason = "use new_field instead"

	tests := []struct {
		name    string
		builder func() *field.Descriptor
	}{
		{"Int8", func() *field.Descriptor { return field.Int8("v").Deprecated(reason).Descriptor() }},
		{"Int16", func() *field.Descriptor { return field.Int16("v").Deprecated(reason).Descriptor() }},
		{"Int32", func() *field.Descriptor { return field.Int32("v").Deprecated(reason).Descriptor() }},
		{"Int64", func() *field.Descriptor { return field.Int64("v").Deprecated(reason).Descriptor() }},
		{"Uint8", func() *field.Descriptor { return field.Uint8("v").Deprecated(reason).Descriptor() }},
		{"Uint16", func() *field.Descriptor { return field.Uint16("v").Deprecated(reason).Descriptor() }},
		{"Uint32", func() *field.Descriptor { return field.Uint32("v").Deprecated(reason).Descriptor() }},
		{"Uint64", func() *field.Descriptor { return field.Uint64("v").Deprecated(reason).Descriptor() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fd := tt.builder()
			assert.True(t, fd.Deprecated)
			assert.Equal(t, reason, fd.DeprecatedReason)
		})
	}
}

// =============================================================================
// Default Value Tests
// =============================================================================

// TestNumericFieldDefaults tests Default, DefaultFunc, and UpdateDefault.
func TestNumericFieldDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		defBuilder  func() *field.Descriptor
		funcBuilder func() *field.Descriptor
		updBuilder  func() *field.Descriptor
		expectedDef any
	}{
		{"Int8", func() *field.Descriptor { return field.Int8("v").Default(5).Descriptor() },
			func() *field.Descriptor { return field.Int8("v").DefaultFunc(func() int8 { return 7 }).Descriptor() },
			func() *field.Descriptor { return field.Int8("v").UpdateDefault(func() int8 { return 8 }).Descriptor() }, int8(5)},
		{"Int16", func() *field.Descriptor { return field.Int16("v").Default(100).Descriptor() },
			func() *field.Descriptor {
				return field.Int16("v").DefaultFunc(func() int16 { return 200 }).Descriptor()
			},
			func() *field.Descriptor {
				return field.Int16("v").UpdateDefault(func() int16 { return 300 }).Descriptor()
			}, int16(100)},
		{"Int32", func() *field.Descriptor { return field.Int32("v").Default(1000).Descriptor() },
			func() *field.Descriptor {
				return field.Int32("v").DefaultFunc(func() int32 { return 2000 }).Descriptor()
			},
			func() *field.Descriptor {
				return field.Int32("v").UpdateDefault(func() int32 { return 3000 }).Descriptor()
			}, int32(1000)},
		{"Int64", func() *field.Descriptor { return field.Int64("v").Default(10000).Descriptor() },
			func() *field.Descriptor {
				return field.Int64("v").DefaultFunc(func() int64 { return 20000 }).Descriptor()
			},
			func() *field.Descriptor {
				return field.Int64("v").UpdateDefault(func() int64 { return 30000 }).Descriptor()
			}, int64(10000)},
		{"Uint8", func() *field.Descriptor { return field.Uint8("v").Default(128).Descriptor() },
			func() *field.Descriptor {
				return field.Uint8("v").DefaultFunc(func() uint8 { return 200 }).Descriptor()
			},
			func() *field.Descriptor {
				return field.Uint8("v").UpdateDefault(func() uint8 { return 250 }).Descriptor()
			}, uint8(128)},
		{"Uint16", func() *field.Descriptor { return field.Uint16("v").Default(8080).Descriptor() },
			func() *field.Descriptor {
				return field.Uint16("v").DefaultFunc(func() uint16 { return 9000 }).Descriptor()
			},
			func() *field.Descriptor {
				return field.Uint16("v").UpdateDefault(func() uint16 { return 9090 }).Descriptor()
			}, uint16(8080)},
		{"Uint32", func() *field.Descriptor { return field.Uint32("v").Default(12345).Descriptor() },
			func() *field.Descriptor {
				return field.Uint32("v").DefaultFunc(func() uint32 { return 67890 }).Descriptor()
			},
			func() *field.Descriptor {
				return field.Uint32("v").UpdateDefault(func() uint32 { return 11111 }).Descriptor()
			}, uint32(12345)},
		{"Uint64", func() *field.Descriptor { return field.Uint64("v").Default(123456789).Descriptor() },
			func() *field.Descriptor {
				return field.Uint64("v").DefaultFunc(func() uint64 { return 987654321 }).Descriptor()
			},
			func() *field.Descriptor {
				return field.Uint64("v").UpdateDefault(func() uint64 { return 111111111 }).Descriptor()
			}, uint64(123456789)},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/Default", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expectedDef, tt.defBuilder().Default)
		})
		t.Run(tt.name+"/DefaultFunc", func(t *testing.T) {
			t.Parallel()
			assert.NotNil(t, tt.funcBuilder().Default)
		})
		t.Run(tt.name+"/UpdateDefault", func(t *testing.T) {
			t.Parallel()
			assert.NotNil(t, tt.updBuilder().UpdateDefault)
		})
	}
}

// =============================================================================
// Custom Validator, GoType, ValueScanner Tests
// =============================================================================

// TestNumericFieldCustomValidate tests custom Validate method.
func TestNumericFieldCustomValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		builder func() *field.Descriptor
	}{
		{"Int8", func() *field.Descriptor {
			return field.Int8("v").Validate(func(_ int8) error { return nil }).Descriptor()
		}},
		{"Int16", func() *field.Descriptor {
			return field.Int16("v").Validate(func(_ int16) error { return nil }).Descriptor()
		}},
		{"Int32", func() *field.Descriptor {
			return field.Int32("v").Validate(func(_ int32) error { return nil }).Descriptor()
		}},
		{"Int64", func() *field.Descriptor {
			return field.Int64("v").Validate(func(_ int64) error { return nil }).Descriptor()
		}},
		{"Uint8", func() *field.Descriptor {
			return field.Uint8("v").Validate(func(_ uint8) error { return nil }).Descriptor()
		}},
		{"Uint16", func() *field.Descriptor {
			return field.Uint16("v").Validate(func(_ uint16) error { return nil }).Descriptor()
		}},
		{"Uint32", func() *field.Descriptor {
			return field.Uint32("v").Validate(func(_ uint32) error { return nil }).Descriptor()
		}},
		{"Uint64", func() *field.Descriptor {
			return field.Uint64("v").Validate(func(_ uint64) error { return nil }).Descriptor()
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Len(t, tt.builder().Validators, 1)
		})
	}
}

// Custom types for GoType tests
type (
	myInt8   int8
	myInt16  int16
	myInt32  int32
	myInt64  int64
	myUint8  uint8
	myUint16 uint16
	myUint32 uint32
	myUint64 uint64
)

// TestNumericFieldGoType tests GoType method.
func TestNumericFieldGoType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		builder func() *field.Descriptor
	}{
		{"Int8", func() *field.Descriptor { return field.Int8("v").GoType(myInt8(0)).Descriptor() }},
		{"Int16", func() *field.Descriptor { return field.Int16("v").GoType(myInt16(0)).Descriptor() }},
		{"Int32", func() *field.Descriptor { return field.Int32("v").GoType(myInt32(0)).Descriptor() }},
		{"Int64", func() *field.Descriptor { return field.Int64("v").GoType(myInt64(0)).Descriptor() }},
		{"Uint8", func() *field.Descriptor { return field.Uint8("v").GoType(myUint8(0)).Descriptor() }},
		{"Uint16", func() *field.Descriptor { return field.Uint16("v").GoType(myUint16(0)).Descriptor() }},
		{"Uint32", func() *field.Descriptor { return field.Uint32("v").GoType(myUint32(0)).Descriptor() }},
		{"Uint64", func() *field.Descriptor { return field.Uint64("v").GoType(myUint64(0)).Descriptor() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.NotNil(t, tt.builder().Info.RType)
		})
	}
}

// TestNumericFieldValueScanner tests ValueScanner method.
func TestNumericFieldValueScanner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		builder func() *field.Descriptor
	}{
		{"Int8", func() *field.Descriptor {
			return field.Int8("v").ValueScanner(field.ValueScannerFunc[int8, *sql.NullInt64]{
				V: func(v int8) (driver.Value, error) { return int64(v), nil },
				S: func(ns *sql.NullInt64) (int8, error) { return int8(ns.Int64), nil },
			}).Descriptor()
		}},
		{"Int16", func() *field.Descriptor {
			return field.Int16("v").ValueScanner(field.ValueScannerFunc[int16, *sql.NullInt64]{
				V: func(v int16) (driver.Value, error) { return int64(v), nil },
				S: func(ns *sql.NullInt64) (int16, error) { return int16(ns.Int64), nil },
			}).Descriptor()
		}},
		{"Int32", func() *field.Descriptor {
			return field.Int32("v").ValueScanner(field.ValueScannerFunc[int32, *sql.NullInt64]{
				V: func(v int32) (driver.Value, error) { return int64(v), nil },
				S: func(ns *sql.NullInt64) (int32, error) { return int32(ns.Int64), nil },
			}).Descriptor()
		}},
		{"Int64", func() *field.Descriptor {
			return field.Int64("v").ValueScanner(field.ValueScannerFunc[int64, *sql.NullInt64]{
				V: func(v int64) (driver.Value, error) { return v, nil },
				S: func(ns *sql.NullInt64) (int64, error) { return ns.Int64, nil },
			}).Descriptor()
		}},
		{"Uint8", func() *field.Descriptor {
			return field.Uint8("v").ValueScanner(field.ValueScannerFunc[uint8, *sql.NullInt64]{
				V: func(v uint8) (driver.Value, error) { return int64(v), nil },
				S: func(ns *sql.NullInt64) (uint8, error) { return uint8(ns.Int64), nil },
			}).Descriptor()
		}},
		{"Uint16", func() *field.Descriptor {
			return field.Uint16("v").ValueScanner(field.ValueScannerFunc[uint16, *sql.NullInt64]{
				V: func(v uint16) (driver.Value, error) { return int64(v), nil },
				S: func(ns *sql.NullInt64) (uint16, error) { return uint16(ns.Int64), nil },
			}).Descriptor()
		}},
		{"Uint32", func() *field.Descriptor {
			return field.Uint32("v").ValueScanner(field.ValueScannerFunc[uint32, *sql.NullInt64]{
				V: func(v uint32) (driver.Value, error) { return int64(v), nil },
				S: func(ns *sql.NullInt64) (uint32, error) { return uint32(ns.Int64), nil },
			}).Descriptor()
		}},
		{"Uint64", func() *field.Descriptor {
			return field.Uint64("v").ValueScanner(field.ValueScannerFunc[uint64, *sql.NullInt64]{
				V: func(v uint64) (driver.Value, error) { return int64(v), nil },
				S: func(ns *sql.NullInt64) (uint64, error) { return uint64(ns.Int64), nil },
			}).Descriptor()
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.NotNil(t, tt.builder().ValueScanner)
		})
	}
}

// =============================================================================
// Comprehensive Validator Tests for All Numeric Types
// =============================================================================

// TestIntValidatorsComprehensive tests validators for int type.
func TestIntValidatorsComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("Range", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("val").Range(-100, 100).Descriptor()
		v := fd.Validators[0].(func(int) error)
		assert.NoError(t, v(-100))
		assert.NoError(t, v(0))
		assert.NoError(t, v(100))
		assert.Error(t, v(-101))
		assert.Error(t, v(101))
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("val").Min(10).Descriptor()
		v := fd.Validators[0].(func(int) error)
		assert.NoError(t, v(10))
		assert.NoError(t, v(100))
		assert.Error(t, v(9))
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("val").Max(50).Descriptor()
		v := fd.Validators[0].(func(int) error)
		assert.NoError(t, v(50))
		assert.NoError(t, v(-100))
		assert.Error(t, v(51))
	})

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("val").Positive().Descriptor()
		v := fd.Validators[0].(func(int) error)
		assert.NoError(t, v(1))
		assert.NoError(t, v(999))
		assert.Error(t, v(0))
		assert.Error(t, v(-1))
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("val").Negative().Descriptor()
		v := fd.Validators[0].(func(int) error)
		assert.NoError(t, v(-1))
		assert.NoError(t, v(-999))
		assert.Error(t, v(0))
		assert.Error(t, v(1))
	})

	t.Run("NonNegative", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("val").NonNegative().Descriptor()
		v := fd.Validators[0].(func(int) error)
		assert.NoError(t, v(0))
		assert.NoError(t, v(999))
		assert.Error(t, v(-1))
	})
}

// TestIntBuilderProperties tests Int builder boolean/string/misc properties.
func TestIntBuilderProperties(t *testing.T) {
	t.Parallel()

	t.Run("Unique", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").Unique().Descriptor()
		assert.True(t, fd.Unique)
	})
	t.Run("Optional", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").Optional().Descriptor()
		assert.True(t, fd.Optional)
	})
	t.Run("Nillable", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").Nillable().Descriptor()
		assert.True(t, fd.Nillable)
	})
	t.Run("Immutable", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").Immutable().Descriptor()
		assert.True(t, fd.Immutable)
	})
	t.Run("Comment", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").Comment("test").Descriptor()
		assert.Equal(t, "test", fd.Comment)
	})
	t.Run("StructTag", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").StructTag(`json:"v"`).Descriptor()
		assert.Equal(t, `json:"v"`, fd.Tag)
	})
	t.Run("StorageKey", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").StorageKey("col").Descriptor()
		assert.Equal(t, "col", fd.StorageKey)
	})
	t.Run("Default", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").Default(42).Descriptor()
		assert.Equal(t, 42, fd.Default)
	})
	t.Run("DefaultFunc", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").DefaultFunc(func() int { return 42 }).Descriptor()
		assert.NotNil(t, fd.Default)
	})
	t.Run("UpdateDefault", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").UpdateDefault(func() int { return 42 }).Descriptor()
		assert.NotNil(t, fd.UpdateDefault)
	})
	t.Run("Validate", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").Validate(func(_ int) error { return nil }).Descriptor()
		require.Len(t, fd.Validators, 1)
	})
	t.Run("SchemaType", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").SchemaType(map[string]string{dialect.Postgres: "integer"}).Descriptor()
		assert.Equal(t, "integer", fd.SchemaType[dialect.Postgres])
	})
	t.Run("Annotations", func(t *testing.T) {
		t.Parallel()
		ann := &field.Annotation{StructTag: map[string]string{"json": `"test"`}}
		fd := field.Int("v").Annotations(ann).Descriptor()
		require.Len(t, fd.Annotations, 1)
	})
	t.Run("Deprecated", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").Deprecated("old").Descriptor()
		assert.True(t, fd.Deprecated)
		assert.Equal(t, "old", fd.DeprecatedReason)
	})
	t.Run("GoType", func(t *testing.T) {
		t.Parallel()
		type myInt int
		fd := field.Int("v").GoType(myInt(0)).Descriptor()
		assert.NotNil(t, fd.Info.RType)
	})
	t.Run("ValueScanner", func(t *testing.T) {
		t.Parallel()
		fd := field.Int("v").ValueScanner(field.ValueScannerFunc[int, *sql.NullInt64]{
			V: func(v int) (driver.Value, error) { return int64(v), nil },
			S: func(ns *sql.NullInt64) (int, error) { return int(ns.Int64), nil },
		}).Descriptor()
		assert.NotNil(t, fd.ValueScanner)
	})
}

// TestUintValidatorsComprehensive tests validators for uint type.
func TestUintValidatorsComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("Range", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("val").Range(10, 200).Descriptor()
		v := fd.Validators[0].(func(uint) error)
		assert.NoError(t, v(10))
		assert.NoError(t, v(100))
		assert.NoError(t, v(200))
		assert.Error(t, v(9))
		assert.Error(t, v(201))
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("val").Min(5).Descriptor()
		v := fd.Validators[0].(func(uint) error)
		assert.NoError(t, v(5))
		assert.NoError(t, v(100))
		assert.Error(t, v(4))
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("val").Max(50).Descriptor()
		v := fd.Validators[0].(func(uint) error)
		assert.NoError(t, v(50))
		assert.NoError(t, v(0))
		assert.Error(t, v(51))
	})

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("val").Positive().Descriptor()
		v := fd.Validators[0].(func(uint) error)
		assert.NoError(t, v(1))
		assert.NoError(t, v(999))
		assert.Error(t, v(0))
	})
}

// TestUintBuilderProperties tests Uint builder properties.
func TestUintBuilderProperties(t *testing.T) {
	t.Parallel()

	t.Run("Unique", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").Unique().Descriptor()
		assert.True(t, fd.Unique)
	})
	t.Run("Optional", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").Optional().Descriptor()
		assert.True(t, fd.Optional)
	})
	t.Run("Nillable", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").Nillable().Descriptor()
		assert.True(t, fd.Nillable)
	})
	t.Run("Immutable", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").Immutable().Descriptor()
		assert.True(t, fd.Immutable)
	})
	t.Run("Comment", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").Comment("test").Descriptor()
		assert.Equal(t, "test", fd.Comment)
	})
	t.Run("StructTag", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").StructTag(`json:"v"`).Descriptor()
		assert.Equal(t, `json:"v"`, fd.Tag)
	})
	t.Run("StorageKey", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").StorageKey("col").Descriptor()
		assert.Equal(t, "col", fd.StorageKey)
	})
	t.Run("Default", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").Default(42).Descriptor()
		assert.Equal(t, uint(42), fd.Default)
	})
	t.Run("DefaultFunc", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").DefaultFunc(func() uint { return 42 }).Descriptor()
		assert.NotNil(t, fd.Default)
	})
	t.Run("UpdateDefault", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").UpdateDefault(func() uint { return 42 }).Descriptor()
		assert.NotNil(t, fd.UpdateDefault)
	})
	t.Run("Validate", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").Validate(func(_ uint) error { return nil }).Descriptor()
		require.Len(t, fd.Validators, 1)
	})
	t.Run("SchemaType", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").SchemaType(map[string]string{dialect.Postgres: "integer"}).Descriptor()
		assert.Equal(t, "integer", fd.SchemaType[dialect.Postgres])
	})
	t.Run("Annotations", func(t *testing.T) {
		t.Parallel()
		ann := &field.Annotation{StructTag: map[string]string{"json": `"test"`}}
		fd := field.Uint("v").Annotations(ann).Descriptor()
		require.Len(t, fd.Annotations, 1)
	})
	t.Run("Deprecated", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").Deprecated("old").Descriptor()
		assert.True(t, fd.Deprecated)
		assert.Equal(t, "old", fd.DeprecatedReason)
	})
	t.Run("GoType", func(t *testing.T) {
		t.Parallel()
		type myUint uint
		fd := field.Uint("v").GoType(myUint(0)).Descriptor()
		assert.NotNil(t, fd.Info.RType)
	})
	t.Run("ValueScanner", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint("v").ValueScanner(field.ValueScannerFunc[uint, *sql.NullInt64]{
			V: func(v uint) (driver.Value, error) { return int64(v), nil },
			S: func(ns *sql.NullInt64) (uint, error) { return uint(ns.Int64), nil },
		}).Descriptor()
		assert.NotNil(t, fd.ValueScanner)
	})
}

// TestInt16ValidatorsComprehensive tests validators for int16 type.
func TestInt16ValidatorsComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("Range", func(t *testing.T) {
		t.Parallel()
		fd := field.Int16("val").Range(-1000, 1000).Descriptor()
		v := fd.Validators[0].(func(int16) error)
		assert.NoError(t, v(-1000))
		assert.NoError(t, v(0))
		assert.NoError(t, v(1000))
		assert.Error(t, v(-1001))
		assert.Error(t, v(1001))
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()
		fd := field.Int16("val").Min(-500).Descriptor()
		v := fd.Validators[0].(func(int16) error)
		assert.NoError(t, v(-500))
		assert.NoError(t, v(0))
		assert.Error(t, v(-501))
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()
		fd := field.Int16("val").Max(500).Descriptor()
		v := fd.Validators[0].(func(int16) error)
		assert.NoError(t, v(500))
		assert.NoError(t, v(-1000))
		assert.Error(t, v(501))
	})

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()
		fd := field.Int16("val").Positive().Descriptor()
		v := fd.Validators[0].(func(int16) error)
		assert.NoError(t, v(1))
		assert.NoError(t, v(32767))
		assert.Error(t, v(0))
		assert.Error(t, v(-1))
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()
		fd := field.Int16("val").Negative().Descriptor()
		v := fd.Validators[0].(func(int16) error)
		assert.NoError(t, v(-1))
		assert.NoError(t, v(-32768))
		assert.Error(t, v(0))
		assert.Error(t, v(1))
	})

	t.Run("NonNegative", func(t *testing.T) {
		t.Parallel()
		fd := field.Int16("val").NonNegative().Descriptor()
		v := fd.Validators[0].(func(int16) error)
		assert.NoError(t, v(0))
		assert.NoError(t, v(32767))
		assert.Error(t, v(-1))
	})
}

// TestInt32ValidatorsComprehensive tests validators for int32 type.
func TestInt32ValidatorsComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("Range", func(t *testing.T) {
		t.Parallel()
		fd := field.Int32("val").Range(-100000, 100000).Descriptor()
		v := fd.Validators[0].(func(int32) error)
		assert.NoError(t, v(-100000))
		assert.NoError(t, v(0))
		assert.NoError(t, v(100000))
		assert.Error(t, v(-100001))
		assert.Error(t, v(100001))
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()
		fd := field.Int32("val").Min(-50000).Descriptor()
		v := fd.Validators[0].(func(int32) error)
		assert.NoError(t, v(-50000))
		assert.NoError(t, v(0))
		assert.Error(t, v(-50001))
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()
		fd := field.Int32("val").Max(50000).Descriptor()
		v := fd.Validators[0].(func(int32) error)
		assert.NoError(t, v(50000))
		assert.NoError(t, v(-999))
		assert.Error(t, v(50001))
	})

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()
		fd := field.Int32("val").Positive().Descriptor()
		v := fd.Validators[0].(func(int32) error)
		assert.NoError(t, v(1))
		assert.NoError(t, v(2147483647))
		assert.Error(t, v(0))
		assert.Error(t, v(-1))
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()
		fd := field.Int32("val").Negative().Descriptor()
		v := fd.Validators[0].(func(int32) error)
		assert.NoError(t, v(-1))
		assert.NoError(t, v(-2147483648))
		assert.Error(t, v(0))
		assert.Error(t, v(1))
	})

	t.Run("NonNegative", func(t *testing.T) {
		t.Parallel()
		fd := field.Int32("val").NonNegative().Descriptor()
		v := fd.Validators[0].(func(int32) error)
		assert.NoError(t, v(0))
		assert.NoError(t, v(2147483647))
		assert.Error(t, v(-1))
	})
}

// TestInt64ValidatorsComprehensive tests validators for int64 type.
func TestInt64ValidatorsComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("Range", func(t *testing.T) {
		t.Parallel()
		fd := field.Int64("val").Range(-1000000, 1000000).Descriptor()
		v := fd.Validators[0].(func(int64) error)
		assert.NoError(t, v(-1000000))
		assert.NoError(t, v(0))
		assert.NoError(t, v(1000000))
		assert.Error(t, v(-1000001))
		assert.Error(t, v(1000001))
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()
		fd := field.Int64("val").Min(-500000).Descriptor()
		v := fd.Validators[0].(func(int64) error)
		assert.NoError(t, v(-500000))
		assert.NoError(t, v(0))
		assert.Error(t, v(-500001))
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()
		fd := field.Int64("val").Max(500000).Descriptor()
		v := fd.Validators[0].(func(int64) error)
		assert.NoError(t, v(500000))
		assert.NoError(t, v(-999))
		assert.Error(t, v(500001))
	})

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()
		fd := field.Int64("val").Positive().Descriptor()
		v := fd.Validators[0].(func(int64) error)
		assert.NoError(t, v(1))
		assert.NoError(t, v(9223372036854775807))
		assert.Error(t, v(0))
		assert.Error(t, v(-1))
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()
		fd := field.Int64("val").Negative().Descriptor()
		v := fd.Validators[0].(func(int64) error)
		assert.NoError(t, v(-1))
		assert.NoError(t, v(-9223372036854775808))
		assert.Error(t, v(0))
		assert.Error(t, v(1))
	})

	t.Run("NonNegative", func(t *testing.T) {
		t.Parallel()
		fd := field.Int64("val").NonNegative().Descriptor()
		v := fd.Validators[0].(func(int64) error)
		assert.NoError(t, v(0))
		assert.NoError(t, v(9223372036854775807))
		assert.Error(t, v(-1))
	})
}

// TestUint16ValidatorsComprehensive tests validators for uint16 type.
func TestUint16ValidatorsComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("Range", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint16("val").Range(100, 60000).Descriptor()
		v := fd.Validators[0].(func(uint16) error)
		assert.NoError(t, v(100))
		assert.NoError(t, v(30000))
		assert.NoError(t, v(60000))
		assert.Error(t, v(99))
		assert.Error(t, v(60001))
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint16("val").Min(1024).Descriptor()
		v := fd.Validators[0].(func(uint16) error)
		assert.NoError(t, v(1024))
		assert.NoError(t, v(65535))
		assert.Error(t, v(1023))
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint16("val").Max(49151).Descriptor()
		v := fd.Validators[0].(func(uint16) error)
		assert.NoError(t, v(49151))
		assert.NoError(t, v(0))
		assert.Error(t, v(49152))
	})

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint16("val").Positive().Descriptor()
		v := fd.Validators[0].(func(uint16) error)
		assert.NoError(t, v(1))
		assert.NoError(t, v(65535))
		assert.Error(t, v(0))
	})
}

// TestUint32ValidatorsComprehensive tests validators for uint32 type.
func TestUint32ValidatorsComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("Range", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint32("val").Range(0, 1000000).Descriptor()
		v := fd.Validators[0].(func(uint32) error)
		assert.NoError(t, v(0))
		assert.NoError(t, v(500000))
		assert.NoError(t, v(1000000))
		assert.Error(t, v(1000001))
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint32("val").Min(100).Descriptor()
		v := fd.Validators[0].(func(uint32) error)
		assert.NoError(t, v(100))
		assert.NoError(t, v(4294967295))
		assert.Error(t, v(99))
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint32("val").Max(999999).Descriptor()
		v := fd.Validators[0].(func(uint32) error)
		assert.NoError(t, v(999999))
		assert.NoError(t, v(0))
		assert.Error(t, v(1000000))
	})

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint32("val").Positive().Descriptor()
		v := fd.Validators[0].(func(uint32) error)
		assert.NoError(t, v(1))
		assert.NoError(t, v(4294967295))
		assert.Error(t, v(0))
	})
}

// TestUint64ValidatorsComprehensive tests validators for uint64 type.
func TestUint64ValidatorsComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("Range", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint64("val").Range(0, 1000000000).Descriptor()
		v := fd.Validators[0].(func(uint64) error)
		assert.NoError(t, v(0))
		assert.NoError(t, v(500000000))
		assert.NoError(t, v(1000000000))
		assert.Error(t, v(1000000001))
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint64("val").Min(100).Descriptor()
		v := fd.Validators[0].(func(uint64) error)
		assert.NoError(t, v(100))
		assert.NoError(t, v(18446744073709551615))
		assert.Error(t, v(99))
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint64("val").Max(999999).Descriptor()
		v := fd.Validators[0].(func(uint64) error)
		assert.NoError(t, v(999999))
		assert.NoError(t, v(0))
		assert.Error(t, v(1000000))
	})

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()
		fd := field.Uint64("val").Positive().Descriptor()
		v := fd.Validators[0].(func(uint64) error)
		assert.NoError(t, v(1))
		assert.NoError(t, v(18446744073709551615))
		assert.Error(t, v(0))
	})
}

// TestFloat64ValidatorsComprehensive tests validators for float64 type.
func TestFloat64ValidatorsComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("Range", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("val").Range(-1.5, 9.5).Descriptor()
		v := fd.Validators[0].(func(float64) error)
		assert.NoError(t, v(-1.5))
		assert.NoError(t, v(0))
		assert.NoError(t, v(9.5))
		assert.Error(t, v(-1.6))
		assert.Error(t, v(9.6))
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("val").Min(0.5).Descriptor()
		v := fd.Validators[0].(func(float64) error)
		assert.NoError(t, v(0.5))
		assert.NoError(t, v(100.0))
		assert.Error(t, v(0.4))
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("val").Max(99.9).Descriptor()
		v := fd.Validators[0].(func(float64) error)
		assert.NoError(t, v(99.9))
		assert.NoError(t, v(-100.0))
		assert.Error(t, v(100.0))
	})

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("val").Positive().Descriptor()
		v := fd.Validators[0].(func(float64) error)
		assert.NoError(t, v(0.001))
		assert.NoError(t, v(999.9))
		assert.Error(t, v(0))
		assert.Error(t, v(-0.001))
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("val").Negative().Descriptor()
		v := fd.Validators[0].(func(float64) error)
		assert.NoError(t, v(-0.001))
		assert.NoError(t, v(-999.9))
		assert.Error(t, v(0))
		assert.Error(t, v(0.001))
	})

	t.Run("NonNegative", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("val").NonNegative().Descriptor()
		v := fd.Validators[0].(func(float64) error)
		assert.NoError(t, v(0))
		assert.NoError(t, v(999.9))
		assert.Error(t, v(-0.001))
	})
}

// TestFloat64BuilderProperties tests Float (float64) builder properties.
func TestFloat64BuilderProperties(t *testing.T) {
	t.Parallel()

	t.Run("Basic", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").Descriptor()
		assert.Equal(t, "v", fd.Name)
		assert.Equal(t, field.TypeFloat64, fd.Info.Type)
	})
	t.Run("Unique", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").Unique().Descriptor()
		assert.True(t, fd.Unique)
	})
	t.Run("Optional", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").Optional().Descriptor()
		assert.True(t, fd.Optional)
	})
	t.Run("Nillable", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").Nillable().Descriptor()
		assert.True(t, fd.Nillable)
	})
	t.Run("Immutable", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").Immutable().Descriptor()
		assert.True(t, fd.Immutable)
	})
	t.Run("Comment", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").Comment("test").Descriptor()
		assert.Equal(t, "test", fd.Comment)
	})
	t.Run("StructTag", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").StructTag(`json:"v"`).Descriptor()
		assert.Equal(t, `json:"v"`, fd.Tag)
	})
	t.Run("StorageKey", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").StorageKey("col").Descriptor()
		assert.Equal(t, "col", fd.StorageKey)
	})
	t.Run("Default", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").Default(3.14).Descriptor()
		assert.Equal(t, 3.14, fd.Default)
	})
	t.Run("DefaultFunc", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").DefaultFunc(func() float64 { return 3.14 }).Descriptor()
		assert.NotNil(t, fd.Default)
	})
	t.Run("UpdateDefault", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").UpdateDefault(func() float64 { return 3.14 }).Descriptor()
		assert.NotNil(t, fd.UpdateDefault)
	})
	t.Run("Validate", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").Validate(func(_ float64) error { return nil }).Descriptor()
		require.Len(t, fd.Validators, 1)
	})
	t.Run("SchemaType", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").SchemaType(map[string]string{dialect.Postgres: "numeric(5,2)"}).Descriptor()
		assert.Equal(t, "numeric(5,2)", fd.SchemaType[dialect.Postgres])
	})
	t.Run("Annotations", func(t *testing.T) {
		t.Parallel()
		ann := &field.Annotation{StructTag: map[string]string{"json": `"test"`}}
		fd := field.Float("v").Annotations(ann).Descriptor()
		require.Len(t, fd.Annotations, 1)
	})
	t.Run("Deprecated", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").Deprecated("old").Descriptor()
		assert.True(t, fd.Deprecated)
		assert.Equal(t, "old", fd.DeprecatedReason)
	})
	t.Run("GoType", func(t *testing.T) {
		t.Parallel()
		type myFloat float64
		fd := field.Float("v").GoType(myFloat(0)).Descriptor()
		assert.NotNil(t, fd.Info.RType)
	})
	t.Run("ValueScanner", func(t *testing.T) {
		t.Parallel()
		fd := field.Float("v").ValueScanner(field.ValueScannerFunc[float64, *sql.NullFloat64]{
			V: func(v float64) (driver.Value, error) { return v, nil },
			S: func(ns *sql.NullFloat64) (float64, error) { return ns.Float64, nil },
		}).Descriptor()
		assert.NotNil(t, fd.ValueScanner)
	})
}

// TestFloat32ValidatorsComprehensive tests validators for float32 type.
func TestFloat32ValidatorsComprehensive(t *testing.T) {
	t.Parallel()

	t.Run("Range", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("val").Range(-1.5, 9.5).Descriptor()
		v := fd.Validators[0].(func(float32) error)
		assert.NoError(t, v(-1.5))
		assert.NoError(t, v(0))
		assert.NoError(t, v(9.5))
		assert.Error(t, v(-1.6))
		assert.Error(t, v(9.6))
	})

	t.Run("Min", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("val").Min(0.5).Descriptor()
		v := fd.Validators[0].(func(float32) error)
		assert.NoError(t, v(0.5))
		assert.NoError(t, v(100.0))
		assert.Error(t, v(0.4))
	})

	t.Run("Max", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("val").Max(99.9).Descriptor()
		v := fd.Validators[0].(func(float32) error)
		assert.NoError(t, v(99.9))
		assert.NoError(t, v(-100.0))
		assert.Error(t, v(100.0))
	})

	t.Run("Positive", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("val").Positive().Descriptor()
		v := fd.Validators[0].(func(float32) error)
		assert.NoError(t, v(0.001))
		assert.NoError(t, v(999.9))
		assert.Error(t, v(0))
		assert.Error(t, v(-0.001))
	})

	t.Run("Negative", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("val").Negative().Descriptor()
		v := fd.Validators[0].(func(float32) error)
		assert.NoError(t, v(-0.001))
		assert.NoError(t, v(-999.9))
		assert.Error(t, v(0))
		assert.Error(t, v(0.001))
	})

	t.Run("NonNegative", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("val").NonNegative().Descriptor()
		v := fd.Validators[0].(func(float32) error)
		assert.NoError(t, v(0))
		assert.NoError(t, v(999.9))
		assert.Error(t, v(-0.001))
	})
}

// TestFloat32BuilderProperties tests Float32 builder properties.
func TestFloat32BuilderProperties(t *testing.T) {
	t.Parallel()

	t.Run("Basic", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").Descriptor()
		assert.Equal(t, "v", fd.Name)
		assert.Equal(t, field.TypeFloat32, fd.Info.Type)
	})
	t.Run("Unique", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").Unique().Descriptor()
		assert.True(t, fd.Unique)
	})
	t.Run("Optional", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").Optional().Descriptor()
		assert.True(t, fd.Optional)
	})
	t.Run("Nillable", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").Nillable().Descriptor()
		assert.True(t, fd.Nillable)
	})
	t.Run("Immutable", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").Immutable().Descriptor()
		assert.True(t, fd.Immutable)
	})
	t.Run("Comment", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").Comment("test").Descriptor()
		assert.Equal(t, "test", fd.Comment)
	})
	t.Run("StructTag", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").StructTag(`json:"v"`).Descriptor()
		assert.Equal(t, `json:"v"`, fd.Tag)
	})
	t.Run("StorageKey", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").StorageKey("col").Descriptor()
		assert.Equal(t, "col", fd.StorageKey)
	})
	t.Run("Default", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").Default(3.14).Descriptor()
		assert.Equal(t, float32(3.14), fd.Default)
	})
	t.Run("DefaultFunc", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").DefaultFunc(func() float32 { return 3.14 }).Descriptor()
		assert.NotNil(t, fd.Default)
	})
	t.Run("UpdateDefault", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").UpdateDefault(func() float32 { return 3.14 }).Descriptor()
		assert.NotNil(t, fd.UpdateDefault)
	})
	t.Run("Validate", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").Validate(func(_ float32) error { return nil }).Descriptor()
		require.Len(t, fd.Validators, 1)
	})
	t.Run("SchemaType", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").SchemaType(map[string]string{dialect.Postgres: "real"}).Descriptor()
		assert.Equal(t, "real", fd.SchemaType[dialect.Postgres])
	})
	t.Run("Annotations", func(t *testing.T) {
		t.Parallel()
		ann := &field.Annotation{StructTag: map[string]string{"json": `"test"`}}
		fd := field.Float32("v").Annotations(ann).Descriptor()
		require.Len(t, fd.Annotations, 1)
	})
	t.Run("Deprecated", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").Deprecated("old").Descriptor()
		assert.True(t, fd.Deprecated)
		assert.Equal(t, "old", fd.DeprecatedReason)
	})
	t.Run("GoType", func(t *testing.T) {
		t.Parallel()
		type myFloat32 float32
		fd := field.Float32("v").GoType(myFloat32(0)).Descriptor()
		assert.NotNil(t, fd.Info.RType)
	})
	t.Run("ValueScanner", func(t *testing.T) {
		t.Parallel()
		fd := field.Float32("v").ValueScanner(field.ValueScannerFunc[float32, *sql.NullFloat64]{
			V: func(v float32) (driver.Value, error) { return float64(v), nil },
			S: func(ns *sql.NullFloat64) (float32, error) { return float32(ns.Float64), nil },
		}).Descriptor()
		assert.NotNil(t, fd.ValueScanner)
	})
}

// =============================================================================
// Builder Chain Test
// =============================================================================

// TestNumericFieldBuilderChain tests method chaining.
func TestNumericFieldBuilderChain(t *testing.T) {
	t.Parallel()

	fd := field.Int64("count").
		Unique().Optional().Nillable().Immutable().
		Comment("A count field").
		StructTag(`json:"count,omitempty"`).
		StorageKey("counter").
		Default(0).
		Descriptor()

	assert.Equal(t, "count", fd.Name)
	assert.True(t, fd.Unique)
	assert.True(t, fd.Optional)
	assert.True(t, fd.Nillable)
	assert.True(t, fd.Immutable)
	assert.Equal(t, "A count field", fd.Comment)
	assert.Equal(t, `json:"count,omitempty"`, fd.Tag)
	assert.Equal(t, "counter", fd.StorageKey)
	assert.Equal(t, int64(0), fd.Default)
}
