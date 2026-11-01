package apitest

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
)

// A join entity as velox-gen writes it, between tickets and invoices: no tenant of its
// own, and a TenantRef for each side.
type refLink struct {
	TicketID  int64
	InvoiceID int64
}

var refLinks = struct {
	TicketID  velox.NumberCol[refLink, int64]
	InvoiceID velox.NumberCol[refLink, int64]
}{velox.NumberCol[refLink, int64]{I: 0}, velox.NumberCol[refLink, int64]{I: 1}}

var refLinkMeta velox.MetaOnce[refLink]

func (*refLink) VeloxMeta() *velox.Meta[refLink] {
	return refLinkMeta.Load(func() velox.Desc[refLink] {
		return velox.Desc[refLink]{
			Table:      "ref_links",
			Columns:    []velox.ColumnDesc{{Field: "TicketID", Name: "ticket_id"}, {Field: "InvoiceID", Name: "invoice_id"}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			TenantRefs: []velox.TenantRef{
				{Column: 0, Table: "tickets", Key: "id", Tenant: "org_id"},
				{Column: 1, Table: "invoices", Key: "id", Tenant: "org_id"},
			},
			Addr: func(x *refLink, i int) any {
				if i == 0 {
					return &x.TicketID
				}
				return &x.InvoiceID
			},
		}
	})
}

// TestTenantRefJoinRows: a join row has no tenant column, so nothing scoped it: a link
// between tenants needed no tenant at all. Under a tenant both sides must be the
// tenant's; with neither a tenant nor WithAllTenants the write fails, as a statement on
// either side does; WithAllTenants may link anything.
func TestTenantRefJoinRows(t *testing.T) {
	db, raw := refDB(t)
	if _, err := raw.Exec(`CREATE TABLE ref_links (ticket_id integer NOT NULL REFERENCES tickets (id), invoice_id integer NOT NULL REFERENCES invoices (id), PRIMARY KEY (ticket_id, invoice_id))`); err != nil {
		t.Fatal(err)
	}
	links := func() int {
		t.Helper()
		var n int
		if err := raw.QueryRow("SELECT COUNT(*) FROM ref_links").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	bg := context.Background()
	t1 := velox.WithTenant(bg, int64(1))
	for name, c := range map[string]struct {
		link   refLink
		column string
	}{
		"another tenant's ticket":  {refLink{2, 1}, "ticket_id"},
		"another tenant's invoice": {refLink{1, 2}, "invoice_id"},
		"no such ticket":           {refLink{99, 1}, "ticket_id"},
		"no such invoice":          {refLink{1, 99}, "invoice_id"},
	} {
		t.Run(name, func(t *testing.T) {
			wantRefused(t, db.Insert(&c.link).Exec(t1), c.column)
			wantRefused(t, db.InsertAll([]refLink{{1, 1}, c.link}).Exec(t1), c.column)
			if n := links(); n != 0 {
				t.Errorf("%d links", n)
			}
		})
	}
	t.Run("an update that repoints one", func(t *testing.T) {
		_, err := db.Update[refLink]().Set(refLinks.InvoiceID.Set(2)).Where(refLinks.TicketID.EQ(1)).Exec(t1)
		wantRefused(t, err, "invoice_id")
		_, err = db.Update[refLink]().Set(refLinks.InvoiceID.Add(1)).Where(refLinks.TicketID.EQ(1)).Exec(t1)
		if !errors.Is(err, velox.ErrTenantRef) {
			t.Errorf("arithmetic: %v", err)
		}
	})
	t.Run("no tenant", func(t *testing.T) {
		for name, c := range map[string]context.Context{"none": bg, "the zero tenant": velox.WithTenant(bg, int64(0))} {
			if err := db.Insert(&refLink{1, 1}).Exec(c); !errors.Is(err, velox.ErrNoTenant) {
				t.Errorf("%s: %v", name, err)
			}
			if _, _, err := db.Insert(&refLink{1, 1}).SQL(c); !errors.Is(err, velox.ErrNoTenant) {
				t.Errorf("%s: SQL: %v", name, err)
			}
		}
	})
	t.Run("both the tenant's", func(t *testing.T) {
		if err := db.Insert(&refLink{1, 1}).Exec(t1); err != nil {
			t.Fatal(err)
		}
		if n := links(); n != 1 {
			t.Errorf("%d links", n)
		}
		if n, err := db.Update[refLink]().Set(refLinks.InvoiceID.Set(1)).Where(refLinks.TicketID.EQ(1)).Exec(t1); err != nil || n != 1 {
			t.Errorf("%d, %v", n, err)
		}
	})
	t.Run("WithAllTenants", func(t *testing.T) {
		if err := db.Insert(&refLink{2, 1}).Exec(velox.WithAllTenants(bg, "the migration")); err != nil {
			t.Error(err)
		}
	})
	t.Run("a delete is not scoped, nor checked", func(t *testing.T) {
		if n, err := db.Delete[refLink]().Where(refLinks.TicketID.EQ(2)).Exec(t1); err != nil || n != 1 {
			t.Errorf("%d, %v", n, err)
		}
	})
}
