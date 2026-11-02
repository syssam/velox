package testmodel

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/scope"
)

// Org is the organization the tests act for: Invoice and Ticket are confined to
// it (Ticket's Policy wraps the scope with its own rules).
var Org = scope.NewKey[int64]("org")

var (
	invoiceScope = scope.By(Org, Invoices.OrgID.EQ, func(x *Invoice) *int64 { return &x.OrgID })
	ticketScope  = scope.By(Org, Tickets.OrgID.EQ, func(x *Ticket) *int64 { return &x.OrgID })
)

// Policy confines Invoice to the context's Org. velox calls it once, on a nil
// *Invoice.
func (*Invoice) Policy() velox.Policy[Invoice] { return invoiceScope.Policy() }

// Hooks fills OrgID on insert.
func (*Invoice) Hooks() []velox.Hook[Invoice] { return []velox.Hook[Invoice]{invoiceScope.Fill()} }
