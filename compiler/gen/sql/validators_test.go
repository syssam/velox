package sql

import (
	"strings"
	"testing"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// validatorFixture builds a User with a schema-validated field (name), a
// required enum with no default (role) and an unvalidated field (bio), on a
// graph with NO feature flags enabled.
func validatorFixture(t *testing.T) (*mockHelper, *gen.Type) {
	t.Helper()
	typ := createTestTypeWithSchema(t, "User", &load.Schema{
		Fields: []*load.Field{
			{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}, Validators: 1},
			{
				Name:  "role",
				Info:  &field.TypeInfo{Type: field.TypeEnum},
				Enums: []struct{ N, V string }{{N: "admin", V: "admin"}, {N: "user", V: "user"}},
			},
			{Name: "bio", Info: &field.TypeInfo{Type: field.TypeString}, Optional: true},
		},
	})
	helper := newMockHelper()
	helper.graph = &gen.Graph{
		Config: &gen.Config{Package: "github.com/test/project/ent"},
		Nodes:  []*gen.Type{typ},
	}
	return helper, typ
}

// TestValidatorsGeneratedWithoutFeatureFlag pins Ent parity: schema validators
// (NotEmpty, MaxLen, Range, custom Validate) and the enum validator are always
// generated and enforced on CREATE and UPDATE. They used to be gated on the
// opt-in FeatureValidator (default off), so a default project accepted
// SetName("").SetRole("bogus") with no error — every validator declared on
// the schema was silently dead. Ent emits them unconditionally
// (entc/gen/template/builder/create.tmpl).
func TestValidatorsGeneratedWithoutFeatureFlag(t *testing.T) {
	helper, typ := validatorFixture(t)
	if len(helper.graph.Features) != 0 {
		t.Fatal("fixture must not enable any feature")
	}

	// The leaf package declares the schema validator variable and the enum
	// validator function.
	pkgSrc := genPackage(helper, typ, buildEntityPkgEnumRegistry(helper.graph.Nodes)).GoString()
	for _, want := range []string{"NameValidator func(string) error", "func RoleValidator(v Role) error"} {
		if !strings.Contains(pkgSrc, want) {
			t.Errorf("package does not declare %q", want)
		}
	}
	if strings.Contains(pkgSrc, "BioValidator") {
		t.Error("package declares a validator for a field that has none")
	}

	// The runtime init assigns the schema validator — a declared, unassigned
	// validator is a nil func that panics on the first Save. The enum
	// validator is a function and is not assigned.
	rtSrc := genEntityRuntime(helper, typ).GoString()
	if !strings.Contains(rtSrc, "user.NameValidator =") {
		t.Errorf("runtime init does not assign user.NameValidator\n%s", rtSrc)
	}
	if strings.Contains(rtSrc, "RoleValidator") {
		t.Errorf("runtime init assigns the enum validator function\n%s", rtSrc)
	}

	// Create check() calls both.
	createFile, err := genCreate(helper, typ)
	if err != nil {
		t.Fatalf("genCreate: %v", err)
	}
	body := funcBody(t, createFile.GoString(), "func (c *UserCreate) check() error {")
	for _, want := range []string{"user.NameValidator(v)", "user.RoleValidator(v)"} {
		if !strings.Contains(body, want) {
			t.Errorf("create check() does not call %s\n%s", want, body)
		}
	}

	// Both update builders' check() call both.
	updateFile, err := genUpdate(helper, typ)
	if err != nil {
		t.Fatalf("genUpdate: %v", err)
	}
	updateSrc := updateFile.GoString()
	for _, builder := range []string{"UserUpdate", "UserUpdateOne"} {
		body := funcBody(t, updateSrc, "func (_u *"+builder+") check() error {")
		for _, want := range []string{"user.NameValidator(v)", "user.RoleValidator(v)"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s.check() does not call %s\n%s", builder, want, body)
			}
		}
		if strings.Contains(body, "BioValidator") {
			t.Errorf("%s.check() validates a field that declares no validator", builder)
		}
	}
}

// TestEnumOnlyValidatorIsNeverNil covers a type whose only validation is an
// enum: no default, no schema validator. The runtime init used to be skipped
// for such a type (Type.HasValidators ignores enums), leaving a var
// <Enum>Validator nil while create check() called it. Enum validators are now
// generated functions, as in Ent, so there is nothing to assign and nothing
// that can be nil.
func TestEnumOnlyValidatorIsNeverNil(t *testing.T) {
	typ := createTestTypeWithSchema(t, "Ticket", &load.Schema{
		Fields: []*load.Field{{
			Name:  "status",
			Info:  &field.TypeInfo{Type: field.TypeEnum},
			Enums: []struct{ N, V string }{{N: "open", V: "open"}, {N: "closed", V: "closed"}},
		}},
	})
	helper := newMockHelper()
	helper.graph = &gen.Graph{
		Config: &gen.Config{Package: "github.com/test/project/ent"},
		Nodes:  []*gen.Type{typ},
	}
	pkgSrc := genPackage(helper, typ, buildEntityPkgEnumRegistry(helper.graph.Nodes)).GoString()
	if !strings.Contains(pkgSrc, "func StatusValidator(v Status) error {") {
		t.Errorf("enum validator is not a generated function\n%s", pkgSrc)
	}
	if strings.Contains(pkgSrc, "StatusValidator func(") {
		t.Errorf("enum validator is declared as a variable that could be nil\n%s", pkgSrc)
	}
	createFile, err := genCreate(helper, typ)
	if err != nil {
		t.Fatalf("genCreate: %v", err)
	}
	if !strings.Contains(createFile.GoString(), "ticket.StatusValidator(v)") {
		t.Error("create check() does not call the enum validator")
	}
}
