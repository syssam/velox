package gen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

// TestSchema_RejectsAdversarialFieldNames asserts that the schema boundary
// refuses field names containing SQL metacharacters — quotes, semicolons,
// comments, whitespace, null bytes, unicode smugglers. This is the primary
// defense; the SQL builder's Quote() wraps identifiers but does not escape
// embedded quote chars, so a bypass here would be directly exploitable.
//
// Valid Go identifiers are required because field names become both Go
// struct fields and SQL column names; requiring a single validation rule
// that satisfies both aligns the security boundary with the generator's
// existing assumption.
func TestSchema_RejectsAdversarialFieldNames(t *testing.T) {
	payloads := []struct {
		name    string
		payload string
	}{
		{"embedded_double_quote", `name"; DROP TABLE users; --`},
		{"embedded_single_quote", `'; DROP TABLE users; --`},
		{"embedded_backtick", "name`DROP"},
		{"embedded_semicolon", "name;TRUNCATE"},
		{"embedded_sql_line_comment", "name-- bypass"},
		{"embedded_sql_block_comment", "name/*bypass*/"},
		{"embedded_boolean_payload", "name OR 1=1"},
		{"embedded_whitespace", "name bypass"},
		{"embedded_tab", "name\tbypass"},
		{"embedded_newline", "name\nbypass"},
		{"embedded_null_byte", "name\x00null"},
		{"leading_digit", "1name"},
		{"unicode_quote", "name\u2018bypass"},
		{"non_breaking_space", "name\u00a0bypass"},
		{"parentheses", "name(x)"},
		{"dash", "name-bypass"},
	}

	for _, p := range payloads {
		t.Run(p.name, func(t *testing.T) {
			_, err := NewType(&Config{Package: "example.com/app"}, &load.Schema{
				Name: "User",
				Fields: []*load.Field{
					{Name: p.payload, Info: &field.TypeInfo{Type: field.TypeString}},
				},
			})
			require.Error(t, err, "adversarial field name %q should be rejected", p.payload)
			assert.Contains(t, err.Error(), "not a valid identifier",
				"expected identifier validation error for %q, got: %v", p.payload, err)
		})
	}
}

// TestGraph_RejectsAdversarialEdgeNames asserts the same validation applies
// to edge names, which also flow into generated code (QueryXxx methods,
// join-table foreign-key columns).
func TestGraph_RejectsAdversarialEdgeNames(t *testing.T) {
	payloads := []string{
		`posts"; DROP`,
		"posts; SELECT",
		"posts OR 1=1",
		"posts-comments",
		"posts\x00null",
	}
	for _, p := range payloads {
		t.Run(p, func(t *testing.T) {
			postType := &Type{
				Name: "Post",
				ID:   &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
			}
			userType := &Type{
				Name:  "User",
				ID:    &Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
				Edges: []*Edge{{Name: p, Type: postType}},
			}
			g := &Graph{
				Config: &Config{Package: "example.com/app"},
				Nodes:  []*Type{userType, postType},
				nodes:  map[string]*Type{"User": userType, "Post": postType},
			}
			err := g.Validate()
			require.Error(t, err, "adversarial edge name %q should be rejected", p)
			assert.Contains(t, err.Error(), "not a valid identifier",
				"expected identifier validation error for %q, got: %v", p, err)
		})
	}
}

// TestSchema_AcceptsRealisticFieldNames is the inverse — these names must
// NOT be falsely rejected by the hardening above. Includes Go reserved
// keywords ("type", "func", "range", "select", "map") — they PascalCase
// into valid struct fields (Type, Func, Range) and Builder.Ident quotes
// them as SQL identifiers.
func TestSchema_AcceptsRealisticFieldNames(t *testing.T) {
	names := []string{
		"name", "email", "created_at", "user_id", "_private", "camelCase",
		"PascalCase", "field1", "a", strings.Repeat("x", 64),
		// Go reserved keywords commonly used as schema field names.
		"type", "func", "range", "select", "map", "chan", "interface",
	}
	for _, n := range names {
		t.Run(n, func(t *testing.T) {
			_, err := NewType(&Config{Package: "example.com/app"}, &load.Schema{
				Name: "User",
				Fields: []*load.Field{
					{Name: n, Info: &field.TypeInfo{Type: field.TypeString}},
				},
			})
			assert.NoError(t, err, "field name %q should be accepted", n)
		})
	}
}
