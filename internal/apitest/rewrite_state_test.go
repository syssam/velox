package apitest

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// replaceWhole is a Rewrite hook that replaces the whole statement
// (*st = velox.Statement{…}), keeping only its exported fields.
func replaceWhole(_ context.Context, st *velox.Statement) error {
	freshStatement(st)
	return nil
}

// TestRewriteReplacingStatementKeepsRecordState: a Rewrite hook that
// replaces the whole statement does not lose velox's read-back state
// (ret, fill, dflt, softDelete): the generated key and the declared
// defaults still land in the entity on PostgreSQL, a mixed InsertAll still
// writes DEFAULT cells, MySQL still reads its defaulted columns back, and a
// recorded soft delete on PostgreSQL 18 is still a delete, Soft.
func TestRewriteReplacingStatementKeepsRecordState(t *testing.T) {
	hook := velox.WithRewrite(replaceWhole)

	// (a) PostgreSQL: RETURNING fills the key and the defaults.
	e := &fakeconn.Exec{Rows: [][]any{{int64(1), "new", int64(7), "n"}}}
	a := dDef{Name: "a"}
	if err := openFake(velox.Postgres, e, hook).Insert(&a).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if a.ID != 1 || a.Status != "new" || a.Score != 7 || a.Note == nil || *a.Note != "n" {
		t.Errorf("PostgreSQL read back %+v", a)
	}

	// (b) A mixed InsertAll writes DEFAULT cells, not the Go zero.
	e = &fakeconn.Exec{Rows: [][]any{{int64(1), "new", int64(1), "n"}, {int64(2), "new", int64(7), "n"}}}
	xs := []dDef{{Name: "x", Score: 1}, {Name: "y"}}
	if err := openFake(velox.Postgres, e, hook).InsertAll(xs).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 1 || !strings.Contains(e.Log[0], "VALUES ($1, $2), ($3, DEFAULT)") || xs[1].ID != 2 || xs[1].Score != 7 {
		t.Errorf("InsertAll: %q, %+v", e.Log, xs)
	}

	// (c) MySQL reads the defaulted columns back by key.
	e = &fakeconn.Exec{Affected: 1, LastID: 9, HasLastID: true, Results: [][][]any{{{"new", int64(7), "n"}}}}
	m := dDef{Name: "m"}
	if err := openFake(velox.MySQL, e, hook).Insert(&m).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 2 || m.ID != 9 || m.Status != "new" || m.Score != 7 {
		t.Errorf("MySQL: %q, %+v", e.Log, m)
	}

	// (d) A recorded soft delete on PostgreSQL 18 reports a delete, Soft
	// (the tracker reads Soft from velox's statement, not the hooks' copy).
	e = &fakeconn.Exec{Results: [][][]any{{pair([]any{int64(2), int64(1), int64(1), "b", nil}, []any{int64(2), int64(1), int64(1), "b", t0})}}}
	var rec velox.Recorder
	if _, err := openFake18(e, 180000, hook).Record(&rec).Delete[fixture.SDoc]().Where(fixture.SDocs.ID.EQ(2)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if cs := slices.Collect(rec.All()); len(cs) != 1 || cs[0].Op != velox.OpDelete || !cs[0].Soft {
		t.Errorf("soft delete Changes %+v, want one delete, Soft", cs)
	}
}
