package apitest

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
)

func pnoteReading(p velox.Pred[fixture.Pnote], err error) context.Context {
	return context.WithValue(context.Background(), fixture.PnoteKey{}, fixture.PnoteAnswer{P: p, Err: err})
}

func pnoteWriting(p velox.Pred[fixture.Pnote], err error) context.Context {
	return context.WithValue(context.Background(), fixture.PnoteWriteKey{}, fixture.PnoteAnswer{P: p, Err: err})
}

type pbox struct {
	ID     int64
	NoteID int64
	Note   velox.One[fixture.Pnote]
}

var pboxMeta velox.MetaOnce[pbox]

func (*pbox) VeloxMeta() *velox.Meta[pbox] { return pboxMeta.Load(pboxDescriptor) }

func pboxDescriptor() velox.Descriptor[pbox] {
	return velox.Descriptor[pbox]{
		Table:      "pboxes",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "NoteID", Name: "note_id"}},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[fixture.Pnote]("Note", 1, 0)},
		FieldPtr: func(x *pbox, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.NoteID
		},
		EdgeFieldPtr: func(x *pbox, i int) any { return &x.Note },
	}
}

var pboxNote = velox.Rel[pbox, fixture.Pnote]{I: 0}

// A pcomment inherits its note's Read policy: its own Read is a Has.
type pcomment struct {
	ID     int64
	NoteID int64
	Note   velox.One[fixture.Pnote]
}

func (*pcomment) Policy() velox.Policy[pcomment] {
	return velox.Policy[pcomment]{
		Read: func(context.Context) (velox.Pred[pcomment], error) { return pcommentNote.Has(), nil },
	}
}

var pcommentMeta velox.MetaOnce[pcomment]

func (*pcomment) VeloxMeta() *velox.Meta[pcomment] { return pcommentMeta.Load(pcommentDescriptor) }

func pcommentDescriptor() velox.Descriptor[pcomment] {
	return velox.Descriptor[pcomment]{
		Policy:     velox.BindPolicy((*pcomment)(nil).Policy),
		Table:      "pcomments",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "NoteID", Name: "note_id"}},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[fixture.Pnote]("Note", 1, 0)},
		FieldPtr: func(x *pcomment, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.NoteID
		},
		EdgeFieldPtr: func(x *pcomment, i int) any { return &x.Note },
	}
}

var pcommentNote = velox.Rel[pcomment, fixture.Pnote]{I: 0}

// pcycA's Read is that its B is visible, and pcycB's that its A is: each
// reaches its own Read through the other's.
type pcycA struct {
	ID  int64
	BID int64
	B   velox.One[pcycB]
}

type pcycB struct {
	ID  int64
	AID int64
	A   velox.One[pcycA]
}

var (
	pcycAB = velox.Rel[pcycA, pcycB]{I: 0}
	pcycBA = velox.Rel[pcycB, pcycA]{I: 0}
)

func (*pcycA) Policy() velox.Policy[pcycA] {
	return velox.Policy[pcycA]{Read: func(context.Context) (velox.Pred[pcycA], error) { return pcycAB.Has(), nil }}
}

func (*pcycB) Policy() velox.Policy[pcycB] {
	return velox.Policy[pcycB]{Read: func(context.Context) (velox.Pred[pcycB], error) { return pcycBA.Has(), nil }}
}

var (
	pcycAMeta velox.MetaOnce[pcycA]
	pcycBMeta velox.MetaOnce[pcycB]
)

func (*pcycA) VeloxMeta() *velox.Meta[pcycA] { return pcycAMeta.Load(pcycADescriptor) }

func (*pcycB) VeloxMeta() *velox.Meta[pcycB] { return pcycBMeta.Load(pcycBDescriptor) }

func pcycADescriptor() velox.Descriptor[pcycA] {
	return velox.Descriptor[pcycA]{
		Policy:     velox.BindPolicy((*pcycA)(nil).Policy),
		Table:      "pcyc_as",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "BID", Name: "b_id"}},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[pcycB]("B", 1, 0)},
		FieldPtr: func(x *pcycA, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.BID
		},
		EdgeFieldPtr: func(x *pcycA, i int) any { return &x.B },
	}
}

func pcycBDescriptor() velox.Descriptor[pcycB] {
	return velox.Descriptor[pcycB]{
		Policy:     velox.BindPolicy((*pcycB)(nil).Policy),
		Table:      "pcyc_bs",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "AID", Name: "a_id"}},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[pcycA]("A", 1, 0)},
		FieldPtr: func(x *pcycB, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.AID
		},
		EdgeFieldPtr: func(x *pcycB, i int) any { return &x.A },
	}
}

// Small entities for what Invoice cannot show.

type keyOnly struct{ ID int64 }

var keyOnlyMeta velox.MetaOnce[keyOnly]

func (*keyOnly) VeloxMeta() *velox.Meta[keyOnly] { return keyOnlyMeta.Load(keyOnlyDescriptor) }

func keyOnlyDescriptor() velox.Descriptor[keyOnly] {
	return velox.Descriptor[keyOnly]{
		Table: "key_only", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
		FieldPtr: func(x *keyOnly, i int) any { return &x.ID },
	}
}

type wideKey struct {
	ID  uint64
	Opt *bool
}

var wideKeyMeta velox.MetaOnce[wideKey]

func (*wideKey) VeloxMeta() *velox.Meta[wideKey] { return wideKeyMeta.Load(wideKeyDescriptor) }

func wideKeyDescriptor() velox.Descriptor[wideKey] {
	return velox.Descriptor[wideKey]{
		Table: "wide_key", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Opt", Name: "opt", Default: true}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *wideKey, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.Opt
		},
	}
}

type tinyKey struct {
	ID   int8
	Name string
}

var tinyKeyMeta velox.MetaOnce[tinyKey]

func (*tinyKey) VeloxMeta() *velox.Meta[tinyKey] { return tinyKeyMeta.Load(tinyKeyDescriptor) }

func tinyKeyDescriptor() velox.Descriptor[tinyKey] {
	return velox.Descriptor[tinyKey]{
		Table: "tiny_key", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *tinyKey, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.Name
		},
	}
}

var dnoteID = velox.NumberCol[fixture.Dnote, int64]{I: 0}

// panickyEntity's Descriptor panics; addrlessEntity's has no FieldPtr.
type panickyEntity struct{ ID int64 }

var panickyMeta velox.MetaOnce[panickyEntity]

func (*panickyEntity) VeloxMeta() *velox.Meta[panickyEntity] {
	return panickyMeta.Load(func() velox.Descriptor[panickyEntity] { panic("no schema today") })
}

type addrlessEntity struct{ ID int64 }

var addrlessMeta velox.MetaOnce[addrlessEntity]

func (*addrlessEntity) VeloxMeta() *velox.Meta[addrlessEntity] {
	return addrlessMeta.Load(func() velox.Descriptor[addrlessEntity] {
		return velox.Descriptor[addrlessEntity]{Table: "addrless", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}}
	})
}
