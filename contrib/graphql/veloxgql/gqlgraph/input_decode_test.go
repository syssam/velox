package gqlgraph_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/mixin"
	"github.com/syssam/velox/scope"
)

// post is a hand-written entity scoped by its tenant column, as velox-gen
// would write it, for CheckExists: a key, a tenant and a title. Its Read policy hides the posts
// titled "hidden".
type post struct {
	ID     int64
	Tenant string
	Title  string
}

var posts = struct {
	ID     velox.NumberCol[post, int64]
	Tenant velox.StringCol[post, string]
	Title  velox.StringCol[post, string]
}{
	ID:     velox.NumberCol[post, int64]{I: 0},
	Tenant: velox.StringCol[post, string]{I: 1},
	Title:  velox.StringCol[post, string]{I: 2},
}

// tenantKey is the scope of posts: the tenant the context names.
var tenantKey = scope.NewKey[string]("tenant")

// TenantPolicy confines posts to the context's tenant, as a tenancy mixin
// would.
func (*post) TenantPolicy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Predicate, error) {
			t, err := tenantKey.Require(ctx)
			if err != nil {
				return velox.Predicate{}, err
			}
			return velox.FieldEQ("tenant", t), nil
		},
		Insert: func(ctx context.Context, r velox.Row) error {
			return tenantKey.Match(ctx, r.Value("tenant"))
		},
	}
}

func (*post) Policy() velox.Policy {
	return velox.Policy{
		Read: func(context.Context) (velox.Predicate, error) { return velox.FieldNEQ("title", "hidden"), nil },
	}
}

// Hooks fill the tenant of each inserted post from the context.
func (*post) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		if t, ok := tenantKey.From(ctx); ok {
			return m.Fill("tenant", t)
		}
		return nil
	}, velox.OpInsert)}
}

var postMeta velox.MetaOnce[post]

func (*post) VeloxMeta() *velox.Meta[post] { return postMeta.Load(postDescriptor) }

// postTenantMixin is post's TenantPolicy as a mixin, whose policy runs before post's own.
type postTenantMixin struct{ mixin.Schema }

func (postTenantMixin) Policy() velox.Policy { return (*post)(nil).TenantPolicy() }

// Mixin lists postTenantMixin.
func (*post) Mixin() []schema.Mixin { return []schema.Mixin{postTenantMixin{}} }

func postDescriptor() velox.Descriptor[post] {
	return velox.Descriptor[post]{
		Table:      "posts",
		Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Tenant", Name: "tenant", Immutable: true}, {Field: "Title", Name: "title"}},
		PrimaryKey: []int{0},
		Schema:     (*post)(nil),
		FieldPtr: func(x *post, i int) any {
			switch i {
			case 0:
				return &x.ID
			case 1:
				return &x.Tenant
			case 2:
				return &x.Title
			}
			return nil
		},
	}
}

// txRecord records how each transaction of a DB ended, and its statements:
// "insert" or "select", with " in tx" in a transaction.
type txRecord struct {
	mu    sync.Mutex
	txs   []string
	stmts []string
}

func (r *txRecord) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	s := st.Op.String()
	if st.InTx {
		s += " in tx"
	}
	r.mu.Lock()
	r.stmts = append(r.stmts, s)
	r.mu.Unlock()
	return next(ctx)
}

func (r *txRecord) txAround(ctx context.Context, tx *velox.TxInfo, next func(context.Context) error) error {
	defer func() {
		r.mu.Lock()
		r.txs = append(r.txs, tx.Outcome.String())
		r.mu.Unlock()
	}()
	return next(ctx)
}

func (r *txRecord) take() (txs, stmts []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	txs, stmts = r.txs, r.stmts
	r.txs, r.stmts = nil, nil
	return txs, stmts
}

// mutationDB opens an empty in-memory database with the people and posts
// tables, with hooks that record into the returned record.
func mutationDB(t *testing.T) (*velox.DB, *txRecord) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	return mutationDBOn(t, sqlDB)
}

// mutationDBOn is mutationDB on sqlDB, an in-memory database it closes when
// the test ends.
func mutationDBOn(t *testing.T, sqlDB *sql.DB) (*velox.DB, *txRecord) {
	t.Helper()
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	for _, ddl := range []string{
		`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, tenant TEXT NOT NULL, title TEXT NOT NULL)`,
	} {
		if _, err := sqlDB.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	r := &txRecord{}
	return velox.Open(velox.SQLite, sqlDB, velox.WithAround(r.around), velox.WithTxAround(r.txAround)), r
}

// codeOf is the code err carries for veloxgql.ErrorPresenter, "" for none.
func codeOf(err error) string {
	var c interface{ Extensions() map[string]any }
	if !errors.As(err, &c) {
		return ""
	}
	s, _ := c.Extensions()["code"].(string)
	return s
}

func TestDecodeID(t *testing.T) {
	const at = "CreatePostInput.authorID"
	user := veloxgql.EncodeID("User", int64(7))
	if k, err := gqlgraph.DecodeID[int64](at, user, "User"); err != nil || k != 7 {
		t.Errorf("DecodeID = %v, %v; want 7", k, err)
	}
	type userID int64
	if k, err := gqlgraph.DecodeIDAs(at, user, "User", func(k int64) userID { return userID(k) }); err != nil || k != 7 {
		t.Errorf("DecodeIDAs = %v, %v; want 7", k, err)
	}
	for _, c := range []struct {
		name, id string
		want     error
	}{
		{"malformed", "!", veloxgql.ErrBadID},
		{"another type", veloxgql.EncodeID("Post", int64(7)), veloxgql.ErrWrongIDType},
	} {
		_, err := gqlgraph.DecodeID[int64](at, c.id, "User")
		_, errAs := gqlgraph.DecodeIDAs(at, c.id, "User", func(k int64) userID { return userID(k) })
		for _, err := range []error{err, errAs} {
			if !errors.Is(err, c.want) {
				t.Errorf("%s: %v, want %v", c.name, err, c.want)
			}
			if code := codeOf(err); code != veloxgql.CodeBadUserInput {
				t.Errorf("%s: code %q, want BAD_USER_INPUT", c.name, code)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "veloxgql: "+at+": ") {
				t.Errorf("%s: %q does not start with the input field", c.name, err)
			}
		}
	}
}

// encodeUsers returns the IDs of the Users 1 to n.
func encodeUsers(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = veloxgql.EncodeID("User", int64(i+1))
	}
	return ids
}

// TestDecodeIDsCap: an ID list is capped by MaxIDs, before any ID is
// decoded.
func TestDecodeIDsCap(t *testing.T) {
	const at = "UpdatePostInput.addTagIDs"
	if k, err := gqlgraph.DecodeIDs[int64](at, encodeUsers(gqlgraph.MaxIDs), "User"); err != nil || len(k) != gqlgraph.MaxIDs {
		t.Errorf("MaxIDs IDs: %d keys, %v; want %d", len(k), err, gqlgraph.MaxIDs)
	}
	// One over, the last malformed: the cap is checked first.
	over := append(encodeUsers(gqlgraph.MaxIDs), "!")
	for name, err := range map[string]error{
		"DecodeIDs": func() error { _, err := gqlgraph.DecodeIDs[int64](at, over, "User"); return err }(),
		"DecodeIDsAs": func() error {
			_, err := gqlgraph.DecodeIDsAs(at, over, "User", func(k int64) int64 { return k })
			return err
		}(),
	} {
		if !errors.Is(err, veloxgql.ErrPageTooLarge) || errors.Is(err, veloxgql.ErrBadID) {
			t.Errorf("%s of MaxIDs+1 IDs: %v, want ErrPageTooLarge alone", name, err)
		}
		if code := codeOf(err); code != veloxgql.CodeBadUserInput {
			t.Errorf("%s of MaxIDs+1 IDs: code %q, want BAD_USER_INPUT", name, code)
		}
		if err != nil && !strings.HasPrefix(err.Error(), "veloxgql: "+at+": ") {
			t.Errorf("%s: %q does not start with the input field", name, err)
		}
	}

	// A bad ID in a list names its place in the list.
	_, err := gqlgraph.DecodeIDs[int64](at, []string{encodeUsers(1)[0], "!"}, "User")
	if !errors.Is(err, veloxgql.ErrBadID) || codeOf(err) != veloxgql.CodeBadUserInput || !strings.HasPrefix(err.Error(), "veloxgql: "+at+"[1]: ") {
		t.Errorf("a malformed ID in the list: %v, want BAD_USER_INPUT ErrBadID naming %s[1]", err, at)
	}
}

// TestDecodeIDsRepeats: a repeated ID is kept once, in the order of its
// first place; the keys are what a relation handle's Add takes.
func TestDecodeIDsRepeats(t *testing.T) {
	a, b := veloxgql.EncodeID("User", int64(1)), veloxgql.EncodeID("User", int64(2))
	k, err := gqlgraph.DecodeIDs[int64]("x", []string{a, b, a}, "User")
	if err != nil || !slices.Equal(k, []any{int64(1), int64(2)}) {
		t.Errorf("DecodeIDs = %v, %v; want [1 2]", k, err)
	}
	type userID int64
	v, err := gqlgraph.DecodeIDsAs("x", []string{b, a, b, b}, "User", func(k int64) userID { return userID(k) })
	if err != nil || !slices.Equal(v, []any{userID(2), userID(1)}) {
		t.Errorf("DecodeIDsAs = %v, %v; want [2 1]", v, err)
	}
	if k, err := gqlgraph.DecodeIDs[int64]("x", nil, "User"); err != nil || len(k) != 0 {
		t.Errorf("DecodeIDs of none = %v, %v; want none", k, err)
	}
}

// TestNotZero: only the field's zero value is refused, with BAD_USER_INPUT
// naming the input's field.
func TestNotZero(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		code string
	}{
		`""`:        {gqlgraph.NotZero("In.x", ""), "BAD_USER_INPUT"},
		"0":         {gqlgraph.NotZero("In.x", 0), "BAD_USER_INPUT"},
		"zero time": {gqlgraph.NotZero("In.x", time.Time{}), "BAD_USER_INPUT"},
		`"a"`:       {gqlgraph.NotZero("In.x", "a"), ""},
		"1":         {gqlgraph.NotZero("In.x", 1), ""},
	} {
		if got := codeOf(c.err); got != c.code {
			t.Errorf("NotZero(%s): code %q, want %q", name, got, c.code)
		}
	}
	if err := gqlgraph.NotZero("In.x", ""); err == nil || !strings.HasPrefix(err.Error(), "veloxgql: In.x: invalid value: ") {
		t.Errorf("NotZero names %v, want In.x", err)
	}
}

func TestFromJSON(t *testing.T) {
	type meta struct{ Tags []string }
	got, err := gqlgraph.FromJSON[meta]("CreatePostInput.meta", veloxgql.JSON(`{"Tags":["a"]}`))
	if err != nil || !slices.Equal(got.Tags, []string{"a"}) {
		t.Errorf("FromJSON = %v, %v", got, err)
	}
	_, err = gqlgraph.FromJSON[meta]("CreatePostInput.meta", veloxgql.JSON(`{"Tags":1}`))
	if codeOf(err) != veloxgql.CodeBadUserInput || !strings.HasPrefix(err.Error(), "veloxgql: CreatePostInput.meta: ") {
		t.Errorf("a JSON of another shape: %v, want BAD_USER_INPUT naming the field", err)
	}
	if strings.Contains(err.Error(), "gqlgraph_test") || strings.Contains(err.Error(), "meta.Tags") {
		t.Errorf("%q names a Go type", err)
	}
}
