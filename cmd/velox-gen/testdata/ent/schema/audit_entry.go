package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
)

// AuditEntry is ent's table of a 60-byte name, whose primary key and
// sequence PostgreSQL names itself, shortening the table.
type AuditEntry struct{ schema.Schema }

// Annotations returns its table.
func (AuditEntry) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("audit_entries_kept_for_every_change_an_editor_makes_to_posts")}
}

// Fields returns the column after the ID.
func (AuditEntry) Fields() []schema.Field {
	return []schema.Field{field.String("note").MaxLen(255)}
}
