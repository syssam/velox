package velox

import (
	"context"
	"time"
)

// The Desc entities of the internal tests (package velox cannot import
// internal/testmodel, which imports velox). Each is described as velox-gen
// would write it: a struct, a MetaOnce, a VeloxMeta and a Desc.

// loadDesc builds d's EntityInfo as the entity's first use does.
func loadDesc[E any](d Desc[E]) (*EntityInfo, error) {
	var o MetaOnce[E]
	m := o.Load(func() Desc[E] { return d })
	return m.info, m.err
}

// hnote's hooks, for updates and deletes, run the function in the context
// under hnoteKey, if any, before the write.
type hnote struct {
	ID    int64
	Title string
	Note  string
	N     int64
	Due   *int64
}

type hnoteKey struct{}

func hnoteHook(ctx context.Context, m *Mutation[hnote]) error {
	if f, ok := ctx.Value(hnoteKey{}).(func(*Mutation[hnote]) error); ok {
		return f(m)
	}
	return nil
}

func (*hnote) Hooks() []Hook[hnote] {
	return []Hook[hnote]{On(func(next Mutator[hnote]) Mutator[hnote] {
		return MutateFunc[hnote](func(ctx context.Context, m *Mutation[hnote]) error {
			if err := hnoteHook(ctx, m); err != nil {
				return err
			}
			return next.Mutate(ctx, m)
		})
	}, OpUpdate, OpDelete)}
}

var hnoteMeta MetaOnce[hnote]

func (*hnote) VeloxMeta() *Meta[hnote] { return hnoteMeta.Load(hnoteDesc) }

func hnoteDesc() Desc[hnote] {
	return Desc[hnote]{
		Hooks: func() BoundHooks { return BindHooks((*hnote)(nil).Hooks()) },
		Table: "hnotes",
		Columns: []ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"}, {Field: "Note", Name: "note"},
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
	ID    NumberCol[hnote, int64]
	Title StringCol[hnote, string]
	Note  StringCol[hnote, string]
	N     NumberCol[hnote, int64]
	Due   NullNumberCol[hnote, int64]
}{NumberCol[hnote, int64]{I: 0}, StringCol[hnote, string]{I: 1}, StringCol[hnote, string]{I: 2}, NumberCol[hnote, int64]{I: 3}, NullNumberCol[hnote, int64]{I: 4}}

// moveOrg is tenant-scoped, and its insert hook moves every row to tenant 9.
type moveOrg struct {
	ID    int64
	OrgID int64
}

func (*moveOrg) Hooks() []Hook[moveOrg] {
	return []Hook[moveOrg]{func(next Mutator[moveOrg]) Mutator[moveOrg] {
		return MutateFunc[moveOrg](func(ctx context.Context, m *Mutation[moveOrg]) error {
			for _, x := range m.Rows() {
				x.OrgID = 9
			}
			return next.Mutate(ctx, m)
		})
	}}
}

var moveOrgMeta MetaOnce[moveOrg]

func (*moveOrg) VeloxMeta() *Meta[moveOrg] { return moveOrgMeta.Load(moveOrgDesc) }

func moveOrgDesc() Desc[moveOrg] {
	return Desc[moveOrg]{
		Hooks:      func() BoundHooks { return BindHooks((*moveOrg)(nil).Hooks()) },
		Table:      "move_org",
		Columns:    []ColumnDesc{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}},
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
	Staff Many[employee]
}

var companyMeta MetaOnce[company]

func (*company) VeloxMeta() *Meta[company] { return companyMeta.Load(companyDesc) }

func companyDesc() Desc[company] {
	return Desc[company]{
		Table:      "company",
		Columns:    []ColumnDesc{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Relations:  []Relation{EdgeTo[employee]("Staff", 0, 1)}, // employee.CompanyID
		Addr:       func(x *company, i int) any { return &x.ID },
		RelAddr:    func(x *company, i int) any { return &x.Staff },
	}
}

type employee struct {
	ID        int64
	CompanyID int64
	BossID    *int64
	Boss      One[employee]
	Company   One[company]
}

var employeeMeta MetaOnce[employee]

func (*employee) VeloxMeta() *Meta[employee] { return employeeMeta.Load(employeeDesc) }

func employeeDesc() Desc[employee] {
	return Desc[employee]{
		Table:      "employee",
		Columns:    []ColumnDesc{{Field: "ID", Name: "id"}, {Field: "CompanyID", Name: "company_id"}, {Field: "BossID", Name: "boss_id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Relations:  []Relation{EdgeTo[employee]("Boss", 2, 0), EdgeTo[company]("Company", 1, 0)},
		Addr: func(x *employee, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.CompanyID
			}
			return &x.BossID
		},
		RelAddr: func(x *employee, i int) any {
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
	Lines Many[keyedLine]
}

var keyedMeta MetaOnce[keyed]

func (*keyed) VeloxMeta() *Meta[keyed] { return keyedMeta.Load(keyedDesc) }

func keyedDesc() Desc[keyed] {
	return Desc[keyed]{
		Table:      "keyed",
		Columns:    []ColumnDesc{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Relations:  []Relation{EdgeTo[keyedLine]("Lines", 0, 1)},
		Addr:       func(x *keyed, i int) any { return &x.ID },
		RelAddr:    func(x *keyed, i int) any { return &x.Lines },
	}
}

type keyedLine struct {
	ID      int64
	KeyedID int64 // an int64 cannot refer to keyed's string key
}

var keyedLineMeta MetaOnce[keyedLine]

func (*keyedLine) VeloxMeta() *Meta[keyedLine] { return keyedLineMeta.Load(keyedLineDesc) }

func keyedLineDesc() Desc[keyedLine] {
	return Desc[keyedLine]{
		Table:      "keyed_line",
		Columns:    []ColumnDesc{{Field: "ID", Name: "id"}, {Field: "KeyedID", Name: "keyed_id"}},
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
	Lines Many[blobLine]
}

var blobOwnerMeta MetaOnce[blobOwner]

func (*blobOwner) VeloxMeta() *Meta[blobOwner] { return blobOwnerMeta.Load(blobOwnerDesc) }

func blobOwnerDesc() Desc[blobOwner] {
	return Desc[blobOwner]{
		Table:      "blob_owner",
		Columns:    []ColumnDesc{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Relations:  []Relation{EdgeTo[blobLine]("Lines", 0, 1)},
		Addr:       func(x *blobOwner, i int) any { return &x.ID },
		RelAddr:    func(x *blobOwner, i int) any { return &x.Lines },
	}
}

type blobLine struct {
	ID    int64
	Owner []byte
}

var blobLineMeta MetaOnce[blobLine]

func (*blobLine) VeloxMeta() *Meta[blobLine] { return blobLineMeta.Load(blobLineDesc) }

func blobLineDesc() Desc[blobLine] {
	return Desc[blobLine]{
		Table:      "blob_line",
		Columns:    []ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Owner", Name: "owner"}},
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
	T  One[brokenTarget]
}

var invalidHolderMeta MetaOnce[invalidHolder]

func (*invalidHolder) VeloxMeta() *Meta[invalidHolder] {
	return invalidHolderMeta.Load(invalidHolderDesc)
}

func invalidHolderDesc() Desc[invalidHolder] {
	return Desc[invalidHolder]{
		Table:      "invalid_holder",
		Columns:    []ColumnDesc{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Relations:  []Relation{EdgeTo[brokenTarget]("T", 0, 0)},
		Addr:       func(x *invalidHolder, i int) any { return &x.ID },
		RelAddr:    func(x *invalidHolder, i int) any { return &x.T },
	}
}

type brokenTarget struct{ ID int64 }

var brokenTargetMeta MetaOnce[brokenTarget]

func (*brokenTarget) VeloxMeta() *Meta[brokenTarget] { return brokenTargetMeta.Load(brokenTargetDesc) }

func brokenTargetDesc() Desc[brokenTarget] {
	return Desc[brokenTarget]{
		Table:      "broken_target",
		Columns:    []ColumnDesc{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
	}
}

type stampCols struct {
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ordered has an embedded struct, a nullable column, a column name that is
// not its field's, an SQL type, a default and an update default.
type ordered struct {
	stampCols
	ID       int64
	TenantID int64
	Code     string
	Total    float64
	ShipAt   *time.Time
}

func orderedDesc() Desc[ordered] {
	return Desc[ordered]{
		Table: "orders",
		Columns: []ColumnDesc{
			{Field: "CreatedAt", Name: "created_at"},
			{Field: "UpdatedAt", Name: "updated_at", UpdateDefault: func() any { return time.Now() }},
			{Field: "ID", Name: "id"},
			{Field: "TenantID", Name: "tenant_id"},
			{Field: "Code", Name: "order_code", Default: "none"},
			{Field: "Total", Name: "total", SQLType: "numeric(18,2)"},
			{Field: "ShipAt", Name: "ship_at"},
		},
		PrimaryKey: []int{2},
		Tenant:     3,
		Indexes:    []IndexInfo{{Columns: []int{3, 4}, Unique: true}},
		Addr: func(x *ordered, i int) any {
			return [...]any{&x.CreatedAt, &x.UpdatedAt, &x.ID, &x.TenantID, &x.Code, &x.Total, &x.ShipAt}[i]
		},
	}
}

// The tenant column's refusals (validate), each on an entity of its own.
type nullTenant struct {
	ID    int64
	OrgID *int64
}

func nullTenantDesc() Desc[nullTenant] {
	return Desc[nullTenant]{
		Table: "null_tenant", Columns: []ColumnDesc{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id"}},
		PrimaryKey: []int{0}, Tenant: 1,
		Addr: func(x *nullTenant, i int) any { return [...]any{&x.ID, &x.OrgID}[i] },
	}
}

type sliceTenant struct {
	ID  int64
	Org []byte
}

func sliceTenantDesc() Desc[sliceTenant] {
	return Desc[sliceTenant]{
		Table: "slice_tenant", Columns: []ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Org", Name: "org"}},
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

func defaultTenantDesc() Desc[defaultTenant] {
	return Desc[defaultTenant]{
		Table: "default_tenant", Columns: []ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Org", Name: "org", Default: "acme"}},
		PrimaryKey: []int{0}, Tenant: 1,
		Addr: func(x *defaultTenant, i int) any { return [...]any{&x.ID, &x.Org}[i] },
	}
}

type defaultFuncTenant struct {
	ID    int64
	OrgID int64
}

func defaultFuncTenantDesc() Desc[defaultFuncTenant] {
	return Desc[defaultFuncTenant]{
		Table: "default_func_tenant", Columns: []ColumnDesc{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", DefaultFunc: func() any { return int64(1) }}},
		PrimaryKey: []int{0}, Tenant: 1,
		Addr: func(x *defaultFuncTenant, i int) any { return [...]any{&x.ID, &x.OrgID}[i] },
	}
}

type updateDefaultTenant struct {
	ID    int64
	OrgID int64
}

func updateDefaultTenantDesc() Desc[updateDefaultTenant] {
	return Desc[updateDefaultTenant]{
		Table: "update_default_tenant", Columns: []ColumnDesc{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", UpdateDefault: func() any { return int64(1) }}},
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
	hookedTag   = Col[hooked, string]{I: 1}
	hookedTitle = Col[hooked, string]{I: 2}
)

func (*hooked) Hooks() []Hook[hooked] {
	return []Hook[hooked]{
		On(func(next Mutator[hooked]) Mutator[hooked] {
			return MutateFunc[hooked](func(ctx context.Context, m *Mutation[hooked]) error {
				for _, x := range m.Rows() {
					x.Tag += "entity;"
				}
				return next.Mutate(ctx, m)
			})
		}, OpInsert),
		On(func(next Mutator[hooked]) Mutator[hooked] {
			return MutateFunc[hooked](func(ctx context.Context, m *Mutation[hooked]) error {
				m.Set(hookedTag.Set("updated"))
				return next.Mutate(ctx, m)
			})
		}, OpUpdate),
		On(func(next Mutator[hooked]) Mutator[hooked] {
			return MutateFunc[hooked](func(ctx context.Context, m *Mutation[hooked]) error {
				m.Set(hookedTag.Set("x"))
				return next.Mutate(ctx, m)
			})
		}, OpDelete),
	}
}

func (*hooked) Policy() Policy[hooked] {
	return Policy[hooked]{
		Read: func(context.Context) (Pred[hooked], error) { return hookedTitle.EQ("r"), nil },
		Insert: func(_ context.Context, x *hooked) error {
			if x.Title == "" {
				return Deny("no title")
			}
			return nil
		},
	}
}

var hookedMeta MetaOnce[hooked]

func (*hooked) VeloxMeta() *Meta[hooked] { return hookedMeta.Load(hookedDesc) }

func hookedDesc() Desc[hooked] {
	return Desc[hooked]{
		Policy:     func() BoundPolicy { return BindPolicy((*hooked)(nil).Policy()) },
		Hooks:      func() BoundHooks { return BindHooks((*hooked)(nil).Hooks()) },
		Table:      "hooked",
		Columns:    []ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Tag", Name: "tag"}, {Field: "Title", Name: "title"}},
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
