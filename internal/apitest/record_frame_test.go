package apitest

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
)

// frameDB opens the edge setter tests' SQLite tables with a hook on sPost
// that, before the insert, writes two users through m.DB() as an InsertAll
// of one statement per row (WithMaxParams(2)), the second of which
// conflicts with the first on its key, and runs sub around that write. It
// returns the DB, its pool, and the error the hook's write returned.
func frameDB(t *testing.T, sub func(ctx context.Context, db *velox.DB, write func(context.Context, *velox.DB) error) error) (*velox.DB, *sql.DB, *error) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "frame.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	for _, s := range sDDL {
		exec(t, raw, s)
	}
	exec(t, raw, "INSERT INTO s_tags (id, name) VALUES (1, 'a')")
	var hookErr error
	write := func(ctx context.Context, db *velox.DB) error {
		return db.InsertAll([]sUser{{ID: 10, Name: "x"}, {ID: 10, Name: "y"}}).Exec(ctx)
	}
	hook := func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			hookErr = sub(ctx, m.DB(), write) // swallowed
			return next.Mutate(ctx, m)
		})
	}
	return velox.Open(velox.SQLite, raw, velox.WithHooks[sPost](hook), engine.WithMaxParams(2)), raw, &hookErr
}

// TestRecordFrameFailsOnSwallowedError: a recorded write inside an edge
// write's frame that fails, its first statement stored, fails the frame
// even when a hook swallows its error: the Insert returns it, wrapped,
// nothing commits, and no sink runs. Outside a transaction velox's own
// rolls back; in the caller's the write's savepoint does, and the caller's
// transaction commits without it.
func TestRecordFrameFailsOnSwallowedError(t *testing.T) {
	plainWrite := func(ctx context.Context, db *velox.DB, write func(context.Context, *velox.DB) error) error {
		return write(ctx, db)
	}
	check := func(t *testing.T, raw *sql.DB, err, hookErr error, rec *velox.Recorder) {
		t.Helper()
		if hookErr == nil {
			t.Fatal("the hook's write did not fail")
		}
		if err == nil || !strings.Contains(err.Error(), "a recorded write within this write failed") || !errors.Is(err, hookErr) {
			t.Errorf("Insert: %v, want the hook's write's error wrapped", err)
		}
		for _, table := range []string{"s_users", "s_posts", "s_post_tags"} {
			if n := int64s(t, raw, "SELECT COUNT(*) FROM "+table); n[0] != 0 {
				t.Errorf("%s holds %d rows, want none", table, n[0])
			}
		}
		if rec.Len() != 0 {
			t.Errorf("%d Changes, want none", rec.Len())
		}
	}
	t.Run("own transaction", func(t *testing.T) {
		db, raw, hookErr := frameDB(t, plainWrite)
		var rec velox.Recorder
		err := db.Record(&rec).Insert(&sPost{AuthorID: 1, Title: "p"}).Set(sPosts.Tags.Add(1)).Exec(user)
		check(t, raw, err, *hookErr, &rec)
	})
	t.Run("caller's transaction", func(t *testing.T) {
		db, raw, hookErr := frameDB(t, plainWrite)
		var rec velox.Recorder
		var err error
		txErr := db.Record(&rec).Tx(user, func(ctx context.Context, tx *velox.DB) error {
			err = tx.Insert(&sPost{AuthorID: 1, Title: "p"}).Set(sPosts.Tags.Add(1)).Exec(ctx)
			return nil // the caller goes on, and commits
		})
		if txErr != nil {
			t.Fatal(txErr)
		}
		check(t, raw, err, *hookErr, &rec)
	})
}

// TestRecordFrameToleratesSavepointedFailure: a hook that runs its write in
// a savepoint of its own, tx.Tx, tolerates its failure: the savepoint's
// rollback undoes it, and the frame goes on and commits, with the post and
// its link reported.
func TestRecordFrameToleratesSavepointedFailure(t *testing.T) {
	inTx := func(ctx context.Context, db *velox.DB, write func(context.Context, *velox.DB) error) error {
		return db.Tx(ctx, write)
	}
	db, raw, hookErr := frameDB(t, inTx)
	var rec velox.Recorder
	p := sPost{AuthorID: 1, Title: "p"}
	if err := db.Record(&rec).Insert(&p).Set(sPosts.Tags.Add(1)).Exec(user); err != nil {
		t.Fatal(err)
	}
	if *hookErr == nil {
		t.Fatal("the hook's write did not fail")
	}
	if n := int64s(t, raw, "SELECT COUNT(*) FROM s_users"); n[0] != 0 {
		t.Errorf("s_users holds %d rows, want none: the savepoint rolled back", n[0])
	}
	want64(t, "tags", postTags(t, raw, p.ID), 1)
	if rec.Len() != 2 {
		t.Errorf("%d Changes, want the post and its link", rec.Len())
	}
}

// dAt has a time key and a column with a declared default.
type dAt struct {
	At     time.Time
	Status string
}

var dAtMeta velox.MetaOnce[dAt]

func (*dAt) VeloxMeta() *velox.Meta[dAt] {
	return dAtMeta.Load(func() velox.Descriptor[dAt] {
		return velox.Descriptor[dAt]{
			Table: "dats",
			Columns: []velox.ColumnDescriptor{
				{Field: "At", Name: "at"},
				{Field: "Status", Name: "status", DBDefault: allDialects},
			},
			PrimaryKey: []int{0},
			FieldPtr: func(x *dAt, i int) any {
				if i == 0 {
					return &x.At
				}
				return &x.Status
			},
		}
	})
}

// TestRecordMySQLInsertTimeKey: a recorded insert on MySQL finds its stored
// row by a time key held in another zone, by its instant, so the Change's
// new row and the entity's declared default are the stored ones.
func TestRecordMySQLInsertTimeKey(t *testing.T) {
	zone := time.FixedZone("+08", 8*3600)
	at := time.Date(2026, 3, 1, 9, 0, 0, 0, zone)
	e := &fakeconn.Exec{Affected: 1, Rows: [][]any{{at.UTC(), "new"}}}
	var rec velox.Recorder
	x := dAt{At: at}
	if err := openFake(velox.MySQL, e).Record(&rec).Insert(&x).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{"BEGIN", "INSERT INTO `dats` (`at`) VALUES (?)", "SELECT `at`, `status` FROM `dats` WHERE `at` IN (?)", "COMMIT"})
	cs := slices.Collect(rec.All())
	if x.Status != "new" || len(cs) != 1 {
		t.Fatalf("entity %+v, %d Changes", x, len(cs))
	}
	if _, cur, _ := velox.RowsOf[dAt](cs[0]); cur == nil || cur.Status != "new" {
		t.Errorf("the Change's new row %+v", cur)
	}
}

// TestRecordHookWriteJoinsEdgeFrame: an insert with edge setters runs its
// hooks inside its frame, so a write a hook makes through m.DB() joins the
// frame's one sink call, before the entity's own Changes; without edge
// setters it is a write of its own (TestRecordHookWriteOwnSinkCall).
func TestRecordHookWriteJoinsEdgeFrame(t *testing.T) {
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "join.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	for _, s := range sDDL {
		exec(t, raw, s)
	}
	exec(t, raw, "INSERT INTO s_tags (id, name) VALUES (1, 'a')")
	hook := func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			if err := m.DB().Insert(&sUser{ID: 10, Name: "x"}).Exec(ctx); err != nil {
				return err
			}
			return next.Mutate(ctx, m)
		})
	}
	var calls [][]string
	sink := velox.RecordFunc(func(_ context.Context, _ *velox.DB, cs []velox.Change) error {
		var names []string
		for _, c := range cs {
			names = append(names, c.Entity)
		}
		calls = append(calls, names)
		return nil
	})
	db := velox.Open(velox.SQLite, raw, velox.WithHooks[sPost](hook)).Record(sink)
	if err := db.Insert(&sPost{AuthorID: 1, Title: "p"}).Set(sPosts.Tags.Add(1)).Exec(user); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || len(calls[0]) != 3 || calls[0][0] != "sUser" || calls[0][1] != "sPost" {
		t.Errorf("sink calls %v, want one: the hook's sUser, then the sPost and its link", calls)
	}
}
