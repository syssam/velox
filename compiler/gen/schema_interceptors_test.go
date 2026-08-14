package gen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/load"
)

// Schema- and mixin-level Interceptors() must be rejected at codegen time.
//
// velox used to accept them silently. The loader read them, codegen emitted
// `<pkg>.Interceptors[i] = <schema>.Interceptors()[i]` into the generated
// init(), and no execution path ever read that array back — every query
// runs against the client's *entity.InterceptorStore. A schema declaring
// them compiled, generated, assigned, and did nothing, with no warning.
// For a hook whose usual job is authorization, silent inertness is the
// worst available outcome.
//
// Wiring them up instead was considered and rejected: a schema-level
// interceptor applies to every client in the process, including the client
// an application uses for internal invariant checks (a dependency Exist()
// before a delete, a uniqueness probe, a lock acquisition). Narrowing those
// corrupts data rather than leaking it, and nothing detects it afterwards.
//
// If this test is ever changed to allow schema interceptors, the two-handle
// pattern in tests/integration/e2e_authz_two_handle_test.go stops being
// expressible, and docs/privacy.md's guidance on invariant reads becomes
// wrong.
func TestValidate_RejectsSchemaLevelInterceptors(t *testing.T) {
	tests := []struct {
		name   string
		inters []*load.Position
	}{
		{"declared on the schema", []*load.Position{{Index: 0}}},
		{"declared on a mixin", []*load.Position{{Index: 0, MixedIn: true, MixinIndex: 0}}},
		{"several", []*load.Position{{Index: 0}, {Index: 1, MixedIn: true}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := &Graph{Nodes: []*Type{{
				Name:   "Invoice",
				schema: &load.Schema{Name: "Invoice", Interceptors: tc.inters},
			}}}

			err := g.Validate()
			require.Error(t, err, "a schema declaring Interceptors() must not generate")
			assert.Contains(t, err.Error(), "schema-level Interceptors() is not supported")
			assert.Contains(t, err.Error(), "Policy()",
				"the error must point at the mechanism that does work for authorization")
			assert.Contains(t, err.Error(), "client.Invoice.Intercept(...)",
				"and at the per-client escape hatch, named for this entity")
		})
	}
}

// A schema without Interceptors() is unaffected.
func TestValidate_AllowsSchemaWithoutInterceptors(t *testing.T) {
	g := &Graph{Nodes: []*Type{{
		Name:   "Invoice",
		schema: &load.Schema{Name: "Invoice"},
	}}}

	assert.NoError(t, g.Validate())
}
