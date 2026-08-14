package sql

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// field.Sensitive() marks a value that must never be readable back —
// a password hash, an invite token, an API secret.
//
// Before 2026-08-14 velox carried Ent's *validation* for the flag
// (Type.check rejects a sensitive field with struct tags) but none of
// its *behavior*: the generated struct kept a normal json tag and
// String() printed the value verbatim. A field marked Sensitive still
// reached every log line formatting the entity and every response
// marshaling it.
//
// Both surfaces are pinned here. The GraphQL surface is pinned
// separately in contrib/graphql.
func sensitiveUserSource(t testing.TB) string {
	t.Helper()
	h := newMockHelper()
	h.rootPkg = "github.com/test/project"
	userType := createTypeWithSchemaFields(t, "User", []*load.Field{
		{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
		{Name: "password", Info: &field.TypeInfo{Type: field.TypeString}, Sensitive: true},
	})
	h.graph.Nodes = []*gen.Type{userType}
	f := genEntityPkgFileWithRegistry(h, userType, h.graph.Nodes, nil)
	require.NotNil(t, f)
	return f.GoString()
}

func TestSensitiveField_StructTagIsJSONDash(t *testing.T) {
	src := sensitiveUserSource(t)

	assert.Contains(t, src, "`json:\"-\"`",
		"a sensitive field must be tagged json:\"-\" so it is never marshaled "+
			"into an API response or a structured log")
	assert.NotContains(t, src, "json:\"password",
		"the sensitive field must not keep its normal json tag")
	assert.Contains(t, src, "json:\"name,omitempty\"",
		"non-sensitive fields keep their normal tag")
}

func TestSensitiveField_StringMethodMasksValue(t *testing.T) {
	src := sensitiveUserSource(t)

	require.Contains(t, src, `"password=<sensitive>"`,
		"String() must print a placeholder — String() is what %%v on an entity "+
			"reaches, so an unmasked value lands in every log line")

	body := src[strings.Index(src, "func (e *User) String()"):]
	assert.NotContains(t, body, "e.Password",
		"String() must not read the sensitive field at all")
	assert.Contains(t, body, "e.Name",
		"non-sensitive fields are still printed")
}
