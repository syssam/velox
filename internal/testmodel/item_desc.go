// Written by hand in the shape velox-gen will generate (schema plan 3).

package testmodel

import (
	"github.com/syssam/velox2"
)

// Items holds the typed handles of Item's columns and relations.
var Items = struct {
	ID        velox.NumberCol[Item, int64]
	InvoiceID velox.NumberCol[Item, int64]
	SKU       velox.StringCol[Item, string]
	Qty       velox.NumberCol[Item, int64]
	Invoice   velox.Rel[Item, Invoice]
}{
	ID:        velox.NumberCol[Item, int64]{I: 0},
	InvoiceID: velox.NumberCol[Item, int64]{I: 1},
	SKU:       velox.StringCol[Item, string]{I: 2},
	Qty:       velox.NumberCol[Item, int64]{I: 3},
	Invoice:   velox.Rel[Item, Invoice]{I: 0},
}

var itemMeta velox.MetaOnce[Item]

// VeloxMeta is called by velox, not by applications.
func (*Item) VeloxMeta() *velox.Meta[Item] { return itemMeta.Load(itemDesc) }

func itemDesc() velox.Desc[Item] {
	return velox.Desc[Item]{
		Table: "items",
		Columns: []velox.ColumnDesc{
			{Field: "ID", Name: "id"},
			{Field: "InvoiceID", Name: "invoice_id"},
			{Field: "SKU", Name: "sku"},
			{Field: "Qty", Name: "qty"},
		},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Relations:  []velox.Relation{velox.EdgeTo[Invoice]("Invoice", 1, 2)},
		Addr: func(x *Item, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.InvoiceID
			case 2:
				return &x.SKU
			case 3:
				return &x.Qty
			}
			return nil
		},
		RelAddr: func(x *Item, i int) any {
			if i == 0 {
				return &x.Invoice
			}
			return nil
		},
	}
}
