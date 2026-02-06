package failure

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
)

// User holds a schema that causes a failure during load.
type User struct {
	velox.Schema
}

// Fields panics intentionally to test error handling during schema loading.
func (User) Fields() []velox.Field {
	// This panic will be caught by safeFields and returned as an error.
	panic("intentional panic in Fields() for testing error handling")
	return []velox.Field{
		field.String("name"),
	}
}
