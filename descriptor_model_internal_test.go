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
	return m.d.Info, m.d.Err
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

func hnoteHook(ctx context.Context, m *Mutation) error {
	if f, ok := ctx.Value(hnoteKey{}).(func(*Mutation) error); ok {
		return f(m)
	}
	return nil
}

func (*hnote) Hooks() []Hook {
	return []Hook{func(next Mutator) Mutator {
		return MutateFunc(func(ctx context.Context, m *Mutation) error {
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
		Schema: (*hnote)(nil),
		Table:  "hnotes",
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

func (*hooked) Hooks() []Hook {
	return []Hook{
		Before(func(_ context.Context, m *Mutation) error {
			for r := range m.Rows() {
				if err := r.Set("tag", r.Value("tag").(string)+"entity;"); err != nil {
					return err
				}
			}
			return nil
		}, OpInsert),
		Before(func(_ context.Context, m *Mutation) error {
			return m.Set("tag", "updated")
		}, OpUpdate),
		Before(func(_ context.Context, m *Mutation) error {
			return m.Set("tag", "x")
		}, OpDelete),
	}
}

func (*hooked) Policy() Policy {
	return Policy{
		Read: func(context.Context) (Predicate, error) { return FieldEQ("title", "r"), nil },
		Insert: func(_ context.Context, r Row) error {
			if r.Value("title") == "" {
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
		Schema: (*hooked)(nil),

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
