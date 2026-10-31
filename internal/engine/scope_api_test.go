package engine_test

import (
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestTicketMetadata(t *testing.T) {
	if err := velox.CheckEntity[testmodel.Ticket](); err != nil {
		t.Fatal(err)
	}
	info := infoOf[testmodel.Ticket]()
	if i, ok := info.Column("org_id"); !ok || info.Tenant != i || info.Columns[i].Field != "OrgID" {
		t.Errorf("tenant %d, org_id is %d (%v)", info.Tenant, i, ok)
	}
}
