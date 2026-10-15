// Written by hand in the shape velox-gen will generate (schema plan 3).

package testmodel

import (
	"github.com/syssam/velox2"
)

// Tickets holds the typed handles of Ticket's columns and relations.
var Tickets = struct {
	CreatedBy velox.StringCol[Ticket, string]
	ID        velox.NumberCol[Ticket, int64]
	OrgID     velox.NumberCol[Ticket, int64]
	Owner     velox.StringCol[Ticket, string]
	Title     velox.StringCol[Ticket, string]
	Done      velox.Col[Ticket, bool]
	InvoiceID velox.NumberCol[Ticket, int64]
	ParentID  velox.NullNumberCol[Ticket, int64]
	Invoice   velox.Rel[Ticket, Invoice]
	Parent    velox.Rel[Ticket, Ticket]
}{
	CreatedBy: velox.StringCol[Ticket, string]{I: 0},
	ID:        velox.NumberCol[Ticket, int64]{I: 1},
	OrgID:     velox.NumberCol[Ticket, int64]{I: 2},
	Owner:     velox.StringCol[Ticket, string]{I: 3},
	Title:     velox.StringCol[Ticket, string]{I: 4},
	Done:      velox.Col[Ticket, bool]{I: 5},
	InvoiceID: velox.NumberCol[Ticket, int64]{I: 6},
	ParentID:  velox.NullNumberCol[Ticket, int64]{I: 7},
	Invoice:   velox.Rel[Ticket, Invoice]{I: 0},
	Parent:    velox.Rel[Ticket, Ticket]{I: 1},
}

var ticketMeta velox.MetaOnce[Ticket]

// VeloxMeta is called by velox, not by applications.
func (*Ticket) VeloxMeta() *velox.Meta[Ticket] { return ticketMeta.Load(ticketDesc) }

func ticketDesc() velox.Desc[Ticket] {
	return velox.Desc[Ticket]{
		Table: "tickets",
		Columns: []velox.ColumnDesc{
			{Field: "CreatedBy", Name: "created_by"},
			{Field: "ID", Name: "id"},
			{Field: "OrgID", Name: "org_id"},
			{Field: "Owner", Name: "owner"},
			{Field: "Title", Name: "title"},
			{Field: "Done", Name: "done"},
			{Field: "InvoiceID", Name: "invoice_id"},
			{Field: "ParentID", Name: "parent_id"},
		},
		PrimaryKey: []int{1},
		Tenant:     2,
		Indexes:    []velox.IndexInfo{{Columns: []int{2, 4}, Unique: true}},
		Relations: []velox.Relation{
			velox.EdgeTo[Invoice]("Invoice", 6, 2),
			velox.EdgeTo[Ticket]("Parent", 7, 1),
		},
		Addr: func(x *Ticket, i int) any {
			switch i {
			case 0:
				return &x.CreatedBy
			case 1:
				return &x.ID
			case 2:
				return &x.OrgID
			case 3:
				return &x.Owner
			case 4:
				return &x.Title
			case 5:
				return &x.Done
			case 6:
				return &x.InvoiceID
			case 7:
				return &x.ParentID
			}
			return nil
		},
		RelAddr: func(x *Ticket, i int) any {
			switch i {
			case 0:
				return &x.Invoice
			case 1:
				return &x.Parent
			}
			return nil
		},
	}
}
