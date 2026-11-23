package fixture

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"
)

// PNote is INote's table without INote's behaviour: its policy and its
// interceptor call the functions below, which a test sets and resets.
// Each one unset allows.
type PNote struct {
	ID      int64
	OwnerID int64
	Body    string
	Hidden  bool
}

// The functions PNote's behaviour calls: its Read, Write and Insert
// policies, its Query rule, and its interceptor's Filter.
var (
	PNoteRead   func(ctx context.Context) (velox.Predicate, error)
	PNoteWrite  func(ctx context.Context) (velox.Predicate, error)
	PNoteInsert func(ctx context.Context, r velox.Row) error
	PNoteQuery  func(ctx context.Context, f privacy.Filter) error
	PNoteFilter func(ctx context.Context) (velox.Predicate, error)
)

// PNotes are PNote's handles.
var PNotes = struct {
	ID      velox.NumberCol[PNote, int64]
	OwnerID velox.NumberCol[PNote, int64]
	Body    velox.StringCol[PNote, string]
}{velox.NumberCol[PNote, int64]{I: 0}, velox.NumberCol[PNote, int64]{I: 1}, velox.StringCol[PNote, string]{I: 2}}

var pNoteMeta velox.MetaOnce[PNote]

// VeloxMeta returns PNote's metadata.
func (*PNote) VeloxMeta() *velox.Meta[PNote] { return pNoteMeta.Load(pNoteDescriptor) }

// Policy calls PNoteRead, PNoteWrite, PNoteInsert and PNoteQuery.
func (*PNote) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Predicate, error) {
			if PNoteRead == nil {
				return velox.Allow(), nil
			}
			return PNoteRead(ctx)
		},
		Write: func(ctx context.Context) (velox.Predicate, error) {
			if PNoteWrite == nil {
				return velox.Allow(), nil
			}
			return PNoteWrite(ctx)
		},
		Insert: func(ctx context.Context, r velox.Row) error {
			if PNoteInsert == nil {
				return nil
			}
			return PNoteInsert(ctx, r)
		},
		Query: privacy.QueryPolicy{privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
			if PNoteQuery == nil {
				return privacy.Skip
			}
			return PNoteQuery(ctx, f)
		})},
	}
}

// Interceptors calls PNoteFilter.
func (*PNote) Interceptors() []velox.Interceptor {
	return []velox.Interceptor{{Filter: func(ctx context.Context) (velox.Predicate, error) {
		if PNoteFilter == nil {
			return velox.Allow(), nil
		}
		return PNoteFilter(ctx)
	}}}
}

func pNoteDescriptor() velox.Descriptor[PNote] {
	return velox.Descriptor[PNote]{
		Table: "notes",
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"}, {Field: "OwnerID", Name: "owner_id"}, {Field: "Body", Name: "body"}, {Field: "Hidden", Name: "hidden"},
		},
		PrimaryKey: []int{0},
		Schema:     (*PNote)(nil),
		FieldPtr: func(x *PNote, i int) any {
			return [...]any{&x.ID, &x.OwnerID, &x.Body, &x.Hidden}[i]
		},
	}
}
