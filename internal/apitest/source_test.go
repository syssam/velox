package apitest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	ts "github.com/syssam/velox/internal/testmodel/schema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/untyped"
	"github.com/syssam/velox/veloxtest"
)

// sourceDB is a SQLite database of the test model with sample 1, "a", and
// its two lines, opened with opts.
func sourceDB(t *testing.T, opts ...velox.Option) *velox.DB {
	t.Helper()
	db := veloxtest.Open(t, "sqlite", "", []schema.Interface{ts.Sample{}, ts.Line{}, ts.Invoice{}, ts.Item{}, ts.Ticket{}},
		veloxtest.WithOptions(opts...))
	s := newSample("a")
	if err := db.Insert(&s).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAll([]testmodel.Line{{SampleID: s.ID}, {SampleID: s.ID}}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}

// newSample is a sample named name, ready to insert.
func newSample(name string) testmodel.Sample {
	return testmodel.Sample{Name: name, Status: "new", When: time.Now(), Blob: []byte("b")}
}

// wantLines runs a sample's QueryLines and wants n lines.
func wantLines(t *testing.T, ctx context.Context, what string, s *testmodel.Sample, n int) {
	t.Helper()
	got, err := s.QueryLines().AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatalf("%s: QueryLines: %v", what, err)
	}
	if len(got) != n {
		t.Fatalf("%s: QueryLines read %d lines, want %d", what, len(got), n)
	}
}

// wantSample runs a line's QuerySample and wants the sample named name.
func wantSample(t *testing.T, what string, l *testmodel.Line, name string) {
	t.Helper()
	got, err := l.QuerySample().One(ctx)
	if err != nil {
		t.Fatalf("%s: QuerySample: %v", what, err)
	}
	if got.Name != name {
		t.Fatalf("%s: QuerySample read %q, want %q", what, got.Name, name)
	}
}

// TestSourceReads: every way of reading an entity gives it the DB that read
// it, which its edge queries run on.
func TestSourceReads(t *testing.T) {
	db := sourceDB(t)
	S, L := testmodel.Samples, testmodel.Lines
	q := db.Query[testmodel.Sample]().Where(S.Name.EQ("a"))

	all, err := q.Limit(10).All(ctx)
	if err != nil || len(all) != 1 {
		t.Fatal(all, err)
	}
	wantLines(t, ctx, "All", &all[0], 2)

	first, err := q.First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "First", first, 2)

	one, err := q.One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "One", one, 2)

	// A statement in the loop body would wait for SQLite's one connection.
	var iterated []testmodel.Sample
	for s, err := range q.Limit(10).Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		iterated = append(iterated, s)
	}
	wantLines(t, ctx, "Iter", &iterated[0], 2)

	raw, err := db.Raw[testmodel.Sample]("test", "SELECT * FROM samples WHERE name = 'a'").All(ctx)
	if err != nil || len(raw) != 1 {
		t.Fatal(raw, err)
	}
	wantLines(t, ctx, "Raw.All", &raw[0], 2)
	rawOne, err := db.Raw[testmodel.Sample]("test", "SELECT * FROM samples WHERE name = 'a'").One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "Raw.One", rawOne, 2)
	iterated = nil
	for s, err := range db.Raw[testmodel.Sample]("test", "SELECT * FROM samples WHERE name = 'a'").Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		iterated = append(iterated, s)
	}
	wantLines(t, ctx, "Raw.Iter", &iterated[0], 2)

	rows, err := untyped.Query(db, untyped.TableOf[testmodel.Sample]()).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	us := untyped.RowsOf[testmodel.Sample](rows)
	if len(us) != 1 {
		t.Fatal(us)
	}
	wantLines(t, ctx, "untyped.Query", &us[0], 2)
	uf, err := untyped.FromQuery(q).First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "untyped First", uf.(*testmodel.Sample), 2)

	lines, err := db.Query[testmodel.Line]().With(L.Sample).Limit(10).All(ctx)
	if err != nil || len(lines) != 2 {
		t.Fatal(lines, err)
	}
	wantSample(t, "the owner of With", &lines[0], "a")
	ls, _ := lines[0].Edges.Sample.Get()
	wantLines(t, ctx, "With", ls, 2)

	nested, err := q.With(S.Lines.With(L.Sample)).First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nl, _ := nested.Edges.Lines.Get()
	wantSample(t, "With", &nl[0], "a")
	ns, _ := nl[1].Edges.Sample.Get()
	wantLines(t, ctx, "nested With", ns, 2)

	each, err := velox.LoadEach(ctx, db.Query[testmodel.Line](), S.Lines, []*testmodel.Sample{first})
	if err != nil || len(each[0]) != 2 {
		t.Fatal(each, err)
	}
	wantSample(t, "LoadEach", &each[0][0], "a")

	joined, err := db.Query[testmodel.Line]().Join(L.Sample).Limit(10).All(ctx)
	if err != nil || len(joined) != 2 {
		t.Fatal(joined, err)
	}
	js, ok := joined[0].Edges.Sample.Get()
	if !ok || js == nil {
		t.Fatal("Join: no sample")
	}
	wantLines(t, ctx, "Join", js, 2)
	var ilines []testmodel.Line
	for l, err := range db.Query[testmodel.Line]().LeftJoin(L.Sample).Limit(10).Iter(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		ilines = append(ilines, l)
	}
	wantSample(t, "Iter with LeftJoin", &ilines[0], "a")
	ijs, _ := ilines[0].Edges.Sample.Get()
	wantLines(t, ctx, "Iter's LeftJoin", ijs, 2)
	urows, err := untyped.Query(db, untyped.TableOf[testmodel.Line]()).Join(int(testmodel.Lines.Sample.I), untyped.TableOf[testmodel.Sample]()).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ul := untyped.RowsOf[testmodel.Line](urows)
	ujs, _ := ul[0].Edges.Sample.Get()
	wantLines(t, ctx, "untyped Join", ujs, 2)
}

// TestSourceLeftJoinDenied: a LeftJoin to an entity the context may not
// read keeps each row with no target, and the row reads its edge on its DB,
// under the policy that hid the target.
func TestSourceLeftJoinDenied(t *testing.T) {
	type denyKey struct{}
	db := sourceDB(t, velox.WithFilter(func(ctx context.Context, _ *velox.DB, fe velox.FilterEntity, _ velox.Op) (velox.Predicate, error) {
		if fe.Name() == "Sample" && ctx.Value(denyKey{}) != nil {
			return velox.Predicate{}, velox.Deny("no samples")
		}
		return velox.Allow(), nil
	}))
	denied := context.WithValue(ctx, denyKey{}, true)
	lines, err := db.Query[testmodel.Line]().LeftJoin(testmodel.Lines.Sample).Limit(10).All(denied)
	if err != nil || len(lines) != 2 {
		t.Fatal(lines, err)
	}
	if s, ok := lines[0].Edges.Sample.Get(); !ok || s != nil {
		t.Fatalf("the denied target: %v, %v", s, ok)
	}
	if _, err := lines[0].QuerySample().One(denied); !errors.Is(err, velox.ErrDenied) {
		t.Errorf("QuerySample under the denial: %v, want ErrDenied", err)
	}
	wantSample(t, "the denied target's owner", &lines[0], "a")
}

// TestSourceWrites: an entity inserted, upserted, updated or saved reads
// its edges on the DB that wrote it.
func TestSourceWrites(t *testing.T) {
	db := sourceDB(t)
	S, L := testmodel.Samples, testmodel.Lines

	b := newSample("b")
	if err := db.Insert(&b).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Insert(&testmodel.Line{SampleID: b.ID}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "Insert", &b, 1)

	all := []testmodel.Sample{newSample("c"), newSample("d")}
	if err := db.InsertAll(all).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "InsertAll", &all[1], 0)

	e, f := newSample("e"), newSample("f")
	if err := db.InsertBulk(db.Insert(&e), db.Insert(&f).Set(S.Score.Set(2))).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "InsertBulk's first", &e, 0)
	wantLines(t, ctx, "InsertBulk's second", &f, 0)

	up := newSample("b2")
	up.ID = b.ID
	if err := db.Insert(&up).OnConflict(S.ID).UpdateAll().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "upsert", &up, 1)

	one, err := db.Update[testmodel.Sample]().Set(S.Score.Set(7)).Where(S.ID.EQ(b.ID)).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "UpdateQuery.One by key", one, 1)
	one, err = db.Update[testmodel.Sample]().Set(S.Score.Set(8)).Where(S.Name.EQ("b2")).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "UpdateQuery.One", one, 1)

	saved := testmodel.Sample{ID: b.ID, Score: 9}
	if err := db.Save(&saved, S.Score).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "Save", &saved, 1)

	line := testmodel.Line{SampleID: b.ID}
	if err := untyped.Insert(db, untyped.TableOf[testmodel.Line](), &line).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantSample(t, "untyped.Insert", &line, "b2")
	_ = L
}

// TestSourceNone: an entity no statement made has no DB: each statement of
// its edge query fails with ErrNoSource, naming the edge and db.Query, and
// none panics.
func TestSourceNone(t *testing.T) {
	var decoded testmodel.Sample
	if err := json.Unmarshal([]byte(`{"id":1,"name":"a"}`), &decoded); err != nil {
		t.Fatal(err)
	}
	loaded, _ := velox.LoadedOne(&testmodel.Sample{ID: 1}).Get()
	for what, s := range map[string]*testmodel.Sample{
		"built by hand": {ID: 1},
		"LoadedOne":     loaded,
		"decoded":       &decoded,
	} {
		q := s.QueryLines().Limit(10)
		_, errAll := q.All(ctx)
		_, errFirst := q.First(ctx)
		_, errCount := q.Count(ctx)
		_, errExists := q.Exists(ctx)
		_, _, errSQL := q.SQL(ctx)
		var errIter error
		for _, err := range q.Iter(ctx) {
			errIter = err
		}
		_, errEach := velox.LoadEach(ctx, s.QueryLines(), testmodel.Samples.Lines, []*testmodel.Sample{{ID: 1}})
		for name, err := range map[string]error{"All": errAll, "First": errFirst, "Count": errCount, "Exists": errExists, "SQL": errSQL, "Iter": errIter} {
			if !errors.Is(err, velox.ErrNoSource) || !strings.Contains(err.Error(), "testmodel.Sample.lines") || !strings.Contains(err.Error(), "db.Query") {
				t.Errorf("%s: %s: %v, want ErrNoSource naming the edge and db.Query", what, name, err)
			}
		}
		if errEach == nil {
			t.Errorf("%s: LoadEach on the edge query ran", what)
		}
	}
}

// TestSourceTx: an entity read in a transaction reads its edges in it while
// it is live, seeing its uncommitted rows, and on the DB it was begun on once
// it has committed or rolled back. A savepoint's does the same.
func TestSourceTx(t *testing.T) {
	db := sourceDB(t)
	S := testmodel.Samples
	var kept *testmodel.Sample
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		s, err := tx.Query[testmodel.Sample]().Where(S.Name.EQ("a")).First(ctx)
		if err != nil {
			return err
		}
		if err := tx.Insert(&testmodel.Line{SampleID: s.ID}).Exec(ctx); err != nil {
			return err
		}
		wantLines(t, ctx, "in the transaction", s, 3)
		kept = s
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "after commit", kept, 3)

	rollback := errors.New("roll back")
	err = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		s, err := tx.Query[testmodel.Sample]().Where(S.Name.EQ("a")).First(ctx)
		if err != nil {
			return err
		}
		if err := tx.Insert(&testmodel.Line{SampleID: s.ID}).Exec(ctx); err != nil {
			return err
		}
		wantLines(t, ctx, "in the transaction to roll back", s, 4)
		kept = s
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	wantLines(t, ctx, "after rollback", kept, 3)

	err = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		var inner *testmodel.Sample
		err := tx.Tx(ctx, func(ctx context.Context, sp *velox.DB) error {
			s, err := sp.Query[testmodel.Sample]().Where(S.Name.EQ("a")).First(ctx)
			if err != nil {
				return err
			}
			if err := sp.Insert(&testmodel.Line{SampleID: s.ID}).Exec(ctx); err != nil {
				return err
			}
			wantLines(t, ctx, "in the savepoint", s, 4)
			inner = s
			return rollback
		})
		if !errors.Is(err, rollback) {
			return err
		}
		// The savepoint rolled back: its entity reads in the transaction.
		if err := tx.Insert(&testmodel.Line{SampleID: inner.ID}).Exec(ctx); err != nil {
			return err
		}
		wantLines(t, ctx, "after the savepoint, in the transaction", inner, 4)
		kept = inner
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "after the savepoint's transaction", kept, 4)
}

// TestSourceWithoutPolicy: an entity read through WithoutPolicy reads its
// edges under the policies, with the viewer of the context the edge query
// runs under.
func TestSourceWithoutPolicy(t *testing.T) {
	db := sourceDB(t)
	org := testmodel.Org.With(ctx, 1)
	inv := testmodel.Invoice{Number: "N1", Data: []byte("d")}
	if err := db.Insert(&inv).Exec(org); err != nil {
		t.Fatal(err)
	}
	tk := testmodel.Ticket{Owner: "ann", Title: "t", InvoiceID: inv.ID}
	if err := db.Insert(&tk).Exec(testmodel.WithUser(org, "ann")); err != nil {
		t.Fatal(err)
	}
	priv := db.WithoutPolicy("test")
	got, err := priv.Query[testmodel.Ticket]().Limit(1).First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := got.QueryInvoice().One(ctx); err == nil {
		t.Error("the edge of an entity read WithoutPolicy was read without the policy")
	}
	if _, err := got.QueryInvoice().One(testmodel.Org.With(ctx, 2)); !errors.Is(err, velox.ErrNotFound) {
		t.Errorf("another org: %v, want ErrNotFound", err)
	}
	if i, err := got.QueryInvoice().One(org); err != nil || i.ID != inv.ID {
		t.Errorf("the invoice's org: %v, %v", i, err)
	}
	// A transaction begun WithoutPolicy gives its entities a Source under
	// the policies too, in it and after it.
	var kept *testmodel.Ticket
	if err := priv.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		var err error
		if kept, err = tx.Query[testmodel.Ticket]().Limit(1).First(ctx); err != nil {
			return err
		}
		if _, err := kept.QueryInvoice().One(ctx); err == nil {
			t.Error("in a WithoutPolicy transaction, the edge was read without the policy")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := kept.QueryInvoice().One(ctx); err == nil {
		t.Error("after a WithoutPolicy transaction, the edge was read without the policy")
	}
}

// TestSourceKeepsFiltersAndRecording: the edge query of an entity read
// through a recording DB, or a DB with a filter, runs with the DB's filter,
// and records nothing, as it is a read.
func TestSourceKeepsFiltersAndRecording(t *testing.T) {
	db := sourceDB(t, velox.WithFilter(func(ctx context.Context, _ *velox.DB, fe velox.FilterEntity, _ velox.Op) (velox.Predicate, error) {
		if fe.Name() == "Line" {
			return velox.FieldNEQ("id", int64(1)), nil
		}
		return velox.Allow(), nil
	}))
	var rec velox.Recorder
	rdb := db.Record(&rec)
	s, err := rdb.Query[testmodel.Sample]().Limit(1).First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "the filter", s, 1)
	n := newSample("r")
	if err := rdb.Insert(&n).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	wantLines(t, ctx, "a recorded insert", &n, 0)
	if rec.Len() != 1 {
		t.Errorf("recorded %d changes, want the insert's 1", rec.Len())
	}
}
