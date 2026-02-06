package sql

import (
	"testing"

	"github.com/dave/jennifer/jen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

func TestGenClient_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{createTestType("User")}

	file := genClient(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "type Client struct")
	assert.Contains(t, code, "func NewClient")
	assert.Contains(t, code, "UserClient")
}

func TestGenClient_MultipleEntities(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{
		createTestType("User"),
		createTestType("Post"),
		createTestType("Comment"),
	}

	file := genClient(helper)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "UserClient")
	assert.Contains(t, code, "PostClient")
	assert.Contains(t, code, "CommentClient")
}

func TestGenConfigExecQueryMethods(t *testing.T) {
	helper := newMockHelper()
	f := jen.NewFile("ent")

	genConfigExecQueryMethods(helper, f)

	code := f.GoString()
	assert.Contains(t, code, "ExecContext")
	assert.Contains(t, code, "QueryContext")
	assert.Contains(t, code, "database/sql")
	assert.Contains(t, code, "Driver.ExecContext is not supported")
	assert.Contains(t, code, "Driver.QueryContext is not supported")
}

func TestGenHooksStruct(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{
		createTestType("User"),
		createTestType("Post"),
	}

	f := jen.NewFile("ent")
	genHooksStruct(helper, f)

	code := f.GoString()
	assert.Contains(t, code, "type hooks struct")
	assert.Contains(t, code, "User")
	assert.Contains(t, code, "Post")
}

func TestGenIntersStruct(t *testing.T) {
	helper := newMockHelper()
	helper.graph.Nodes = []*gen.Type{
		createTestType("User"),
	}

	f := jen.NewFile("ent")
	genIntersStruct(helper, f)

	code := f.GoString()
	assert.Contains(t, code, "type inters struct")
	assert.Contains(t, code, "User")
}

func TestGenOptions(t *testing.T) {
	helper := newMockHelper()

	f := jen.NewFile("ent")
	genOptions(helper, f)

	code := f.GoString()
	assert.Contains(t, code, "Option")
	assert.Contains(t, code, "Driver")
	assert.Contains(t, code, "Log")
}
