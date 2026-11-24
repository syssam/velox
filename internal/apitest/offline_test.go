package apitest

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
)

// TestOfflineSQL: velox.Open(d, nil) renders a statement's SQL on each
// dialect with no database: Query, UpdateQuery and DeleteQuery's SQL send
// nothing. A statement it would send, and a Tx, fail with an error rather
// than a panic, and so does SQL when a policy reads the database.
func TestOfflineSQL(t *testing.T) {
	bg := context.Background()
	O := fixture.IOwners
	byName := velox.Pred[fixture.IOwner](velox.FieldInSelect("name", "INote", "id"))
	for d, want := range map[velox.Dialect]string{
		velox.Postgres: `"vx1"."id" = CASE WHEN "owners"."name" COLLATE "C" ~ '`,
		velox.MySQL:    "`vx1`.`id` = CASE WHEN `owners`.`name` REGEXP '",
		velox.SQLite:   `"vx1"."id" = CASE WHEN CAST(CAST("owners"."name" AS INTEGER) AS TEXT) = "owners"."name"`,
	} {
		db := velox.Open(d, nil)
		q, _, err := db.Query[fixture.IOwner]().Where(byName).Limit(1).SQL(bg)
		if err != nil || !strings.HasPrefix(q, "SELECT ") || !strings.Contains(q, want) {
			t.Errorf("%s query: %s, %v", d, q, err)
		}
		u, _, err := db.Update[fixture.IOwner]().Set(O.PinnedID.Set(1)).Where(byName).SQL(bg)
		if err != nil || !strings.HasPrefix(u, "UPDATE ") || !strings.Contains(u, want) {
			t.Errorf("%s update: %s, %v", d, u, err)
		}
		del, _, err := db.Delete[fixture.IOwner]().Where(byName).SQL(bg)
		if err != nil || !strings.HasPrefix(del, "DELETE ") || !strings.Contains(del, want) {
			t.Errorf("%s delete: %s, %v", d, del, err)
		}

		const offline = "nil *sql.DB"
		if _, err := db.Query[fixture.IOwner]().Where(byName).Limit(1).All(bg); err == nil || !strings.Contains(err.Error(), offline) {
			t.Errorf("%s All: %v", d, err)
		}
		if _, err := db.Update[fixture.IOwner]().Set(O.PinnedID.Set(1)).Where(byName).Exec(bg); err == nil || !strings.Contains(err.Error(), offline) {
			t.Errorf("%s Exec: %v", d, err)
		}
		if err := db.Tx(bg, func(context.Context, *velox.DB) error { return nil }); err == nil || !strings.Contains(err.Error(), offline) {
			t.Errorf("%s Tx: %v", d, err)
		}
	}

	// A Read policy that reads the database fails SQL on such a DB.
	fixture.PNoteRead = func(ctx context.Context, db *velox.DB) (velox.Predicate, error) {
		if _, err := db.Query[fixture.IOwner]().Limit(1).All(ctx); err != nil {
			return velox.Predicate{}, err
		}
		return velox.Allow(), nil
	}
	t.Cleanup(func() { fixture.PNoteRead = nil })
	if _, _, err := velox.Open(velox.Postgres, nil).Query[fixture.PNote]().Limit(1).SQL(bg); err == nil || !strings.Contains(err.Error(), "nil *sql.DB") {
		t.Errorf("a policy that reads: %v", err)
	}
}
