package gen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	velox "github.com/syssam/velox"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// --- Op (predicate.go) ---

func TestOp_Name(t *testing.T) {
	tests := []struct {
		op   Op
		want string
	}{
		{EQ, "EQ"}, {NEQ, "NEQ"}, {GT, "GT"}, {GTE, "GTE"},
		{LT, "LT"}, {LTE, "LTE"}, {IsNil, "IsNil"}, {NotNil, "NotNil"},
		{In, "In"}, {NotIn, "NotIn"}, {EqualFold, "EqualFold"},
		{Contains, "Contains"}, {ContainsFold, "ContainsFold"},
		{HasPrefix, "HasPrefix"}, {HasSuffix, "HasSuffix"},
		{Op(999), "Unknown"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.op.Name())
	}
}

func TestOp_Variadic(t *testing.T) {
	assert.True(t, In.Variadic())
	assert.True(t, NotIn.Variadic())
	assert.False(t, EQ.Variadic())
	assert.False(t, IsNil.Variadic())
}

func TestOp_Niladic(t *testing.T) {
	assert.True(t, IsNil.Niladic())
	assert.True(t, NotNil.Niladic())
	assert.False(t, EQ.Niladic())
	assert.False(t, In.Niladic())
}

// --- SchemaMode (storage.go) ---

func TestSchemaMode_Support(t *testing.T) {
	m := Unique | Indexes
	assert.True(t, m.Support(Unique))
	assert.True(t, m.Support(Indexes))
	assert.False(t, m.Support(Cascade))
	assert.False(t, m.Support(Migrate))
	assert.True(t, (Unique | Cascade).Support(Cascade))
}

func TestStorage_String(t *testing.T) {
	s := &Storage{Name: "sql"}
	assert.Equal(t, "sql", s.String())
}

// --- Type accessors (type.go) ---

func TestType_IsView(t *testing.T) {
	assert.False(t, Type{schema: &load.Schema{View: false}}.IsView())
	assert.True(t, Type{schema: &load.Schema{View: true}}.IsView())
	assert.False(t, Type{schema: nil}.IsView())
}

func TestType_IsEdgeSchema(t *testing.T) {
	typ := Type{}
	assert.False(t, typ.IsEdgeSchema())
	typ.EdgeSchema.To = &Edge{Name: "to"}
	assert.True(t, typ.IsEdgeSchema())

	typ2 := Type{}
	typ2.EdgeSchema.From = &Edge{Name: "from"}
	assert.True(t, typ2.IsEdgeSchema())
}

func TestType_HasCompositeID(t *testing.T) {
	typ := Type{}
	assert.False(t, typ.HasCompositeID())

	typ.EdgeSchema.To = &Edge{Name: "to"}
	typ.EdgeSchema.ID = []*Field{{Name: "a"}, {Name: "b"}}
	assert.True(t, typ.HasCompositeID())
}

func TestType_HasOneFieldID(t *testing.T) {
	typ := Type{ID: &Field{Name: "id"}}
	assert.True(t, typ.HasOneFieldID())

	// Composite ID → false.
	typ.EdgeSchema.To = &Edge{Name: "to"}
	typ.EdgeSchema.ID = []*Field{{Name: "a"}, {Name: "b"}}
	assert.False(t, typ.HasOneFieldID())

	// Nil ID → false.
	typ2 := Type{}
	assert.False(t, typ2.HasOneFieldID())
}

func TestType_LabelHelper(t *testing.T) {
	assert.Equal(t, "user", Type{Name: "User"}.Label())
	assert.Equal(t, "credit_card", Type{Name: "CreditCard"}.Label())
}

func TestType_TableHelper(t *testing.T) {
	// Default: pluralized snake_case.
	assert.Equal(t, "users", Type{Name: "User", schema: &load.Schema{}}.Table())
	assert.Equal(t, "credit_cards", Type{Name: "CreditCard", schema: &load.Schema{}}.Table())

	// Override via schema Config.Table.
	typ := Type{Name: "User", schema: &load.Schema{Config: velox.Config{Table: "my_users"}}}
	assert.Equal(t, "my_users", typ.Table())
}

func TestType_PackageHelper(t *testing.T) {
	assert.Equal(t, "user", Type{Name: "User"}.Package())
	// With alias.
	assert.Equal(t, "usr", Type{Name: "User", alias: "usr"}.Package())
}

func TestType_PackageDir(t *testing.T) {
	assert.Equal(t, "user", Type{Name: "User"}.PackageDir())
	assert.Equal(t, "creditcard", Type{Name: "CreditCard"}.PackageDir())
}

func TestType_PackageAlias(t *testing.T) {
	assert.Equal(t, "", Type{Name: "User"}.PackageAlias())
	assert.Equal(t, "usr", Type{Name: "User", alias: "usr"}.PackageAlias())
}

func TestType_Receiver(t *testing.T) {
	assert.Equal(t, "m", Type{Name: "User"}.Receiver())
}

func TestType_Pos(t *testing.T) {
	typ := Type{schema: &load.Schema{Pos: "schema/user.go:10"}}
	assert.Equal(t, "schema/user.go:10", typ.Pos())
}

func TestType_HasEdge(t *testing.T) {
	typ := Type{
		Edges: []*Edge{{Name: "posts"}, {Name: "groups"}},
	}
	assert.True(t, typ.hasEdge("posts"))
	assert.True(t, typ.hasEdge("groups"))
	assert.False(t, typ.hasEdge("comments"))
}

func TestType_HasAssoc(t *testing.T) {
	assocEdge := &Edge{Name: "posts", Rel: Relation{Type: O2M}}
	inverseEdge := &Edge{Name: "owner", Inverse: "owner", Rel: Relation{Type: M2O}}
	typ := Type{
		Edges: []*Edge{assocEdge, inverseEdge},
	}
	e, ok := typ.HasAssoc("posts")
	assert.True(t, ok)
	assert.Equal(t, "posts", e.Name)

	_, ok = typ.HasAssoc("owner")
	assert.False(t, ok, "inverse edge should not be returned")

	_, ok = typ.HasAssoc("unknown")
	assert.False(t, ok)
}

func TestType_HasValidators(t *testing.T) {
	assert.False(t, Type{Fields: []*Field{{Name: "name"}}}.HasValidators())
	assert.True(t, Type{Fields: []*Field{{Name: "name", Validators: 1}}}.HasValidators())

	// Via ID field.
	typ := Type{
		ID:     &Field{Name: "id", UserDefined: true, Validators: 1},
		Fields: []*Field{},
	}
	assert.True(t, typ.HasValidators())
}

func TestType_HasDefault(t *testing.T) {
	assert.False(t, Type{Fields: []*Field{{Name: "name"}}}.HasDefault())
	assert.True(t, Type{Fields: []*Field{{Name: "name", Default: true}}}.HasDefault())

	// Via ID field.
	typ := Type{
		ID:     &Field{Name: "id", UserDefined: true, Default: true},
		Fields: []*Field{},
	}
	assert.True(t, typ.HasDefault())
}

func TestType_HasUpdateDefault(t *testing.T) {
	assert.False(t, Type{Fields: []*Field{{Name: "name"}}}.HasUpdateDefault())
	assert.True(t, Type{Fields: []*Field{{Name: "updated_at", UpdateDefault: true}}}.HasUpdateDefault())
}

func TestType_HasOptional(t *testing.T) {
	assert.False(t, Type{Fields: []*Field{{Name: "name"}}}.HasOptional())
	assert.True(t, Type{Fields: []*Field{{Name: "bio", Optional: true}}}.HasOptional())
}

func TestType_HasNumeric(t *testing.T) {
	assert.False(t, Type{Fields: []*Field{{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}}}}.HasNumeric())
	assert.True(t, Type{Fields: []*Field{{Name: "age", Type: &field.TypeInfo{Type: field.TypeInt}}}}.HasNumeric())
}

func TestType_NeedsDefaults(t *testing.T) {
	// Field with Default.
	assert.True(t, Type{Fields: []*Field{{Name: "status", Default: true}}}.NeedsDefaults())
	// Optional non-nillable standard type.
	assert.True(t, Type{Fields: []*Field{{Name: "age", Optional: true, Type: &field.TypeInfo{Type: field.TypeInt}}}}.NeedsDefaults())
	// Optional nillable — no.
	assert.False(t, Type{Fields: []*Field{{Name: "age", Optional: true, Nillable: true, Type: &field.TypeInfo{Type: field.TypeInt}}}}.NeedsDefaults())
	// Optional enum without Default — no.
	assert.False(t, Type{Fields: []*Field{{Name: "role", Optional: true, Type: &field.TypeInfo{Type: field.TypeEnum}}}}.NeedsDefaults())
}

func TestType_HasUpdateCheckers(t *testing.T) {
	// No validators, no enums → false.
	assert.False(t, Type{Fields: []*Field{{Name: "name"}}}.HasUpdateCheckers())
	// Mutable field with validator.
	assert.True(t, Type{Fields: []*Field{{Name: "name", Validators: 1}}}.HasUpdateCheckers())
	// Immutable field with validator — no.
	assert.False(t, Type{Fields: []*Field{{Name: "id", Validators: 1, Immutable: true}}}.HasUpdateCheckers())
	// Enum field → true.
	assert.True(t, Type{Fields: []*Field{{Name: "status", Type: &field.TypeInfo{Type: field.TypeEnum}}}}.HasUpdateCheckers())
	// Required unique edge.
	assert.True(t, Type{
		Fields: []*Field{},
		Edges:  []*Edge{{Name: "owner", Unique: true, Optional: false, Rel: Relation{Type: M2O}}},
	}.HasUpdateCheckers())
}

func TestType_FKEdges(t *testing.T) {
	// M2O edge that owns FK (non-inverse, M2O).
	ownerType := &Type{Name: "User"}
	fkEdge := &Edge{Name: "owner", Rel: Relation{Type: M2O}, Type: ownerType}
	nonFKEdge := &Edge{Name: "posts", Rel: Relation{Type: O2M}, Type: ownerType}
	typ := Type{Name: "Post", Edges: []*Edge{fkEdge, nonFKEdge}}
	fkEdges := typ.FKEdges()
	assert.Len(t, fkEdges, 1)
	assert.Equal(t, "owner", fkEdges[0].Name)
}

func TestType_NumM2M(t *testing.T) {
	typ := Type{Edges: []*Edge{
		{Name: "groups", Rel: Relation{Type: M2M}},
		{Name: "posts", Rel: Relation{Type: O2M}},
		{Name: "tags", Rel: Relation{Type: M2M}},
	}}
	assert.Equal(t, 2, typ.NumM2M())
}

func TestType_NameMethods(t *testing.T) {
	typ := Type{Name: "User"}
	assert.Equal(t, "UserCreate", typ.CreateName())
	assert.Equal(t, "_c", typ.CreateReceiver())
	assert.Equal(t, "UserCreateBulk", typ.CreateBulkName())
	assert.Equal(t, "_c", typ.CreateBulReceiver())
	assert.Equal(t, "UserUpdate", typ.UpdateName())
	assert.Equal(t, "_u", typ.UpdateReceiver())
	assert.Equal(t, "UserUpdateOne", typ.UpdateOneName())
	assert.Equal(t, "_u", typ.UpdateOneReceiver())
	assert.Equal(t, "UserDelete", typ.DeleteName())
	assert.Equal(t, "_d", typ.DeleteReceiver())
	assert.Equal(t, "UserDeleteOne", typ.DeleteOneName())
	assert.Equal(t, "_d", typ.DeleteOneReceiver())
	assert.Equal(t, "UserMutation", typ.MutationName())
	assert.Equal(t, "userOption", typ.MutationOptionName())
	assert.Equal(t, "_g", typ.GroupReceiver())
	assert.Equal(t, "_s", typ.SelectReceiver())
	assert.Equal(t, "TypeUser", typ.TypeName())
	assert.Equal(t, "Value", typ.ValueName())
}

func TestType_NumHooks(t *testing.T) {
	typ := Type{schema: &load.Schema{
		Hooks:        []*load.Position{{MixinIndex: -1}},
		Interceptors: []*load.Position{{MixinIndex: -1}, {MixinIndex: -1}},
		Policy:       []*load.Position{},
	}}
	assert.Equal(t, 1, typ.NumHooks())
	assert.Equal(t, 2, typ.NumInterceptors())
	assert.Equal(t, 0, typ.NumPolicy())
}

func TestType_MutableFields(t *testing.T) {
	fields := []*Field{
		{Name: "name", Immutable: false},
		{Name: "id", Immutable: true},
		{Name: "age", Immutable: false},
	}
	typ := Type{Fields: fields}
	mutable := typ.MutableFields()
	assert.Len(t, mutable, 2)
	assert.Equal(t, "name", mutable[0].Name)
	assert.Equal(t, "age", mutable[1].Name)
}

func TestType_ImmutableFields(t *testing.T) {
	fields := []*Field{
		{Name: "name", Immutable: false},
		{Name: "created_at", Immutable: true},
	}
	typ := Type{Fields: fields}
	immut := typ.ImmutableFields()
	assert.Len(t, immut, 1)
	assert.Equal(t, "created_at", immut[0].Name)
}

func TestType_MutationFields(t *testing.T) {
	// MutationFields returns non-edge fields from Fields (not ID).
	typ := Type{
		ID:     &Field{Name: "id", UserDefined: false},
		Fields: []*Field{{Name: "name"}, {Name: "age"}},
	}
	mf := typ.MutationFields()
	assert.Len(t, mf, 2)

	// Edge field is excluded (fk != nil).
	edgeField := &Field{Name: "owner_id", fk: &ForeignKey{}}
	typ2 := Type{
		Fields: []*Field{{Name: "name"}, edgeField},
	}
	mf2 := typ2.MutationFields()
	assert.Len(t, mf2, 1)
	assert.Equal(t, "name", mf2[0].Name)
}

func TestType_EnumFields(t *testing.T) {
	typ := Type{Fields: []*Field{
		{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
		{Name: "status", Type: &field.TypeInfo{Type: field.TypeEnum}},
		{Name: "role", Type: &field.TypeInfo{Type: field.TypeEnum}},
	}}
	enums := typ.EnumFields()
	assert.Len(t, enums, 2)
}

func TestType_FieldBy(t *testing.T) {
	typ := Type{
		ID: &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
		Fields: []*Field{
			{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}},
			{Name: "age", Type: &field.TypeInfo{Type: field.TypeInt}},
		},
	}
	// Find by name.
	f, ok := typ.FieldBy(func(f *Field) bool { return f.Name == "age" })
	require.True(t, ok)
	assert.Equal(t, "age", f.Name)

	// Find ID field.
	f, ok = typ.FieldBy(func(f *Field) bool { return f.Name == "id" })
	require.True(t, ok)
	assert.Equal(t, "id", f.Name)

	// Not found.
	_, ok = typ.FieldBy(func(f *Field) bool { return f.Name == "unknown" })
	assert.False(t, ok)
}

func TestType_HasValueScanner(t *testing.T) {
	assert.False(t, Type{Fields: []*Field{{Name: "name", def: &load.Field{}}}}.HasValueScanner())
	assert.True(t, Type{Fields: []*Field{{Name: "data", def: &load.Field{ValueScanner: true}}}}.HasValueScanner())
}

func TestType_DeprecatedFields(t *testing.T) {
	typ := Type{Fields: []*Field{
		{Name: "name", def: &load.Field{Deprecated: false}},
		{Name: "old_name", def: &load.Field{Deprecated: true}},
	}}
	dep := typ.DeprecatedFields()
	assert.Len(t, dep, 1)
	assert.Equal(t, "old_name", dep[0].Name)
}

func TestType_EntSQL(t *testing.T) {
	typ := Type{Annotations: Annotations{}}
	assert.Nil(t, typ.EntSQL())
}

func TestType_MixedInFields(t *testing.T) {
	typ := Type{
		schema: &load.Schema{},
		Fields: []*Field{
			{Name: "id", Default: true, Position: &load.Position{MixinIndex: 0, MixedIn: true}},
			{Name: "created_at", Default: true, Position: &load.Position{MixinIndex: 1, MixedIn: true}},
			{Name: "name", Position: &load.Position{MixinIndex: -1}},
		},
	}
	mixedIn := typ.MixedInFields()
	assert.Len(t, mixedIn, 2) // Two unique mixin indices: 0, 1
}

func TestType_NumMixin(t *testing.T) {
	typ := Type{
		schema: &load.Schema{},
		Fields: []*Field{
			{Name: "id", Position: &load.Position{MixinIndex: 0, MixedIn: true}},
			{Name: "created_at", Position: &load.Position{MixinIndex: 0, MixedIn: true}},
		},
	}
	assert.Equal(t, 1, typ.NumMixin())
}

func TestType_NumConstraint(t *testing.T) {
	typ := Type{
		Fields: []*Field{
			{Name: "email", Unique: true},
			{Name: "age"},
		},
		Edges: []*Edge{
			{Name: "owner", Unique: true, Optional: false, Rel: Relation{Type: M2O}},
			{Name: "posts", Rel: Relation{Type: O2M}},
		},
	}
	assert.Equal(t, 2, typ.NumConstraint()) // 1 unique field + 1 HasConstraint edge
}

func TestType_RuntimeMixin(t *testing.T) {
	// No hooks/interceptors/policies and no mixed-in defaults → false.
	typ := Type{
		schema: &load.Schema{},
		Fields: []*Field{},
	}
	assert.False(t, typ.RuntimeMixin())

	// With mixin hooks → true.
	typ2 := Type{
		schema: &load.Schema{
			Hooks: []*load.Position{{MixinIndex: 0, MixedIn: true}},
		},
		Fields: []*Field{},
	}
	assert.True(t, typ2.RuntimeMixin())
}

// --- Template tests (template.go) ---

func TestNewTemplate(t *testing.T) {
	tmpl := NewTemplate("test")
	require.NotNil(t, tmpl)
	assert.Equal(t, "test", tmpl.Name())
}

func TestTemplate_Funcs(t *testing.T) {
	tmpl := NewTemplate("test")
	result := tmpl.Funcs(map[string]any{"custom": func() string { return "hello" }})
	assert.Equal(t, tmpl, result, "Funcs should return self for chaining")
}

func TestTemplate_SkipIf(t *testing.T) {
	tmpl := NewTemplate("test")
	result := tmpl.SkipIf(func(*Graph) bool { return true })
	assert.Equal(t, tmpl, result, "SkipIf should return self for chaining")
}

func TestTemplate_Parse(t *testing.T) {
	tmpl := NewTemplate("test")
	result, err := tmpl.Parse("{{ .Name }}")
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestMustParse(t *testing.T) {
	tmpl := NewTemplate("test")
	parsed, err := tmpl.Parse("{{ .Name }}")
	require.NoError(t, err)
	result := MustParse(parsed, nil)
	require.NotNil(t, result)

	assert.Panics(t, func() {
		MustParse(nil, assert.AnError)
	})
}

// --- EdgesWithID (type.go) ---

func TestSqlAnnotate_NilAnnotation(t *testing.T) {
	assert.Nil(t, sqlAnnotate(nil))
	assert.Nil(t, sqlAnnotate(map[string]any{}))
	assert.Nil(t, sqlAnnotate(map[string]any{"sql": nil}))
}

func TestSqlAnnotate_ValidAnnotation(t *testing.T) {
	a := sqlAnnotate(map[string]any{
		"sql": map[string]any{
			"ColumnType":  "JSONB",
			"Check":       "age >= 0",
			"DefaultExpr": "gen_random_uuid()",
		},
	})
	require.NotNil(t, a)
	assert.Equal(t, "JSONB", a.ColumnType)
	assert.Equal(t, "age >= 0", a.Check)
	assert.Equal(t, "gen_random_uuid()", a.DefaultExpr)
}

func TestValidateSQLAnnotation_InvalidColumnType(t *testing.T) {
	err := validateSQLAnnotation(map[string]any{
		"sql": map[string]any{"ColumnType": "DROP TABLE users"},
	})
	assert.Error(t, err)
}

func TestValidateSQLAnnotation_InvalidCheck(t *testing.T) {
	err := validateSQLAnnotation(map[string]any{
		"sql": map[string]any{"Check": "DELETE FROM users"},
	})
	assert.Error(t, err)
}

func TestValidateSQLAnnotation_InvalidDefaultExpr(t *testing.T) {
	err := validateSQLAnnotation(map[string]any{
		"sql": map[string]any{"DefaultExpr": "TRUNCATE TABLE users"},
	})
	assert.Error(t, err)
}

func TestType_EdgesWithID(t *testing.T) {
	normalType := &Type{Name: "Post", ID: &Field{Name: "id"}}
	compositeType := &Type{Name: "Membership"}
	compositeType.EdgeSchema.To = &Edge{Name: "to"}
	compositeType.EdgeSchema.ID = []*Field{{Name: "a"}, {Name: "b"}}

	typ := Type{Edges: []*Edge{
		{Name: "posts", Type: normalType, Rel: Relation{Type: O2M}},
		{Name: "memberships", Type: compositeType, Rel: Relation{Type: O2M}},
	}}
	edges := typ.EdgesWithID()
	assert.Len(t, edges, 1)
	assert.Equal(t, "posts", edges[0].Name)
}
