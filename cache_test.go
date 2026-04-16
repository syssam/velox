package velox_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox"
)

func TestCacheKey_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		key      velox.CacheKey
		expected string
	}{
		{
			name: "all fields populated",
			key: velox.CacheKey{
				Table:      "users",
				Operation:  "query",
				Predicates: "age>18",
				OrderBy:    "name ASC",
				Limit:      10,
				Offset:     20,
			},
			expected: "users\x00query\x00age>18\x00name ASC\x0010\x0020",
		},
		{
			name: "empty fields",
			key: velox.CacheKey{
				Table:      "",
				Operation:  "",
				Predicates: "",
				OrderBy:    "",
			},
			expected: "\x00\x00\x00\x00" + "0\x000",
		},
		{
			name: "table and operation only",
			key: velox.CacheKey{
				Table:     "posts",
				Operation: "count",
			},
			expected: "posts\x00count\x00\x00\x000\x000",
		},
		{
			name: "all fields with special characters",
			key: velox.CacheKey{
				Table:      "user_posts",
				Operation:  "select",
				Predicates: "status='active' AND role='admin'",
				OrderBy:    "created_at DESC",
			},
			expected: "user_posts\x00select\x00status='active' AND role='admin'\x00created_at DESC\x000\x000",
		},
		{
			name: "different limit and offset produce different keys",
			key: velox.CacheKey{
				Table:      "users",
				Operation:  "query",
				Predicates: "age>18",
				OrderBy:    "name ASC",
				Limit:      10,
				Offset:     100,
			},
			expected: "users\x00query\x00age>18\x00name ASC\x0010\x00100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.key.String())
		})
	}
}

func TestCacheKey_ZeroValue(t *testing.T) {
	t.Parallel()

	var key velox.CacheKey
	assert.Equal(t, "\x00\x00\x00\x000\x000", key.String())
	assert.Equal(t, 0, key.Limit)
	assert.Equal(t, 0, key.Offset)
}
