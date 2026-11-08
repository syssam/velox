package schema

import (
	"crypto/rand"

	"github.com/syssam/velox/internal/testbind/bindtype"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// NewPaperID is a random UUID, Paper's default key.
func NewPaperID() bindtype.UUID {
	var u bindtype.UUID
	_, _ = rand.Read(u[:])
	u[6] = u[6]&0x0f | 0x40 // version 4
	u[8] = u[8]&0x3f | 0x80 // RFC 4122 variant
	return u
}

// Paper has a UUID key, a default for it, a Version column, Remarks keyed
// to it and Topics, keyed by string, through a join table.
type Paper struct{ schema.Schema }

// Fields returns the UUID key, the title and the optimistic-locking
// version.
func (Paper) Fields() []schema.Field {
	return []schema.Field{
		field.UUID("id", bindtype.UUID{}).Default(NewPaperID),
		field.String("title"),
		field.Int64("version").Version(),
	}
}

// Edges returns Remarks, to-many by remarks.paper_id, and Topics, many to
// many through paper_topics.
func (Paper) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("remarks", Remark.Type),
		edge.To("topics", Topic.Type).StorageKey(edge.Table("paper_topics"), edge.Columns("paper_id", "topic_id")),
	}
}

// Topic has a string key that the application chooses.
type Topic struct{ schema.Schema }

// Fields returns the string key and the name.
func (Topic) Fields() []schema.Field {
	return []schema.Field{field.String("id").MaxLen(36), field.String("name")}
}

// Edges returns Papers, the inverse of Paper's Topics.
func (Topic) Edges() []schema.Edge {
	return []schema.Edge{edge.From("papers", Paper.Type).Ref("topics")}
}

// Remark has an int64 key and a UUID key to its Paper.
type Remark struct{ schema.Schema }

// Fields returns the paper's key and the body.
func (Remark) Fields() []schema.Field {
	return []schema.Field{field.UUID("paper_id", bindtype.UUID{}), field.String("body")}
}

// Edges returns Paper, the inverse of Paper's Remarks, by paper_id.
func (Remark) Edges() []schema.Edge {
	return []schema.Edge{edge.From("paper", Paper.Type).Ref("remarks").Unique().Required().Field("paper_id")}
}
