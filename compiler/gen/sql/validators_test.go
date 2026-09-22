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

	// The leaf package declares the validator variables.
	pkgSrc := genPackage(helper, typ, buildEntityPkgEnumRegistry(helper.graph.Nodes)).GoString()
	for _, want := range []string{"NameValidator func(", "RoleValidator func("} {
		if !strings.Contains(pkgSrc, want) {
			t.Errorf("package does not declare %q", want)
		}
	}
	if strings.Contains(pkgSrc, "BioValidator") {
		t.Error("package declares a validator for a field that has none")
	}

	// The runtime init assigns them — a declared, unassigned validator is a
	// nil func that panics on the first Save.
	rtSrc := genEntityRuntime(helper, typ).GoString()
	for _, want := range []string{"user.NameValidator =", "user.RoleValidator ="} {
		if !strings.Contains(rtSrc, want) {
			t.Errorf("runtime init does not assign %q\n%s", want, rtSrc)
		}
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

// TestEnumOnlyValidatorIsAssignedAtInit covers a type whose only runtime
// field code is an enum validator: no default, no schema validator. The
// runtime init used to be skipped for such a type (Type.HasValidators ignores
// enums), leaving <Enum>Validator nil while create check() called it.
func TestEnumOnlyValidatorIsAssignedAtInit(t *testing.T) {
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
	rtSrc := genEntityRuntime(helper, typ).GoString()
	if !strings.Contains(rtSrc, "ticket.StatusValidator =") {
		t.Errorf("enum-only type never assigns StatusValidator; create check() would call a nil func\n%s", rtSrc)
	}
}
