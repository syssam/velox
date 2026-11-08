package apitest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// sumRows reads db.Rows of "SELECT org_id, COUNT(*) …" into a map.
func sumRows(t *testing.T, ctx context.Context, db *velox.DB, query string) map[int64]int64 {
	t.Helper()
	got := map[int64]int64{}
	for row, err := range db.Rows(ctx, "report", query) {
		if err != nil {
			t.Fatal(err)
		}
		var org, n int64
		if err := row.Scan(&org, &n); err != nil {
			t.Fatal(err)
		}
		got[org] = n
	}
	return got
}

// TestRowsReadsAReport: Rows yields the rows of SQL that is no entity's,
// scanned in column order, on SQLite's one connection.
func TestRowsReadsAReport(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	got := sumRows(t, ctx, db, "SELECT org_id, COUNT(*) FROM invoices GROUP BY org_id")
	if len(got) != 1 || got[1] != 3 {
		t.Errorf("got %v, want map[1:3]", got)
	}
}

// TestRowsInTx: in a transaction, Rows runs on the transaction's
// connection: it sees the transaction's own uncommitted row, and does not
// wait for the connection the transaction holds, where velox.Pool is nil.
func TestRowsInTx(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	noHang(t, func() {
		err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			if velox.Pool(tx) != nil {
				t.Error("velox.Pool on a transaction's DB is not nil")
			}
			if _, err := tx.Exec(ctx, "test", `INSERT INTO invoices (id, org_id, number, total, paid, note, data) VALUES (4, 2, 'd', 0, false, '', x'')`); err != nil {
				return err
			}
			if got := sumRows(t, ctx, tx, "SELECT org_id, COUNT(*) FROM invoices GROUP BY org_id"); got[2] != 1 {
				t.Errorf("in the transaction: %v, want org 2's uncommitted row", got)
			}
			return errors.New("roll back")
		})
		if err == nil || err.Error() != "roll back" {
			t.Errorf("Tx: %v", err)
		}
	})
	if got := sumRows(t, ctx, db, "SELECT org_id, COUNT(*) FROM invoices GROUP BY org_id"); got[2] != 0 {
		t.Errorf("after the rollback: %v", got)
	}
}

// TestRowsErrors: no reason, bad SQL and the row cap are each yielded once,
// as an error; a reason missing sends nothing.
func TestRowsErrors(t *testing.T) {
	e := &fakeconn.Exec{Cols: []string{"n"}, Rows: [][]any{{int64(1)}, {int64(2)}, {int64(3)}}}
	db := openFake(velox.Postgres, e, velox.WithMaxRows(2))
	errs := func(reason, q string) []error {
		var out []error
		for _, err := range db.Rows(ctx, reason, q) {
			if err != nil {
				out = append(out, err)
			}
		}
		return out
	}
	if got := errs(" ", "SELECT n"); len(got) != 1 || !strings.Contains(got[0].Error(), "velox: Rows: raw SQL needs a reason") || e.Calls != 0 {
		t.Errorf("no reason: %v, %d sent", got, e.Calls)
	}
	if got := errs("r", "SELECT n"); len(got) != 1 || !errors.Is(got[0], velox.ErrTooManyRows) {
		t.Errorf("over the cap: %v", got)
	}
	db = connectInvoices(t, ":memory:")
	var n int
	for _, err := range db.Rows(ctx, "r", "SELECT nope FROM invoices") {
		n++
		if err == nil || !strings.HasPrefix(err.Error(), "velox: Rows: ") {
			t.Errorf("bad SQL: %v", err)
		}
	}
	if n != 1 {
		t.Errorf("bad SQL yielded %d times, want 1", n)
	}
}

// TestRowsBreakAndHooks: a break stops the rows, yields no error after it
// and frees SQLite's one connection; the Around hook sees one OpRawQuery
// named Rows, with the reason, the rows read and the loop body's time.
func TestRowsBreakAndHooks(t *testing.T) {
	const body = 10 * time.Millisecond
	rec := &rawRecorder{}
	e := &fakeconn.Exec{Cols: []string{"n"}, Rows: [][]any{{int64(1)}, {int64(2)}, {int64(3)}}}
	db := openFake(velox.Postgres, e, velox.WithAround(rec.around))
	n := 0
	for row, err := range db.Rows(ctx, "why", "SELECT n FROM t WHERE x = $1", 7) {
		if err != nil {
			t.Fatal(err)
		}
		if err := row.Scan(new(int64)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(body)
		if n++; n == 2 {
			break
		}
	}
	if len(rec.sts) != 1 {
		t.Fatalf("%d statements seen, want 1", len(rec.sts))
	}
	st := rec.sts[0]
	if st.Op != velox.OpRawQuery || st.Entity.Name != "Rows" || st.PolicyBypass != "why" || st.SQL != "SELECT n FROM t WHERE x = $1" ||
		len(st.Args) != 1 || st.Args[0] != 7 || st.Result.LoopBody < 2*body {
		t.Errorf("the hook saw %+v", st)
	}

	sq := connectInvoices(t, ":memory:")
	noHang(t, func() {
		for _, err := range sq.Rows(ctx, "r", "SELECT id FROM invoices") {
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if got := sumRows(t, ctx, sq, "SELECT org_id, COUNT(*) FROM invoices GROUP BY org_id"); got[1] != 3 {
			t.Errorf("after a break: %v", got)
		}
	})
}
