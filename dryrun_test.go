package velox_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox2"
)

// A daudited is an entity whose hooks do what a hook with an audit trail
// does: after the statement, read a count and write a dnote row on the
// write's DB, m.DB(), and return the write's error. Under a dry run the write
// is refused, so the hook records what it got in the auditRec its context
// carries.
type daudited struct {
	ID    int64
	Owner string
}

type auditRec struct {
	reads    int
	readErr  error
	writeErr error
	writes   int
}

type auditKey struct{}

func auditHook(next velox.Mutator[daudited]) velox.Mutator[daudited] {
	return velox.MutateFunc[daudited](func(ctx context.Context, m *velox.Mutation[daudited]) error {
		if err := next.Mutate(ctx, m); err != nil {
			return err
		}
		rec, _ := ctx.Value(auditKey{}).(*auditRec)
		if rec == nil {
			rec = new(auditRec)
		}
		rec.reads++
		_, rec.readErr = m.DB().Query[dnote]().Count(ctx)
		rec.writes++
		rec.writeErr = m.DB().Insert(&dnote{Owner: "audit"}).Exec(ctx)
		return rec.writeErr
	})
}

func (*daudited) Hooks() []velox.Hook[daudited] { return []velox.Hook[daudited]{auditHook} }

var dauditedMeta velox.MetaOnce[daudited]

func (*daudited) VeloxMeta() *velox.Meta[daudited] { return dauditedMeta.Load(dauditedDesc) }

func dauditedDesc() velox.Desc[daudited] {
	return velox.Desc[daudited]{
		Hooks:      func() velox.BoundHooks { return velox.BindHooks((*daudited)(nil).Hooks()) },
		Table:      "daudited",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Owner", Name: "owner"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *daudited, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.Owner
		},
	}
}

var (
	dauditedID    = velox.NumberCol[daudited, int64]{I: 0}
	dauditedOwner = velox.StringCol[daudited, string]{I: 1}
)

// sentWrites returns the statements e was given that write or begin.
func sentWrites(e *velox.FakeExec) []string {
	var out []string
	for _, s := range e.Log {
		for _, p := range []string{"INSERT", "UPDATE", "DELETE", "BEGIN", "SAVEPOINT"} {
			if strings.HasPrefix(s, p) {
				out = append(out, s)
			}
		}
	}
	return out
}

// dryContext returns a context that IsDryRun reports true for, as SQL(ctx)
// gives to everything it runs.
func dryContext(t *testing.T, e *velox.FakeExec) (context.Context, *velox.DB) {
	t.Helper()
	var dry context.Context
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(ctx context.Context, st *velox.Statement) error {
		if st.DryRun {
			dry = ctx
		}
		return nil
	}))
	if _, _, err := db.Query[dnote]().Limit(1).SQL(context.Background()); err != nil {
		t.Fatal(err)
	}
	if dry == nil || !velox.IsDryRun(dry) {
		t.Fatal("no dry-run context")
	}
	return dry, db
}

// TestDryRunSendsNoWrite pins Review Focus 2: SQL(ctx) of a write whose hook
// writes an audit row through m.DB() returns the statement and sends no
// write; the hook's read runs, and its write is refused with ErrDryRun. The
// same statement sent for real writes the row.
func TestDryRunSendsNoWrite(t *testing.T) {
	stmts := []struct {
		name string
		sql  func(ctx context.Context, db *velox.DB) (string, error)
		exec func(ctx context.Context, db *velox.DB) error
		want string
	}{
		{"Insert",
			func(ctx context.Context, db *velox.DB) (string, error) {
				s, _, err := db.Insert(&daudited{Owner: "ann"}).SQL(ctx)
				return s, err
			},
			func(ctx context.Context, db *velox.DB) error { return db.Insert(&daudited{Owner: "ann"}).Exec(ctx) },
			`INSERT INTO "daudited"`},
		{"InsertAll",
			func(ctx context.Context, db *velox.DB) (string, error) {
				s, _, err := db.InsertAll([]daudited{{Owner: "ann"}, {Owner: "bob"}}).SQL(ctx)
				return s, err
			},
			func(ctx context.Context, db *velox.DB) error {
				return db.InsertAll([]daudited{{Owner: "ann"}, {Owner: "bob"}}).Exec(ctx)
			},
			`INSERT INTO "daudited"`},
		{"Update",
			func(ctx context.Context, db *velox.DB) (string, error) {
				s, _, err := db.Update[daudited]().Set(dauditedOwner.Set("x")).Where(dauditedID.EQ(1)).SQL(ctx)
				return s, err
			},
			func(ctx context.Context, db *velox.DB) error {
				_, err := db.Update[daudited]().Set(dauditedOwner.Set("x")).Where(dauditedID.EQ(1)).Exec(ctx)
				return err
			},
			`UPDATE "daudited"`},
		{"Save",
			func(ctx context.Context, db *velox.DB) (string, error) {
				s, _, err := db.Save(&daudited{ID: 1, Owner: "x"}, dauditedOwner).SQL(ctx)
				return s, err
			},
			func(ctx context.Context, db *velox.DB) error {
				return db.Save(&daudited{ID: 1, Owner: "x"}, dauditedOwner).Exec(ctx)
			},
			`UPDATE "daudited"`},
		{"Delete",
			func(ctx context.Context, db *velox.DB) (string, error) {
				s, _, err := db.Delete[daudited]().Where(dauditedID.EQ(1)).SQL(ctx)
				return s, err
			},
			func(ctx context.Context, db *velox.DB) error {
				_, err := db.Delete[daudited]().Where(dauditedID.EQ(1)).Exec(ctx)
				return err
			},
			`DELETE FROM "daudited"`},
	}
	for _, c := range stmts {
		t.Run(c.name, func(t *testing.T) {
			e := &velox.FakeExec{Affected: 1, Rows: [][]any{{int64(3)}}}
			db := velox.OpenFake(velox.Postgres, e)
			rec := &auditRec{}
			ctx := context.WithValue(context.Background(), auditKey{}, rec)

			text, err := c.sql(ctx, db)
			if err != nil {
				t.Fatalf("SQL(ctx): %v", err)
			}
			if !strings.HasPrefix(text, c.want) {
				t.Errorf("SQL(ctx) = %q, want %s...", text, c.want)
			}
			if w := sentWrites(e); len(w) != 0 {
				t.Errorf("a dry run sent %q", w)
			}
			if rec.writes != 1 || !errors.Is(rec.writeErr, velox.ErrDryRun) {
				t.Errorf("the hook's write: %d tried, error %v, want ErrDryRun", rec.writes, rec.writeErr)
			}
			if rec.reads != 1 || rec.readErr != nil {
				t.Errorf("the hook's read: %d, error %v", rec.reads, rec.readErr)
			}
			if !strings.Contains(strings.Join(e.Log, "\n"), `SELECT COUNT(*) FROM "dnotes"`) {
				t.Errorf("the hook's read was not sent: %q", e.Log)
			}

			// Sent for real, the same hook writes its row.
			e2 := &velox.FakeExec{Affected: 1, Rows: [][]any{{int64(3)}}}
			rec2 := &auditRec{}
			ctx2 := context.WithValue(context.Background(), auditKey{}, rec2)
			if err := c.exec(ctx2, velox.OpenFake(velox.Postgres, e2)); err != nil {
				t.Fatalf("Exec: %v", err)
			}
			if rec2.writeErr != nil || !strings.Contains(strings.Join(sentWrites(e2), "\n"), `INSERT INTO "dnotes"`) {
				t.Errorf("Exec did not write the audit row: %v, sent %q", rec2.writeErr, sentWrites(e2))
			}
		})
	}
}

// TestDryRunRefusesEveryWrite: under a dry-run context, Exec of an Insert,
// an InsertAll, an upsert, an Update and a Delete, Save, raw Exec and Tx fail with
// ErrDryRun, and nothing is sent, not even BEGIN.
func TestDryRunRefusesEveryWrite(t *testing.T) {
	e := &velox.FakeExec{Affected: 1, Rows: [][]any{{int64(1)}}}
	dry, db := dryContext(t, e)
	before := len(e.Log)
	owner := velox.StringCol[dnote, string]{I: 1}
	for name, run := range map[string]func() error{
		"Insert":    func() error { return db.Insert(&dnote{Owner: "a"}).Exec(dry) },
		"InsertAll": func() error { return db.InsertAll([]dnote{{Owner: "a"}, {Owner: "b"}}).Exec(dry) },
		"upsert": func() error {
			return db.Insert(&dnote{ID: 1, Owner: "a"}).OnConflict(dnoteID).Update(owner).Exec(dry)
		},
		"Update": func() error {
			_, err := db.Update[dnote]().Set(owner.Set("x")).Where(dnoteID.EQ(1)).Exec(dry)
			return err
		},
		"Delete": func() error {
			_, err := db.Delete[dnote]().Where(dnoteID.EQ(1)).Exec(dry)
			return err
		},
		"Save": func() error { return db.Save(&dnote{ID: 1, Owner: "x"}, owner).Exec(dry) },
		"raw Exec": func() error {
			_, err := db.Exec(dry, "test", "DELETE FROM dnotes")
			return err
		},
		"Tx": func() error {
			return db.Tx(dry, func(context.Context, *velox.DB) error {
				t.Error("fn of a Tx under a dry run ran")
				return nil
			})
		},
	} {
		err := run()
		if !errors.Is(err, velox.ErrDryRun) {
			t.Errorf("%s: %v, want ErrDryRun", name, err)
		}
		if err != nil && name != "Tx" && !strings.HasPrefix(err.Error(), "velox: ") {
			t.Errorf("%s: %q does not start velox: ", name, err)
		}
	}
	if len(e.Log) != before {
		t.Errorf("sent %q", e.Log[before:])
	}

	// In a transaction, a write under a dry-run context is refused as well,
	// and so is a savepoint.
	ctx := context.Background()
	err := db.Tx(ctx, func(_ context.Context, tx *velox.DB) error {
		if err := tx.Insert(&dnote{Owner: "a"}).Exec(dry); !errors.Is(err, velox.ErrDryRun) {
			t.Errorf("Insert in a transaction under a dry run: %v", err)
		}
		if err := tx.Tx(dry, func(context.Context, *velox.DB) error { return nil }); !errors.Is(err, velox.ErrDryRun) {
			t.Errorf("savepoint under a dry run: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if w := sentWrites(e); len(w) != 1 || w[0] != "BEGIN" {
		t.Errorf("sent %q, want the transaction's BEGIN only", w)
	}
}

// TestDryRunReadsRun: reads under a dry-run context run as normal, through
// every route a read has.
func TestDryRunReadsRun(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{{{int64(1), "ann"}}, {{int64(1), "ann"}}, {{int64(5)}}, {{int64(1), "ann"}}}}
	dry, db := dryContext(t, e)
	before := e.Calls

	rows, err := db.Query[dnote]().Limit(10).All(dry)
	if err != nil || len(rows) != 1 || rows[0].Owner != "ann" {
		t.Errorf("All: %v %v", rows, err)
	}
	one, err := db.Query[dnote]().Where(dnoteID.EQ(1)).One(dry)
	if err != nil || one.Owner != "ann" {
		t.Errorf("One: %v %v", one, err)
	}
	n, err := db.Query[dnote]().Count(dry)
	if err != nil || n != 5 {
		t.Errorf("Count: %d %v", n, err)
	}
	var got int
	for _, err := range db.Query[dnote]().Limit(10).Iter(dry) {
		if err != nil {
			t.Fatal(err)
		}
		got++
	}
	if got != 1 {
		t.Errorf("Iter read %d rows, want 1", got)
	}
	if e.Calls-before != 4 {
		t.Errorf("%d statements sent, want 4 reads", e.Calls-before)
	}
	if w := sentWrites(e); len(w) != 0 {
		t.Errorf("sent %q", w)
	}
}

// TestDryRunInsertAllTakesNoTransaction: an InsertAll that would run in a
// transaction of its own (more rows than one statement holds, or a
// conditioned upsert of several rows) fails under a dry run with ErrDryRun
// naming the entity, before any BEGIN.
func TestDryRunInsertAllTakesNoTransaction(t *testing.T) {
	e := &velox.FakeExec{Affected: 1, Rows: [][]any{{int64(1)}}}
	dry, db := dryContext(t, e)
	before := len(e.Log)

	restore := velox.SetMaxParams(velox.Postgres, 2) // one row per statement
	err := db.InsertAll([]dnote{{Owner: "a"}, {Owner: "b"}, {Owner: "c"}}).Exec(dry)
	restore()
	if !errors.Is(err, velox.ErrDryRun) || !strings.HasPrefix(err.Error(), "velox: velox_test.dnote: ") {
		t.Errorf("large InsertAll: %v", err)
	}

	// pnote has a Read and a Write policy, so its upsert's update branch is
	// conditioned.
	err = db.InsertAll([]pnote{{ID: 1, Owner: "a"}, {ID: 2, Owner: "b"}}).OnConflict(pn.ID).Update(pn.Owner).Exec(dry)
	if !errors.Is(err, velox.ErrDryRun) || !strings.HasPrefix(err.Error(), "velox: velox_test.pnote: ") {
		t.Errorf("conditioned upsert: %v", err)
	}
	if len(e.Log) != before {
		t.Errorf("sent %q", e.Log[before:])
	}
}

// A dhooked's hook does what its context's dhookFunc says around next.
type dhooked struct {
	ID    int64
	Owner string
}

type dhookKey struct{}

type dhookFunc func(ctx context.Context, m *velox.Mutation[dhooked], next velox.Mutator[dhooked]) error

func (*dhooked) Hooks() []velox.Hook[dhooked] {
	return []velox.Hook[dhooked]{func(next velox.Mutator[dhooked]) velox.Mutator[dhooked] {
		return velox.MutateFunc[dhooked](func(ctx context.Context, m *velox.Mutation[dhooked]) error {
			return ctx.Value(dhookKey{}).(dhookFunc)(ctx, m, next)
		})
	}}
}

var dhookedMeta velox.MetaOnce[dhooked]

func (*dhooked) VeloxMeta() *velox.Meta[dhooked] { return dhookedMeta.Load(dhookedDesc) }

func dhookedDesc() velox.Desc[dhooked] {
	return velox.Desc[dhooked]{
		Hooks:      func() velox.BoundHooks { return velox.BindHooks((*dhooked)(nil).Hooks()) },
		Table:      "dhooked",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Owner", Name: "owner"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr: func(x *dhooked, i int) any {
			if i == 0 {
				return &x.ID
			}
			return &x.Owner
		},
	}
}

// TestDryRunHookErrors: SQL(ctx) returns the statement when a hook's write
// after next is refused with ErrDryRun, and the hook's error in every other
// case: a write before next (nothing was rendered yet), an error of its own
// after next, and one that joins its own to ErrDryRun.
func TestDryRunHookErrors(t *testing.T) {
	boom := errors.New("boom")
	audit := func(ctx context.Context, m *velox.Mutation[dhooked]) error {
		return m.DB().Insert(&dnote{Owner: "audit"}).Exec(ctx)
	}
	for _, c := range []struct {
		name string
		hook dhookFunc
		want error // nil: SQL(ctx) succeeds
	}{
		{"write after next", func(ctx context.Context, m *velox.Mutation[dhooked], next velox.Mutator[dhooked]) error {
			if err := next.Mutate(ctx, m); err != nil {
				return err
			}
			return audit(ctx, m)
		}, nil},
		{"write before next", func(ctx context.Context, m *velox.Mutation[dhooked], next velox.Mutator[dhooked]) error {
			if err := audit(ctx, m); err != nil {
				return err
			}
			return next.Mutate(ctx, m)
		}, velox.ErrDryRun},
		{"own error after next", func(ctx context.Context, m *velox.Mutation[dhooked], next velox.Mutator[dhooked]) error {
			if err := next.Mutate(ctx, m); err != nil {
				return err
			}
			return boom
		}, boom},
		{"joined error after next", func(ctx context.Context, m *velox.Mutation[dhooked], next velox.Mutator[dhooked]) error {
			if err := next.Mutate(ctx, m); err != nil {
				return err
			}
			return errors.Join(audit(ctx, m), boom)
		}, boom},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := &velox.FakeExec{Affected: 1}
			db := velox.OpenFake(velox.Postgres, e)
			ctx := context.WithValue(context.Background(), dhookKey{}, c.hook)
			for name, sql := range map[string]func() (string, error){
				"Insert": func() (string, error) { s, _, err := db.Insert(&dhooked{Owner: "a"}).SQL(ctx); return s, err },
				"Update": func() (string, error) {
					s, _, err := db.Update[dhooked]().Set(velox.StringCol[dhooked, string]{I: 1}.Set("x")).
						Where(velox.NumberCol[dhooked, int64]{I: 0}.EQ(1)).SQL(ctx)
					return s, err
				},
			} {
				s, err := sql()
				switch {
				case c.want == nil && (err != nil || s == ""):
					t.Errorf("%s: %q, %v; want the statement", name, s, err)
				case c.want != nil && !errors.Is(err, c.want):
					t.Errorf("%s: %q, %v; want %v", name, s, err, c.want)
				}
			}
			if w := sentWrites(e); len(w) != 0 {
				t.Errorf("sent %q", w)
			}
		})
	}
}

// TestDryRunBeforeOutsideTx: a write that is both under a dry run and outside
// its context's transaction fails with ErrDryRun, with hooks (run) or without
// (plain) alike.
func TestDryRunBeforeOutsideTx(t *testing.T) {
	e := &velox.FakeExec{Affected: 1, Rows: [][]any{{int64(1)}}}
	var dry context.Context
	hooked := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(ctx context.Context, st *velox.Statement) error {
		if st.DryRun {
			dry = ctx
		}
		return nil
	}))
	plain := velox.OpenFake(velox.Postgres, e)
	err := hooked.Tx(context.Background(), func(ctx context.Context, tx *velox.DB) error {
		if _, _, err := tx.Query[dnote]().Limit(1).SQL(ctx); err != nil {
			return err
		}
		for name, db := range map[string]*velox.DB{"hooks": hooked, "no hooks": plain} {
			err := db.Insert(&dnote{Owner: "a"}).Exec(dry)
			if !errors.Is(err, velox.ErrDryRun) || errors.Is(err, velox.ErrOutsideTx) {
				t.Errorf("%s: %v, want ErrDryRun", name, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
