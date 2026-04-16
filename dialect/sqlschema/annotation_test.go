package sqlschema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
)

// ---------------------------------------------------------------------------
// ValidateColumnType (existing tests, preserved)
// ---------------------------------------------------------------------------

func TestValidateColumnType(t *testing.T) {
	valid := []string{
		"JSONB",
		"VARCHAR(255)",
		"TEXT",
		"DECIMAL(10,2)",
		"CHAR(10)",
		"BIGINT UNSIGNED",
		"TIMESTAMP WITH TIME ZONE",
	}
	for _, typ := range valid {
		if err := ValidateColumnType(typ); err != nil {
			t.Errorf("ValidateColumnType(%q) = %v, want nil", typ, err)
		}
	}

	invalid := []string{
		"TEXT; DROP TABLE users;",
		"TEXT; DELETE FROM users",
		"INT -- comment injection",
		"TEXT DROP TABLE",
		"TEXT INSERT INTO",
		"VARCHAR ALTER TABLE",
	}
	for _, typ := range invalid {
		if err := ValidateColumnType(typ); err == nil {
			t.Errorf("ValidateColumnType(%q) = nil, want error", typ)
		}
	}
}

func TestColumnTypePanicsOnInvalid(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("ColumnType with semicolon should panic")
		}
	}()
	ColumnType("TEXT; DROP TABLE users;")
}

// ---------------------------------------------------------------------------
// CascadeAction.ConstName
// ---------------------------------------------------------------------------

func TestCascadeAction_ConstName(t *testing.T) {
	tests := []struct {
		action CascadeAction
		want   string
	}{
		{Cascade, "Cascade"},
		{SetNull, "SetNull"},
		{Restrict, "Restrict"},
		{SetDefault, "SetDefault"},
		{NoAction, "NoAction"},
		{CascadeAction("CUSTOM"), "CUSTOM"}, // unknown falls through to string
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.action.ConstName())
		})
	}
}

// ---------------------------------------------------------------------------
// Constructor functions
// ---------------------------------------------------------------------------

func TestConstructor_Table(t *testing.T) {
	a := Table("users")
	assert.Equal(t, "users", a.Table)
	assert.Equal(t, AnnotationName, a.Name())
}

func TestConstructor_Size(t *testing.T) {
	a := Size(255)
	assert.Equal(t, int64(255), a.Size)
}

func TestConstructor_OnDelete(t *testing.T) {
	a := OnDelete(Cascade)
	assert.Equal(t, Cascade, a.OnDelete)
}

func TestConstructor_OnUpdate(t *testing.T) {
	a := OnUpdate(SetNull)
	assert.Equal(t, SetNull, a.OnUpdate)
}

func TestConstructor_WithComments(t *testing.T) {
	a := WithComments(true)
	require.NotNil(t, a.WithComments)
	assert.True(t, *a.WithComments)

	b := WithComments(false)
	require.NotNil(t, b.WithComments)
	assert.False(t, *b.WithComments)
}

func TestConstructor_ColumnType(t *testing.T) {
	a := ColumnType("JSONB")
	assert.Equal(t, "JSONB", a.ColumnType)
}

func TestConstructor_Collation(t *testing.T) {
	a := Collation("utf8mb4_unicode_ci")
	assert.Equal(t, "utf8mb4_unicode_ci", a.Collation)
}

func TestConstructor_Check(t *testing.T) {
	a := Check("age >= 0")
	assert.Equal(t, "age >= 0", a.Check)
}

func TestConstructor_Default(t *testing.T) {
	a := Default("CURRENT_TIMESTAMP")
	assert.Equal(t, "CURRENT_TIMESTAMP", a.Default)
}

func TestConstructor_DefaultExpr(t *testing.T) {
	a := DefaultExpr("gen_random_uuid()")
	assert.Equal(t, "gen_random_uuid()", a.DefaultExpr)
}

func TestConstructor_Charset(t *testing.T) {
	a := Charset("utf8mb4")
	assert.Equal(t, "utf8mb4", a.Charset)
}

func TestConstructor_Schema(t *testing.T) {
	a := Schema("public")
	assert.Equal(t, "public", a.Schema)
}

func TestConstructor_Skip(t *testing.T) {
	a := Skip()
	assert.True(t, a.Skip)
}

func TestConstructor_IndexType(t *testing.T) {
	a := IndexType("GIN")
	assert.Equal(t, "GIN", a.IndexType)
}

func TestConstructor_StorageParams(t *testing.T) {
	a := StorageParams("fillfactor=90")
	assert.Equal(t, "fillfactor=90", a.StorageParams)
}

func TestConstructor_Desc(t *testing.T) {
	ia := Desc()
	require.NotNil(t, ia)
	assert.True(t, ia.Desc)
	assert.Equal(t, AnnotationName, ia.Name())
}

func TestConstructor_View(t *testing.T) {
	a := View("SELECT name FROM pets")
	require.NotNil(t, a)
	assert.Equal(t, "SELECT name FROM pets", a.ViewAs)
}

func TestConstructor_ViewFor(t *testing.T) {
	a := ViewFor("postgres", func(s *sql.Selector) {
		s.Select("name").From(sql.Table("pets"))
	})
	require.NotNil(t, a)
	require.Contains(t, a.ViewFor, "postgres")
	assert.Contains(t, a.ViewFor["postgres"], "name")
	assert.Contains(t, a.ViewFor["postgres"], "pets")
}

// ---------------------------------------------------------------------------
// Annotation.Name (interface compliance)
// ---------------------------------------------------------------------------

func TestAnnotation_Name(t *testing.T) {
	a := Annotation{}
	assert.Equal(t, "sql", a.Name())
}

func TestIndexAnnotation_Name(t *testing.T) {
	ia := IndexAnnotation{}
	assert.Equal(t, "sql", ia.Name())
}

// ---------------------------------------------------------------------------
// Getter methods
// ---------------------------------------------------------------------------

func TestGetTable(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		v, ok := Table("users").GetTable()
		assert.True(t, ok)
		assert.Equal(t, "users", v)
	})
	t.Run("unset", func(t *testing.T) {
		v, ok := Annotation{}.GetTable()
		assert.False(t, ok)
		assert.Equal(t, "", v)
	})
}

func TestGetSize(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		v, ok := Size(100).GetSize()
		assert.True(t, ok)
		assert.Equal(t, int64(100), v)
	})
	t.Run("unset", func(t *testing.T) {
		v, ok := Annotation{}.GetSize()
		assert.False(t, ok)
		assert.Equal(t, int64(0), v)
	})
}

func TestGetOnDelete(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		v, ok := OnDelete(Cascade).GetOnDelete()
		assert.True(t, ok)
		assert.Equal(t, Cascade, v)
	})
	t.Run("unset", func(t *testing.T) {
		v, ok := Annotation{}.GetOnDelete()
		assert.False(t, ok)
		assert.Equal(t, CascadeAction(""), v)
	})
}

func TestGetOnUpdate(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		v, ok := OnUpdate(Restrict).GetOnUpdate()
		assert.True(t, ok)
		assert.Equal(t, Restrict, v)
	})
	t.Run("unset", func(t *testing.T) {
		v, ok := Annotation{}.GetOnUpdate()
		assert.False(t, ok)
		assert.Equal(t, CascadeAction(""), v)
	})
}

func TestGetWithComments(t *testing.T) {
	t.Run("set_true", func(t *testing.T) {
		v, ok := WithComments(true).GetWithComments()
		assert.True(t, ok)
		assert.True(t, v)
	})
	t.Run("set_false", func(t *testing.T) {
		v, ok := WithComments(false).GetWithComments()
		assert.True(t, ok)
		assert.False(t, v)
	})
	t.Run("unset_defaults_to_true", func(t *testing.T) {
		v, ok := Annotation{}.GetWithComments()
		assert.False(t, ok)
		assert.True(t, v) // default is true
	})
}

func TestGetColumnType(t *testing.T) {
	assert.Equal(t, "JSONB", ColumnType("JSONB").GetColumnType())
	assert.Equal(t, "", Annotation{}.GetColumnType())
}

func TestGetCollation(t *testing.T) {
	assert.Equal(t, "utf8mb4_unicode_ci", Collation("utf8mb4_unicode_ci").GetCollation())
	assert.Equal(t, "", Annotation{}.GetCollation())
}

func TestGetCheck(t *testing.T) {
	assert.Equal(t, "age >= 0", Check("age >= 0").GetCheck())
	assert.Equal(t, "", Annotation{}.GetCheck())
}

func TestGetDefault(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		v, ok := Default("CURRENT_TIMESTAMP").GetDefault()
		assert.True(t, ok)
		assert.Equal(t, "CURRENT_TIMESTAMP", v)
	})
	t.Run("unset", func(t *testing.T) {
		v, ok := Annotation{}.GetDefault()
		assert.False(t, ok)
		assert.Equal(t, "", v)
	})
}

func TestGetDefaultExpr(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		v, ok := DefaultExpr("gen_random_uuid()").GetDefaultExpr()
		assert.True(t, ok)
		assert.Equal(t, "gen_random_uuid()", v)
	})
	t.Run("unset", func(t *testing.T) {
		v, ok := Annotation{}.GetDefaultExpr()
		assert.False(t, ok)
		assert.Equal(t, "", v)
	})
}

func TestGetCharset(t *testing.T) {
	assert.Equal(t, "utf8mb4", Charset("utf8mb4").GetCharset())
	assert.Equal(t, "", Annotation{}.GetCharset())
}

func TestGetIncremental(t *testing.T) {
	t.Run("set_true", func(t *testing.T) {
		b := true
		v, ok := Annotation{Incremental: &b}.GetIncremental()
		assert.True(t, ok)
		assert.True(t, v)
	})
	t.Run("set_false", func(t *testing.T) {
		b := false
		v, ok := Annotation{Incremental: &b}.GetIncremental()
		assert.True(t, ok)
		assert.False(t, v)
	})
	t.Run("unset", func(t *testing.T) {
		v, ok := Annotation{}.GetIncremental()
		assert.False(t, ok)
		assert.False(t, v)
	})
}

func TestGetIndexType(t *testing.T) {
	assert.Equal(t, "GIN", IndexType("GIN").GetIndexType())
	assert.Equal(t, "", Annotation{}.GetIndexType())
}

func TestGetStorageParams(t *testing.T) {
	assert.Equal(t, "fillfactor=90", StorageParams("fillfactor=90").GetStorageParams())
	assert.Equal(t, "", Annotation{}.GetStorageParams())
}

// ---------------------------------------------------------------------------
// Merge
// ---------------------------------------------------------------------------

func TestMerge_OverridesFields(t *testing.T) {
	a1 := Annotation{Table: "t1", Size: 10, OnDelete: Cascade, ColumnType: "TEXT"}
	a2 := Annotation{Table: "t2", Size: 20, OnUpdate: SetNull, Collation: "utf8"}

	merged := Merge(a1, a2)

	// a2 overrides Table and Size
	assert.Equal(t, "t2", merged.Table)
	assert.Equal(t, int64(20), merged.Size)
	// a1 fields that a2 didn't set are preserved
	assert.Equal(t, Cascade, merged.OnDelete)
	assert.Equal(t, "TEXT", merged.ColumnType)
	// a2 new fields
	assert.Equal(t, SetNull, merged.OnUpdate)
	assert.Equal(t, "utf8", merged.Collation)
}

func TestMerge_ZeroValuesDontOverride(t *testing.T) {
	a1 := Annotation{
		Table:         "users",
		Size:          100,
		OnDelete:      Cascade,
		ColumnType:    "JSONB",
		Check:         "x > 0",
		Default:       "hello",
		DefaultExpr:   "now()",
		Charset:       "utf8",
		IndexType:     "BTREE",
		StorageParams: "fillfactor=90",
	}
	a2 := Annotation{} // all zero values

	merged := Merge(a1, a2)

	assert.Equal(t, "users", merged.Table)
	assert.Equal(t, int64(100), merged.Size)
	assert.Equal(t, Cascade, merged.OnDelete)
	assert.Equal(t, "JSONB", merged.ColumnType)
	assert.Equal(t, "x > 0", merged.Check)
	assert.Equal(t, "hello", merged.Default)
	assert.Equal(t, "now()", merged.DefaultExpr)
	assert.Equal(t, "utf8", merged.Charset)
	assert.Equal(t, "BTREE", merged.IndexType)
	assert.Equal(t, "fillfactor=90", merged.StorageParams)
}

func TestMerge_EmptyAnnotations(t *testing.T) {
	merged := Merge()
	assert.Equal(t, Annotation{}, merged)
}

func TestMerge_SingleAnnotation(t *testing.T) {
	a := Annotation{Table: "users", Size: 50}
	merged := Merge(a)
	assert.Equal(t, a.Table, merged.Table)
	assert.Equal(t, a.Size, merged.Size)
}

func TestMerge_PointerFields(t *testing.T) {
	trueVal := true
	falseVal := false

	t.Run("WithComments_override", func(t *testing.T) {
		a1 := Annotation{WithComments: &trueVal}
		a2 := Annotation{WithComments: &falseVal}
		merged := Merge(a1, a2)
		require.NotNil(t, merged.WithComments)
		assert.False(t, *merged.WithComments)
	})

	t.Run("WithComments_nil_doesnt_override", func(t *testing.T) {
		a1 := Annotation{WithComments: &trueVal}
		a2 := Annotation{} // WithComments is nil
		merged := Merge(a1, a2)
		require.NotNil(t, merged.WithComments)
		assert.True(t, *merged.WithComments)
	})

	t.Run("Incremental_override", func(t *testing.T) {
		a1 := Annotation{Incremental: &trueVal}
		a2 := Annotation{Incremental: &falseVal}
		merged := Merge(a1, a2)
		require.NotNil(t, merged.Incremental)
		assert.False(t, *merged.Incremental)
	})

	t.Run("Incremental_nil_doesnt_override", func(t *testing.T) {
		a1 := Annotation{Incremental: &falseVal}
		a2 := Annotation{} // Incremental is nil
		merged := Merge(a1, a2)
		require.NotNil(t, merged.Incremental)
		assert.False(t, *merged.Incremental)
	})
}

func TestMerge_SkipField(t *testing.T) {
	a1 := Annotation{Skip: false}
	a2 := Annotation{Skip: true}
	merged := Merge(a1, a2)
	assert.True(t, merged.Skip)
}

func TestMerge_SchemaField(t *testing.T) {
	a1 := Annotation{Schema: "public"}
	a2 := Annotation{Schema: "private"}
	merged := Merge(a1, a2)
	assert.Equal(t, "private", merged.Schema)
}

func TestMerge_ThreeAnnotations(t *testing.T) {
	a1 := Annotation{Table: "t1", Check: "a > 0"}
	a2 := Annotation{Table: "t2", Charset: "utf8"}
	a3 := Annotation{Table: "t3", Default: "x"}

	merged := Merge(a1, a2, a3)

	assert.Equal(t, "t3", merged.Table)     // last wins
	assert.Equal(t, "a > 0", merged.Check)  // from a1
	assert.Equal(t, "utf8", merged.Charset) // from a2
	assert.Equal(t, "x", merged.Default)    // from a3
}

// ---------------------------------------------------------------------------
// ValidateColumnType — additional edge cases
// ---------------------------------------------------------------------------

func TestValidateColumnType_UpdateKeyword(t *testing.T) {
	err := ValidateColumnType("INT UPDATE SET")
	assert.Error(t, err)
}

func TestValidateColumnType_ExecKeyword(t *testing.T) {
	assert.Error(t, ValidateColumnType("EXEC sp_help"))
	assert.Error(t, ValidateColumnType("EXECUTE sp_help"))
}

func TestValidateColumnType_TruncateKeyword(t *testing.T) {
	assert.Error(t, ValidateColumnType("TRUNCATE TABLE users"))
	assert.Error(t, ValidateExpression("TRUNCATE TABLE users"))
}

func TestValidateColumnType_EmptyString(t *testing.T) {
	// Empty string is valid (no dangerous content)
	assert.NoError(t, ValidateColumnType(""))
}

// ---------------------------------------------------------------------------
// ColumnType panics on various injection patterns
// ---------------------------------------------------------------------------

func TestColumnType_PanicsOnDDLKeyword(t *testing.T) {
	dangerous := []string{
		"TEXT DROP users",
		"INT; SELECT 1",
		"VARCHAR DELETE FROM x",
		"JSONB SELECT * FROM users",
	}
	for _, typ := range dangerous {
		t.Run(typ, func(t *testing.T) {
			assert.Panics(t, func() { ColumnType(typ) })
		})
	}
}

// ---------------------------------------------------------------------------
// Interface compliance
// ---------------------------------------------------------------------------

func TestAnnotation_ImplementsSchemaAnnotation(t *testing.T) {
	// Compile-time check is via var _ in the source, but let's also
	// verify at runtime via the Name() method.
	var a interface{ Name() string } = Annotation{}
	assert.Equal(t, AnnotationName, a.Name())
}

func TestIndexAnnotation_ImplementsSchemaAnnotation(t *testing.T) {
	var a interface{ Name() string } = IndexAnnotation{}
	assert.Equal(t, AnnotationName, a.Name())
}

// ---------------------------------------------------------------------------
// Struct literal construction (Ent-compatible style)
// ---------------------------------------------------------------------------

func TestAnnotation_StructLiteral(t *testing.T) {
	inc := true
	a := Annotation{
		Table:         "orders",
		Schema:        "shop",
		Skip:          false,
		Size:          64,
		ColumnType:    "VARCHAR(64)",
		Collation:     "utf8_general_ci",
		Check:         "amount > 0",
		OnDelete:      Cascade,
		OnUpdate:      NoAction,
		Default:       "'pending'",
		DefaultExpr:   "now()",
		Charset:       "utf8mb4",
		Incremental:   &inc,
		IndexType:     "BTREE",
		StorageParams: "fillfactor=80",
	}

	assert.Equal(t, "sql", a.Name())

	tbl, ok := a.GetTable()
	assert.True(t, ok)
	assert.Equal(t, "orders", tbl)

	sz, ok := a.GetSize()
	assert.True(t, ok)
	assert.Equal(t, int64(64), sz)

	od, ok := a.GetOnDelete()
	assert.True(t, ok)
	assert.Equal(t, Cascade, od)

	ou, ok := a.GetOnUpdate()
	assert.True(t, ok)
	assert.Equal(t, NoAction, ou)

	assert.Equal(t, "VARCHAR(64)", a.GetColumnType())
	assert.Equal(t, "utf8_general_ci", a.GetCollation())
	assert.Equal(t, "amount > 0", a.GetCheck())
	assert.Equal(t, "utf8mb4", a.GetCharset())
	assert.Equal(t, "BTREE", a.GetIndexType())
	assert.Equal(t, "fillfactor=80", a.GetStorageParams())

	def, ok := a.GetDefault()
	assert.True(t, ok)
	assert.Equal(t, "'pending'", def)

	dexpr, ok := a.GetDefaultExpr()
	assert.True(t, ok)
	assert.Equal(t, "now()", dexpr)

	incVal, ok := a.GetIncremental()
	assert.True(t, ok)
	assert.True(t, incVal)

	wc, ok := a.GetWithComments()
	assert.False(t, ok) // not set
	assert.True(t, wc)  // default true
}

// ---------------------------------------------------------------------------
// IndexAnnotation struct fields
// ---------------------------------------------------------------------------

func TestIndexAnnotation_StructLiteral(t *testing.T) {
	ia := IndexAnnotation{
		Type:           "GIN",
		Types:          map[string]string{"postgres": "GIN", "mysql": "BTREE"},
		Where:          "status = 'active'",
		Desc:           true,
		DescColumns:    map[string]bool{"created_at": true},
		OpClass:        "jsonb_ops",
		OpClassColumns: map[string]string{"tags": "jsonb_path_ops"},
		Prefix:         10,
		PrefixColumns:  map[string]uint{"name": 5},
		IncludeColumns: []string{"id", "name"},
	}

	assert.Equal(t, "sql", ia.Name())
	assert.Equal(t, "GIN", ia.Type)
	assert.True(t, ia.Desc)
	assert.Equal(t, "status = 'active'", ia.Where)
	assert.Equal(t, "jsonb_ops", ia.OpClass)
	assert.Equal(t, uint(10), ia.Prefix)
	assert.Equal(t, []string{"id", "name"}, ia.IncludeColumns)
	assert.Equal(t, "GIN", ia.Types["postgres"])
	assert.True(t, ia.DescColumns["created_at"])
	assert.Equal(t, "jsonb_path_ops", ia.OpClassColumns["tags"])
	assert.Equal(t, uint(5), ia.PrefixColumns["name"])
}

// ---------------------------------------------------------------------------
// View and ViewFor
// ---------------------------------------------------------------------------

func TestView_ReturnsPointer(t *testing.T) {
	a := View("SELECT 1")
	require.NotNil(t, a)
	assert.Equal(t, "SELECT 1", a.ViewAs)
	assert.Equal(t, "sql", a.Name())
}

func TestViewFor_GeneratesQuery(t *testing.T) {
	a := ViewFor("postgres", func(s *sql.Selector) {
		s.Select("id", "name").From(sql.Table("users"))
	})
	require.NotNil(t, a)
	require.Contains(t, a.ViewFor, "postgres")
	query := a.ViewFor["postgres"]
	assert.Contains(t, query, "id")
	assert.Contains(t, query, "name")
	assert.Contains(t, query, "users")
}

// ---------------------------------------------------------------------------
// CascadeAction string values
// ---------------------------------------------------------------------------

func TestCascadeAction_StringValues(t *testing.T) {
	assert.Equal(t, CascadeAction("CASCADE"), Cascade)
	assert.Equal(t, CascadeAction("SET NULL"), SetNull)
	assert.Equal(t, CascadeAction("RESTRICT"), Restrict)
	assert.Equal(t, CascadeAction("SET DEFAULT"), SetDefault)
	assert.Equal(t, CascadeAction("NO ACTION"), NoAction)
}

// ---------------------------------------------------------------------------
// AnnotationName constant
// ---------------------------------------------------------------------------

func TestAnnotationName(t *testing.T) {
	assert.Equal(t, "sql", AnnotationName)
}

// ---------------------------------------------------------------------------
// ValidateExpression
// ---------------------------------------------------------------------------

func TestValidateExpression(t *testing.T) {
	valid := []string{
		"gen_random_uuid()",
		"age >= 0 AND status != 'deleted'",
		"now()",
		"lower(title)",
		"CURRENT_TIMESTAMP",
	}
	for _, expr := range valid {
		if err := ValidateExpression(expr); err != nil {
			t.Errorf("ValidateExpression(%q) = %v, want nil", expr, err)
		}
	}

	invalid := []string{
		"x; DROP TABLE users",
		"1; DELETE FROM users",
		"x -- comment injection",
		"x DROP TABLE y",
		"x INSERT INTO y",
		"x ALTER TABLE y",
	}
	for _, expr := range invalid {
		if err := ValidateExpression(expr); err == nil {
			t.Errorf("ValidateExpression(%q) = nil, want error", expr)
		}
	}
}

// ---------------------------------------------------------------------------
// Check, Default, and DefaultExpr injection panics
// ---------------------------------------------------------------------------

func TestCheck_PanicsOnInjection(t *testing.T) {
	assert.Panics(t, func() { Check("x; DROP TABLE users") })
}

func TestDefault_PanicsOnInjection(t *testing.T) {
	assert.Panics(t, func() { Default("value; DROP TABLE users") })
}

func TestDefaultExpr_PanicsOnInjection(t *testing.T) {
	assert.Panics(t, func() { DefaultExpr("x; DROP TABLE users") })
}

func TestCheck_ValidExpression(t *testing.T) {
	assert.NotPanics(t, func() { Check("age >= 0 AND status != 'deleted'") })
}

func TestDefault_ValidLiteral(t *testing.T) {
	assert.NotPanics(t, func() { Default("'pending'") })
}

func TestDefaultExpr_ValidExpression(t *testing.T) {
	assert.NotPanics(t, func() { DefaultExpr("gen_random_uuid()") })
}
