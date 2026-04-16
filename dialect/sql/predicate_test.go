package sql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testPred is a predicate type for testing.
type testPred = func(*Selector)

// buildSQL executes a predicate against a test selector and returns the SQL.
func buildSQL(t *testing.T, pred func(*Selector)) string {
	t.Helper()
	s := Select("*").From(Table("users"))
	pred(s)
	query, _ := s.Query()
	return query
}

// --- StringField ---

func TestStringField_EQ(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.EQ("alice"))
	assert.Contains(t, sql, "`name` = ")
}

func TestStringField_NEQ(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.NEQ("alice"))
	assert.Contains(t, sql, "`name` <>")
}

func TestStringField_In(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.In("a", "b", "c"))
	assert.Contains(t, sql, "`name` IN")
}

func TestStringField_NotIn(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.NotIn("a", "b"))
	assert.Contains(t, sql, "`name` NOT IN")
}

func TestStringField_GT(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.GT("m"))
	assert.Contains(t, sql, "`name` >")
}

func TestStringField_GTE(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.GTE("m"))
	assert.Contains(t, sql, "`name` >=")
}

func TestStringField_LT(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.LT("m"))
	assert.Contains(t, sql, "`name` <")
}

func TestStringField_LTE(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.LTE("m"))
	assert.Contains(t, sql, "`name` <=")
}

func TestStringField_Contains(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.Contains("ali"))
	assert.Contains(t, sql, "LIKE")
}

func TestStringField_ContainsFold(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.ContainsFold("ali"))
	assert.Contains(t, sql, "LIKE")
}

func TestStringField_HasPrefix(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.HasPrefix("al"))
	assert.Contains(t, sql, "LIKE")
}

func TestStringField_HasSuffix(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.HasSuffix("ce"))
	assert.Contains(t, sql, "LIKE")
}

func TestStringField_EqualFold(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.EqualFold("ALICE"))
	assert.Contains(t, sql, "name")
}

func TestStringField_IsNull(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.IsNull())
	assert.Contains(t, sql, "IS NULL")
}

func TestStringField_NotNull(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.NotNull())
	assert.Contains(t, sql, "IS NOT NULL")
}

func TestStringField_IsNil(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.IsNil())
	assert.Contains(t, sql, "IS NULL")
}

func TestStringField_NotNil(t *testing.T) {
	f := StringField[testPred]("name")
	sql := buildSQL(t, f.NotNil())
	assert.Contains(t, sql, "IS NOT NULL")
}

func TestStringField_Name(t *testing.T) {
	f := StringField[testPred]("email")
	assert.Equal(t, "email", f.Name())
}

// --- IntField ---

func TestIntField_EQ(t *testing.T) {
	f := IntField[testPred]("age")
	sql := buildSQL(t, f.EQ(25))
	assert.Contains(t, sql, "`age` =")
}

func TestIntField_NEQ(t *testing.T) {
	f := IntField[testPred]("age")
	sql := buildSQL(t, f.NEQ(25))
	assert.Contains(t, sql, "`age` <>")
}

func TestIntField_In(t *testing.T) {
	f := IntField[testPred]("age")
	sql := buildSQL(t, f.In(18, 25, 30))
	assert.Contains(t, sql, "`age` IN")
}

func TestIntField_NotIn(t *testing.T) {
	f := IntField[testPred]("age")
	sql := buildSQL(t, f.NotIn(18, 25))
	assert.Contains(t, sql, "`age` NOT IN")
}

func TestIntField_GT(t *testing.T) {
	f := IntField[testPred]("age")
	sql := buildSQL(t, f.GT(18))
	assert.Contains(t, sql, "`age` >")
}

func TestIntField_GTE(t *testing.T) {
	f := IntField[testPred]("age")
	sql := buildSQL(t, f.GTE(18))
	assert.Contains(t, sql, "`age` >=")
}

func TestIntField_LT(t *testing.T) {
	f := IntField[testPred]("age")
	sql := buildSQL(t, f.LT(65))
	assert.Contains(t, sql, "`age` <")
}

func TestIntField_LTE(t *testing.T) {
	f := IntField[testPred]("age")
	sql := buildSQL(t, f.LTE(65))
	assert.Contains(t, sql, "`age` <=")
}

func TestIntField_IsNull(t *testing.T) {
	f := IntField[testPred]("age")
	sql := buildSQL(t, f.IsNull())
	assert.Contains(t, sql, "IS NULL")
}

func TestIntField_NotNull(t *testing.T) {
	f := IntField[testPred]("age")
	sql := buildSQL(t, f.NotNull())
	assert.Contains(t, sql, "IS NOT NULL")
}

func TestIntField_Name(t *testing.T) {
	f := IntField[testPred]("age")
	assert.Equal(t, "age", f.Name())
}

// --- Int64Field ---

func TestInt64Field_EQ(t *testing.T) {
	f := Int64Field[testPred]("count")
	sql := buildSQL(t, f.EQ(int64(100)))
	assert.Contains(t, sql, "`count` =")
}

func TestInt64Field_In(t *testing.T) {
	f := Int64Field[testPred]("count")
	sql := buildSQL(t, f.In(int64(1), int64(2), int64(3)))
	assert.Contains(t, sql, "`count` IN")
}

func TestInt64Field_GT(t *testing.T) {
	f := Int64Field[testPred]("count")
	sql := buildSQL(t, f.GT(int64(0)))
	assert.Contains(t, sql, "`count` >")
}

func TestInt64Field_IsNull(t *testing.T) {
	f := Int64Field[testPred]("count")
	sql := buildSQL(t, f.IsNull())
	assert.Contains(t, sql, "IS NULL")
}

func TestInt64Field_Name(t *testing.T) {
	f := Int64Field[testPred]("count")
	assert.Equal(t, "count", f.Name())
}

// --- Float64Field ---

func TestFloat64Field_EQ(t *testing.T) {
	f := Float64Field[testPred]("price")
	sql := buildSQL(t, f.EQ(9.99))
	assert.Contains(t, sql, "`price` =")
}

func TestFloat64Field_GT(t *testing.T) {
	f := Float64Field[testPred]("price")
	sql := buildSQL(t, f.GT(0.0))
	assert.Contains(t, sql, "`price` >")
}

func TestFloat64Field_In(t *testing.T) {
	f := Float64Field[testPred]("price")
	sql := buildSQL(t, f.In(1.0, 2.0))
	assert.Contains(t, sql, "`price` IN")
}

func TestFloat64Field_IsNull(t *testing.T) {
	f := Float64Field[testPred]("price")
	sql := buildSQL(t, f.IsNull())
	assert.Contains(t, sql, "IS NULL")
}

func TestFloat64Field_Name(t *testing.T) {
	f := Float64Field[testPred]("price")
	assert.Equal(t, "price", f.Name())
}

// --- BoolField ---

func TestBoolField_EQ(t *testing.T) {
	f := BoolField[testPred]("active")
	sql := buildSQL(t, f.EQ(true))
	assert.Contains(t, sql, "`active`")
}

func TestBoolField_NEQ(t *testing.T) {
	f := BoolField[testPred]("active")
	sql := buildSQL(t, f.NEQ(true))
	assert.Contains(t, sql, "`active`")
}

func TestBoolField_IsNull(t *testing.T) {
	f := BoolField[testPred]("active")
	sql := buildSQL(t, f.IsNull())
	assert.Contains(t, sql, "IS NULL")
}

func TestBoolField_NotNull(t *testing.T) {
	f := BoolField[testPred]("active")
	sql := buildSQL(t, f.NotNull())
	assert.Contains(t, sql, "IS NOT NULL")
}

func TestBoolField_Name(t *testing.T) {
	f := BoolField[testPred]("active")
	assert.Equal(t, "active", f.Name())
}

// --- TimeField ---

func TestTimeField_EQ(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	now := time.Now()
	sql := buildSQL(t, f.EQ(now))
	assert.Contains(t, sql, "`created_at` =")
}

func TestTimeField_GT(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	sql := buildSQL(t, f.GT(time.Now()))
	assert.Contains(t, sql, "`created_at` >")
}

func TestTimeField_In(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	sql := buildSQL(t, f.In(time.Now(), time.Now().Add(time.Hour)))
	assert.Contains(t, sql, "`created_at` IN")
}

func TestTimeField_IsNull(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	sql := buildSQL(t, f.IsNull())
	assert.Contains(t, sql, "IS NULL")
}

func TestTimeField_Name(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	assert.Equal(t, "created_at", f.Name())
}

// --- EnumField ---

type testEnum string

func TestEnumField_EQ(t *testing.T) {
	f := EnumField[testPred, testEnum]("status")
	sql := buildSQL(t, f.EQ("active"))
	assert.Contains(t, sql, "`status` =")
}

func TestEnumField_NEQ(t *testing.T) {
	f := EnumField[testPred, testEnum]("status")
	sql := buildSQL(t, f.NEQ("inactive"))
	assert.Contains(t, sql, "`status` <>")
}

func TestEnumField_In(t *testing.T) {
	f := EnumField[testPred, testEnum]("status")
	sql := buildSQL(t, f.In("active", "pending"))
	assert.Contains(t, sql, "`status` IN")
}

func TestEnumField_IsNull(t *testing.T) {
	f := EnumField[testPred, testEnum]("status")
	sql := buildSQL(t, f.IsNull())
	assert.Contains(t, sql, "IS NULL")
}

func TestEnumField_Name(t *testing.T) {
	f := EnumField[testPred, testEnum]("status")
	assert.Equal(t, "status", f.Name())
}

// --- Int64Field (remaining methods) ---

func TestInt64Field_NEQ(t *testing.T) {
	f := Int64Field[testPred]("count")
	sql := buildSQL(t, f.NEQ(int64(100)))
	assert.Contains(t, sql, "`count` <>")
}

func TestInt64Field_NotIn(t *testing.T) {
	f := Int64Field[testPred]("count")
	sql := buildSQL(t, f.NotIn(int64(1), int64(2)))
	assert.Contains(t, sql, "`count` NOT IN")
}

func TestInt64Field_GTE(t *testing.T) {
	f := Int64Field[testPred]("count")
	sql := buildSQL(t, f.GTE(int64(10)))
	assert.Contains(t, sql, "`count` >=")
}

func TestInt64Field_LT(t *testing.T) {
	f := Int64Field[testPred]("count")
	sql := buildSQL(t, f.LT(int64(100)))
	assert.Contains(t, sql, "`count` <")
}

func TestInt64Field_LTE(t *testing.T) {
	f := Int64Field[testPred]("count")
	sql := buildSQL(t, f.LTE(int64(100)))
	assert.Contains(t, sql, "`count` <=")
}

func TestInt64Field_NotNull(t *testing.T) {
	f := Int64Field[testPred]("count")
	sql := buildSQL(t, f.NotNull())
	assert.Contains(t, sql, "IS NOT NULL")
}

func TestInt64Field_IsNil(t *testing.T) {
	f := Int64Field[testPred]("count")
	sql := buildSQL(t, f.IsNil())
	assert.Contains(t, sql, "IS NULL")
}

func TestInt64Field_NotNil(t *testing.T) {
	f := Int64Field[testPred]("count")
	sql := buildSQL(t, f.NotNil())
	assert.Contains(t, sql, "IS NOT NULL")
}

// --- Float64Field (remaining methods) ---

func TestFloat64Field_NEQ(t *testing.T) {
	f := Float64Field[testPred]("price")
	sql := buildSQL(t, f.NEQ(1.0))
	assert.Contains(t, sql, "`price` <>")
}

func TestFloat64Field_NotIn(t *testing.T) {
	f := Float64Field[testPred]("price")
	sql := buildSQL(t, f.NotIn(1.0, 2.0))
	assert.Contains(t, sql, "`price` NOT IN")
}

func TestFloat64Field_GTE(t *testing.T) {
	f := Float64Field[testPred]("price")
	sql := buildSQL(t, f.GTE(0.0))
	assert.Contains(t, sql, "`price` >=")
}

func TestFloat64Field_LT(t *testing.T) {
	f := Float64Field[testPred]("price")
	sql := buildSQL(t, f.LT(100.0))
	assert.Contains(t, sql, "`price` <")
}

func TestFloat64Field_LTE(t *testing.T) {
	f := Float64Field[testPred]("price")
	sql := buildSQL(t, f.LTE(100.0))
	assert.Contains(t, sql, "`price` <=")
}

func TestFloat64Field_NotNull(t *testing.T) {
	f := Float64Field[testPred]("price")
	sql := buildSQL(t, f.NotNull())
	assert.Contains(t, sql, "IS NOT NULL")
}

func TestFloat64Field_IsNil(t *testing.T) {
	f := Float64Field[testPred]("price")
	sql := buildSQL(t, f.IsNil())
	assert.Contains(t, sql, "IS NULL")
}

func TestFloat64Field_NotNil(t *testing.T) {
	f := Float64Field[testPred]("price")
	sql := buildSQL(t, f.NotNil())
	assert.Contains(t, sql, "IS NOT NULL")
}

// --- BoolField (remaining methods) ---

func TestBoolField_IsNil(t *testing.T) {
	f := BoolField[testPred]("active")
	sql := buildSQL(t, f.IsNil())
	assert.Contains(t, sql, "IS NULL")
}

func TestBoolField_NotNil(t *testing.T) {
	f := BoolField[testPred]("active")
	sql := buildSQL(t, f.NotNil())
	assert.Contains(t, sql, "IS NOT NULL")
}

// --- TimeField (remaining methods) ---

func TestTimeField_NEQ(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	sql := buildSQL(t, f.NEQ(time.Now()))
	assert.Contains(t, sql, "`created_at` <>")
}

func TestTimeField_NotIn(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	sql := buildSQL(t, f.NotIn(time.Now()))
	assert.Contains(t, sql, "`created_at` NOT IN")
}

func TestTimeField_GTE(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	sql := buildSQL(t, f.GTE(time.Now()))
	assert.Contains(t, sql, "`created_at` >=")
}

func TestTimeField_LT(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	sql := buildSQL(t, f.LT(time.Now()))
	assert.Contains(t, sql, "`created_at` <")
}

func TestTimeField_LTE(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	sql := buildSQL(t, f.LTE(time.Now()))
	assert.Contains(t, sql, "`created_at` <=")
}

func TestTimeField_NotNull(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	sql := buildSQL(t, f.NotNull())
	assert.Contains(t, sql, "IS NOT NULL")
}

func TestTimeField_IsNil(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	sql := buildSQL(t, f.IsNil())
	assert.Contains(t, sql, "IS NULL")
}

func TestTimeField_NotNil(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	sql := buildSQL(t, f.NotNil())
	assert.Contains(t, sql, "IS NOT NULL")
}

// --- EnumField (remaining methods) ---

func TestEnumField_NotIn(t *testing.T) {
	f := EnumField[testPred, testEnum]("status")
	sql := buildSQL(t, f.NotIn("active", "pending"))
	assert.Contains(t, sql, "`status` NOT IN")
}

func TestEnumField_NotNull(t *testing.T) {
	f := EnumField[testPred, testEnum]("status")
	sql := buildSQL(t, f.NotNull())
	assert.Contains(t, sql, "IS NOT NULL")
}

func TestEnumField_IsNil(t *testing.T) {
	f := EnumField[testPred, testEnum]("status")
	sql := buildSQL(t, f.IsNil())
	assert.Contains(t, sql, "IS NULL")
}

func TestEnumField_NotNil(t *testing.T) {
	f := EnumField[testPred, testEnum]("status")
	sql := buildSQL(t, f.NotNil())
	assert.Contains(t, sql, "IS NOT NULL")
}

// --- UUIDField ---

func TestUUIDField_Name(t *testing.T) {
	f := UUIDField[testPred, string]("uuid")
	assert.Equal(t, "uuid", f.Name())
}

func TestUUIDField_EQ(t *testing.T) {
	f := UUIDField[testPred, string]("uuid")
	sql := buildSQL(t, f.EQ("abc-123"))
	assert.Contains(t, sql, "`uuid` =")
}

func TestUUIDField_NEQ(t *testing.T) {
	f := UUIDField[testPred, string]("uuid")
	sql := buildSQL(t, f.NEQ("abc-123"))
	assert.Contains(t, sql, "`uuid` <>")
}

func TestUUIDField_In(t *testing.T) {
	f := UUIDField[testPred, string]("uuid")
	sql := buildSQL(t, f.In("a", "b"))
	assert.Contains(t, sql, "`uuid` IN")
}

func TestUUIDField_NotIn(t *testing.T) {
	f := UUIDField[testPred, string]("uuid")
	sql := buildSQL(t, f.NotIn("a", "b"))
	assert.Contains(t, sql, "`uuid` NOT IN")
}

func TestUUIDField_GT(t *testing.T) {
	f := UUIDField[testPred, string]("uuid")
	sql := buildSQL(t, f.GT("abc"))
	assert.Contains(t, sql, "`uuid` >")
}

func TestUUIDField_GTE(t *testing.T) {
	f := UUIDField[testPred, string]("uuid")
	sql := buildSQL(t, f.GTE("abc"))
	assert.Contains(t, sql, "`uuid` >=")
}

func TestUUIDField_LT(t *testing.T) {
	f := UUIDField[testPred, string]("uuid")
	sql := buildSQL(t, f.LT("abc"))
	assert.Contains(t, sql, "`uuid` <")
}

func TestUUIDField_LTE(t *testing.T) {
	f := UUIDField[testPred, string]("uuid")
	sql := buildSQL(t, f.LTE("abc"))
	assert.Contains(t, sql, "`uuid` <=")
}

func TestUUIDField_IsNull(t *testing.T) {
	f := UUIDField[testPred, string]("uuid")
	sql := buildSQL(t, f.IsNull())
	assert.Contains(t, sql, "IS NULL")
}

func TestUUIDField_NotNull(t *testing.T) {
	f := UUIDField[testPred, string]("uuid")
	sql := buildSQL(t, f.NotNull())
	assert.Contains(t, sql, "IS NOT NULL")
}

func TestUUIDField_IsNil(t *testing.T) {
	f := UUIDField[testPred, string]("uuid")
	sql := buildSQL(t, f.IsNil())
	assert.Contains(t, sql, "IS NULL")
}

func TestUUIDField_NotNil(t *testing.T) {
	f := UUIDField[testPred, string]("uuid")
	sql := buildSQL(t, f.NotNil())
	assert.Contains(t, sql, "IS NOT NULL")
}

// --- OtherField ---

func TestOtherField_Name(t *testing.T) {
	f := OtherField[testPred, string]("data")
	assert.Equal(t, "data", f.Name())
}

func TestOtherField_EQ(t *testing.T) {
	f := OtherField[testPred, string]("data")
	sql := buildSQL(t, f.EQ("value"))
	assert.Contains(t, sql, "`data` =")
}

func TestOtherField_NEQ(t *testing.T) {
	f := OtherField[testPred, string]("data")
	sql := buildSQL(t, f.NEQ("value"))
	assert.Contains(t, sql, "`data` <>")
}

func TestOtherField_In(t *testing.T) {
	f := OtherField[testPred, string]("data")
	sql := buildSQL(t, f.In("a", "b"))
	assert.Contains(t, sql, "`data` IN")
}

func TestOtherField_NotIn(t *testing.T) {
	f := OtherField[testPred, string]("data")
	sql := buildSQL(t, f.NotIn("a", "b"))
	assert.Contains(t, sql, "`data` NOT IN")
}

func TestOtherField_GT(t *testing.T) {
	f := OtherField[testPred, string]("data")
	sql := buildSQL(t, f.GT("m"))
	assert.Contains(t, sql, "`data` >")
}

func TestOtherField_GTE(t *testing.T) {
	f := OtherField[testPred, string]("data")
	sql := buildSQL(t, f.GTE("m"))
	assert.Contains(t, sql, "`data` >=")
}

func TestOtherField_LT(t *testing.T) {
	f := OtherField[testPred, string]("data")
	sql := buildSQL(t, f.LT("m"))
	assert.Contains(t, sql, "`data` <")
}

func TestOtherField_LTE(t *testing.T) {
	f := OtherField[testPred, string]("data")
	sql := buildSQL(t, f.LTE("m"))
	assert.Contains(t, sql, "`data` <=")
}

func TestOtherField_IsNull(t *testing.T) {
	f := OtherField[testPred, string]("data")
	sql := buildSQL(t, f.IsNull())
	assert.Contains(t, sql, "IS NULL")
}

func TestOtherField_NotNull(t *testing.T) {
	f := OtherField[testPred, string]("data")
	sql := buildSQL(t, f.NotNull())
	assert.Contains(t, sql, "IS NOT NULL")
}

func TestOtherField_IsNil(t *testing.T) {
	f := OtherField[testPred, string]("data")
	sql := buildSQL(t, f.IsNil())
	assert.Contains(t, sql, "IS NULL")
}

func TestOtherField_NotNil(t *testing.T) {
	f := OtherField[testPred, string]("data")
	sql := buildSQL(t, f.NotNil())
	assert.Contains(t, sql, "IS NOT NULL")
}

// --- IntField (remaining methods) ---

func TestIntField_IsNil(t *testing.T) {
	f := IntField[testPred]("age")
	sql := buildSQL(t, f.IsNil())
	assert.Contains(t, sql, "IS NULL")
}

func TestIntField_NotNil(t *testing.T) {
	f := IntField[testPred]("age")
	sql := buildSQL(t, f.NotNil())
	assert.Contains(t, sql, "IS NOT NULL")
}

// --- Method Reference Tests (critical for gqlrelay.AppendIf) ---

func TestMethodReference_StringField_EQ(t *testing.T) {
	f := StringField[testPred]("name")
	// Verify method reference is a valid func(string) testPred
	fn := f.EQ
	pred := fn("test")
	require.NotNil(t, pred)
	sql := buildSQL(t, pred)
	assert.Contains(t, sql, "`name` = ")
}

func TestMethodReference_StringField_Contains(t *testing.T) {
	f := StringField[testPred]("name")
	fn := f.Contains
	pred := fn("sub")
	require.NotNil(t, pred)
	sql := buildSQL(t, pred)
	assert.Contains(t, sql, "LIKE")
}

func TestMethodReference_StringField_IsNil(t *testing.T) {
	f := StringField[testPred]("name")
	// Niladic method reference: func() testPred
	fn := f.IsNil
	pred := fn()
	require.NotNil(t, pred)
	sql := buildSQL(t, pred)
	assert.Contains(t, sql, "IS NULL")
}

func TestMethodReference_IntField_In(t *testing.T) {
	f := IntField[testPred]("age")
	// Variadic method reference: func(...int) testPred
	fn := f.In
	pred := fn(1, 2, 3)
	require.NotNil(t, pred)
	sql := buildSQL(t, pred)
	assert.Contains(t, sql, "`age` IN")
}

func TestMethodReference_Int64Field_GT(t *testing.T) {
	f := Int64Field[testPred]("count")
	fn := f.GT
	pred := fn(int64(100))
	require.NotNil(t, pred)
	sql := buildSQL(t, pred)
	assert.Contains(t, sql, "`count` >")
}

func TestMethodReference_BoolField_EQ(t *testing.T) {
	f := BoolField[testPred]("active")
	fn := f.EQ
	pred := fn(true)
	require.NotNil(t, pred)
	sql := buildSQL(t, pred)
	assert.Contains(t, sql, "`active`")
}

func TestMethodReference_TimeField_LTE(t *testing.T) {
	f := TimeField[testPred, time.Time]("created_at")
	fn := f.LTE
	pred := fn(time.Now())
	require.NotNil(t, pred)
	sql := buildSQL(t, pred)
	assert.Contains(t, sql, "`created_at` <=")
}

func TestMethodReference_EnumField_NEQ(t *testing.T) {
	f := EnumField[testPred, testEnum]("status")
	fn := f.NEQ
	pred := fn("active")
	require.NotNil(t, pred)
	sql := buildSQL(t, pred)
	assert.Contains(t, sql, "`status` <>")
}
