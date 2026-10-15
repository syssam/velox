package testmodel

import "github.com/syssam/velox2"

// Item is a line of an Invoice: Invoice.Items finds it by its InvoiceID, and
// Item.Invoice belongs to the Invoice whose key InvoiceID holds.
type Item struct {
	ID        int64
	InvoiceID int64
	SKU       string
	Qty       int64
	Invoice   velox.One[Invoice]
}
