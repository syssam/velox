package apitest

import (
	"errors"
	"reflect"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// refOrg is a named id of the kind of refLink's targets' tenant columns.
type refOrg int64

// TestTenantRefJoinTenantKind: a join row has no tenant column, so its tenant was
// bound as given. On SQLite and MySQL "2" then matches tenant 2, and on MySQL "2x"
// does, or 2 matches every text tenant that starts with it. It now converts as a scoped
// entity's does, to the kind each TenantRef states.
func TestTenantRefJoinTenantKind(t *testing.T) {
	const check = `SELECT "id" FROM "tickets" WHERE "id" IN ($1) AND "org_id" = $2`
	t.Run("refused", func(t *testing.T) {
		for name, c := range map[string]struct {
			id   any
			want error
		}{
			"a string":      {"2", velox.ErrTenantMismatch},
			"a float":       {2.0, velox.ErrTenantMismatch},
			"another kind":  {refText("2"), velox.ErrTenantMismatch},
			"too large":     {uint64(1 << 63), velox.ErrTenantMismatch},
			"a named zero":  {refOrg(0), velox.ErrNoTenant},
			"a zero string": {"", velox.ErrTenantMismatch},
		} {
			t.Run(name, func(t *testing.T) {
				e := &fakeconn.Exec{Affected: 1, Answer: passRefs}
				db := openFake(velox.Postgres, e)
				if err := db.Insert(&refLink{1, 1}).Exec(tenant(c.id)); !errors.Is(err, c.want) {
					t.Errorf("Insert: %v, want %v", err, c.want)
				}
				if _, _, err := db.Insert(&refLink{1, 1}).SQL(tenant(c.id)); !errors.Is(err, c.want) {
					t.Errorf("SQL: %v, want %v", err, c.want)
				}
				if _, err := db.Update[refLink]().Set(refLinks.InvoiceID.Set(1)).Where(refLinks.TicketID.EQ(1)).Exec(tenant(c.id)); !errors.Is(err, c.want) {
					t.Errorf("Update: %v, want %v", err, c.want)
				}
				if e.Calls != 0 {
					t.Errorf("%d sent: %q", e.Calls, e.Log)
				}
			})
		}
	})
	t.Run("converted", func(t *testing.T) {
		for name, c := range map[string]struct {
			id   any
			want any
		}{
			"int64":   {int64(7), int64(7)},
			"int":     {7, int64(7)},
			"uint8":   {uint8(7), int64(7)},
			"a named": {refOrg(7), refOrg(7)},
		} {
			t.Run(name, func(t *testing.T) {
				e := &fakeconn.Exec{Affected: 1, Answer: passRefs}
				db := openFake(velox.Postgres, e)
				if err := db.Insert(&refLink{1, 1}).Exec(tenant(c.id)); err != nil {
					t.Fatal(err)
				}
				if len(e.Log) != 3 || e.Log[0] != check || !reflect.DeepEqual(e.ArgLog[0], []any{int64(1), c.want}) {
					t.Errorf("%q %#v", e.Log, e.ArgLog)
				}
			})
		}
	})
}

// refText is a named id of a kind no tenant column of refLink's targets has.
type refText string
