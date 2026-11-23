package apitest

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/schema"
)

// The entities of the untyped behaviour tests, written by hand in the shape
// velox-gen writes. ubDoc's Policy, Interceptors and Hooks delegate to the
// package's ub* variables, which a test sets with ubSet; its hooks come from
// two sources, a mixin's (ubMixinHooks, which fills org_id) and the entity's
// (ubHook). ubComment belongs to a doc through the linked relation "doc",
// and its Read is that the doc is visible. ubLoose names the relation "doc"
// too, but its relation is not linked. ubAudit is written by run-time hooks.

// ubKind is a named string type, as an enum's.
type ubKind string

type ubDoc struct {
	ID    int64
	OrgID int64
	Title string
	Kind  ubKind
	Size  int64
	Rank  int32
	Note  *string
}

type ubComment struct {
	ID    int64
	DocID int64
	Body  string
	Doc   velox.One[ubDoc]
}

type ubLoose struct {
	ID    int64
	DocID int64
	Doc   velox.One[ubDoc]
}

type ubAudit struct {
	ID   int64
	What string
}

var ubDocs = struct {
	ID    velox.NumberCol[ubDoc, int64]
	Title velox.StringCol[ubDoc, string]
}{velox.NumberCol[ubDoc, int64]{I: 0}, velox.StringCol[ubDoc, string]{I: 2}}

// The behaviour of ubDoc, which a test sets with ubSet. A nil Read, Write
// or Filter is Allow; a nil Insert accepts; a nil hook calls next.
var (
	ubRead   func(ctx context.Context) (velox.Predicate, error)
	ubWrite  func(ctx context.Context) (velox.Predicate, error)
	ubInsert func(ctx context.Context, r velox.Row) error
	ubFilter func(ctx context.Context) (velox.Predicate, error)
	ubHook   func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error
	ubTrace  []string
)

// ubSet runs f to set ubDoc's behaviour, and resets it when the test ends.
func ubSet(t *testing.T, f func()) {
	t.Helper()
	ubTrace = nil
	f()
	t.Cleanup(func() {
		ubRead, ubWrite, ubInsert, ubFilter, ubHook, ubTrace = nil, nil, nil, nil, nil, nil
	})
}

func ubAllow(f func(ctx context.Context) (velox.Predicate, error)) func(ctx context.Context) (velox.Predicate, error) {
	return func(ctx context.Context) (velox.Predicate, error) {
		if f == nil {
			return velox.Allow(), nil
		}
		return f(ctx)
	}
}

func (*ubDoc) Policy() velox.Policy {
	return velox.Policy{
		Read:  func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) { return ubAllow(ubRead)(ctx) },
		Write: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) { return ubAllow(ubWrite)(ctx) },
		Insert: func(ctx context.Context, _ *velox.DB, r velox.Row) error {
			if ubInsert == nil {
				return nil
			}
			return ubInsert(ctx, r)
		},
	}
}

func (*ubDoc) Interceptors() []velox.Interceptor {
	return []velox.Interceptor{{Filter: func(ctx context.Context) (velox.Predicate, error) { return ubAllow(ubFilter)(ctx) }}}
}

// ubMixinHooks is a tenancy mixin's Hooks: it fills org_id from the
// context's testmodel.Org, and records that it ran.
func ubMixinHooks() []velox.Hook {
	return []velox.Hook{func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			ubTrace = append(ubTrace, "mixin")
			if org, ok := testmodel.Org.From(ctx); ok && m.Op() == velox.OpInsert {
				if err := m.Fill("org_id", org); err != nil {
					return err
				}
			}
			return next.Mutate(ctx, m)
		})
	}}
}

func (*ubDoc) Hooks() []velox.Hook {
	return []velox.Hook{func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			ubTrace = append(ubTrace, "entity")
			if ubHook == nil {
				return next.Mutate(ctx, m)
			}
			return ubHook(ctx, m, next)
		})
	}}
}

var ubDocMeta velox.MetaOnce[ubDoc]

// Mixin lists the behaviour that runs before ubDoc's own.
func (*ubDoc) Mixin() []schema.Mixin {
	return fixture.Fns{Hooks: []func() []velox.Hook{ubMixinHooks}}.Mixin()
}

func (*ubDoc) VeloxMeta() *velox.Meta[ubDoc] {
	return ubDocMeta.Load(func() velox.Descriptor[ubDoc] {
		return velox.Descriptor[ubDoc]{
			Table: "ub_docs",
			Columns: []velox.ColumnDescriptor{
				{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true}, {Field: "Title", Name: "title"},
				{Field: "Kind", Name: "kind"}, {Field: "Size", Name: "size"}, {Field: "Rank", Name: "rank"}, {Field: "Note", Name: "note"},
			},
			PrimaryKey: []int{0},
			Schema:     (*ubDoc)(nil),
			FieldPtr: func(x *ubDoc, i int) any {
				return [...]any{&x.ID, &x.OrgID, &x.Title, &x.Kind, &x.Size, &x.Rank, &x.Note}[i]
			},
		}
	})
}

// Policy lets a comment be read only when its doc is: velox.HasEdge("doc")
// names the relation, whose target's Read policy applies inside the EXISTS.
func (*ubComment) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context, *velox.DB) (velox.Predicate, error) { return velox.HasEdge("doc"), nil }}
}

var ubCommentMeta velox.MetaOnce[ubComment]

func (*ubComment) VeloxMeta() *velox.Meta[ubComment] {
	return ubCommentMeta.Load(func() velox.Descriptor[ubComment] {
		return velox.Descriptor[ubComment]{
			Table:        "ub_comments",
			Columns:      []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "DocID", Name: "doc_id"}, {Field: "Body", Name: "body"}},
			PrimaryKey:   []int{0},
			Entities:     fixture.Entities(velox.LinkEntity[ubDoc]("ubDoc", "")),
			Edges:        []velox.EdgeDescriptor{velox.EdgeTo[ubDoc]("doc", 1, 0)},
			Schema:       (*ubComment)(nil),
			FieldPtr:     func(x *ubComment, i int) any { return [...]any{&x.ID, &x.DocID, &x.Body}[i] },
			EdgeFieldPtr: func(x *ubComment, i int) any { return &x.Doc },
		}
	})
}

// Policy names the relation "doc", which ubLoose's descriptor does not link.
func (*ubLoose) Policy() velox.Policy {
	return velox.Policy{Read: func(context.Context, *velox.DB) (velox.Predicate, error) { return velox.HasEdge("doc"), nil }}
}

var ubLooseMeta velox.MetaOnce[ubLoose]

func (*ubLoose) VeloxMeta() *velox.Meta[ubLoose] {
	return ubLooseMeta.Load(func() velox.Descriptor[ubLoose] {
		return velox.Descriptor[ubLoose]{
			Table:        "ub_comments",
			Columns:      []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "DocID", Name: "doc_id"}},
			PrimaryKey:   []int{0},
			Edges:        []velox.EdgeDescriptor{velox.EdgeTo[ubDoc]("doc", 1, 0)},
			Schema:       (*ubLoose)(nil),
			FieldPtr:     func(x *ubLoose, i int) any { return [...]any{&x.ID, &x.DocID}[i] },
			EdgeFieldPtr: func(x *ubLoose, i int) any { return &x.Doc },
		}
	})
}

var ubAuditMeta velox.MetaOnce[ubAudit]

func (*ubAudit) VeloxMeta() *velox.Meta[ubAudit] {
	return ubAuditMeta.Load(func() velox.Descriptor[ubAudit] {
		return velox.Descriptor[ubAudit]{
			Table:      "ub_audits",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "What", Name: "what"}},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *ubAudit, i int) any { return [...]any{&x.ID, &x.What}[i] },
		}
	})
}

// ubDB opens a SQLite database in a temporary file with the tables of the
// ub entities, and docs 1 "a" and 2 "secret" of org 7, with comments 1 on
// doc 1 and 2 on doc 2.
func ubDB(t *testing.T, opts ...velox.Option) *velox.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "ub.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	for _, s := range []string{
		`CREATE TABLE "ub_docs" ("id" integer PRIMARY KEY, "org_id" integer NOT NULL, "title" text NOT NULL, "kind" text NOT NULL,
			"size" integer NOT NULL, "rank" integer NOT NULL, "note" text)`,
		`CREATE TABLE "ub_comments" ("id" integer PRIMARY KEY, "doc_id" integer NOT NULL, "body" text NOT NULL DEFAULT '')`,
		`CREATE TABLE "ub_audits" ("id" integer PRIMARY KEY, "what" text NOT NULL)`,
		`INSERT INTO "ub_docs" VALUES (1, 7, 'a', 'memo', 10, 1, NULL), (2, 7, 'secret', 'memo', 20, 2, 'n')`,
		`INSERT INTO "ub_comments" VALUES (1, 1, 'on a'), (2, 2, 'on secret')`,
	} {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return velox.Open(velox.SQLite, raw, opts...)
}

// ubCount returns E's rows, read past every policy. ubDoc's interceptor
// still applies: a test that sets ubFilter does not count with it.
func ubCount[E any, PE velox.Entity[E]](t *testing.T, db *velox.DB) int64 {
	t.Helper()
	n, err := db.WithoutPolicy("count").Query[E, PE]().Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// ubTitles returns the titles of the docs the context sees, by id.
func ubTitles(ctx context.Context, t *testing.T, db *velox.DB) []string {
	t.Helper()
	ds, err := db.Query[ubDoc]().OrderBy(ubDocs.ID.Asc()).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, d := range ds {
		out = append(out, d.Title)
	}
	return out
}

func wantErrContains(t *testing.T, what string, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("%s: %v, want an error containing %q", what, err, want)
	}
}

// TestBeforeRunsBeforeNext: Before runs f, then next when f returned nil,
// for the ops it names: what f sets is what next writes, and an error from
// f refuses the write before anything is sent. With no op it runs on no
// write.
func TestBeforeRunsBeforeNext(t *testing.T) {
	var none int
	upper := velox.Before(func(_ context.Context, m *velox.Mutation) error {
		ubTrace = append(ubTrace, "before")
		for r := range m.Rows() {
			if r.Value("title") == "refuse" {
				return errors.New("refused by f")
			}
			title, _ := r.Value("title").(string)
			if err := r.Set("title", strings.ToUpper(title)); err != nil {
				return err
			}
		}
		return nil
	}, velox.OpInsert)
	never := velox.Before(func(context.Context, *velox.Mutation) error { none++; return nil })
	ubSet(t, func() {
		ubHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
			return upper(never(velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
				ubTrace = append(ubTrace, "next")
				return next.Mutate(ctx, m)
			}))).Mutate(ctx, m)
		}
	})
	db := ubDB(t)
	ctx := testmodel.Org.With(context.Background(), int64(7))
	d := ubDoc{ID: 3, Title: "low", Kind: "memo"}
	if err := db.Insert(&d).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if d.Title != "LOW" || !slices.Equal(ubTrace, []string{"mixin", "entity", "before", "next"}) {
		t.Errorf("title %q, trace %v", d.Title, ubTrace)
	}
	if got := ubTitles(ctx, t, db); !slices.Equal(got, []string{"a", "secret", "LOW"}) {
		t.Errorf("titles %v", got)
	}
	ubTrace = nil
	err := db.Insert(&ubDoc{ID: 4, Title: "refuse", Kind: "memo"}).Exec(ctx)
	wantErrContains(t, "f refuses", err, "refused by f")
	if slices.Contains(ubTrace, "next") || ubCount[ubDoc](t, db) != 3 {
		t.Errorf("after f refused: trace %v, %d rows", ubTrace, ubCount[ubDoc](t, db))
	}
	// An update is not among the ops: f does not run, and next does.
	ubTrace = nil
	if _, err := db.Update[ubDoc]().Set(ubDocs.Title.Set("x")).Where(ubDocs.ID.EQ(3)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(ubTrace, "before") || !slices.Contains(ubTrace, "next") {
		t.Errorf("update: trace %v", ubTrace)
	}
	if none != 0 {
		t.Errorf("Before with no op ran %d times", none)
	}
}

// TestInsertPolicySeesHookFill: the hooks run before the policy, so
// Policy.Insert sees the org_id that the mixin's hook filled, and a row
// that names another organization is refused.
func TestInsertPolicySeesHookFill(t *testing.T) {
	var seen []any
	ubSet(t, func() {
		ubInsert = func(ctx context.Context, r velox.Row) error {
			seen = append(seen, r.Value("org_id"))
			return testmodel.Org.Match(ctx, r.Value("org_id"))
		}
	})
	db := ubDB(t)
	ctx := testmodel.Org.With(context.Background(), int64(7))
	d := ubDoc{ID: 3, Title: "t", Kind: "memo"}
	if err := db.Insert(&d).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if d.OrgID != 7 || !slices.Equal(seen, []any{int64(7)}) {
		t.Errorf("OrgID %d, the policy saw %v", d.OrgID, seen)
	}
	err := db.Insert(&ubDoc{ID: 4, OrgID: 8, Title: "u", Kind: "memo"}).Exec(ctx)
	if err == nil || ubCount[ubDoc](t, db) != 3 {
		t.Errorf("a row of org 8: %v, %d rows", err, ubCount[ubDoc](t, db))
	}
}

// TestUnknownColumnFailsClosed: a name the entity has no column of fails
// the statement, from a policy, an interceptor and a hook, and nothing is
// sent.
func TestUnknownColumnFailsClosed(t *testing.T) {
	const want = `no column "nope"`
	nope := func(context.Context) (velox.Predicate, error) { return velox.FieldEQ("nope", 1), nil }
	for name, c := range map[string]func(){
		"policy Read":  func() { ubRead = nope },
		"policy Write": func() { ubWrite = nope },
		"interceptor":  func() { ubFilter = nope },
		"hook Where": func() {
			ubHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
				m.Where(velox.FieldEQ("nope", 1))
				return next.Mutate(ctx, m)
			}
		},
		"hook Set": func() {
			ubHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
				_ = m.Set("nope", 1)
				return next.Mutate(ctx, m)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			ubSet(t, c)
			db := ubDB(t)
			ctx := context.Background()
			if !strings.HasPrefix(name, "hook") && name != "policy Write" {
				_, err := db.Query[ubDoc]().Limit(1).All(ctx)
				wantErrContains(t, "query", err, want)
				if errors.Is(err, velox.ErrDenied) {
					t.Errorf("query: an unknown column is not a denial: %v", err)
				}
			}
			_, err := db.Update[ubDoc]().Set(ubDocs.Title.Set("x")).Where(ubDocs.ID.EQ(1)).Exec(ctx)
			wantErrContains(t, "update", err, want)
			ubFilter, ubHook = nil, nil // so that the check reads the rows
			if got := ubTitles(ctx, t, db.WithoutPolicy("check")); !slices.Equal(got, []string{"a", "secret"}) {
				t.Errorf("after the update: %v", got)
			}
		})
	}
	t.Run("row Value and Set", func(t *testing.T) {
		var verr error
		ubSet(t, func() {
			ubHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
				for r := range m.Rows() {
					verr = r.Set("nope", 1)
				}
				return next.Mutate(ctx, m)
			}
		})
		db := ubDB(t)
		err := db.Insert(&ubDoc{ID: 3, Title: "t", Kind: "memo"}).Exec(context.Background())
		wantErrContains(t, "Row.Set's error", verr, want)
		wantErrContains(t, "insert", err, want)
		if n := ubCount[ubDoc](t, db); n != 2 {
			t.Errorf("%d rows after the refused insert", n)
		}
	})
}

// TestCondValueConverted: a value of the column's own kind class is
// converted, an int for an int64 column, a string for a named string
// column and an int64 for an int32 one; one out of the column's range, or
// of another kind, fails the statement.
func TestCondValueConverted(t *testing.T) {
	db := ubDB(t)
	ctx := context.Background()
	for _, c := range []struct {
		cond velox.Predicate
		want []string
	}{
		{velox.FieldEQ("size", 10), []string{"a"}},
		{velox.FieldGT("size", int8(10)), []string{"secret"}},
		{velox.FieldEQ("kind", "memo"), []string{"a", "secret"}},
		{velox.FieldIn("kind", "memo", ubKind("x")), []string{"a", "secret"}},
		{velox.FieldEQ("rank", int64(2)), []string{"secret"}},
		{velox.FieldIn("rank", uint8(1), 2), []string{"a", "secret"}},
		{velox.FieldHasPrefix("title", "sec"), []string{"secret"}},
	} {
		ubSet(t, func() { ubFilter = func(context.Context) (velox.Predicate, error) { return c.cond, nil } })
		if got := ubTitles(ctx, t, db); !slices.Equal(got, c.want) {
			t.Errorf("%v: %v, want %v", c.cond, got, c.want)
		}
	}
	for _, c := range []struct {
		name string
		cond velox.Predicate
		want string
	}{
		{"out of range", velox.FieldEQ("rank", int64(math.MaxInt32)+1), "2147483648 for rank, an int32: value out of the column's range"},
		{"a string for an integer", velox.FieldEQ("size", "10"), "size is an int64, not a string"},
		{"an integer for a string", velox.FieldEQ("kind", 1), "kind is an apitest.ubKind, not an int"},
		{"a float for an integer", velox.FieldEQ("size", 1.5), "size is an int64, not a float64"},
	} {
		ubSet(t, func() { ubFilter = func(context.Context) (velox.Predicate, error) { return c.cond, nil } })
		_, err := db.Query[ubDoc]().Limit(1).All(ctx)
		wantErrContains(t, c.name, err, c.want)
	}
}

// TestEdgeHasAppliesTargetPolicy: velox.HasEdge("doc") in a comment's
// policy is an EXISTS on the linked target, and the doc's Read policy
// applies inside it: a comment on a doc the reader cannot see is hidden.
func TestEdgeHasAppliesTargetPolicy(t *testing.T) {
	ubSet(t, func() {
		ubRead = func(context.Context) (velox.Predicate, error) { return velox.FieldNEQ("title", "secret"), nil }
	})
	db := ubDB(t)
	ctx := context.Background()
	cs, err := db.Query[ubComment]().AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Body != "on a" {
		t.Errorf("comments %+v, want the one on a", cs)
	}
	q, _, err := db.Query[ubComment]().Limit(1).SQL(ctx)
	if err != nil || !strings.Contains(q, `EXISTS (SELECT 1 FROM "ub_docs"`) || !strings.Contains(q, `"title" <> ?`) {
		t.Errorf("SQL %s, %v", q, err)
	}
	// Under WithoutPolicy neither policy runs.
	if n, err := db.WithoutPolicy("test").Query[ubComment]().Count(ctx); n != 2 || err != nil {
		t.Errorf("WithoutPolicy: %d, %v", n, err)
	}
}

// TestEdgeNotLinkedFails: a condition by the name of a relation, on an
// entity whose descriptor has no entity table, fails its statement, and is
// not a denial.
func TestEdgeNotLinkedFails(t *testing.T) {
	db := ubDB(t)
	_, err := db.Query[ubLoose]().Limit(1).All(context.Background())
	wantErrContains(t, "query", err, "relation doc: apitest.ubLoose has no entity table")
	if errors.Is(err, velox.ErrDenied) {
		t.Errorf("an unlinked relation is not a denial: %v", err)
	}
	_, err = db.WithoutPolicy("test").Query[ubLoose]().Limit(1).All(context.Background())
	if err != nil {
		t.Errorf("WithoutPolicy, with no policy to resolve: %v", err)
	}
}

// TestRowValueNullable: Row.Value of a nullable column is nil for NULL and
// the value, not a pointer, otherwise; Row.Set of nil sets NULL.
func TestRowValueNullable(t *testing.T) {
	var seen []any
	ubSet(t, func() {
		ubInsert = func(_ context.Context, r velox.Row) error {
			seen = append(seen, r.Value("note"))
			return nil
		}
		ubHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
			for r := range m.Rows() {
				if r.Value("title") == "clear" {
					if err := r.Set("note", nil); err != nil {
						return err
					}
				}
			}
			return next.Mutate(ctx, m)
		}
	})
	db := ubDB(t)
	ctx := testmodel.Org.With(context.Background(), int64(7))
	note := "n"
	ds := []ubDoc{{ID: 3, Title: "x", Kind: "memo"}, {ID: 4, Title: "y", Kind: "memo", Note: &note}, {ID: 5, Title: "clear", Kind: "memo", Note: &note}}
	if err := db.InsertAll(ds).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, []any{nil, "n", nil}) {
		t.Errorf("Row.Value(note): %#v, want nil, \"n\", nil", seen)
	}
	got, err := db.Query[ubDoc]().Where(ubDocs.ID.EQ(5)).One(ctx)
	if err != nil || got.Note != nil {
		t.Errorf("after Set(note, nil): %+v, %v", got, err)
	}
}

// TestFillOnlyZero: Fill sets the column in each row that leaves it at its
// zero value, and leaves the others as the caller set them.
func TestFillOnlyZero(t *testing.T) {
	ubSet(t, func() {
		ubHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
			if err := m.Fill("title", "filled"); err != nil {
				return err
			}
			return next.Mutate(ctx, m)
		}
	})
	db := ubDB(t)
	ctx := testmodel.Org.With(context.Background(), int64(7))
	ds := []ubDoc{{ID: 3, Kind: "memo"}, {ID: 4, Title: "mine", Kind: "memo"}}
	if err := db.InsertAll(ds).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if ds[0].Title != "filled" || ds[1].Title != "mine" {
		t.Errorf("titles %q, %q", ds[0].Title, ds[1].Title)
	}
	if got := ubTitles(ctx, t, db); !slices.Equal(got, []string{"a", "secret", "filled", "mine"}) {
		t.Errorf("stored %v", got)
	}
	// Fill on an update is a misuse.
	_, err := db.Update[ubDoc]().Set(ubDocs.Title.Set("x")).Where(ubDocs.ID.EQ(3)).Exec(ctx)
	const fillMsg = `velox: apitest.ubDoc: hook: Fill("title") on an update or a delete: Fill sets an insert's rows; Set sets an update's assignment`
	if err == nil || err.Error() != fillMsg {
		t.Errorf("Fill on an update: %v, want %s", err, fillMsg)
	}
}

// TestSetOnDeleteIsMisuse: Mutation.Set on a delete is a misuse: it fails
// the delete, and nothing is deleted.
func TestSetOnDeleteIsMisuse(t *testing.T) {
	var serr error
	ubSet(t, func() {
		ubHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
			if m.Op() == velox.OpDelete {
				serr = m.Set("title", "x")
			}
			return next.Mutate(ctx, m)
		}
	})
	db := ubDB(t)
	_, err := db.Delete[ubDoc]().Where(ubDocs.ID.EQ(1)).Exec(context.Background())
	const msg = `velox: apitest.ubDoc: hook: Set("title") on a delete: a delete assigns no columns`
	if err == nil || err.Error() != msg || serr == nil {
		t.Errorf("delete: %v; Set returned %v; want %s", err, serr, msg)
	}
	if n := ubCount[ubDoc](t, db); n != 2 {
		t.Errorf("%d rows after the refused delete", n)
	}
}

// TestValueOnInsertIsMisuse: Mutation.Value on an insert is a misuse: it
// fails the insert, and nothing is inserted. An insert's values are read
// from Rows.
func TestValueOnInsertIsMisuse(t *testing.T) {
	var ok bool
	ubSet(t, func() {
		ubHook = func(ctx context.Context, m *velox.Mutation, next velox.Mutator) error {
			_, ok = m.Value("title")
			return next.Mutate(ctx, m)
		}
	})
	db := ubDB(t)
	err := db.Insert(&ubDoc{ID: 3, Title: "t", Kind: "memo"}).Exec(context.Background())
	const msg = `velox: apitest.ubDoc: hook: Value("title") on an insert: read an insert's values from its rows`
	if err == nil || err.Error() != msg || ok {
		t.Errorf("insert: %v, Value reported %v; want %s", err, ok, msg)
	}
	if n := ubCount[ubDoc](t, db); n != 2 {
		t.Errorf("%d rows after the refused insert", n)
	}
}

// TestRowSetInPolicyIsMisuse: a policy decides and does not change the
// row: Row.Set in Policy.Insert fails the insert, which is not a denial,
// and nothing is inserted.
func TestRowSetInPolicyIsMisuse(t *testing.T) {
	ubSet(t, func() {
		ubInsert = func(_ context.Context, r velox.Row) error {
			_ = r.Set("title", "changed")
			return nil
		}
	})
	db := ubDB(t)
	d := ubDoc{ID: 3, Title: "t", Kind: "memo"}
	err := db.Insert(&d).Exec(context.Background())
	const msg = `velox: apitest.ubDoc: policy: Set("title") in an Insert policy: a policy decides and does not change the row; change it in a hook`
	if err == nil || err.Error() != msg || errors.Is(err, velox.ErrDenied) {
		t.Errorf("insert: %v, want %s", err, msg)
	}
	if n := ubCount[ubDoc](t, db); n != 2 {
		t.Errorf("%d rows after the refused insert", n)
	}
}

// ubRunTime is a run-time hook that records that it ran, and writes an
// audit row on the DB the write runs on.
func ubRunTime(label string) velox.Hook {
	return func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			ubTrace = append(ubTrace, label)
			for r := range m.Rows() {
				d := r.Entity().(*ubDoc)
				if d.OrgID == 0 {
					return errors.New("the run-time hook ran before the mixin's Fill")
				}
			}
			if err := next.Mutate(ctx, m); err != nil {
				return err
			}
			return m.DB().Insert(&ubAudit{What: label + " " + m.Op().String()}).Exec(ctx)
		})
	}
}

// TestRunTimeHooksRunAfterSchemaHooks: WithHooks's hooks run after the
// schema's, the mixin's and the entity's, in the order given: they see
// what the mixin filled.
func TestRunTimeHooksRunAfterSchemaHooks(t *testing.T) {
	ubSet(t, func() {})
	db := ubDB(t, velox.WithHooks[ubDoc](ubRunTime("rt1"), ubRunTime("rt2")))
	ctx := testmodel.Org.With(context.Background(), int64(7))
	if err := db.Insert(&ubDoc{ID: 3, Title: "t", Kind: "memo"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if want := []string{"mixin", "entity", "rt1", "rt2"}; !slices.Equal(ubTrace, want) {
		t.Errorf("trace %v, want %v", ubTrace, want)
	}
	if n := ubCount[ubAudit](t, db); n != 2 {
		t.Errorf("%d audit rows, want 2", n)
	}
}

// TestRunTimeHooksInTx: a Tx of the DB runs the run-time hooks, and their
// m.DB() is the transaction's: a rollback takes the audit row with the
// write.
func TestRunTimeHooksInTx(t *testing.T) {
	ubSet(t, func() {})
	db := ubDB(t, velox.WithHooks[ubDoc](ubRunTime("rt")))
	ctx := testmodel.Org.With(context.Background(), int64(7))
	rollback := errors.New("roll back")
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		if err := tx.Insert(&ubDoc{ID: 3, Title: "t", Kind: "memo"}).Exec(ctx); err != nil {
			return err
		}
		if n := ubCount[ubAudit](t, tx); n != 1 {
			t.Errorf("inside the transaction: %d audit rows", n)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if !slices.Equal(ubTrace, []string{"mixin", "entity", "rt"}) {
		t.Errorf("trace %v", ubTrace)
	}
	if a, d := ubCount[ubAudit](t, db), ubCount[ubDoc](t, db); a != 0 || d != 2 {
		t.Errorf("after the rollback: %d audit rows, %d docs", a, d)
	}
	ubTrace = nil
	if err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := tx.Update[ubDoc]().Set(ubDocs.Title.Set("x")).Where(ubDocs.ID.EQ(1)).Exec(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ubTrace, []string{"mixin", "entity", "rt"}) || ubCount[ubAudit](t, db) != 1 {
		t.Errorf("a committed update: trace %v, %d audit rows", ubTrace, ubCount[ubAudit](t, db))
	}
}

// TestRunTimeHooksOnlyTheirEntity: WithHooks[ubDoc]'s hooks run on ubDoc's
// writes only, not on another entity's.
func TestRunTimeHooksOnlyTheirEntity(t *testing.T) {
	ubSet(t, func() {})
	db := ubDB(t, velox.WithHooks[ubDoc](ubRunTime("rt"))).WithoutPolicy("test")
	ctx := context.Background()
	if err := db.Insert(&ubComment{ID: 3, DocID: 1, Body: "c"}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Delete[ubComment]().All().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if len(ubTrace) != 0 || ubCount[ubAudit](t, db) != 0 {
		t.Errorf("a comment's writes ran %v, %d audit rows", ubTrace, ubCount[ubAudit](t, db))
	}
	if _, err := db.Delete[ubDoc]().Where(ubDocs.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(ubTrace, "rt") || ubCount[ubAudit](t, db) != 1 {
		t.Errorf("a doc's delete: trace %v, %d audit rows", ubTrace, ubCount[ubAudit](t, db))
	}
}
