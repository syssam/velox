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
