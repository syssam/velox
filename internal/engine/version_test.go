package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestVersionSQL: Save conditions on the version *x read and adds 1 to it;
// an upsert's update branch adds 1 to the existing row's, by its table.
func TestVersionSQL(t *testing.T) {
	P := testmodel.Papers
	for _, c := range []struct {
		d            velox.Dialect
		save, upsert string
	}{
		{velox.Postgres,
			`UPDATE "papers" SET "title" = $1, "version" = "version" + $2 WHERE "id" = $3 AND "version" = $4`,
			`INSERT INTO "papers" ("id", "title", "version") VALUES ($1, $2, $3) ON CONFLICT ("id") DO UPDATE SET "title" = EXCLUDED."title", "version" = "papers"."version" + 1`},
		{velox.MySQL,
			"UPDATE `papers` SET `title` = ?, `version` = `version` + ? WHERE `id` = ? AND `version` = ?",
			"INSERT INTO `papers` (`id`, `title`, `version`) VALUES (?, ?, ?) AS new ON DUPLICATE KEY UPDATE `title` = new.`title`, `version` = `papers`.`version` + 1"},
		{velox.SQLite,
			`UPDATE "papers" SET "title" = ?, "version" = CASE WHEN typeof("version" + ?) = 'real' THEN abs(-9223372036854775808) ELSE "version" + ? END WHERE "id" = ? AND "version" = ?`,
			`INSERT INTO "papers" ("id", "title", "version") VALUES (?, ?, ?) ON CONFLICT ("id") DO UPDATE SET "title" = EXCLUDED."title", "version" = "papers"."version" + 1`},
	} {
		db := openFake(c.d, &fakeconn.Exec{})
		p := testmodel.Paper{Title: "t", Version: 3}
		q, args, err := db.Save(&p, P.Title).SQL(ctx)
		if err != nil || q != c.save || args[len(args)-1] != int64(3) {
			t.Errorf("%v Save:\n got %s %v %v\nwant %s", c.d, q, args, err, c.save)
		}
		if q, _, err = db.Insert(&p).OnConflict(P.ID).Update(P.Title).SQL(ctx); err != nil || q != c.upsert {
			t.Errorf("%v upsert:\n got %s %v\nwant %s", c.d, q, err, c.upsert)
		}
	}
}

// TestVersionSetByAHook: a hook that assigns the version fails the write
// with ErrVersionSet, and nothing is sent.
func TestVersionSetByAHook(t *testing.T) {
	hook := func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			if err := m.Set("version", int64(42)); err != nil {
				return err
			}
			return next.Mutate(ctx, m)
		})
	}
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e, velox.WithHooks[testmodel.Paper](hook))
	p := testmodel.Paper{Title: "t"}
	if err := db.Save(&p, testmodel.Papers.Title).Exec(ctx); !errors.Is(err, velox.ErrVersionSet) || e.Calls != 0 {
		t.Errorf("a hook's version: %v, %d statements", err, e.Calls)
	}
}

// TestSaveStaleWithAFake: a Save that matches no row fails with ErrStale and
// ErrNotFound, and leaves *x's version; one that matches moves it on.
func TestSaveStaleWithAFake(t *testing.T) {
	e := &fakeconn.Exec{Affected: 0}
	db := openFake(velox.SQLite, e)
	p := testmodel.Paper{Title: "t", Version: 5}
	err := db.Save(&p, testmodel.Papers.Title).Exec(ctx)
	if !errors.Is(err, velox.ErrStale) || !errors.Is(err, velox.ErrNotFound) || p.Version != 5 {
		t.Fatalf("no row: %v, version %d", err, p.Version)
	}
	if want := "velox: testmodel.Paper: stale: the row's version moved since it was read (not found)"; err.Error() != want {
		t.Errorf("error %q, want %q", err, want)
	}
	e.Affected = 1
	if err := db.Save(&p, testmodel.Papers.Title).Exec(ctx); err != nil || p.Version != 6 {
		t.Errorf("one row: %v, version %d", err, p.Version)
	}
}
