package velox

import (
	"context"
	"time"
)

// The Descriptor entities of the root's tests (package velox cannot import
// internal/testmodel, which imports velox). Each is described as velox-gen
// would write it: a struct, a MetaOnce, a VeloxMeta and a Descriptor.

// loadDescriptor builds d's EntityInfo as the entity's first use does.
func loadDescriptor[E any](d Descriptor[E]) (*EntityInfo, error) {
	var o MetaOnce[E]
	m := o.Load(func() Descriptor[E] { return d })
	return m.info, m.err
}

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

func hnoteHook(ctx context.Context, m *Mutation[hnote]) error {
	if f, ok := ctx.Value(hnoteKey{}).(func(*Mutation[hnote]) error); ok {
		return f(m)
	}
	return nil
}

func (*hnote) Hooks() []Hook[hnote] {
	return []Hook[hnote]{func(next Mutator[hnote]) Mutator[hnote] {
		return MutateFunc[hnote](func(ctx context.Context, m *Mutation[hnote]) error {
			if err := hnoteHook(ctx, m); err != nil {
				return err
			}
			return next.Mutate(ctx, m)
		})
	}}
}

var hnoteMeta MetaOnce[hnote]

func (*hnote) VeloxMeta() *Meta[hnote] { return hnoteMeta.Load(hnoteDescriptor) }

func hnoteDescriptor() Descriptor[hnote] {
	return Descriptor[hnote]{
		Hooks: func() BoundHooks { return BindHooks((*hnote)(nil).Hooks()) },
		Table: "hnotes",
		Columns: []ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"}, {Field: "Note", Name: "note"},
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
	ID    NumberCol[hnote, int64]
	Title StringCol[hnote, string]
	Note  StringCol[hnote, string]
	N     NumberCol[hnote, int64]
	Due   NullNumberCol[hnote, int64]
}{NumberCol[hnote, int64]{I: 0}, StringCol[hnote, string]{I: 1}, StringCol[hnote, string]{I: 2}, NumberCol[hnote, int64]{I: 3}, NullNumberCol[hnote, int64]{I: 4}}

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

func orderedDescriptor() Descriptor[ordered] {
	return Descriptor[ordered]{
		Table: "orders",
		Columns: []ColumnDescriptor{
			{Field: "CreatedAt", Name: "created_at"},
			{Field: "UpdatedAt", Name: "updated_at", UpdateDefault: func() any { return time.Now() }},
			{Field: "ID", Name: "id"},
			{Field: "TenantID", Name: "tenant_id"},
			{Field: "Code", Name: "order_code", Default: "none"},
			{Field: "Total", Name: "total"},
			{Field: "ShipAt", Name: "ship_at"},
		},
		PrimaryKey: []int{2},
		Indexes:    []IndexInfo{{Columns: []int{3, 4}, Unique: true}},
		FieldPtr: func(x *ordered, i int) any {
			return [...]any{&x.CreatedAt, &x.UpdatedAt, &x.ID, &x.TenantID, &x.Code, &x.Total, &x.ShipAt}[i]
		},
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

func (*hooked) VeloxMeta() *Meta[hooked] { return hookedMeta.Load(hookedDescriptor) }

func hookedDescriptor() Descriptor[hooked] {
	return Descriptor[hooked]{
		Policy:     func() BoundPolicy { return BindPolicy((*hooked)(nil).Policy()) },
		Hooks:      func() BoundHooks { return BindHooks((*hooked)(nil).Hooks()) },
		Table:      "hooked",
		Columns:    []ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Tag", Name: "tag"}, {Field: "Title", Name: "title"}},
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
