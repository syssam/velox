package apitest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// logged returns a logger at level writing JSON lines to a buffer, and a
// function that decodes the records written so far.
func logged(t *testing.T, level slog.Level) (*slog.Logger, func() []map[string]any, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level}))
	return l, func() []map[string]any {
		var out []map[string]any
		for line := range strings.Lines(buf.String()) {
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatal(err)
			}
			out = append(out, m)
		}
		return out
	}, &buf
}

func argsOf(t *testing.T, rec map[string]any) []any {
	t.Helper()
	a, ok := rec["args"].([]any)
	if !ok {
		t.Fatalf("no args in %v", rec)
	}
	return a
}

var DA = fixture.DAuthors

// TestWithLogWithoutArgs: each statement is logged at Debug with its SQL,
// duration and rows, and no arguments; a failed one with its error,
// redacted.
func TestWithLogWithoutArgs(t *testing.T) {
	l, records, _ := logged(t, slog.LevelDebug)
	e := &fakeconn.Exec{Affected: 2}
	db := openFake(velox.Postgres, e, velox.WithLog(l))
	if _, err := db.Update[fixture.DAuthor]().Set(DA.Name.Set("ada")).Where(DA.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	e.Err = errors.New("db down")
	if _, err := db.Query[fixture.DAuthor]().Limit(1).All(ctx); err == nil {
		t.Fatal("no error")
	}
	rs := records()
	if len(rs) != 2 {
		t.Fatalf("%d records: %v", len(rs), rs)
	}
	r := rs[0]
	if r["level"] != "DEBUG" || r["msg"] != "velox: statement" || r["sql"] != `UPDATE "authors" SET "name" = $1 WHERE "id" = $2` || r["rows"] != float64(2) || r["duration"] == nil {
		t.Errorf("update: %v", r)
	}
	if _, ok := r["args"]; ok {
		t.Errorf("args without LogArgs: %v", r)
	}
	if _, ok := r["err"]; ok {
		t.Errorf("err on success: %v", r)
	}
	// Without LogArgs a failed statement's error text is not printed: it
	// may hold a value.
	if got := rs[1]["err"].(string); strings.Contains(got, "db down") || !strings.Contains(got, "the message is hidden") {
		t.Errorf("failed read: %v", rs[1])
	}
}

// TestWithLogNeverPrintsASensitiveArgument: with LogArgs, a Sensitive
// column's value is "<sensitive>" wherever it is compared, assigned or
// inserted, in a subquery on another entity too; an Expr's bound value and
// raw SQL's arguments are "<hidden>"; the rest are shown.
func TestWithLogNeverPrintsASensitiveArgument(t *testing.T) {
	l, records, buf := logged(t, slog.LevelDebug)
	e := &fakeconn.Exec{Affected: 1}
	db := openFake(velox.Postgres, e, velox.WithLog(l, velox.LogArgs()))

	if _, err := db.Update[fixture.DAuthor]().Set(DA.Email.Set("new-secret@x")).Where(DA.Email.EQ("old-secret@x"), DA.Name.In("ada", "bo")).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Insert(&fixture.DAuthor{OrgID: 7, Name: "cy", Email: new("ins-secret@x")}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query[fixture.DBook]().Where(fixture.DBooks.Author.HasWith(DA.Email.EQ("sub-secret@x")), fixture.DBooks.Title.EQ("t")).Limit(3).All(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Query[fixture.DAuthor]().Where(velox.Expr[fixture.DAuthor]("? = ?", DA.Email, "expr-secret@x")).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "test", "UPDATE authors SET email = ? WHERE id = ?", "raw-secret@x", 1); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "secret") {
		t.Fatalf("a secret was logged:\n%s", buf)
	}
	rs := records()
	if len(rs) != 5 {
		t.Fatalf("%d records", len(rs))
	}
	check := func(i int, want ...any) {
		t.Helper()
		got := argsOf(t, rs[i])
		if !slices.EqualFunc(got, want, func(a, b any) bool { return a == b }) {
			t.Errorf("record %d (%s):\n got %v\nwant %v", i, rs[i]["sql"], got, want)
		}
	}
	check(0, "<sensitive>", "<sensitive>", "ada", "bo")
	ins := argsOf(t, rs[1])
	if !slices.Contains(ins, any("<sensitive>")) || !slices.Contains(ins, any("cy")) || !slices.Contains(ins, any(float64(7))) {
		t.Errorf("insert: %v", ins)
	}
	check(2, "<sensitive>", "t", float64(3))
	check(3, "<hidden>", float64(1))
	check(4, "<hidden>", "<hidden>")
}

// TestWithLogHidesArgumentsAHookChanged: an Around hook given before
// WithLog that changes the arguments makes them all "<hidden>": velox can
// no longer tie them to columns.
func TestWithLogHidesArgumentsAHookChanged(t *testing.T) {
	l, records, _ := logged(t, slog.LevelDebug)
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		st.Args = slices.Clone(st.Args)
		st.Args[0] = "changed"
		return next(ctx)
	}), velox.WithLog(l, velox.LogArgs()))
	if _, err := db.Query[fixture.DAuthor]().Where(DA.Name.EQ("ada")).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if got := argsOf(t, records()[0]); !slices.Equal(got, []any{"<hidden>", "<hidden>"}) {
		t.Errorf("got %v", got)
	}
}

// TestWithLogOffBelowDebug: a logger that does not enable Debug gets no
// record, and the statement runs.
func TestWithLogOffBelowDebug(t *testing.T) {
	l, records, _ := logged(t, slog.LevelInfo)
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, velox.WithLog(l, velox.LogArgs()))
	if _, err := db.Query[fixture.DAuthor]().Limit(1).All(ctx); err != nil || e.Calls != 1 {
		t.Fatalf("%v, %d calls", err, e.Calls)
	}
	if rs := records(); len(rs) != 0 {
		t.Errorf("logged %v", rs)
	}
}

// TestWithLogMasksOnEveryDialect: the masking is the renderer's, so it
// holds for each dialect's text, MySQL's and SQLite's JSON paths included.
func TestWithLogMasksOnEveryDialect(t *testing.T) {
	for _, d := range []velox.Dialect{velox.MySQL, velox.SQLite} {
		l, records, buf := logged(t, slog.LevelDebug)
		e := &fakeconn.Exec{Affected: 1}
		db := openFake(d, e, velox.WithLog(l, velox.LogArgs()))
		if _, err := db.Update[fixture.DAuthor]().Set(DA.Email.Set("new-secret@x")).Where(DA.Email.EQ("old-secret@x"), DA.Name.EQ("ada")).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Query[fixture.DBook]().Where(fixture.DBooks.Author.HasWith(DA.Email.In("a-secret", "b-secret"))).Limit(3).All(ctx); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), "secret") {
			t.Fatalf("%v: a secret was logged:\n%s", d, buf)
		}
		if got := argsOf(t, records()[0]); !slices.Equal(got, []any{"<sensitive>", "<sensitive>", "ada"}) {
			t.Errorf("%v: %v", d, got)
		}
	}
}

// TestWithLogRedactsDriverErrors: a driver's message can echo a value, as
// MySQL's 1062 and PostgreSQL's 22P02 do. Without LogArgs, or when an
// argument is Sensitive or hidden, the record's err names the constraint or
// the error's types and code, never the message. With LogArgs and every
// argument shown, it is the error's text.
func TestWithLogRedactsDriverErrors(t *testing.T) {
	dup := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry 'dup-secret@x' for key 'authors.email'"}
	bad := &pgconn.PgError{Code: "22P02", Message: `invalid input syntax for type uuid: "pg-secret"`}
	for _, c := range []struct {
		name string
		d    velox.Dialect
		err  error
		args bool
		run  func(db *velox.DB) error
		want string // in err
	}{
		{"mysql insert, no LogArgs", velox.MySQL, dup, false, func(db *velox.DB) error {
			return db.Insert(&fixture.DAuthor{OrgID: 7, Name: "cy", Email: new("dup-secret@x")}).Exec(ctx)
		}, "velox: fixture.DAuthor: unique constraint email violated (code 1062); the message is hidden"},
		{"mysql insert, a Sensitive argument", velox.MySQL, dup, true, func(db *velox.DB) error {
			return db.Insert(&fixture.DAuthor{OrgID: 7, Name: "cy", Email: new("dup-secret@x")}).Exec(ctx)
		}, "unique constraint email violated (code 1062)"},
		{"pg read, no LogArgs", velox.Postgres, bad, false, func(db *velox.DB) error {
			_, err := db.Query[fixture.DAuthor]().Where(DA.Name.EQ("pg-secret")).Limit(1).All(ctx)
			return err
		}, "*pgconn.PgError (code 22P02); the message is hidden"},
		{"pg read, an Expr argument", velox.Postgres, bad, true, func(db *velox.DB) error {
			_, err := db.Query[fixture.DAuthor]().Where(velox.Expr[fixture.DAuthor]("? = ?", DA.Name, "pg-secret")).Limit(1).All(ctx)
			return err
		}, "(code 22P02); the message is hidden"},
	} {
		l, records, buf := logged(t, slog.LevelDebug)
		e := &fakeconn.Exec{Err: c.err}
		var opts []velox.LogOption
		if c.args {
			opts = append(opts, velox.LogArgs())
		}
		db := openFake(c.d, e, velox.WithLog(l, opts...))
		if err := c.run(db); err == nil {
			t.Fatalf("%s: no error", c.name)
		}
		if strings.Contains(buf.String(), "secret") {
			t.Errorf("%s: a value was logged:\n%s", c.name, buf)
		}
		if got, _ := records()[0]["err"].(string); !strings.Contains(got, c.want) {
			t.Errorf("%s: err %q, want %q in it", c.name, got, c.want)
		}
	}

	// Every argument shown: the full text.
	l, records, _ := logged(t, slog.LevelDebug)
	db := openFake(velox.Postgres, &fakeconn.Exec{Err: bad}, velox.WithLog(l, velox.LogArgs()))
	if _, err := db.Query[fixture.DAuthor]().Where(DA.Name.EQ("shown")).Limit(1).All(ctx); err == nil {
		t.Fatal("no error")
	}
	if got, _ := records()[0]["err"].(string); !strings.Contains(got, "invalid input syntax") {
		t.Errorf("every argument shown: err %q", got)
	}
}
