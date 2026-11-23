package apitest

import (
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/untyped"
)

// keyedInvoice is an invoice read with a computed sort key beside its
// columns: Scan fills the embedded entity's fields by column name and the
// key by its As name.
type keyedInvoice struct {
	inv
	SortKey string `velox:"sort_key_0"`
}

// TestEntityRowsWithAComputedKey: ent's AppendSelectExprAs beside full
// entity rows, ordered by the expression and paged by a keyset over it, is a
// projection of every column (ColumnByName over the entity's metadata) and
// the Calc, read with Scan into a struct that embeds the entity, with the
// keyset an Expr over the same Term (ADR 0142).
func TestEntityRowsWithAComputedKey(t *testing.T) {
	db := reportDB(t) // totals 10, 20, 30, 40 for ids 1..4
	if _, err := velox.Pool(db).Exec(`UPDATE invoices SET total = CASE id WHEN 2 THEN 30 ELSE total END`); err != nil {
		t.Fatal(err)
	}
	key := velox.Calc[inv]("COALESCE('v' || CAST(? AS TEXT), 'n')", I.Total)
	var cols []velox.Selectable[inv]
	for _, c := range untyped.TableOf[inv]().Info().Columns {
		term, _ := velox.ColumnByName[inv](c.Name)
		cols = append(cols, term)
	}
	page := func(after *keyedInvoice) []keyedInvoice {
		t.Helper()
		q := db.Query[inv]().Select(cols...).Select(key.As("sort_key_0")).
			OrderBy(key.Asc(), I.ID.Asc()).Limit(2)
		if after != nil {
			q = q.Where(velox.Expr[inv]("? > ? OR (? = ? AND ? > ?)", key, after.SortKey, key, after.SortKey, I.ID, after.ID))
		}
		rows, err := velox.Scan[keyedInvoice](ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	first := page(nil)
	if len(first) != 2 || first[0].ID != 1 || first[1].ID != 2 || first[1].SortKey != "v30.0" || first[1].Number != "b" || first[1].OrgID != 1 {
		t.Fatalf("first page: %+v", first)
	}
	second := page(&first[1])
	if len(second) != 2 || second[0].ID != 3 || second[1].ID != 4 || second[0].SortKey != "v30.0" {
		t.Fatalf("second page: %+v", second)
	}
}
