package gen

import (
	"testing"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"

	"github.com/stretchr/testify/require"
)

func TestType(t *testing.T) {
	require := require.New(t)
	typ, err := NewType(&Config{Package: "entc/gen"}, T1)
	require.NoError(err)
	require.NotNil(typ)
	require.Equal("T1", typ.Name)
	require.Equal("t1", typ.Label())
	require.Equal("t1", typ.Package())
	require.Equal("m", typ.Receiver())

	_, err = NewType(&Config{Package: "entc/gen"}, &load.Schema{
		Name: "TestSchema",
		Fields: []*load.Field{
			{Name: "foo", Unique: true, Default: true, Info: &field.TypeInfo{Type: field.TypeInt}},
		},
	})
	require.EqualError(err, "unique field \"foo\" cannot have default value", "unique field can not have default")

	_, err = NewType(&Config{Package: "entc/gen"}, &load.Schema{
		Name: "TestSchema",
		Fields: []*load.Field{
			{Name: "foo", Sensitive: true, Tag: `yaml:"pwd"`, Info: &field.TypeInfo{Type: field.TypeString}},
		},
	})
	require.EqualError(err, "sensitive field \"foo\" cannot have struct tags", "sensitive field cannot have tags")

	typ, err = NewType(&Config{Package: "entc/gen"}, &load.Schema{
		Name: "TestSchema",
		Fields: []*load.Field{
			{Name: "id", Info: &field.TypeInfo{Type: field.TypeString}, Annotations: dict("sql", dict("collation", "utf8_ci_bin"))},
		},
	})
	require.NoError(err)
	require.NotNil(typ)
	require.NotNil(t, typ.ID)
	pkCol := typ.ID.PK()
	require.NotNil(pkCol)
	require.Equal("utf8_ci_bin", pkCol.Collation)

	_, err = NewType(&Config{Package: "entc/gen"}, &load.Schema{
		Name: "T",
		Fields: []*load.Field{
			{Name: "foo", Unique: true, Info: &field.TypeInfo{Type: field.TypeInt}},
			{Name: "foo", Unique: true, Info: &field.TypeInfo{Type: field.TypeInt}},
		},
	})
	require.EqualError(err, "field \"foo\" redeclared for type \"T\"", "field foo redeclared")

	_, err = NewType(&Config{Package: "entc/gen"}, &load.Schema{
		Name: "T",
		Fields: []*load.Field{
			{Name: "enums", Info: &field.TypeInfo{Type: field.TypeEnum}, Enums: []struct{ N, V string }{{V: "v"}, {V: "v"}}},
		},
	})
	require.EqualError(err, "duplicate values \"v\" for enum field \"enums\"", "duplicate enums")

	_, err = NewType(&Config{Package: "entc/gen"}, &load.Schema{
		Name: "T",
		Fields: []*load.Field{
			{Name: "enums", Info: &field.TypeInfo{Type: field.TypeEnum}, Enums: []struct{ N, V string }{{}}},
		},
	})
	require.EqualError(err, "\"enums\" field value cannot be empty", "empty value for enums")

	_, err = NewType(&Config{Package: "entc/gen"}, &load.Schema{
		Name: "T",
		Fields: []*load.Field{
			{Name: "", Info: &field.TypeInfo{Type: field.TypeInt}},
		},
	})
	require.EqualError(err, "field name cannot be empty", "empty field name")

	_, err = NewType(&Config{Package: "entc/gen"}, &load.Schema{
		Name: "T",
		Fields: []*load.Field{
			{Name: "id", Info: &field.TypeInfo{Type: field.TypeInt}, Optional: true},
		},
	})
	require.EqualError(err, "id field cannot be optional", "id field cannot be optional")

	_, err = NewType(&Config{Package: "entc/gen"}, &load.Schema{Name: "Type"})
	require.EqualError(err, "schema lowercase name conflicts with Go keyword \"type\"")
	_, err = NewType(&Config{Package: "entc/gen"}, &load.Schema{Name: "Int"})
	require.EqualError(err, "schema lowercase name conflicts with Go predeclared identifier \"int\"")
	_, err = NewType(&Config{Package: "entc/gen"}, &load.Schema{Name: "Value"})
	require.EqualError(err, "schema name conflicts with ent predeclared identifier \"Value\"")
}

func TestType_Label(t *testing.T) {
	tests := []struct {
		name  string
		label string
	}{
		{"User", "user"},
		{"UserInfo", "user_info"},
		{"PHBOrg", "phb_org"},
		{"UserID", "user_id"},
		{"HTTPCode", "http_code"},
		{"UserIDs", "user_ids"},
	}
	for _, tt := range tests {
		typ := &Type{Name: tt.name}
		require.Equal(t, tt.label, typ.Label())
	}
}

func TestType_Table(t *testing.T) {
	tests := []struct {
		name  string
		label string
	}{
		{"User", "users"},
		{"Device", "devices"},
		{"UserInfo", "user_infos"},
		{"PHBOrg", "phb_orgs"},
		{"HTTPCode", "http_codes"},
	}
	for _, tt := range tests {
		typ := &Type{Name: tt.name}
		require.Equal(t, tt.label, typ.Table())
	}
}

func TestField_EnumName(t *testing.T) {
	tests := []struct {
		name string
		enum string
	}{
		{"GIF", "TypeGIF"},
		{"SVG", "TypeSVG"},
		{"PNG", "TypePNG"},
		{"MP4", "TypeMP4"},
		{"unknown", "TypeUnknown"},
		{"user_data", "TypeUserData"},
		{"test user", "TypeTestUser"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.enum, Field{Name: "Type"}.EnumName(tt.name))
	}
}

func TestType_WithRuntimeMixin(t *testing.T) {
	position := &load.Position{MixedIn: true}
	typ := &Type{
		ID: &Field{},
		Fields: []*Field{
			{Default: true, Position: position},
			{UpdateDefault: true, Position: position},
			{Validators: 1, Position: position},
		},
	}
	require.True(t, typ.RuntimeMixin())
}

func TestType_TagTypes(t *testing.T) {
	typ := &Type{
		Fields: []*Field{
			{StructTag: `json:"age"`},
			{StructTag: `json:"name,omitempty`},
			{StructTag: `json:"name,omitempty" sql:"nothing"`},
			{StructTag: `sql:"nothing" yaml:"ignore"`},
			{StructTag: `sql:"nothing" yaml:"ignore"`},
			{StructTag: `invalid`},
			{StructTag: `"invalid"`},
		},
	}
	tags := typ.TagTypes()
	require.Equal(t, []string{"json", "sql", "yaml"}, tags)
}

func TestType_Package(t *testing.T) {
	tests := []struct {
		name string
		pkg  string
	}{
		{"User", "user"},
		{"UserInfo", "userinfo"},
		{"PHBOrg", "phborg"},
		{"UserID", "userid"},
		{"HTTPCode", "httpcode"},
	}
	for _, tt := range tests {
		typ := &Type{Name: tt.name}
		require.Equal(t, tt.pkg, typ.Package())
	}
}

func TestType_AddIndex(t *testing.T) {
	size := int64(1024)
	typ, err := NewType(&Config{}, &load.Schema{
		Name: "User",
		Fields: []*load.Field{
			{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
			{Name: "text", Info: &field.TypeInfo{Type: field.TypeString}, Size: &size},
		},
	})
	require.NoError(t, err)
	typ.Edges = append(typ.Edges,
		&Edge{Name: "next", Rel: Relation{Type: O2O, Columns: []string{"prev_id"}}},
		&Edge{Name: "prev", Inverse: "next", Rel: Relation{Type: O2O, Columns: []string{"prev_id"}}},
		&Edge{Name: "owner", Inverse: "files", Rel: Relation{Type: M2O, Columns: []string{"file_id"}}},
	)

	err = typ.AddIndex(&load.Index{Unique: true})
	require.Error(t, err, "missing fields or edges")

	err = typ.AddIndex(&load.Index{Unique: true, Fields: []string{"unknown"}})
	require.Error(t, err, "unknown field for index")

	err = typ.AddIndex(&load.Index{Unique: true, Fields: []string{"id"}})
	require.NoError(t, err, "valid index for ID field")

	err = typ.AddIndex(&load.Index{Unique: true, Fields: []string{"name"}, Edges: []string{"parent"}})
	require.Error(t, err, "missing edge")

	err = typ.AddIndex(&load.Index{Unique: true, Fields: []string{"name"}, Edges: []string{"next"}})
	require.Error(t, err, "not an inverse edge for O2O relation")

	err = typ.AddIndex(&load.Index{Unique: true, Edges: []string{"prev", "owner"}})
	require.NoError(t, err, "valid index defined only on edges")

	err = typ.AddIndex(&load.Index{Unique: true, Fields: []string{"name"}, Edges: []string{"prev"}})
	require.NoError(t, err, "valid index on O2O relation and field")

	err = typ.AddIndex(&load.Index{Unique: true, Fields: []string{"name"}, Edges: []string{"owner"}})
	require.NoError(t, err, "valid index on M2O relation and field")
}

func TestField_Constant(t *testing.T) {
	tests := []struct {
		name     string
		constant string
	}{
		{"user", "FieldUser"},
		{"user_id", "FieldUserID"},
		{"user_name", "FieldUserName"},
	}
	for _, tt := range tests {
		typ := &Field{Name: tt.name}
		require.Equal(t, tt.constant, typ.Constant())
	}
}

func TestField_DefaultName(t *testing.T) {
	tests := []struct {
		name     string
		constant string
	}{
		{"active", "DefaultActive"},
		{"expired_at", "DefaultExpiredAt"},
		{"group_name", "DefaultGroupName"},
	}
	for _, tt := range tests {
		typ := &Field{Name: tt.name}
		require.Equal(t, tt.constant, typ.DefaultName())
	}
}

func TestField_incremental(t *testing.T) {
	tests := []struct {
		annotations map[string]any
		def         bool
		expected    bool
	}{
		{dict("sql", nil), false, false},
		{dict("sql", nil), true, true},
		{dict("sql", dict("incremental", true)), false, true},
		{dict("sql", dict("incremental", false)), true, false},
	}
	for _, tt := range tests {
		typ := &Field{Annotations: tt.annotations}
		require.Equal(t, tt.expected, typ.incremental(tt.def))
	}
}

func TestField_needsAutoDefault(t *testing.T) {
	cfgWithFeature := &Config{Features: []Feature{FeatureAutoDefault}}
	cfgWithoutFeature := &Config{Features: []Feature{}}

	tests := []struct {
		name     string
		field    Field
		expected bool
	}{
		{
			name:     "feature disabled",
			field:    Field{Optional: true, cfg: cfgWithoutFeature, Type: &field.TypeInfo{Type: field.TypeString}},
			expected: false,
		},
		{
			name:     "feature enabled, optional string without default",
			field:    Field{Optional: true, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeString}},
			expected: true,
		},
		{
			name:     "feature enabled, optional int without default",
			field:    Field{Optional: true, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeInt}},
			expected: true,
		},
		{
			name:     "feature enabled, optional bool without default",
			field:    Field{Optional: true, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeBool}},
			expected: true,
		},
		{
			name:     "feature enabled, required string (also gets default)",
			field:    Field{Optional: false, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeString}},
			expected: true,
		},
		{
			name:     "feature enabled, required int (also gets default)",
			field:    Field{Optional: false, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeInt}},
			expected: true,
		},
		{
			name:     "feature enabled, nillable field (NULL columns don't need DEFAULT)",
			field:    Field{Optional: true, Nillable: true, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeString}},
			expected: false,
		},
		{
			name:     "feature enabled, has explicit default",
			field:    Field{Optional: true, Default: true, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeString}},
			expected: false,
		},
		{
			name:     "feature enabled, required with explicit default",
			field:    Field{Optional: false, Default: true, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeString}},
			expected: false,
		},
		{
			name:     "feature enabled, enum field (no universal zero value)",
			field:    Field{Optional: true, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeEnum}},
			expected: false,
		},
		{
			name:     "feature enabled, json field (no universal zero value)",
			field:    Field{Optional: true, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeJSON}},
			expected: false,
		},
		{
			name:     "feature enabled, time field (requires explicit DefaultExpr)",
			field:    Field{Optional: true, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeTime}},
			expected: false,
		},
		{
			name:     "feature enabled, uuid field (requires explicit DefaultExpr)",
			field:    Field{Optional: true, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeUUID}},
			expected: false,
		},
		{
			name:     "feature enabled, bytes field (requires explicit default)",
			field:    Field{Optional: true, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeBytes}},
			expected: false,
		},
		{
			name:     "feature enabled, other/custom field (requires explicit default)",
			field:    Field{Optional: true, cfg: cfgWithFeature, Type: &field.TypeInfo{Type: field.TypeOther}},
			expected: false,
		},
		{
			name:     "feature enabled, nil type",
			field:    Field{Optional: true, cfg: cfgWithFeature, Type: nil},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.field.needsAutoDefault())
		})
	}
}

func TestField_zeroValue(t *testing.T) {
	tests := []struct {
		fieldType field.Type
		expected  any
	}{
		{field.TypeString, ""},
		{field.TypeBool, false},
		{field.TypeInt, 0},
		{field.TypeInt8, 0},
		{field.TypeInt16, 0},
		{field.TypeInt32, 0},
		{field.TypeInt64, 0},
		{field.TypeUint, 0},
		{field.TypeUint8, 0},
		{field.TypeUint16, 0},
		{field.TypeUint32, 0},
		{field.TypeUint64, 0},
		{field.TypeFloat32, 0.0},
		{field.TypeFloat64, 0.0},
		{field.TypeEnum, nil},
		{field.TypeJSON, nil},
	}
	for _, tt := range tests {
		t.Run(tt.fieldType.String(), func(t *testing.T) {
			f := Field{Type: &field.TypeInfo{Type: tt.fieldType}}
			require.Equal(t, tt.expected, f.zeroValue())
		})
	}
}

func TestField_Column_AutoDefault(t *testing.T) {
	cfgWithFeature := &Config{Features: []Feature{FeatureAutoDefault}}
	cfgWithoutFeature := &Config{Features: []Feature{}}
	// Create a minimal Type to avoid nil pointer in sqlComment()
	dummyType := &Type{Name: "Test"}

	tests := []struct {
		name            string
		field           Field
		expectedDefault any
	}{
		{
			name:            "string field with feature enabled gets empty string default",
			field:           Field{Name: "nickname", cfg: cfgWithFeature, typ: dummyType, Type: &field.TypeInfo{Type: field.TypeString}},
			expectedDefault: "",
		},
		{
			name:            "int field with feature enabled gets zero default",
			field:           Field{Name: "count", cfg: cfgWithFeature, typ: dummyType, Type: &field.TypeInfo{Type: field.TypeInt}},
			expectedDefault: 0,
		},
		{
			name:            "bool field with feature enabled gets false default",
			field:           Field{Name: "active", cfg: cfgWithFeature, typ: dummyType, Type: &field.TypeInfo{Type: field.TypeBool}},
			expectedDefault: false,
		},
		{
			name:            "string field without feature gets no default",
			field:           Field{Name: "nickname", cfg: cfgWithoutFeature, typ: dummyType, Type: &field.TypeInfo{Type: field.TypeString}},
			expectedDefault: nil,
		},
		{
			name:            "time field with feature enabled gets no default (requires explicit)",
			field:           Field{Name: "created_at", cfg: cfgWithFeature, typ: dummyType, Type: &field.TypeInfo{Type: field.TypeTime}},
			expectedDefault: nil,
		},
		{
			name:            "enum field with feature enabled gets no default (requires explicit)",
			field:           Field{Name: "status", cfg: cfgWithFeature, typ: dummyType, Type: &field.TypeInfo{Type: field.TypeEnum}},
			expectedDefault: nil,
		},
		{
			name:            "nillable field with feature enabled gets no default (NULL allowed)",
			field:           Field{Name: "bio", Nillable: true, cfg: cfgWithFeature, typ: dummyType, Type: &field.TypeInfo{Type: field.TypeString}},
			expectedDefault: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			col := tt.field.Column()
			require.Equal(t, tt.expectedDefault, col.Default)
		})
	}
}

func TestBuilderField(t *testing.T) {
	tests := []struct {
		name  string
		field string
	}{
		{"active", "active"},
		{"type", "_type"},
		{"config", "_config"},
		{"SSOCert", "_SSOCert"},
		{"driver", "_driver"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.field, Edge{Name: tt.name}.BuilderField())
		require.Equal(t, tt.field, Field{Name: tt.name}.BuilderField())
	}
}

func TestEdge(t *testing.T) {
	u, g := &Type{Name: "User"}, &Type{Name: "Group"}
	groups := &Edge{Name: "groups", Type: g, Owner: u, Rel: Relation{Type: M2M}}
	users := &Edge{Name: "users", Inverse: "groups", Type: u, Owner: u, Rel: Relation{Type: M2M}}

	require.True(t, users.IsInverse())
	require.False(t, groups.IsInverse())

	require.Equal(t, "GroupsLabel", users.LabelConstant())
	require.Equal(t, "GroupsLabel", groups.LabelConstant())

	require.Equal(t, "UsersInverseLabel", users.InverseLabelConstant())
	require.Equal(t, "user_groups", users.Label())
	require.Equal(t, "user_groups", groups.Label())
}

func TestValidSchemaName(t *testing.T) {
	// Test conflicts with ent predeclared identifiers
	err := ValidSchemaName("Config")
	require.Error(t, err)
	err = ValidSchemaName("Mutation")
	require.Error(t, err)

	// Test valid schema names
	err = ValidSchemaName("Boring")
	require.NoError(t, err)
	err = ValidSchemaName("Order")
	require.NoError(t, err)

	// Test empty name
	err = ValidSchemaName("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be empty")

	// Test path traversal protection
	err = ValidSchemaName("../evil")
	require.Error(t, err)
	require.Contains(t, err.Error(), "path separator")

	err = ValidSchemaName("dir/file")
	require.Error(t, err)
	require.Contains(t, err.Error(), "path separator")

	err = ValidSchemaName(`dir\file`)
	require.Error(t, err)
	require.Contains(t, err.Error(), "path separator")

	err = ValidSchemaName("parent..")
	require.Error(t, err)
	require.Contains(t, err.Error(), "parent directory reference")

	// Test hidden files protection
	err = ValidSchemaName(".hidden")
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot start with a dot")

	// Test invalid Go identifier
	err = ValidSchemaName("123invalid")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a valid Go identifier")

	err = ValidSchemaName("has-hyphen")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a valid Go identifier")
}
