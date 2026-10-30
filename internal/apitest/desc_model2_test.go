package apitest

import (
	"context"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/apitest/fixture"
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

func (*pbox) VeloxMeta() *velox.Meta[pbox] { return pboxMeta.Load(pboxDesc) }

func pboxDesc() velox.Desc[pbox] {
	return velox.Desc[pbox]{
		Table:      "pboxes",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "NoteID", Name: "note_id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Edges:      []velox.EdgeDesc{velox.EdgeTo[fixture.Pnote]("Note", 1, 0)},
		Addr: func(x *pbox, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.NoteID
		},
		EdgeAddr: func(x *pbox, i int) any { return &x.Note },
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

func (*pcomment) VeloxMeta() *velox.Meta[pcomment] { return pcommentMeta.Load(pcommentDesc) }

func pcommentDesc() velox.Desc[pcomment] {
	return velox.Desc[pcomment]{
		Policy:     func() velox.BoundPolicy { return velox.BindPolicy((*pcomment)(nil).Policy()) },
		Table:      "pcomments",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "NoteID", Name: "note_id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Edges:      []velox.EdgeDesc{velox.EdgeTo[fixture.Pnote]("Note", 1, 0)},
		Addr: func(x *pcomment, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.NoteID
		},
		EdgeAddr: func(x *pcomment, i int) any { return &x.Note },
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

func (*pcycA) VeloxMeta() *velox.Meta[pcycA] { return pcycAMeta.Load(pcycADesc) }

func (*pcycB) VeloxMeta() *velox.Meta[pcycB] { return pcycBMeta.Load(pcycBDesc) }

func pcycADesc() velox.Desc[pcycA] {
	return velox.Desc[pcycA]{
		Policy:     func() velox.BoundPolicy { return velox.BindPolicy((*pcycA)(nil).Policy()) },
		Table:      "pcyc_as",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "BID", Name: "b_id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Edges:      []velox.EdgeDesc{velox.EdgeTo[pcycB]("B", 1, 0)},
		Addr: func(x *pcycA, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.BID
		},
		EdgeAddr: func(x *pcycA, i int) any { return &x.B },
	}
}

func pcycBDesc() velox.Desc[pcycB] {
	return velox.Desc[pcycB]{
		Policy:     func() velox.BoundPolicy { return velox.BindPolicy((*pcycB)(nil).Policy()) },
		Table:      "pcyc_bs",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "AID", Name: "a_id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Edges:      []velox.EdgeDesc{velox.EdgeTo[pcycA]("A", 1, 0)},
		Addr: func(x *pcycB, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.AID
		},
		EdgeAddr: func(x *pcycB, i int) any { return &x.A },
	}
}

// Small entities for what Invoice cannot show.

type keyOnly struct{ ID int64 }

var keyOnlyMeta velox.MetaOnce[keyOnly]

func (*keyOnly) VeloxMeta() *velox.Meta[keyOnly] { return keyOnlyMeta.Load(keyOnlyDesc) }

func keyOnlyDesc() velox.Desc[keyOnly] {
	return velox.Desc[keyOnly]{
		Table: "key_only", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}, Tenant: -1,
		Addr: func(x *keyOnly, i int) any { return &x.ID },
	}
}

type wideKey struct {
	ID  uint64
	Opt *bool
}

var wideKeyMeta velox.MetaOnce[wideKey]

func (*wideKey) VeloxMeta() *velox.Meta[wideKey] { return wideKeyMeta.Load(wideKeyDesc) }

func wideKeyDesc() velox.Desc[wideKey] {
	return velox.Desc[wideKey]{
		Table: "wide_key", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Opt", Name: "opt", Default: true}},
		PrimaryKey: []int{0}, Tenant: -1,
		Addr: func(x *wideKey, i int) any {
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

func (*tinyKey) VeloxMeta() *velox.Meta[tinyKey] { return tinyKeyMeta.Load(tinyKeyDesc) }

func tinyKeyDesc() velox.Desc[tinyKey] {
	return velox.Desc[tinyKey]{
		Table: "tiny_key", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
		PrimaryKey: []int{0}, Tenant: -1,
		Addr: func(x *tinyKey, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.Name
		},
	}
}

// tunique's primary key holds its tenant, but its unique code does not: on
// MySQL a conflict on the code could reach another tenant's row.
type tunique struct {
	OrgID int64
	ID    int64
	Code  string
}

var tuniqueMeta velox.MetaOnce[tunique]

func (*tunique) VeloxMeta() *velox.Meta[tunique] { return tuniqueMeta.Load(tuniqueDesc) }

func tuniqueDesc() velox.Desc[tunique] {
	return velox.Desc[tunique]{
		Table:      "tuniques",
		Columns:    []velox.ColumnDesc{{Field: "OrgID", Name: "org_id"}, {Field: "ID", Name: "id"}, {Field: "Code", Name: "code"}},
		PrimaryKey: []int{0, 1},
		Tenant:     0,
		Indexes:    []velox.IndexInfo{{Columns: []int{2}, Unique: true}},
		Addr: func(x *tunique, i int) any {
			switch i {
			case 0:
				return &x.OrgID
			case 1:
				return &x.ID
			}
			return &x.Code
		},
	}
}

var tu = struct {
	OrgID velox.NumberCol[tunique, int64]
	ID    velox.NumberCol[tunique, int64]
	Code  velox.StringCol[tunique, string]
}{velox.NumberCol[tunique, int64]{I: 0}, velox.NumberCol[tunique, int64]{I: 1}, velox.StringCol[tunique, string]{I: 2}}

var dnoteID = velox.NumberCol[fixture.Dnote, int64]{I: 0}

// panickyEntity's Desc panics; addrlessEntity's has no Addr.
type panickyEntity struct{ ID int64 }

var panickyMeta velox.MetaOnce[panickyEntity]

func (*panickyEntity) VeloxMeta() *velox.Meta[panickyEntity] {
	return panickyMeta.Load(func() velox.Desc[panickyEntity] { panic("no schema today") })
}

type addrlessEntity struct{ ID int64 }

var addrlessMeta velox.MetaOnce[addrlessEntity]

func (*addrlessEntity) VeloxMeta() *velox.Meta[addrlessEntity] {
	return addrlessMeta.Load(func() velox.Desc[addrlessEntity] {
		return velox.Desc[addrlessEntity]{Table: "addrless", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0}, Tenant: -1}
	})
}
