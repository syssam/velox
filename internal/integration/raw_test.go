package integration

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/veloxtest"
)

// TestRaw: Raw and Exec on every driver. Raw scans by column name, whatever
// the order, into the entity's fields, and leaves the fields of the columns
// it does not return zero; a column the entity has no field for is an error.
// Neither is org-scoped, so a context with no org runs them.
func TestRaw(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		q := func(s string) string { return rebind(tg.d, s) }
		bare := context.Background()

		// An aggregate, scanned into the entity it sums.
		totals, err := tg.db.Raw[testmodel.Invoice]("revenue per organization",
			q("SELECT SUM(total) AS total, org_id FROM invoices WHERE id > ? GROUP BY org_id ORDER BY org_id"), int64(0)).All(bare)
		if err != nil {
			t.Fatal(err)
		}
		if want := []*testmodel.Invoice{{OrgID: 1, Total: 100}, {OrgID: 2, Total: 50}}; !veloxtest.Equal(totals, want) {
			t.Errorf("totals %+v, want %+v", totals, want)
		}

		// Every column scans as the builder's query scans it.
		raw, err := tg.db.Raw[testmodel.Invoice]("compare", q("SELECT * FROM invoices WHERE id = ?"), int64(2)).One(bare)
		if err != nil {
			t.Fatal(err)
		}
		built, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(2)).One(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(raw, built) {
			t.Errorf("Raw read %+v, the builder %+v", raw, built)
		}

		var numbers []string
		for x, err := range tg.db.Raw[testmodel.Invoice]("list", "SELECT number FROM invoices ORDER BY id").Iter(bare) {
			if err != nil {
				t.Fatal(err)
			}
			numbers = append(numbers, x.Number)
		}
		wantStrings(t, numbers, []string{"INV-1", "INV-2", "50%_OFF", "500X", `back\slash`})

		_, err = tg.db.Raw[testmodel.Invoice]("r", "SELECT id, number AS label FROM invoices").All(bare)
		if err == nil || !strings.Contains(err.Error(), `"label"`) {
			t.Errorf("a column the entity has no field for: %v", err)
		}

		n, err := tg.db.Exec(bare, "rewrite notes", q("UPDATE invoices SET note = ? WHERE org_id = ?"), "x", int64(1))
		if err != nil || n != 4 {
			t.Errorf("Exec: %d rows, %v; want 4", n, err)
		}
		if got := query(t, tg, I.Note.EQ("x")); !slices.Equal(got, []int64{1, 2, 3, 4}) {
			t.Errorf("notes rewritten on %v", got)
		}

		// In a transaction both run on it; a row lock where the database has
		// one.
		lock := "SELECT id, total FROM invoices WHERE id = ?"
		if tg.d != velox.SQLite {
			lock += " FOR UPDATE"
		}
		err = tg.db.Tx(bare, func(ctx context.Context, tx *velox.DB) error {
			inv, err := tx.Raw[testmodel.Invoice]("lock the invoice", q(lock), int64(5)).One(ctx)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, "pay the invoice", q("UPDATE invoices SET total = ? WHERE id = ?"), inv.Total+1, inv.ID)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := query(t, tg, I.Total.EQ(51)); !slices.Equal(got, []int64{5}) {
			t.Errorf("after the transaction, total 51 on %v", got)
		}
	})
}

// TestRawDecodes: Raw decodes an encoded column through its codec, on every
// driver and in every column type a JSON field is stored in.
func TestRawDecodes(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		x := jsonRec(1)
		if err := tg.db.Insert(&x).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := tg.db.Raw[jrec]("read back", "SELECT nums, shape, attrs, tags, id FROM json_recs").One(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		wantRec(t, *got, jsonRec(1))
	})
}
