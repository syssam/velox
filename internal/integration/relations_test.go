package integration

import (
	"context"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/schema"
)

var It = testmodel.Items

func TestWith(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		invs, err := tg.db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).
			With(I.Items.Where(It.Qty.GT(0)).OrderBy(It.SKU.Desc()).With(It.Invoice)).Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(invs) != 5 {
			t.Fatalf("%d invoices, want 5", len(invs))
		}
		// The filter drops item B (qty 0).
		want := map[int64][]string{1: {"A"}, 2: {"C"}, 3: nil, 4: nil, 5: nil}
		for _, inv := range invs {
			items, loaded := inv.Edges.Items.Get()
			if !loaded || items == nil {
				t.Fatalf("invoice %d: loaded %v %v", inv.ID, loaded, items)
			}
			var skus []string
			for _, it := range items {
				skus = append(skus, it.SKU)
				back, ok := it.Edges.Invoice.Get()
				if !ok || back == nil || back.ID != inv.ID {
					t.Errorf("item %d: its invoice %v %v", it.ID, back, ok)
				}
			}
			wantStrings(t, skus, want[inv.ID])
		}
	})
}

func TestHas(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		ids := func(ps ...velox.Pred[testmodel.Invoice]) []int64 {
			got, err := tg.db.Query[testmodel.Invoice]().Where(ps...).OrderBy(I.ID.Asc()).Limit(10).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var out []int64
			for _, inv := range got {
				out = append(out, inv.ID)
			}
			return out
		}
		wantIDs(t, ids(I.Items.Has()), []int64{1, 2})
		wantIDs(t, ids(I.Items.HasWith(It.Qty.GT(3))), []int64{2})
		wantIDs(t, ids(velox.Not(I.Items.Has())), []int64{3, 4, 5})
		wantIDs(t, ids(I.Items.HasWith(It.Invoice.HasWith(I.Number.EQ("INV-2")))), []int64{2})
		// Invoices 3 and 5 already have no note ("") and updated_at t0, so they
		// match without changing: 3 rows needs clientFoundRows on MySQL.
		n, err := tg.db.Update[testmodel.Invoice]().Set(I.Note.Set(""), I.UpdatedAt.Set(t0)).Where(velox.Not(I.Items.Has())).Exec(ctx)
		if err != nil || n != 3 {
			t.Errorf("update with EXISTS: %d, %v", n, err)
		}
		notes := map[int64]string{}
		all, err := tg.db.Query[testmodel.Invoice]().Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, inv := range all {
			notes[inv.ID] = inv.Note
		}
		if notes[4] != "" || notes[2] != "a note" {
			t.Errorf("notes after the update: 2 %v, 4 %v", notes[2], notes[4])
		}
	})
}

func TestAfterPages(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		for name, c := range map[string]struct {
			order []velox.Order[testmodel.Invoice]
			want  []int64
		}{
			"total desc":     {[]velox.Order[testmodel.Invoice]{I.Total.Desc(), I.ID.Asc()}, []int64{5, 4, 3, 2, 1}},
			"ties: paid, id": {[]velox.Order[testmodel.Invoice]{I.Paid.Desc(), I.ID.Asc()}, []int64{1, 3, 5, 2, 4}},
			"id ascending":   {[]velox.Order[testmodel.Invoice]{I.ID.Asc()}, []int64{1, 2, 3, 4, 5}},
		} {
			t.Run(name, func(t *testing.T) {
				q := tg.db.Query[testmodel.Invoice]().OrderBy(c.order...).Limit(2)
				var got []int64
				var last *testmodel.Invoice
				for range 4 {
					page := q
					if last != nil {
						page = q.After(last)
					}
					rows, err := page.All(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if len(rows) == 0 {
						break
					}
					for _, r := range rows {
						got = append(got, r.ID)
					}
					last = &rows[len(rows)-1]
				}
				wantIDs(t, got, c.want)
			})
		}
	})
}

func TestOfOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		itemIDs := func(p velox.Pred[testmodel.Item]) []int64 {
			t.Helper()
			got, err := tg.db.Query[testmodel.Item]().Where(p).OrderBy(It.ID.Asc()).Limit(10).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var out []int64
			for _, it := range got {
				out = append(out, it.ID)
			}
			return out
		}
		inv1, inv2 := testmodel.Invoice{ID: 1}, testmodel.Invoice{ID: 2}
		wantIDs(t, itemIDs(I.Items.Of(&inv1, &inv2)), []int64{1, 2, 3})
		wantIDs(t, itemIDs(I.Items.Of(&inv1, &inv1)), []int64{1, 2})
		wantIDs(t, itemIDs(I.Items.Of()), nil)
		wantIDs(t, itemIDs(I.Items.Where(It.Qty.GT(0)).Of(&inv1)), []int64{1})

		// The inverse; the target's org applies to it.
		item3 := testmodel.Item{ID: 3, InvoiceID: 2}
		for _, c := range []struct {
			ctx  context.Context
			want []int64
		}{{ctx, []int64{2}}, {testmodel.Org.With(context.Background(), int64(1)), []int64{2}}, {testmodel.Org.With(context.Background(), int64(2)), nil}} {
			got, err := tg.db.Query[testmodel.Invoice]().Where(It.Invoice.Of(&item3)).Limit(10).All(c.ctx)
			if err != nil {
				t.Fatal(err)
			}
			wantIDs(t, ids(got), c.want)
		}

		// A NULL key relates to nothing; the target's Read policy applies:
		// ticket 5's parent, ticket 3, is bob's.
		parents := func(qctx context.Context, x testmodel.Ticket) []int64 {
			t.Helper()
			got, err := tg.db.Query[testmodel.Ticket]().Where(Tk.Parent.Of(&x)).Limit(10).All(qctx)
			if err != nil {
				t.Fatal(err)
			}
			return ticketIDs(got)
		}
		three := int64(3)
		wantIDs(t, parents(admin.ctx, testmodel.Ticket{ID: 1}), nil)
		wantIDs(t, parents(admin.ctx, testmodel.Ticket{ID: 5, ParentID: &three}), []int64{3})
		wantIDs(t, parents(ann.ctx, testmodel.Ticket{ID: 5, ParentID: &three}), nil)

		// A write whose only condition is Of with no owners changes nothing.
		n, err := tg.db.Update[testmodel.Item]().Set(It.Qty.Set(9)).Where(I.Items.Of()).Exec(ctx)
		if err != nil || n != 0 {
			t.Errorf("update of no owners: %d, %v", n, err)
		}
	})
}

// The per-owner model: shelves have notes, which are org-scoped and have a
// Read policy that hides secret ones. Written by hand as velox-gen writes a
// Descriptor.

type shelf struct {
	ID    int64
	Notes velox.Many[note]
	Tags  velox.Many[tag]
}

var Shelves = struct {
	ID    velox.NumberCol[shelf, int64]
	Notes velox.Rel[shelf, note]
	Tags  velox.Rel[shelf, tag]
}{velox.NumberCol[shelf, int64]{I: 0}, velox.Rel[shelf, note]{I: 0}, velox.Rel[shelf, tag]{I: 1}}

var shelfMeta velox.MetaOnce[shelf]

func (*shelf) VeloxMeta() *velox.Meta[shelf] { return shelfMeta.Load(shelfDescriptor) }

func shelfDescriptor() velox.Descriptor[shelf] {
	return velox.Descriptor[shelf]{
		Table:      "shelves",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}},
		PrimaryKey: []int{0},
		Edges:      []velox.EdgeDescriptor{velox.EdgeTo[note]("Notes", 0, 1), velox.EdgeThrough[shelfTag, tag]("Tags", 0, 1, 2, 0)},
		FieldPtr:   func(x *shelf, i int) any { return &x.ID },
		EdgeFieldPtr: func(x *shelf, i int) any {
			if i == 0 {
				return &x.Notes
			}
			return &x.Tags
		},
	}
}

// shelfTag is the join entity of shelf.Tags. Its key is an id of its own, so
// a link between a shelf and a tag may repeat.
type shelfTag struct {
	ID      int64
	ShelfID int64
	TagID   int64
}

var shelfTagMeta velox.MetaOnce[shelfTag]

func (*shelfTag) VeloxMeta() *velox.Meta[shelfTag] { return shelfTagMeta.Load(shelfTagDescriptor) }

func shelfTagDescriptor() velox.Descriptor[shelfTag] {
	return velox.Descriptor[shelfTag]{
		Table:      "shelf_tags",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "ShelfID", Name: "shelf_id"}, {Field: "TagID", Name: "tag_id"}},
		PrimaryKey: []int{0},
		FieldPtr:   func(x *shelfTag, i int) any { return [...]any{&x.ID, &x.ShelfID, &x.TagID}[i] },
	}
}

type note struct {
	ID      int64
	ShelfID int64
	OrgID   int64
	Pos     int64
	Secret  bool
}

var Notes = struct {
	ID    velox.NumberCol[note, int64]
	OrgID velox.NumberCol[note, int64]
	Pos   velox.NumberCol[note, int64]
}{velox.NumberCol[note, int64]{I: 0}, velox.NumberCol[note, int64]{I: 2}, velox.NumberCol[note, int64]{I: 3}}

var noteMeta velox.MetaOnce[note]

func (*note) VeloxMeta() *velox.Meta[note] { return noteMeta.Load(noteDescriptor) }

// Policy hides secret notes; fixture.OrgPolicy, bound before it, confines
// notes to the context's org.
func (*note) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context) (velox.Predicate, error) {
		return velox.FieldEQ("secret", false), nil
	}}
}

func (*note) Hooks() []velox.Hook { return fixture.OrgHooks() }

// Mixin lists the behaviour that runs before note's own.
func (*note) Mixin() []schema.Mixin {
	return fixture.Fns{Policy: []func() velox.Policy{fixture.OrgPolicy}}.Mixin()
}

func noteDescriptor() velox.Descriptor[note] {
	return velox.Descriptor[note]{
		Schema: (*note)(nil),
		Table:  "notes",
		Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "ShelfID", Name: "shelf_id"}, {Field: "OrgID", Name: "org_id", Immutable: true},
			{Field: "Pos", Name: "pos"}, {Field: "Secret", Name: "secret"}},
		PrimaryKey: []int{0},
		FieldPtr:   func(x *note, i int) any { return [...]any{&x.ID, &x.ShelfID, &x.OrgID, &x.Pos, &x.Secret}[i] },
	}
}

// stmtLog counts the statements sent, by entity.
type stmtLog struct {
	mu sync.Mutex
	n  map[string]int
}

func (l *stmtLog) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	l.mu.Lock()
	l.n[st.Entity.Name]++
	l.mu.Unlock()
	return next(ctx)
}

func (l *stmtLog) take() map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.n
	l.n = map[string]int{}
	return n
}

// shelfSetup creates shelves 1–4 and their notes, which are (id: shelf, org,
// pos, secret) 1: 1, 2, 0; 2: 1, 1, 1; 3: 1, 1, 1; 4: 1, 1, 0, secret; 5: 1,
// 1, 2; 6: 2, 1, 5; 7: 2, 1, 5; 8: 4, 1, 3, secret.
func shelfSetup(t *testing.T, tg target) {
	t.Helper()
	tg.exec(t, "DROP TABLE IF EXISTS notes")
	tg.exec(t, "DROP TABLE IF EXISTS shelves")
	tg.exec(t, "CREATE TABLE shelves (id BIGINT PRIMARY KEY)")
	tg.exec(t, "CREATE TABLE notes (id BIGINT PRIMARY KEY, shelf_id BIGINT NOT NULL, org_id BIGINT NOT NULL, pos BIGINT NOT NULL, secret BOOLEAN NOT NULL)")
	for id := range int64(4) {
		tg.exec(t, "INSERT INTO shelves (id) VALUES (?)", id+1)
	}
	for _, r := range []struct {
		id, shelf, org, pos int64
		secret              bool
	}{{1, 1, 2, 0, false}, {2, 1, 1, 1, false}, {3, 1, 1, 1, false}, {4, 1, 1, 0, true}, {5, 1, 1, 2, false},
		{6, 2, 1, 5, false}, {7, 2, 1, 5, false}, {8, 4, 1, 3, true}} {
		tg.exec(t, "INSERT INTO notes (id, shelf_id, org_id, pos, secret) VALUES (?, ?, ?, ?, ?)", r.id, r.shelf, r.org, r.pos, r.secret)
	}
}

// TestLimitEachOnRealDatabases pages a one-to-many relation per owner. The
// notes are (id: shelf, org, pos, secret) 1: 1, 2, 0; 2: 1, 1, 1; 3: 1, 1, 1;
// 4: 1, 1, 0, secret; 5: 1, 1, 2; 6: 2, 1, 5; 7: 2, 1, 5; 8: 4, 1, 3, secret.
// Under org 1, note 1 is another org's and note 4 secret: both sort first
// and take no rank. Notes 2 and 3, and 6 and 7, tie, and break by id. Shelf 3
// has no notes, and shelf 4 only a hidden one.
func TestLimitEachOnRealDatabases(t *testing.T) {
	log := &stmtLog{n: map[string]int{}}
	each(t, func(t *testing.T, tg target) {
		shelfSetup(t, tg)
		acme := testmodel.Org.With(context.Background(), int64(1))
		notes := func(qctx context.Context, rel velox.Rel[shelf, note], ps ...velox.Pred[shelf]) map[int64][]int64 {
			t.Helper()
			log.take()
			ss, err := tg.db.Query[shelf]().Where(ps...).OrderBy(Shelves.ID.Asc()).With(rel).Limit(10).All(qctx)
			if err != nil {
				t.Fatal(err)
			}
			if n := log.take()["integration.note"]; n != 1 {
				t.Errorf("%d statements for the notes, want 1", n)
			}
			got := map[int64][]int64{}
			for _, s := range ss {
				ns, ok := s.Notes.Get()
				if !ok || ns == nil {
					t.Fatalf("shelf %d: notes not loaded", s.ID)
				}
				got[s.ID] = []int64{}
				for _, n := range ns {
					got[s.ID] = append(got[s.ID], n.ID)
				}
			}
			return got
		}
		check := func(got, want map[int64][]int64) {
			t.Helper()
			for id, w := range want {
				wantIDs(t, got[id], w)
			}
			if len(got) != len(want) {
				t.Errorf("owners %v, want %v", got, want)
			}
		}
		byPos := Shelves.Notes.OrderBy(Notes.Pos.Asc())
		check(notes(acme, byPos.LimitEach(1)), map[int64][]int64{1: {2}, 2: {6}, 3: {}, 4: {}})
		check(notes(acme, byPos.LimitEach(2)), map[int64][]int64{1: {2, 3}, 2: {6, 7}, 3: {}, 4: {}})
		// n+1 rows tell whether an owner has a next page: shelf 1 has, 2 not.
		check(notes(acme, byPos.LimitEach(3)), map[int64][]int64{1: {2, 3, 5}, 2: {6, 7}, 3: {}, 4: {}})
		// Descending: the tie still breaks by id, ascending.
		check(notes(acme, Shelves.Notes.OrderBy(Notes.Pos.Desc()).LimitEach(2)), map[int64][]int64{1: {5, 2}, 2: {6, 7}, 3: {}, 4: {}})
		// One owner.
		check(notes(acme, byPos.LimitEach(2), Shelves.ID.EQ(1)), map[int64][]int64{1: {2, 3}})
		// WithoutPolicy: every org, and the secret notes too. Notes 1 and 4 tie
		// at position 0, and the id breaks the tie.
		check(notes(ctx, byPos.LimitEach(2)), map[int64][]int64{1: {1, 4}, 2: {6, 7}, 3: {}, 4: {8}})

		// The generated model: each invoice's item with the most quantity.
		invs, err := tg.db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).With(I.Items.LimitEach(1).OrderBy(It.Qty.Desc()).With(It.Invoice)).Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := map[int64][]string{1: {"A"}, 2: {"C"}, 3: nil, 4: nil, 5: nil}
		for _, inv := range invs {
			items, ok := inv.Edges.Items.Get()
			if !ok {
				t.Fatalf("invoice %d: items not loaded", inv.ID)
			}
			var skus []string
			for _, it := range items {
				skus = append(skus, it.SKU)
				if back, ok := it.Edges.Invoice.Get(); !ok || back == nil || back.ID != inv.ID {
					t.Errorf("item %d: its invoice %v %v", it.ID, back, ok)
				}
			}
			wantStrings(t, skus, want[inv.ID])
		}
	}, velox.WithAround(log.around))
}

// shelves returns shelves with ids, in that order.
func shelves(ids ...int64) []*shelf {
	out := make([]*shelf, len(ids))
	for i, id := range ids {
		out[i] = &shelf{ID: id}
	}
	return out
}

// noteIDs is each list of notes' ids.
func noteIDs(lists [][]note) [][]int64 {
	out := [][]int64{}
	for _, ns := range lists {
		ids := []int64{}
		for _, n := range ns {
			ids = append(ids, n.ID)
		}
		out = append(out, ids)
	}
	return out
}

// tagIDLists is each list of tags' ids.
func tagIDLists(lists [][]tag) [][]int64 {
	out := [][]int64{}
	for _, ts := range lists {
		ids := []int64{}
		for _, g := range ts {
			ids = append(ids, g.ID)
		}
		out = append(out, ids)
	}
	return out
}

func wantIDLists(t *testing.T, got, want [][]int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%d lists %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		wantIDs(t, got[i], want[i])
	}
}

// TestLoadEachOnRealDatabases reads each owner's page with LoadEach: the notes
// of shelfSetup (one-to-many, with the org and a Read policy) and the tags
// of throughSetup (many-to-many, with the org), aligned with the owners,
// zero-child owners included, in one statement each.
func TestLoadEachOnRealDatabases(t *testing.T) {
	log := &stmtLog{n: map[string]int{}}
	each(t, func(t *testing.T, tg target) {
		shelfSetup(t, tg)
		throughSetup(t, tg)
		acme := testmodel.Org.With(context.Background(), int64(1))
		notes := func(qctx context.Context, q velox.Query[note], r velox.Rel[shelf, note], owners ...int64) [][]int64 {
			t.Helper()
			log.take()
			got, err := velox.LoadEach(qctx, q, r, shelves(owners...))
			if err != nil {
				t.Fatal(err)
			}
			if n := log.take()["integration.note"]; n != 1 {
				t.Errorf("%d statements for the notes, want 1", n)
			}
			return noteIDs(got)
		}
		byPos := tg.db.Query[note]().OrderBy(Notes.Pos.Asc())
		// Under org 1, note 1 is another org's and note 4 secret; shelf 3
		// has no notes, and shelf 4 only a secret one.
		wantIDLists(t, notes(acme, byPos, Shelves.Notes.LimitEach(2), 1, 2, 3, 4), [][]int64{{2, 3}, {6, 7}, {}, {}})
		wantIDLists(t, notes(acme, byPos, Shelves.Notes.LimitEach(3), 4, 1, 1), [][]int64{{}, {2, 3, 5}, {2, 3, 5}})
		wantIDLists(t, notes(ctx, byPos, Shelves.Notes.LimitEach(2), 1, 2, 3, 4), [][]int64{{1, 4}, {6, 7}, {}, {8}})
		wantIDLists(t, notes(acme, byPos, Shelves.Notes, 1, 3), [][]int64{{2, 3, 5}, {}})
		// After a row, inside the window: shelf 1's page after note 2.
		x, err := tg.db.Query[note]().Where(Notes.ID.EQ(2)).One(acme)
		if err != nil {
			t.Fatal(err)
		}
		wantIDLists(t, notes(acme, tg.db.Query[note]().OrderBy(Notes.Pos.Asc(), Notes.ID.Asc()).After(x), Shelves.Notes.LimitEach(2), 1, 2),
			[][]int64{{3, 5}, {6, 7}})
		// Only the position asked: the owner's key is read too.
		ns, err := velox.LoadEach(acme, byPos.Columns(Notes.Pos), Shelves.Notes.LimitEach(1), shelves(1, 2))
		if err != nil {
			t.Fatal(err)
		}
		if len(ns) != 2 || len(ns[0]) != 1 || ns[0][0].ShelfID != 1 || ns[0][0].Pos != 1 || ns[0][0].ID != 0 || len(ns[1]) != 1 || ns[1][0].Pos != 5 {
			t.Errorf("only Pos: %+v", ns)
		}

		// Many-to-many: book 4's tag is org 2's.
		books := []*book{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}
		byName := tg.db.Query[tag]().OrderBy(Tags.Name.Asc())
		for _, c := range []struct {
			qctx context.Context
			r    velox.Rel[book, tag]
			want [][]int64
		}{
			{acme, Books.Tags.LimitEach(1), [][]int64{{2}, {2}, {}, {}}},
			{acme, Books.Tags.LimitEach(2), [][]int64{{2, 1}, {2}, {}, {}}},
			{ctx, Books.Tags.LimitEach(1), [][]int64{{2}, {2}, {}, {3}}},
			{ctx, Books.Tags, [][]int64{{2, 1}, {2}, {}, {3}}},
		} {
			log.take()
			got, err := velox.LoadEach(c.qctx, byName, c.r, books)
			if err != nil {
				t.Fatal(err)
			}
			if n := log.take(); n["integration.tag"] != 1 || n["integration.bookTag"] != 0 {
				t.Errorf("statements %v, want one for the tags and none for the join rows", n)
			}
			wantIDLists(t, tagIDLists(got), c.want)
		}
	}, velox.WithAround(log.around))
}

// TestCountEachOnRealDatabases counts each owner's rows with CountEach, as
// TestLoadEachOnRealDatabases reads them: 0 for an owner with none.
func TestCountEachOnRealDatabases(t *testing.T) {
	log := &stmtLog{n: map[string]int{}}
	each(t, func(t *testing.T, tg target) {
		shelfSetup(t, tg)
		throughSetup(t, tg)
		acme := testmodel.Org.With(context.Background(), int64(1))
		count := func(got []int64, err error) []int64 {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
			total := 0
			for _, k := range log.take() {
				total += k
			}
			if total != 1 {
				t.Errorf("%d statements, want one", total)
			}
			return got
		}
		log.take()
		q := tg.db.Query[note]()
		wantIDs(t, count(velox.CountEach(acme, q, Shelves.Notes, shelves(1, 2, 3, 4))), []int64{3, 2, 0, 0})
		wantIDs(t, count(velox.CountEach(ctx, q, Shelves.Notes, shelves(4, 1, 3, 1))), []int64{1, 5, 0, 5})
		wantIDs(t, count(velox.CountEach(acme, q.Where(Notes.Pos.GT(1)), Shelves.Notes, shelves(1, 2))), []int64{1, 2})
		books := []*book{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}
		wantIDs(t, count(velox.CountEach(acme, tg.db.Query[tag](), Books.Tags, books)), []int64{2, 1, 0, 0})
		wantIDs(t, count(velox.CountEach(ctx, tg.db.Query[tag](), Books.Tags, books)), []int64{2, 1, 0, 1})
	}, velox.WithAround(log.around))
}
