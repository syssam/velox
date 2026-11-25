package integration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/untyped"
)

// insertLog records the rows of each INSERT sent, and counts the other
// statements.
type insertLog struct {
	mu   sync.Mutex
	rows []int // the rows of each INSERT, in order
}

func (l *insertLog) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	if st.Op == velox.OpInsert {
		l.mu.Lock()
		l.rows = append(l.rows, len(st.Rows))
		l.mu.Unlock()
	}
	return next(ctx)
}

func (l *insertLog) take() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.rows
	l.rows = nil
	return r
}

// TestInsertBulk: each row of an InsertBulk is stored as its Insert alone
// stores it, a setter's zero kept over the declared database default and
// an unset column given that default and read back, on each database.
// Rows naming the same columns share one INSERT; rows of another column
// set go in another, all in one transaction (ADR 0153).
func TestInsertBulk(t *testing.T) {
	log := &insertLog{}
	each(t, func(t *testing.T, tg target) {
		freshZeroies(t, tg)
		Z := zeroies
		zeros := []velox.Setter[zeroy]{Z.Seats.Set(0), Z.Rate.Set(0), Z.Active.Set(false), Z.Plan.Set("")}
		rows := []zeroy{{Name: "zero"}, {Name: "unset"}, {Name: "input"}, {Name: "seats"}, {Name: "zero2"}}
		log.take()
		err := tg.db.InsertBulk(
			tg.db.Insert(&rows[0]).Set(zeros...),
			tg.db.Insert(&rows[1]),
			tg.db.Insert(&rows[2]).SetInput(zeroInput{}),
			tg.db.Insert(&rows[3]).Set(Z.Seats.Set(0)),
			tg.db.Insert(&rows[4]).Set(Z.Plan.Set(""), Z.Seats.Set(0), Z.Rate.Set(0), Z.Active.Set(false)),
		).Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// zero, input and zero2 name the same four columns; unset none;
		// seats one: three INSERTs, the first of three rows.
		if got := log.take(); len(got) != 3 || got[0] != 3 || got[1] != 1 || got[2] != 1 {
			t.Errorf("INSERTs of %v rows; want [3 1 1]", got)
		}
		ids := map[int64]bool{}
		for _, r := range rows {
			if r.ID == 0 || ids[r.ID] {
				t.Errorf("%s: key %d read back; want a new one", r.Name, r.ID)
			}
			ids[r.ID] = true
		}
		for _, name := range []string{"zero", "input", "zero2"} {
			if z := storedZeroy(t, tg, name); z.Seats != 0 || z.Rate != 0 || z.Active || z.Plan != "" {
				t.Errorf("%s: stored %+v; want zeros", name, z)
			}
		}
		if z := storedZeroy(t, tg, "unset"); z.Seats != 100 || z.Rate != 1.5 || !z.Active || z.Plan != "free" {
			t.Errorf("unset: stored %+v; want the defaults", z)
		}
		if r := rows[1]; r.Seats != 100 || r.Rate != 1.5 || !r.Active || r.Plan != "free" {
			t.Errorf("unset: read back %+v; want the defaults", r)
		}
		if z := storedZeroy(t, tg, "seats"); z.Seats != 0 || z.Rate != 1.5 || !z.Active || z.Plan != "free" {
			t.Errorf("seats: stored %+v; want seats 0 and the other defaults", z)
		}
		if r := rows[3]; r.Seats != 0 || r.Rate != 1.5 || !r.Active || r.Plan != "free" {
			t.Errorf("seats: read back %+v", r)
		}
	}, velox.WithAround(log.around))
}

// TestInsertBulkAtomic: a bulk of several statements is all or nothing: a
// later statement's error undoes the earlier ones'.
func TestInsertBulkAtomic(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		freshZeroies(t, tg)
		Z := zeroies
		if err := tg.db.Insert(&zeroy{Name: "taken"}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		err := tg.db.InsertBulk(
			tg.db.Insert(&zeroy{Name: "first"}).Set(Z.Seats.Set(0)),
			tg.db.Insert(&zeroy{Name: "taken"}),
		).Exec(ctx)
		if _, ok := errors.AsType[*velox.ConstraintError](err); !ok {
			t.Fatalf("%v; want the unique violation", err)
		}
		n, err := tg.db.Query[zeroy]().Where(zeroies.Name.EQ("first")).Count(ctx)
		if err != nil || n != 0 {
			t.Errorf("%d rows of the first statement kept, %v; want 0", n, err)
		}
	})
}

// TestInsertBulkUpsert: the upsert options of an InsertBulk apply to every
// row; a row's own option is refused.
func TestInsertBulkUpsert(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		freshZeroies(t, tg)
		Z := zeroies
		if err := tg.db.Insert(&zeroy{Name: "a"}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		err := tg.db.InsertBulk(
			tg.db.Insert(&zeroy{Name: "a"}).Set(Z.Plan.Set("pro")),
			tg.db.Insert(&zeroy{Name: "b"}).Set(Z.Plan.Set("pro")),
			tg.db.Insert(&zeroy{Name: "c"}).Set(Z.Plan.Set("team"), Z.Seats.Set(0)),
		).OnConflict(Z.Name).Update(Z.Plan).Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for name, plan := range map[string]string{"a": "pro", "b": "pro", "c": "team"} {
			if z := storedZeroy(t, tg, name); z.Plan != plan {
				t.Errorf("%s: plan %q; want %q", name, z.Plan, plan)
			}
		}
		// A row's skipped SetPtr leaves its column out of its own Update,
		// as alone, and not out of another row's.
		err = tg.db.InsertBulk(
			tg.db.Insert(&zeroy{Name: "a", Plan: "x"}).Set(Z.Plan.SetPtr(nil), Z.Seats.Set(5)),
			tg.db.Insert(&zeroy{Name: "b", Plan: "biz"}).Set(Z.Seats.Set(6)),
		).OnConflict(Z.Name).Update(Z.Plan, Z.Seats).Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if a, b := storedZeroy(t, tg, "a"), storedZeroy(t, tg, "b"); a.Plan != "pro" || a.Seats != 5 || b.Plan != "biz" || b.Seats != 6 {
			t.Errorf("a %+v, b %+v; want a's plan kept and seats 5, b's plan biz and seats 6", a, b)
		}
		err = tg.db.InsertBulk(tg.db.Insert(&zeroy{Name: "d"}).OnConflict(Z.Name).DoNothing()).Exec(ctx)
		if err == nil || !strings.Contains(err.Error(), "carries its own upsert option") {
			t.Errorf("a row's own upsert: %v", err)
		}
	})
}

// TestInsertBulkChunks: a group of rows is chunked by the parameter limit,
// as InsertAll's rows are.
func TestInsertBulkChunks(t *testing.T) {
	log := &insertLog{}
	each(t, func(t *testing.T, tg target) {
		freshZeroies(t, tg)
		Z := zeroies
		rows := make([]zeroy, 5)
		qs := make([]velox.InsertQuery[zeroy], len(rows))
		for i := range rows {
			rows[i].Name = string(rune('a' + i))
			qs[i] = tg.db.Insert(&rows[i]).Set(Z.Seats.Set(int64(i)))
		}
		log.take()
		if err := tg.db.InsertBulk(qs...).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		// Five columns a row, at most ten parameters a statement.
		if got := log.take(); len(got) != 3 || got[0] != 2 || got[1] != 2 || got[2] != 1 {
			t.Errorf("INSERTs of %v rows; want [2 2 1]", got)
		}
		for i, r := range rows {
			if z := storedZeroy(t, tg, r.Name); r.ID == 0 || z.Seats != int64(i) {
				t.Errorf("%s: key %d, stored %+v", r.Name, r.ID, z)
			}
		}
	}, velox.WithAround(log.around), engine.WithMaxParams(10))
}

// TestInsertBulkHooks: each row's hooks run as for an Insert of it, row 0's
// outermost, on a mutation of that one row; a hook's Set of a zero is
// stored, as a setter's is; Affected is the bulk's.
func TestInsertBulkHooks(t *testing.T) {
	var (
		mu       sync.Mutex
		order    []string
		affected []int
	)
	hook := func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			n := 0
			var name string
			for r := range m.Rows() {
				n++
				name, _ = r.Value("name").(string)
			}
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			if n != 1 {
				t.Errorf("a mutation of %d rows; want 1", n)
			}
			if name == "hooked" {
				if err := m.Set("seats", int64(0)); err != nil {
					return err
				}
			}
			err := next.Mutate(ctx, m)
			mu.Lock()
			affected = append(affected, m.Affected())
			mu.Unlock()
			return err
		})
	}
	each(t, func(t *testing.T, tg target) {
		freshZeroies(t, tg)
		order, affected = nil, nil
		rows := []zeroy{{Name: "a"}, {Name: "hooked"}, {Name: "b"}}
		if err := tg.db.InsertBulk(tg.db.Insert(&rows[0]), tg.db.Insert(&rows[1]), tg.db.Insert(&rows[2])).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if strings.Join(order, ",") != "a,hooked,b" {
			t.Errorf("hooks ran for %v; want a, hooked, b", order)
		}
		for _, n := range affected {
			if n != 3 {
				t.Errorf("Affected %v; want 3 for each", affected)
				break
			}
		}
		if z := storedZeroy(t, tg, "hooked"); z.Seats != 0 {
			t.Errorf("hooked: seats %d; want the hook's 0", z.Seats)
		}
		if z := storedZeroy(t, tg, "a"); z.Seats != 100 {
			t.Errorf("a: seats %d; want the default", z.Seats)
		}
	}, velox.WithHooks[zeroy](hook))
}

// TestInsertBulkRecord: a recording DB reports a Change for every row of a
// bulk, across its statements.
func TestInsertBulkRecord(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		freshZeroies(t, tg)
		Z := zeroies
		var rec velox.Recorder
		rdb := tg.db.Record(&rec)
		rows := []zeroy{{Name: "a"}, {Name: "b"}, {Name: "c"}}
		if err := rdb.InsertBulk(rdb.Insert(&rows[0]), rdb.Insert(&rows[1]).Set(Z.Active.Set(false)), rdb.Insert(&rows[2])).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		keys := map[any]bool{}
		for c := range rec.All() {
			keys[c.Key] = true
		}
		if len(keys) != 3 || !keys[rows[0].ID] || !keys[rows[1].ID] || !keys[rows[2].ID] {
			t.Errorf("changes of %v; want the keys %d, %d, %d", keys, rows[0].ID, rows[1].ID, rows[2].ID)
		}
	})
}

// TestInsertBulkSQLAndRefusals: SQL gives the first group's INSERT and
// sends nothing; queries InsertBulk cannot take, and Set on the bulk, fail
// before anything is sent; a bulk of nothing does nothing.
func TestInsertBulkSQLAndRefusals(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		freshZeroies(t, tg)
		Z := zeroies
		db := tg.db
		q, args, err := db.InsertBulk(db.Insert(&zeroy{Name: "a"}).Set(Z.Seats.Set(0)), db.Insert(&zeroy{Name: "b"}), db.Insert(&zeroy{Name: "c"}).Set(Z.Seats.Set(1))).SQL(ctx)
		if err != nil || !strings.HasPrefix(q, "INSERT INTO") || !strings.Contains(q, "seats") || len(args) != 4 {
			t.Errorf("SQL: %q %v, %v; want the INSERT of a and c", q, args, err)
		}
		if n, err := db.Query[zeroy]().Count(ctx); err != nil || n != 0 {
			t.Errorf("SQL stored %d rows, %v", n, err)
		}
		if err := db.InsertBulk[zeroy]().Exec(ctx); err != nil {
			t.Errorf("a bulk of nothing: %v", err)
		}
		if q, _, err := db.InsertBulk[zeroy]().SQL(ctx); q != "" || err != nil {
			t.Errorf("SQL of nothing: %q, %v", q, err)
		}
		other := velox.Open(tg.d, tg.direct)
		inputErr := errors.New("bad input")
		for name, c := range map[string]struct {
			q    velox.InsertQuery[zeroy]
			want string
		}{
			"other DB":  {db.InsertBulk(db.Insert(&zeroy{Name: "x"}), other.Insert(&zeroy{Name: "y"})), "query 1 was built on another DB"},
			"InsertAll": {db.InsertBulk(db.InsertAll([]zeroy{{Name: "x"}})), "query 0 is not an Insert of one row"},
			"nested":    {db.InsertBulk(db.InsertBulk(db.Insert(&zeroy{Name: "x"}))), "query 0 is not an Insert of one row"},
			"UpdateAll": {db.InsertBulk(db.Insert(&zeroy{Name: "x"}).UpdateAll()), "carries its own upsert option"},
			"Set":       {db.InsertBulk(db.Insert(&zeroy{Name: "x"})).Set(Z.Seats.Set(1)), "InsertBulk takes no Set"},
			"nil":       {db.InsertBulk(db.Insert((*zeroy)(nil))), "Insert of a nil pointer"},
			"input":     {db.InsertBulk(db.Insert(&zeroy{Name: "x"}).SetInput(failingInput{inputErr})), "bad input"},
		} {
			if err := c.q.Exec(ctx); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s: Exec: %v; want %q", name, err, c.want)
			}
			if _, _, err := c.q.SQL(ctx); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s: SQL: %v; want %q", name, err, c.want)
			}
		}
		if err := db.InsertBulk(db.Insert(&zeroy{Name: "x"}).SetInput(failingInput{inputErr})).Exec(ctx); !errors.Is(err, inputErr) {
			t.Errorf("SetInput's error: %v; want it as it is", err)
		}
		if n, err := db.Query[zeroy]().Count(ctx); err != nil || n != 0 {
			t.Errorf("the refusals stored %d rows, %v", n, err)
		}
	})
}

// failingInput is an Input whose Setters fail.
type failingInput struct{ err error }

func (f failingInput) Setters() ([]velox.Setter[zeroy], error) { return nil, f.err }

// TestUntypedInsertBulk: untyped.InsertBulk is InsertBulk: each row as its
// Insert alone, a hook's Set of a zero kept and an unset column given its
// default; rows by column set; OnConflict for every row; the queries it
// cannot take refused before anything is sent.
func TestUntypedInsertBulk(t *testing.T) {
	log := &insertLog{}
	hook := func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			for r := range m.Rows() {
				if r.Value("name") == "hooked" {
					if err := m.Set("plan", ""); err != nil {
						return err
					}
				}
			}
			return next.Mutate(ctx, m)
		})
	}
	each(t, func(t *testing.T, tg target) {
		freshZeroies(t, tg)
		zt := untyped.TableOf[zeroy]()
		rows := []zeroy{{Name: "a"}, {Name: "hooked"}, {Name: "b"}}
		log.take()
		err := untyped.InsertBulk(tg.db, zt, untyped.Insert(tg.db, zt, &rows[0]), untyped.Insert(tg.db, zt, &rows[1]), untyped.Insert(tg.db, zt, &rows[2])).Exec(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := log.take(); len(got) != 2 || got[0] != 2 || got[1] != 1 {
			t.Errorf("INSERTs of %v rows; want [2 1]", got)
		}
		if z := storedZeroy(t, tg, "hooked"); z.Plan != "" || z.Seats != 100 {
			t.Errorf("hooked: stored %+v; want the hook's empty plan and the default seats", z)
		}
		if z := storedZeroy(t, tg, "b"); z.Plan != "free" || z.Seats != 100 || rows[2].ID == 0 || rows[0].Plan != "free" {
			t.Errorf("b: stored %+v, read back %+v %+v", z, rows[0], rows[2])
		}
		up := zeroy{Name: "a", Seats: 9}
		if err := untyped.InsertBulk(tg.db, zt, untyped.Insert(tg.db, zt, &up)).OnConflict(1).Update(2).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if z := storedZeroy(t, tg, "a"); z.Seats != 9 {
			t.Errorf("upsert: seats %d; want 9", z.Seats)
		}
		other := velox.Open(tg.d, tg.direct)
		for name, c := range map[string]struct {
			q    untyped.TableInsert
			want string
		}{
			"other DB":   {untyped.InsertBulk(tg.db, zt, untyped.Insert(other, zt, &zeroy{Name: "x"})), "built on another DB"},
			"InsertRows": {untyped.InsertBulk(tg.db, zt, untyped.InsertRows(tg.db, zt, zt.NewRows(1))), "not an Insert of one row"},
			"upsert":     {untyped.InsertBulk(tg.db, zt, untyped.Insert(tg.db, zt, &zeroy{Name: "x"}).OnConflict(1)), "carries its own upsert option"},
			"row":        {untyped.InsertBulk(tg.db, zt, untyped.Insert(tg.db, zt, &up)), ""},
		} {
			err := c.q.Exec(ctx)
			if c.want == "" {
				if err == nil {
					t.Errorf("%s: no error for a duplicate name", name)
				}
				continue
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s: %v; want %q", name, err, c.want)
			}
			if _, _, err := c.q.SQL(ctx); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s: SQL: %v; want %q", name, err, c.want)
			}
		}
		if q, _, err := untyped.InsertBulk(tg.db, zt, untyped.Insert(tg.db, zt, &zeroy{Name: "s"})).SQL(ctx); err != nil || !strings.HasPrefix(q, "INSERT INTO") {
			t.Errorf("SQL: %q, %v", q, err)
		}
		if err := untyped.InsertBulk(tg.db, zt).Exec(ctx); err != nil {
			t.Errorf("a bulk of nothing: %v", err)
		}
	}, velox.WithAround(log.around), velox.WithHooks[zeroy](hook))
}
