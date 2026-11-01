package apitest

import (
	"context"
	"errors"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestRawSQLCannotEndTheTransaction: raw SQL that would end the transaction is refused
// unsent, so fn's later work and its error stay one transaction. Unrefused, COMMIT and
// END left the earlier row durable, and each left the later one durable.
func TestRawSQLCannotEndTheTransaction(t *testing.T) {
	insert := func(ctx context.Context, tx *velox.DB, id string) {
		t.Helper()
		if _, err := tx.Exec(ctx, "test", "INSERT INTO t (id) VALUES ("+id+")"); err != nil {
			t.Errorf("insert %s: %v", id, err)
		}
	}
	boom := errors.New("boom")
	for _, q := range []string{"COMMIT", "commit;", "END", "ROLLBACK", "BEGIN", "START TRANSACTION", "ABORT",
		" \n-- why\n/* and /* why */ COMMIT", "; ; COMMIT", "ROLLBACK TRANSACTION", "ROLLBACK WORK"} {
		t.Run(q, func(t *testing.T) {
			db := sqliteTable(t)
			err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				insert(ctx, tx, "1")
				if _, err := tx.Exec(ctx, "test", q); !errors.Is(err, velox.ErrTxControl) {
					t.Errorf("Exec: %v", err)
				}
				if _, err := tx.Raw[testmodel.Invoice]("test", q).All(ctx); !errors.Is(err, velox.ErrTxControl) {
					t.Errorf("Raw: %v", err)
				}
				insert(ctx, tx, "2")
				return boom
			})
			if !errors.Is(err, boom) {
				t.Errorf("the Tx: %v", err)
			}
			if n := tableRows(t, db); n != 0 {
				t.Errorf("%d rows are durable", n)
			}
		})
	}
	t.Run("a nested Tx after a refused ROLLBACK stays in the transaction", func(t *testing.T) {
		db := sqliteTable(t)
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			if _, err := tx.Exec(ctx, "test", "ROLLBACK"); !errors.Is(err, velox.ErrTxControl) {
				t.Errorf("%v", err)
			}
			if err := tx.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				insert(ctx, tx, "1")
				return nil
			}); err != nil {
				t.Error(err)
			}
			return boom
		})
		if !errors.Is(err, boom) || tableRows(t, db) != 0 {
			t.Errorf("%v, %d rows", err, tableRows(t, db))
		}
	})
	t.Run("ROLLBACK TO is the caller's savepoint", func(t *testing.T) {
		db := sqliteTable(t)
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			insert(ctx, tx, "1")
			for _, q := range []string{"SAVEPOINT mine", "INSERT INTO t (id) VALUES (2)", "ROLLBACK TO mine", "RELEASE mine",
				"SAVEPOINT mine2", "ROLLBACK TRANSACTION TO SAVEPOINT mine2"} {
				if _, err := tx.Exec(ctx, "test", q); err != nil {
					t.Errorf("%s: %v", q, err)
				}
			}
			return nil
		})
		if err != nil || tableRows(t, db) != 1 {
			t.Errorf("%v, %d rows", err, tableRows(t, db))
		}
	})
	t.Run("outside a transaction it is the caller's", func(t *testing.T) {
		db := sqliteTable(t)
		for _, q := range []string{"BEGIN", "INSERT INTO t (id) VALUES (1)", "COMMIT"} {
			if _, err := db.Exec(ctx, "test", q); err != nil {
				t.Errorf("%s: %v", q, err)
			}
		}
	})
}

// TestSQLiteRawTextEndingTheTransaction: SQLite runs all of a text, and velox reads its
// first statement, so what ends the transaction past it runs. velox asks, and breaks
// the transaction: the statement fails, later ones fail unsent, and fn's nil fails.
func TestSQLiteRawTextEndingTheTransaction(t *testing.T) {
	for _, q := range []string{"INSERT INTO t (id) VALUES (1); COMMIT", "SELECT 1; ROLLBACK", "SELECT 1; END"} {
		t.Run(q, func(t *testing.T) {
			db := sqliteTable(t)
			err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				if _, err := tx.Exec(ctx, "test", q); !errors.Is(err, velox.ErrTxControl) {
					t.Errorf("the text: %v", err)
				}
				if _, err := tx.Exec(ctx, "test", "INSERT INTO t (id) VALUES (2)"); !errors.Is(err, velox.ErrTxControl) {
					t.Errorf("the next statement: %v", err)
				}
				if err := tx.Tx(ctx, func(context.Context, *velox.DB) error { return nil }); !errors.Is(err, velox.ErrTxControl) {
					t.Errorf("a nested Tx: %v", err)
				}
				return nil
			})
			if !errors.Is(err, velox.ErrTxControl) {
				t.Errorf("the Tx: %v", err)
			}
			var n int
			if err := velox.Pool(db).QueryRow("SELECT COUNT(*) FROM t WHERE id = 2").Scan(&n); err != nil || n != 0 {
				t.Errorf("the later row: %d, %v", n, err)
			}
			if _, err := db.Exec(ctx, "test", "INSERT INTO t (id) VALUES (3)"); err != nil {
				t.Errorf("the connection after the Tx: %v", err)
			}
		})
	}
	t.Run("a raw statement that leaves it open costs one probe, and breaks nothing", func(t *testing.T) {
		db := sqliteTable(t)
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			if _, err := tx.Exec(ctx, "test", "INSERT INTO t (id) VALUES (1)"); err != nil {
				t.Error(err)
			}
			return nil
		})
		if err != nil || tableRows(t, db) != 1 {
			t.Errorf("%v, %d rows", err, tableRows(t, db))
		}
	})
}

// TestMySQLDDLInATransaction: MySQL commits before and after DDL, so in a transaction it
// is refused, unless of a TEMPORARY table; elsewhere, and on other dialects, it runs.
func TestMySQLDDLInATransaction(t *testing.T) {
	for _, c := range []struct {
		d       velox.Dialect
		q       string
		refused bool
	}{
		{velox.MySQL, "CREATE TABLE x (id int)", true},
		{velox.MySQL, "/*!40101 ALTER TABLE x ADD y int */", true},
		{velox.MySQL, "# why\nDROP TABLE x", true},
		{velox.MySQL, "/* why */ TRUNCATE x", true},
		{velox.MySQL, "RENAME TABLE x TO y", true},
		{velox.MySQL, "CREATE TEMPORARY TABLE x (id int)", false},
		{velox.MySQL, "DROP TEMPORARY TABLE x", false},
		{velox.MySQL, "UPDATE x SET y = 1", false},
		{velox.Postgres, "CREATE TABLE x (id int)", false},
		{velox.Postgres, "ALTER TABLE x ADD y int", false},
		{velox.Postgres, "PREPARE TRANSACTION 'x'", true},
		{velox.Postgres, "PREPARE p AS SELECT 1", false},
		{velox.Postgres, "COMMITTED", false},
	} {
		t.Run(c.d.String()+" "+c.q, func(t *testing.T) {
			e := &fakeconn.Exec{Affected: 1}
			err := openFake(c.d, e).Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
				_, err := tx.Exec(ctx, "test", c.q)
				return err
			})
			if got := errors.Is(err, velox.ErrTxControl); got != c.refused {
				t.Errorf("%v, want refused %v", err, c.refused)
			}
			sent := false
			for _, q := range e.Log {
				sent = sent || q == c.q
			}
			if sent == c.refused {
				t.Errorf("sent: %v", e.Log)
			}
		})
	}
}
