package engine_test

import (
	"context"
	"github.com/syssam/velox2"
)

// The Desc entities of the engine's tests. Each is described as velox-gen
// would write it: a struct, a MetaOnce, a VeloxMeta and a Desc.

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

func (*hnote) VeloxMeta() *velox.Meta[hnote] { return hnoteMeta.Load(hnoteDesc) }

func hnoteDesc() velox.Desc[hnote] {
	return velox.Desc[hnote]{
		Hooks: func() velox.BoundHooks { return velox.BindHooks((*hnote)(nil).Hooks()) },
		Table: "hnotes",
		Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"}, {Field: "Note", Name: "note"},
			{Field: "N", Name: "n"}, {Field: "Due", Name: "due"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *hnote, i int) any {
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

// moveOrg is tenant-scoped, and its insert hook moves every row to tenant 9.
type moveOrg struct {
	ID    int64
	OrgID int64
}

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

func (*moveOrg) VeloxMeta() *velox.Meta[moveOrg] { return moveOrgMeta.Load(moveOrgDesc) }

func moveOrgDesc() velox.Desc[moveOrg] {
	return velox.Desc[moveOrg]{
		Hooks:      func() velox.BoundHooks { return velox.BindHooks((*moveOrg)(nil).Hooks()) },
		Table:      "move_org",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}},
		PrimaryKey: []int{0},
		Tenant:     1,
		Addr: func(x *moveOrg, i int) any {
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

func (*company) VeloxMeta() *velox.Meta[company] { return companyMeta.Load(companyDesc) }

func companyDesc() velox.Desc[company] {
	return velox.Desc[company]{
		Table:      "company",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Edges:      []velox.EdgeDesc{velox.EdgeTo[employee]("Staff", 0, 1)}, // employee.CompanyID
		Addr:       func(x *company, i int) any { return &x.ID },
		EdgeAddr:   func(x *company, i int) any { return &x.Staff },
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

func (*employee) VeloxMeta() *velox.Meta[employee] { return employeeMeta.Load(employeeDesc) }

func employeeDesc() velox.Desc[employee] {
	return velox.Desc[employee]{
		Table:      "employee",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "CompanyID", Name: "company_id"}, {Field: "BossID", Name: "boss_id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Edges:      []velox.EdgeDesc{velox.EdgeTo[employee]("Boss", 2, 0), velox.EdgeTo[company]("Company", 1, 0)},
		Addr: func(x *employee, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.CompanyID
			}
			return &x.BossID
		},
		EdgeAddr: func(x *employee, i int) any {
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

func (*keyed) VeloxMeta() *velox.Meta[keyed] { return keyedMeta.Load(keyedDesc) }

func keyedDesc() velox.Desc[keyed] {
	return velox.Desc[keyed]{
		Table:      "keyed",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Edges:      []velox.EdgeDesc{velox.EdgeTo[keyedLine]("Lines", 0, 1)},
		Addr:       func(x *keyed, i int) any { return &x.ID },
		EdgeAddr:   func(x *keyed, i int) any { return &x.Lines },
	}
}

type keyedLine struct {
	ID      int64
	KeyedID int64 // an int64 cannot refer to keyed's string key
}

var keyedLineMeta velox.MetaOnce[keyedLine]

func (*keyedLine) VeloxMeta() *velox.Meta[keyedLine] { return keyedLineMeta.Load(keyedLineDesc) }

func keyedLineDesc() velox.Desc[keyedLine] {
	return velox.Desc[keyedLine]{
		Table:      "keyed_line",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "KeyedID", Name: "keyed_id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *keyedLine, i int) any {
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

func (*blobOwner) VeloxMeta() *velox.Meta[blobOwner] { return blobOwnerMeta.Load(blobOwnerDesc) }

func blobOwnerDesc() velox.Desc[blobOwner] {
	return velox.Desc[blobOwner]{
		Table:      "blob_owner",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Edges:      []velox.EdgeDesc{velox.EdgeTo[blobLine]("Lines", 0, 1)},
		Addr:       func(x *blobOwner, i int) any { return &x.ID },
		EdgeAddr:   func(x *blobOwner, i int) any { return &x.Lines },
	}
}

type blobLine struct {
	ID    int64
	Owner []byte
}

var blobLineMeta velox.MetaOnce[blobLine]

func (*blobLine) VeloxMeta() *velox.Meta[blobLine] { return blobLineMeta.Load(blobLineDesc) }

func blobLineDesc() velox.Desc[blobLine] {
	return velox.Desc[blobLine]{
		Table:      "blob_line",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Owner", Name: "owner"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *blobLine, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.Owner
		},
	}
}

// invalidHolder's relation T has a target that cannot be built: its Desc has
// no Addr.
type invalidHolder struct {
	ID int64
	T  velox.One[brokenTarget]
}

var invalidHolderMeta velox.MetaOnce[invalidHolder]

func (*invalidHolder) VeloxMeta() *velox.Meta[invalidHolder] {
	return invalidHolderMeta.Load(invalidHolderDesc)
}

func invalidHolderDesc() velox.Desc[invalidHolder] {
	return velox.Desc[invalidHolder]{
		Table:      "invalid_holder",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Edges:      []velox.EdgeDesc{velox.EdgeTo[brokenTarget]("T", 0, 0)},
		Addr:       func(x *invalidHolder, i int) any { return &x.ID },
		EdgeAddr:   func(x *invalidHolder, i int) any { return &x.T },
	}
}

type brokenTarget struct{ ID int64 }

var brokenTargetMeta velox.MetaOnce[brokenTarget]

func (*brokenTarget) VeloxMeta() *velox.Meta[brokenTarget] {
	return brokenTargetMeta.Load(brokenTargetDesc)
}

func brokenTargetDesc() velox.Desc[brokenTarget] {
	return velox.Desc[brokenTarget]{
		Table:      "broken_target",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
	}
}

// The tenant column's refusals (validate), each on an entity of its own.
type nullTenant struct {
	ID    int64
	OrgID *int64
}

func nullTenantDesc() velox.Desc[nullTenant] {
	return velox.Desc[nullTenant]{
		Table: "null_tenant", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}},
		PrimaryKey: []int{0}, Tenant: 1,
		Addr: func(x *nullTenant, i int) any { return [...]any{&x.ID, &x.OrgID}[i] },
	}
}

type sliceTenant struct {
	ID  int64
	Org []byte
}

func sliceTenantDesc() velox.Desc[sliceTenant] {
	return velox.Desc[sliceTenant]{
		Table: "slice_tenant", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Org", Name: "org"}},
		PrimaryKey: []int{0}, Tenant: 1,
		Addr: func(x *sliceTenant, i int) any { return [...]any{&x.ID, &x.Org}[i] },
	}
}

// A tenant column that a default or an update default would fill could put a
// row in a tenant the context never named.
type defaultTenant struct {
	ID  int64
	Org string
}

func defaultTenantDesc() velox.Desc[defaultTenant] {
	return velox.Desc[defaultTenant]{
		Table: "default_tenant", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Org", Name: "org", Default: "acme"}},
		PrimaryKey: []int{0}, Tenant: 1,
		Addr: func(x *defaultTenant, i int) any { return [...]any{&x.ID, &x.Org}[i] },
	}
}

type defaultFuncTenant struct {
	ID    int64
	OrgID int64
}

func defaultFuncTenantDesc() velox.Desc[defaultFuncTenant] {
	return velox.Desc[defaultFuncTenant]{
		Table: "default_func_tenant", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", DefaultFunc: func() any { return int64(1) }}},
		PrimaryKey: []int{0}, Tenant: 1,
		Addr: func(x *defaultFuncTenant, i int) any { return [...]any{&x.ID, &x.OrgID}[i] },
	}
}

type updateDefaultTenant struct {
	ID    int64
	OrgID int64
}

func updateDefaultTenantDesc() velox.Desc[updateDefaultTenant] {
	return velox.Desc[updateDefaultTenant]{
		Table: "update_default_tenant", Columns: []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", UpdateDefault: func() any { return int64(1) }}},
		PrimaryKey: []int{0}, Tenant: 1,
		Addr: func(x *updateDefaultTenant, i int) any { return [...]any{&x.ID, &x.OrgID}[i] },
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

func (*hooked) VeloxMeta() *velox.Meta[hooked] { return hookedMeta.Load(hookedDesc) }

func hookedDesc() velox.Desc[hooked] {
	return velox.Desc[hooked]{
		Policy:     func() velox.BoundPolicy { return velox.BindPolicy((*hooked)(nil).Policy()) },
		Hooks:      func() velox.BoundHooks { return velox.BindHooks((*hooked)(nil).Hooks()) },
		Table:      "hooked",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Tag", Name: "tag"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *hooked, i int) any {
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
