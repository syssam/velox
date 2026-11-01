package apitest

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// sqliteInvoices is a SQLite database whose invoices' created_at has default
// def, as velox's DDL writes the column.
func sqliteInvoices(t *testing.T, def string) (*velox.DB, *sql.DB) {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	_, err = raw.Exec(`CREATE TABLE invoices (created_at datetime NOT NULL DEFAULT ` + def + `, updated_at datetime NOT NULL DEFAULT ` + def + `, id integer PRIMARY KEY,
		org_id integer NOT NULL, number text NOT NULL, total real NOT NULL, paid boolean NOT NULL,
		paid_at datetime, note text NOT NULL, data blob NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	return velox.Open(velox.SQLite, raw), raw
}

// TestSQLiteDefaultTimePagesWithAfter: a row made by the database's default of
// mixin.Time, and so with no velox parameter behind it, sorts with velox's
// time parameters, so After's pages neither repeat nor stop.
func TestSQLiteDefaultTimePagesWithAfter(t *testing.T) {
	def := mixin.CreateTime{}.Fields()[0].Descriptor().SchemaDefault[field.SQLite]
	db, raw := sqliteInvoices(t, def)
	const rows = 5
	for i := range rows {
		if _, err := raw.Exec(`INSERT INTO invoices (id, org_id, number, total, paid, note, data) VALUES (?, 1, ?, 0, false, '', x'')`, i+1, string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	var text string
	if err := raw.QueryRow("SELECT CAST(created_at AS TEXT) FROM invoices LIMIT 1").Scan(&text); err != nil {
		t.Fatal(err)
	}
	const layout = "2006-01-02 15:04:05.000000000-07:00"
	if at, err := time.Parse(layout, text); err != nil || len(text) != len(layout) || !strings.HasSuffix(text, "+00:00") {
		t.Errorf("the default wrote %q (%v, %v), want velox's format", text, at, err)
	}
	seen := map[int64]bool{}
	q := db.Query[testmodel.Invoice]().OrderBy(I.CreatedAt.Asc(), I.ID.Asc()).Limit(2)
	for range rows + 2 {
		page, err := q.All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, inv := range page {
			if seen[inv.ID] {
				t.Fatalf("row %d came back", inv.ID)
			}
			seen[inv.ID] = true
		}
		q = q.After(&page[len(page)-1])
	}
	if len(seen) != rows {
		t.Errorf("%d rows of %d", len(seen), rows)
	}
	// A later instant from velox sorts after the default's, and an earlier one
	// before it.
	for _, c := range []struct {
		at   time.Time
		want int64
	}{{time.Now().Add(time.Hour), rows}, {time.Now().Add(-time.Hour), 0}} {
		n, err := db.Query[testmodel.Invoice]().Where(I.CreatedAt.LT(c.at)).Count(ctx)
		if err != nil || n != c.want {
			t.Errorf("before %v: %d, %v; want %d", c.at, n, err, c.want)
		}
	}
}

// TestSQLiteRawScansTextTimes: SQLite gives a computed column no declared type,
// so the driver hands over the text. Every form SQLite's date functions and
// velox write scans into a time field, a nullable one too.
func TestSQLiteRawScansTextTimes(t *testing.T) {
	want := time.Date(2026, 11, 1, 12, 30, 45, 0, time.UTC)
	for _, text := range []string{
		"2026-11-01 12:30:45",
		"2026-11-01T12:30:45",
		"2026-11-01 12:30:45.000",
		"2026-11-01T12:30:45Z",
		"2026-11-01 12:30:45.000000000+00:00",
		"2026-11-01 20:30:45.000000000+08:00",
		"2026-11-01T12:30:45.000+00:00",
	} {
		db, raw := sqliteInvoices(t, "CURRENT_TIMESTAMP")
		if _, err := raw.Exec(`INSERT INTO invoices (created_at, paid_at, id, org_id, number, total, paid, note, data) VALUES (?, ?, 1, 1, 'a', 0, false, '', x'')`, text, text); err != nil {
			t.Fatal(err)
		}
		rows, err := db.Raw[testmodel.Invoice]("test", "SELECT MAX(created_at) AS created_at, MAX(paid_at) AS paid_at FROM invoices").All(ctx)
		if err != nil {
			t.Errorf("%s: %v", text, err)
			continue
		}
		if !rows[0].CreatedAt.Equal(want) {
			t.Errorf("%s: read %v", text, rows[0].CreatedAt)
		}
		if rows[0].PaidAt == nil || !rows[0].PaidAt.Equal(want) {
			t.Errorf("%s: read a nullable %v", text, rows[0].PaidAt)
		}
	}
	day := "2026-11-01"
	db, raw := sqliteInvoices(t, "CURRENT_TIMESTAMP")
	if _, err := raw.Exec(`INSERT INTO invoices (created_at, id, org_id, number, total, paid, note, data) VALUES (?, 1, 1, 'a', 0, false, '', x'')`, day); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Raw[testmodel.Invoice]("test", "SELECT MAX(created_at) AS created_at, NULL AS paid_at FROM invoices").All(ctx)
	if err != nil {
		t.Fatalf("%s: %v", day, err)
	}
	if !rows[0].CreatedAt.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || rows[0].PaidAt != nil {
		t.Errorf("%s: read %v and %v", day, rows[0].CreatedAt, rows[0].PaidAt)
	}

	// Text that is no time stays the error it was.
	db, raw = sqliteInvoices(t, "CURRENT_TIMESTAMP")
	if _, err := raw.Exec(`INSERT INTO invoices (created_at, id, org_id, number, total, paid, note, data) VALUES ('soon', 1, 1, 'a', 0, false, '', x'')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Raw[testmodel.Invoice]("test", "SELECT MAX(created_at) AS created_at FROM invoices").All(ctx); err == nil {
		t.Error("'soon' scanned into a time")
	}
}
