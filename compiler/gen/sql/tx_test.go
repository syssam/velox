package sql

import (
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

func TestGenTx_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genTx(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "type Tx struct")
	assert.Contains(t, code, "Commit")
	assert.Contains(t, code, "Rollback")
}

func TestGenTx_MultipleEntities(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{
		createTestType("User"),
		createTestType("Post"),
	}

	file := genTx(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "type Tx struct")
	assert.Contains(t, code, "User")
	assert.Contains(t, code, "Post")
}

func TestGenTxExecQueryMethods(t *testing.T) {
	f := jen.NewFile("ent")

	genTxExecQueryMethods(f)

	code := f.GoString()
	assert.Contains(t, code, "ExecContext")
	assert.Contains(t, code, "QueryContext")
	assert.Contains(t, code, "database/sql")
	assert.Contains(t, code, "Tx.ExecContext is not supported")
	assert.Contains(t, code, "Tx.QueryContext is not supported")
}
