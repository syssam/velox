package velox

import (
	"context"
	"errors"
	"testing"
)

// moveOrg is tenant-scoped, and its BeforeInsert moves every row to tenant 9.
type moveOrg struct {
	ID    int64
	OrgID int64
}

func (x *moveOrg) Schema(s *Schema) {
	s.Tenant(&x.OrgID)
	s.BeforeInsert(func(_ context.Context, x *moveOrg) error {
		x.OrgID = 9
		return nil
	})
}

var moveOrgMeta MetaOnce[moveOrg]

func (*moveOrg) VeloxMeta() *Meta[moveOrg] {
	return moveOrgMeta.Get(func() Shape[moveOrg] {
		return Shape[moveOrg]{Columns: []string{"ID", "OrgID"}, Addr: func(x *moveOrg, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.OrgID
		}}
	})
}

// TestBeforeInsertCannotMoveTheTenant is Review Focus 4: the tenant is
// checked again after the BeforeInsert hooks.
func TestBeforeInsertCannotMoveTheTenant(t *testing.T) {
	e := &FakeExec{Rows: [][]any{{int64(1)}}}
	db := OpenFake(Postgres, e)
	x := moveOrg{}
	err := db.Insert(&x).Exec(WithTenant(context.Background(), int64(7)))
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("got %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	if err := db.InsertAll([]moveOrg{{ID: 1}}).Exec(WithTenant(context.Background(), int64(7))); !errors.Is(err, ErrTenantMismatch) {
		t.Errorf("InsertAll: %v", err)
	}
}
