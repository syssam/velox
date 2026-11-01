package apitest

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestBidiUpsertRefusesToUpdateTheKey: an upsert's update branch would write
// one row's key, and leave the old partner pointing at it.
func TestBidiUpsertRefusesToUpdateTheKey(t *testing.T) {
	S := bspouses
	const want = "velox: apitest.bspouse: an upsert cannot update the key of the bidirectional edge Spouse"
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	for name, err := range map[string]error{
		"Insert":    db.Insert(&bspouse{ID: 1}).OnConflict(S.ID).Update(S.SpouseID).Exec(ctx),
		"InsertAll": db.InsertAll([]bspouse{{ID: 1}, {ID: 2}}).OnConflict(S.ID).Update(S.SpouseID).Exec(ctx),
	} {
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(e.Log) != 0 {
		t.Errorf("sent %q", e.Log)
	}

	t.Run("a Rewrite hook may not add it", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 1}
		db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			if st.Conflict != nil {
				st.Conflict.Update = []int{1}
			}
			return nil
		}))
		err := db.Insert(&bspouse{ID: 1}).OnConflict(S.ID).Update(S.ID).Exec(ctx)
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%v", err)
		}
	})

	t.Run("on SQLite the pair stays whole", func(t *testing.T) {
		raw, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = raw.Close() }()
		raw.SetMaxOpenConns(1)
		if _, err := raw.Exec(`CREATE TABLE bspouses (id integer PRIMARY KEY, spouse_id integer UNIQUE);
			INSERT INTO bspouses VALUES (1, 2), (2, 1)`); err != nil {
			t.Fatal(err)
		}
		db := velox.Open(velox.SQLite, raw)
		if err := db.Insert(&bspouse{ID: 1}).OnConflict(S.ID).Update(S.SpouseID).Exec(ctx); err == nil {
			t.Fatal("the upsert ran")
		}
		var a, b sql.NullInt64
		if err := raw.QueryRow("SELECT (SELECT spouse_id FROM bspouses WHERE id = 1), (SELECT spouse_id FROM bspouses WHERE id = 2)").Scan(&a, &b); err != nil {
			t.Fatal(err)
		}
		if a.Int64 != 2 || b.Int64 != 1 {
			t.Errorf("rows point at %v and %v", a, b)
		}
	})
}
