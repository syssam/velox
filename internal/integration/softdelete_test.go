package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/schema/mixin/softdelete"
	"github.com/syssam/velox/scope"
)

// softTables creates the fixture's s_owners and s_docs on tg: owners 1
// (pinned doc 2) and 2, and docs (id: org, owner, title) 1: 1, 1, a;
// 2: 1, 1, b; 3: 2, 2, c. On PostgreSQL and SQLite, (org_id, title) is
// unique over live rows, by a partial index; MySQL has none.
func softTables(t *testing.T, tg target) {
	t.Helper()
	text, ts := "text", "timestamptz"
	switch tg.d {
	case velox.MySQL:
		text, ts = "VARCHAR(64)", "DATETIME(6)"
	case velox.SQLite:
		ts = "DATETIME"
	}
	tg.exec(t, "DROP TABLE IF EXISTS s_docs")
	tg.exec(t, "DROP TABLE IF EXISTS s_owners")
	tg.exec(t, "CREATE TABLE s_owners (id bigint PRIMARY KEY, name "+text+" NOT NULL, pinned_id bigint)")
	tg.exec(t, "CREATE TABLE s_docs (id bigint PRIMARY KEY, org_id bigint NOT NULL, owner_id bigint NOT NULL, title "+text+" NOT NULL, deleted_at "+ts+")")
	if tg.d != velox.MySQL {
		tg.exec(t, "CREATE UNIQUE INDEX s_docs_org_title ON s_docs (org_id, title) WHERE deleted_at IS NULL")
	}
	tg.exec(t, "INSERT INTO s_owners (id, name, pinned_id) VALUES (?, ?, ?), (?, ?, ?)", 1, "o1", 2, 2, "o2", nil)
	tg.exec(t, "INSERT INTO s_docs (id, org_id, owner_id, title) VALUES (?, ?, ?, ?), (?, ?, ?, ?), (?, ?, ?, ?)",
		1, 1, 1, "a", 2, 1, 1, "b", 3, 2, 2, "c")
}

// TestSoftDeleteOnRealDatabases: on each database, a delete sets deleted_at
// and hides the row from reads, relations and updates; Skip hard-deletes,
// and keeps the tenant; on PostgreSQL and SQLite a deleted row frees its
// unique title.
func TestSoftDeleteOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		softTables(t, tg)
		D, O := fixture.SDocs, fixture.SOwners
		in1 := testmodel.Org.With(context.Background(), int64(1))
		if n, err := tg.pdb.Delete[fixture.SDoc]().Where(D.ID.EQ(2)).Exec(in1); err != nil || n != 1 {
			t.Fatalf("Delete: %d, %v", n, err)
		}
		var left int64
		if err := tg.direct.QueryRow("SELECT COUNT(*) FROM s_docs WHERE deleted_at IS NOT NULL").Scan(&left); err != nil || left != 1 {
			t.Errorf("rows with deleted_at: %d, %v; want 1", left, err)
		}
		if n, err := tg.pdb.Delete[fixture.SDoc]().Where(D.ID.EQ(2)).Exec(in1); err != nil || n != 0 {
			t.Errorf("re-delete: %d, %v; want 0", n, err)
		}
		if n, err := tg.pdb.Query[fixture.SDoc]().Count(in1); err != nil || n != 1 {
			t.Errorf("Count: %d, %v; want 1", n, err)
		}
		os, err := tg.pdb.Query[fixture.SOwner]().With(O.Docs).With(O.Pinned).Where(O.ID.EQ(1)).AllowUnbounded().All(in1)
		if err != nil || len(os) != 1 {
			t.Fatalf("owners: %v, %v", os, err)
		}
		if docs, _ := os[0].Docs.Get(); len(docs) != 1 || docs[0].ID != 1 {
			t.Errorf("With(Docs): %v", docs)
		}
		if p, ok := os[0].Pinned.Get(); ok && p != nil {
			t.Errorf("With(Pinned) loaded the deleted doc")
		}
		if n, err := tg.pdb.Query[fixture.SOwner]().Where(O.Pinned.Has()).Count(in1); err != nil || n != 0 {
			t.Errorf("Pinned.Has: %d, %v; want 0", n, err)
		}
		if js, err := tg.pdb.Query[fixture.SOwner]().Join(O.Pinned).AllowUnbounded().All(in1); err != nil || len(js) != 0 {
			t.Errorf("Join(Pinned): %v, %v; want none", js, err)
		}
		if n, err := tg.pdb.Update[fixture.SDoc]().Set(D.Title.Set("z")).Where(D.ID.In(1, 2)).Exec(in1); err != nil || n != 1 {
			t.Errorf("Update: %d, %v; want 1", n, err)
		}

		if tg.d != velox.MySQL {
			d := fixture.SDoc{ID: 9, OwnerID: 1, Title: "b"}
			if err := tg.pdb.Insert(&d).Exec(in1); err != nil {
				t.Errorf("re-insert of the deleted title: %v", err)
			}
			if err := tg.pdb.Insert(&fixture.SDoc{ID: 10, OwnerID: 1, Title: "b"}).Exec(in1); err == nil {
				t.Error("a second live title b was inserted")
			}
		}

		skip := softdelete.Skip(in1)
		if _, err := tg.pdb.Query[fixture.SDoc]().Count(softdelete.Skip(context.Background())); !errors.Is(err, scope.ErrNoScope) {
			t.Errorf("Skip without an org: %v", err)
		}
		if n, err := tg.pdb.Delete[fixture.SDoc]().Where(D.ID.In(2, 3)).Exec(skip); err != nil || n != 1 {
			t.Errorf("hard delete: %d, %v; want 1 (doc 3 is org 2's)", n, err)
		}
		var rows int64
		if err := tg.direct.QueryRow("SELECT COUNT(*) FROM s_docs WHERE id IN (2, 3)").Scan(&rows); err != nil || rows != 1 {
			t.Errorf("rows 2 and 3 left: %d, %v; want 1", rows, err)
		}
	})
}

// TestSoftDeleteUpdateOneReturnsTheRow: UpdateQuery.One that sets deleted_at
// returns the row it wrote, although softdelete's interceptor hides it from
// reads: the read-back adds no row condition.
func TestSoftDeleteUpdateOneReturnsTheRow(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		softTables(t, tg)
		D := fixture.SDocs
		in1 := testmodel.Org.With(context.Background(), int64(1))
		got, err := tg.pdb.Update[fixture.SDoc]().Where(D.ID.EQ(1)).Set(D.DeletedAt.Set(time.Now())).One(in1)
		if err != nil || got.ID != 1 || got.DeletedAt == nil {
			t.Fatalf("One: %+v, %v", got, err)
		}
		if n, err := tg.pdb.Query[fixture.SDoc]().Where(D.ID.EQ(1)).Count(in1); err != nil || n != 0 {
			t.Errorf("the row is still read: %d, %v", n, err)
		}
	})
}
