package integration

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/schema/mixin/softdelete"
)

// changed is the FieldChange of the column of the given name in c's
// Diff, and whether there is one.
func changed(c velox.Change, name string) (velox.FieldChange, bool) {
	for _, f := range c.Diff() {
		if f.Name == name {
			return f, true
		}
	}
	return velox.FieldChange{}, false
}

// TestRecordBulkUpdate: on each database a recorded bulk update reports
// every row it changed, in key order, with the old and new values the
// database holds; updated_at, the UpdateDefault column, is a changed field
// unless RecordIgnore names it, and invoice 2, whose note was already "a
// note", lists only total and updated_at.
func TestRecordBulkUpdate(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		var rec, ign velox.Recorder
		n, err := tg.db.Record(&rec).Update[testmodel.Invoice]().Where(I.OrgID.EQ(1), I.ID.LT(3)).Set(I.Total.Add(1), I.Note.Set("a note")).Exec(ctx)
		if err != nil || n != 2 {
			t.Fatalf("Update: %d, %v", n, err)
		}
		cs := slices.Collect(rec.All())
		if len(cs) != 2 {
			t.Fatalf("%d Changes, want 2", len(cs))
		}
		for i, c := range cs {
			id := int64(i + 1)
			if c.Key != id || c.Op != velox.OpUpdate {
				t.Errorf("change %d: %+v", i, c)
			}
			if f, ok := changed(c, "total"); !ok || f.Old != float64(10*id) || f.New != float64(10*id+1) {
				t.Errorf("invoice %d total: %+v", id, f)
			}
			if _, ok := changed(c, "note"); ok != (id == 1) {
				t.Errorf("invoice %d: note changed %v", id, ok)
			}
			if f, ok := changed(c, "updated_at"); !ok || !f.Old.(time.Time).Equal(t0) {
				t.Errorf("invoice %d updated_at: %+v", id, f)
			}
			if I.Number.Old(c) == "" || I.Number.New(c) != I.Number.Old(c) || I.Total.New(c) != float64(10*id+1) {
				t.Errorf("invoice %d: number %q -> %q, total %v", id, I.Number.Old(c), I.Number.New(c), I.Total.New(c))
			}
		}
		if _, err := tg.db.Record(&ign, velox.RecordIgnore("updated_at")).Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Add(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		cs = slices.Collect(ign.All())
		if len(cs) != 1 || len(cs[0].Diff()) != 1 {
			t.Fatalf("ignoring updated_at: %+v", cs)
		}
		if _, ok := changed(cs[0], "total"); !ok || I.UpdatedAt.New(cs[0]).IsZero() {
			t.Errorf("ignoring updated_at: %+v %v", cs[0].Diff(), I.UpdatedAt.New(cs[0]))
		}
	})
}

// TestRecordConcurrent: recorded updates of one row from several goroutines
// each see the value the one before left: the old totals form one chain,
// none read twice, as the read locks the row (SQLite runs them one at a
// time).
func TestRecordConcurrent(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		const n = 8
		var rec velox.Recorder
		rdb := tg.db.Record(&rec)
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for range n {
			wg.Go(func() {
				// A context of its own: on SQLite's one connection, velox
				// refuses a statement under the context another goroutine's
				// transaction holds.
				c, cancel := context.WithCancel(ctx)
				defer cancel()
				_, err := rdb.Update[testmodel.Invoice]().Where(I.ID.EQ(1)).Set(I.Total.Add(1)).Exec(c)
				errs <- err
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var olds []float64
		news := map[float64]bool{}
		for c := range rec.All() {
			f, ok := changed(c, "total")
			if !ok || f.New != f.Old.(float64)+1 {
				t.Fatalf("change %+v", c.Diff())
			}
			olds = append(olds, f.Old.(float64))
			news[f.New.(float64)] = true
		}
		if len(olds) != n {
			t.Fatalf("%d Changes, want %d", len(olds), n)
		}
		slices.Sort(olds)
		for i, o := range olds {
			if o != float64(10+i) || (i > 0 && !news[o]) {
				t.Fatalf("old totals %v, want 10 to %d, each the new total of another", olds, 10+n-1)
			}
		}
	})
}

// TestRecordHardDelete: a recorded delete reports each row's old values and
// no new ones.
func TestRecordHardDelete(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		tg.exec(t, "DELETE FROM tickets")
		tg.exec(t, "DELETE FROM items")
		var rec velox.Recorder
		n, err := tg.db.Record(&rec).Delete[testmodel.Invoice]().Where(I.ID.In(2, 4)).Exec(ctx)
		if err != nil || n != 2 {
			t.Fatalf("Delete: %d, %v", n, err)
		}
		cs := slices.Collect(rec.All())
		if len(cs) != 2 || cs[0].Key != int64(2) || cs[1].Key != int64(4) {
			t.Fatalf("changes %+v", cs)
		}
		for _, c := range cs {
			if c.Op != velox.OpDelete || c.NewRow() != nil || I.Note.Old(c) != "a note" || I.Total.Old(c) != float64(10*c.Key.(int64)) {
				t.Errorf("change %v: %+v old %+v", c.Key, c, c.OldRow())
			}
		}
		wantIDs(t, query(t, tg), []int64{1, 3, 5})
	})
}

// TestRecordUpsert: on each database a recorded upsert reports the row that
// conflicted as an update, with the old values the database held and the
// new ones it stores (owner and invoice_id, which the update branch leaves
// alone, keep their stored values, not the ones sent), and the new row as
// an insert with the key the database gave it. An upsert that changes
// nothing, and one DoNothing keeps, report nothing.
func TestRecordUpsert(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		var rec velox.Recorder
		rdb := tg.db.Record(&rec)
		zed := testmodel.WithUser(ctx, "zed")
		xs := []testmodel.Ticket{
			{OrgID: 1, Owner: "zed", Title: "A1", Done: true, InvoiceID: 9},
			{OrgID: 1, Owner: "zed", Title: "N1", Done: true, InvoiceID: 2},
		}
		if err := rdb.InsertAll(xs).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(zed); err != nil {
			t.Fatal(err)
		}
		cs := slices.Collect(rec.All())
		if len(cs) != 2 {
			t.Fatalf("%d Changes, want 2: %+v", len(cs), cs)
		}
		up, in := cs[0], cs[1]
		if up.Op != velox.OpUpdate || up.Key != int64(1) || Tk.Done.Old(up) || !Tk.Done.New(up) {
			t.Errorf("update: %+v, done %v -> %v", up, Tk.Done.Old(up), Tk.Done.New(up))
		}
		if Tk.Owner.New(up) != "ann" || Tk.InvoiceID.New(up) != 1 || Tk.CreatedBy.New(up) != "seed" || len(up.Diff()) != 1 {
			t.Errorf("update's new row %+v, want the stored one; Diff %+v", up.NewRow(), up.Diff())
		}
		var id int64
		if err := tg.direct.QueryRow("SELECT id FROM tickets WHERE org_id = 1 AND title = 'N1'").Scan(&id); err != nil {
			t.Fatal(err)
		}
		if in.Op != velox.OpInsert || in.Key != id || in.OldRow() != nil || Tk.Owner.New(in) != "zed" || Tk.CreatedBy.New(in) != "zed" {
			t.Errorf("insert: %+v, key %v, want %d", in, in.Key, id)
		}

		rec.Reset()
		same := testmodel.Ticket{OrgID: 1, Owner: "zed", Title: "A1", Done: true}
		if err := rdb.Insert(&same).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(zed); err != nil {
			t.Fatal(err)
		}
		kept := testmodel.Ticket{OrgID: 1, Owner: "zed", Title: "A2", Done: false}
		if err := rdb.Insert(&kept).OnConflict(Tk.OrgID, Tk.Title).DoNothing().Exec(zed); err != nil {
			t.Fatal(err)
		}
		if rec.Len() != 0 {
			t.Errorf("an unchanged and a kept row were reported: %+v", slices.Collect(rec.All()))
		}
		back := testmodel.Ticket{OrgID: 1, Owner: "zed", Title: "A1", Done: false}
		if err := rdb.Insert(&back).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(zed); err != nil {
			t.Fatal(err)
		}
		cs = slices.Collect(rec.All())
		if len(cs) != 1 || cs[0].Op != velox.OpUpdate || cs[0].Key != int64(1) || !Tk.Done.Old(cs[0]) || Tk.Done.New(cs[0]) {
			t.Errorf("single upsert: %+v", cs)
		}
	})
}

// TestRecordUpsertCollation: the database's collation decides what
// conflicts, and recording follows it: "Alice@x" sent over a stored
// "alice@x", under a case-insensitive title, is one update with the real
// old and new values, and the title as stored. PostgreSQL has no
// case-insensitive collation without an extension, and is skipped.
func TestRecordUpsertCollation(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		var ddl string
		switch tg.d {
		case velox.SQLite:
			ddl = strings.Replace(sqliteTickets, "title TEXT NOT NULL", "title TEXT COLLATE NOCASE NOT NULL", 1)
		case velox.MySQL:
			ddl = strings.Replace(mysqlTickets, "title VARCHAR(64) NOT NULL", "title VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL", 1)
		default:
			t.Skip("no case-insensitive collation without an extension")
		}
		tg.exec(t, "DROP TABLE tickets")
		tg.exec(t, ddl)
		tg.exec(t, "INSERT INTO tickets (created_by, id, org_id, owner, title, done, invoice_id) VALUES ('seed', 1, 1, 'ann', 'alice@x', ?, 1)", false)
		var rec velox.Recorder
		tk := testmodel.Ticket{OrgID: 1, Owner: "zed", Title: "Alice@x", Done: true}
		if err := tg.db.Record(&rec).Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(testmodel.WithUser(ctx, "zed")); err != nil {
			t.Fatal(err)
		}
		cs := slices.Collect(rec.All())
		if len(cs) != 1 {
			t.Fatalf("%d Changes, want 1: %+v", len(cs), cs)
		}
		c := cs[0]
		if c.Op != velox.OpUpdate || c.Key != int64(1) || Tk.Done.Old(c) || !Tk.Done.New(c) || Tk.Title.Old(c) != "alice@x" || Tk.Title.New(c) != "alice@x" || Tk.Owner.New(c) != "ann" {
			t.Errorf("change %+v: done %v -> %v, title %q -> %q", c, Tk.Done.Old(c), Tk.Done.New(c), Tk.Title.Old(c), Tk.Title.New(c))
		}
	})
}

// TestRecordUpsertGeneratedKey: an upsert whose conflict column is the
// generated key, left zero, never conflicts (the INSERT leaves the key
// out): each row is an insert with the key the database gave it, for
// Insert and for a DoNothing InsertAll. MySQL, which returns no keys from
// an upsert, refuses it before sending anything.
func TestRecordUpsertGeneratedKey(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		var rec velox.Recorder
		rdb := tg.db.Record(&rec)
		zed := testmodel.WithUser(ctx, "zed")
		one := testmodel.Ticket{OrgID: 1, Owner: "zed", Title: "G1"}
		if tg.d == velox.MySQL {
			err := rdb.Insert(&one).OnConflict(Tk.ID).Update(Tk.Done).Exec(zed)
			if err == nil || !strings.Contains(err.Error(), "insert such rows without OnConflict") {
				t.Errorf("MySQL: %v, want the refusal", err)
			}
			return
		}
		if err := rdb.Insert(&one).OnConflict(Tk.ID).Update(Tk.Done).Exec(zed); err != nil {
			t.Fatal(err)
		}
		xs := []testmodel.Ticket{{OrgID: 1, Owner: "zed", Title: "G2"}, {OrgID: 1, Owner: "zed", Title: "G3"}}
		if err := rdb.InsertAll(xs).OnConflict(Tk.ID).DoNothing().Exec(zed); err != nil {
			t.Fatal(err)
		}
		cs := slices.Collect(rec.All())
		if len(cs) != 3 {
			t.Fatalf("%d Changes, want 3: %+v", len(cs), cs)
		}
		for i, title := range []string{"G1", "G2", "G3"} {
			var id int64
			if err := tg.direct.QueryRow(rebind(tg.d, "SELECT id FROM tickets WHERE org_id = 1 AND title = ?"), title).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if c := cs[i]; c.Op != velox.OpInsert || c.Key != id || Tk.Title.New(c) != title {
				t.Errorf("change %d: %+v, key %v, want %d", i, c, c.Key, id)
			}
		}
	})
}

// TestRecordUpsertNullConflict: under a unique parent_id, a row whose
// parent_id is NULL never conflicts: it is an insert with the key the
// database gave it (MySQL, which returns no keys, refuses it before sending
// anything). A DoNothing InsertAll whose generated key is not in the
// conflict target reports exactly the rows it inserted, not the one kept.
func TestRecordUpsertNullConflict(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		uq := ", CONSTRAINT tickets_parent_uq UNIQUE (parent_id))"
		tg.exec(t, "DROP TABLE tickets")
		tg.exec(t, strings.TrimSuffix(strings.TrimSpace(tg.tickets), ")")+uq)
		tg.exec(t, "INSERT INTO tickets (created_by, id, org_id, owner, title, done, invoice_id) VALUES ('seed', 1, 1, 'ann', 'K', ?, 1)", false)
		var rec velox.Recorder
		rdb := tg.db.Record(&rec)
		zed := testmodel.WithUser(ctx, "zed")
		id := func(title string) int64 {
			var id int64
			if err := tg.direct.QueryRow(rebind(tg.d, "SELECT id FROM tickets WHERE org_id = 1 AND title = ?"), title).Scan(&id); err != nil {
				t.Fatal(err)
			}
			return id
		}
		n := testmodel.Ticket{OrgID: 1, Owner: "zed", Title: "N"}
		err := rdb.Insert(&n).OnConflict(Tk.ParentID).Update(Tk.Done).Exec(zed)
		if tg.d == velox.MySQL {
			if err == nil || !strings.Contains(err.Error(), "conflict column parent_id is NULL") {
				t.Errorf("MySQL: %v, want the refusal", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			cs := slices.Collect(rec.All())
			if len(cs) != 1 || cs[0].Op != velox.OpInsert || cs[0].Key != id("N") {
				t.Errorf("NULL conflict: %+v, want an insert of key %d", cs, id("N"))
			}
		}
		rec.Reset()
		xs := []testmodel.Ticket{{OrgID: 1, Owner: "zed", Title: "K"}, {OrgID: 1, Owner: "zed", Title: "M"}}
		if err := rdb.InsertAll(xs).OnConflict(Tk.OrgID, Tk.Title).DoNothing().Exec(zed); err != nil {
			t.Fatal(err)
		}
		cs := slices.Collect(rec.All())
		if len(cs) != 1 || cs[0].Op != velox.OpInsert || cs[0].Key != id("M") || Tk.Title.New(cs[0]) != "M" {
			t.Errorf("DoNothing: %+v, want the insert of M (key %d)", cs, id("M"))
		}
	})
}

// TestRecordSoftDelete: on each database a recorded delete of a soft-deleted
// entity reports one Change, a delete marked Soft, with the row before
// (deleted_at NULL) and after (deleted_at set); Changed and Diff, which
// report updates, give nothing. Under softdelete.Skip the delete is hard: not
// Soft, and no new row.
func TestRecordSoftDelete(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		softTables(t, tg)
		D := fixture.SDocs
		in1 := testmodel.Org.With(context.Background(), int64(1))
		var rec velox.Recorder
		rdb := tg.pdb.Record(&rec)
		if n, err := rdb.Delete[fixture.SDoc]().Where(D.ID.EQ(2)).Exec(in1); err != nil || n != 1 {
			t.Fatalf("Delete: %d, %v", n, err)
		}
		cs := slices.Collect(rec.All())
		if len(cs) != 1 {
			t.Fatalf("%d Changes, want 1", len(cs))
		}
		c := cs[0]
		if c.Op != velox.OpDelete || !c.Soft || c.Key != int64(2) || c.Entity != "SDoc" {
			t.Errorf("soft delete: %+v", c)
		}
		old, cur, ok := velox.RowsOf[fixture.SDoc](c)
		if !ok || old == nil || cur == nil || old.DeletedAt != nil || cur.DeletedAt == nil || old.Title != "b" || cur.Title != "b" {
			t.Fatalf("RowsOf: %+v %+v %v", old, cur, ok)
		}
		if c.Changed(D.DeletedAt) || c.Diff() != nil {
			t.Errorf("Changed %v, Diff %+v: a soft delete is no update", c.Changed(D.DeletedAt), c.Diff())
		}
		if !D.DeletedAt.Old(c).IsZero() || D.DeletedAt.New(c).IsZero() || D.Title.Old(c) != "b" {
			t.Errorf("handles: deleted_at %v -> %v, title %q", D.DeletedAt.Old(c), D.DeletedAt.New(c), D.Title.Old(c))
		}
		rec.Reset()
		if n, err := rdb.Delete[fixture.SDoc]().Where(D.ID.EQ(2)).Exec(in1); err != nil || n != 0 || rec.Len() != 0 {
			t.Errorf("re-delete: %d, %v, %d Changes; want none", n, err, rec.Len())
		}
		if n, err := rdb.Delete[fixture.SDoc]().Where(D.ID.EQ(1)).Exec(softdelete.Skip(in1)); err != nil || n != 1 {
			t.Fatalf("hard delete: %d, %v", n, err)
		}
		cs = slices.Collect(rec.All())
		if len(cs) != 1 || cs[0].Op != velox.OpDelete || cs[0].Soft || cs[0].NewRow() != nil || cs[0].Key != int64(1) || D.Title.Old(cs[0]) != "a" {
			t.Errorf("hard delete: %+v", cs)
		}
	})
}

// personTables creates persons 1 to 4, with no spouse, and their friends'
// join table, empty.
func personTables(t *testing.T, tg target) {
	t.Helper()
	tg.exec(t, "DROP TABLE IF EXISTS person_friends")
	tg.exec(t, "DROP TABLE IF EXISTS persons")
	tg.exec(t, "CREATE TABLE persons (id BIGINT PRIMARY KEY, name VARCHAR(64) NOT NULL, person_spouse BIGINT, UNIQUE (person_spouse))")
	tg.exec(t, "CREATE TABLE person_friends (node_id BIGINT NOT NULL, friend_id BIGINT NOT NULL, PRIMARY KEY (node_id, friend_id))")
	for id := range int64(4) {
		tg.exec(t, "INSERT INTO persons (id, name) VALUES (?, ?)", id+1, "p")
	}
}

// spouseChange is a Change of a person: its key, op, and spouse before and
// after (0 for none).
type spouseChange struct {
	key      int64
	op       velox.Op
	old, cur int64
}

// spouseChanges are rec's Changes as spouseChanges; it resets rec.
func spouseChanges(t *testing.T, rec *velox.Recorder) []spouseChange {
	t.Helper()
	var out []spouseChange
	for c := range rec.All() {
		if c.Entity != "person" {
			t.Errorf("a Change of %s", c.Entity)
		}
		out = append(out, spouseChange{c.Key.(int64), c.Op, Persons.SpouseID.Old(c), Persons.SpouseID.New(c)})
	}
	rec.Reset()
	return out
}

// TestRecordBidiRepoint: on each database a recorded write of the key of the
// bidirectional one-to-one edge Spouse reports every row whose key it
// changes, once each, in the order of the statements that first touch them:
// the row written, the old partner it cleared, and the new partner it
// linked. A write that links the pair already linked touches rows and
// changes none, and reports nothing; a write that fails reports nothing.
func TestRecordBidiRepoint(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		personTables(t, tg)
		P := Persons
		var rec velox.Recorder
		rdb := tg.db.Record(&rec)
		save := func(id int64, spouse *int64) {
			t.Helper()
			if err := rdb.Save(&person{ID: id, SpouseID: spouse}, P.SpouseID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		check := func(step string, want ...spouseChange) {
			t.Helper()
			if got := spouseChanges(t, &rec); !slices.Equal(got, want) {
				t.Errorf("%s: %+v, want %+v", step, got, want)
			}
		}
		if err := rdb.Insert(&person{ID: 5, Name: "p", SpouseID: new(int64(1))}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		check("insert", spouseChange{5, velox.OpInsert, 0, 1}, spouseChange{1, velox.OpUpdate, 0, 5})
		save(5, new(int64(2)))
		check("re-point", spouseChange{5, velox.OpUpdate, 1, 2}, spouseChange{1, velox.OpUpdate, 5, 0}, spouseChange{2, velox.OpUpdate, 0, 5})
		save(5, new(int64(2)))
		check("same pair")
		if _, err := rdb.Update[person]().Set(P.SpouseID.SetNull()).Where(P.ID.EQ(2)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		check("clear", spouseChange{2, velox.OpUpdate, 5, 0}, spouseChange{5, velox.OpUpdate, 2, 0})
		err := rdb.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			return tx.Save(&person{ID: 3, SpouseID: new(int64(4))}, P.SpouseID).Exec(ctx)
		})
		if err != nil {
			t.Fatal(err)
		}
		check("in a Tx", spouseChange{3, velox.OpUpdate, 0, 4}, spouseChange{4, velox.OpUpdate, 0, 3})
		if err := rdb.Save(&person{ID: 1, SpouseID: new(int64(4))}, P.SpouseID).Exec(ctx); err == nil {
			t.Fatal("linking a taken partner succeeded")
		}
		check("a taken partner")
	})
}

// friendChanges are rec's Changes, each its op and the join row's node and
// friend; it resets rec.
func friendChanges(t *testing.T, rec *velox.Recorder) []string {
	t.Helper()
	var out []string
	for c := range rec.All() {
		row, _ := c.NewRow().(*personFriend)
		if c.Op == velox.OpDelete {
			row, _ = c.OldRow().(*personFriend)
		}
		if c.Entity != "personFriend" || row == nil {
			t.Errorf("a Change %+v", c)
			continue
		}
		out = append(out, fmt.Sprintf("%v %d-%d", c.Op, row.NodeID, row.FriendID))
	}
	rec.Reset()
	return out
}

// TestRecordM2M: on each database the edge setters of the bidirectional
// many-to-many edge Friends report each join row they insert or delete, both
// rows of a link, as both are written: Add an insert per row, and none for a
// link that exists; Remove a delete per row, and none for a key not linked;
// Clear and Add in one write the deletes, then the inserts. An insert of
// the join entity reports both rows too.
func TestRecordM2M(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		personTables(t, tg)
		P := Persons
		var rec velox.Recorder
		rdb := tg.db.Record(&rec)
		set := func(step string, s velox.Setter[person], want ...string) {
			t.Helper()
			if _, err := rdb.Update[person]().Set(s).Where(P.ID.EQ(1)).Exec(ctx); err != nil {
				t.Fatalf("%s: %v", step, err)
			}
			got := friendChanges(t, &rec)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("%s: %q, want %q", step, got, want)
			}
		}
		set("add", P.Friends.Add(2, 3), "insert 1-2", "insert 2-1", "insert 1-3", "insert 3-1")
		set("add again", P.Friends.Add(2))
		set("remove", P.Friends.Remove(2, 4), "delete 1-2", "delete 2-1")
		if _, err := rdb.Update[person]().Set(P.Friends.Clear(), P.Friends.Add(4)).Where(P.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		got := friendChanges(t, &rec)
		if len(got) != 4 || !slices.Equal(got[:2], []string{"delete 1-3", "delete 3-1"}) || !slices.Contains(got, "insert 1-4") || !slices.Contains(got, "insert 4-1") {
			t.Errorf("clear and add: %q, want the deletes of 1-3 and 3-1, then the inserts of 1-4 and 4-1", got)
		}
		if err := rdb.Insert(&personFriend{2, 3}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if got := friendChanges(t, &rec); !slices.Equal(got, []string{"insert 2-3", "insert 3-2"}) {
			t.Errorf("insert of a join row: %q, want both rows", got)
		}
	})
}
