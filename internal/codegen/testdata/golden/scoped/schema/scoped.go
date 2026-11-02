//go:build ignore

package schema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

func org() *field.NumberBuilder[int64] { return field.Int64("org_id").Immutable() }

// Note is scoped by org, and declares neither Policy nor Hooks.
type Note struct{ schema.Schema }

func (Note) Fields() []schema.Field {
	return []schema.Field{org().Annotations(schema.Scoped("org")), field.String("title")}
}

// Task is scoped by org, and declares both (the golden Config binds them).
type Task struct{ schema.Schema }

func (Task) Fields() []schema.Field {
	return []schema.Field{org().Annotations(schema.Scoped("org")), field.String("title")}
}

// Memo is scoped by org and declares only a Policy.
type Memo struct{ schema.Schema }

func (Memo) Fields() []schema.Field {
	return []schema.Field{org().Annotations(schema.Scoped("org"))}
}

// Tag is scoped by org and declares only Hooks.
type Tag struct{ schema.Schema }

func (Tag) Fields() []schema.Field {
	return []schema.Field{org().Annotations(schema.Scoped("org"))}
}

// Home is scoped by a string, under another key.
type Home struct{ schema.Schema }

func (Home) Fields() []schema.Field {
	return []schema.Field{field.String("region").Immutable().Annotations(schema.Scoped("region"))}
}

// Owner has no scope.
type Owner struct{ schema.Schema }

func (Owner) Fields() []schema.Field { return []schema.Field{field.String("name")} }
