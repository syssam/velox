package apitest

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/schema"
)

// pvDoc is an entity whose policy is written as for veloxe: a
// privacy.Policy with Query and Mutation rules, after a tenant mixin's
// velox Read and Insert.
type pvDoc struct {
	ID     int64
	OrgID  int64
	Owner  string
	Secret bool
	Title  string
}

var pvDocs = struct {
	ID    velox.NumberCol[pvDoc, int64]
	Owner velox.StringCol[pvDoc, string]
	Title velox.StringCol[pvDoc, string]
}{velox.NumberCol[pvDoc, int64]{I: 0}, velox.StringCol[pvDoc, string]{I: 2}, velox.StringCol[pvDoc, string]{I: 4}}

var pvDocMeta velox.MetaOnce[pvDoc]

func (*pvDoc) VeloxMeta() *velox.Meta[pvDoc] { return pvDocMeta.Load(pvDocDescriptor) }

func pvDocDescriptor() velox.Descriptor[pvDoc] {
	return velox.Descriptor[pvDoc]{
		Schema: (*pvDoc)(nil),
		Table:  "pv_docs",
		Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "OrgID", Name: "org_id", Immutable: true},
			{Field: "Owner", Name: "owner"}, {Field: "Secret", Name: "secret"}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		FieldPtr:   func(x *pvDoc, i int) any { return [...]any{&x.ID, &x.OrgID, &x.Owner, &x.Secret, &x.Title}[i] },
	}
}

// Mixin is the tenant's, a velox Policy: it applies to reads and writes.
func (*pvDoc) Mixin() []schema.Mixin {
	return fixture.Fns{Policy: []func() velox.Policy{fixture.OrgPolicy}, Hooks: []func() []velox.Hook{fixture.OrgHooks}}.Mixin()
}

// pvRoleKey is the context key of the caller's role in these tests.
type pvRoleKey struct{}

func pvAs(role, user string) context.Context {
	ctx := testmodel.Org.With(context.Background(), int64(1))
	ctx = testmodel.WithUser(ctx, user)
	return context.WithValue(ctx, pvRoleKey{}, role)
}

func pvRole(ctx context.Context) string { r, _ := ctx.Value(pvRoleKey{}).(string); return r }

// pvAfterAllow counts the runs of the rule after the one that allows an
// admin: an Allow ends the list, so an admin's reads never reach it.
var pvAfterAllow atomic.Int64

// Policy is shaped as nextapp's veloxe schemas write theirs.
func (*pvDoc) Policy() velox.Policy {
	return privacy.Policy{
		Query: privacy.QueryPolicy{
			privacy.ContextQueryMutationRule(func(ctx context.Context) error {
				if pvRole(ctx) == "banned" {
					return privacy.Denyf("banned")
				}
				return nil
			}),
			privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
				if pvRole(ctx) == "admin" {
					return privacy.Allow
				}
				f.Where(velox.FieldEQ("secret", false))
				return privacy.Skip
			}),
			privacy.QueryRuleFunc(func(ctx context.Context, q privacy.Query) error {
				pvAfterAllow.Add(1)
				cc, ok := q.Filter().(interface{ HasColumn(string) bool })
				if !ok || !cc.HasColumn("owner") || cc.HasColumn("nope") {
					return errors.New("the filter does not report the entity's columns")
				}
				if pvRole(ctx) == "own" {
					u := testmodel.UserFromContext(ctx)
					q.Filter().Where(velox.FieldEQ("owner", u))
				}
				return privacy.Skip
			}),
		},
		Mutation: privacy.MutationPolicy{
			privacy.OnMutationOperation(privacy.ContextQueryMutationRule(func(ctx context.Context) error {
				if pvRole(ctx) == "nodelete" {
					return privacy.Deny
				}
				return privacy.Skip
			}), velox.OpDelete),
			privacy.MutationRuleFunc(func(_ context.Context, m privacy.Mutation) error {
				if v, ok := m.Value("title"); ok && v == "forbidden" {
					return privacy.Denyf("title %v", v)
				}
				return privacy.Skip
			}),
			privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
				if pvRole(ctx) == "own" {
					u := testmodel.UserFromContext(ctx)
					f.Where(velox.FieldEQ("owner", u)) // ignored on an insert
				}
				return privacy.Skip
			}),
		},
	}
}

// pvDB opens an in-memory SQLite database holding docs 1 a (ann), 2 b (ann,
// secret) and 3 c (bob) of org 1, and 4 d (ann) of org 2.
func pvDB(t *testing.T) *velox.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	for _, s := range []string{
		`CREATE TABLE pv_docs (id INTEGER PRIMARY KEY, org_id INTEGER NOT NULL, owner TEXT NOT NULL, secret BOOLEAN NOT NULL, title TEXT NOT NULL)`,
		`INSERT INTO pv_docs VALUES (1, 1, 'ann', false, 'a'), (2, 1, 'ann', true, 'b'), (3, 1, 'bob', false, 'c'), (4, 2, 'ann', false, 'd')`,
	} {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return velox.Open(velox.SQLite, raw)
}

func pvTitles(ctx context.Context, t *testing.T, db *velox.DB) []string {
	t.Helper()
	docs, err := db.Query[pvDoc]().OrderBy(pvDocs.ID.Asc()).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range docs {
		out = append(out, d.Title)
	}
	return out
}

// Query rules narrow reads in order, after the mixin's Read: Skip goes on,
// Allow ends the list keeping no later rule's condition, and a Deny fails
// the read as a PolicyError that wraps it.
func TestPrivacyQueryRules(t *testing.T) {
	db := pvDB(t)
	for _, c := range []struct {
		role string
		want []string
	}{
		{"", []string{"a", "c"}},
		{"own", []string{"a"}},
		{"admin", []string{"a", "b", "c"}},
	} {
		if got := pvTitles(pvAs(c.role, "ann"), t, db); !slices.Equal(got, c.want) {
			t.Errorf("%q reads %v, want %v", c.role, got, c.want)
		}
	}
	before := pvAfterAllow.Load()
	pvTitles(pvAs("admin", "ann"), t, db)
	if n := pvAfterAllow.Load() - before; n != 0 {
		t.Errorf("the rule after Allow ran %d times", n)
	}
	if n, err := db.Query[pvDoc]().Count(pvAs("", "ann")); err != nil || n != 2 {
		t.Errorf("count: %d %v", n, err)
	}

	_, err := db.Query[pvDoc]().AllowUnbounded().All(pvAs("banned", "ann"))
	if pe, ok := errors.AsType[*velox.PolicyError](err); !ok || pe.Op != velox.OpSelect || !errors.Is(err, privacy.Deny) || !errors.Is(err, velox.ErrDenied) {
		t.Fatalf("banned: %v", err)
	}
	if want := "velox: apitest.pvDoc: select denied by policy: banned: velox/privacy: deny rule"; err.Error() != want {
		t.Errorf("banned: %q, want %q", err, want)
	}
}

// Query rules do not apply to writes, as veloxe's query policy did not; the
// mixin's Read does.
func TestPrivacyQueryRulesLeaveWrites(t *testing.T) {
	db := pvDB(t)
	n, err := db.Update[pvDoc]().Set(pvDocs.Title.Set("x")).Where(pvDocs.ID.GT(0)).Exec(pvAs("banned", "ann"))
	if err != nil || n != 3 {
		t.Fatalf("update by a caller the Query rules deny: %d %v, want the org's 3 rows", n, err)
	}
	n, err = db.Delete[pvDoc]().Where(pvDocs.Title.EQ("x")).Exec(pvAs("", "ann"))
	if err != nil || n != 3 {
		t.Fatalf("delete of the secret row too: %d %v", n, err)
	}
}

// DecisionContext replaces the rule lists: Allow lifts them, and not the
// mixin's Read; Deny refuses.
func TestPrivacyDecisionContext(t *testing.T) {
	db := pvDB(t)
	ctx := privacy.DecisionContext(pvAs("banned", "ann"), privacy.Allow)
	if got := pvTitles(ctx, t, db); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("under Allow: %v", got)
	}
	ctx = privacy.DecisionContext(pvAs("admin", "ann"), privacy.Deny)
	if _, err := db.Query[pvDoc]().AllowUnbounded().All(ctx); !errors.Is(err, privacy.Deny) {
		t.Errorf("under Deny: %v", err)
	}
	if _, err := db.Update[pvDoc]().Set(pvDocs.Title.Set("x")).Where(pvDocs.ID.EQ(1)).Exec(ctx); !errors.Is(err, privacy.Deny) {
		t.Errorf("update under Deny: %v", err)
	}
	if d, ok := privacy.DecisionFromContext(privacy.DecisionContext(context.Background(), privacy.Skip)); ok || d != nil {
		t.Errorf("Skip is kept: %v %v", d, ok)
	}
}

// Mutation rules see the write's kind and an inserted row; their conditions
// narrow updates and deletes and are ignored on an insert.
func TestPrivacyMutationRules(t *testing.T) {
	db := pvDB(t)
	if _, err := db.Delete[pvDoc]().Where(pvDocs.ID.EQ(1)).Exec(pvAs("nodelete", "ann")); !errors.Is(err, privacy.Deny) {
		t.Errorf("delete: %v", err)
	}
	if n, err := db.Update[pvDoc]().Set(pvDocs.Title.Set("y")).Where(pvDocs.ID.EQ(1)).Exec(pvAs("nodelete", "ann")); err != nil || n != 1 {
		t.Errorf("update by nodelete: %d %v", n, err)
	}
	if n, err := db.Update[pvDoc]().Set(pvDocs.Title.Set("z")).Where(pvDocs.ID.GT(0)).Exec(pvAs("own", "bob")); err != nil || n != 1 {
		t.Errorf("update narrowed to bob's rows: %d %v", n, err)
	}
	if n, err := db.Delete[pvDoc]().Where(pvDocs.ID.GT(0)).Exec(pvAs("own", "bob")); err != nil || n != 1 {
		t.Errorf("delete narrowed to bob's rows: %d %v", n, err)
	}
	doc := pvDoc{ID: 9, Owner: "ann", Title: "new"}
	if err := db.Insert(&doc).Exec(pvAs("own", "bob")); err != nil || doc.OrgID != 1 {
		t.Errorf("insert of another's row by own: %v %+v", err, doc)
	}
	bad := pvDoc{ID: 10, Owner: "ann", Title: "forbidden"}
	err := db.Insert(&bad).Exec(pvAs("", "ann"))
	if pe, ok := errors.AsType[*velox.PolicyError](err); !ok || pe.Op != velox.OpInsert || !errors.Is(err, privacy.Deny) {
		t.Errorf("insert of a forbidden title: %v", err)
	}
}
