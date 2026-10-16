package velox_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
)

// lzProject and lzTask: a task belongs to a project. lzTask's Policy and
// Hooks build a HasWhere on the task's own relation in their bodies, outside
// the functions they return, which ran inside the task's metadata build and
// deadlocked on its once before HasWhere was lazy.
type lzProject struct {
	ID   int64
	Done bool
}

var lzProjects = struct {
	ID   velox.NumberCol[lzProject, int64]
	Done velox.Col[lzProject, bool]
}{velox.NumberCol[lzProject, int64]{I: 0}, velox.Col[lzProject, bool]{I: 1}}

var lzProjectMeta velox.MetaOnce[lzProject]

func (*lzProject) VeloxMeta() *velox.Meta[lzProject] { return lzProjectMeta.Load(lzProjectDesc) }

func lzProjectDesc() velox.Desc[lzProject] {
	return velox.Desc[lzProject]{
		Table:      "lz_projects",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Done", Name: "done"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr:       func(x *lzProject, i int) any { return [...]any{&x.ID, &x.Done}[i] },
	}
}

type lzTask struct {
	ID        int64
	ProjectID int64
	Project   velox.One[lzProject]
}

var lzTasks = struct {
	ID      velox.NumberCol[lzTask, int64]
	Project velox.Rel[lzTask, lzProject]
}{velox.NumberCol[lzTask, int64]{I: 0}, velox.Rel[lzTask, lzProject]{I: 0}}

var lzTaskMeta velox.MetaOnce[lzTask]

func (*lzTask) VeloxMeta() *velox.Meta[lzTask] { return lzTaskMeta.Load(lzTaskDesc) }

func (*lzTask) Policy() velox.Policy[lzTask] {
	open := lzTasks.Project.HasWhere(lzProjects.Done.EQ(false)) // built in the body
	return velox.Policy[lzTask]{Read: func(context.Context) (velox.Pred[lzTask], error) { return open, nil }}
}

func (*lzTask) Hooks() []velox.Hook[lzTask] {
	hasProject := lzTasks.Project.Has() // built in the body
	return []velox.Hook[lzTask]{velox.On(func(next velox.Mutator[lzTask]) velox.Mutator[lzTask] {
		return velox.MutateFunc[lzTask](func(ctx context.Context, m *velox.Mutation[lzTask]) error {
			m.Where(hasProject)
			return next.Mutate(ctx, m)
		})
	}, velox.OpDelete)}
}

func lzTaskDesc() velox.Desc[lzTask] {
	return velox.Desc[lzTask]{
		Policy:     func() velox.BoundPolicy { return velox.BindPolicy((*lzTask)(nil).Policy()) },
		Hooks:      func() velox.BoundHooks { return velox.BindHooks((*lzTask)(nil).Hooks()) },
		Table:      "lz_tasks",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "ProjectID", Name: "project_id"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Relations:  []velox.Relation{velox.EdgeTo[lzProject]("Project", 1, 0)},
		Addr:       func(x *lzTask, i int) any { return [...]any{&x.ID, &x.ProjectID}[i] },
		RelAddr:    func(x *lzTask, i int) any { return &x.Project },
	}
}

// within runs f and fails the test when it does not return in time.
func within(t *testing.T, d time.Duration, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("still running after %v: the entity's metadata build waits on itself", d)
	}
}

func TestPolicyBodyMayBuildHas(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	within(t, 5*time.Second, func() {
		text, _, err := db.Query[lzTask]().Limit(1).SQL(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		want := `SELECT "id", "project_id" FROM "lz_tasks" WHERE EXISTS (SELECT 1 FROM "lz_projects" AS "vx1" WHERE "vx1"."id" = "lz_tasks"."project_id" AND "done" = $1) LIMIT $2`
		if text != want {
			t.Errorf("got  %s\nwant %s", text, want)
		}
		// The hook's predicate, built in the same body, is resolved too.
		text, _, err = db.Delete[lzTask]().Where(lzTasks.ID.EQ(1)).SQL(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		if n := strings.Count(text, "EXISTS"); n != 2 {
			t.Errorf("%d EXISTS in %s, want 2: the hook's and the policy's", n, text)
		}
	})
}

// lzLate points at an invalid entity, and counts the times its own metadata
// is built.
type lzLate struct {
	ID  int64
	Bad velox.One[badDesc]
}

var (
	lzLates     = struct{ Bad velox.Rel[lzLate, badDesc] }{velox.Rel[lzLate, badDesc]{I: 0}}
	lzLateMeta  velox.MetaOnce[lzLate]
	lzLateLoads int
)

func (*lzLate) VeloxMeta() *velox.Meta[lzLate] {
	return lzLateMeta.Load(func() velox.Desc[lzLate] {
		lzLateLoads++
		return velox.Desc[lzLate]{
			Table:      "lz_lates",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Relations:  []velox.Relation{velox.EdgeTo[badDesc]("Bad", 0, 0)},
			Addr:       func(x *lzLate, i int) any { return &x.ID },
			RelAddr:    func(x *lzLate, i int) any { return &x.Bad },
		}
	})
}

func TestHasWhereErrorAtStatement(t *testing.T) {
	// Building the predicate reads no metadata; the statement reports the
	// relation's error and sends nothing.
	le := &velox.FakeExec{}
	ldb := velox.OpenFake(velox.Postgres, le)
	lp := lzLates.Bad.HasWhere()
	if lzLateLoads != 0 {
		t.Fatalf("HasWhere built the owner's metadata (%d loads)", lzLateLoads)
	}
	const lwant = "velox: velox_test.lzLate.Bad: its target github.com/syssam/velox2_test.badDesc is invalid: velox_test.badDesc: the tenant names column 3, of 1"
	if _, err := ldb.Query[lzLate]().Where(lp).Limit(1).All(ctx); err == nil || err.Error() != lwant {
		t.Errorf("All: %v, want %s", err, lwant)
	}
	if le.Calls != 0 || lzLateLoads != 1 {
		t.Errorf("%d statements sent, %d loads", le.Calls, lzLateLoads)
	}

	// dBadThroughs.BadJoin's join entity is invalid; the predicate builds
	// without an error and the statement reports it, sending nothing.
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	p := dBadThroughs.BadJoin.HasWhere(dTags.Name.EQ("x"))
	const want = "velox: velox_test.dBadThrough.BadJoin: its join entity is invalid: velox_test.badDesc: the tenant names column 3, of 1"
	q := db.Query[dBadThrough]().Where(p).Limit(1)
	if _, err := q.All(ctx); err == nil || err.Error() != want {
		t.Errorf("All: %v, want %s", err, want)
	}
	if _, _, err := q.SQL(ctx); err == nil || err.Error() != want {
		t.Errorf("SQL: %v, want %s", err, want)
	}
	if _, err := db.Delete[dBadThrough]().Where(p).Exec(ctx); err == nil || err.Error() != want {
		t.Errorf("Delete: %v, want %s", err, want)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements were sent", e.Calls)
	}
}

// lzNote is a row with a hook that writes an audit row on the database the
// write runs on.
type lzNote struct {
	ID    int64
	Title string
}

type lzAudit struct {
	ID   int64
	Note string
}

var lzNoteMeta velox.MetaOnce[lzNote]

func (*lzNote) VeloxMeta() *velox.Meta[lzNote] { return lzNoteMeta.Load(lzNoteDesc) }

var lzSawDB []bool

func (*lzNote) Hooks() []velox.Hook[lzNote] {
	return []velox.Hook[lzNote]{func(next velox.Mutator[lzNote]) velox.Mutator[lzNote] {
		return velox.MutateFunc[lzNote](func(ctx context.Context, m *velox.Mutation[lzNote]) error {
			if err := next.Mutate(ctx, m); err != nil {
				return err
			}
			if m.Op() != velox.OpInsert {
				return nil
			}
			hdb, ok := velox.DBOf(ctx)
			lzSawDB = append(lzSawDB, ok)
			if !ok {
				return errors.New("DBOf: no DB in a hook")
			}
			for _, n := range m.Rows() {
				if err := hdb.Insert(&lzAudit{Note: n.Title}).Exec(ctx); err != nil {
					return err
				}
			}
			return nil
		})
	}}
}

func lzNoteDesc() velox.Desc[lzNote] {
	return velox.Desc[lzNote]{
		Hooks:      func() velox.BoundHooks { return velox.BindHooks((*lzNote)(nil).Hooks()) },
		Table:      "lz_notes",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr:       func(x *lzNote, i int) any { return [...]any{&x.ID, &x.Title}[i] },
	}
}

var lzAuditMeta velox.MetaOnce[lzAudit]

func (*lzAudit) VeloxMeta() *velox.Meta[lzAudit] { return lzAuditMeta.Load(lzAuditDesc) }

func lzAuditDesc() velox.Desc[lzAudit] {
	return velox.Desc[lzAudit]{
		Table:      "lz_audits",
		Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "Note", Name: "note"}},
		PrimaryKey: []int{0},
		Tenant:     -1,
		Addr:       func(x *lzAudit, i int) any { return [...]any{&x.ID, &x.Note}[i] },
	}
}

func TestDBOfIsTheTransaction(t *testing.T) {
	if _, ok := velox.DBOf(context.Background()); ok {
		t.Fatal("DBOf outside a hook is ok")
	}
	raw, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	// One connection, so that a hook which wrote on the outer DB instead of
	// the transaction would wait for the transaction forever.
	raw.SetMaxOpenConns(1)
	for _, ddl := range []string{
		`CREATE TABLE lz_notes (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL)`,
		`CREATE TABLE lz_audits (id INTEGER PRIMARY KEY AUTOINCREMENT, note TEXT NOT NULL)`,
	} {
		if _, err := raw.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	db := velox.Open(velox.SQLite, raw)
	count := func(table string) int {
		t.Helper()
		var n int
		if err := raw.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	lzSawDB = nil
	within(t, 10*time.Second, func() {
		errRollback := errors.New("rollback")
		err := db.Tx(ctx, func(tx *velox.DB) error {
			if err := tx.Insert(&lzNote{Title: "a"}).Exec(ctx); err != nil {
				return err
			}
			return errRollback
		})
		if !errors.Is(err, errRollback) {
			t.Errorf("Tx: %v", err)
		}
		if n, a := count("lz_notes"), count("lz_audits"); n != 0 || a != 0 {
			t.Errorf("after the rollback: %d notes, %d audits, want none", n, a)
		}
		if err := db.Insert(&lzNote{Title: "b"}).Exec(ctx); err != nil {
			t.Errorf("Insert: %v", err)
		}
		if n, a := count("lz_notes"), count("lz_audits"); n != 1 || a != 1 {
			t.Errorf("outside a transaction: %d notes, %d audits, want 1 each", n, a)
		}
	})
	if len(lzSawDB) != 2 {
		t.Errorf("the hook ran %d times, want 2", len(lzSawDB))
	}
}
