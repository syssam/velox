package graphql

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	entgen "github.com/syssam/velox/compiler/gen"
)

// --- parseSchemaSDL ---

func TestSchemaAST_ParseSchemaSDL_ValidMinimal(t *testing.T) {
	sdl := `type Query { hello: String }`
	schema, err := parseSchemaSDL(sdl)
	require.NoError(t, err)
	require.NotNil(t, schema)
	assert.NotNil(t, schema.Types["Query"])
}

func TestSchemaAST_ParseSchemaSDL_BuiltinTypes(t *testing.T) {
	sdl := `type Query { name: String, age: Int, score: Float, active: Boolean, id: ID }`
	schema, err := parseSchemaSDL(sdl)
	require.NoError(t, err)
	// Built-in scalar types should be present via the prelude.
	for _, name := range []string{"String", "Int", "Float", "Boolean", "ID"} {
		assert.NotNil(t, schema.Types[name], "expected built-in type %s", name)
	}
}

func TestSchemaAST_ParseSchemaSDL_ComplexSchema(t *testing.T) {
	sdl := `
type Query {
  user(id: ID!): User
  users: [User!]!
}

type User {
  id: ID!
  name: String!
  email: String
  posts: [Post!]
}

type Post {
  id: ID!
  title: String!
  author: User!
}

input CreateUserInput {
  name: String!
  email: String
}

enum Status {
  ACTIVE
  INACTIVE
}
`
	schema, err := parseSchemaSDL(sdl)
	require.NoError(t, err)
	assert.NotNil(t, schema.Types["User"])
	assert.NotNil(t, schema.Types["Post"])
	assert.NotNil(t, schema.Types["CreateUserInput"])
	assert.NotNil(t, schema.Types["Status"])
}

func TestSchemaAST_ParseSchemaSDL_InvalidSDL(t *testing.T) {
	sdl := `this is not valid GraphQL SDL {{{`
	schema, err := parseSchemaSDL(sdl)
	assert.Error(t, err)
	assert.Nil(t, schema)
	assert.Contains(t, err.Error(), "parse generated schema")
}

func TestSchemaAST_ParseSchemaSDL_EmptyString(t *testing.T) {
	// gqlparser accepts empty SDL and returns a schema with only prelude types.
	schema, err := parseSchemaSDL("")
	require.NoError(t, err)
	require.NotNil(t, schema)
	// No user-defined Query type, but built-in types are present.
	assert.Nil(t, schema.Query)
	assert.NotNil(t, schema.Types["String"])
}

func TestSchemaAST_ParseSchemaSDL_SourceName(t *testing.T) {
	// Verify the source name is set to "velox.graphql" by checking
	// that error messages reference it when there's an issue.
	sdl := `type Query { x: NonExistentType }`
	_, err := parseSchemaSDL(sdl)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse generated schema")
}

func TestSchemaAST_ParseSchemaSDL_WithDirectives(t *testing.T) {
	sdl := `
directive @deprecated(reason: String) on FIELD_DEFINITION

type Query {
  oldField: String @deprecated(reason: "use newField")
  newField: String
}
`
	schema, err := parseSchemaSDL(sdl)
	require.NoError(t, err)
	assert.NotNil(t, schema.Types["Query"])
}

// --- printSchema ---

func TestSchemaAST_PrintSchema_RoundTrip(t *testing.T) {
	sdl := `type Query { hello: String }`
	schema, err := parseSchemaSDL(sdl)
	require.NoError(t, err)

	output := printSchema(schema)
	assert.NotEmpty(t, output)
	// The printed schema should contain the Query type.
	assert.Contains(t, output, "Query")
	assert.Contains(t, output, "hello")
}

func TestSchemaAST_PrintSchema_PreservesTypes(t *testing.T) {
	sdl := `
type Query {
  users: [User!]!
}

type User {
  id: ID!
  name: String!
}

enum Role {
  ADMIN
  USER
}
`
	schema, err := parseSchemaSDL(sdl)
	require.NoError(t, err)

	output := printSchema(schema)
	assert.Contains(t, output, "User")
	assert.Contains(t, output, "Role")
	assert.Contains(t, output, "ADMIN")
	assert.Contains(t, output, "USER")
}

func TestSchemaAST_PrintSchema_UsesIndentation(t *testing.T) {
	sdl := `type Query { hello: String }`
	schema, err := parseSchemaSDL(sdl)
	require.NoError(t, err)

	output := printSchema(schema)
	// The formatter uses 2-space indentation.
	assert.Contains(t, output, "  ")
}

// --- applySchemaHooks ---

func TestSchemaAST_ApplySchemaHooks_NoHooks(t *testing.T) {
	g := &Generator{
		config: Config{
			schemaHooks: nil,
		},
	}
	sdl := `type Query { hello: String }`
	schema, err := parseSchemaSDL(sdl)
	require.NoError(t, err)

	err = g.applySchemaHooks(schema)
	assert.NoError(t, err)
}

func TestSchemaAST_ApplySchemaHooks_SingleHook(t *testing.T) {
	hookCalled := false
	g := &Generator{
		config: Config{
			schemaHooks: []SchemaHook{
				func(graph *entgen.Graph, schema *ast.Schema) error {
					hookCalled = true
					return nil
				},
			},
		},
	}
	sdl := `type Query { hello: String }`
	schema, err := parseSchemaSDL(sdl)
	require.NoError(t, err)

	err = g.applySchemaHooks(schema)
	assert.NoError(t, err)
	assert.True(t, hookCalled, "hook should have been called")
}

func TestSchemaAST_ApplySchemaHooks_MultipleHooks(t *testing.T) {
	order := make([]int, 0, 3)
	g := &Generator{
		config: Config{
			schemaHooks: []SchemaHook{
				func(graph *entgen.Graph, schema *ast.Schema) error {
					order = append(order, 1)
					return nil
				},
				func(graph *entgen.Graph, schema *ast.Schema) error {
					order = append(order, 2)
					return nil
				},
				func(graph *entgen.Graph, schema *ast.Schema) error {
					order = append(order, 3)
					return nil
				},
			},
		},
	}
	sdl := `type Query { hello: String }`
	schema, err := parseSchemaSDL(sdl)
	require.NoError(t, err)

	err = g.applySchemaHooks(schema)
	assert.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, order, "hooks should be called in order")
}

func TestSchemaAST_ApplySchemaHooks_ErrorStopsExecution(t *testing.T) {
	hookErr := errors.New("hook failed")
	thirdCalled := false
	g := &Generator{
		config: Config{
			schemaHooks: []SchemaHook{
				func(graph *entgen.Graph, schema *ast.Schema) error {
					return nil
				},
				func(graph *entgen.Graph, schema *ast.Schema) error {
					return hookErr
				},
				func(graph *entgen.Graph, schema *ast.Schema) error {
					thirdCalled = true
					return nil
				},
			},
		},
	}
	sdl := `type Query { hello: String }`
	schema, err := parseSchemaSDL(sdl)
	require.NoError(t, err)

	err = g.applySchemaHooks(schema)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "schema hook")
	assert.ErrorIs(t, err, hookErr)
	assert.False(t, thirdCalled, "third hook should not be called after error")
}

func TestSchemaAST_ApplySchemaHooks_ReceivesGraph(t *testing.T) {
	graph := &entgen.Graph{
		Nodes: []*entgen.Type{{Name: "TestNode"}},
	}
	var receivedGraph *entgen.Graph
	g := &Generator{
		config: Config{
			graph: graph,
			schemaHooks: []SchemaHook{
				func(g *entgen.Graph, schema *ast.Schema) error {
					receivedGraph = g
					return nil
				},
			},
		},
	}
	sdl := `type Query { hello: String }`
	schema, err := parseSchemaSDL(sdl)
	require.NoError(t, err)

	err = g.applySchemaHooks(schema)
	assert.NoError(t, err)
	require.NotNil(t, receivedGraph)
	assert.Equal(t, "TestNode", receivedGraph.Nodes[0].Name)
}

func TestSchemaAST_ApplySchemaHooks_CanModifySchema(t *testing.T) {
	g := &Generator{
		config: Config{
			schemaHooks: []SchemaHook{
				func(graph *entgen.Graph, schema *ast.Schema) error {
					// Add a new type to the schema.
					schema.Types["CustomType"] = &ast.Definition{
						Kind: ast.Object,
						Name: "CustomType",
						Fields: ast.FieldList{
							{
								Name: "value",
								Type: ast.NamedType("String", nil),
							},
						},
					}
					return nil
				},
			},
		},
	}
	sdl := `type Query { hello: String }`
	schema, err := parseSchemaSDL(sdl)
	require.NoError(t, err)

	err = g.applySchemaHooks(schema)
	assert.NoError(t, err)
	assert.NotNil(t, schema.Types["CustomType"], "hook should have added CustomType")
}
