package apitest

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestInSelectSQL: InSelect is an EXISTS correlated on the two columns, and
// NotInSelect its Not, in a query, an update and a delete.
func TestInSelectSQL(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	big := It.InvoiceID.Select(It.Qty.GT(2))
	text, args := sqlOK(t)(db.Query[testmodel.Invoice]().Where(I.ID.InSelect(big), I.ID.NotInSelect(It.InvoiceID.Select())).Limit(5).SQL(ctx))
	want := `SELECT ` + allCols + ` FROM "invoices" WHERE EXISTS (SELECT 1 FROM "items" AS "vx1" WHERE "vx1"."invoice_id" = "invoices"."id" AND "qty" > $1) AND NOT (EXISTS (SELECT 1 FROM "items" AS "vx1" WHERE "vx1"."invoice_id" = "invoices"."id")) LIMIT 5`
	if text != want || len(args) != 1 {
		t.Errorf("\n got %s\nwant %s", text, want)
	}
	// The other way: items whose invoice is paid, a subquery on a column
	// that is not a key.
	text, _ = sqlOK(t)(db.Query[testmodel.Item]().Where(It.InvoiceID.InSelect(I.ID.Select(I.Paid.EQ(true)))).Limit(5).SQL(ctx))
	if !strings.Contains(text, `EXISTS (SELECT 1 FROM "invoices" AS "vx1" WHERE "vx1"."id" = "items"."invoice_id" AND "paid")`) {
		t.Errorf("items: %s", text)
	}
	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.InSelect(big)).Exec(ctx); err != nil || !strings.Contains(e.SQL, `UPDATE "invoices" SET`) || !strings.Contains(e.SQL, `WHERE EXISTS (SELECT 1 FROM "items"`) {
		t.Errorf("update: %v %s", err, e.SQL)
	}
	if _, err := db.Delete[testmodel.Invoice]().Where(I.ID.NotInSelect(big)).Exec(ctx); err != nil || !strings.Contains(e.SQL, `DELETE FROM "invoices" WHERE NOT (EXISTS`) {
		t.Errorf("delete: %v %s", err, e.SQL)
	}
}

// TestInSelectRenderCache: two subqueries that differ only in their columns
// or their entity render apart, though their conditions have the same shape.
func TestInSelectRenderCache(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	sqlOf := func(p velox.Pred[testmodel.Invoice]) string {
		t.Helper()
		text, _ := sqlOK(t)(db.Query[testmodel.Invoice]().Where(p).Limit(1).SQL(ctx))
		return text
	}
	byInvoice := sqlOf(I.ID.InSelect(It.InvoiceID.Select()))
	byID := sqlOf(I.ID.InSelect(It.ID.Select()))
	byQty := sqlOf(I.OrgID.InSelect(It.Qty.Select()))
	fromInvoices := sqlOf(I.ID.InSelect(I.OrgID.Select()))
	viaRelation := sqlOf(I.Items.Has())
	seen := map[string]string{}
	for name, s := range map[string]string{"byInvoice": byInvoice, "byID": byID, "byQty": byQty, "fromInvoices": fromInvoices} {
		if other, ok := seen[s]; ok {
			t.Errorf("%s and %s render alike: %s", name, other, s)
		}
		seen[s] = name
	}
	if !strings.Contains(byID, `"vx1"."id" = "invoices"."id"`) || !strings.Contains(byQty, `"vx1"."qty" = "invoices"."org_id"`) ||
		!strings.Contains(fromInvoices, `FROM "invoices" AS "vx1" WHERE "vx1"."org_id" = "invoices"."id"`) {
		t.Errorf("columns:\n%s\n%s\n%s", byID, byQty, fromInvoices)
	}
	if byInvoice != viaRelation {
		t.Errorf("the subquery on the relation's keys differs from Has:\n%s\n%s", byInvoice, viaRelation)
	}
}

// TestInSelectErrors: the zero Subquery, and a bad condition inside Select,
// fail the statement.
func TestInSelectErrors(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	badItem := velox.Rel[testmodel.Item, testmodel.Invoice]{I: 9}
	for name, tc := range map[string]struct {
		p    velox.Pred[testmodel.Invoice]
		want string
	}{
		"zero":           {I.ID.InSelect(velox.Subquery[int64]{}), "zero Subquery"},
		"zero, negated":  {I.ID.NotInSelect(velox.Subquery[int64]{}), "zero Subquery"},
		"bad relation":   {I.ID.InSelect(It.InvoiceID.Select(badItem.Has())), "has no relation 9"},
		"zero condition": {I.ID.InSelect(It.InvoiceID.Select(velox.Pred[testmodel.Item]{})), "zero Predicate"},
		"bad column":     {velox.NumberCol[testmodel.Invoice, int64]{I: 99}.InSelect(It.ID.Select()), "testmodel.Invoice: has no column 99"},
		"bad target":     {I.ID.InSelect(velox.NumberCol[testmodel.Item, int64]{I: 99}.Select()), "testmodel.Item: has no column 99"},
	} {
		_, err := db.Query[testmodel.Invoice]().Where(tc.p).Limit(1).All(ctx)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error containing %q", name, err, tc.want)
		}
	}
}

// TestInSelectOnABidirectionalJoin: a condition on a bidirectional join
// entity is also asked with its two columns swapped, an InSelect's too.
func TestInSelectOnABidirectionalJoin(t *testing.T) {
	F := bnodeFriends
	nodeID := velox.NumberCol[bnode, int64]{I: 0}
	e := &fakeconn.Exec{}
	if _, err := openPolicied(velox.Postgres, e).Delete[bnodeFriend]().Where(F.FriendID.InSelect(nodeID.Select(nodeID.GT(5)))).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	want := `DELETE FROM "bnode_friends" WHERE (EXISTS (SELECT 1 FROM "bnodes" AS "vx1" WHERE "vx1"."id" = "bnode_friends"."friend_id" AND "id" > $1) OR EXISTS (SELECT 1 FROM "bnodes" AS "vx1" WHERE "vx1"."id" = "bnode_friends"."node_id" AND "id" > $2))`
	if len(e.Log) != 1 || e.Log[0] != want || !reflect.DeepEqual(e.Args, []any{int64(5), int64(5)}) {
		t.Errorf("sent %q %v,\nwant %q", e.Log, e.Args, want)
	}
}

// TestInSelectRuns: on SQLite, the subquery's entity gets its interceptor
// and its Read policy, as HasWith's target does; NULL is in no subquery.
func TestInSelectRuns(t *testing.T) {
	db := interceptorDB(t)
	bg := context.Background()
	if _, err := db.Exec(bg, "test fixture", `INSERT INTO "owners" VALUES (3, 'o3', NULL)`); err != nil {
		t.Fatal(err)
	}
	O, N := fixture.IOwners, fixture.INotes
	owners := func(db *velox.DB, p velox.Pred[fixture.IOwner]) []int64 {
		t.Helper()
		os, err := db.Query[fixture.IOwner]().Where(p).OrderBy(O.ID.Asc()).AllowUnbounded().All(bg)
		if err != nil {
			t.Fatal(err)
		}
		return ownerIDs(os)
	}
	// Visible notes: 1 of owner 1 and 4 of owner 2. Note 2 is hidden by
	// the interceptor, note 3 by the Read policy.
	wantIDs(t, "owners of notes", owners(db, O.ID.InSelect(N.OwnerID.Select())), 1, 2)
	wantIDs(t, "owners of a hidden note", owners(db, O.ID.InSelect(N.OwnerID.Select(N.Body.EQ("b")))))
	wantIDs(t, "owners of a secret note", owners(db, O.ID.InSelect(N.OwnerID.Select(N.Body.EQ("secret")))))
	wantIDs(t, "without the policy", owners(db.WithoutPolicy("test"), O.ID.InSelect(N.OwnerID.Select(N.Body.EQ("secret")))), 1)
	wantIDs(t, "owners of no note", owners(db, O.ID.NotInSelect(N.OwnerID.Select())), 3)
	// Owner 1 pins note 2, which is hidden; owner 2 pins note 1; owner 3
	// pins nothing, and NULL is in no subquery.
	wantIDs(t, "pinned a visible note", owners(db, O.PinnedID.InSelect(N.ID.Select())), 2)
	wantIDs(t, "pinned no visible note", owners(db, O.PinnedID.NotInSelect(N.ID.Select())), 1, 3)
	// Nested: owners of notes whose owner is named o2.
	nested := O.ID.InSelect(N.OwnerID.Select(N.OwnerID.InSelect(O.ID.Select(O.Name.EQ("o2")))))
	wantIDs(t, "nested", owners(db, nested), 2)

	notes, err := db.Query[fixture.INote]().Where(N.OwnerID.InSelect(O.ID.Select(O.Name.EQ("o2")))).AllowUnbounded().All(bg)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "notes of o2", noteIDs(notes), 4)
	n, err := db.Query[fixture.IOwner]().Where(O.ID.InSelect(N.OwnerID.Select())).Count(bg)
	if err != nil || n != 2 {
		t.Errorf("Count: %d, %v", n, err)
	}
}
