package velox_test

import (
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// TestRouteSkipsADownReplica: a replica in back-off is skipped until its
// back-off ends; with every replica in back-off, reads go to the primary.
func TestRouteSkipsADownReplica(t *testing.T) {
	s := openReplicas(true)
	for _, e := range []*velox.FakeExec{s.p, s.r0, s.r1} {
		e.Rows = [][]any{invoiceRow(1)}
	}
	read := func() {
		t.Helper()
		if _, err := s.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
	}
	velox.SetReplicaDown(s.db, 0, time.Now().Add(time.Hour))
	read()
	read()
	velox.SetReplicaDown(s.db, 1, time.Now().Add(time.Hour))
	read()
	velox.SetReplicaDown(s.db, 0, time.Now().Add(-time.Second)) // its back-off has ended
	read()
	s.check(t, true, [3]int{1, 1, 2}, onR1, onR1, onPrimary, onR0)
}
