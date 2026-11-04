// Package fixture holds the hand-written entities that velox's tests share between
// the root package, which reaches its internals, and internal/apitest, which does not.
package fixture

import (
	"context"
	"sync/atomic"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// More Descriptor entities for the tests that need a shape internal/testmodel does
// not have: policies that count their calls, cycles, keys of other types.

// The policy fixtures: a pnote has a read and a write policy that count
// their calls, and a pbox refers to a pnote and has no policy.
type Pnote struct {
	ID        int64
	Owner     string
	Locked    bool
	ParentID  *int64
	InvoiceID int64
	Parent    velox.One[Pnote]
	Invoice   velox.One[testmodel.Invoice]
}

var PnoteReads, PnoteWrites atomic.Int64

// PnoteKey's value, a pnoteAnswer, is what pnote's read policy returns.
type PnoteKey struct{}

type PnoteAnswer struct {
	P   velox.Pred[Pnote]
	Err error
}

// PnoteWriteKey's value, a pnoteAnswer, is what pnote's write policy returns.
type PnoteWriteKey struct{}

func (*Pnote) Policy() velox.Policy[Pnote] {
	return velox.Policy[Pnote]{
		Read: func(ctx context.Context) (velox.Pred[Pnote], error) {
			PnoteReads.Add(1)
			if a, ok := ctx.Value(PnoteKey{}).(PnoteAnswer); ok {
				return a.P, a.Err
			}
			return Pn.Owner.EQ("ann"), nil
		},
		Write: func(ctx context.Context) (velox.Pred[Pnote], error) {
			PnoteWrites.Add(1)
			if a, ok := ctx.Value(PnoteWriteKey{}).(PnoteAnswer); ok {
				return a.P, a.Err
			}
			return Pn.Locked.EQ(false), nil
		},
	}
}

var PnoteMeta velox.MetaOnce[Pnote]

func (*Pnote) VeloxMeta() *velox.Meta[Pnote] { return PnoteMeta.Load(PnoteDescriptor) }

func PnoteDescriptor() velox.Descriptor[Pnote] {
	return velox.Descriptor[Pnote]{
		Policy: velox.BindPolicy((*Pnote)(nil).Policy),
		Table:  "pnotes",
		Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Owner", Name: "owner"}, {Field: "Locked", Name: "locked"},
			{Field: "ParentID", Name: "parent_id"}, {Field: "InvoiceID", Name: "invoice_id"}},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[Pnote]("Parent", 3, 0), velox.EdgeTo[testmodel.Invoice]("Invoice", 4, 0)},
		FieldPtr: func(x *Pnote, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Owner
			case 2:
				return &x.Locked
			case 3:
				return &x.ParentID
			case 4:
				return &x.InvoiceID
			}
			return nil
		},
		EdgeFieldPtr: func(x *Pnote, i int) any {
			if i == 0 {
				return &x.Parent
			}
			return &x.Invoice
		},
	}
}

var Pn = struct {
	ID        velox.NumberCol[Pnote, int64]
	Owner     velox.StringCol[Pnote, string]
	Locked    velox.Col[Pnote, bool]
	InvoiceID velox.NumberCol[Pnote, int64]
	Parent    velox.Rel[Pnote, Pnote]
	Invoice   velox.Rel[Pnote, testmodel.Invoice]
}{
	ID:        velox.NumberCol[Pnote, int64]{I: 0},
	Owner:     velox.StringCol[Pnote, string]{I: 1},
	Locked:    velox.Col[Pnote, bool]{I: 2},
	InvoiceID: velox.NumberCol[Pnote, int64]{I: 4},
	Parent:    velox.Rel[Pnote, Pnote]{I: 0},
	Invoice:   velox.Rel[Pnote, testmodel.Invoice]{I: 1},
}

// A dnote records, in the dryRuns its context carries, what IsDryRun told
// each of its hooks and policy functions.
type Dnote struct {
	ID    int64
	Owner string
}

// DryRuns maps a hook's name to what IsDryRun told it, call by call.
type DryRuns map[string][]bool

type DryRunsKey struct{}

func NoteDryRun(ctx context.Context, hook string) {
	if m, ok := ctx.Value(DryRunsKey{}).(DryRuns); ok {
		m[hook] = append(m[hook], velox.IsDryRun(ctx))
	}
}

func (*Dnote) Policy() velox.Policy[Dnote] {
	return velox.Policy[Dnote]{
		Read: func(ctx context.Context) (velox.Pred[Dnote], error) {
			NoteDryRun(ctx, "Read")
			return velox.Allow[Dnote](), nil
		},
		Write: func(ctx context.Context) (velox.Pred[Dnote], error) {
			NoteDryRun(ctx, "Write")
			return velox.Allow[Dnote](), nil
		},
		Insert: func(ctx context.Context, _ *Dnote) error {
			NoteDryRun(ctx, "Insert")
			return nil
		},
	}
}

// DnoteHook is a hook that records that it ran under name and calls next.
func DnoteHook(name string) velox.Hook[Dnote] {
	return func(next velox.Mutator[Dnote]) velox.Mutator[Dnote] {
		return velox.MutateFunc[Dnote](func(ctx context.Context, m *velox.Mutation[Dnote]) error {
			NoteDryRun(ctx, name)
			return next.Mutate(ctx, m)
		})
	}
}

func (*Dnote) Hooks() []velox.Hook[Dnote] {
	return []velox.Hook[Dnote]{
		velox.On(DnoteHook("InsertHook"), velox.OpInsert),
		velox.On(DnoteHook("UpdateHook"), velox.OpUpdate),
		velox.On(DnoteHook("DeleteHook"), velox.OpDelete),
	}
}

var DnoteMeta velox.MetaOnce[Dnote]

func (*Dnote) VeloxMeta() *velox.Meta[Dnote] { return DnoteMeta.Load(DnoteDescriptor) }

func DnoteDescriptor() velox.Descriptor[Dnote] {
	return velox.Descriptor[Dnote]{
		Policy:     velox.BindPolicy((*Dnote)(nil).Policy),
		Hooks:      velox.BindHooks((*Dnote)(nil).Hooks),
		Table:      "dnotes",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Owner", Name: "owner"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *Dnote, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.Owner
		},
	}
}
