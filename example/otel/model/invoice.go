// Package model holds the otel example's entity, as a billing team would
// own it. invoice_velox.go beside this file is written by go tool velox-gen.
package model

import "github.com/syssam/velox2"

// Status is where an invoice is in its life.
type Status string

// The statuses of an invoice.
const (
	Sent Status = "sent"
	Paid Status = "paid"
)

// Invoice is a bill sent to a customer. No two invoices share a number.
type Invoice struct {
	ID         int64
	Number     string
	Customer   string
	Status     Status
	TotalCents int64
}

// Schema names the table and the unique number.
func (x *Invoice) Schema(s *velox.Schema) {
	s.Table("invoices")
	s.Unique(&x.Number)
}
