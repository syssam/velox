package sql

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// Custom Go types bound to fields below. The generator only needs their
// reflect shape; nothing here is executed.
type (
	gtLabel string
	gtBig   int64
	gtBlob  []byte
	gtTier  string
	gtLevel string
)

func (gtTier) Values() []string { return []string{"free", "pro"} }

func (gtLevel) Values() []string { return []string{"low", "high"} }

func (l gtLevel) String() string { return string(l) }

func goTypeValidatorFixture(t *testing.T) (*mockHelper, *gen.Type) {
	t.Helper()
	descs := []*field.Descriptor{
		field.String("label").GoType(gtLabel("")).NotEmpty().MaxLen(20).Descriptor(),
		field.String("alias").GoType(gtLabel("")).Optional().Nillable().NotEmpty().Descriptor(),
		field.Int64("big").GoType(gtBig(0)).Positive().Descriptor(),
		field.Bytes("blob").GoType(gtBlob{}).MaxLen(8).Descriptor(),
		// Not a GoType, but the same path: the basic-type switch had no
		// []byte case, so a bytes validator was asserted as func(any) error.
		field.Bytes("raw").NotEmpty().Descriptor(),
		field.String("nullstr").GoType(sql.NullString{}).NotEmpty().Descriptor(),
		field.Enum("tier").GoType(gtTier("")).Default("free").Descriptor(),
		field.Enum("level").GoType(gtLevel("")).Optional().Nillable().Descriptor(),
		field.Enum("plain").Values("a", "b").Default("a").Descriptor(),
	}
	fields := make([]*load.Field, 0, len(descs))
	for _, d := range descs {
		lf, err := load.NewField(d)
		require.NoError(t, err)
		fields = append(fields, lf)
	}
	h := newMockHelper()
	typ := createTypeWithSchemaFields(t, "User", fields)
	h.graph.Nodes = []*gen.Type{typ}
	return h, typ
}

// TestGoTypeValidators_DeclaredAssertedAndCalledAtBasicType pins that the
// three sites of a GoType field's validator agree on one type: the leaf
// package declares it at the BASIC type, the runtime init asserts the
// descriptor's validator to that same type, and check() converts the
// GoType value to it (Ent's $f.BasicType "v").
//
// They used to disagree: the leaf declared func(gtLabel) error while the
// runtime asserted func(string) error, and the enum validator named a leaf
// enum type that does not exist for a GoType enum and called an IsValid()
// the user's type does not have. Both failed to compile in every project
// with such a field once validators stopped being opt-in.
func TestGoTypeValidators_DeclaredAssertedAndCalledAtBasicType(t *testing.T) {
	t.Parallel()
	h, typ := goTypeValidatorFixture(t)

	pkg := genPackage(h, typ, buildEntityPkgEnumRegistry(h.graph.Nodes)).GoString()
	rt := genEntityRuntime(h, typ).GoString()
	cf, err := genCreate(h, typ)
	require.NoError(t, err)
	create := cf.GoString()
	uf, err := genUpdate(h, typ)
	require.NoError(t, err)
	update := uf.GoString()

	for _, tc := range []struct {
		validator, basic, arg string
		multi                 bool
	}{
		{"LabelValidator", "string", "string(v)", true},
		{"AliasValidator", "string", "string(v)", false},
		{"BigValidator", "int64", "int64(v)", false},
		{"BlobValidator", "[]byte", "[]byte(v)", false},
		{"NullstrValidator", "string", "v.String", false},
		{"RawValidator", "[]byte", "v", false},
	} {
		t.Run(tc.validator, func(t *testing.T) {
			assert.Contains(t, pkg, tc.validator+" func("+tc.basic+") error",
				"leaf declaration must use the basic type")
			if tc.multi {
				assert.Contains(t, rt, "= func() func("+tc.basic+") error {")
				assert.Contains(t, rt, ".(func("+tc.basic+") error)")
			} else {
				assert.Contains(t, rt, "Validators[0].(func("+tc.basic+") error)")
			}
			call := "user." + tc.validator + "(" + tc.arg + ")"
			assert.Contains(t, create, call, "create check() must convert to the basic type")
			assert.Contains(t, update, call, "update check() must convert to the basic type")
		})
	}

	t.Run("enum validators are generated functions", func(t *testing.T) {
		// GoType enum: literal cases, no reference to a leaf enum type or
		// to IsValid.
		assert.Contains(t, pkg, "func TierValidator(v ")
		assert.Contains(t, pkg, "switch v {\n\tcase \"free\", \"pro\":")
		// A Stringer GoType enum switches on String().
		assert.Contains(t, pkg, "func LevelValidator(v ")
		assert.Contains(t, pkg, "switch v.String() {\n\tcase \"low\", \"high\":")
		// A generated enum switches on its constants.
		assert.Contains(t, pkg, "func PlainValidator(v Plain) error")
		assert.Contains(t, pkg, "case PlainA, PlainB:")
		assert.NotContains(t, pkg, "v.IsValid()", "no validator may rely on a generated IsValid")
		// Nothing assigns them at runtime, so they can never be nil.
		for _, name := range []string{"TierValidator", "LevelValidator", "PlainValidator"} {
			assert.NotContains(t, rt, name, "enum validators are not runtime-assigned")
			assert.NotContains(t, pkg, name+" func(", "enum validators are not variables")
			assert.Contains(t, create, "user."+name+"(v)")
		}
	})
}

// TestGoTypeString_ConvertsStringKinds pins that String() compiles for a
// string field with a custom GoType: a string kind is converted with
// string(...), any other GoType (sql.NullString) goes through Fprintf.
// WriteString(e.Label) with Label of type gtLabel did not compile.
func TestGoTypeString_ConvertsStringKinds(t *testing.T) {
	t.Parallel()
	h, typ := goTypeValidatorFixture(t)
	f := h.NewFile("entity")
	genEntityPkgStringMethod(h, f, typ)
	code := f.GoString()

	assert.Contains(t, code, "b.WriteString(string(e.Label))")
	assert.Contains(t, code, "b.WriteString(string(*e.Alias))")
	assert.Contains(t, code, `fmt.Fprintf(&b, "nullstr=%v", e.Nullstr)`)
	assert.NotContains(t, code, "b.WriteString(e.Label)")
	assert.NotContains(t, code, "b.WriteString(e.Nullstr)")
}
