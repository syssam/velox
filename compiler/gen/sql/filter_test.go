package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
)

func TestGenFilter_BasicEntity(t *testing.T) {
	helper := newMockHelper()
	testType := createTestType("User")
	helper.graph.Nodes = []*gen.Type{testType}

	file := genFilter(helper, testType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "UserFilter")
	assert.Contains(t, code, "WhereP")
	assert.Contains(t, code, "Where")
	assert.Contains(t, code, "HasColumn")
}

func TestGenFilter_WhereP_ImplementsFilterInterface(t *testing.T) {
	helper := newMockHelper()
	testType := createTestType("Post")
	helper.graph.Nodes = []*gen.Type{testType}

	file := genFilter(helper, testType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "PostFilter")
	assert.Contains(t, code, "func (f *PostFilter) WhereP")
	assert.Contains(t, code, "sql.Selector")
}

func TestGenFilter_Where_TypeSafe(t *testing.T) {
	helper := newMockHelper()
	testType := createTestType("Comment")
	helper.graph.Nodes = []*gen.Type{testType}

	file := genFilter(helper, testType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "CommentFilter")
	assert.Contains(t, code, "func (f *CommentFilter) Where")
	assert.Contains(t, code, "predicate.Comment")
}

func TestGenFilter_HasColumn(t *testing.T) {
	helper := newMockHelper()
	testType := createTestType("Tag")
	helper.graph.Nodes = []*gen.Type{testType}

	file := genFilter(helper, testType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "TagFilter")
	assert.Contains(t, code, "func (f *TagFilter) HasColumn")
	assert.Contains(t, code, "slices.Contains")
}

func TestGenFilter_StructDefinition(t *testing.T) {
	helper := newMockHelper()
	testType := createTestType("Order")
	helper.graph.Nodes = []*gen.Type{testType}

	file := genFilter(helper, testType)
	require.NotNil(t, file)

	code := file.GoString()
	assert.Contains(t, code, "type OrderFilter struct")
	assert.Contains(t, code, "config")
	assert.Contains(t, code, "predicates")
}

func TestGenFilter_MultipleEntities(t *testing.T) {
	helper := newMockHelper()
	userType := createTestType("User")
	postType := createTestType("Post")
	helper.graph.Nodes = []*gen.Type{userType, postType}

	for _, typ := range []*gen.Type{userType, postType} {
		file := genFilter(helper, typ)
		require.NotNil(t, file, "genFilter returned nil for %s", typ.Name)

		code := file.GoString()
		filterName := typ.Name + "Filter"
		assert.Contains(t, code, filterName)
		assert.Contains(t, code, "WhereP")
		assert.Contains(t, code, "Where")
		assert.Contains(t, code, "HasColumn")
	}
}
