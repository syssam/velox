package engine_test

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/scope"
)

// The Descriptor entities of the engine's tests. Each is described as velox-gen
// would write it: a struct, a MetaOnce, a VeloxMeta and a Descriptor.

// hnote's hooks, for every write, run the function in the context
// under hnoteKey, if any, before the write.
type hnote struct {
	ID    int64
	Title string
	Note  string
	N     int64
	Due   *int64
}

type hnoteKey struct{}

func hnoteHook(ctx context.Context, m *velox.Mutation[hnote]) error {
	if f, ok := ctx.Value(hnoteKey{}).(func(*velox.Mutation[hnote]) error); ok {
		return f(m)
	}
	return nil
}

func (*hnote) Hooks() []velox.Hook[hnote] {
	return []velox.Hook[hnote]{func(next velox.Mutator[hnote]) velox.Mutator[hnote] {
		return velox.MutateFunc[hnote](func(ctx context.Context, m *velox.Mutation[hnote]) error {
			if err := hnoteHook(ctx, m); err != nil {
				return err
			}
			return next.Mutate(ctx, m)
		})
	}}
}

var hnoteMeta velox.MetaOnce[hnote]

func (*hnote) VeloxMeta() *velox.Meta[hnote] { return hnoteMeta.Load(hnoteDescriptor) }

func hnoteDescriptor() velox.Descriptor[hnote] {
	return velox.Descriptor[hnote]{
		Hooks: []func() velox.BoundHooks{func() velox.BoundHooks { return velox.BindHooks((*hnote)(nil).Hooks()) }},
		Table: "hnotes",
		Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"}, {Field: "Note", Name: "note"},
			{Field: "N", Name: "n"}, {Field: "Due", Name: "due"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *hnote, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Title
			case 2:
				return &x.Note
			case 3:
				return &x.N
			}
			return &x.Due
		},
	}
}

var hn = struct {
	ID    velox.NumberCol[hnote, int64]
	Title velox.StringCol[hnote, string]
	Note  velox.StringCol[hnote, string]
	N     velox.NumberCol[hnote, int64]
	Due   velox.NullNumberCol[hnote, int64]
}{velox.NumberCol[hnote, int64]{I: 0}, velox.StringCol[hnote, string]{I: 1}, velox.StringCol[hnote, string]{I: 2}, velox.NumberCol[hnote, int64]{I: 3}, velox.NullNumberCol[hnote, int64]{I: 4}}

// moveOrg is confined to the context's org, and its insert hook moves every row
// to org 9.
type moveOrg struct {
	ID    int64
	OrgID int64
}

var moveOrgScope = scope.By(moveOrgKey, velox.NumberCol[moveOrg, int64]{I: 1}.EQ, func(x *moveOrg) *int64 { return &x.OrgID })

var moveOrgKey = scope.NewKey[int64]("org")

func (*moveOrg) Policy() velox.Policy[moveOrg] { return moveOrgScope.Policy() }

func (*moveOrg) Hooks() []velox.Hook[moveOrg] {
	return []velox.Hook[moveOrg]{func(next velox.Mutator[moveOrg]) velox.Mutator[moveOrg] {
		return velox.MutateFunc[moveOrg](func(ctx context.Context, m *velox.Mutation[moveOrg]) error {
			for _, x := range m.Rows() {
				x.OrgID = 9
			}
			return next.Mutate(ctx, m)
		})
	}}
}

var moveOrgMeta velox.MetaOnce[moveOrg]

func (*moveOrg) VeloxMeta() *velox.Meta[moveOrg] { return moveOrgMeta.Load(moveOrgDescriptor) }

func moveOrgDescriptor() velox.Descriptor[moveOrg] {
	return velox.Descriptor[moveOrg]{
		Policy:     []func() velox.BoundPolicy{func() velox.BoundPolicy { return velox.BindPolicy((*moveOrg)(nil).Policy()) }},
		Hooks:      []func() velox.BoundHooks{func() velox.BoundHooks { return velox.BindHooks((*moveOrg)(nil).Hooks()) }},
		Table:      "move_org",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *moveOrg, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.OrgID
		},
	}
}

// The relation fixtures: a company has employees, each with a manager.
type company struct {
	ID    int64
	Staff velox.Many[employee]
}

var companyMeta velox.MetaOnce[company]

func (*company) VeloxMeta() *velox.Meta[company] { return companyMeta.Load(companyDescriptor) }

func companyDescriptor() velox.Descriptor[company] {
	return velox.Descriptor[company]{
		Table:        "company",
		Columns:      []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
		PrimaryKey:   []int{0},
		Edges:        []velox.EdgeDescriptor{velox.EdgeTo[employee]("Staff", 0, 1)}, // employee.CompanyID
		FieldPtr:     func(x *company, i int) any { return &x.ID },
		EdgeFieldPtr: func(x *company, i int) any { return &x.Staff },
	}
}

type employee struct {
	ID        int64
	CompanyID int64
	BossID    *int64
	Boss      velox.One[employee]
	Company   velox.One[company]
}

var employeeMeta velox.MetaOnce[employee]

func (*employee) VeloxMeta() *velox.Meta[employee] { return employeeMeta.Load(employeeDescriptor) }

func employeeDescriptor() velox.Descriptor[employee] {
	return velox.Descriptor[employee]{
		Table:      "employee",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "CompanyID", Name: "company_id"}, {Field: "BossID", Name: "boss_id"}},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[employee]("Boss", 2, 0), velox.EdgeTo[company]("Company", 1, 0)},
		FieldPtr: func(x *employee, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.CompanyID
			}
			return &x.BossID
		},
		EdgeFieldPtr: func(x *employee, i int) any {
			if i == 0 {
				return &x.Boss
			}
			return &x.Company
		},
	}
}

// keyed has a string key, which an int64 cannot refer to.
type keyed struct {
	ID    string
	Lines velox.Many[keyedLine]
}

var keyedMeta velox.MetaOnce[keyed]

func (*keyed) VeloxMeta() *velox.Meta[keyed] { return keyedMeta.Load(keyedDescriptor) }

func keyedDescriptor() velox.Descriptor[keyed] {
	return velox.Descriptor[keyed]{
		Table:        "keyed",
		Columns:      []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
		PrimaryKey:   []int{0},
		Edges:        []velox.EdgeDescriptor{velox.EdgeTo[keyedLine]("Lines", 0, 1)},
		FieldPtr:     func(x *keyed, i int) any { return &x.ID },
		EdgeFieldPtr: func(x *keyed, i int) any { return &x.Lines },
	}
}

type keyedLine struct {
	ID      int64
	KeyedID int64 // an int64 cannot refer to keyed's string key
}

var keyedLineMeta velox.MetaOnce[keyedLine]

func (*keyedLine) VeloxMeta() *velox.Meta[keyedLine] { return keyedLineMeta.Load(keyedLineDescriptor) }

func keyedLineDescriptor() velox.Descriptor[keyedLine] {
	return velox.Descriptor[keyedLine]{
		Table:      "keyed_line",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "KeyedID", Name: "keyed_id"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *keyedLine, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.KeyedID
		},
	}
}

// blobOwner and blobLine are keyed by a []byte, which cannot be compared.
type blobOwner struct {
	ID    []byte
	Lines velox.Many[blobLine]
}

var blobOwnerMeta velox.MetaOnce[blobOwner]

func (*blobOwner) VeloxMeta() *velox.Meta[blobOwner] { return blobOwnerMeta.Load(blobOwnerDescriptor) }

func blobOwnerDescriptor() velox.Descriptor[blobOwner] {
	return velox.Descriptor[blobOwner]{
		Table:        "blob_owner",
		Columns:      []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
		PrimaryKey:   []int{0},
		Edges:        []velox.EdgeDescriptor{velox.EdgeTo[blobLine]("Lines", 0, 1)},
		FieldPtr:     func(x *blobOwner, i int) any { return &x.ID },
		EdgeFieldPtr: func(x *blobOwner, i int) any { return &x.Lines },
	}
}

type blobLine struct {
	ID    int64
	Owner []byte
}

var blobLineMeta velox.MetaOnce[blobLine]

func (*blobLine) VeloxMeta() *velox.Meta[blobLine] { return blobLineMeta.Load(blobLineDescriptor) }

func blobLineDescriptor() velox.Descriptor[blobLine] {
	return velox.Descriptor[blobLine]{
		Table:      "blob_line",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Owner", Name: "owner"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *blobLine, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.Owner
		},
	}
}

// invalidHolder's relation T has a target that cannot be built: its Descriptor has
// no FieldPtr.
type invalidHolder struct {
	ID int64
	T  velox.One[brokenTarget]
}

var invalidHolderMeta velox.MetaOnce[invalidHolder]

func (*invalidHolder) VeloxMeta() *velox.Meta[invalidHolder] {
	return invalidHolderMeta.Load(invalidHolderDescriptor)
}

func invalidHolderDescriptor() velox.Descriptor[invalidHolder] {
	return velox.Descriptor[invalidHolder]{
		Table:        "invalid_holder",
		Columns:      []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
		PrimaryKey:   []int{0},
		Edges:        []velox.EdgeDescriptor{velox.EdgeTo[brokenTarget]("T", 0, 0)},
		FieldPtr:     func(x *invalidHolder, i int) any { return &x.ID },
		EdgeFieldPtr: func(x *invalidHolder, i int) any { return &x.T },
	}
}

type brokenTarget struct{ ID int64 }

var brokenTargetMeta velox.MetaOnce[brokenTarget]

func (*brokenTarget) VeloxMeta() *velox.Meta[brokenTarget] {
	return brokenTargetMeta.Load(brokenTargetDescriptor)
}

func brokenTargetDescriptor() velox.Descriptor[brokenTarget] {
	return velox.Descriptor[brokenTarget]{
		Table:      "broken_target",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
	}
}

// hooked has an embedded struct, so that a column is a promoted field, an
// insert hook, update and delete hooks, and a Read and an Insert policy.
type tagMix struct{ Tag string }

type hooked struct {
	ID int64
	tagMix
	Title string
}

var (
	hookedTag   = velox.Col[hooked, string]{I: 1}
	hookedTitle = velox.Col[hooked, string]{I: 2}
)

func (*hooked) Hooks() []velox.Hook[hooked] {
	return []velox.Hook[hooked]{
		velox.On(func(next velox.Mutator[hooked]) velox.Mutator[hooked] {
			return velox.MutateFunc[hooked](func(ctx context.Context, m *velox.Mutation[hooked]) error {
				for _, x := range m.Rows() {
					x.Tag += "entity;"
				}
				return next.Mutate(ctx, m)
			})
		}, velox.OpInsert),
		velox.On(func(next velox.Mutator[hooked]) velox.Mutator[hooked] {
			return velox.MutateFunc[hooked](func(ctx context.Context, m *velox.Mutation[hooked]) error {
				m.Set(hookedTag.Set("updated"))
				return next.Mutate(ctx, m)
			})
		}, velox.OpUpdate),
		velox.On(func(next velox.Mutator[hooked]) velox.Mutator[hooked] {
			return velox.MutateFunc[hooked](func(ctx context.Context, m *velox.Mutation[hooked]) error {
				m.Set(hookedTag.Set("x"))
				return next.Mutate(ctx, m)
			})
		}, velox.OpDelete),
	}
}

func (*hooked) Policy() velox.Policy[hooked] {
	return velox.Policy[hooked]{
		Read: func(context.Context) (velox.Pred[hooked], error) { return hookedTitle.EQ("r"), nil },
		Insert: func(_ context.Context, x *hooked) error {
			if x.Title == "" {
				return velox.Deny("no title")
			}
			return nil
		},
	}
}

var hookedMeta velox.MetaOnce[hooked]

func (*hooked) VeloxMeta() *velox.Meta[hooked] { return hookedMeta.Load(hookedDescriptor) }

func hookedDescriptor() velox.Descriptor[hooked] {
	return velox.Descriptor[hooked]{
		Policy:     []func() velox.BoundPolicy{func() velox.BoundPolicy { return velox.BindPolicy((*hooked)(nil).Policy()) }},
		Hooks:      []func() velox.BoundHooks{func() velox.BoundHooks { return velox.BindHooks((*hooked)(nil).Hooks()) }},
		Table:      "hooked",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Tag", Name: "tag"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		FieldPtr: func(x *hooked, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Tag
			}
			return &x.Title
		},
	}
}
