// Written by hand in the shape velox-gen will generate (schema plan 3).

package testmodel

import (
	"time"

	"github.com/syssam/velox2"
)

// Invoices holds the typed handles of Invoice's columns and relations.
var Invoices = struct {
	CreatedAt velox.OrderedCol[Invoice, time.Time]
	UpdatedAt velox.OrderedCol[Invoice, time.Time]
	ID        velox.NumberCol[Invoice, int64]
	OrgID     velox.NumberCol[Invoice, int64]
	Number    velox.StringCol[Invoice, string]
	Total     velox.NumberCol[Invoice, float64]
	Paid      velox.Col[Invoice, bool]
	PaidAt    velox.NullOrderedCol[Invoice, time.Time]
	Note      velox.NullStringCol[Invoice, string]
	Data      velox.Col[Invoice, []byte]
	Items     velox.Rel[Invoice, Item]
}{
	CreatedAt: velox.OrderedCol[Invoice, time.Time]{I: 0},
	UpdatedAt: velox.OrderedCol[Invoice, time.Time]{I: 1},
	ID:        velox.NumberCol[Invoice, int64]{I: 2},
	OrgID:     velox.NumberCol[Invoice, int64]{I: 3},
	Number:    velox.StringCol[Invoice, string]{I: 4},
	Total:     velox.NumberCol[Invoice, float64]{I: 5},
	Paid:      velox.Col[Invoice, bool]{I: 6},
	PaidAt:    velox.NullOrderedCol[Invoice, time.Time]{I: 7},
	Note:      velox.NullStringCol[Invoice, string]{I: 8},
	Data:      velox.Col[Invoice, []byte]{I: 9},
	Items:     velox.Rel[Invoice, Item]{I: 0},
}

var invoiceMeta velox.MetaOnce[Invoice]

// VeloxMeta is called by velox, not by applications.
func (*Invoice) VeloxMeta() *velox.Meta[Invoice] { return invoiceMeta.Load(invoiceDesc) }

func invoiceDesc() velox.Desc[Invoice] {
	return velox.Desc[Invoice]{
		Table: "invoices",
		Columns: []velox.ColumnDesc{
			{Field: "CreatedAt", Name: "created_at", DefaultFunc: func() any { return time.Now() }},
			{Field: "UpdatedAt", Name: "updated_at", UpdateDefault: func() any { return time.Now() }},
			{Field: "ID", Name: "id"},
			{Field: "OrgID", Name: "org_id"},
			{Field: "Number", Name: "number"},
			{Field: "Total", Name: "total"},
			{Field: "Paid", Name: "paid"},
			{Field: "PaidAt", Name: "paid_at"},
			{Field: "Note", Name: "note"},
			{Field: "Data", Name: "data"},
		},
		PrimaryKey: []int{2},
		Tenant:     3,
		Indexes:    []velox.IndexInfo{{Columns: []int{3, 4}, Unique: true}},
		Relations:  []velox.Relation{velox.EdgeTo[Item]("Items", 2, 1)},
		Addr: func(x *Invoice, i int) any {
			switch i {
			case 0:
				return &x.CreatedAt
			case 1:
				return &x.UpdatedAt
			case 2:
				return &x.ID
			case 3:
				return &x.OrgID
			case 4:
				return &x.Number
			case 5:
				return &x.Total
			case 6:
				return &x.Paid
			case 7:
				return &x.PaidAt
			case 8:
				return &x.Note
			case 9:
				return &x.Data
			}
			return nil
		},
		RelAddr: func(x *Invoice, i int) any {
			if i == 0 {
				return &x.Items
			}
			return nil
		},
	}
}
