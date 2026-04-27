package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"testing"
	"time"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	"ariga.io/atlas/sql/sqlite"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema/field"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Suppress unused import warnings.
var (
	_ fs.File
	_ = reflect.TypeOf
	_ migrate.File
)

// ---------------------------------------------------------------------------
// compareVersions / parseVersion
// ---------------------------------------------------------------------------

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1, v2   string
		expected int
	}{
		{"8.0.0", "8.0.0", 0},
		{"8.0.1", "8.0.0", 1},
		{"8.0.0", "8.0.1", -1},
		{"8.1.0", "8.0.0", 1},
		{"9.0.0", "8.0.0", 1},
		{"5.7.0", "8.0.0", -1},
		{"10.2.1-MariaDB", "10.2.0", 1},
		// Partial versions.
		{"8", "8.0.0", 0},
		{"8.1", "8.0.0", 1},
		// Invalid versions.
		{"invalid", "8.0.0", -1},
		{"8.0.0", "invalid", 1},
		{"invalid", "invalid", 0},
		{"", "", 0},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_vs_%s", tt.v1, tt.v2), func(t *testing.T) {
			assert.Equal(t, tt.expected, compareVersions(tt.v1, tt.v2))
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		v     string
		ok    bool
		major int
		minor int
		patch int
	}{
		{"8.0.1", true, 8, 0, 1},
		{"10.2.1-MariaDB", true, 10, 2, 1},
		{"8", true, 8, 0, 0},
		{"8.1", true, 8, 1, 0},
		{"abc", false, 0, 0, 0},
		{"", false, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.v, func(t *testing.T) {
			ver, ok := parseVersion(tt.v)
			assert.Equal(t, tt.ok, ok)
			if ok {
				assert.Equal(t, tt.major, ver.major)
				assert.Equal(t, tt.minor, ver.minor)
				assert.Equal(t, tt.patch, ver.patch)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ConstName (via sqlschema.CascadeAction type alias)
// ---------------------------------------------------------------------------

func TestReferenceOption_ConstName(t *testing.T) {
	tests := []struct {
		input    ReferenceOption
		expected string
	}{
		{NoAction, "NoAction"},
		{SetDefault, "SetDefault"},
		{Cascade, "Cascade"},
		{SetNull, "SetNull"},
		{Restrict, "Restrict"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.input.ConstName())
		})
	}
}

// ---------------------------------------------------------------------------
// indexType
// ---------------------------------------------------------------------------

func TestIndexType(t *testing.T) {
	t.Run("nil annotation", func(t *testing.T) {
		idx := &Index{Name: "idx"}
		v, ok := indexType(idx, dialect.Postgres)
		assert.False(t, ok)
		assert.Empty(t, v)
	})

	t.Run("dialect-specific type", func(t *testing.T) {
		idx := &Index{
			Name: "idx",
			Annotation: &sqlschema.IndexAnnotation{
				Types: map[string]string{
					dialect.Postgres: "GIN",
					dialect.MySQL:    "BTREE",
				},
			},
		}
		v, ok := indexType(idx, dialect.Postgres)
		assert.True(t, ok)
		assert.Equal(t, "GIN", v)

		v, ok = indexType(idx, dialect.MySQL)
		assert.True(t, ok)
		assert.Equal(t, "BTREE", v)

		v, ok = indexType(idx, dialect.SQLite)
		assert.False(t, ok)
		assert.Empty(t, v)
	})

	t.Run("generic type", func(t *testing.T) {
		idx := &Index{
			Name: "idx",
			Annotation: &sqlschema.IndexAnnotation{
				Type: "HASH",
			},
		}
		v, ok := indexType(idx, dialect.Postgres)
		assert.True(t, ok)
		assert.Equal(t, "HASH", v)
	})

	t.Run("dialect-specific takes precedence over generic", func(t *testing.T) {
		idx := &Index{
			Name: "idx",
			Annotation: &sqlschema.IndexAnnotation{
				Type: "HASH",
				Types: map[string]string{
					dialect.Postgres: "GIN",
				},
			},
		}
		v, ok := indexType(idx, dialect.Postgres)
		assert.True(t, ok)
		assert.Equal(t, "GIN", v)
	})
}

// ---------------------------------------------------------------------------
// Indexes.append
// ---------------------------------------------------------------------------

func TestIndexes_Append(t *testing.T) {
	var idxs Indexes
	idx1 := &Index{Name: "a"}
	idx2 := &Index{Name: "b"}
	idx3 := &Index{Name: "a"} // duplicate name

	idxs.append(idx1)
	idxs.append(idx2)
	idxs.append(idx3) // should be ignored

	assert.Len(t, idxs, 2)
}

// ---------------------------------------------------------------------------
// Table.index (implicit index lookup)
// ---------------------------------------------------------------------------

func TestTable_IndexLookup(t *testing.T) {
	t.Run("by realname", func(t *testing.T) {
		tbl := NewTable("test").
			AddColumn(&Column{Name: "email", Type: field.TypeString})
		tbl.Indexes = append(tbl.Indexes, &Index{
			Name:     "idx_email",
			realname: "real_idx_email",
			Columns:  []*Column{tbl.Columns[0]},
		})
		idx, ok := tbl.index("real_idx_email")
		assert.True(t, ok)
		assert.Equal(t, "idx_email", idx.Name)
	})

	t.Run("by single column name", func(t *testing.T) {
		tbl := NewTable("test").
			AddColumn(&Column{Name: "email", Type: field.TypeString})
		tbl.Indexes = append(tbl.Indexes, &Index{
			Name:    "idx_email",
			Columns: []*Column{tbl.Columns[0]},
		})
		idx, ok := tbl.index("email")
		assert.True(t, ok)
		assert.Equal(t, "idx_email", idx.Name)
	})

	t.Run("implicit unique from column", func(t *testing.T) {
		tbl := NewTable("test").
			AddColumn(&Column{Name: "email", Type: field.TypeString, Unique: true})
		idx, ok := tbl.index("email")
		assert.True(t, ok)
		assert.True(t, idx.Unique)
	})

	t.Run("postgres naming convention", func(t *testing.T) {
		tbl := NewTable("users").
			AddColumn(&Column{Name: "email", Type: field.TypeString, Unique: true})
		// Simulate postgres-style name: users_email_key
		idx, ok := tbl.index("users_email_key")
		assert.True(t, ok)
		assert.True(t, idx.Unique)
		assert.Equal(t, "email", idx.Name)
	})

	t.Run("not found", func(t *testing.T) {
		tbl := NewTable("test").
			AddColumn(&Column{Name: "id", Type: field.TypeInt})
		idx, ok := tbl.index("nonexistent")
		assert.False(t, ok)
		assert.Nil(t, idx)
	})
}

// ---------------------------------------------------------------------------
// setAtChecks
// ---------------------------------------------------------------------------

func TestSetAtChecks(t *testing.T) {
	t.Run("single check", func(t *testing.T) {
		et := &Table{
			Annotation: &sqlschema.Annotation{Check: "age > 0"},
		}
		at := schema.NewTable("test")
		setAtChecks(et, at)
		require.Len(t, at.Checks(), 1)
		assert.Equal(t, "age > 0", at.Checks()[0].Expr)
	})

	t.Run("named checks", func(t *testing.T) {
		et := &Table{
			Annotation: &sqlschema.Annotation{
				Checks: map[string]string{
					"check_age":    "age >= 0",
					"check_salary": "salary > 0",
				},
			},
		}
		at := schema.NewTable("test")
		setAtChecks(et, at)
		require.Len(t, at.Checks(), 2)
		// Sorted by name
		assert.Equal(t, "check_age", at.Checks()[0].Name)
		assert.Equal(t, "check_salary", at.Checks()[1].Name)
	})

	t.Run("both single and named checks", func(t *testing.T) {
		et := &Table{
			Annotation: &sqlschema.Annotation{
				Check: "status IN ('active', 'inactive')",
				Checks: map[string]string{
					"check_age": "age >= 0",
				},
			},
		}
		at := schema.NewTable("test")
		setAtChecks(et, at)
		require.Len(t, at.Checks(), 2)
	})
}

// ---------------------------------------------------------------------------
// atDefault
// ---------------------------------------------------------------------------

func TestAtDefault(t *testing.T) {
	mkAtlas := func(d string) *Atlas {
		return &Atlas{
			sqlDialect: &SQLite{Driver: nopDriver{dialect: d}},
			dialect:    d,
		}
	}

	t.Run("nil default", func(t *testing.T) {
		a := mkAtlas(dialect.SQLite)
		c1 := &Column{Name: "test", Type: field.TypeString}
		c2 := schema.NewColumn("test")
		require.NoError(t, a.atDefault(c1, c2))
		assert.Nil(t, c2.Default)
	})

	t.Run("Expr default", func(t *testing.T) {
		a := mkAtlas(dialect.SQLite)
		c1 := &Column{Name: "test", Type: field.TypeTime, Default: Expr("CURRENT_TIMESTAMP")}
		c2 := schema.NewColumn("test")
		require.NoError(t, a.atDefault(c1, c2))
		raw, ok := c2.Default.(*schema.RawExpr)
		require.True(t, ok)
		assert.Equal(t, "(CURRENT_TIMESTAMP)", raw.X)
	})

	t.Run("Expr already parenthesized", func(t *testing.T) {
		a := mkAtlas(dialect.SQLite)
		c1 := &Column{Name: "test", Type: field.TypeTime, Default: Expr("(now())")}
		c2 := schema.NewColumn("test")
		require.NoError(t, a.atDefault(c1, c2))
		raw, ok := c2.Default.(*schema.RawExpr)
		require.True(t, ok)
		assert.Equal(t, "(now())", raw.X)
	})

	t.Run("Expr single char", func(t *testing.T) {
		a := mkAtlas(dialect.SQLite)
		c1 := &Column{Name: "test", Type: field.TypeTime, Default: Expr("x")}
		c2 := schema.NewColumn("test")
		require.NoError(t, a.atDefault(c1, c2))
		raw, ok := c2.Default.(*schema.RawExpr)
		require.True(t, ok)
		assert.Equal(t, "x", raw.X) // len <= 1, no parenthesizing
	})

	t.Run("map[string]Expr default matching dialect", func(t *testing.T) {
		a := mkAtlas(dialect.SQLite)
		c1 := &Column{Name: "test", Type: field.TypeTime, Default: map[string]Expr{
			dialect.SQLite: "CURRENT_TIMESTAMP",
		}}
		c2 := schema.NewColumn("test")
		require.NoError(t, a.atDefault(c1, c2))
		raw, ok := c2.Default.(*schema.RawExpr)
		require.True(t, ok)
		assert.Equal(t, "(CURRENT_TIMESTAMP)", raw.X)
	})

	t.Run("map[string]Expr default no matching dialect", func(t *testing.T) {
		a := mkAtlas(dialect.SQLite)
		c1 := &Column{Name: "test", Type: field.TypeTime, Default: map[string]Expr{
			dialect.Postgres: "now()",
		}}
		c2 := schema.NewColumn("test")
		require.NoError(t, a.atDefault(c1, c2))
		assert.Nil(t, c2.Default)
	})

	t.Run("string default", func(t *testing.T) {
		a := mkAtlas(dialect.SQLite)
		c1 := &Column{Name: "test", Type: field.TypeString, Default: "hello"}
		c2 := schema.NewColumn("test")
		require.NoError(t, a.atDefault(c1, c2))
		lit, ok := c2.Default.(*schema.Literal)
		require.True(t, ok)
		assert.Equal(t, "hello", lit.V)
	})

	t.Run("enum default", func(t *testing.T) {
		a := mkAtlas(dialect.SQLite)
		c1 := &Column{Name: "status", Type: field.TypeEnum, Default: "active"}
		c2 := schema.NewColumn("status")
		require.NoError(t, a.atDefault(c1, c2))
		lit, ok := c2.Default.(*schema.Literal)
		require.True(t, ok)
		assert.Equal(t, "active", lit.V)
	})

	t.Run("JSON default", func(t *testing.T) {
		a := mkAtlas(dialect.SQLite)
		c1 := &Column{Name: "data", Type: field.TypeJSON, Default: "{}"}
		c2 := schema.NewColumn("data")
		require.NoError(t, a.atDefault(c1, c2))
		lit, ok := c2.Default.(*schema.Literal)
		require.True(t, ok)
		assert.Equal(t, "{}", lit.V)
	})

	t.Run("int default", func(t *testing.T) {
		a := mkAtlas(dialect.SQLite)
		c1 := &Column{Name: "age", Type: field.TypeInt64, Default: int64(25)}
		c2 := schema.NewColumn("age")
		require.NoError(t, a.atDefault(c1, c2))
		raw, ok := c2.Default.(*schema.RawExpr)
		require.True(t, ok)
		assert.Equal(t, "25", raw.X)
	})

	t.Run("bool default", func(t *testing.T) {
		a := mkAtlas(dialect.SQLite)
		c1 := &Column{Name: "active", Type: field.TypeBool, Default: true}
		c2 := schema.NewColumn("active")
		require.NoError(t, a.atDefault(c1, c2))
		raw, ok := c2.Default.(*schema.RawExpr)
		require.True(t, ok)
		assert.Equal(t, "true", raw.X)
	})

	t.Run("invalid string default for JSON column", func(t *testing.T) {
		a := mkAtlas(dialect.SQLite)
		c1 := &Column{Name: "data", Type: field.TypeJSON, Default: 42}
		c2 := schema.NewColumn("data")
		err := a.atDefault(c1, c2)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid default value for JSON column")
	})

	t.Run("invalid string default for string column", func(t *testing.T) {
		a := mkAtlas(dialect.SQLite)
		c1 := &Column{Name: "name", Type: field.TypeString, Default: 42}
		c2 := schema.NewColumn("name")
		err := a.atDefault(c1, c2)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid default value for string column")
	})
}

// ---------------------------------------------------------------------------
// SQLite atTypeC
// ---------------------------------------------------------------------------

func TestSQLite_AtTypeC(t *testing.T) {
	d := &SQLite{Driver: nopDriver{dialect: dialect.SQLite}}

	tests := []struct {
		name     string
		col      *Column
		wantType string
	}{
		{"bool", &Column{Type: field.TypeBool}, "bool"},
		{"int8", &Column{Type: field.TypeInt8}, "integer"},
		{"uint8", &Column{Type: field.TypeUint8}, "integer"},
		{"int16", &Column{Type: field.TypeInt16}, "integer"},
		{"uint16", &Column{Type: field.TypeUint16}, "integer"},
		{"int32", &Column{Type: field.TypeInt32}, "integer"},
		{"uint32", &Column{Type: field.TypeUint32}, "integer"},
		{"int", &Column{Type: field.TypeInt}, "integer"},
		{"int64", &Column{Type: field.TypeInt64}, "integer"},
		{"uint", &Column{Type: field.TypeUint}, "integer"},
		{"uint64", &Column{Type: field.TypeUint64}, "integer"},
		{"bytes", &Column{Type: field.TypeBytes}, "blob"},
		{"string", &Column{Type: field.TypeString}, "text"},
		{"enum", &Column{Type: field.TypeEnum}, "text"},
		{"float32", &Column{Type: field.TypeFloat32}, "real"},
		{"float64", &Column{Type: field.TypeFloat64}, "real"},
		{"time", &Column{Type: field.TypeTime}, "datetime"},
		{"json", &Column{Type: field.TypeJSON}, "json"},
		{"uuid", &Column{Type: field.TypeUUID}, "uuid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c2 := schema.NewColumn("test")
			c2.Type = &schema.ColumnType{}
			err := d.atTypeC(tt.col, c2)
			require.NoError(t, err)
			require.NotNil(t, c2.Type.Type)
		})
	}

	t.Run("SchemaType override", func(t *testing.T) {
		c1 := &Column{
			Type:       field.TypeString,
			SchemaType: map[string]string{dialect.SQLite: "text"},
		}
		c2 := schema.NewColumn("test")
		c2.Type = &schema.ColumnType{}
		err := d.atTypeC(c1, c2)
		require.NoError(t, err)
	})

	t.Run("TypeOther", func(t *testing.T) {
		c1 := &Column{Type: field.TypeOther, typ: "custom_type"}
		c2 := schema.NewColumn("test")
		c2.Type = &schema.ColumnType{}
		err := d.atTypeC(c1, c2)
		require.NoError(t, err)
	})
}

// ---------------------------------------------------------------------------
// MySQL atTypeC
// ---------------------------------------------------------------------------

func TestMySQL_AtTypeC(t *testing.T) {
	d := &MySQL{Driver: nopDriver{dialect: dialect.MySQL}, version: "8.0.0"}

	tests := []struct {
		name string
		col  *Column
	}{
		{"bool", &Column{Type: field.TypeBool}},
		{"int8", &Column{Type: field.TypeInt8}},
		{"uint8", &Column{Type: field.TypeUint8}},
		{"int16", &Column{Type: field.TypeInt16}},
		{"uint16", &Column{Type: field.TypeUint16}},
		{"int32", &Column{Type: field.TypeInt32}},
		{"uint32", &Column{Type: field.TypeUint32}},
		{"int64", &Column{Type: field.TypeInt64}},
		{"int", &Column{Type: field.TypeInt}},
		{"uint64", &Column{Type: field.TypeUint64}},
		{"uint", &Column{Type: field.TypeUint}},
		{"bytes_default", &Column{Type: field.TypeBytes}},
		{"bytes_small", &Column{Type: field.TypeBytes, Size: 100}},
		{"bytes_medium", &Column{Type: field.TypeBytes, Size: 1 << 20}},
		{"bytes_large", &Column{Type: field.TypeBytes, Size: 1 << 30}},
		{"json", &Column{Type: field.TypeJSON}},
		{"string", &Column{Type: field.TypeString, Size: 100}},
		{"string_tinytext", &Column{Type: field.TypeString, typ: "tinytext"}},
		{"string_text", &Column{Type: field.TypeString, typ: "text"}},
		{"string_mediumtext", &Column{Type: field.TypeString, Size: 1<<24 - 1}},
		{"string_longtext", &Column{Type: field.TypeString, Size: 1 << 24}},
		{"float32", &Column{Type: field.TypeFloat32}},
		{"float64", &Column{Type: field.TypeFloat64}},
		{"time", &Column{Type: field.TypeTime}},
		{"enum", &Column{Type: field.TypeEnum, Enums: []string{"a", "b"}}},
		{"uuid", &Column{Type: field.TypeUUID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c2 := schema.NewColumn("test")
			c2.Type = &schema.ColumnType{}
			err := d.atTypeC(tt.col, c2)
			require.NoError(t, err)
			require.NotNil(t, c2.Type.Type)
		})
	}

	t.Run("SchemaType override", func(t *testing.T) {
		c1 := &Column{
			Type:       field.TypeString,
			SchemaType: map[string]string{dialect.MySQL: "mediumtext"},
		}
		c2 := schema.NewColumn("test")
		c2.Type = &schema.ColumnType{}
		err := d.atTypeC(c1, c2)
		require.NoError(t, err)
	})

	t.Run("JSON old MySQL version", func(t *testing.T) {
		d2 := &MySQL{Driver: nopDriver{dialect: dialect.MySQL}, version: "5.6.0"}
		c1 := &Column{Type: field.TypeJSON}
		c2 := schema.NewColumn("test")
		c2.Type = &schema.ColumnType{}
		err := d2.atTypeC(c1, c2)
		require.NoError(t, err)
	})

	t.Run("MariaDB UUID", func(t *testing.T) {
		d2 := &MySQL{Driver: nopDriver{dialect: dialect.MySQL}, version: "10.7.0-MariaDB"}
		c1 := &Column{Type: field.TypeUUID}
		c2 := schema.NewColumn("test")
		c2.Type = &schema.ColumnType{}
		err := d2.atTypeC(c1, c2)
		require.NoError(t, err)
	})
}

// ---------------------------------------------------------------------------
// Postgres atTypeC
// ---------------------------------------------------------------------------

func TestPostgres_AtTypeC(t *testing.T) {
	d := &Postgres{Driver: nopDriver{dialect: dialect.Postgres}, version: "14.0.0"}

	tests := []struct {
		name string
		col  *Column
	}{
		{"bool", &Column{Type: field.TypeBool}},
		{"int8", &Column{Type: field.TypeInt8}},
		{"uint8", &Column{Type: field.TypeUint8}},
		{"int16", &Column{Type: field.TypeInt16}},
		{"uint16", &Column{Type: field.TypeUint16}},
		{"int32", &Column{Type: field.TypeInt32}},
		{"uint32", &Column{Type: field.TypeUint32}},
		{"int64", &Column{Type: field.TypeInt64}},
		{"int", &Column{Type: field.TypeInt}},
		{"uint64", &Column{Type: field.TypeUint64}},
		{"uint", &Column{Type: field.TypeUint}},
		{"float32", &Column{Type: field.TypeFloat32}},
		{"float64", &Column{Type: field.TypeFloat64}},
		{"bytes", &Column{Type: field.TypeBytes}},
		{"uuid", &Column{Type: field.TypeUUID}},
		{"json", &Column{Type: field.TypeJSON}},
		{"string", &Column{Type: field.TypeString, Size: 100}},
		{"string_large", &Column{Type: field.TypeString, Size: maxCharSize + 1}},
		{"time", &Column{Type: field.TypeTime}},
		{"enum", &Column{Type: field.TypeEnum, Enums: []string{"a", "b"}}},
		{"other", &Column{Type: field.TypeOther, typ: "custom"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c2 := schema.NewColumn("test")
			c2.Type = &schema.ColumnType{}
			err := d.atTypeC(tt.col, c2)
			require.NoError(t, err)
			require.NotNil(t, c2.Type.Type)
		})
	}

	t.Run("SchemaType override", func(t *testing.T) {
		c1 := &Column{
			Type:       field.TypeString,
			SchemaType: map[string]string{dialect.Postgres: "text"},
		}
		c2 := schema.NewColumn("test")
		c2.Type = &schema.ColumnType{}
		err := d.atTypeC(c1, c2)
		require.NoError(t, err)
	})

	t.Run("SchemaType serial with foreign key", func(t *testing.T) {
		fk := &ForeignKey{Symbol: "fk_test"}
		c1 := &Column{
			Type:       field.TypeInt64,
			SchemaType: map[string]string{dialect.Postgres: "serial"},
			foreign:    fk,
		}
		c2 := schema.NewColumn("test")
		c2.Type = &schema.ColumnType{}
		err := d.atTypeC(c1, c2)
		require.NoError(t, err)
	})
}

// ---------------------------------------------------------------------------
// MySQL specific helpers
// ---------------------------------------------------------------------------

func TestMySQL_Mariadb(t *testing.T) {
	t.Run("is MariaDB", func(t *testing.T) {
		d := &MySQL{version: "10.5.8-MariaDB"}
		v, ok := d.mariadb()
		assert.True(t, ok)
		assert.Equal(t, "10.5.8", v)
	})

	t.Run("not MariaDB", func(t *testing.T) {
		d := &MySQL{version: "8.0.26"}
		_, ok := d.mariadb()
		assert.False(t, ok)
	})
}

func TestMySQL_DefaultSize(t *testing.T) {
	t.Run("MySQL 5.7+", func(t *testing.T) {
		d := &MySQL{version: "5.7.0"}
		c := &Column{Type: field.TypeString}
		assert.Equal(t, DefaultStringLen, d.defaultSize(c))
	})

	t.Run("MySQL 5.6 with unique", func(t *testing.T) {
		d := &MySQL{version: "5.6.0"}
		c := &Column{Type: field.TypeString, Unique: true}
		assert.Equal(t, int64(191), d.defaultSize(c))
	})

	t.Run("MySQL 5.6 non-unique non-indexed", func(t *testing.T) {
		d := &MySQL{version: "5.6.0"}
		c := &Column{Type: field.TypeString}
		assert.Equal(t, DefaultStringLen, d.defaultSize(c))
	})

	t.Run("MariaDB 10.2.2+", func(t *testing.T) {
		d := &MySQL{version: "10.2.2-MariaDB"}
		c := &Column{Type: field.TypeString, Unique: true}
		assert.Equal(t, DefaultStringLen, d.defaultSize(c))
	})

	t.Run("MariaDB 10.1 with unique", func(t *testing.T) {
		d := &MySQL{version: "10.1.0-MariaDB"}
		c := &Column{Type: field.TypeString, Unique: true}
		assert.Equal(t, int64(191), d.defaultSize(c))
	})
}

func TestMySQL_SupportsDefault(t *testing.T) {
	t.Run("MySQL 8 Expr", func(t *testing.T) {
		d := &MySQL{version: "8.0.0"}
		c := &Column{Type: field.TypeString, Size: 100, Default: Expr("NOW()")}
		assert.True(t, d.supportsDefault(c))
	})

	t.Run("MySQL 5.6 Expr", func(t *testing.T) {
		d := &MySQL{version: "5.6.0"}
		c := &Column{Type: field.TypeString, Size: 100, Default: Expr("NOW()")}
		assert.False(t, d.supportsDefault(c))
	})

	t.Run("MariaDB 10.2 Expr", func(t *testing.T) {
		d := &MySQL{version: "10.2.0-MariaDB"}
		c := &Column{Type: field.TypeString, Size: 100, Default: Expr("NOW()")}
		assert.True(t, d.supportsDefault(c))
	})

	t.Run("MariaDB 10.1 Expr", func(t *testing.T) {
		d := &MySQL{version: "10.1.0-MariaDB"}
		c := &Column{Type: field.TypeString, Size: 100, Default: Expr("NOW()")}
		assert.False(t, d.supportsDefault(c))
	})

	t.Run("MySQL 8 map[string]Expr", func(t *testing.T) {
		d := &MySQL{version: "8.0.0"}
		c := &Column{Type: field.TypeString, Size: 100, Default: map[string]Expr{dialect.MySQL: "NOW()"}}
		assert.True(t, d.supportsDefault(c))
	})

	t.Run("literal default", func(t *testing.T) {
		d := &MySQL{version: "8.0.0"}
		c := &Column{Type: field.TypeString, Size: 100, Default: "hello"}
		assert.True(t, d.supportsDefault(c))
	})

	t.Run("literal default text too large", func(t *testing.T) {
		d := &MySQL{version: "8.0.0"}
		c := &Column{Type: field.TypeString, Size: 1 << 16, Default: "hello"}
		assert.False(t, d.supportsDefault(c))
	})

	t.Run("MariaDB literal default text", func(t *testing.T) {
		d := &MySQL{version: "10.5.0-MariaDB"}
		c := &Column{Type: field.TypeString, Size: 1 << 16, Default: "hello"}
		assert.True(t, d.supportsDefault(c))
	})
}

func TestMySQL_SupportsUUID(t *testing.T) {
	t.Run("MariaDB 10.7+", func(t *testing.T) {
		d := &MySQL{version: "10.7.0-MariaDB"}
		assert.True(t, d.supportsUUID())
	})

	t.Run("MariaDB 10.6", func(t *testing.T) {
		d := &MySQL{version: "10.6.0-MariaDB"}
		assert.False(t, d.supportsUUID())
	})

	t.Run("MySQL", func(t *testing.T) {
		d := &MySQL{version: "8.0.0"}
		assert.False(t, d.supportsUUID())
	})
}

func TestMySQL_AtImplicitIndexName(t *testing.T) {
	d := &MySQL{}

	t.Run("exact match", func(t *testing.T) {
		idx := &Index{Name: "email"}
		c := &Column{Name: "email"}
		assert.True(t, d.atImplicitIndexName(idx, c))
	})

	t.Run("numbered suffix", func(t *testing.T) {
		idx := &Index{Name: "email_2"}
		c := &Column{Name: "email"}
		assert.True(t, d.atImplicitIndexName(idx, c))
	})

	t.Run("not matching", func(t *testing.T) {
		idx := &Index{Name: "name"}
		c := &Column{Name: "email"}
		assert.False(t, d.atImplicitIndexName(idx, c))
	})
}

func TestMySQL_AtTable(t *testing.T) {
	t.Run("default charset and collation", func(t *testing.T) {
		d := &MySQL{version: "8.0.0"}
		et := &Table{}
		at := schema.NewTable("test")
		d.atTable(et, at)
		// Should set utf8mb4 charset and utf8mb4_bin collation
		var charset, collation string
		for _, a := range at.Attrs {
			switch v := a.(type) {
			case *schema.Charset:
				charset = v.V
			case *schema.Collation:
				collation = v.V
			}
		}
		assert.Equal(t, "utf8mb4", charset)
		assert.Equal(t, "utf8mb4_bin", collation)
	})

	t.Run("annotation overrides", func(t *testing.T) {
		d := &MySQL{version: "8.0.16"}
		et := &Table{
			Annotation: &sqlschema.Annotation{
				Charset:   "latin1",
				Collation: "latin1_general_ci",
				Options:   "ENGINE=InnoDB",
				Check:     "id > 0",
			},
		}
		at := schema.NewTable("test")
		d.atTable(et, at)
		var charset, collation string
		for _, a := range at.Attrs {
			switch v := a.(type) {
			case *schema.Charset:
				charset = v.V
			case *schema.Collation:
				collation = v.V
			}
		}
		assert.Equal(t, "latin1", charset)
		assert.Equal(t, "latin1_general_ci", collation)
	})

	t.Run("MySQL < 8.0.16 skips checks", func(t *testing.T) {
		d := &MySQL{version: "5.7.0"}
		et := &Table{
			Annotation: &sqlschema.Annotation{
				Check: "id > 0",
			},
		}
		at := schema.NewTable("test")
		d.atTable(et, at)
		assert.Empty(t, at.Checks())
	})
}

// ---------------------------------------------------------------------------
// Postgres specific helpers
// ---------------------------------------------------------------------------

func TestPostgres_AtIndex(t *testing.T) {
	d := &Postgres{version: "14.0.0"}

	t.Run("basic index", func(t *testing.T) {
		at := schema.NewTable("test")
		at.AddColumns(schema.NewColumn("name"))
		idx1 := &Index{
			Name:    "idx_name",
			Columns: []*Column{{Name: "name"}},
		}
		idx2 := schema.NewIndex("idx_name")
		err := d.atIndex(idx1, at, idx2)
		require.NoError(t, err)
		require.Len(t, idx2.Parts, 1)
	})

	t.Run("index with operator class", func(t *testing.T) {
		at := schema.NewTable("test")
		at.AddColumns(schema.NewColumn("data"))
		idx1 := &Index{
			Name:    "idx_data",
			Columns: []*Column{{Name: "data"}},
			Annotation: &sqlschema.IndexAnnotation{
				OpClass: "gin_trgm_ops",
			},
		}
		idx2 := schema.NewIndex("idx_data")
		err := d.atIndex(idx1, at, idx2)
		require.NoError(t, err)
		require.Len(t, idx2.Parts, 1)
	})

	t.Run("index with type", func(t *testing.T) {
		at := schema.NewTable("test")
		at.AddColumns(schema.NewColumn("data"))
		idx1 := &Index{
			Name:    "idx_data",
			Columns: []*Column{{Name: "data"}},
			Annotation: &sqlschema.IndexAnnotation{
				Type: "GIN",
			},
		}
		idx2 := schema.NewIndex("idx_data")
		err := d.atIndex(idx1, at, idx2)
		require.NoError(t, err)
	})

	t.Run("index with include columns", func(t *testing.T) {
		at := schema.NewTable("test")
		at.AddColumns(schema.NewColumn("name"), schema.NewColumn("email"))
		idx1 := &Index{
			Name:    "idx_name",
			Columns: []*Column{{Name: "name"}},
			Annotation: &sqlschema.IndexAnnotation{
				IncludeColumns: []string{"email"},
			},
		}
		idx2 := schema.NewIndex("idx_name")
		err := d.atIndex(idx1, at, idx2)
		require.NoError(t, err)
	})

	t.Run("index with WHERE predicate", func(t *testing.T) {
		at := schema.NewTable("test")
		at.AddColumns(schema.NewColumn("status"))
		idx1 := &Index{
			Name:    "idx_status",
			Columns: []*Column{{Name: "status"}},
			Annotation: &sqlschema.IndexAnnotation{
				Where: "status = 'active'",
			},
		}
		idx2 := schema.NewIndex("idx_status")
		err := d.atIndex(idx1, at, idx2)
		require.NoError(t, err)
		var pred *postgres.IndexPredicate
		for _, a := range idx2.Attrs {
			if p, ok := a.(*postgres.IndexPredicate); ok {
				pred = p
				break
			}
		}
		require.NotNil(t, pred, "IndexPredicate attribute must be set on the Atlas index")
		assert.Equal(t, "status = 'active'", pred.P)
	})

	t.Run("index with missing column", func(t *testing.T) {
		at := schema.NewTable("test")
		idx1 := &Index{
			Name:    "idx_missing",
			Columns: []*Column{{Name: "nonexistent"}},
		}
		idx2 := schema.NewIndex("idx_missing")
		err := d.atIndex(idx1, at, idx2)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nonexistent")
	})

	t.Run("include column missing", func(t *testing.T) {
		at := schema.NewTable("test")
		at.AddColumns(schema.NewColumn("name"))
		idx1 := &Index{
			Name:    "idx_name",
			Columns: []*Column{{Name: "name"}},
			Annotation: &sqlschema.IndexAnnotation{
				IncludeColumns: []string{"nonexistent"},
			},
		}
		idx2 := schema.NewIndex("idx_name")
		err := d.atIndex(idx1, at, idx2)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nonexistent")
	})

	t.Run("Postgres < 11 skips includes", func(t *testing.T) {
		d2 := &Postgres{version: "10.0.0"}
		at := schema.NewTable("test")
		at.AddColumns(schema.NewColumn("name"), schema.NewColumn("email"))
		idx1 := &Index{
			Name:    "idx_name",
			Columns: []*Column{{Name: "name"}},
			Annotation: &sqlschema.IndexAnnotation{
				IncludeColumns: []string{"email"},
			},
		}
		idx2 := schema.NewIndex("idx_name")
		err := d2.atIndex(idx1, at, idx2)
		require.NoError(t, err)
		// Includes should not be added for version < 11
	})
}

func TestPostgres_AtImplicitIndexName(t *testing.T) {
	d := &Postgres{}

	t.Run("exact match", func(t *testing.T) {
		idx := &Index{Name: "users_email_key"}
		tbl := &Table{Name: "users"}
		c := &Column{Name: "email"}
		assert.True(t, d.atImplicitIndexName(idx, tbl, c))
	})

	t.Run("numbered suffix", func(t *testing.T) {
		idx := &Index{Name: "users_email_key1"}
		tbl := &Table{Name: "users"}
		c := &Column{Name: "email"}
		assert.True(t, d.atImplicitIndexName(idx, tbl, c))
	})

	t.Run("not matching", func(t *testing.T) {
		idx := &Index{Name: "users_name_key"}
		tbl := &Table{Name: "users"}
		c := &Column{Name: "email"}
		assert.False(t, d.atImplicitIndexName(idx, tbl, c))
	})
}

// ---------------------------------------------------------------------------
// SQLite specific helpers
// ---------------------------------------------------------------------------

func TestSQLite_AtImplicitIndexName(t *testing.T) {
	d := &SQLite{}

	t.Run("exact column match", func(t *testing.T) {
		idx := &Index{Name: "email"}
		tbl := &Table{Name: "users"}
		c := &Column{Name: "email"}
		assert.True(t, d.atImplicitIndexName(idx, tbl, c))
	})

	t.Run("autoindex format", func(t *testing.T) {
		idx := &Index{Name: "sqlite_autoindex_users_1"}
		tbl := &Table{Name: "users"}
		c := &Column{Name: "email"}
		assert.True(t, d.atImplicitIndexName(idx, tbl, c))
	})

	t.Run("autoindex 0 is invalid", func(t *testing.T) {
		idx := &Index{Name: "sqlite_autoindex_users_0"}
		tbl := &Table{Name: "users"}
		c := &Column{Name: "email"}
		assert.False(t, d.atImplicitIndexName(idx, tbl, c))
	})

	t.Run("not matching", func(t *testing.T) {
		idx := &Index{Name: "something_else"}
		tbl := &Table{Name: "users"}
		c := &Column{Name: "email"}
		assert.False(t, d.atImplicitIndexName(idx, tbl, c))
	})
}

func TestSQLite_AtIndex(t *testing.T) {
	d := &SQLite{}

	t.Run("with WHERE predicate", func(t *testing.T) {
		at := schema.NewTable("test")
		at.AddColumns(schema.NewColumn("status"))
		idx1 := &Index{
			Name:    "idx_status",
			Columns: []*Column{{Name: "status"}},
			Annotation: &sqlschema.IndexAnnotation{
				Where: "status = 'active'",
			},
		}
		idx2 := schema.NewIndex("idx_status")
		err := d.atIndex(idx1, at, idx2)
		require.NoError(t, err)
		var pred *sqlite.IndexPredicate
		for _, a := range idx2.Attrs {
			if p, ok := a.(*sqlite.IndexPredicate); ok {
				pred = p
				break
			}
		}
		require.NotNil(t, pred, "IndexPredicate attribute must be set on the Atlas index")
		assert.Equal(t, "status = 'active'", pred.P)
	})

	t.Run("missing column", func(t *testing.T) {
		at := schema.NewTable("test")
		idx1 := &Index{
			Name:    "idx_missing",
			Columns: []*Column{{Name: "nonexistent"}},
		}
		idx2 := schema.NewIndex("idx_missing")
		err := d.atIndex(idx1, at, idx2)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// MySQL atIndex
// ---------------------------------------------------------------------------

func TestMySQL_AtIndex(t *testing.T) {
	d := &MySQL{version: "8.0.0"}

	t.Run("with index type", func(t *testing.T) {
		at := schema.NewTable("test")
		at.AddColumns(schema.NewColumn("data"))
		idx1 := &Index{
			Name:    "idx_data",
			Columns: []*Column{{Name: "data"}},
			Annotation: &sqlschema.IndexAnnotation{
				Type: "BTREE",
			},
		}
		idx2 := schema.NewIndex("idx_data")
		err := d.atIndex(idx1, at, idx2)
		require.NoError(t, err)
	})

	t.Run("with prefix", func(t *testing.T) {
		at := schema.NewTable("test")
		col := schema.NewColumn("text_col")
		at.AddColumns(col)
		idx1 := &Index{
			Name:    "idx_text",
			Columns: []*Column{{Name: "text_col"}},
			Annotation: &sqlschema.IndexAnnotation{
				Prefix: 100,
			},
		}
		idx2 := schema.NewIndex("idx_text")
		err := d.atIndex(idx1, at, idx2)
		require.NoError(t, err)
	})

	t.Run("missing column", func(t *testing.T) {
		at := schema.NewTable("test")
		idx1 := &Index{
			Name:    "idx_missing",
			Columns: []*Column{{Name: "nonexistent"}},
		}
		idx2 := schema.NewIndex("idx_missing")
		err := d.atIndex(idx1, at, idx2)
		require.Error(t, err)
	})
}

// ---------------------------------------------------------------------------
// indexParts
// ---------------------------------------------------------------------------

func TestIndexParts(t *testing.T) {
	t.Run("nil annotation", func(t *testing.T) {
		idx := &Index{Name: "idx"}
		parts := indexParts(idx)
		assert.Empty(t, parts)
	})

	t.Run("prefix on single column", func(t *testing.T) {
		idx := &Index{
			Name:       "idx",
			Columns:    []*Column{{Name: "col"}},
			Annotation: &sqlschema.IndexAnnotation{Prefix: 10},
		}
		parts := indexParts(idx)
		assert.Equal(t, uint(10), parts["col"])
	})

	t.Run("prefix columns", func(t *testing.T) {
		idx := &Index{
			Name:    "idx",
			Columns: []*Column{{Name: "a"}, {Name: "b"}},
			Annotation: &sqlschema.IndexAnnotation{
				PrefixColumns: map[string]uint{"a": 10, "b": 20},
			},
		}
		parts := indexParts(idx)
		assert.Equal(t, uint(10), parts["a"])
		assert.Equal(t, uint(20), parts["b"])
	})
}

// ---------------------------------------------------------------------------
// indexOpClass
// ---------------------------------------------------------------------------

func TestIndexOpClass(t *testing.T) {
	t.Run("nil annotation", func(t *testing.T) {
		idx := &Index{Name: "idx"}
		opc := indexOpClass(idx)
		assert.Empty(t, opc)
	})

	t.Run("OpClass on single column", func(t *testing.T) {
		idx := &Index{
			Name:       "idx",
			Columns:    []*Column{{Name: "col"}},
			Annotation: &sqlschema.IndexAnnotation{OpClass: "gin_trgm_ops"},
		}
		opc := indexOpClass(idx)
		assert.Equal(t, "gin_trgm_ops", opc["col"])
	})

	t.Run("OpClassColumns", func(t *testing.T) {
		idx := &Index{
			Name:    "idx",
			Columns: []*Column{{Name: "a"}, {Name: "b"}},
			Annotation: &sqlschema.IndexAnnotation{
				OpClassColumns: map[string]string{"a": "text_pattern_ops"},
			},
		}
		opc := indexOpClass(idx)
		assert.Equal(t, "text_pattern_ops", opc["a"])
	})
}

// ---------------------------------------------------------------------------
// descIndexes
// ---------------------------------------------------------------------------

func TestDescIndexes(t *testing.T) {
	t.Run("nil annotation", func(t *testing.T) {
		idx := &Index{Name: "idx"}
		descs := descIndexes(idx)
		assert.Empty(t, descs)
	})

	t.Run("Desc on single column", func(t *testing.T) {
		idx := &Index{
			Name:       "idx",
			Columns:    []*Column{{Name: "col"}},
			Annotation: &sqlschema.IndexAnnotation{Desc: true},
		}
		descs := descIndexes(idx)
		assert.True(t, descs["col"])
	})

	t.Run("DescColumns", func(t *testing.T) {
		idx := &Index{
			Name:    "idx",
			Columns: []*Column{{Name: "a"}, {Name: "b"}},
			Annotation: &sqlschema.IndexAnnotation{
				DescColumns: map[string]bool{"a": true, "b": false},
			},
		}
		descs := descIndexes(idx)
		assert.True(t, descs["a"])
		assert.False(t, descs["b"])
	})
}

// ---------------------------------------------------------------------------
// WriteDriver.formatArg
// ---------------------------------------------------------------------------

func TestWriteDriver_FormatArg(t *testing.T) {
	w := &WriteDriver{Driver: nopDriver{dialect: dialect.MySQL}}

	tests := []struct {
		name     string
		input    any
		expected string
	}{
		{"nil", nil, "NULL"},
		{"int", int(42), "42"},
		{"int8", int8(8), "8"},
		{"int16", int16(16), "16"},
		{"int32", int32(32), "32"},
		{"int64", int64(64), "64"},
		{"uint", uint(1), "1"},
		{"uint8", uint8(2), "2"},
		{"uint16", uint16(3), "3"},
		{"uint32", uint32(4), "4"},
		{"uint64", uint64(5), "5"},
		{"float32", float32(1.5), "1.5"},
		{"float64", float64(2.5), "2.5"},
		{"bool_true", true, "1"},
		{"bool_false", false, "0"},
		{"string", "hello", "'hello'"},
		{"string_with_quote", "it's", "'it''s'"},
		{"json_raw", json.RawMessage(`{"key":"val"}`), `'{"key":"val"}'`},
		{"bytes", []byte("data"), "{{ BINARY_VALUE }}"},
		{"time", time.Now(), "{{ TIME_VALUE }}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := w.formatArg(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}

	t.Run("unknown type", func(t *testing.T) {
		result, err := w.formatArg(struct{}{})
		require.NoError(t, err)
		assert.Equal(t, "{{ VALUE }}", result)
	})

	t.Run("FormatFunc override", func(t *testing.T) {
		w2 := &WriteDriver{
			Driver: nopDriver{dialect: dialect.MySQL},
			FormatFunc: func(s string) (string, error) {
				return "CUSTOM(" + s + ")", nil
			},
		}
		result, err := w2.formatArg(42)
		require.NoError(t, err)
		assert.Equal(t, "CUSTOM(42)", result)
	})
}

// ---------------------------------------------------------------------------
// WriteDriver.Exec edge cases
// ---------------------------------------------------------------------------

func TestWriteDriver_Exec_ResultAssignment(t *testing.T) {
	b := &bytes.Buffer{}
	w := NewWriteDriver(dialect.MySQL, b)
	ctx := context.Background()

	// Test that sql.Result is assigned
	var r sql.Result
	err := w.Exec(ctx, "INSERT INTO t VALUES (1)", nil, &r)
	require.NoError(t, err)

	id, _ := r.LastInsertId()
	assert.Equal(t, int64(0), id)
	affected, _ := r.RowsAffected()
	assert.Equal(t, int64(0), affected)
}

func TestWriteDriver_Exec_InvalidArgs(t *testing.T) {
	b := &bytes.Buffer{}
	w := NewWriteDriver(dialect.MySQL, b)
	ctx := context.Background()

	// Passing wrong args type
	err := w.Exec(ctx, "SELECT ?", "not_a_slice", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected args type")
}

// ---------------------------------------------------------------------------
// WriteDriver.Query edge cases
// ---------------------------------------------------------------------------

func TestWriteDriver_Query_Update(t *testing.T) {
	b := &bytes.Buffer{}
	w := NewWriteDriver(dialect.MySQL, b)
	ctx := context.Background()

	// UPDATE triggers exec path via Query
	err := w.Query(ctx, "UPDATE users SET name = 'test'", nil, nil)
	require.NoError(t, err)
	assert.Contains(t, b.String(), "UPDATE users")
}

func TestWriteDriver_Query_Select_NopDriver(t *testing.T) {
	b := &bytes.Buffer{}
	w := NewWriteDriver(dialect.MySQL, b)
	ctx := context.Background()

	// SELECT with nop driver should error
	err := w.Query(ctx, "SELECT * FROM users", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query is not supported")
}

func TestWriteDriver_Query_Select_WithDriver(t *testing.T) {
	b := &bytes.Buffer{}
	w := &WriteDriver{
		Writer: b,
		Driver: nopDriver{dialect: dialect.MySQL},
	}
	ctx := context.Background()

	// SELECT with non-nil nopDriver should be a nopDriver case
	err := w.Query(ctx, "SELECT * FROM users", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query is not supported")
}

// ---------------------------------------------------------------------------
// DirWriter edge cases
// ---------------------------------------------------------------------------

func TestDirWriter_FlushErrors(t *testing.T) {
	t.Run("undocumented change", func(t *testing.T) {
		w := &DirWriter{}
		_, _ = w.Write([]byte("SELECT 1;"))
		err := w.Flush("test")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "undocumented change")
	})

	t.Run("no changes", func(t *testing.T) {
		w := &DirWriter{}
		err := w.Flush("test")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no changes to flush")
	})
}

func TestDirWriter_FlushChange(t *testing.T) {
	w := &DirWriter{Dir: &noopDir{}}
	_, _ = w.Write([]byte("SELECT 1;"))
	// FlushChange should call Change and then Flush.
	// With a noopDir, Flush will fail because WritePlan requires a real dir,
	// but we verify the Change is added.
	_ = w.FlushChange("test", "test comment")
	// The change should have been added
	assert.Len(t, w.changes, 1)
	assert.Equal(t, "test comment", w.changes[0].Comment)
}

// noopDir is a minimal migrate.Dir for testing
type noopDir struct{}

func (d *noopDir) Open(string) (fs.File, error)            { return nil, fmt.Errorf("not implemented") }
func (d *noopDir) WriteFile(string, []byte) error          { return fmt.Errorf("not implemented") }
func (d *noopDir) Files() ([]migrate.File, error)          { return nil, nil }
func (d *noopDir) Checksum() (migrate.HashFile, error)     { return nil, nil }
func (d *noopDir) WriteChecksum(hf migrate.HashFile) error { return nil }

// ---------------------------------------------------------------------------
// skipQuoted
// ---------------------------------------------------------------------------

func TestSkipQuoted(t *testing.T) {
	t.Run("single quoted", func(t *testing.T) {
		s := "'hello'"
		result, end := skipQuoted(s, 0)
		assert.Equal(t, "'hello'", result)
		assert.Equal(t, 6, end)
	})

	t.Run("double quoted", func(t *testing.T) {
		s := `"hello"`
		result, end := skipQuoted(s, 0)
		assert.Equal(t, `"hello"`, result)
		assert.Equal(t, 6, end)
	})

	t.Run("backtick quoted", func(t *testing.T) {
		s := "`hello`"
		result, end := skipQuoted(s, 0)
		assert.Equal(t, "`hello`", result)
		assert.Equal(t, 6, end)
	})

	t.Run("escaped quote", func(t *testing.T) {
		s := `'it\'s fine'`
		result, end := skipQuoted(s, 0)
		assert.Equal(t, `'it\'s fine'`, result)
		assert.Equal(t, 11, end)
	})

	t.Run("unterminated", func(t *testing.T) {
		s := "'unterminated"
		_, end := skipQuoted(s, 0)
		assert.Equal(t, -1, end)
	})

	t.Run("bytes version", func(t *testing.T) {
		b := []byte("'hello'")
		result, end := skipQuoted(b, 0)
		assert.Equal(t, []byte("'hello'"), result)
		assert.Equal(t, 6, end)
	})
}

// ---------------------------------------------------------------------------
// trimReturning
// ---------------------------------------------------------------------------

func TestTrimReturning(t *testing.T) {
	t.Run("no RETURNING", func(t *testing.T) {
		q := []byte("INSERT INTO t VALUES (1);")
		assert.Equal(t, "INSERT INTO t VALUES (1);", string(trimReturning(q)))
	})

	t.Run("with RETURNING", func(t *testing.T) {
		q := []byte("INSERT INTO t VALUES (1) RETURNING id;")
		assert.Equal(t, "INSERT INTO t VALUES (1);", string(trimReturning(q)))
	})

	t.Run("RETURNING without semicolon", func(t *testing.T) {
		// When there's no semicolon after RETURNING, the whole clause is consumed
		// but the remainder after RETURNING is not emitted (no semicolon to stop at).
		q := []byte("INSERT INTO t VALUES (1) RETURNING id")
		result := string(trimReturning(q))
		// The function only strips RETURNING when followed by ';'
		assert.Equal(t, "INSERT INTO t VALUES (1) RETURNING id", result)
	})

	t.Run("malformed quoted string", func(t *testing.T) {
		q := []byte("INSERT INTO t VALUES ('unterminated")
		// Should return original query
		assert.Equal(t, string(q), string(trimReturning(q)))
	})
}

// ---------------------------------------------------------------------------
// removeAttr
// ---------------------------------------------------------------------------

func TestRemoveAttr(t *testing.T) {
	attrs := []schema.Attr{
		&schema.Charset{V: "utf8"},
		&schema.Collation{V: "utf8_bin"},
	}
	result := removeAttr(attrs, reflect.TypeFor[*schema.Charset]())
	require.Len(t, result, 1)
	_, ok := result[0].(*schema.Collation)
	assert.True(t, ok)

	// Remove non-existent type
	result = removeAttr(attrs, reflect.TypeFor[*schema.Comment]())
	require.Len(t, result, 2)
}

// ---------------------------------------------------------------------------
// entDialect
// ---------------------------------------------------------------------------

func TestEntDialect(t *testing.T) {
	a := &Atlas{withForeignKeys: true}

	t.Run("unsupported", func(t *testing.T) {
		a.dialect = "unsupported"
		_, err := a.entDialect(context.Background(), nopDriver{dialect: "unsupported"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported dialect")
	})
}

// ---------------------------------------------------------------------------
// Atlas.symbol
// ---------------------------------------------------------------------------

func TestAtlas_Symbol(t *testing.T) {
	t.Run("short name", func(t *testing.T) {
		a := &Atlas{dialect: dialect.MySQL}
		assert.Equal(t, "short", a.symbol("short"))
	})

	t.Run("long name MySQL", func(t *testing.T) {
		a := &Atlas{dialect: dialect.MySQL}
		long := "a_very_long_symbol_name_that_exceeds_the_64_character_limit_for_mysql_identifiers"
		result := a.symbol(long)
		assert.LessOrEqual(t, len(result), 64)
		assert.Contains(t, result, "_")
	})

	t.Run("long name Postgres", func(t *testing.T) {
		a := &Atlas{dialect: dialect.Postgres}
		long := "a_very_long_symbol_name_that_exceeds_the_63_character_limit_for_postgres_ident"
		result := a.symbol(long)
		assert.LessOrEqual(t, len(result), 63)
	})
}

// ---------------------------------------------------------------------------
// CopyTables edge cases
// ---------------------------------------------------------------------------

func TestCopyTables_Errors(t *testing.T) {
	t.Run("missing index column", func(t *testing.T) {
		tbl := &Table{
			Name: "test",
			Columns: []*Column{
				{Name: "id", Type: field.TypeInt},
			},
			Indexes: []*Index{
				{Name: "idx", Columns: []*Column{{Name: "nonexistent"}}},
			},
		}
		_, err := CopyTables([]*Table{tbl})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing index column")
	})

	t.Run("missing FK ref-table", func(t *testing.T) {
		tbl := &Table{
			Name: "test",
			Columns: []*Column{
				{Name: "id", Type: field.TypeInt},
				{Name: "ref_id", Type: field.TypeInt},
			},
			ForeignKeys: []*ForeignKey{
				{
					Columns:    []*Column{{Name: "ref_id"}},
					RefTable:   &Table{Name: "nonexistent"},
					RefColumns: []*Column{{Name: "id"}},
				},
			},
		}
		_, err := CopyTables([]*Table{tbl})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing foreign-key ref-table")
	})

	t.Run("missing FK column", func(t *testing.T) {
		tbl := &Table{
			Name: "test",
			Columns: []*Column{
				{Name: "id", Type: field.TypeInt},
			},
			ForeignKeys: []*ForeignKey{
				{
					Columns:    []*Column{{Name: "nonexistent"}},
					RefTable:   &Table{Name: "test"},
					RefColumns: []*Column{{Name: "id"}},
				},
			},
		}
		_, err := CopyTables([]*Table{tbl})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing foreign-key column")
	})

	t.Run("missing FK ref-column", func(t *testing.T) {
		tbl := &Table{
			Name: "test",
			Columns: []*Column{
				{Name: "id", Type: field.TypeInt},
				{Name: "ref_id", Type: field.TypeInt},
			},
			ForeignKeys: []*ForeignKey{
				{
					Columns:    []*Column{{Name: "ref_id"}},
					RefTable:   &Table{Name: "test"},
					RefColumns: []*Column{{Name: "nonexistent"}},
				},
			},
		}
		_, err := CopyTables([]*Table{tbl})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing foreign-key ref-column")
	})
}

// ---------------------------------------------------------------------------
// DDL basic usage
// ---------------------------------------------------------------------------

func TestDDL_Basic(t *testing.T) {
	tables := []*Table{{
		Name: "test",
		Pos:  "test.go:1",
		Columns: []*Column{
			{Name: "id", Type: field.TypeInt},
		},
		PrimaryKey: []*Column{{Name: "id"}},
	}}

	result, err := DDL(context.Background(), DDLArgs{
		Dialect: dialect.SQLite,
		Tables:  tables,
	})
	require.NoError(t, err)
	assert.Contains(t, result, "CREATE TABLE")
}

// ---------------------------------------------------------------------------
// DDL unsupported dialect
// ---------------------------------------------------------------------------

func TestDDL_UnsupportedDialect(t *testing.T) {
	_, err := DDL(context.Background(), DDLArgs{
		Dialect: "unsupported",
		Tables:  []*Table{{Name: "test"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported dialect")
}

// ---------------------------------------------------------------------------
// Atlas.init edge cases
// ---------------------------------------------------------------------------

func TestAtlas_Init(t *testing.T) {
	t.Run("ModeReplay without dir", func(t *testing.T) {
		a := &Atlas{mode: ModeReplay, withForeignKeys: true}
		err := a.init()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "WithDir()")
	})

	t.Run("WithSkipChanges custom", func(t *testing.T) {
		a := &Atlas{withForeignKeys: true, skip: DropTable | DropColumn, mode: ModeInspect}
		err := a.init()
		require.NoError(t, err)
		assert.Len(t, a.diffHooks, 1) // filterChanges for custom skip
	})

	t.Run("dropColumns and dropIndexes", func(t *testing.T) {
		a := &Atlas{withForeignKeys: true, dropColumns: true, dropIndexes: true, mode: ModeInspect}
		err := a.init()
		require.NoError(t, err)
		// Default skip (DropIndex | DropColumn) gets cleared, so no filterChanges added
		assert.Empty(t, a.diffHooks)
	})
}

// ---------------------------------------------------------------------------
// atTypeRangeSQL
// ---------------------------------------------------------------------------

func TestAtTypeRangeSQL(t *testing.T) {
	t.Run("SQLite", func(t *testing.T) {
		d := &SQLite{}
		result := d.atTypeRangeSQL(TypeTable, "users", "posts")
		assert.Contains(t, result, "INSERT INTO `velox_types` (`type`) VALUES ('users'), ('posts')")
	})

	t.Run("MySQL", func(t *testing.T) {
		d := &MySQL{}
		result := d.atTypeRangeSQL(TypeTable, "users")
		assert.Contains(t, result, "INSERT INTO `velox_types` (`type`) VALUES ('users')")
	})

	t.Run("Postgres", func(t *testing.T) {
		d := &Postgres{}
		result := d.atTypeRangeSQL(TypeTable, "users")
		assert.Contains(t, result, `INSERT INTO "velox_types" ("type") VALUES ('users')`)
	})

	t.Run("EntCompat", func(t *testing.T) {
		d := &SQLite{}
		result := d.atTypeRangeSQL(EntTypeTable, "users")
		assert.Contains(t, result, "INSERT INTO `ent_types` (`type`) VALUES ('users')")
	})
}

// ---------------------------------------------------------------------------
// pkRange
// ---------------------------------------------------------------------------

func TestPkRange(t *testing.T) {
	t.Run("new type", func(t *testing.T) {
		a := &Atlas{types: []string{"users"}}
		r, err := a.pkRange(&Table{Name: "posts"})
		require.NoError(t, err)
		assert.Equal(t, int64(1<<32), r)
		assert.Contains(t, a.types, "posts")
	})

	t.Run("existing type", func(t *testing.T) {
		a := &Atlas{types: []string{"users", "posts"}}
		r, err := a.pkRange(&Table{Name: "posts"})
		require.NoError(t, err)
		assert.Equal(t, int64(1<<32), r)
	})

	t.Run("max types exceeded", func(t *testing.T) {
		types := make([]string, MaxTypes+1)
		a := &Atlas{types: types}
		_, err := a.pkRange(&Table{Name: "new_table"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "max number of types exceeded")
	})
}

// ---------------------------------------------------------------------------
// diffDriver.RealmDiff
// ---------------------------------------------------------------------------

func TestDiffDriver_RealmDiff(t *testing.T) {
	dd := &diffDriver{}
	_, err := dd.RealmDiff(&schema.Realm{}, &schema.Realm{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support working with realms")
}

// ---------------------------------------------------------------------------
// expandArgs with Postgres placeholders
// ---------------------------------------------------------------------------

func TestWriteDriver_ExpandArgs_Postgres(t *testing.T) {
	b := &bytes.Buffer{}
	w := NewWriteDriver(dialect.Postgres, b)

	result := w.expandArgs(`INSERT INTO t VALUES ($1, $2)`, []any{"hello", 42})
	assert.Equal(t, `INSERT INTO t VALUES ('hello', 42)`, result)
}

func TestWriteDriver_ExpandArgs_MySQL(t *testing.T) {
	b := &bytes.Buffer{}
	w := NewWriteDriver(dialect.MySQL, b)

	result := w.expandArgs("INSERT INTO t VALUES (?, ?)", []any{"hello", 42})
	assert.Equal(t, "INSERT INTO t VALUES ('hello', 42)", result)
}

func TestWriteDriver_ExpandArgs_QuotedString(t *testing.T) {
	b := &bytes.Buffer{}
	w := NewWriteDriver(dialect.MySQL, b)

	// Placeholder inside quoted string should be preserved
	result := w.expandArgs("INSERT INTO t VALUES ('literal?')", []any{})
	assert.Equal(t, "INSERT INTO t VALUES ('literal?')", result)
}

// ---------------------------------------------------------------------------
// nopDriver
// ---------------------------------------------------------------------------

func TestNopDriver(t *testing.T) {
	d := nopDriver{dialect: dialect.MySQL}
	assert.Equal(t, dialect.MySQL, d.Dialect())
	assert.NoError(t, d.Query(context.Background(), "", nil, nil))
}

// ---------------------------------------------------------------------------
// filterChanges - more change types
// ---------------------------------------------------------------------------

func TestFilterChanges_AllTypes(t *testing.T) {
	tbl := &schema.Table{Name: "test"}
	col := &schema.Column{Name: "col", Type: &schema.ColumnType{Type: &schema.StringType{T: "varchar(255)"}}}
	idx := &schema.Index{Name: "idx"}
	fk := &schema.ForeignKey{Symbol: "fk"}
	chk := &schema.Check{Name: "chk", Expr: "id > 0"}

	allChanges := []schema.Change{
		&schema.AddSchema{S: &schema.Schema{Name: "s"}},
		&schema.ModifySchema{S: &schema.Schema{Name: "s"}, Changes: []schema.Change{
			&schema.AddTable{T: tbl},
		}},
		&schema.DropSchema{S: &schema.Schema{Name: "s"}},
		&schema.AddTable{T: tbl},
		&schema.ModifyTable{T: tbl, Changes: []schema.Change{
			&schema.AddColumn{C: col},
			&schema.ModifyColumn{From: col, To: col},
			&schema.DropColumn{C: col},
			&schema.AddIndex{I: idx},
			&schema.ModifyIndex{From: idx, To: idx},
			&schema.DropIndex{I: idx},
			&schema.AddForeignKey{F: fk},
			&schema.ModifyForeignKey{From: fk, To: fk},
			&schema.DropForeignKey{F: fk},
			&schema.AddCheck{C: chk},
			&schema.ModifyCheck{From: chk, To: chk},
			&schema.DropCheck{C: chk},
		}},
		&schema.DropTable{T: tbl},
	}

	t.Run("skip nothing", func(t *testing.T) {
		mdiff := DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return allChanges, nil
		})
		filtered := filterChanges(NoChange)
		df, err := filtered(mdiff).Diff(nil, nil)
		require.NoError(t, err)
		assert.Len(t, df, len(allChanges))
	})

	t.Run("skip AddSchema", func(t *testing.T) {
		mdiff := DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return []schema.Change{&schema.AddSchema{S: &schema.Schema{Name: "s"}}}, nil
		})
		filtered := filterChanges(AddSchema)
		df, err := filtered(mdiff).Diff(nil, nil)
		require.NoError(t, err)
		assert.Empty(t, df)
	})

	t.Run("skip DropSchema", func(t *testing.T) {
		mdiff := DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return []schema.Change{&schema.DropSchema{S: &schema.Schema{Name: "s"}}}, nil
		})
		filtered := filterChanges(DropSchema)
		df, err := filtered(mdiff).Diff(nil, nil)
		require.NoError(t, err)
		assert.Empty(t, df)
	})

	t.Run("skip ModifySchema", func(t *testing.T) {
		mdiff := DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return []schema.Change{&schema.ModifySchema{S: &schema.Schema{Name: "s"}}}, nil
		})
		filtered := filterChanges(ModifySchema)
		df, err := filtered(mdiff).Diff(nil, nil)
		require.NoError(t, err)
		assert.Empty(t, df)
	})

	t.Run("skip ModifyColumn", func(t *testing.T) {
		mdiff := DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return []schema.Change{
				&schema.ModifyTable{T: tbl, Changes: []schema.Change{
					&schema.AddColumn{C: col},
					&schema.ModifyColumn{From: col, To: col},
				}},
			}, nil
		})
		filtered := filterChanges(ModifyColumn)
		df, err := filtered(mdiff).Diff(nil, nil)
		require.NoError(t, err)
		require.Len(t, df, 1)
		mt := df[0].(*schema.ModifyTable)
		assert.Len(t, mt.Changes, 1)
	})

	t.Run("skip checks", func(t *testing.T) {
		mdiff := DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return []schema.Change{
				&schema.ModifyTable{T: tbl, Changes: []schema.Change{
					&schema.AddCheck{C: chk},
					&schema.ModifyCheck{From: chk, To: chk},
					&schema.DropCheck{C: chk},
				}},
			}, nil
		})
		filtered := filterChanges(AddCheck | ModifyCheck | DropCheck)
		df, err := filtered(mdiff).Diff(nil, nil)
		require.NoError(t, err)
		require.Len(t, df, 1)
		mt := df[0].(*schema.ModifyTable)
		assert.Empty(t, mt.Changes)
	})

	t.Run("skip indexes", func(t *testing.T) {
		mdiff := DiffFunc(func(_, _ *schema.Schema) ([]schema.Change, error) {
			return []schema.Change{
				&schema.ModifyTable{T: tbl, Changes: []schema.Change{
					&schema.ModifyIndex{From: idx, To: idx},
				}},
			}, nil
		})
		filtered := filterChanges(ModifyIndex)
		df, err := filtered(mdiff).Diff(nil, nil)
		require.NoError(t, err)
		require.Len(t, df, 1)
		mt := df[0].(*schema.ModifyTable)
		assert.Empty(t, mt.Changes)
	})
}

// ---------------------------------------------------------------------------
// atUniqueC for all dialects
// ---------------------------------------------------------------------------

func TestSQLite_AtUniqueC(t *testing.T) {
	d := &SQLite{}

	t.Run("adds unique index", func(t *testing.T) {
		t1 := &Table{Name: "users"}
		c1 := &Column{Name: "email"}
		t2 := schema.NewTable("users")
		c2 := schema.NewColumn("email")
		t2.AddColumns(c2)
		d.atUniqueC(t1, c1, t2, c2)
		require.Len(t, t2.Indexes, 1)
		assert.Equal(t, "users_email_key", t2.Indexes[0].Name)
	})

	t.Run("skips when explicit index exists", func(t *testing.T) {
		t1 := &Table{
			Name: "users",
			Indexes: []*Index{
				{Name: "email", Unique: true},
			},
		}
		c1 := &Column{Name: "email"}
		t2 := schema.NewTable("users")
		c2 := schema.NewColumn("email")
		t2.AddColumns(c2)
		d.atUniqueC(t1, c1, t2, c2)
		assert.Empty(t, t2.Indexes) // should not add implicit index
	})
}

func TestMySQL_AtUniqueC(t *testing.T) {
	d := &MySQL{}

	t.Run("adds unique index", func(t *testing.T) {
		t1 := &Table{Name: "users"}
		c1 := &Column{Name: "email"}
		t2 := schema.NewTable("users")
		c2 := schema.NewColumn("email")
		t2.AddColumns(c2)
		d.atUniqueC(t1, c1, t2, c2)
		require.Len(t, t2.Indexes, 1)
		assert.Equal(t, "email", t2.Indexes[0].Name)
	})

	t.Run("skips when explicit index exists", func(t *testing.T) {
		t1 := &Table{
			Name: "users",
			Indexes: []*Index{
				{Name: "email", Unique: true},
			},
		}
		c1 := &Column{Name: "email"}
		t2 := schema.NewTable("users")
		c2 := schema.NewColumn("email")
		t2.AddColumns(c2)
		d.atUniqueC(t1, c1, t2, c2)
		assert.Empty(t, t2.Indexes)
	})
}

func TestPostgres_AtUniqueC(t *testing.T) {
	d := &Postgres{}

	t.Run("adds unique index", func(t *testing.T) {
		t1 := &Table{Name: "users"}
		c1 := &Column{Name: "email"}
		t2 := schema.NewTable("users")
		c2 := schema.NewColumn("email")
		t2.AddColumns(c2)
		d.atUniqueC(t1, c1, t2, c2)
		require.Len(t, t2.Indexes, 1)
		assert.Equal(t, "users_email_key", t2.Indexes[0].Name)
	})

	t.Run("skips when explicit index exists", func(t *testing.T) {
		t1 := &Table{
			Name: "users",
			Indexes: []*Index{
				{Name: "users_email_key", Unique: true},
			},
		}
		c1 := &Column{Name: "email"}
		t2 := schema.NewTable("users")
		c2 := schema.NewColumn("email")
		t2.AddColumns(c2)
		d.atUniqueC(t1, c1, t2, c2)
		assert.Empty(t, t2.Indexes)
	})
}

// ---------------------------------------------------------------------------
// atIncrementC / atIncrementT for all dialects
// ---------------------------------------------------------------------------

func TestSQLite_AtIncrementC(t *testing.T) {
	d := &SQLite{}

	t.Run("without default", func(t *testing.T) {
		at := schema.NewTable("test")
		c := schema.NewColumn("id")
		d.atIncrementC(at, c)
		// Should add AutoIncrement attr to column
		assert.NotEmpty(t, c.Attrs)
	})

	t.Run("with default", func(t *testing.T) {
		at := schema.NewTable("test")
		c := schema.NewColumn("id")
		c.SetDefault(&schema.RawExpr{X: "1"})
		d.atIncrementC(at, c)
		// Should NOT add AutoIncrement to column, instead remove from table
	})
}

func TestMySQL_AtIncrementC(t *testing.T) {
	d := &MySQL{}

	t.Run("without default", func(t *testing.T) {
		at := schema.NewTable("test")
		c := schema.NewColumn("id")
		d.atIncrementC(at, c)
		assert.NotEmpty(t, c.Attrs)
	})

	t.Run("with default", func(t *testing.T) {
		at := schema.NewTable("test")
		c := schema.NewColumn("id")
		c.SetDefault(&schema.RawExpr{X: "1"})
		d.atIncrementC(at, c)
	})
}

func TestPostgres_AtIncrementC(t *testing.T) {
	d := &Postgres{}

	t.Run("without default", func(t *testing.T) {
		at := schema.NewTable("test")
		c := schema.NewColumn("id")
		c.Type = &schema.ColumnType{Type: &schema.IntegerType{T: "bigint"}}
		d.atIncrementC(at, c)
		assert.NotEmpty(t, c.Attrs)
	})

	t.Run("with default", func(t *testing.T) {
		at := schema.NewTable("test")
		c := schema.NewColumn("id")
		c.Type = &schema.ColumnType{Type: &schema.IntegerType{T: "bigint"}}
		c.SetDefault(&schema.RawExpr{X: "1"})
		d.atIncrementC(at, c)
	})
}

func TestSQLite_AtIncrementT(t *testing.T) {
	d := &SQLite{}
	at := schema.NewTable("test")
	d.atIncrementT(at, 100)
	assert.NotEmpty(t, at.Attrs)
}

func TestMySQL_AtIncrementT(t *testing.T) {
	d := &MySQL{}

	t.Run("positive value", func(t *testing.T) {
		at := schema.NewTable("test")
		d.atIncrementT(at, 100)
		assert.NotEmpty(t, at.Attrs)
	})

	t.Run("negative value ignored", func(t *testing.T) {
		at := schema.NewTable("test")
		d.atIncrementT(at, -1)
		assert.Empty(t, at.Attrs)
	})
}

func TestPostgres_AtIncrementT(t *testing.T) {
	d := &Postgres{}
	at := schema.NewTable("test")
	d.atIncrementT(at, 100)
	assert.NotEmpty(t, at.Attrs)
}

// ---------------------------------------------------------------------------
// aColumns with column features (collation, comment, unique, increment)
// ---------------------------------------------------------------------------

func TestAtlas_AColumns(t *testing.T) {
	a := &Atlas{
		sqlDialect: &SQLite{Driver: nopDriver{dialect: dialect.SQLite}},
		dialect:    dialect.SQLite,
	}

	t.Run("basic columns", func(t *testing.T) {
		et := NewTable("users").
			AddPrimary(&Column{Name: "id", Type: field.TypeInt, Increment: true}).
			AddColumn(&Column{Name: "name", Type: field.TypeString, Collation: "utf8mb4_bin", Comment: "user name"}).
			AddColumn(&Column{Name: "email", Type: field.TypeString, Unique: true})

		at := schema.NewTable("users")
		err := a.aColumns(et, at)
		require.NoError(t, err)
		assert.Len(t, at.Columns, 3)
	})

	t.Run("unique column that is also PK", func(t *testing.T) {
		// Unique constraint should not be added for single-column PK
		pk := &Column{Name: "id", Type: field.TypeInt, Unique: true, Increment: true}
		et := NewTable("users").AddPrimary(pk)

		at := schema.NewTable("users")
		err := a.aColumns(et, at)
		require.NoError(t, err)
		// Should not have any unique indexes since it's the PK
		assert.Empty(t, at.Indexes)
	})
}

// ---------------------------------------------------------------------------
// aVColumns (view columns)
// ---------------------------------------------------------------------------

func TestAtlas_AVColumns(t *testing.T) {
	a := &Atlas{
		sqlDialect: &SQLite{Driver: nopDriver{dialect: dialect.SQLite}},
		dialect:    dialect.SQLite,
	}

	et := NewTable("my_view")
	et.View = true
	et.AddColumn(&Column{Name: "id", Type: field.TypeInt})
	et.AddColumn(&Column{Name: "name", Type: field.TypeString, Collation: "utf8", Comment: "name col"})

	av := schema.NewView("my_view", "SELECT id, name FROM users")
	err := a.aVColumns(et, av)
	require.NoError(t, err)
	assert.Len(t, av.Columns, 2)
}

// ---------------------------------------------------------------------------
// realm with views and comments
// ---------------------------------------------------------------------------

func TestAtlas_Realm_Views(t *testing.T) {
	a := &Atlas{
		sqlDialect: &SQLite{Driver: nopDriver{dialect: dialect.SQLite}},
		dialect:    dialect.SQLite,
	}

	usersTable := NewTable("users").
		AddPrimary(&Column{Name: "id", Type: field.TypeInt}).
		AddColumn(&Column{Name: "name", Type: field.TypeString})
	usersTable.Comment = "users table"

	view := NewView("active_users")
	view.AddColumn(&Column{Name: "id", Type: field.TypeInt})
	view.AddColumn(&Column{Name: "name", Type: field.TypeString})
	view.Annotation = &sqlschema.Annotation{ViewAs: "SELECT * FROM users WHERE active = 1"}
	view.Comment = "active users view"

	// External view (no ViewAs) should be skipped
	extView := NewView("ext_view")
	extView.AddColumn(&Column{Name: "id", Type: field.TypeInt})

	realm, err := a.realm([]*Table{usersTable, view, extView})
	require.NoError(t, err)
	require.NotNil(t, realm)
	require.Len(t, realm.Schemas, 1)
	assert.Len(t, realm.Schemas[0].Tables, 1)
	assert.Len(t, realm.Schemas[0].Views, 1)
}

func TestAtlas_Realm_ForeignKeys(t *testing.T) {
	a := &Atlas{
		sqlDialect: &SQLite{Driver: nopDriver{dialect: dialect.SQLite}},
		dialect:    dialect.SQLite,
	}

	usersTable := NewTable("users").
		AddPrimary(&Column{Name: "id", Type: field.TypeInt})

	postsTable := NewTable("posts").
		AddPrimary(&Column{Name: "id", Type: field.TypeInt}).
		AddColumn(&Column{Name: "user_id", Type: field.TypeInt})
	postsTable.AddForeignKey(&ForeignKey{
		Symbol:     "posts_user_id",
		Columns:    []*Column{postsTable.Columns[1]},
		RefTable:   usersTable,
		RefColumns: []*Column{usersTable.Columns[0]},
		OnDelete:   Cascade,
	})

	a.setupTables([]*Table{usersTable, postsTable})
	realm, err := a.realm([]*Table{usersTable, postsTable})
	require.NoError(t, err)
	require.NotNil(t, realm)
	assert.Len(t, realm.Schemas[0].Tables, 2)
}

func TestAtlas_Realm_IncrementStart(t *testing.T) {
	a := &Atlas{
		sqlDialect: &SQLite{Driver: nopDriver{dialect: dialect.SQLite}},
		dialect:    dialect.SQLite,
	}

	start := 1000
	tbl := NewTable("counters").
		AddPrimary(&Column{Name: "id", Type: field.TypeInt})
	tbl.Annotation = &sqlschema.Annotation{IncrementStart: &start}

	realm, err := a.realm([]*Table{tbl})
	require.NoError(t, err)
	require.NotNil(t, realm)
}

func TestAtlas_Realm_UniversalID_IncrementStart_Conflict(t *testing.T) {
	a := &Atlas{
		sqlDialect:  &SQLite{Driver: nopDriver{dialect: dialect.SQLite}},
		dialect:     dialect.SQLite,
		universalID: true,
		types:       []string{"counters"},
	}

	start := 1000
	tbl := NewTable("counters").
		AddPrimary(&Column{Name: "id", Type: field.TypeInt})
	tbl.Annotation = &sqlschema.Annotation{IncrementStart: &start}

	_, err := a.realm([]*Table{tbl})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

// ---------------------------------------------------------------------------
// WriteDriver.expandArgs edge cases
// ---------------------------------------------------------------------------

func TestWriteDriver_ExpandArgs_UnterminatedString(t *testing.T) {
	b := &bytes.Buffer{}
	w := NewWriteDriver(dialect.MySQL, b)

	// Unterminated string should return original query
	result := w.expandArgs("INSERT INTO t VALUES ('unterminated", []any{})
	assert.Equal(t, "INSERT INTO t VALUES ('unterminated", result)
}

func TestWriteDriver_ExpandArgs_InvalidPlaceholder(t *testing.T) {
	b := &bytes.Buffer{}
	w := NewWriteDriver(dialect.Postgres, b)

	// Invalid postgres placeholder (out of range)
	result := w.expandArgs("SELECT $99", []any{"hello"})
	assert.Equal(t, "SELECT $99", result)
}

// ---------------------------------------------------------------------------
// Postgres supportsDefault
// ---------------------------------------------------------------------------

func TestPostgres_SupportsDefault(t *testing.T) {
	d := &Postgres{}
	assert.True(t, d.supportsDefault(&Column{Type: field.TypeString}))
	assert.True(t, d.supportsDefault(&Column{Type: field.TypeInt64}))
}

// ---------------------------------------------------------------------------
// SQLite supportsDefault
// ---------------------------------------------------------------------------

func TestSQLite_SupportsDefault(t *testing.T) {
	d := &SQLite{}
	assert.True(t, d.supportsDefault(&Column{Type: field.TypeString}))
	assert.True(t, d.supportsDefault(&Column{Type: field.TypeInt64}))
}

// ---------------------------------------------------------------------------
// Stringer type in formatArg
// ---------------------------------------------------------------------------

type testStringer struct{ val string }

func (s testStringer) String() string { return s.val }

func TestWriteDriver_FormatArg_Stringer(t *testing.T) {
	w := &WriteDriver{Driver: nopDriver{dialect: dialect.MySQL}}
	result, err := w.formatArg(testStringer{val: "hello"})
	require.NoError(t, err)
	assert.Equal(t, "'hello'", result)
}

// ---------------------------------------------------------------------------
// WriteDriver.Query INSERT RETURNING with Rows
// ---------------------------------------------------------------------------

func TestWriteDriver_Query_InsertReturningRows(t *testing.T) {
	b := &bytes.Buffer{}
	w := NewWriteDriver(dialect.Postgres, b)
	ctx := context.Background()

	var rows sql.Rows
	err := w.Query(ctx, `INSERT INTO "users" ("name") VALUES ($1) RETURNING "id", "name"`, []any{"test"}, &rows)
	require.NoError(t, err)
	require.True(t, rows.Next())
	cols, err := rows.Columns()
	require.NoError(t, err)
	assert.Equal(t, []string{`"id"`, `"name"`}, cols)
}
