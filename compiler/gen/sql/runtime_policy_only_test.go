package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// TestGenEntityRuntime_PolicyOrHooksWithoutRuntimeFields pins that a schema
// whose only runtime code is a Policy() or Hooks() declared on the schema
// itself (no defaults, no validators, no mixin) still gets it stitched. The
// init used to return early for such a type: RuntimePolicy stayed nil, so the
// entity silently had no policy, and the Hooks slots stayed empty, so every
// mutation failed with "uninitialized hook".
func TestGenEntityRuntime_PolicyOrHooksWithoutRuntimeFields(t *testing.T) {
	t.Parallel()
	helper := newMockHelper()
	helper.rootPkg = "github.com/test/project/ent"
	plain := []*load.Field{{Name: "title", Info: &field.TypeInfo{Type: field.TypeString}}}

	policyOnly := createTestTypeWithSchema(t, "Doc", &load.Schema{
		Fields: plain,
		Policy: []*load.Position{{Index: 0}},
	})
	require.Equal(t, 1, policyOnly.NumPolicy())
	code := schemaInitSrc(helper, policyOnly)
	assert.Contains(t, code, "RuntimePolicy = ")
	assert.Contains(t, code, `RegisterEntityPolicy("Doc"`)

	hooksOnly := createTestTypeWithSchema(t, "Note", &load.Schema{
		Fields: plain,
		Hooks:  []*load.Position{{Index: 0}},
	})
	require.Equal(t, 1, hooksOnly.NumHooks())
	code = schemaInitSrc(helper, hooksOnly)
	assert.Contains(t, code, "Hooks[0] = ")
}
