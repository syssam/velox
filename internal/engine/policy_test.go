package engine_test

import (
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

func TestTicketMetadata(t *testing.T) {
	if err := velox.CheckEntity[testmodel.Ticket](); err != nil {
		t.Fatal(err)
	}
	info := infoOf[testmodel.Ticket]()
	if i, ok := info.Column("org_id"); !ok || info.Columns[i].Field != "OrgID" || !info.Columns[i].Immutable {
		t.Errorf("org_id is %d (%v): %+v", i, ok, info.Columns[i])
	}
}
