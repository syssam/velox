package velox_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestSQLCommentText(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithCallers(), velox.WithSQLComment("billing", "route", "missing"))
	c := velox.WithTags(ctx, "route", "GET /invoices", "user", "42")
	line := here() + 1
	if _, err := db.Query[testmodel.Invoice]().Columns(I.ID).Where(I.Number.EQ("x")).Limit(1).All(c); err != nil {
		t.Fatal(err)
	}
	want := `SELECT "id" FROM "invoices" WHERE "number" = $1 LIMIT $2 /*app='billing',caller='comment_test.go%3A` +
		strconv.Itoa(line) + `',route='GET%20%2Finvoices'*/`
	if e.SQL != want {
		t.Errorf("sent:\n%s\nwant:\n%s", e.SQL, want)
	}
	// SQL(ctx) returns the text that would be sent from its own call site.
	line = here() + 1
	text, _, err := db.Query[testmodel.Invoice]().Columns(I.ID).Where(I.Number.EQ("x")).Limit(1).SQL(c)
	if err != nil || !strings.HasSuffix(text, "%3A"+strconv.Itoa(line)+`',route='GET%20%2Finvoices'*/`) {
		t.Errorf("SQL(ctx): %s, %v", text, err)
	}
}

func TestSQLCommentWithoutCallers(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.SQLite, e, velox.WithSQLComment("billing"))
	if _, err := db.Query[testmodel.Invoice]().Columns(I.ID).Where(I.Number.EQ("x")).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id" FROM "invoices" WHERE "number" = ? LIMIT ? /*app='billing'*/`; e.SQL != want {
		t.Errorf("sent %s", e.SQL)
	}
}

// TestCommentLeavesTheFingerprint is Review Focus 4: the comment is not part
// of the shape. A Rewrite hook sees no comment, an Around hook the sent text.
func TestCommentLeavesTheFingerprint(t *testing.T) {
	var rewritten string
	var st velox.Statement
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithCallers(), velox.WithSQLComment("billing"),
		velox.WithRewrite(func(_ context.Context, s *velox.Statement) error { rewritten = s.SQL; return nil }),
		lastStatement(&st))
	if _, err := db.Query[testmodel.Invoice]().Columns(I.ID).Where(I.Number.EQ("x")).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if st.Fingerprint != "da95560fb97b3ada" || !strings.Contains(st.SQL, "/*app='billing',caller='comment_test.go%3A") || rewritten != "" {
		t.Errorf("fingerprint %s, sent %s, the Rewrite hook saw %q", st.Fingerprint, st.SQL, rewritten)
	}
}

// TestCommentCannotBeEscaped is Review Focus 1.
func TestCommentCannotBeEscaped(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.MySQL, e, velox.WithSQLComment("it's */ x", "route"))
	evil := velox.WithTags(ctx, "route", "*/ DROP TABLE invoices; /* ' \n é")
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(evil); err != nil {
		t.Fatal(err)
	}
	comment := e.SQL[strings.Index(e.SQL, " /*"):]
	if strings.Count(e.SQL, "/*") != 1 || strings.Count(e.SQL, "*/") != 1 || !strings.HasSuffix(e.SQL, "*/") {
		t.Errorf("the comment can be escaped: %s", e.SQL)
	}
	if want := ` /*app='it%27s%20%2A%2F%20x',route='%2A%2F%20DROP%20TABLE%20invoices%3B%20%2F%2A%20%27%20%0A%20%C3%A9'*/`; comment != want {
		t.Errorf("comment %s\nwant    %s", comment, want)
	}
}

// TestCommentIsSafeForConcurrentUse is Review Focus 2, under -race: each
// statement carries its own context's tag.
func TestCommentIsSafeForConcurrentUse(t *testing.T) {
	var mu sync.Mutex
	sent := map[string]string{}
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithCallers(), velox.WithSQLComment("billing", "route"),
		velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			mu.Lock()
			sent[st.Tags[0].Value] = st.SQL
			mu.Unlock()
			return next(ctx)
		}))
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Go(func() {
			for i := range 50 {
				v := strconv.Itoa(g*1000 + i%7)
				if _, err := db.Query[testmodel.Invoice]().Limit(1).All(velox.WithTags(ctx, "route", v)); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	for v, text := range sent {
		if !strings.HasSuffix(text, ",route='"+v+"'*/") {
			t.Errorf("route %s sent %s", v, text)
		}
	}
}

// TestSQLCommentPanics: an empty app, and a tag that would give the comment
// an empty or repeated key, are mistakes found at Open (Ruling 9).
func TestSQLCommentPanics(t *testing.T) {
	for _, args := range [][]string{{""}, {"billing", ""}, {"billing", "route", "route"}, {"billing", "app"}, {"billing", "caller"}} {
		func() {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(r.(string), "velox: WithSQLComment") {
					t.Errorf("WithSQLComment(%q): recovered %v", args, r)
				}
			}()
			velox.WithSQLComment(args[0], args[1:]...)
		}()
	}
}
