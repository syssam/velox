package fixture

import (
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/mixin/softdelete"
)

// SOwner owns docs and pins one, which may be soft-deleted. It has no
// behaviour.
type SOwner struct {
	ID       int64
	Name     string
	PinnedID *int64
	Docs     velox.Many[SDoc]
	Pinned   velox.One[SDoc]
}

// SOwners are SOwner's handles.
var SOwners = struct {
	ID       velox.NumberCol[SOwner, int64]
	Name     velox.StringCol[SOwner, string]
	PinnedID velox.NullNumberCol[SOwner, int64]
	Docs     velox.Rel[SOwner, SDoc]
	Pinned   velox.Rel[SOwner, SDoc]
}{
	velox.NumberCol[SOwner, int64]{I: 0}, velox.StringCol[SOwner, string]{I: 1}, velox.NullNumberCol[SOwner, int64]{I: 2},
	velox.Rel[SOwner, SDoc]{I: 0}, velox.Rel[SOwner, SDoc]{I: 1},
}

var sOwnerMeta velox.MetaOnce[SOwner]

// VeloxMeta returns SOwner's metadata.
func (*SOwner) VeloxMeta() *velox.Meta[SOwner] { return sOwnerMeta.Load(sOwnerDescriptor) }

func sOwnerDescriptor() velox.Descriptor[SOwner] {
	return velox.Descriptor[SOwner]{
		Table:      "s_owners",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}, {Field: "PinnedID", Name: "pinned_id"}},
		PrimaryKey: []int{0},
		Edges: []velox.EdgeDescriptor{
			velox.EdgeTo[SDoc]("docs", 0, 2),
			velox.EdgeTo[SDoc]("pinned", 2, 0),
		},
		FieldPtr: func(x *SOwner, i int) any {
			return [...]any{&x.ID, &x.Name, &x.PinnedID}[i]
		},
		EdgeFieldPtr: func(x *SOwner, i int) any {
			return [...]any{&x.Docs, &x.Pinned}[i]
		},
	}
}

// SDoc lists, as velox-gen binds them, an InOrg-like tenancy mixin
// (OrgPolicy, OrgHooks) and softdelete.Mixin, in that order: its delete sets
// deleted_at, and its reads and writes skip rows whose deleted_at is set.
type SDoc struct {
	ID        int64
	OrgID     int64
	OwnerID   int64
	Title     string
	DeletedAt *time.Time
	Owner     velox.One[SOwner]
}

// SDocs are SDoc's handles.
var SDocs = struct {
	ID        velox.NumberCol[SDoc, int64]
	OrgID     velox.NumberCol[SDoc, int64]
	OwnerID   velox.NumberCol[SDoc, int64]
	Title     velox.StringCol[SDoc, string]
	DeletedAt velox.NullTimeCol[SDoc, time.Time]
	Owner     velox.Rel[SDoc, SOwner]
}{
	velox.NumberCol[SDoc, int64]{I: 0}, velox.NumberCol[SDoc, int64]{I: 1}, velox.NumberCol[SDoc, int64]{I: 2},
	velox.StringCol[SDoc, string]{I: 3}, velox.NullTimeCol[SDoc, time.Time]{I: 4}, velox.Rel[SDoc, SOwner]{I: 0},
}

var sDocMeta velox.MetaOnce[SDoc]

// VeloxMeta returns SDoc's metadata.
func (*SDoc) VeloxMeta() *velox.Meta[SDoc] { return sDocMeta.Load(sDocDescriptor) }

func sDocDescriptor() velox.Descriptor[SDoc] {
	return velox.Descriptor[SDoc]{
		Table: "s_docs",
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true}, {Field: "OwnerID", Name: "owner_id"},
			{Field: "Title", Name: "title"}, {Field: "DeletedAt", Name: "deleted_at"},
		},
		PrimaryKey: []int{0},
		Indexes:    []velox.IndexInfo{{Columns: []int{1, 3}, Unique: true}},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[SOwner]("owner", 2, 0)},
		Schema:     Fns{Policy: []func() velox.Policy{OrgPolicy}, Hooks: []func() []velox.Hook{OrgHooks, softdelete.Mixin{}.Hooks}, Interceptors: []func() []velox.Interceptor{softdelete.Mixin{}.Interceptors}},

		FieldPtr: func(x *SDoc, i int) any {
			return [...]any{&x.ID, &x.OrgID, &x.OwnerID, &x.Title, &x.DeletedAt}[i]
		},
		EdgeFieldPtr: func(x *SDoc, i int) any {
			if i == 0 {
				return &x.Owner
			}
			return nil
		},
	}
}

// SoftDeleteDDL creates s_owners and s_docs on SQLite, with the partial
// unique index of (org_id, title) over live rows.
var SoftDeleteDDL = []string{
	`CREATE TABLE "s_owners" ("id" INTEGER PRIMARY KEY, "name" TEXT NOT NULL, "pinned_id" INTEGER)`,
	`CREATE TABLE "s_docs" ("id" INTEGER PRIMARY KEY, "org_id" INTEGER NOT NULL, "owner_id" INTEGER NOT NULL, "title" TEXT NOT NULL, "deleted_at" DATETIME)`,
	`CREATE UNIQUE INDEX "s_docs_org_title" ON "s_docs" ("org_id", "title") WHERE "deleted_at" IS NULL`,
}
