package apitest

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// pairDB opens a SQLite database with wgPair's table.
func pairDB(t *testing.T) (*velox.DB, *sql.DB) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "pairs.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	exec(t, raw, "CREATE TABLE pairs (a INTEGER NOT NULL, b INTEGER NOT NULL, price INTEGER, PRIMARY KEY (a, b))")
	return velox.Open(velox.SQLite, raw), raw
}

// TestUpdateOneSetsKey: One that assigns a key column returns the row under
// its new key.
func TestUpdateOneSetsKey(t *testing.T) {
	db, raw := pairDB(t)
	exec(t, raw, "INSERT INTO pairs (a, b, price) VALUES (1, 2, 5)")
	p, err := db.Update[wgPair]().Where(wgPairs.A.EQ(1), wgPairs.B.EQ(2)).Set(wgPairs.B.Set(3)).One(ctx)
	if err != nil || p.A != 1 || p.B != 3 || p.Price == nil || *p.Price != 5 {
		t.Fatalf("One: %+v, %v", p, err)
	}
}

// TestUpdateOneSetsKeyByRead: when the conditions do not pin the key, One
// reads it first, and still returns the row under the key it assigned.
func TestUpdateOneSetsKeyByRead(t *testing.T) {
	db, raw := pairDB(t)
	exec(t, raw, "INSERT INTO pairs (a, b, price) VALUES (1, 2, 5)")
	p, err := db.Update[wgPair]().Where(wgPairs.Price.EQ(5)).Set(wgPairs.A.Set(4)).One(ctx)
	if err != nil || p.A != 4 || p.B != 2 {
		t.Fatalf("One: %+v, %v", p, err)
	}
}

// TestUpdateOneAddsToKey: One refuses an Add on a key column before it
// sends anything: it could not find the row again.
func TestUpdateOneAddsToKey(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e)
	_, err := db.Update[wgPair]().Where(wgPairs.A.EQ(1), wgPairs.B.EQ(2)).Set(wgPairs.B.Add(1)).One(ctx)
	if err == nil || !strings.Contains(err.Error(), "changes by Add or Sub") || len(e.Log) != 0 {
		t.Fatalf("One: %v; sent %q", err, e.Log)
	}
}

// writes is log without the statements that are not a SELECT or an UPDATE.
func writes(log []string) []string {
	return slices.DeleteFunc(slices.Clone(log), func(s string) bool {
		return !strings.HasPrefix(s, "SELECT") && !strings.HasPrefix(s, "UPDATE")
	})
}

// TestUpdateOneByKeyStatements: when the conditions pin the whole key, One
// sends the UPDATE as given and reads the row back: no key read, no lock.
func TestUpdateOneByKeyStatements(t *testing.T) {
	cases := []struct {
		name string
		d    velox.Dialect
		run  func(db *velox.DB) error
		row  []any
		want []string
	}{{
		"postgres", velox.Postgres,
		func(db *velox.DB) error {
			p, err := db.Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).One(user)
			if err == nil && (p.ID != 1 || p.Title != "q") {
				return fmt.Errorf("row %+v", p)
			}
			return err
		},
		[]any{int64(1), int64(7), "q"},
		[]string{
			`UPDATE "s_posts" SET "title" = $1 WHERE "id" = $2`,
			`SELECT "id", "author_id", "title" FROM "s_posts" WHERE "id" = $1 LIMIT 2`,
		},
	}, {
		"mysql", velox.MySQL,
		func(db *velox.DB) error {
			_, err := db.Update[sPost]().Where(sPosts.ID.EQ(1), sPosts.AuthorID.EQ(7)).Set(sPosts.Title.Set("q")).One(user)
			return err
		},
		[]any{int64(1), int64(7), "q"},
		[]string{
			"UPDATE `s_posts` SET `title` = ? WHERE `id` = ? AND `author_id` = ?",
			"SELECT `id`, `author_id`, `title` FROM `s_posts` WHERE `id` = ? LIMIT 2",
		},
	}, {
		"composite key", velox.Postgres,
		func(db *velox.DB) error {
			p, err := db.Update[wgPair]().Where(wgPairs.A.EQ(1), wgPairs.B.EQ(2)).Set(wgPairs.B.Set(3)).One(ctx)
			if err == nil && p.B != 3 {
				return fmt.Errorf("row %+v", p)
			}
			return err
		},
		[]any{int64(1), int64(3), nil},
		[]string{
			`UPDATE "pairs" SET "b" = $1 WHERE "a" = $2 AND "b" = $3`,
			`SELECT "a", "b", "price" FROM "pairs" WHERE "a" = $1 AND "b" = $2 LIMIT 2`,
		},
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &fakeconn.Exec{Results: [][][]any{{c.row}}, Affected: 1}
			if err := c.run(openFake(c.d, e)); err != nil {
				t.Fatal(err)
			}
			if got := writes(e.Log); !slices.Equal(got, c.want) {
				t.Errorf("statements:\n%s\nwant:\n%s\nall: %q", strings.Join(got, "\n"), strings.Join(c.want, "\n"), e.Log)
			}
			if e.Log[0] != "BEGIN" || e.Log[len(e.Log)-1] != "COMMIT" {
				t.Errorf("not in one transaction: %q", e.Log)
			}
		})
	}
}

// TestUpdateOneByKeyAffected: what One makes of the UPDATE's count when the
// conditions pin the key. None: it reads the row under the conditions and
// the policies, as a hook that skips the write leaves it, and reports
// ErrNotFound when there is none. More than one: ErrNotSingular, rolled
// back.
func TestUpdateOneByKeyAffected(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{{int64(1), int64(7), "a"}}}}
	p, err := db0(e).Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).One(user)
	if err != nil || p.Title != "a" {
		t.Fatalf("no row affected, the row there: %+v, %v", p, err)
	}
	want := []string{
		`UPDATE "s_posts" SET "title" = $1 WHERE "id" = $2`,
		`SELECT "id", "author_id", "title" FROM "s_posts" WHERE "id" = $1 LIMIT 2`,
	}
	if got := writes(e.Log); !slices.Equal(got, want) {
		t.Errorf("statements: %q", got)
	}

	e = &fakeconn.Exec{Results: [][][]any{{}}}
	if _, err := db0(e).Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).One(user); !velox.IsNotFound(err) {
		t.Errorf("no row affected, none there: %v", err)
	}

	e = &fakeconn.Exec{Affected: 2}
	_, err = db0(e).Update[sPost]().Where(sPosts.ID.EQ(1)).Set(sPosts.Title.Set("q")).One(user)
	if !errors.Is(err, velox.ErrNotSingular) || e.Log[len(e.Log)-1] != "ROLLBACK" {
		t.Errorf("two rows affected: %v; %q", err, e.Log)
	}
}

func db0(e *fakeconn.Exec) *velox.DB { return openFake(velox.Postgres, e) }
