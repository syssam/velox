package fixture

import (
	"context"
	"errors"

	"github.com/syssam/velox"
)

// FilterMode is what INote's interceptor does under a context, set with
// WithFilterMode.
type FilterMode int

// The modes of INote's interceptor.
const (
	FilterHide FilterMode = iota // hide rows whose hidden is true: the default
	FilterSkip                   // no restriction
	FilterFail                   // return ErrFilter
	FilterZero                   // return the zero Pred
)

// ErrFilter is the error of INote's interceptor under FilterFail.
var ErrFilter = errors.New("filter failed")

type filterModeKey struct{}

// WithFilterMode returns ctx under which INote's interceptor does m.
func WithFilterMode(ctx context.Context, m FilterMode) context.Context {
	return velox.WithScopeValue(context.WithValue(ctx, filterModeKey{}, m), "filter", int(m))
}

// FilterCalls counts the calls of INote's interceptor.
var FilterCalls int

// IOwner has notes, a pinned note and tagged notes through IJoin.
type IOwner struct {
	ID       int64
	Name     string
	PinnedID *int64
	Notes    velox.Many[INote]
	Pinned   velox.One[INote]
	Tagged   velox.Many[INote]
}

// IOwners are IOwner's handles.
var IOwners = struct {
	ID       velox.NumberCol[IOwner, int64]
	Name     velox.StringCol[IOwner, string]
	PinnedID velox.NullNumberCol[IOwner, int64]
	Notes    velox.Rel[IOwner, INote]
	Pinned   velox.Rel[IOwner, INote]
	Tagged   velox.Rel[IOwner, INote]
}{
	velox.NumberCol[IOwner, int64]{I: 0}, velox.StringCol[IOwner, string]{I: 1}, velox.NullNumberCol[IOwner, int64]{I: 2},
	velox.Rel[IOwner, INote]{I: 0}, velox.Rel[IOwner, INote]{I: 1}, velox.Rel[IOwner, INote]{I: 2},
}

var iOwnerMeta velox.MetaOnce[IOwner]

// VeloxMeta returns IOwner's metadata.
func (*IOwner) VeloxMeta() *velox.Meta[IOwner] { return iOwnerMeta.Load(iOwnerDescriptor) }

func iOwnerDescriptor() velox.Descriptor[IOwner] {
	return velox.Descriptor[IOwner]{
		Table:      "owners",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}, {Field: "PinnedID", Name: "pinned_id"}},
		PrimaryKey: []int{0},
		Edges: []velox.EdgeDescriptor{
			velox.EdgeTo[INote]("Notes", 0, 1),
			velox.EdgeTo[INote]("Pinned", 2, 0),
			velox.EdgeThrough[IJoin, INote]("Tagged", 0, 0, 1, 0),
		},
		FieldPtr: func(x *IOwner, i int) any {
			return [...]any{&x.ID, &x.Name, &x.PinnedID}[i]
		},
		EdgeFieldPtr: func(x *IOwner, i int) any {
			return [...]any{&x.Notes, &x.Pinned, &x.Tagged}[i]
		},
	}
}

// INote has an interceptor, which hides rows whose hidden is true, and a
// policy: Read hides the body "secret" and Write the body "locked".
type INote struct {
	ID      int64
	OwnerID int64
	Body    string
	Hidden  bool
	Owner   velox.One[IOwner]
}

// INotes are INote's handles.
var INotes = struct {
	ID      velox.NumberCol[INote, int64]
	OwnerID velox.NumberCol[INote, int64]
	Body    velox.StringCol[INote, string]
	Hidden  velox.Col[INote, bool]
	Owner   velox.Rel[INote, IOwner]
}{
	velox.NumberCol[INote, int64]{I: 0}, velox.NumberCol[INote, int64]{I: 1}, velox.StringCol[INote, string]{I: 2},
	velox.Col[INote, bool]{I: 3}, velox.Rel[INote, IOwner]{I: 0},
}

var iNoteMeta velox.MetaOnce[INote]

// VeloxMeta returns INote's metadata.
func (*INote) VeloxMeta() *velox.Meta[INote] { return iNoteMeta.Load(iNoteDescriptor) }

// Interceptors hides the rows whose hidden is true, as the context's mode
// says.
func (*INote) Interceptors() []velox.Interceptor[INote] {
	return []velox.Interceptor[INote]{{Filter: func(ctx context.Context) (velox.Pred[INote], error) {
		FilterCalls++
		m, _ := ctx.Value(filterModeKey{}).(FilterMode)
		switch m {
		case FilterSkip:
			return velox.Allow[INote](), nil
		case FilterFail:
			return velox.Pred[INote]{}, ErrFilter
		case FilterZero:
			return velox.Pred[INote]{}, nil
		}
		return INotes.Hidden.EQ(false), nil
	}}}
}

// Policy hides the body "secret" from reads and the body "locked" from
// writes.
func (*INote) Policy() velox.Policy[INote] {
	return velox.Policy[INote]{
		Read:  func(context.Context) (velox.Pred[INote], error) { return INotes.Body.NEQ("secret"), nil },
		Write: func(context.Context) (velox.Pred[INote], error) { return INotes.Body.NEQ("locked"), nil },
	}
}

func iNoteDescriptor() velox.Descriptor[INote] {
	return velox.Descriptor[INote]{
		Table: "notes",
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"}, {Field: "OwnerID", Name: "owner_id"}, {Field: "Body", Name: "body"}, {Field: "Hidden", Name: "hidden"},
		},
		PrimaryKey:   []int{0},
		Indexes:      []velox.IndexInfo{{Columns: []int{2}, Unique: true}},
		Edges:        []velox.EdgeDescriptor{velox.EdgeTo[IOwner]("Owner", 1, 0)},
		Policy:       []func() velox.BoundPolicy{func() velox.BoundPolicy { return velox.BindPolicy((*INote)(nil).Policy()) }},
		Interceptors: []func() velox.BoundInterceptors{func() velox.BoundInterceptors { return velox.BindInterceptors((*INote)(nil).Interceptors()) }},
		FieldPtr: func(x *INote, i int) any {
			return [...]any{&x.ID, &x.OwnerID, &x.Body, &x.Hidden}[i]
		},
		EdgeFieldPtr: func(x *INote, i int) any {
			if i == 0 {
				return &x.Owner
			}
			return nil
		},
	}
}

// IJoin is the join entity of IOwner.Tagged.
type IJoin struct {
	OwnerID int64
	NoteID  int64
}

var iJoinMeta velox.MetaOnce[IJoin]

// VeloxMeta returns IJoin's metadata.
func (*IJoin) VeloxMeta() *velox.Meta[IJoin] { return iJoinMeta.Load(iJoinDescriptor) }

func iJoinDescriptor() velox.Descriptor[IJoin] {
	return velox.Descriptor[IJoin]{
		Table:      "owner_notes",
		Columns:    []velox.ColumnDescriptor{{Field: "OwnerID", Name: "owner_id"}, {Field: "NoteID", Name: "note_id"}},
		PrimaryKey: []int{0, 1},
		FieldPtr: func(x *IJoin, i int) any {
			return [...]any{&x.OwnerID, &x.NoteID}[i]
		},
	}
}
