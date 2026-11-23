package apitest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/schema/mixin/softdelete"
)

// my is q quoted for MySQL.
func my(q string) string { return strings.ReplaceAll(q, `"`, "`") }

// TestRecordSoftDelete: a recorded delete of an entity with softdelete's
// mixin sends softdelete's update as a recorded update sends one, and
// reports each row as a delete, Soft, with the row before and after it.
// Changed and Diff, which report updates, give nothing. Under Skip the
// delete is hard: not Soft, no new row.
func TestRecordSoftDelete(t *testing.T) {
	const cols = `"id", "org_id", "owner_id", "title", "deleted_at"`
	pg := []string{
		"BEGIN",
		`SELECT ` + cols + ` FROM "s_docs" WHERE "id" = $1 AND "deleted_at" IS NULL ORDER BY "id" FOR UPDATE`,
		`UPDATE "s_docs" SET "deleted_at" = $1 WHERE "id" = $2 AND "id" = ANY($3) AND "deleted_at" IS NULL RETURNING ` + cols,
		"COMMIT",
	}
	for _, c := range []struct {
		d    velox.Dialect
		want []string
	}{{velox.Postgres, pg}, {velox.SQLite, []string{
		"BEGIN",
		`SELECT ` + cols + ` FROM "s_docs" WHERE "id" = ? AND "deleted_at" IS NULL ORDER BY "id"`,
		`UPDATE "s_docs" SET "deleted_at" = ? WHERE "id" = ? AND "id" IN (?) AND "deleted_at" IS NULL RETURNING ` + cols,
		"COMMIT",
	}}, {velox.MySQL, []string{
		"BEGIN",
		my(`SELECT ` + cols + ` FROM "s_docs" WHERE "id" = ? AND "deleted_at" IS NULL ORDER BY "id" FOR UPDATE`),
		my(`UPDATE "s_docs" SET "deleted_at" = ? WHERE "id" = ? AND "id" IN (?) AND "deleted_at" IS NULL`),
		my(`SELECT ` + cols + ` FROM "s_docs" WHERE "id" IN (?)`),
		"COMMIT",
	}}} {
		t.Run(c.d.String(), func(t *testing.T) {
			D := fixture.SDocs
			e := &fakeconn.Exec{Affected: 1, Results: [][][]any{
				{{int64(2), int64(1), int64(1), "b", nil}},
				{{int64(2), int64(1), int64(1), "b", t0}},
			}}
			var rec velox.Recorder
			n, err := openFake(c.d, e).Record(&rec).Delete[fixture.SDoc]().Where(D.ID.EQ(2)).Exec(ctx)
			if err != nil || n != 1 {
				t.Fatalf("Delete: %d, %v", n, err)
			}
			wantLog(t, e, c.want)
			cs := slices.Collect(rec.All())
			if len(cs) != 1 || cs[0].Op != velox.OpDelete || !cs[0].Soft || cs[0].Key != int64(2) {
				t.Fatalf("Changes %+v, want one soft delete of 2", cs)
			}
			ch := cs[0]
			old, cur, ok := velox.RowsOf[fixture.SDoc](ch)
			if !ok || old.DeletedAt != nil || cur == nil || cur.DeletedAt == nil || !cur.DeletedAt.Equal(t0) {
				t.Errorf("RowsOf: %+v %+v %v", old, cur, ok)
			}
			if ch.Changed(D.DeletedAt) || ch.Diff() != nil {
				t.Errorf("Changed %v, Diff %+v: want false and nil", ch.Changed(D.DeletedAt), ch.Diff())
			}
			if !D.DeletedAt.Old(ch).IsZero() || !D.DeletedAt.New(ch).Equal(t0) || D.Title.New(ch) != "b" {
				t.Errorf("handles: %v -> %v, %q", D.DeletedAt.Old(ch), D.DeletedAt.New(ch), D.Title.New(ch))
			}
		})
	}
	t.Run("Skip", func(t *testing.T) {
		D := fixture.SDocs
		e := &fakeconn.Exec{Affected: 1, Results: [][][]any{{{int64(2), int64(1), int64(1), "b", nil}}, {{int64(2)}}}}
		var rec velox.Recorder
		if _, err := openFake(velox.Postgres, e).Record(&rec).Delete[fixture.SDoc]().Where(D.ID.EQ(2)).Exec(softdelete.Skip(ctx)); err != nil {
			t.Fatal(err)
		}
		wantLog(t, e, []string{
			"BEGIN",
			`SELECT ` + cols + ` FROM "s_docs" WHERE "id" = $1 ORDER BY "id" FOR UPDATE`,
			`DELETE FROM "s_docs" WHERE "id" = $1 AND "id" = ANY($2) RETURNING "id"`,
			"COMMIT",
		})
		cs := slices.Collect(rec.All())
		if len(cs) != 1 || cs[0].Op != velox.OpDelete || cs[0].Soft || cs[0].NewRow() != nil || cs[0].OldRow() == nil {
			t.Errorf("Changes %+v, want one hard delete", cs)
		}
	})
	t.Run("not recording", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 1}
		if _, err := openFake(velox.Postgres, e).Delete[fixture.SDoc]().Where(fixture.SDocs.ID.EQ(2)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantLog(t, e, []string{`UPDATE "s_docs" SET "deleted_at" = $1 WHERE "id" = $2 AND "deleted_at" IS NULL`})
	})
}

// spouse is a bspouse row as the fake returns it: spouse 0 is NULL.
func spouse(id, partner int64) []any {
	if partner == 0 {
		return []any{id, nil}
	}
	return []any{id, partner}
}

// spouses is rec's Changes, each as "op key old->new" of spouse_id.
func spouses(rec *velox.Recorder) []string {
	var out []string
	for c := range rec.All() {
		out = append(out, fmt.Sprintf("%v %v %d->%d", c.Op, c.Key, bspouses.SpouseID.Old(c), bspouses.SpouseID.New(c)))
	}
	return out
}

// TestRecordBidiRepoint: a recorded write of the key of a bidirectional
// one-to-one edge sends each of its statements as a recorded update sends
// one, a locked read of its rows first, and reports every row whose key
// changed once: here 5 re-points from 1 to 2, which clears 1 and links 2.
// A recorded insert that sets the key reports its row, then the partner it
// links.
func TestRecordBidiRepoint(t *testing.T) {
	const (
		cols = `"id", "spouse_id"`
		lock = " FOR UPDATE"
	)
	pgSQL := []string{
		"BEGIN",
		`SELECT ` + cols + ` FROM "bspouses" WHERE "id" = $1 ORDER BY "id"` + lock,
		`UPDATE "bspouses" SET "spouse_id" = $1 WHERE "id" = $2 AND "id" = ANY($3) RETURNING ` + cols,
		`SELECT ` + cols + ` FROM "bspouses" WHERE "spouse_id" = $1 ORDER BY "id"` + lock,
		`UPDATE "bspouses" SET "spouse_id" = $1 WHERE "spouse_id" = $2 AND "id" = ANY($3) RETURNING ` + cols,
		`SELECT ` + cols + ` FROM "bspouses" WHERE "id" = $1 AND "spouse_id" IS NULL ORDER BY "id"` + lock,
		`UPDATE "bspouses" SET "spouse_id" = $1 WHERE "id" = $2 AND "spouse_id" IS NULL AND "id" = ANY($3) RETURNING ` + cols,
		"COMMIT",
	}
	sqlite := make([]string, len(pgSQL))
	for i, q := range pgSQL {
		q = strings.ReplaceAll(q, lock, "")
		for n := 3; n >= 1; n-- {
			q = strings.ReplaceAll(q, fmt.Sprintf("$%d", n), "?")
		}
		// SQLite writes an In list where PostgreSQL writes an array.
		sqlite[i] = strings.ReplaceAll(q, " = ANY(?)", " IN (?)")
	}
	mysql := []string{
		"BEGIN",
		my(`SELECT ` + cols + ` FROM "bspouses" WHERE "id" = ? ORDER BY "id"` + lock),
		my(`UPDATE "bspouses" SET "spouse_id" = ? WHERE "id" = ? AND "id" IN (?)`),
		my(`SELECT ` + cols + ` FROM "bspouses" WHERE "id" IN (?)`),
		my(`SELECT ` + cols + ` FROM "bspouses" WHERE "spouse_id" = ? ORDER BY "id"` + lock),
		my(`UPDATE "bspouses" SET "spouse_id" = ? WHERE "spouse_id" = ? AND "id" IN (?)`),
		my(`SELECT ` + cols + ` FROM "bspouses" WHERE "id" IN (?)`),
		my(`SELECT ` + cols + ` FROM "bspouses" WHERE "id" = ? AND "spouse_id" IS NULL ORDER BY "id"` + lock),
		my(`UPDATE "bspouses" SET "spouse_id" = ? WHERE "id" = ? AND "spouse_id" IS NULL AND "id" IN (?)`),
		my(`SELECT ` + cols + ` FROM "bspouses" WHERE "id" IN (?)`),
		"COMMIT",
	}
	want := []string{"update 5 1->2", "update 1 5->0", "update 2 0->5"}
	for _, c := range []struct {
		d    velox.Dialect
		want []string
	}{{velox.Postgres, pgSQL}, {velox.SQLite, sqlite}, {velox.MySQL, mysql}} {
		t.Run(c.d.String(), func(t *testing.T) {
			// Each step's read of the row before, then its row after.
			e := &fakeconn.Exec{Affected: 1, Results: [][][]any{
				{spouse(5, 1)}, {spouse(5, 2)},
				{spouse(1, 5)}, {spouse(1, 0)},
				{spouse(2, 0)}, {spouse(2, 5)},
			}}
			var rec velox.Recorder
			if err := openFake(c.d, e).Record(&rec).Save(&bspouse{ID: 5, SpouseID: new(int64(2))}, bspouses.SpouseID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			wantLog(t, e, c.want)
			if got := spouses(&rec); !slices.Equal(got, want) {
				t.Errorf("Changes %q, want %q", got, want)
			}
		})
	}
	t.Run("same row twice", func(t *testing.T) {
		// 5 links 2 again: the clearing of 2 and its link are one Change,
		// from 5 to 5, which is no change; 5 keeps 2. Nothing is reported.
		e := &fakeconn.Exec{Affected: 1, Results: [][][]any{
			{spouse(5, 2)}, {spouse(5, 2)},
			{spouse(2, 5)}, {spouse(2, 0)},
			{spouse(2, 0)}, {spouse(2, 5)},
		}}
		var rec velox.Recorder
		if err := openFake(velox.Postgres, e).Record(&rec).Save(&bspouse{ID: 5, SpouseID: new(int64(2))}, bspouses.SpouseID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantLog(t, e, pgSQL)
		if rec.Len() != 0 {
			t.Errorf("Changes %q, want none", spouses(&rec))
		}
	})
	t.Run("insert", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 1, Results: [][][]any{{spouse(6, 1)}, {spouse(1, 0)}, {spouse(1, 6)}}}
		var rec velox.Recorder
		if err := openFake(velox.Postgres, e).Record(&rec).Insert(&bspouse{ID: 6, SpouseID: new(int64(1))}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantLog(t, e, []string{
			"BEGIN",
			`INSERT INTO "bspouses" ("id", "spouse_id") VALUES ($1, $2) RETURNING ` + cols,
			`SELECT ` + cols + ` FROM "bspouses" WHERE "id" = $1 AND "spouse_id" IS NULL ORDER BY "id"` + lock,
			`UPDATE "bspouses" SET "spouse_id" = $1 WHERE "id" = $2 AND "spouse_id" IS NULL AND "id" = ANY($3) RETURNING ` + cols,
			"COMMIT",
		})
		if got, want := spouses(&rec), []string{"insert 6 0->1", "update 1 0->6"}; !slices.Equal(got, want) {
			t.Errorf("Changes %q, want %q", got, want)
		}
	})
	t.Run("not recording", func(t *testing.T) {
		e := &fakeconn.Exec{Affected: 1}
		if err := openFake(velox.Postgres, e).Save(&bspouse{ID: 5, SpouseID: new(int64(2))}, bspouses.SpouseID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		wantLog(t, e, []string{
			"BEGIN",
			`UPDATE "bspouses" SET "spouse_id" = $1 WHERE "id" = $2`,
			`UPDATE "bspouses" SET "spouse_id" = $1 WHERE "spouse_id" = $2`,
			`UPDATE "bspouses" SET "spouse_id" = $1 WHERE "id" = $2 AND "spouse_id" IS NULL`,
			"COMMIT",
		})
	})
}

// friends is rec's Changes of bnodeFriend rows, each as "op node-friend".
func friends(t *testing.T, rec *velox.Recorder) []string {
	t.Helper()
	var out []string
	for c := range rec.All() {
		row, _ := c.NewRow().(*bnodeFriend)
		if c.Op == velox.OpDelete {
			row, _ = c.OldRow().(*bnodeFriend)
		}
		if row == nil || c.Entity != "bnodeFriend" {
			t.Errorf("a Change %+v", c)
			continue
		}
		out = append(out, fmt.Sprintf("%v %d-%d", c.Op, row.NodeID, row.FriendID))
	}
	return out
}

// TestRecordM2M: the edge setters of a many-to-many edge write the join
// entity through the recorded insert and delete: Add's DoNothing upsert
// reports each join row it inserted, and Remove's delete each it deleted.
// On a bidirectional edge both rows of a link are written, and reported;
// on MySQL the reads by the conflict columns name the mirror rows too, so
// that the count of rows affected accounts for them.
func TestRecordM2M(t *testing.T) {
	friendsOf := velox.Rel[bnode, bnode]{I: 0}
	const fcols = `"node_id", "friend_id"`
	for _, c := range []struct {
		d           velox.Dialect
		add, remove []string
	}{{velox.Postgres, []string{
		"BEGIN",
		`SELECT "id" FROM "bnodes" WHERE "id" = $1 FOR UPDATE`,
		`SELECT "id" FROM "bnodes" WHERE "id" = ANY($1) LIMIT 1`,
		`SELECT ` + fcols + ` FROM "bnode_friends" WHERE ("node_id", "friend_id") IN (($1, $2), ($3, $4)) FOR UPDATE`,
		`INSERT INTO "bnode_friends" (` + fcols + `) VALUES ($1, $2), ($3, $4) ON CONFLICT ("node_id", "friend_id") DO NOTHING RETURNING ` + fcols,
		"COMMIT",
	}, []string{
		"BEGIN",
		`SELECT "id" FROM "bnodes" WHERE "id" = $1 FOR UPDATE`,
		`SELECT "id" FROM "bnodes" WHERE "id" = ANY($1) LIMIT 1`,
		`SELECT ` + fcols + ` FROM "bnode_friends" WHERE (("node_id" = $1 AND "friend_id" = ANY($2)) OR ("friend_id" = $3 AND "node_id" = ANY($4))) ORDER BY "node_id", "friend_id" FOR UPDATE`,
		`DELETE FROM "bnode_friends" WHERE (("node_id" = $1 AND "friend_id" = ANY($2)) OR ("friend_id" = $3 AND "node_id" = ANY($4))) AND (("node_id" = $5 AND "friend_id" = $6) OR ("node_id" = $7 AND "friend_id" = $8)) RETURNING ` + fcols,
		"COMMIT",
	}}, {velox.SQLite, []string{
		"BEGIN",
		`SELECT "id" FROM "bnodes" WHERE "id" = ?`,
		`SELECT "id" FROM "bnodes" WHERE "id" IN (?) LIMIT 1`,
		`SELECT ` + fcols + ` FROM "bnode_friends" WHERE (("node_id" = ? AND "friend_id" = ?) OR ("node_id" = ? AND "friend_id" = ?))`,
		`INSERT INTO "bnode_friends" (` + fcols + `) VALUES (?, ?), (?, ?) ON CONFLICT ("node_id", "friend_id") DO NOTHING RETURNING ` + fcols,
		"COMMIT",
	}, []string{
		"BEGIN",
		`SELECT "id" FROM "bnodes" WHERE "id" = ?`,
		`SELECT "id" FROM "bnodes" WHERE "id" IN (?) LIMIT 1`,
		`SELECT ` + fcols + ` FROM "bnode_friends" WHERE (("node_id" = ? AND "friend_id" IN (?)) OR ("friend_id" = ? AND "node_id" IN (?))) ORDER BY "node_id", "friend_id"`,
		`DELETE FROM "bnode_friends" WHERE (("node_id" = ? AND "friend_id" IN (?)) OR ("friend_id" = ? AND "node_id" IN (?))) AND (("node_id" = ? AND "friend_id" = ?) OR ("node_id" = ? AND "friend_id" = ?)) RETURNING ` + fcols,
		"COMMIT",
	}}, {velox.MySQL, []string{
		"BEGIN",
		my(`SELECT "id" FROM "bnodes" WHERE "id" = ? FOR UPDATE`),
		my(`SELECT "id" FROM "bnodes" WHERE "id" IN (?) LIMIT 1`),
		my(`SELECT ` + fcols + ` FROM "bnode_friends" WHERE (("node_id" = ? AND "friend_id" = ?) OR ("node_id" = ? AND "friend_id" = ?)) FOR UPDATE`),
		my(`INSERT INTO "bnode_friends" (` + fcols + `) VALUES (?, ?), (?, ?) AS new ON DUPLICATE KEY UPDATE "node_id" = "bnode_friends"."node_id"`),
		my(`SELECT ` + fcols + ` FROM "bnode_friends" WHERE (("node_id" = ? AND "friend_id" = ?) OR ("node_id" = ? AND "friend_id" = ?))`),
		"COMMIT",
	}, []string{
		"BEGIN",
		my(`SELECT "id" FROM "bnodes" WHERE "id" = ? FOR UPDATE`),
		my(`SELECT "id" FROM "bnodes" WHERE "id" IN (?) LIMIT 1`),
		my(`SELECT ` + fcols + ` FROM "bnode_friends" WHERE (("node_id" = ? AND "friend_id" IN (?)) OR ("friend_id" = ? AND "node_id" IN (?))) ORDER BY "node_id", "friend_id" FOR UPDATE`),
		my(`DELETE FROM "bnode_friends" WHERE (("node_id" = ? AND "friend_id" IN (?)) OR ("friend_id" = ? AND "node_id" IN (?))) AND (("node_id" = ? AND "friend_id" = ?) OR ("node_id" = ? AND "friend_id" = ?))`),
		"COMMIT",
	}}} {
		t.Run(c.d.String(), func(t *testing.T) {
			links := [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}}
			results := [][][]any{{{int64(1)}}, {{int64(2)}}, {}, links}
			e := &fakeconn.Exec{Affected: 2, Results: results}
			var rec velox.Recorder
			rdb := openFake(c.d, e).Record(&rec)
			if _, err := rdb.Update[bnode]().Set(friendsOf.Add(2)).Where(velox.NumberCol[bnode, int64]{I: 0}.EQ(1)).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			wantLog(t, e, c.add)
			if got, want := friends(t, &rec), []string{"insert 1-2", "insert 2-1"}; !slices.Equal(got, want) {
				t.Errorf("Add: %q, want %q", got, want)
			}
			rec.Reset()
			e = &fakeconn.Exec{Affected: 2, Results: [][][]any{{{int64(1)}}, {{int64(2)}}, links, links}} // the locked read, then the keys the DELETE returns (none on MySQL)
			rdb = openFake(c.d, e).Record(&rec)
			if _, err := rdb.Update[bnode]().Set(friendsOf.Remove(2)).Where(velox.NumberCol[bnode, int64]{I: 0}.EQ(1)).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			wantLog(t, e, c.remove)
			if got, want := friends(t, &rec), []string{"delete 1-2", "delete 2-1"}; !slices.Equal(got, want) {
				t.Errorf("Remove: %q, want %q", got, want)
			}
		})
	}
	t.Run("kept and missing", func(t *testing.T) {
		// The link exists: DoNothing keeps both rows, and RETURNING gives
		// none. Remove of a key not linked deletes nothing.
		links := [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}}
		e := &fakeconn.Exec{Results: [][][]any{{{int64(1)}}, {{int64(2)}}, links, {}}}
		var rec velox.Recorder
		rdb := openFake(velox.Postgres, e).Record(&rec)
		if _, err := rdb.Update[bnode]().Set(friendsOf.Add(2)).Where(velox.NumberCol[bnode, int64]{I: 0}.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		e2 := &fakeconn.Exec{Results: [][][]any{{{int64(1)}}, {{int64(3)}}, {}}}
		if _, err := openFake(velox.Postgres, e2).Record(&rec).Update[bnode]().Set(friendsOf.Remove(3)).Where(velox.NumberCol[bnode, int64]{I: 0}.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if rec.Len() != 0 {
			t.Errorf("Changes %q, want none", friends(t, &rec))
		}
	})
}

// TestRecordEdgeWriteOneSavepoint: in the caller's transaction a recorded
// edge write is one frame, one savepoint: the recorded insert of its join
// rows runs inside it, with no savepoint of its own, and the sink runs
// before its RELEASE.
func TestRecordEdgeWriteOneSavepoint(t *testing.T) {
	friendsOf := velox.Rel[bnode, bnode]{I: 0}
	const fcols = `"node_id", "friend_id"`
	links := [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}}
	e := &fakeconn.Exec{Affected: 2, Results: [][][]any{{{int64(1)}}, {{int64(2)}}, {}, links}}
	var at []int
	sink := velox.RecordFunc(func(context.Context, *velox.DB, []velox.Change) error {
		at = append(at, len(e.Log))
		return nil
	})
	err := openFake(velox.Postgres, e).Record(sink).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Update[bnode]().Set(friendsOf.Add(2)).Where(velox.NumberCol[bnode, int64]{I: 0}.EQ(1)).Exec(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLog(t, e, []string{
		"BEGIN",
		"SAVEPOINT velox_1",
		`SELECT "id" FROM "bnodes" WHERE "id" = $1 FOR UPDATE`,
		`SELECT "id" FROM "bnodes" WHERE "id" = ANY($1) LIMIT 1`,
		`SELECT ` + fcols + ` FROM "bnode_friends" WHERE ("node_id", "friend_id") IN (($1, $2), ($3, $4)) FOR UPDATE`,
		`INSERT INTO "bnode_friends" (` + fcols + `) VALUES ($1, $2), ($3, $4) ON CONFLICT ("node_id", "friend_id") DO NOTHING RETURNING ` + fcols,
		"RELEASE SAVEPOINT velox_1",
		"COMMIT",
	})
	if !slices.Equal(at, []int{6}) {
		t.Errorf("the sink ran after %v statements, want once before the RELEASE (6)", at)
	}
}

// TestRecordEdgeWriteVersionInTx: in the caller's transaction, with the
// server version not known yet, a recorded edge write learns it in a
// savepoint of its own before its frame opens (primeVersion), and its
// writes, inside the one savepoint of the frame, ask nothing: on
// PostgreSQL 18 the join rows' upsert returns old and new. When the read
// fails, the frame's writes take the locked read, still in one savepoint.
func TestRecordEdgeWriteVersionInTx(t *testing.T) {
	friendsOf := velox.Rel[bnode, bnode]{I: 0}
	const fcols = `"node_id", "friend_id"`
	add := func(e *fakeconn.Exec) {
		t.Helper()
		var rec velox.Recorder
		err := openFake18(e, 180000).Record(&rec).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			_, err := tx.Update[bnode]().Set(friendsOf.Add(2)).Where(velox.NumberCol[bnode, int64]{I: 0}.EQ(1)).Exec(ctx)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := friends(t, &rec), []string{"insert 1-2", "insert 2-1"}; !slices.Equal(got, want) {
			t.Errorf("Changes %q, want %q", got, want)
		}
	}
	null := []any{nil, nil}
	e := &fakeconn.Exec{Affected: 2, Results: [][][]any{{{int64(1)}}, {{int64(2)}},
		{pair(null, []any{int64(1), int64(2)}), pair(null, []any{int64(2), int64(1)})}}}
	add(e)
	wantLog(t, e, []string{
		"BEGIN",
		"SAVEPOINT velox_1",
		showVersion,
		"RELEASE SAVEPOINT velox_1",
		"SAVEPOINT velox_2",
		`SELECT "id" FROM "bnodes" WHERE "id" = $1 FOR UPDATE`,
		`SELECT "id" FROM "bnodes" WHERE "id" = ANY($1) LIMIT 1`,
		`INSERT INTO "bnode_friends" (` + fcols + `) VALUES ($1, $2), ($3, $4) ON CONFLICT ("node_id", "friend_id") DO NOTHING RETURNING ` + oldNew(fcols),
		"RELEASE SAVEPOINT velox_2",
		"COMMIT",
	})

	links := [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}}
	e = &fakeconn.Exec{AbortOnError: true, Affected: 2,
		Errs:    []error{nil, errors.New("boom")}, // SAVEPOINT, SHOW
		Results: [][][]any{{{int64(1)}}, {{int64(2)}}, {}, links}}
	add(e)
	wantLog(t, e, []string{
		"BEGIN",
		"SAVEPOINT velox_1",
		showVersion,
		"ROLLBACK TO SAVEPOINT velox_1",
		"RELEASE SAVEPOINT velox_1",
		"SAVEPOINT velox_2",
		`SELECT "id" FROM "bnodes" WHERE "id" = $1 FOR UPDATE`,
		`SELECT "id" FROM "bnodes" WHERE "id" = ANY($1) LIMIT 1`,
		`SELECT ` + fcols + ` FROM "bnode_friends" WHERE ("node_id", "friend_id") IN (($1, $2), ($3, $4)) FOR UPDATE`,
		`INSERT INTO "bnode_friends" (` + fcols + `) VALUES ($1, $2), ($3, $4) ON CONFLICT ("node_id", "friend_id") DO NOTHING RETURNING ` + fcols,
		"RELEASE SAVEPOINT velox_2",
		"COMMIT",
	})
}

// TestRecordUpdateOneEdgesOneSavepoint: in the caller's transaction a
// recorded UpdateQuery.One that writes an edge is one frame: one savepoint,
// whatever the edge write inside it does.
func TestRecordUpdateOneEdgesOneSavepoint(t *testing.T) {
	friendsOf := velox.Rel[bnode, bnode]{I: 0}
	links := [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}}
	e := &fakeconn.Exec{Affected: 2, Results: [][][]any{{{int64(1)}}, {{int64(2)}}, {}, links, {{int64(1)}}}}
	var rec velox.Recorder
	err := openFake(velox.Postgres, e).Record(&rec).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Update[bnode]().Set(friendsOf.Add(2)).Where(velox.NumberCol[bnode, int64]{I: 0}.EQ(1)).One(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range e.Log {
		if strings.HasPrefix(s, "SAVEPOINT") {
			n++
		}
	}
	if n != 1 || rec.Len() != 2 {
		t.Errorf("%d savepoints, %d Changes; want 1 and 2:\n%s", n, rec.Len(), strings.Join(e.Log, "\n"))
	}
}
