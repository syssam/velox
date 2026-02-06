package field_test

import (
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/schema/field"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInt(t *testing.T) {
	fd := field.Int("age").
		Positive().
		Comment("comment").
		Descriptor()
	assert.Equal(t, "age", fd.Name)
	assert.Equal(t, field.TypeInt, fd.Info.Type)
	assert.Len(t, fd.Validators, 1)
	assert.Equal(t, "comment", fd.Comment)

	fd = field.Int("age").
		Default(10).
		Min(10).
		Max(20).
		Descriptor()
	assert.NotNil(t, fd.Default)
	assert.Equal(t, 10, fd.Default)
	assert.Len(t, fd.Validators, 2)

	fd = field.Int("age").
		Range(20, 40).
		Nillable().
		SchemaType(map[string]string{
			dialect.SQLite:   "numeric",
			dialect.Postgres: "int_type",
		}).
		Descriptor()
	assert.Nil(t, fd.Default)
	assert.True(t, fd.Nillable)
	assert.False(t, fd.Immutable)
	assert.Len(t, fd.Validators, 1)
	assert.Equal(t, "numeric", fd.SchemaType[dialect.SQLite])
	assert.Equal(t, "int_type", fd.SchemaType[dialect.Postgres])

	assert.Equal(t, field.TypeInt8, field.Int8("age").Descriptor().Info.Type)
	assert.Equal(t, field.TypeInt16, field.Int16("age").Descriptor().Info.Type)
	assert.Equal(t, field.TypeInt32, field.Int32("age").Descriptor().Info.Type)
	assert.Equal(t, field.TypeInt64, field.Int64("age").Descriptor().Info.Type)
	assert.Equal(t, field.TypeUint, field.Uint("age").Descriptor().Info.Type)
	assert.Equal(t, field.TypeUint8, field.Uint8("age").Descriptor().Info.Type)
	assert.Equal(t, field.TypeUint16, field.Uint16("age").Descriptor().Info.Type)
	assert.Equal(t, field.TypeUint32, field.Uint32("age").Descriptor().Info.Type)
	assert.Equal(t, field.TypeUint64, field.Uint64("age").Descriptor().Info.Type)

	type Count int
	fd = field.Int("active").GoType(Count(0)).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "field_test.Count", fd.Info.Ident)
	assert.Equal(t, "github.com/syssam/velox/schema/field_test", fd.Info.PkgPath)
	assert.Equal(t, "field_test.Count", fd.Info.String())
	assert.False(t, fd.Info.Nillable)
	assert.False(t, fd.Info.ValueScanner())

	fd = field.Int("count").GoType(&sql.NullInt64{}).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "*sql.NullInt64", fd.Info.Ident)
	assert.Equal(t, "database/sql", fd.Info.PkgPath)
	assert.Equal(t, "*sql.NullInt64", fd.Info.String())
	assert.True(t, fd.Info.Nillable)
	assert.True(t, fd.Info.ValueScanner())

	fd = field.Int("count").GoType(false).Descriptor()
	assert.EqualError(t, fd.Err, `GoType must be a "int" type, ValueScanner or provide an external ValueScanner`)
	fd = field.Int("count").GoType(struct{}{}).Descriptor()
	assert.Error(t, fd.Err)
	fd = field.Int("count").GoType(new(Count)).Descriptor()
	assert.Error(t, fd.Err)
}

func TestInt_DefaultFunc(t *testing.T) {
	type CustomInt int

	f1 := func() CustomInt { return 1000 }
	fd := field.Int("id").DefaultFunc(f1).GoType(CustomInt(0)).Descriptor()
	assert.NoError(t, fd.Err)

	fd = field.Int("id").DefaultFunc(f1).Descriptor()
	assert.Error(t, fd.Err, "`var _ int = f1()` should fail")

	f2 := func() int { return 1000 }
	fd = field.Int("dir").GoType(CustomInt(0)).DefaultFunc(f2).Descriptor()
	assert.Error(t, fd.Err, "`var _ CustomInt = f2()` should fail")

	fd = field.Int("id").DefaultFunc(f2).UpdateDefault(f2).Descriptor()
	assert.NoError(t, fd.Err)
	assert.NotNil(t, fd.Default)
	assert.NotNil(t, fd.UpdateDefault)
}

func TestFloat(t *testing.T) {
	f := field.Float("age").Comment("comment").Positive()
	fd := f.Descriptor()
	assert.Equal(t, "age", fd.Name)
	assert.Equal(t, field.TypeFloat64, fd.Info.Type)
	assert.Len(t, fd.Validators, 1)
	assert.Equal(t, "comment", fd.Comment)

	f = field.Float("age").Min(2.5).Max(5)
	fd = f.Descriptor()
	assert.Len(t, fd.Validators, 2)
	assert.Equal(t, field.TypeFloat32, field.Float32("age").Descriptor().Info.Type)

	type Count float64
	fd = field.Float("active").GoType(Count(0)).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "field_test.Count", fd.Info.Ident)
	assert.Equal(t, "github.com/syssam/velox/schema/field_test", fd.Info.PkgPath)
	assert.Equal(t, "field_test.Count", fd.Info.String())
	assert.False(t, fd.Info.Nillable)
	assert.False(t, fd.Info.ValueScanner())

	fd = field.Float("count").GoType(&sql.NullFloat64{}).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "*sql.NullFloat64", fd.Info.Ident)
	assert.Equal(t, "database/sql", fd.Info.PkgPath)
	assert.Equal(t, "*sql.NullFloat64", fd.Info.String())
	assert.True(t, fd.Info.Nillable)
	assert.True(t, fd.Info.ValueScanner())

	fd = field.Float("count").GoType(1).Descriptor()
	assert.Error(t, fd.Err)
	fd = field.Float("count").GoType(struct{}{}).Descriptor()
	assert.Error(t, fd.Err)
	fd = field.Float("count").GoType(new(Count)).Descriptor()
	assert.Error(t, fd.Err)
}

func TestFloat_DefaultFunc(t *testing.T) {
	type CustomFloat float64

	f1 := func() CustomFloat { return 1.2 }
	fd := field.Float("weight").DefaultFunc(f1).GoType(CustomFloat(0.)).Descriptor()
	assert.NoError(t, fd.Err)

	fd = field.Float("weight").DefaultFunc(f1).Descriptor()
	assert.Error(t, fd.Err, "`var _ float = f1()` should fail")

	f2 := func() float64 { return 1000 }
	fd = field.Float("weight").GoType(CustomFloat(0)).DefaultFunc(f2).Descriptor()
	assert.Error(t, fd.Err, "`var _ CustomFloat = f2()` should fail")

	fd = field.Float("weight").DefaultFunc(f2).UpdateDefault(f2).Descriptor()
	assert.NoError(t, fd.Err)
	assert.NotNil(t, fd.Default)
	assert.NotNil(t, fd.UpdateDefault)

	f3 := func() float64 { return 1.2 }
	fd = field.Float("weight").DefaultFunc(f3).Descriptor()
	assert.NoError(t, fd.Err)
}

func TestBool(t *testing.T) {
	fd := field.Bool("active").Default(true).Comment("comment").Immutable().Descriptor()
	assert.Equal(t, "active", fd.Name)
	assert.Equal(t, field.TypeBool, fd.Info.Type)
	assert.NotNil(t, fd.Default)
	assert.True(t, fd.Immutable)
	assert.Equal(t, true, fd.Default)
	assert.Equal(t, "comment", fd.Comment)

	type Status bool
	fd = field.Bool("active").GoType(Status(false)).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "field_test.Status", fd.Info.Ident)
	assert.Equal(t, "github.com/syssam/velox/schema/field_test", fd.Info.PkgPath)
	assert.Equal(t, "field_test.Status", fd.Info.String())
	assert.False(t, fd.Info.Nillable)
	assert.False(t, fd.Info.ValueScanner())

	fd = field.Bool("deleted").GoType(&sql.NullBool{}).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "*sql.NullBool", fd.Info.Ident)
	assert.Equal(t, "database/sql", fd.Info.PkgPath)
	assert.Equal(t, "*sql.NullBool", fd.Info.String())
	assert.True(t, fd.Info.Nillable)
	assert.True(t, fd.Info.ValueScanner())

	fd = field.Bool("active").GoType(1).Descriptor()
	assert.Error(t, fd.Err)
	fd = field.Bool("active").GoType(struct{}{}).Descriptor()
	assert.Error(t, fd.Err)
	fd = field.Bool("active").GoType(new(Status)).Descriptor()
	assert.Error(t, fd.Err)
}

type Pair struct {
	K, V []byte
}

func (*Pair) Scan(any) error              { return nil }
func (Pair) Value() (driver.Value, error) { return nil, nil }

func TestBytes(t *testing.T) {
	fd := field.Bytes("active").
		Unique().
		Default([]byte("{}")).
		Comment("comment").
		Validate(func(bytes []byte) error {
			return nil
		}).
		MaxLen(50).
		Descriptor()
	assert.Equal(t, "active", fd.Name)
	assert.True(t, fd.Unique)
	assert.Equal(t, field.TypeBytes, fd.Info.Type)
	assert.NotNil(t, fd.Default)
	assert.Equal(t, []byte("{}"), fd.Default)
	assert.Equal(t, "comment", fd.Comment)
	assert.Len(t, fd.Validators, 2)

	fd = field.Bytes("ip").GoType(net.IP("127.0.0.1")).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "net.IP", fd.Info.Ident)
	assert.Equal(t, "net", fd.Info.PkgPath)
	assert.Equal(t, "net.IP", fd.Info.String())
	assert.True(t, fd.Info.Nillable)
	assert.False(t, fd.Info.ValueScanner())

	fd = field.Bytes("blob").GoType(sql.NullString{}).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "sql.NullString", fd.Info.Ident)
	assert.Equal(t, "database/sql", fd.Info.PkgPath)
	assert.Equal(t, "sql.NullString", fd.Info.String())
	assert.False(t, fd.Info.Nillable)
	assert.True(t, fd.Info.ValueScanner())

	fd = field.Bytes("uuid").GoType(uuid.UUID{}).DefaultFunc(uuid.New).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "uuid.UUID", fd.Info.Ident)
	assert.Equal(t, "github.com/google/uuid", fd.Info.PkgPath)
	assert.Equal(t, "uuid.UUID", fd.Info.String())
	assert.False(t, fd.Info.Nillable)
	assert.True(t, fd.Info.ValueScanner())
	assert.NotEmpty(t, fd.Default.(func() uuid.UUID)())

	fd = field.Bytes("uuid").
		GoType(uuid.UUID{}).
		DefaultFunc(uuid.New).
		Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "uuid.UUID", fd.Info.String())
	fd = field.Bytes("pair").
		GoType(&Pair{}).
		Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "*field_test.Pair", fd.Info.String())

	fd = field.Bytes("blob").GoType(1).Descriptor()
	assert.Error(t, fd.Err)
	fd = field.Bytes("blob").GoType(struct{}{}).Descriptor()
	assert.Error(t, fd.Err)
	fd = field.Bytes("blob").GoType(new(net.IP)).Descriptor()
	assert.Error(t, fd.Err)
}

func TestBytes_DefaultFunc(t *testing.T) {
	f1 := func() net.IP { return net.IP("0.0.0.0") }
	fd := field.Bytes("ip").GoType(net.IP("127.0.0.1")).DefaultFunc(f1).Descriptor()
	assert.NoError(t, fd.Err)

	var _ []byte = f1()
	fd = field.Bytes("ip").DefaultFunc(f1).Descriptor()
	assert.NoError(t, fd.Err)

	f2 := func() []byte { return []byte("0.0.0.0") }
	var _ net.IP = f2()
	fd = field.Bytes("ip").GoType(net.IP("127.0.0.1")).DefaultFunc(f2).Descriptor()
	assert.NoError(t, fd.Err)

	f3 := func() []uint8 { return []uint8("0.0.0.0") }
	var _ net.IP = f3()
	fd = field.Bytes("ip").GoType(net.IP("127.0.0.1")).DefaultFunc(f3).Descriptor()
	assert.NoError(t, fd.Err)
	fd = field.Bytes("ip").DefaultFunc(f3).Descriptor()
	assert.NoError(t, fd.Err)

	f4 := func() net.IPMask { return net.IPMask("ffff:ff80::") }
	fd = field.Bytes("ip").GoType(net.IP("127.0.0.1")).DefaultFunc(f4).Descriptor()
	assert.Error(t, fd.Err, "`var _ net.IP = f4()` should fail")

	fd = field.Bytes("ip").GoType(net.IP("127.0.0.1")).DefaultFunc(net.IP("127.0.0.1")).Descriptor()
	assert.EqualError(t, fd.Err, `field.Bytes("ip").DefaultFunc expects func but got slice`)
}

type nullBytes []byte

func (b *nullBytes) Scan(v any) error {
	if v == nil {
		return nil
	}
	switch v := v.(type) {
	case []byte:
		*b = v
		return nil
	case string:
		*b = []byte(v)
		return nil
	default:
		return errors.New("unexpected type")
	}
}

func (b nullBytes) Value() (driver.Value, error) { return b, nil }

func TestBytes_ValueScanner(t *testing.T) {
	fd := field.Bytes("dir").
		ValueScanner(field.ValueScannerFunc[[]byte, *nullBytes]{
			V: func(s []byte) (driver.Value, error) {
				return []byte(hex.EncodeToString(s)), nil
			},
			S: func(ns *nullBytes) ([]byte, error) {
				if ns == nil {
					return nil, nil
				}
				b, err := hex.DecodeString(string(*ns))
				if err != nil {
					return nil, err
				}
				return b, nil
			},
		}).Descriptor()
	require.NoError(t, fd.Err)
	require.NotNil(t, fd.ValueScanner)
	_, ok := fd.ValueScanner.(field.ValueScannerFunc[[]byte, *nullBytes])
	require.True(t, ok)

	fd = field.Bytes("url").
		GoType(&url.URL{}).
		ValueScanner(field.BinaryValueScanner[*url.URL]{}).
		Descriptor()
	require.NoError(t, fd.Err)
	require.NotNil(t, fd.ValueScanner)
	_, ok = fd.ValueScanner.(field.TypeValueScanner[*url.URL])
	require.True(t, ok)
}

func TestString_DefaultFunc(t *testing.T) {
	f1 := func() http.Dir { return "/tmp" }
	fd := field.String("dir").GoType(http.Dir("/tmp")).DefaultFunc(f1).Descriptor()
	assert.NoError(t, fd.Err)

	fd = field.String("dir").DefaultFunc(f1).Descriptor()
	assert.Error(t, fd.Err, "`var _ string = f1()` should fail")

	f2 := func() string { return "/tmp" }
	fd = field.String("dir").GoType(http.Dir("/tmp")).DefaultFunc(f2).Descriptor()
	assert.Error(t, fd.Err, "`var _ http.Dir = f2()` should fail")

	f3 := func() sql.NullString { return sql.NullString{} }
	fd = field.String("str").GoType(sql.NullString{}).DefaultFunc(f3).Descriptor()
	assert.NoError(t, fd.Err)

	type S string
	f4 := func() S { return "" }
	fd = field.String("str").GoType(http.Dir("/tmp")).DefaultFunc(f4).Descriptor()
	assert.Error(t, fd.Err, "`var _ http.Dir = f4()` should fail")

	fd = field.String("str").GoType(http.Dir("/tmp")).DefaultFunc("/tmp").Descriptor()
	assert.EqualError(t, fd.Err, `field.String("str").DefaultFunc expects func but got string`)
}

func TestString_ValueScanner(t *testing.T) {
	fd := field.String("dir").
		ValueScanner(field.ValueScannerFunc[string, *sql.NullString]{
			V: func(s string) (driver.Value, error) {
				return base64.StdEncoding.EncodeToString([]byte(s)), nil
			},
			S: func(ns *sql.NullString) (string, error) {
				if !ns.Valid {
					return "", nil
				}
				b, err := base64.StdEncoding.DecodeString(ns.String)
				if err != nil {
					return "", err
				}
				return string(b), nil
			},
		}).Descriptor()
	require.NoError(t, fd.Err)
	require.NotNil(t, fd.ValueScanner)
	_, ok := fd.ValueScanner.(field.TypeValueScanner[string])
	require.True(t, ok)

	fd = field.String("url").
		GoType(&url.URL{}).
		ValueScanner(field.BinaryValueScanner[*url.URL]{}).
		Descriptor()
	require.NoError(t, fd.Err)
	require.NotNil(t, fd.ValueScanner)
	_, ok = fd.ValueScanner.(field.TypeValueScanner[*url.URL])
	require.True(t, ok)
}

func TestSlices(t *testing.T) {
	fd := field.Strings("strings").
		Default([]string{}).
		Comment("comment").
		Validate(func(xs []string) error {
			return nil
		}).
		Descriptor()
	assert.Equal(t, "strings", fd.Name)
	assert.Equal(t, field.TypeJSON, fd.Info.Type)
	assert.NotNil(t, fd.Default)
	assert.Equal(t, []string{}, fd.Default)
	assert.Equal(t, "comment", fd.Comment)
	assert.Len(t, fd.Validators, 1)

	fd = field.Ints("ints").
		Default([]int{}).
		Comment("comment").
		Validate(func(xs []int) error {
			return nil
		}).
		Descriptor()
	assert.Equal(t, "ints", fd.Name)
	assert.Equal(t, field.TypeJSON, fd.Info.Type)
	assert.NotNil(t, fd.Default)
	assert.Equal(t, []int{}, fd.Default)
	assert.Equal(t, "comment", fd.Comment)
	assert.Len(t, fd.Validators, 1)

	fd = field.Floats("floats").
		Default([]float64{}).
		Comment("comment").
		Validate(func(xs []float64) error {
			return nil
		}).
		Descriptor()
	assert.Equal(t, "floats", fd.Name)
	assert.Equal(t, field.TypeJSON, fd.Info.Type)
	assert.NotNil(t, fd.Default)
	assert.Equal(t, []float64{}, fd.Default)
	assert.Equal(t, "comment", fd.Comment)
	assert.Len(t, fd.Validators, 1)
}

type VString string

func (s *VString) Scan(any) error {
	return nil
}

func (s VString) Value() (driver.Value, error) {
	return "", nil
}

func TestString(t *testing.T) {
	fd := field.String("name").
		DefaultFunc(func() string {
			return "Ent"
		}).
		Comment("comment").
		Descriptor()

	assert.Equal(t, "name", fd.Name)
	assert.Equal(t, field.TypeString, fd.Info.Type)
	assert.Equal(t, "Ent", fd.Default.(func() string)())
	assert.Equal(t, "comment", fd.Comment)

	re := regexp.MustCompile("[a-zA-Z0-9]")
	f := field.String("name").Unique().Match(re).Validate(func(string) error { return nil }).Sensitive()
	fd = f.Descriptor()
	assert.Equal(t, field.TypeString, fd.Info.Type)
	assert.Equal(t, "name", fd.Name)
	assert.True(t, fd.Unique)
	assert.Len(t, fd.Validators, 2)
	assert.True(t, fd.Sensitive)

	fd = field.String("name").GoType(http.Dir("dir")).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "http.Dir", fd.Info.Ident)
	assert.Equal(t, "net/http", fd.Info.PkgPath)
	assert.Equal(t, "http.Dir", fd.Info.String())
	assert.False(t, fd.Info.Nillable)
	assert.False(t, fd.Info.ValueScanner())

	fd = field.String("name").GoType(http.MethodOptions).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "string", fd.Info.Ident)
	assert.Equal(t, "", fd.Info.PkgPath)
	assert.Equal(t, "string", fd.Info.String())
	assert.False(t, fd.Info.Nillable)

	fd = field.String("nullable_name").GoType(&sql.NullString{}).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "*sql.NullString", fd.Info.Ident)
	assert.Equal(t, "database/sql", fd.Info.PkgPath)
	assert.Equal(t, "*sql.NullString", fd.Info.String())
	assert.True(t, fd.Info.Nillable)
	assert.True(t, fd.Info.ValueScanner())
	assert.False(t, fd.Info.Stringer())
	assert.True(t, fd.Info.RType.TypeEqual(reflect.TypeOf(&sql.NullString{})))

	fd = field.String("nullable_name").GoType(VString("")).Descriptor()
	assert.True(t, fd.Info.Valuer())
	assert.True(t, fd.Info.ValueScanner())
	assert.False(t, fd.Info.Stringer())

	type tURL struct {
		field.ValueScanner
		*url.URL
	}
	fd = field.String("nullable_url").GoType(&tURL{}).Descriptor()
	assert.Equal(t, "*field_test.tURL", fd.Info.Ident)
	assert.Equal(t, "github.com/syssam/velox/schema/field_test", fd.Info.PkgPath)
	assert.Equal(t, "*field_test.tURL", fd.Info.String())
	assert.True(t, fd.Info.ValueScanner())
	assert.True(t, fd.Info.Stringer())
	assert.Equal(t, "field_test", fd.Info.PkgName)

	fd = field.String("name").GoType(1).Descriptor()
	assert.Error(t, fd.Err)
	fd = field.String("name").GoType(struct{}{}).Descriptor()
	assert.Error(t, fd.Err)
	fd = field.String("name").GoType(new(http.Dir)).Descriptor()
	assert.Error(t, fd.Err)
}

func TestTime(t *testing.T) {
	now := time.Now()
	fd := field.Time("created_at").
		Default(func() time.Time {
			return now
		}).
		Comment("comment").
		Descriptor()
	assert.Equal(t, "created_at", fd.Name)
	assert.Equal(t, field.TypeTime, fd.Info.Type)
	assert.Equal(t, "time.Time", fd.Info.Type.String())
	assert.NotNil(t, fd.Default)
	assert.Equal(t, now, fd.Default.(func() time.Time)())
	assert.Equal(t, "comment", fd.Comment)

	fd = field.Time("updated_at").
		UpdateDefault(func() time.Time {
			return now
		}).
		Descriptor()
	assert.Equal(t, "updated_at", fd.Name)
	assert.Equal(t, now, fd.UpdateDefault.(func() time.Time)())

	type Time time.Time
	fd = field.Time("deleted_at").GoType(Time{}).Default(func() Time { return Time{} }).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "field_test.Time", fd.Info.Ident)
	assert.Equal(t, "github.com/syssam/velox/schema/field_test", fd.Info.PkgPath)
	assert.Equal(t, "field_test.Time", fd.Info.String())
	assert.False(t, fd.Info.Nillable)
	assert.False(t, fd.Info.ValueScanner())

	fd = field.Time("deleted_at").GoType(&sql.NullTime{}).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "*sql.NullTime", fd.Info.Ident)
	assert.Equal(t, "database/sql", fd.Info.PkgPath)
	assert.Equal(t, "*sql.NullTime", fd.Info.String())
	assert.True(t, fd.Info.Nillable)
	assert.True(t, fd.Info.ValueScanner())
	assert.Equal(t, "sql", fd.Info.PkgName)

	fd = field.Time("deleted_at").GoType(Time{}).Default(time.Now).Descriptor()
	assert.Error(t, fd.Err)
	fd = field.Time("active").GoType(1).Descriptor()
	assert.Error(t, fd.Err)
	fd = field.Time("active").GoType(struct{}{}).Descriptor()
	assert.Error(t, fd.Err)
	fd = field.Time("active").GoType(new(Time)).Descriptor()
	assert.Error(t, fd.Err)
}

func TestJSON(t *testing.T) {
	fd := field.JSON("name", map[string]string{}).
		Optional().
		Comment("comment").
		Descriptor()
	assert.True(t, fd.Optional)
	assert.Empty(t, fd.Info.PkgPath)
	assert.Equal(t, "name", fd.Name)
	assert.Equal(t, field.TypeJSON, fd.Info.Type)
	assert.Equal(t, "map[string]string", fd.Info.String())
	assert.Equal(t, "comment", fd.Comment)
	assert.True(t, fd.Info.Nillable)
	assert.False(t, fd.Info.RType.IsPtr())
	assert.Empty(t, fd.Info.PkgName)

	type T struct{ S string }
	fd = field.JSON("name", &T{}).
		Descriptor()
	assert.True(t, fd.Info.Nillable)
	assert.Equal(t, "*field_test.T", fd.Info.Ident)
	assert.Equal(t, "github.com/syssam/velox/schema/field_test", fd.Info.PkgPath)
	assert.True(t, fd.Info.RType.IsPtr())
	assert.Equal(t, "T", fd.Info.RType.Name)
	assert.Equal(t, "github.com/syssam/velox/schema/field_test", fd.Info.RType.PkgPath)

	fd = field.JSON("dir", http.Dir("dir")).
		Optional().
		Descriptor()
	assert.True(t, fd.Optional)
	assert.Equal(t, field.TypeJSON, fd.Info.Type)
	assert.Equal(t, "dir", fd.Name)
	assert.Equal(t, "net/http", fd.Info.PkgPath)
	assert.Equal(t, "http.Dir", fd.Info.String())
	assert.False(t, fd.Info.Nillable)

	fd = field.Strings("strings").
		Optional().
		Default([]string{"a", "b"}).
		Sensitive().
		Descriptor()
	assert.NoError(t, fd.Err)
	assert.True(t, fd.Optional)
	assert.True(t, fd.Sensitive)
	assert.Empty(t, fd.Info.PkgPath)
	assert.Equal(t, "strings", fd.Name)
	assert.Equal(t, []string{"a", "b"}, fd.Default)
	assert.Equal(t, field.TypeJSON, fd.Info.Type)
	assert.Equal(t, "[]string", fd.Info.String())

	fd = field.JSON("dirs", []http.Dir{}).
		Default([]http.Dir{"a", "b"}).
		Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "http", fd.Info.PkgName)

	fd = field.JSON("dirs", []http.Dir{}).
		Default(func() []http.Dir {
			return []http.Dir{"/tmp"}
		}).
		Descriptor()
	assert.NoError(t, fd.Err)

	fd = field.JSON("dirs", []http.Dir{}).
		Default([]string{"a", "b"}).
		Descriptor()
	assert.Error(t, fd.Err)

	fd = field.Any("unknown").
		Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, field.TypeJSON, fd.Info.Type)
	assert.Equal(t, "unknown", fd.Name)
	assert.Equal(t, "any", fd.Info.String())

	fd = field.JSON("values", &url.Values{}).Descriptor()
	assert.Equal(t, "net/url", fd.Info.PkgPath)
	assert.Equal(t, "url", fd.Info.PkgName)
	fd = field.JSON("values", []url.Values{}).Descriptor()
	assert.Equal(t, "net/url", fd.Info.PkgPath)
	assert.Equal(t, "url", fd.Info.PkgName)
	fd = field.JSON("values", []*url.Values{}).Descriptor()
	assert.Equal(t, "net/url", fd.Info.PkgPath)
	assert.Equal(t, "url", fd.Info.PkgName)
	fd = field.JSON("values", map[string]url.Values{}).Descriptor()
	assert.Equal(t, "net/url", fd.Info.PkgPath)
	assert.Equal(t, "url", fd.Info.PkgName)
	fd = field.JSON("values", map[string]*url.Values{}).Descriptor()
	assert.Equal(t, "net/url", fd.Info.PkgPath)
	assert.Equal(t, "url", fd.Info.PkgName)
	fd = field.JSON("addr", net.Addr(nil)).Descriptor()
	assert.EqualError(t, fd.Err, "expect a Go value as JSON type but got nil")
}

func TestField_Tag(t *testing.T) {
	fd := field.Bool("expired").
		StructTag(`json:"expired,omitempty"`).
		Descriptor()
	assert.Equal(t, `json:"expired,omitempty"`, fd.Tag)
}

type Role string

func (Role) Values() []string {
	return []string{"admin", "owner"}
}

type RoleInt int32

func (RoleInt) Values() []string {
	return []string{"unknown", "admin", "owner"}
}

func (i RoleInt) String() string {
	switch i {
	case 1:
		return "admin"
	case 2:
		return "owner"
	default:
		return "unknown"
	}
}

func (i RoleInt) Value() (driver.Value, error) {
	return i.String(), nil
}

func (i *RoleInt) Scan(val any) error {
	switch v := val.(type) {
	case string:
		switch v {
		case "admin":
			*i = 1
		case "owner":
			*i = 2
		default:
			*i = 0
		}
	default:
		return errors.New("bad enum value")
	}

	return nil
}

func TestField_Enums(t *testing.T) {
	fd := field.Enum("role").
		Values(
			"user",
			"admin",
			"master",
		).
		Default("user").
		Comment("comment").
		Descriptor()
	assert.Equal(t, "role", fd.Name)
	assert.Equal(t, "user", fd.Enums[0].V)
	assert.Equal(t, "admin", fd.Enums[1].V)
	assert.Equal(t, "master", fd.Enums[2].V)
	assert.Equal(t, "user", fd.Default)
	assert.Equal(t, "comment", fd.Comment)

	fd = field.Enum("role").
		NamedValues("USER", "user").
		Default("user").
		Descriptor()
	assert.Equal(t, "role", fd.Name)
	assert.Equal(t, "USER", fd.Enums[0].N)
	assert.Equal(t, "user", fd.Enums[0].V)
	assert.Equal(t, "user", fd.Default)

	fd = field.Enum("role").GoType(Role("")).Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "field_test.Role", fd.Info.Ident)
	assert.Equal(t, "github.com/syssam/velox/schema/field_test", fd.Info.PkgPath)
	assert.Equal(t, "field_test.Role", fd.Info.String())
	assert.False(t, fd.Info.Nillable)
	assert.False(t, fd.Info.ValueScanner())
	assert.Equal(t, "admin", fd.Enums[0].V)
	assert.Equal(t, "owner", fd.Enums[1].V)
	assert.False(t, fd.Info.Stringer())

	fd = field.Enum("role").GoType(RoleInt(0)).Descriptor()
	assert.Equal(t, "field_test.RoleInt", fd.Info.Ident)
	assert.Equal(t, "github.com/syssam/velox/schema/field_test", fd.Info.PkgPath)
	assert.Equal(t, "field_test.RoleInt", fd.Info.String())
	assert.False(t, fd.Info.Nillable)
	assert.True(t, fd.Info.ValueScanner())
	assert.Equal(t, "unknown", fd.Enums[0].V)
	assert.Equal(t, "admin", fd.Enums[1].V)
	assert.Equal(t, "owner", fd.Enums[2].V)
	assert.True(t, fd.Info.Stringer())
}

func TestField_UUID(t *testing.T) {
	fd := field.UUID("id", uuid.UUID{}).
		Unique().
		Default(uuid.New).
		Comment("comment").
		Nillable().
		Descriptor()
	assert.Equal(t, "id", fd.Name)
	assert.True(t, fd.Unique)
	assert.Equal(t, "uuid.UUID", fd.Info.String())
	assert.Equal(t, "github.com/google/uuid", fd.Info.PkgPath)
	assert.NotNil(t, fd.Default)
	assert.NotEmpty(t, fd.Default.(func() uuid.UUID)())
	assert.Equal(t, "comment", fd.Comment)
	assert.True(t, fd.Nillable)

	fd = field.UUID("id", &uuid.UUID{}).
		Descriptor()
	assert.Equal(t, "github.com/google/uuid", fd.Info.PkgPath)

	fd = field.UUID("id", uuid.UUID{}).
		Default(uuid.UUID{}).
		Descriptor()
	assert.EqualError(t, fd.Err, "expect type (func() uuid.UUID) for uuid default value")
}

type custom struct {
}

func (c *custom) Scan(_ any) (err error) {
	return nil
}

func (c custom) Value() (driver.Value, error) {
	return nil, nil
}

// mockAnnotation is a test annotation that is not a field.Annotation
type mockAnnotation struct{}

func (mockAnnotation) Name() string { return "Mock" }

func TestField_Other(t *testing.T) {
	fd := field.Other("other", &custom{}).
		Unique().
		Default(&custom{}).
		SchemaType(map[string]string{dialect.Postgres: "varchar"}).
		Descriptor()
	assert.NoError(t, fd.Err)
	assert.Equal(t, "other", fd.Name)
	assert.True(t, fd.Unique)
	assert.Equal(t, "*field_test.custom", fd.Info.String())
	assert.Equal(t, "github.com/syssam/velox/schema/field_test", fd.Info.PkgPath)
	assert.NotNil(t, fd.Default)

	fd = field.Other("other", &custom{}).
		Descriptor()
	assert.Error(t, fd.Err, "missing SchemaType option")

	fd = field.Other("other", &custom{}).
		SchemaType(map[string]string{dialect.Postgres: "varchar"}).
		Default(func() *custom { return &custom{} }).
		Descriptor()
	assert.NoError(t, fd.Err)

	fd = field.Other("other", custom{}).
		SchemaType(map[string]string{dialect.Postgres: "varchar"}).
		Default(func() custom { return custom{} }).
		Descriptor()
	assert.NoError(t, fd.Err)

	fd = field.Other("other", &custom{}).
		SchemaType(map[string]string{dialect.Postgres: "varchar"}).
		Default(func() custom { return custom{} }).
		Descriptor()
	assert.Error(t, fd.Err, "invalid default value")
}

type UserRole string

const (
	Admin   UserRole = "ADMIN"
	User    UserRole = "USER"
	Unknown UserRole = "UNKNOWN"
)

func (UserRole) Values() (roles []string) {
	for _, r := range []UserRole{Admin, User, Unknown} {
		roles = append(roles, string(r))
	}
	return
}

func (e UserRole) String() string {
	return string(e)
}

// MarshalGQL implements graphql.Marshaler interface.
func (e UserRole) MarshalGQL(w io.Writer) {
	_, _ = io.WriteString(w, strconv.Quote(e.String()))
}

// UnmarshalGQL implements graphql.Unmarshaler interface.
func (e *UserRole) UnmarshalGQL(val any) error {
	str, ok := val.(string)
	if !ok {
		return fmt.Errorf("enum %T must be a string", val)
	}
	*e = UserRole(str)
	switch *e {
	case Admin, User, Unknown:
		return nil
	default:
		return fmt.Errorf("%s is not a valid Role", str)
	}
}

type Scalar struct{}

func (Scalar) MarshalGQL(io.Writer)         {}
func (*Scalar) UnmarshalGQL(any) error      { return nil }
func (Scalar) Value() (driver.Value, error) { return nil, nil }

func TestRType_Implements(t *testing.T) {
	type (
		marshaler   interface{ MarshalGQL(w io.Writer) }
		unmarshaler interface{ UnmarshalGQL(v any) error }
		codec       interface {
			marshaler
			unmarshaler
		}
	)
	var (
		codecType     = reflect.TypeOf((*codec)(nil)).Elem()
		marshalType   = reflect.TypeOf((*marshaler)(nil)).Elem()
		unmarshalType = reflect.TypeOf((*unmarshaler)(nil)).Elem()
	)
	for _, f := range []velox.Field{
		field.Enum("role").GoType(Admin),
		field.Other("scalar", &Scalar{}),
		field.Other("scalar", Scalar{}),
	} {
		fd := f.Descriptor()
		assert.True(t, fd.Info.RType.Implements(codecType))
		assert.True(t, fd.Info.RType.Implements(marshalType))
		assert.True(t, fd.Info.RType.Implements(unmarshalType))
	}
}

func TestTypeString(t *testing.T) {
	typ := field.TypeBool
	assert.Equal(t, "bool", typ.String())
	typ = field.TypeInvalid
	assert.Equal(t, "invalid", typ.String())
	typ = 21
	assert.Equal(t, "invalid", typ.String())
}

func TestTypeNumeric(t *testing.T) {
	typ := field.TypeBool
	assert.False(t, typ.Numeric())
	typ = field.TypeUint8
	assert.True(t, typ.Numeric())
}

func TestTypeValid(t *testing.T) {
	typ := field.TypeBool
	assert.True(t, typ.Valid())
	typ = 0
	assert.False(t, typ.Valid())
	typ = 21
	assert.False(t, typ.Valid())
}

func TestTypeConstName(t *testing.T) {
	typ := field.TypeJSON
	assert.Equal(t, "TypeJSON", typ.ConstName())
	typ = field.TypeInt
	assert.Equal(t, "TypeInt", typ.ConstName())
	typ = field.TypeInt64
	assert.Equal(t, "TypeInt64", typ.ConstName())
	typ = field.TypeOther
	assert.Equal(t, "TypeOther", typ.ConstName())
	typ = 21
	assert.Equal(t, "invalid", typ.ConstName())
}

func TestString_MinRuneLen(t *testing.T) {
	fd := field.String("name").MinRuneLen(5).Descriptor()
	assert.Len(t, fd.Validators, 1)

	err := fd.Validators[0].(func(string) error)("hello")
	assert.NoError(t, err)

	err = fd.Validators[0].(func(string) error)("hi")
	assert.ErrorContains(t, err, "less than minimum")

	err = fd.Validators[0].(func(string) error)("你好")
	assert.ErrorContains(t, err, "less than minimum")

	err = fd.Validators[0].(func(string) error)("你好世界！")
	assert.NoError(t, err)

	err = fd.Validators[0].(func(string) error)("")
	assert.Error(t, err)
}

func TestString_MaxRuneLen(t *testing.T) {
	fd := field.String("name").MaxRuneLen(5).Descriptor()
	assert.Len(t, fd.Validators, 1)

	err := fd.Validators[0].(func(string) error)("hello")
	assert.NoError(t, err)

	err = fd.Validators[0].(func(string) error)("hello world")
	assert.ErrorContains(t, err, "exceeds maximum")

	err = fd.Validators[0].(func(string) error)("你好世界你好")
	assert.ErrorContains(t, err, "exceeds maximum")

	err = fd.Validators[0].(func(string) error)("你好世界！")
	assert.NoError(t, err)

	err = fd.Validators[0].(func(string) error)("")
	assert.NoError(t, err)
}

// TestFieldDescriptorOptions tests various descriptor options using table-driven tests.
func TestFieldDescriptorOptions(t *testing.T) {
	tests := []struct {
		name     string
		build    func() *field.Descriptor
		validate func(t *testing.T, fd *field.Descriptor)
	}{
		{
			name: "optional_field",
			build: func() *field.Descriptor {
				return field.String("name").Optional().Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				assert.True(t, fd.Optional)
				assert.False(t, fd.Nillable)
			},
		},
		{
			name: "nillable_field",
			build: func() *field.Descriptor {
				return field.String("name").Nillable().Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				assert.True(t, fd.Nillable)
			},
		},
		{
			name: "immutable_field",
			build: func() *field.Descriptor {
				return field.String("name").Immutable().Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				assert.True(t, fd.Immutable)
			},
		},
		{
			name: "unique_field",
			build: func() *field.Descriptor {
				return field.String("email").Unique().Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				assert.True(t, fd.Unique)
			},
		},
		{
			name: "sensitive_field",
			build: func() *field.Descriptor {
				return field.String("password").Sensitive().Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				assert.True(t, fd.Sensitive)
			},
		},
		{
			name: "storage_key",
			build: func() *field.Descriptor {
				return field.String("userName").StorageKey("user_name").Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				assert.Equal(t, "user_name", fd.StorageKey)
			},
		},
		{
			name: "deprecated_field",
			build: func() *field.Descriptor {
				return field.String("old").Deprecated("use new_field instead").Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				assert.True(t, fd.Deprecated)
				assert.Equal(t, "use new_field instead", fd.DeprecatedReason)
			},
		},
		{
			name: "all_options_combined",
			build: func() *field.Descriptor {
				return field.String("name").
					Optional().
					Nillable().
					Comment("user name").
					StorageKey("user_name").
					Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				assert.True(t, fd.Optional)
				assert.True(t, fd.Nillable)
				assert.Equal(t, "user name", fd.Comment)
				assert.Equal(t, "user_name", fd.StorageKey)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd := tt.build()
			tt.validate(t, fd)
		})
	}
}

// TestIntegerFieldTypes tests all integer field types using table-driven approach.
func TestIntegerFieldTypes(t *testing.T) {
	tests := []struct {
		name         string
		build        func() velox.Field
		expectedType field.Type
	}{
		{"Int", func() velox.Field { return field.Int("val") }, field.TypeInt},
		{"Int8", func() velox.Field { return field.Int8("val") }, field.TypeInt8},
		{"Int16", func() velox.Field { return field.Int16("val") }, field.TypeInt16},
		{"Int32", func() velox.Field { return field.Int32("val") }, field.TypeInt32},
		{"Int64", func() velox.Field { return field.Int64("val") }, field.TypeInt64},
		{"Uint", func() velox.Field { return field.Uint("val") }, field.TypeUint},
		{"Uint8", func() velox.Field { return field.Uint8("val") }, field.TypeUint8},
		{"Uint16", func() velox.Field { return field.Uint16("val") }, field.TypeUint16},
		{"Uint32", func() velox.Field { return field.Uint32("val") }, field.TypeUint32},
		{"Uint64", func() velox.Field { return field.Uint64("val") }, field.TypeUint64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd := tt.build().Descriptor()
			assert.Equal(t, tt.expectedType, fd.Info.Type)
			assert.Equal(t, "val", fd.Name)
		})
	}
}

// TestNumericValidators tests numeric field validators.
func TestNumericValidators(t *testing.T) {
	tests := []struct {
		name      string
		build     func() *field.Descriptor
		testValue int
		expectErr bool
	}{
		{
			name:      "positive_valid",
			build:     func() *field.Descriptor { return field.Int("n").Positive().Descriptor() },
			testValue: 5,
			expectErr: false,
		},
		{
			name:      "positive_invalid",
			build:     func() *field.Descriptor { return field.Int("n").Positive().Descriptor() },
			testValue: -1,
			expectErr: true,
		},
		{
			name:      "positive_zero_invalid",
			build:     func() *field.Descriptor { return field.Int("n").Positive().Descriptor() },
			testValue: 0,
			expectErr: true,
		},
		{
			name:      "negative_valid",
			build:     func() *field.Descriptor { return field.Int("n").Negative().Descriptor() },
			testValue: -5,
			expectErr: false,
		},
		{
			name:      "negative_invalid",
			build:     func() *field.Descriptor { return field.Int("n").Negative().Descriptor() },
			testValue: 1,
			expectErr: true,
		},
		{
			name:      "nonnegative_valid_positive",
			build:     func() *field.Descriptor { return field.Int("n").NonNegative().Descriptor() },
			testValue: 5,
			expectErr: false,
		},
		{
			name:      "nonnegative_valid_zero",
			build:     func() *field.Descriptor { return field.Int("n").NonNegative().Descriptor() },
			testValue: 0,
			expectErr: false,
		},
		{
			name:      "nonnegative_invalid",
			build:     func() *field.Descriptor { return field.Int("n").NonNegative().Descriptor() },
			testValue: -1,
			expectErr: true,
		},
		{
			name:      "min_valid",
			build:     func() *field.Descriptor { return field.Int("n").Min(10).Descriptor() },
			testValue: 15,
			expectErr: false,
		},
		{
			name:      "min_boundary",
			build:     func() *field.Descriptor { return field.Int("n").Min(10).Descriptor() },
			testValue: 10,
			expectErr: false,
		},
		{
			name:      "min_invalid",
			build:     func() *field.Descriptor { return field.Int("n").Min(10).Descriptor() },
			testValue: 5,
			expectErr: true,
		},
		{
			name:      "max_valid",
			build:     func() *field.Descriptor { return field.Int("n").Max(100).Descriptor() },
			testValue: 50,
			expectErr: false,
		},
		{
			name:      "max_boundary",
			build:     func() *field.Descriptor { return field.Int("n").Max(100).Descriptor() },
			testValue: 100,
			expectErr: false,
		},
		{
			name:      "max_invalid",
			build:     func() *field.Descriptor { return field.Int("n").Max(100).Descriptor() },
			testValue: 150,
			expectErr: true,
		},
		{
			name:      "range_valid",
			build:     func() *field.Descriptor { return field.Int("n").Range(10, 100).Descriptor() },
			testValue: 50,
			expectErr: false,
		},
		{
			name:      "range_lower_boundary",
			build:     func() *field.Descriptor { return field.Int("n").Range(10, 100).Descriptor() },
			testValue: 10,
			expectErr: false,
		},
		{
			name:      "range_upper_boundary",
			build:     func() *field.Descriptor { return field.Int("n").Range(10, 100).Descriptor() },
			testValue: 100,
			expectErr: false,
		},
		{
			name:      "range_invalid_low",
			build:     func() *field.Descriptor { return field.Int("n").Range(10, 100).Descriptor() },
			testValue: 5,
			expectErr: true,
		},
		{
			name:      "range_invalid_high",
			build:     func() *field.Descriptor { return field.Int("n").Range(10, 100).Descriptor() },
			testValue: 150,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd := tt.build()
			require.Len(t, fd.Validators, 1)
			validator := fd.Validators[0].(func(int) error)
			err := validator(tt.testValue)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestStringValidators tests string field validators.
func TestStringValidators(t *testing.T) {
	tests := []struct {
		name      string
		build     func() *field.Descriptor
		testValue string
		expectErr bool
	}{
		{
			name:      "notempty_valid",
			build:     func() *field.Descriptor { return field.String("s").NotEmpty().Descriptor() },
			testValue: "hello",
			expectErr: false,
		},
		{
			name:      "notempty_invalid",
			build:     func() *field.Descriptor { return field.String("s").NotEmpty().Descriptor() },
			testValue: "",
			expectErr: true,
		},
		{
			name:      "minlen_valid",
			build:     func() *field.Descriptor { return field.String("s").MinLen(3).Descriptor() },
			testValue: "hello",
			expectErr: false,
		},
		{
			name:      "minlen_boundary",
			build:     func() *field.Descriptor { return field.String("s").MinLen(3).Descriptor() },
			testValue: "abc",
			expectErr: false,
		},
		{
			name:      "minlen_invalid",
			build:     func() *field.Descriptor { return field.String("s").MinLen(3).Descriptor() },
			testValue: "ab",
			expectErr: true,
		},
		{
			name:      "maxlen_valid",
			build:     func() *field.Descriptor { return field.String("s").MaxLen(10).Descriptor() },
			testValue: "hello",
			expectErr: false,
		},
		{
			name:      "maxlen_boundary",
			build:     func() *field.Descriptor { return field.String("s").MaxLen(5).Descriptor() },
			testValue: "hello",
			expectErr: false,
		},
		{
			name:      "maxlen_invalid",
			build:     func() *field.Descriptor { return field.String("s").MaxLen(5).Descriptor() },
			testValue: "hello world",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd := tt.build()
			require.Len(t, fd.Validators, 1)
			validator := fd.Validators[0].(func(string) error)
			err := validator(tt.testValue)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestFieldDeprecation tests the Deprecated method.
func TestFieldDeprecation(t *testing.T) {
	tests := []struct {
		name     string
		build    func() *field.Descriptor
		validate func(t *testing.T, fd *field.Descriptor)
	}{
		{
			name: "deprecated_with_reason",
			build: func() *field.Descriptor {
				return field.String("old").Deprecated("use new_field instead").Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				assert.True(t, fd.Deprecated)
				assert.Equal(t, "use new_field instead", fd.DeprecatedReason)
			},
		},
		{
			name: "deprecated_without_reason",
			build: func() *field.Descriptor {
				return field.String("old").Deprecated().Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				assert.True(t, fd.Deprecated)
				assert.Empty(t, fd.DeprecatedReason)
			},
		},
		{
			name: "deprecated_time_field",
			build: func() *field.Descriptor {
				return field.Time("deleted_at").Deprecated("no longer used").Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				assert.True(t, fd.Deprecated)
				assert.Equal(t, "no longer used", fd.DeprecatedReason)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd := tt.build()
			tt.validate(t, fd)
		})
	}
}

// TestEnumFieldVariants tests various enum field configurations.
func TestEnumFieldVariants(t *testing.T) {
	tests := []struct {
		name     string
		build    func() *field.Descriptor
		validate func(t *testing.T, fd *field.Descriptor)
	}{
		{
			name: "simple_values",
			build: func() *field.Descriptor {
				return field.Enum("status").Values("active", "inactive", "pending").Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				require.Len(t, fd.Enums, 3)
				assert.Equal(t, "active", fd.Enums[0].V)
				assert.Equal(t, "inactive", fd.Enums[1].V)
				assert.Equal(t, "pending", fd.Enums[2].V)
			},
		},
		{
			name: "named_values",
			build: func() *field.Descriptor {
				return field.Enum("priority").
					NamedValues("Low", "low", "Medium", "medium", "High", "high").
					Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				require.Len(t, fd.Enums, 3)
				assert.Equal(t, "Low", fd.Enums[0].N)
				assert.Equal(t, "low", fd.Enums[0].V)
				assert.Equal(t, "Medium", fd.Enums[1].N)
				assert.Equal(t, "medium", fd.Enums[1].V)
				assert.Equal(t, "High", fd.Enums[2].N)
				assert.Equal(t, "high", fd.Enums[2].V)
			},
		},
		{
			name: "enum_with_default",
			build: func() *field.Descriptor {
				return field.Enum("status").Values("active", "inactive").Default("active").Descriptor()
			},
			validate: func(t *testing.T, fd *field.Descriptor) {
				assert.Equal(t, "active", fd.Default)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd := tt.build()
			assert.NoError(t, fd.Err)
			tt.validate(t, fd)
		})
	}
}

// TestFieldTypeInfo tests type information methods.
func TestFieldTypeInfo(t *testing.T) {
	tests := []struct {
		name     string
		typ      field.Type
		numeric  bool
		valid    bool
		constNam string
	}{
		{"TypeBool", field.TypeBool, false, true, "TypeBool"},
		{"TypeInt", field.TypeInt, true, true, "TypeInt"},
		{"TypeInt8", field.TypeInt8, true, true, "TypeInt8"},
		{"TypeInt16", field.TypeInt16, true, true, "TypeInt16"},
		{"TypeInt32", field.TypeInt32, true, true, "TypeInt32"},
		{"TypeInt64", field.TypeInt64, true, true, "TypeInt64"},
		{"TypeUint", field.TypeUint, true, true, "TypeUint"},
		{"TypeUint8", field.TypeUint8, true, true, "TypeUint8"},
		{"TypeUint16", field.TypeUint16, true, true, "TypeUint16"},
		{"TypeUint32", field.TypeUint32, true, true, "TypeUint32"},
		{"TypeUint64", field.TypeUint64, true, true, "TypeUint64"},
		{"TypeFloat32", field.TypeFloat32, true, true, "TypeFloat32"},
		{"TypeFloat64", field.TypeFloat64, true, true, "TypeFloat64"},
		{"TypeString", field.TypeString, false, true, "TypeString"},
		{"TypeTime", field.TypeTime, false, true, "TypeTime"},
		{"TypeBytes", field.TypeBytes, false, true, "TypeBytes"},
		{"TypeJSON", field.TypeJSON, false, true, "TypeJSON"},
		{"TypeUUID", field.TypeUUID, false, true, "TypeUUID"},
		{"TypeEnum", field.TypeEnum, false, true, "TypeEnum"},
		{"TypeOther", field.TypeOther, false, true, "TypeOther"},
		{"TypeInvalid", field.TypeInvalid, false, false, "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.numeric, tt.typ.Numeric(), "Numeric() mismatch")
			assert.Equal(t, tt.valid, tt.typ.Valid(), "Valid() mismatch")
			assert.Equal(t, tt.constNam, tt.typ.ConstName(), "ConstName() mismatch")
		})
	}
}

// TestNumericBuilderMethods tests additional intBuilder methods.
func TestNumericBuilderMethods(t *testing.T) {
	// Test Unique
	fd := field.Int("priority").Unique().Descriptor()
	assert.True(t, fd.Unique)

	// Test Optional
	fd = field.Int("count").Optional().Descriptor()
	assert.True(t, fd.Optional)

	// Test Immutable
	fd = field.Int("version").Immutable().Descriptor()
	assert.True(t, fd.Immutable)

	// Test StructTag
	fd = field.Int("score").StructTag(`json:"score,omitempty"`).Descriptor()
	assert.Equal(t, `json:"score,omitempty"`, fd.Tag)

	// Test Validate
	validator := func(v int) error {
		if v < 0 || v > 100 {
			return fmt.Errorf("score must be between 0 and 100")
		}
		return nil
	}
	fd = field.Int("score").Validate(validator).Descriptor()
	require.Len(t, fd.Validators, 1)

	// Test StorageKey
	fd = field.Int("userId").StorageKey("user_id").Descriptor()
	assert.Equal(t, "user_id", fd.StorageKey)

	// Test Annotations
	ann := &field.Annotation{StructTag: map[string]string{"int": `json:"int"`}}
	fd = field.Int("value").Annotations(ann).Descriptor()
	require.Len(t, fd.Annotations, 1)

	// Test Deprecated
	fd = field.Int("old_count").Deprecated("use new_count").Descriptor()
	assert.True(t, fd.Deprecated)
	assert.Equal(t, "use new_count", fd.DeprecatedReason)

	// Test Int64 with same methods
	fd = field.Int64("bignum").
		Unique().
		Optional().
		Immutable().
		StorageKey("big_num").
		Descriptor()
	assert.True(t, fd.Unique)
	assert.True(t, fd.Optional)
	assert.True(t, fd.Immutable)
	assert.Equal(t, "big_num", fd.StorageKey)

	// Test Float with same methods
	fd = field.Float("rate").
		Optional().
		Immutable().
		StorageKey("rate_value").
		Descriptor()
	assert.True(t, fd.Optional)
	assert.True(t, fd.Immutable)
	assert.Equal(t, "rate_value", fd.StorageKey)
}

// TestUintBuilderMethods tests uintBuilder methods comprehensively.
func TestUintBuilderMethods(t *testing.T) {
	// Test basic
	fd := field.Uint("count").Descriptor()
	assert.Equal(t, "count", fd.Name)
	assert.Equal(t, field.TypeUint, fd.Info.Type)

	// Test Unique
	fd = field.Uint("id").Unique().Descriptor()
	assert.True(t, fd.Unique)

	// Test Range
	fd = field.Uint("age").Range(0, 150).Descriptor()
	require.Len(t, fd.Validators, 1)
	v := fd.Validators[0].(func(uint) error)
	assert.NoError(t, v(50))
	assert.Error(t, v(200))

	// Test Min
	fd = field.Uint("count").Min(10).Descriptor()
	require.Len(t, fd.Validators, 1)
	v = fd.Validators[0].(func(uint) error)
	assert.NoError(t, v(10))
	assert.Error(t, v(5))

	// Test Max
	fd = field.Uint("count").Max(100).Descriptor()
	require.Len(t, fd.Validators, 1)
	v = fd.Validators[0].(func(uint) error)
	assert.NoError(t, v(50))
	assert.Error(t, v(150))

	// Test Positive
	fd = field.Uint("count").Positive().Descriptor()
	require.Len(t, fd.Validators, 1)

	// Test Default
	fd = field.Uint("count").Default(10).Descriptor()
	assert.Equal(t, uint(10), fd.Default)

	// Test DefaultFunc
	fd = field.Uint("count").DefaultFunc(func() uint { return 100 }).Descriptor()
	assert.NotNil(t, fd.Default)

	// Test UpdateDefault
	fd = field.Uint("count").UpdateDefault(func() uint { return 200 }).Descriptor()
	assert.NotNil(t, fd.UpdateDefault)

	// Test Nillable
	fd = field.Uint("count").Nillable().Descriptor()
	assert.True(t, fd.Nillable)

	// Test Comment
	fd = field.Uint("count").Comment("count field").Descriptor()
	assert.Equal(t, "count field", fd.Comment)

	// Test Optional
	fd = field.Uint("count").Optional().Descriptor()
	assert.True(t, fd.Optional)

	// Test Immutable
	fd = field.Uint("count").Immutable().Descriptor()
	assert.True(t, fd.Immutable)

	// Test StructTag
	fd = field.Uint("count").StructTag(`json:"count"`).Descriptor()
	assert.Equal(t, `json:"count"`, fd.Tag)

	// Test Validate
	fd = field.Uint("count").Validate(func(v uint) error { return nil }).Descriptor()
	require.Len(t, fd.Validators, 1)

	// Test StorageKey
	fd = field.Uint("itemCount").StorageKey("item_count").Descriptor()
	assert.Equal(t, "item_count", fd.StorageKey)

	// Test SchemaType
	fd = field.Uint("count").
		SchemaType(map[string]string{dialect.Postgres: "bigint"}).
		Descriptor()
	assert.Equal(t, "bigint", fd.SchemaType[dialect.Postgres])

	// Test Annotations
	ann := &field.Annotation{StructTag: map[string]string{"uint": `json:"uint"`}}
	fd = field.Uint("count").Annotations(ann).Descriptor()
	require.Len(t, fd.Annotations, 1)

	// Test Deprecated
	fd = field.Uint("old").Deprecated("use new").Descriptor()
	assert.True(t, fd.Deprecated)
	assert.Equal(t, "use new", fd.DeprecatedReason)
}

// TestFloat64BuilderMethods tests float64Builder methods comprehensively.
func TestFloat64BuilderMethods(t *testing.T) {
	// Test basic
	fd := field.Float("rate").Descriptor()
	assert.Equal(t, "rate", fd.Name)
	assert.Equal(t, field.TypeFloat64, fd.Info.Type)

	// Test Unique
	fd = field.Float("rate").Unique().Descriptor()
	assert.True(t, fd.Unique)

	// Test Range
	fd = field.Float("rate").Range(0.0, 1.0).Descriptor()
	require.Len(t, fd.Validators, 1)
	v := fd.Validators[0].(func(float64) error)
	assert.NoError(t, v(0.5))
	assert.Error(t, v(2.0))

	// Test Min
	fd = field.Float("rate").Min(0.1).Descriptor()
	require.Len(t, fd.Validators, 1)
	v = fd.Validators[0].(func(float64) error)
	assert.NoError(t, v(0.5))
	assert.Error(t, v(0.05))

	// Test Max
	fd = field.Float("rate").Max(1.0).Descriptor()
	require.Len(t, fd.Validators, 1)
	v = fd.Validators[0].(func(float64) error)
	assert.NoError(t, v(0.5))
	assert.Error(t, v(1.5))

	// Test Positive
	fd = field.Float("amount").Positive().Descriptor()
	require.Len(t, fd.Validators, 1)
	v = fd.Validators[0].(func(float64) error)
	assert.NoError(t, v(1.0))
	assert.Error(t, v(0.0))
	assert.Error(t, v(-1.0))

	// Test Negative
	fd = field.Float("temp").Negative().Descriptor()
	require.Len(t, fd.Validators, 1)
	v = fd.Validators[0].(func(float64) error)
	assert.NoError(t, v(-1.0))
	assert.Error(t, v(0.0))
	assert.Error(t, v(1.0))

	// Test NonNegative
	fd = field.Float("rate").NonNegative().Descriptor()
	require.Len(t, fd.Validators, 1)

	// Test Default
	fd = field.Float("rate").Default(0.5).Descriptor()
	assert.Equal(t, 0.5, fd.Default)

	// Test DefaultFunc
	fd = field.Float("rate").DefaultFunc(func() float64 { return 0.5 }).Descriptor()
	assert.NotNil(t, fd.Default)

	// Test UpdateDefault
	fd = field.Float("rate").UpdateDefault(func() float64 { return 0.75 }).Descriptor()
	assert.NotNil(t, fd.UpdateDefault)

	// Test Nillable
	fd = field.Float("rate").Nillable().Descriptor()
	assert.True(t, fd.Nillable)

	// Test Comment
	fd = field.Float("rate").Comment("rate field").Descriptor()
	assert.Equal(t, "rate field", fd.Comment)

	// Test Optional
	fd = field.Float("rate").Optional().Descriptor()
	assert.True(t, fd.Optional)

	// Test Immutable
	fd = field.Float("rate").Immutable().Descriptor()
	assert.True(t, fd.Immutable)

	// Test StructTag
	fd = field.Float("rate").StructTag(`json:"rate"`).Descriptor()
	assert.Equal(t, `json:"rate"`, fd.Tag)

	// Test Validate
	fd = field.Float("rate").Validate(func(v float64) error { return nil }).Descriptor()
	require.Len(t, fd.Validators, 1)

	// Test StorageKey
	fd = field.Float("interestRate").StorageKey("interest_rate").Descriptor()
	assert.Equal(t, "interest_rate", fd.StorageKey)

	// Test SchemaType
	fd = field.Float("amount").
		SchemaType(map[string]string{dialect.Postgres: "decimal(10,2)"}).
		Descriptor()
	assert.Equal(t, "decimal(10,2)", fd.SchemaType[dialect.Postgres])

	// Test Annotations
	ann := &field.Annotation{StructTag: map[string]string{"float": `json:"float"`}}
	fd = field.Float("rate").Annotations(ann).Descriptor()
	require.Len(t, fd.Annotations, 1)

	// Test Deprecated
	fd = field.Float("old").Deprecated("use new").Descriptor()
	assert.True(t, fd.Deprecated)
	assert.Equal(t, "use new", fd.DeprecatedReason)
}

// TestFloat32BuilderMethods tests float32Builder methods.
func TestFloat32BuilderMethods(t *testing.T) {
	// Test basic
	fd := field.Float32("rate").Descriptor()
	assert.Equal(t, field.TypeFloat32, fd.Info.Type)

	// Test Unique
	fd = field.Float32("rate").Unique().Descriptor()
	assert.True(t, fd.Unique)

	// Test Range
	fd = field.Float32("rate").Range(0.0, 1.0).Descriptor()
	require.Len(t, fd.Validators, 1)

	// Test Min/Max/Positive/Negative/NonNegative
	fd = field.Float32("rate").Min(0.1).Descriptor()
	require.Len(t, fd.Validators, 1)
	fd = field.Float32("rate").Max(1.0).Descriptor()
	require.Len(t, fd.Validators, 1)
	fd = field.Float32("rate").Positive().Descriptor()
	require.Len(t, fd.Validators, 1)
	fd = field.Float32("rate").Negative().Descriptor()
	require.Len(t, fd.Validators, 1)
	fd = field.Float32("rate").NonNegative().Descriptor()
	require.Len(t, fd.Validators, 1)

	// Test Default/DefaultFunc/UpdateDefault
	fd = field.Float32("rate").Default(0.5).Descriptor()
	assert.Equal(t, float32(0.5), fd.Default)
	fd = field.Float32("rate").DefaultFunc(func() float32 { return 0.5 }).Descriptor()
	assert.NotNil(t, fd.Default)
	fd = field.Float32("rate").UpdateDefault(func() float32 { return 0.75 }).Descriptor()
	assert.NotNil(t, fd.UpdateDefault)

	// Test all other methods
	fd = field.Float32("rate").Nillable().Descriptor()
	assert.True(t, fd.Nillable)
	fd = field.Float32("rate").Comment("rate").Descriptor()
	assert.Equal(t, "rate", fd.Comment)
	fd = field.Float32("rate").Optional().Descriptor()
	assert.True(t, fd.Optional)
	fd = field.Float32("rate").Immutable().Descriptor()
	assert.True(t, fd.Immutable)
	fd = field.Float32("rate").StructTag(`json:"rate"`).Descriptor()
	assert.Equal(t, `json:"rate"`, fd.Tag)
	fd = field.Float32("rate").Validate(func(v float32) error { return nil }).Descriptor()
	require.Len(t, fd.Validators, 1)
	fd = field.Float32("rate").StorageKey("rate_value").Descriptor()
	assert.Equal(t, "rate_value", fd.StorageKey)
	fd = field.Float32("rate").SchemaType(map[string]string{dialect.Postgres: "real"}).Descriptor()
	assert.Equal(t, "real", fd.SchemaType[dialect.Postgres])
	ann := &field.Annotation{StructTag: map[string]string{"f32": `json:"f32"`}}
	fd = field.Float32("rate").Annotations(ann).Descriptor()
	require.Len(t, fd.Annotations, 1)
	fd = field.Float32("old").Deprecated("use new").Descriptor()
	assert.True(t, fd.Deprecated)
}

// TestStringBuilderStructTag tests the stringBuilder StructTag method.
func TestStringBuilderStructTag(t *testing.T) {
	fd := field.String("name").StructTag(`json:"name,omitempty" xml:"name"`).Descriptor()
	assert.Equal(t, `json:"name,omitempty" xml:"name"`, fd.Tag)
}

// TestBytesBuilderAllMethods tests all bytesBuilder methods.
func TestBytesBuilderAllMethods(t *testing.T) {
	// Test Nillable
	fd := field.Bytes("data").Nillable().Descriptor()
	assert.True(t, fd.Nillable)

	// Test Optional
	fd = field.Bytes("data").Optional().Descriptor()
	assert.True(t, fd.Optional)

	// Test Sensitive
	fd = field.Bytes("secret").Sensitive().Descriptor()
	assert.True(t, fd.Sensitive)

	// Test Immutable
	fd = field.Bytes("hash").Immutable().Descriptor()
	assert.True(t, fd.Immutable)

	// Test StorageKey
	fd = field.Bytes("blobData").StorageKey("blob_data").Descriptor()
	assert.Equal(t, "blob_data", fd.StorageKey)

	// Test Deprecated
	fd = field.Bytes("old_data").Deprecated("use new_data").Descriptor()
	assert.True(t, fd.Deprecated)
	assert.Equal(t, "use new_data", fd.DeprecatedReason)
}

// TestBoolBuilderDeprecated tests boolBuilder Deprecated method.
func TestBoolBuilderDeprecated(t *testing.T) {
	fd := field.Bool("old_flag").Deprecated("use new_flag").Descriptor()
	assert.True(t, fd.Deprecated)
	assert.Equal(t, "use new_flag", fd.DeprecatedReason)
}

// TestJSONBuilderDeprecated tests jsonBuilder Deprecated method.
func TestJSONBuilderDeprecated(t *testing.T) {
	fd := field.JSON("old_meta", map[string]any{}).Deprecated().Descriptor()
	assert.True(t, fd.Deprecated)
}

// TestEnumBuilderDeprecated tests enumBuilder Deprecated method.
func TestEnumBuilderDeprecated(t *testing.T) {
	fd := field.Enum("old_status").Values("a", "b").Deprecated("use new_status").Descriptor()
	assert.True(t, fd.Deprecated)
	assert.Equal(t, "use new_status", fd.DeprecatedReason)
}

// TestUUIDBuilderDeprecated tests uuidBuilder Deprecated method.
func TestUUIDBuilderDeprecated(t *testing.T) {
	fd := field.UUID("old_id", uuid.UUID{}).Deprecated("use new_id").Descriptor()
	assert.True(t, fd.Deprecated)
	assert.Equal(t, "use new_id", fd.DeprecatedReason)
}

// TestOtherBuilderDeprecated tests otherBuilder Deprecated method.
func TestOtherBuilderDeprecated(t *testing.T) {
	fd := field.Other("old_link", &custom{}).
		SchemaType(map[string]string{dialect.Postgres: "text"}).
		Deprecated("use new_link").
		Descriptor()
	assert.True(t, fd.Deprecated)
	assert.Equal(t, "use new_link", fd.DeprecatedReason)
}

// TestOtherBuilderComment tests otherBuilder Comment method.
func TestOtherBuilderComment(t *testing.T) {
	fd := field.Other("link", &custom{}).
		SchemaType(map[string]string{dialect.Postgres: "text"}).
		Comment("custom link field").
		Descriptor()
	assert.Equal(t, "custom link field", fd.Comment)
}

// TestNamedValuesOddCount tests NamedValues with odd argument count.
func TestNamedValuesOddCount(t *testing.T) {
	fd := field.Enum("status").NamedValues("A", "a", "B").Descriptor()
	assert.Error(t, fd.Err)
	assert.Contains(t, fd.Err.Error(), "odd argument count")
}

// textMarshalType implements encoding.TextMarshaler and TextUnmarshaler
type textMarshalType struct {
	Data string
}

func (t *textMarshalType) MarshalText() ([]byte, error) {
	return []byte(t.Data), nil
}

func (t *textMarshalType) UnmarshalText(text []byte) error {
	t.Data = string(text)
	return nil
}

// TestTextValueScanner tests TextValueScanner methods.
func TestTextValueScanner(t *testing.T) {
	scanner := field.TextValueScanner[*textMarshalType]{}

	// Test Value
	val, err := scanner.Value(&textMarshalType{Data: "hello"})
	assert.NoError(t, err)
	assert.Equal(t, []byte("hello"), val)

	// Test ScanValue
	sv := scanner.ScanValue()
	assert.NotNil(t, sv)
	_, ok := sv.(*sql.NullString)
	assert.True(t, ok)

	// Test FromValue with valid string
	ns := &sql.NullString{String: "world", Valid: true}
	result, err := scanner.FromValue(ns)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "world", result.Data)

	// Test FromValue with invalid input type
	_, err = scanner.FromValue("invalid")
	assert.Error(t, err)

	// Test FromValue with invalid (null) string
	ns = &sql.NullString{Valid: false}
	result, err = scanner.FromValue(ns)
	assert.NoError(t, err)
	assert.Empty(t, result.Data, "result should be empty for null string")
}

// binaryMarshalType implements encoding.BinaryMarshaler and BinaryUnmarshaler
type binaryMarshalType struct {
	Data []byte
}

func (b *binaryMarshalType) MarshalBinary() ([]byte, error) {
	return b.Data, nil
}

func (b *binaryMarshalType) UnmarshalBinary(data []byte) error {
	b.Data = data
	return nil
}

// TestBinaryValueScanner tests BinaryValueScanner methods.
func TestBinaryValueScanner(t *testing.T) {
	scanner := field.BinaryValueScanner[*binaryMarshalType]{}

	// Test Value
	val, err := scanner.Value(&binaryMarshalType{Data: []byte("hello")})
	assert.NoError(t, err)
	assert.Equal(t, []byte("hello"), val)

	// Test ScanValue
	sv := scanner.ScanValue()
	assert.NotNil(t, sv)
	_, ok := sv.(*sql.NullString)
	assert.True(t, ok)

	// Test FromValue with valid string
	ns := &sql.NullString{String: "world", Valid: true}
	result, err := scanner.FromValue(ns)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, []byte("world"), result.Data)

	// Test FromValue with invalid input type
	_, err = scanner.FromValue("invalid")
	assert.Error(t, err)

	// Test FromValue with null string
	ns = &sql.NullString{Valid: false}
	_, err = scanner.FromValue(ns)
	assert.NoError(t, err)
}

// TestValueScannerFunc tests ValueScannerFunc methods.
func TestValueScannerFunc(t *testing.T) {
	vsf := field.ValueScannerFunc[string, *sql.NullString]{
		V: func(s string) (driver.Value, error) {
			return s, nil
		},
		S: func(ns *sql.NullString) (string, error) {
			if !ns.Valid {
				return "", nil
			}
			return ns.String, nil
		},
	}

	// Test Value
	val, err := vsf.Value("hello")
	assert.NoError(t, err)
	assert.Equal(t, "hello", val)

	// Test ScanValue
	sv := vsf.ScanValue()
	assert.NotNil(t, sv)

	// Test FromValue with valid string
	ns := &sql.NullString{String: "world", Valid: true}
	result, err := vsf.FromValue(ns)
	assert.NoError(t, err)
	assert.Equal(t, "world", result)

	// Test FromValue with invalid type
	_, err = vsf.FromValue("not a NullString")
	assert.Error(t, err)

	// Test FromValue with null value
	ns = &sql.NullString{Valid: false}
	result, err = vsf.FromValue(ns)
	assert.NoError(t, err)
	assert.Equal(t, "", result)
}

// BenchmarkFieldBuilder benchmarks field builder performance.
func BenchmarkFieldBuilder(b *testing.B) {
	b.Run("String_simple", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			field.String("name").Descriptor()
		}
	})

	b.Run("String_with_validators", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			field.String("name").NotEmpty().MinLen(3).MaxLen(100).Descriptor()
		}
	})

	b.Run("String_full_config", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			field.String("name").
				Optional().
				Nillable().
				Unique().
				Comment("user name").
				StorageKey("user_name").
				Descriptor()
		}
	})

	b.Run("Int_simple", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			field.Int("age").Descriptor()
		}
	})

	b.Run("Int_with_validators", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			field.Int("age").Positive().Min(0).Max(150).Descriptor()
		}
	})

	b.Run("Enum_simple", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			field.Enum("status").Values("active", "inactive", "pending").Descriptor()
		}
	})

	b.Run("Time_with_default", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			field.Time("created_at").Default(time.Now).Immutable().Descriptor()
		}
	})

	b.Run("UUID_with_default", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			field.UUID("id", uuid.UUID{}).Default(uuid.New).Descriptor()
		}
	})

	b.Run("JSON_complex", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			field.JSON("metadata", map[string]any{}).Optional().Descriptor()
		}
	})
}

// TestText tests the Text field builder.
func TestText(t *testing.T) {
	fd := field.Text("content").
		Optional().
		Nillable().
		Comment("large text content").
		Descriptor()
	assert.Equal(t, "content", fd.Name)
	assert.Equal(t, field.TypeString, fd.Info.Type)
	assert.True(t, fd.Optional)
	assert.True(t, fd.Nillable)
	assert.Equal(t, "large text content", fd.Comment)
	// Text fields have MaxInt32 size for unlimited length
	assert.Equal(t, math.MaxInt32, fd.Size)
}

// TestStringDefault tests string field with literal default.
func TestStringDefault(t *testing.T) {
	fd := field.String("status").
		Default("pending").
		Descriptor()
	assert.Equal(t, "status", fd.Name)
	assert.Equal(t, "pending", fd.Default)
	assert.NoError(t, fd.Err)

	// Test with empty string default
	fd = field.String("empty").
		Default("").
		Descriptor()
	assert.Equal(t, "", fd.Default)
	assert.NoError(t, fd.Err)
}

// TestStringSchemaType tests string field with SchemaType.
func TestStringSchemaType(t *testing.T) {
	fd := field.String("name").
		SchemaType(map[string]string{
			dialect.MySQL:    "varchar(255)",
			dialect.Postgres: "text",
		}).
		Descriptor()
	assert.Equal(t, "varchar(255)", fd.SchemaType[dialect.MySQL])
	assert.Equal(t, "text", fd.SchemaType[dialect.Postgres])
}

// TestStringAnnotations tests string field with annotations.
func TestStringAnnotations(t *testing.T) {
	ann := &field.Annotation{
		StructTag: map[string]string{"name": `json:"name"`},
	}
	fd := field.String("name").
		Annotations(ann).
		Descriptor()
	require.Len(t, fd.Annotations, 1)
	assert.Equal(t, ann, fd.Annotations[0])
}

// TestTimeBuilderMethods tests all timeBuilder methods.
func TestTimeBuilderMethods(t *testing.T) {
	now := time.Now()

	// Test Nillable
	fd := field.Time("deleted_at").Nillable().Descriptor()
	assert.True(t, fd.Nillable)

	// Test Optional
	fd = field.Time("updated_at").Optional().Descriptor()
	assert.True(t, fd.Optional)

	// Test Immutable
	fd = field.Time("created_at").Immutable().Descriptor()
	assert.True(t, fd.Immutable)

	// Test StructTag
	fd = field.Time("timestamp").StructTag(`json:"ts,omitempty"`).Descriptor()
	assert.Equal(t, `json:"ts,omitempty"`, fd.Tag)

	// Test StorageKey
	fd = field.Time("createdAt").StorageKey("created_at").Descriptor()
	assert.Equal(t, "created_at", fd.StorageKey)

	// Test Annotations
	ann := &field.Annotation{StructTag: map[string]string{"time": `json:"time"`}}
	fd = field.Time("event_time").Annotations(ann).Descriptor()
	require.Len(t, fd.Annotations, 1)

	// Test SchemaType
	fd = field.Time("timestamp").
		SchemaType(map[string]string{
			dialect.MySQL: "datetime",
		}).
		Descriptor()
	assert.Equal(t, "datetime", fd.SchemaType[dialect.MySQL])

	// Test combined methods
	fd = field.Time("created_at").
		Default(func() time.Time { return now }).
		Immutable().
		Nillable().
		Optional().
		Comment("creation timestamp").
		StorageKey("created_at").
		Unique().
		Descriptor()
	assert.True(t, fd.Immutable)
	assert.True(t, fd.Nillable)
	assert.True(t, fd.Optional)
	assert.True(t, fd.Unique)
	assert.Equal(t, "creation timestamp", fd.Comment)
	assert.Equal(t, "created_at", fd.StorageKey)
}

// TestEnumBuilderMethods tests all enumBuilder methods.
func TestEnumBuilderMethods(t *testing.T) {
	// Test Nillable
	fd := field.Enum("status").Values("a", "b").Nillable().Descriptor()
	assert.True(t, fd.Nillable)

	// Test Optional
	fd = field.Enum("status").Values("a", "b").Optional().Descriptor()
	assert.True(t, fd.Optional)

	// Test Immutable
	fd = field.Enum("status").Values("a", "b").Immutable().Descriptor()
	assert.True(t, fd.Immutable)

	// Test StructTag
	fd = field.Enum("status").Values("a", "b").StructTag(`json:"status"`).Descriptor()
	assert.Equal(t, `json:"status"`, fd.Tag)

	// Test StorageKey
	fd = field.Enum("userStatus").Values("a", "b").StorageKey("user_status").Descriptor()
	assert.Equal(t, "user_status", fd.StorageKey)

	// Test Annotations
	ann := &field.Annotation{StructTag: map[string]string{"enum": `json:"enum"`}}
	fd = field.Enum("priority").Values("low", "high").Annotations(ann).Descriptor()
	require.Len(t, fd.Annotations, 1)

	// Test SchemaType
	fd = field.Enum("priority").
		Values("low", "high").
		SchemaType(map[string]string{
			dialect.Postgres: "priority_enum",
		}).
		Descriptor()
	assert.Equal(t, "priority_enum", fd.SchemaType[dialect.Postgres])

	// Test combined methods
	fd = field.Enum("status").
		Values("pending", "active", "inactive").
		Default("pending").
		Nillable().
		Optional().
		Immutable().
		Comment("user status").
		StorageKey("status").
		Descriptor()
	assert.Equal(t, "pending", fd.Default)
	assert.True(t, fd.Nillable)
	assert.True(t, fd.Optional)
	assert.True(t, fd.Immutable)
	assert.Equal(t, "user status", fd.Comment)
	assert.Equal(t, "status", fd.StorageKey)
}

// TestMatchValidator tests Match validator with non-matching strings.
func TestMatchValidator(t *testing.T) {
	re := regexp.MustCompile(`^[a-z]+$`)
	fd := field.String("code").Match(re).Descriptor()
	require.Len(t, fd.Validators, 1)

	validator := fd.Validators[0].(func(string) error)

	// Valid match
	err := validator("hello")
	assert.NoError(t, err)

	// Invalid match - contains uppercase
	err = validator("Hello")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not match pattern")

	// Invalid match - contains numbers
	err = validator("hello123")
	assert.Error(t, err)

	// Invalid match - empty string
	err = validator("")
	assert.Error(t, err)
}

// TestAnnotationIDAndName tests the annotation ID function and Name method.
func TestAnnotationIDAndName(t *testing.T) {
	// Test ID function with two fields
	ann := field.ID("user_id", "post_id")
	assert.Equal(t, []string{"user_id", "post_id"}, ann.ID)

	// Test ID function with additional fields
	ann = field.ID("a", "b", "c", "d")
	assert.Equal(t, []string{"a", "b", "c", "d"}, ann.ID)

	// Test Name method
	var a field.Annotation
	assert.Equal(t, "Fields", a.Name())
}

// TestAnnotationMerge tests the Annotation Merge method.
func TestAnnotationMerge(t *testing.T) {
	// Test merge with Annotation value
	a := field.Annotation{
		StructTag: map[string]string{"a": "1"},
	}
	b := field.Annotation{
		StructTag: map[string]string{"b": "2"},
		ID:        []string{"x", "y"},
	}
	merged := a.Merge(b).(field.Annotation)
	assert.Equal(t, "1", merged.StructTag["a"])
	assert.Equal(t, "2", merged.StructTag["b"])
	assert.Equal(t, []string{"x", "y"}, merged.ID)

	// Test merge with *Annotation pointer
	c := field.Annotation{}
	d := &field.Annotation{
		StructTag: map[string]string{"c": "3"},
	}
	merged = c.Merge(d).(field.Annotation)
	assert.Equal(t, "3", merged.StructTag["c"])

	// Test merge with nil pointer
	e := field.Annotation{StructTag: map[string]string{"e": "5"}}
	var nilAnn *field.Annotation
	merged = e.Merge(nilAnn).(field.Annotation)
	assert.Equal(t, "5", merged.StructTag["e"])

	// Test merge with unrelated annotation type
	f := field.Annotation{StructTag: map[string]string{"f": "6"}}
	merged = f.Merge(mockAnnotation{}).(field.Annotation)
	assert.Equal(t, "6", merged.StructTag["f"])
}

// TestBoolBuilderMethods tests additional boolBuilder methods.
func TestBoolBuilderMethods(t *testing.T) {
	// Test StructTag
	fd := field.Bool("active").StructTag(`json:"active,omitempty"`).Descriptor()
	assert.Equal(t, `json:"active,omitempty"`, fd.Tag)

	// Test StorageKey
	fd = field.Bool("isActive").StorageKey("is_active").Descriptor()
	assert.Equal(t, "is_active", fd.StorageKey)

	// Test Annotations
	ann := &field.Annotation{StructTag: map[string]string{"bool": `json:"bool"`}}
	fd = field.Bool("flag").Annotations(ann).Descriptor()
	require.Len(t, fd.Annotations, 1)

	// Test SchemaType
	fd = field.Bool("active").
		SchemaType(map[string]string{
			dialect.MySQL: "tinyint(1)",
		}).
		Descriptor()
	assert.Equal(t, "tinyint(1)", fd.SchemaType[dialect.MySQL])

	// Test Nillable
	fd = field.Bool("deleted").Nillable().Descriptor()
	assert.True(t, fd.Nillable)

	// Test Optional
	fd = field.Bool("verified").Optional().Descriptor()
	assert.True(t, fd.Optional)
}

// TestBytesBuilderMethods tests additional bytesBuilder methods.
func TestBytesBuilderMethods(t *testing.T) {
	// Test StructTag
	fd := field.Bytes("data").StructTag(`json:"-"`).Descriptor()
	assert.Equal(t, `json:"-"`, fd.Tag)

	// Test Annotations
	ann := &field.Annotation{StructTag: map[string]string{"bytes": `json:"bytes"`}}
	fd = field.Bytes("blob").Annotations(ann).Descriptor()
	require.Len(t, fd.Annotations, 1)

	// Test SchemaType
	fd = field.Bytes("data").
		SchemaType(map[string]string{
			dialect.MySQL: "mediumblob",
		}).
		Descriptor()
	assert.Equal(t, "mediumblob", fd.SchemaType[dialect.MySQL])

	// Test MinLen
	fd = field.Bytes("data").MinLen(10).Descriptor()
	require.Len(t, fd.Validators, 1)
	validator := fd.Validators[0].(func([]byte) error)
	assert.Error(t, validator([]byte("short")))
	assert.NoError(t, validator([]byte("long enough data")))

	// Test NotEmpty
	fd = field.Bytes("data").NotEmpty().Descriptor()
	require.Len(t, fd.Validators, 1)
	validator = fd.Validators[0].(func([]byte) error)
	assert.Error(t, validator([]byte{}))
	assert.NoError(t, validator([]byte("x")))
}

// TestJSONBuilderMethods tests additional jsonBuilder methods.
func TestJSONBuilderMethods(t *testing.T) {
	// Test StructTag
	fd := field.JSON("meta", map[string]any{}).StructTag(`json:"metadata"`).Descriptor()
	assert.Equal(t, `json:"metadata"`, fd.Tag)

	// Test StorageKey
	fd = field.JSON("userData", map[string]string{}).StorageKey("user_data").Descriptor()
	assert.Equal(t, "user_data", fd.StorageKey)

	// Test Annotations
	ann := &field.Annotation{StructTag: map[string]string{"json": `json:"json"`}}
	fd = field.JSON("config", map[string]any{}).Annotations(ann).Descriptor()
	require.Len(t, fd.Annotations, 1)

	// Test Immutable
	fd = field.JSON("snapshot", map[string]any{}).Immutable().Descriptor()
	assert.True(t, fd.Immutable)

	// Test Sensitive
	fd = field.JSON("secrets", map[string]string{}).Sensitive().Descriptor()
	assert.True(t, fd.Sensitive)

	// Test SchemaType
	fd = field.JSON("data", map[string]any{}).
		SchemaType(map[string]string{
			dialect.Postgres: "jsonb",
		}).
		Descriptor()
	assert.Equal(t, "jsonb", fd.SchemaType[dialect.Postgres])
}

// TestUUIDBuilderMethods tests additional uuidBuilder methods.
func TestUUIDBuilderMethods(t *testing.T) {
	// Test StructTag
	fd := field.UUID("id", uuid.UUID{}).StructTag(`json:"id"`).Descriptor()
	assert.Equal(t, `json:"id"`, fd.Tag)

	// Test StorageKey
	fd = field.UUID("userId", uuid.UUID{}).StorageKey("user_id").Descriptor()
	assert.Equal(t, "user_id", fd.StorageKey)

	// Test Annotations
	ann := &field.Annotation{StructTag: map[string]string{"uuid": `json:"uuid"`}}
	fd = field.UUID("ref", uuid.UUID{}).Annotations(ann).Descriptor()
	require.Len(t, fd.Annotations, 1)

	// Test SchemaType
	fd = field.UUID("id", uuid.UUID{}).
		SchemaType(map[string]string{
			dialect.Postgres: "uuid",
		}).
		Descriptor()
	assert.Equal(t, "uuid", fd.SchemaType[dialect.Postgres])

	// Test Optional
	fd = field.UUID("ref", uuid.UUID{}).Optional().Descriptor()
	assert.True(t, fd.Optional)

	// Test Immutable
	fd = field.UUID("id", uuid.UUID{}).Immutable().Descriptor()
	assert.True(t, fd.Immutable)
}

// TestOtherBuilderMethods tests additional otherBuilder methods.
func TestOtherBuilderMethods(t *testing.T) {
	// Test StructTag
	fd := field.Other("link", &custom{}).
		SchemaType(map[string]string{dialect.Postgres: "text"}).
		StructTag(`json:"link"`).
		Descriptor()
	assert.Equal(t, `json:"link"`, fd.Tag)

	// Test StorageKey
	fd = field.Other("customLink", &custom{}).
		SchemaType(map[string]string{dialect.Postgres: "text"}).
		StorageKey("custom_link").
		Descriptor()
	assert.Equal(t, "custom_link", fd.StorageKey)

	// Test Annotations
	ann := &field.Annotation{StructTag: map[string]string{"other": `json:"other"`}}
	fd = field.Other("data", &custom{}).
		SchemaType(map[string]string{dialect.Postgres: "text"}).
		Annotations(ann).
		Descriptor()
	require.Len(t, fd.Annotations, 1)

	// Test Nillable
	fd = field.Other("nullable", &custom{}).
		SchemaType(map[string]string{dialect.Postgres: "text"}).
		Nillable().
		Descriptor()
	assert.True(t, fd.Nillable)

	// Test Optional
	fd = field.Other("optional", &custom{}).
		SchemaType(map[string]string{dialect.Postgres: "text"}).
		Optional().
		Descriptor()
	assert.True(t, fd.Optional)

	// Test Immutable
	fd = field.Other("immutable", &custom{}).
		SchemaType(map[string]string{dialect.Postgres: "text"}).
		Immutable().
		Descriptor()
	assert.True(t, fd.Immutable)

	// Test Sensitive
	fd = field.Other("sensitive", &custom{}).
		SchemaType(map[string]string{dialect.Postgres: "text"}).
		Sensitive().
		Descriptor()
	assert.True(t, fd.Sensitive)
}

// TestSliceBuilderMethods tests sliceBuilder methods.
func TestSliceBuilderMethods(t *testing.T) {
	// Test StructTag
	fd := field.Strings("tags").StructTag(`json:"tags"`).Descriptor()
	assert.Equal(t, `json:"tags"`, fd.Tag)

	// Test StorageKey
	fd = field.Strings("userTags").StorageKey("user_tags").Descriptor()
	assert.Equal(t, "user_tags", fd.StorageKey)

	// Test Annotations
	ann := &field.Annotation{StructTag: map[string]string{"slice": `json:"slice"`}}
	fd = field.Strings("items").Annotations(ann).Descriptor()
	require.Len(t, fd.Annotations, 1)

	// Test Immutable
	fd = field.Strings("frozen").Immutable().Descriptor()
	assert.True(t, fd.Immutable)

	// Test Sensitive
	fd = field.Strings("secrets").Sensitive().Descriptor()
	assert.True(t, fd.Sensitive)

	// Test SchemaType
	fd = field.Strings("tags").
		SchemaType(map[string]string{
			dialect.Postgres: "jsonb",
		}).
		Descriptor()
	assert.Equal(t, "jsonb", fd.SchemaType[dialect.Postgres])

	// Test Ints slice
	fd = field.Ints("numbers").
		StorageKey("numbers").
		Optional().
		Descriptor()
	assert.Equal(t, "numbers", fd.StorageKey)
	assert.True(t, fd.Optional)

	// Test Floats slice
	fd = field.Floats("values").
		StorageKey("values").
		Immutable().
		Descriptor()
	assert.Equal(t, "values", fd.StorageKey)
	assert.True(t, fd.Immutable)

	// Test Deprecated
	fd = field.Strings("old").Deprecated("use new_field").Descriptor()
	assert.True(t, fd.Deprecated)
	assert.Equal(t, "use new_field", fd.DeprecatedReason)
}

// BenchmarkValidatorExecution benchmarks validator execution performance.
func BenchmarkValidatorExecution(b *testing.B) {
	b.Run("Int_Positive", func(b *testing.B) {
		fd := field.Int("n").Positive().Descriptor()
		validator := fd.Validators[0].(func(int) error)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = validator(100)
		}
	})

	b.Run("Int_Range", func(b *testing.B) {
		fd := field.Int("n").Range(0, 1000).Descriptor()
		validator := fd.Validators[0].(func(int) error)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = validator(500)
		}
	})

	b.Run("String_NotEmpty", func(b *testing.B) {
		fd := field.String("s").NotEmpty().Descriptor()
		validator := fd.Validators[0].(func(string) error)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = validator("hello world")
		}
	})

	b.Run("String_MaxLen", func(b *testing.B) {
		fd := field.String("s").MaxLen(100).Descriptor()
		validator := fd.Validators[0].(func(string) error)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = validator("hello world")
		}
	})

	b.Run("String_Match", func(b *testing.B) {
		re := regexp.MustCompile(`^[a-zA-Z0-9]+$`)
		fd := field.String("s").Match(re).Descriptor()
		validator := fd.Validators[0].(func(string) error)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = validator("hello123")
		}
	})
}
