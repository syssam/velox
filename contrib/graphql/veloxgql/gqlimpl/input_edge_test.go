package gqlimpl_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

// acct, kid, lbl and acctLbl are hand-written entities, as velox-gen would
// write them, for the tests of Create and Update with every kind of edge: an
// acct has kids (an edge to many whose key, acct_id, is on the kid's rows,
// which the tests also use as a one-to-one edge, pet), and lbls through
// acctLbl (many-to-many); a kid's acct is the inverse, whose key is on the
// kid's row. A kid titled "hidden" is one the caller may not read, and one
// titled "locked" one it may read but not change; a lbl named "hidden" is one
// it may not read. An acct's name may not be empty, nor its age negative.
type acct struct {
	ID   int64
	Name string
	Age  int64
}

var accts = struct {
	ID   velox.NumberCol[acct, int64]
	Name velox.StringCol[acct, string]
	Age  velox.NumberCol[acct, int64]
}{velox.NumberCol[acct, int64]{I: 0}, velox.StringCol[acct, string]{I: 1}, velox.NumberCol[acct, int64]{I: 2}}

var acctMeta velox.MetaOnce[acct]

func (*acct) VeloxMeta() *velox.Meta[acct] {
	return acctMeta.Load(func() velox.Descriptor[acct] {
		return velox.Descriptor[acct]{
			Table:      "accts",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}, {Field: "Age", Name: "age"}},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *acct, i int) any { return [...]any{&x.ID, &x.Name, &x.Age}[i] },
			Check: func(col int, v any) error {
				switch {
				case col == 1 && v.(string) == "":
					return errors.New("must not be empty")
				case col == 2 && v.(int64) < 0:
					return errors.New("must not be negative")
				}
				return nil
			},
		}
	})
}

type kid struct {
	ID     int64
	AcctID *int64
	Title  string
}

var kids = struct {
	ID     velox.NumberCol[kid, int64]
	AcctID velox.NullNumberCol[kid, int64]
	Title  velox.StringCol[kid, string]
}{velox.NumberCol[kid, int64]{I: 0}, velox.NullNumberCol[kid, int64]{I: 1}, velox.StringCol[kid, string]{I: 2}}

var kidMeta velox.MetaOnce[kid]

func (*kid) VeloxMeta() *velox.Meta[kid] {
	return kidMeta.Load(func() velox.Descriptor[kid] {
		return velox.Descriptor[kid]{
			Table:      "kids",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "AcctID", Name: "acct_id"}, {Field: "Title", Name: "title"}},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *kid, i int) any { return [...]any{&x.ID, &x.AcctID, &x.Title}[i] },
			Policy: []func() velox.Policy{func() velox.Policy {
				return velox.Policy{
					Read:  func(context.Context) (velox.Cond, error) { return velox.C("title").NEQ("hidden"), nil },
					Write: func(context.Context) (velox.Cond, error) { return velox.C("title").NEQ("locked"), nil },
				}
			}},
		}
	})
}

type lbl struct {
	ID   int64
	Name string
}

var lbls = struct {
	ID   velox.NumberCol[lbl, int64]
	Name velox.StringCol[lbl, string]
}{velox.NumberCol[lbl, int64]{I: 0}, velox.StringCol[lbl, string]{I: 1}}

var lblMeta velox.MetaOnce[lbl]

func (*lbl) VeloxMeta() *velox.Meta[lbl] {
	return lblMeta.Load(func() velox.Descriptor[lbl] {
		return velox.Descriptor[lbl]{
			Table:      "lbls",
			Columns:    []velox.ColumnDescriptor{{Field: "ID", Name: "id"}, {Field: "Name", Name: "name"}},
			PrimaryKey: []int{0},
			FieldPtr:   func(x *lbl, i int) any { return [...]any{&x.ID, &x.Name}[i] },
			Policy: []func() velox.Policy{func() velox.Policy {
				return velox.Policy{
					Read: func(context.Context) (velox.Cond, error) { return velox.C("name").NEQ("hidden"), nil },
				}
			}},
		}
	})
}

type acctLbl struct {
	AcctID int64
	LblID  int64
}

var acctLbls = struct {
	AcctID velox.NumberCol[acctLbl, int64]
	LblID  velox.NumberCol[acctLbl, int64]
}{velox.NumberCol[acctLbl, int64]{I: 0}, velox.NumberCol[acctLbl, int64]{I: 1}}

var acctLblMeta velox.MetaOnce[acctLbl]

func (*acctLbl) VeloxMeta() *velox.Meta[acctLbl] {
	return acctLblMeta.Load(func() velox.Descriptor[acctLbl] {
		return velox.Descriptor[acctLbl]{
			Table:      "acct_lbls",
			Columns:    []velox.ColumnDescriptor{{Field: "AcctID", Name: "acct_id"}, {Field: "LblID", Name: "lbl_id"}},
			PrimaryKey: []int{0, 1},
			FieldPtr:   func(x *acctLbl, i int) any { return [...]any{&x.AcctID, &x.LblID}[i] },
		}
	})
}

// The edges' descriptors and the inputs' field names, as velox-gen writes
// them.
func acctKidsEdgeGQL() gqlimpl.Owned {
	return gqlimpl.Owned{Owner: velox.TableOf[acct](), Target: velox.TableOf[kid](),
		OwnerKey: int(accts.ID.I), TargetKey: int(kids.ID.I), FK: int(kids.AcctID.I)}
}

func acctLblsEdgeGQL() gqlimpl.M2M {
	return gqlimpl.M2M{Owner: velox.TableOf[acct](), Target: velox.TableOf[lbl](), Join: velox.TableOf[acctLbl](),
		OwnerKey: int(accts.ID.I), TargetKey: int(lbls.ID.I), JoinOwner: int(acctLbls.AcctID.I), JoinTarget: int(acctLbls.LblID.I)}
}

func acctInputGQL(goField string) string {
	switch goField {
	case "Name":
		return "name"
	case "Age":
		return "age"
	}
	return ""
}

func kidInputGQL(goField string) string {
	if goField == "Title" {
		return "title"
	}
	return ""
}

// createAcctInput is CreateAcctInput, with the edges kids (Repoint) and lbls
// (Join).
type createAcctInput struct {
	Name   string
	Age    *int64
	KidIDs []string
	LblIDs []string
}

func (in *createAcctInput) Create(ctx context.Context, x *acct) (*acct, error) {
	if x == nil {
		x = new(acct)
	}
	c := gqlimpl.NewCreate(ctx, gqlimpl.InputConn[acct]("Acct", 0, acctInputGQL), x, int(accts.Name.I))
	x.Name = in.Name
	if in.Age != nil {
		x.Age = *in.Age
		c.Given(int(accts.Age.I))
	}
	kidIDsKeys := gqlimpl.Keys[int64](c, "kidIDs", in.KidIDs, "Kid")
	gqlimpl.Repoint(c, "kidIDs", "Kid", acctKidsEdgeGQL(), kidIDsKeys)
	lblIDsKeys := gqlimpl.Keys[int64](c, "lblIDs", in.LblIDs, "Lbl")
	gqlimpl.Join(c, "lblIDs", "Lbl", acctLblsEdgeGQL(), lblIDsKeys)
	if err := c.Insert(); err != nil {
		return nil, err
	}
	return x, nil
}

// createKidInput is CreateKidInput, with the edge acct, whose key is on the
// kid's row.
type createKidInput struct {
	Title  string
	AcctID *string
}

func (in *createKidInput) Create(ctx context.Context, x *kid) (*kid, error) {
	if x == nil {
		x = new(kid)
	}
	c := gqlimpl.NewCreate(ctx, gqlimpl.InputConn[kid]("Kid", 0, kidInputGQL), x, int(kids.Title.I))
	x.Title = in.Title
	if k := gqlimpl.OptionalKey[int64](c, "acctID", in.AcctID, "Acct"); k != nil {
		gqlimpl.Exists(c, "acctID", "Acct", velox.TableOf[acct](), int(accts.ID.I), []int64{*k})
		x.AcctID = k
	}
	if err := c.Insert(); err != nil {
		return nil, err
	}
	return x, nil
}

// updateAcctInput is UpdateAcctInput, with the edges kids (Children), pet
// (Child, on the same key) and lbls (Through).
type updateAcctInput struct {
	Name                    *string
	AddKidIDs, RemoveKidIDs []string
	ClearKids               bool
	PetID                   *string
	ClearPet                bool
	AddLblIDs, RemoveLblIDs []string
	ClearLbls               bool
}

func (in *updateAcctInput) Update(ctx context.Context, id string, set ...velox.Setter[acct]) (*acct, error) {
	u, key := gqlimpl.NewUpdate[int64](ctx, gqlimpl.InputConn[acct]("Acct", int(accts.ID.I), acctInputGQL), id)
	if in.Name != nil {
		u.Set(int(accts.Name.I), *in.Name)
	}
	addKidIDsKeys, removeKidIDsKeys := gqlimpl.AddRemove[int64](u, "Kid", "addKidIDs", in.AddKidIDs, "removeKidIDs", in.RemoveKidIDs)
	gqlimpl.Children(u, "addKidIDs", "Kid", gqlimpl.Edit{Clear: in.ClearKids, Remove: removeKidIDsKeys, Add: addKidIDsKeys}, key, acctKidsEdgeGQL())
	petIDKey := gqlimpl.OptionalKey[int64](u, "petID", in.PetID, "Kid")
	clearPet := gqlimpl.ClearEdge(u, "petID", in.PetID != nil, "clearPet", in.ClearPet)
	gqlimpl.Child(u, "petID", "Kid", clearPet, petIDKey, key, acctKidsEdgeGQL())
	addLblIDsKeys, removeLblIDsKeys := gqlimpl.AddRemove[int64](u, "Lbl", "addLblIDs", in.AddLblIDs, "removeLblIDs", in.RemoveLblIDs)
	gqlimpl.Through(u, "addLblIDs", "Lbl", gqlimpl.Edit{Clear: in.ClearLbls, Remove: removeLblIDsKeys, Add: addLblIDsKeys}, key, acctLblsEdgeGQL())
	p, err := u.Exec(key, velox.Assignments(set))
	return gqlimpl.As[*acct](p, err)
}

// updateKidInput is UpdateKidInput, with the edge acct, whose key is on the
// kid's row (SetKey).
type updateKidInput struct {
	Title     *string
	AcctID    *string
	ClearAcct bool
}

func (in *updateKidInput) Update(ctx context.Context, id string, set ...velox.Setter[kid]) (*kid, error) {
	u, key := gqlimpl.NewUpdate[int64](ctx, gqlimpl.InputConn[kid]("Kid", int(kids.ID.I), kidInputGQL), id)
	if in.Title != nil {
		u.Set(int(kids.Title.I), *in.Title)
	}
	if in.AcctID != nil {
		acctIDKey := gqlimpl.Key[int64](u, "acctID", *in.AcctID, "Acct")
		gqlimpl.Exists(u, "acctID", "Acct", velox.TableOf[acct](), int(accts.ID.I), []int64{acctIDKey})
		u.SetKey(int(kids.AcctID.I), acctIDKey)
	}
	if gqlimpl.ClearEdge(u, "acctID", in.AcctID != nil, "clearAcct", in.ClearAcct) {
		u.SetKey(int(kids.AcctID.I), nil)
	}
	p, err := u.Exec(key, velox.Assignments(set))
	return gqlimpl.As[*kid](p, err)
}

// inputLog records each statement a DB runs as "<op> <table>", and fails the
// failAt-th one since the last take with errInjected.
type inputLog struct {
	mu     sync.Mutex
	stmts  []string
	failAt int
}

var errInjected = errors.New("injected statement failure")

func (l *inputLog) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	l.mu.Lock()
	l.stmts = append(l.stmts, st.Op.String()+" "+st.Entity.Table)
	fail := l.failAt == len(l.stmts)
	l.mu.Unlock()
	if fail {
		return errInjected
	}
	return next(ctx)
}

// take returns the statements since the last take, and forgets them.
func (l *inputLog) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.stmts
	l.stmts = nil
	return s
}

// countless makes the writes of a countlessConn report no row count, as a
// driver that cannot count them does (velox's -1), while it is on.
type countless struct{ on atomic.Bool }

type countlessConnector struct {
	drv driver.Driver
	c   *countless
}

func (c countlessConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.drv.Open("file::memory:")
	if err != nil {
		return nil, err
	}
	return countlessConn{conn.(savepointInner), c.c}, nil
}

func (c countlessConnector) Driver() driver.Driver { return c.drv }

type countlessConn struct {
	savepointInner
	c *countless
}

func (c countlessConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	r, err := c.savepointInner.ExecContext(ctx, q, args)
	if err != nil || !c.c.on.Load() {
		return r, err
	}
	return uncounted{r}, nil
}

type uncounted struct{ driver.Result }

func (uncounted) RowsAffected() (int64, error) { return 0, errors.New("no count") }

// inputFixture is the database of the inputs' tests, with:
//   - accts 1 "a", 2 "b", 3 "c";
//   - kids (id: acct, title) 1: 1 "k1", 2: 1 "k2", 3: 2 "k3", 4: none
//     "free", 5: none "hidden", 6: 3 "locked", 7: none "locked";
//   - lbls 1 "x", 2 "y", 3 "z", 4 "hidden";
//   - acct_lbls 1-1, 1-2, 1-4, 2-2.
type inputFixture struct {
	db   *velox.DB
	raw  *sql.DB
	log  *inputLog
	cnt  *countless
	ctx  context.Context
	opts []veloxgql.Option
}

func newInputFixture(t *testing.T, opts ...veloxgql.Option) *inputFixture {
	t.Helper()
	probe, err := sql.Open("sqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	drv := probe.Driver()
	_ = probe.Close()
	f := &inputFixture{log: &inputLog{}, cnt: &countless{}, opts: opts}
	f.raw = sql.OpenDB(countlessConnector{drv, f.cnt})
	f.raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = f.raw.Close() })
	for _, s := range []string{
		`CREATE TABLE accts (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER NOT NULL)`,
		`CREATE TABLE kids (id INTEGER PRIMARY KEY, acct_id INTEGER, title TEXT NOT NULL)`,
		`CREATE TABLE lbls (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE acct_lbls (acct_id INTEGER NOT NULL, lbl_id INTEGER NOT NULL, PRIMARY KEY (acct_id, lbl_id))`,
		`INSERT INTO accts (id, name, age) VALUES (1, 'a', 1), (2, 'b', 2), (3, 'c', 3)`,
		`INSERT INTO kids (id, acct_id, title) VALUES (1, 1, 'k1'), (2, 1, 'k2'), (3, 2, 'k3'), (4, NULL, 'free'), (5, NULL, 'hidden'), (6, 3, 'locked'), (7, NULL, 'locked')`,
		`INSERT INTO lbls (id, name) VALUES (1, 'x'), (2, 'y'), (3, 'z'), (4, 'hidden')`,
		`INSERT INTO acct_lbls (acct_id, lbl_id) VALUES (1, 1), (1, 2), (1, 4), (2, 2)`,
	} {
		if _, err := f.raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	f.db = velox.Open(velox.SQLite, f.raw, velox.WithAround(f.log.around))
	f.ctx = veloxgql.NewExtension(f.db, opts...).NewContext(context.Background())
	return f
}

// state is every row of the fixture's tables, read past the policies and the
// log: "accts 1 a 1; …".
func (f *inputFixture) state(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []string{
		`SELECT 'acct', id, name, age FROM accts ORDER BY id`,
		`SELECT 'kid', id, COALESCE(acct_id, 0), title FROM kids ORDER BY id`,
		`SELECT 'lbl', id, name, '' FROM lbls ORDER BY id`,
		`SELECT 'link', acct_id, lbl_id, '' FROM acct_lbls ORDER BY acct_id, lbl_id`,
	} {
		rows, err := f.raw.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var kind, c, d string
			var id int64
			if err := rows.Scan(&kind, &id, &c, &d); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%s %d %s %s; ", kind, id, c, d)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return b.String()
}

// kidOwners is each kid's acct, 0 for none.
func (f *inputFixture) kidOwners(t *testing.T) map[int64]int64 {
	t.Helper()
	rows, err := f.raw.Query(`SELECT id, COALESCE(acct_id, 0) FROM kids`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]int64{}
	for rows.Next() {
		var id, owner int64
		if err := rows.Scan(&id, &owner); err != nil {
			t.Fatal(err)
		}
		out[id] = owner
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// links are the join rows of acct, as their lbl IDs in order.
func (f *inputFixture) links(t *testing.T, acct int64) []int64 {
	t.Helper()
	rows, err := f.raw.Query(`SELECT lbl_id FROM acct_lbls WHERE acct_id = ? ORDER BY lbl_id`, acct)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func acctGQL(k int64) string { return veloxgql.EncodeID("Acct", k) }
func kidGQL(k int64) string  { return veloxgql.EncodeID("Kid", k) }
func lblGQL(k int64) string  { return veloxgql.EncodeID("Lbl", k) }

func kidGQLs(ks ...int64) []string { return mapIDs(kidGQL, ks) }
func lblGQLs(ks ...int64) []string { return mapIDs(lblGQL, ks) }

func mapIDs(enc func(int64) string, ks []int64) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = enc(k)
	}
	return out
}

// wantErr fails t unless err has the text want and the code code ("" for an
// internal error).
func wantErr(t *testing.T, name string, err error, code, want string) {
	t.Helper()
	if err == nil || err.Error() != want || codeOf(err) != code {
		t.Errorf("%s: %v (code %q); want %q, code %q", name, err, codeOf(err), want, code)
	}
}

// TestCreateEdges: a create writes the row, then re-points the children it
// names and inserts its join rows, after checking the IDs of its
// many-to-many edge, in that order; each refusal rolls the whole create
// back.
func TestCreateEdges(t *testing.T) {
	f := newInputFixture(t)
	age := int64(9)
	x, err := (&createAcctInput{Name: "new", Age: &age, KidIDs: kidGQLs(4), LblIDs: lblGQLs(1, 3, 1)}).Create(f.ctx, nil)
	if err != nil || x.ID != 4 || x.Name != "new" || x.Age != 9 {
		t.Fatalf("Create = %+v, %v; want acct 4, new, 9", x, err)
	}
	if got, want := f.log.take(), []string{"select lbls", "insert accts", "update kids", "insert acct_lbls"}; !slices.Equal(got, want) {
		t.Errorf("statements %q, want %q", got, want)
	}
	if got := f.kidOwners(t)[4]; got != 4 {
		t.Errorf("kid 4's acct = %d, want 4", got)
	}
	if got := f.links(t, 4); !slices.Equal(got, []int64{1, 3}) {
		t.Errorf("acct 4's lbls = %v, want [1 3]", got)
	}

	// Each refusal, after the row's insert or before it, keeps nothing.
	before := f.state(t)
	for _, c := range []struct {
		name       string
		in         createAcctInput
		code, want string
	}{
		{"a kid another acct holds", createAcctInput{Name: "n", KidIDs: kidGQLs(4, 1)}, veloxgql.CodeBadUserInput,
			"veloxgql: CreateAcctInput.kidIDs: a Kid is already linked to another Acct"},
		{"a hidden kid", createAcctInput{Name: "n", KidIDs: kidGQLs(5)}, veloxgql.CodeNotFound,
			"veloxgql: CreateAcctInput.kidIDs: no Kid has this ID"},
		{"a kid the caller may not change", createAcctInput{Name: "n", KidIDs: kidGQLs(7)}, veloxgql.CodeNotFound,
			"veloxgql: CreateAcctInput.kidIDs: no Kid has this ID"},
		{"a missing and a hidden lbl", createAcctInput{Name: "n", LblIDs: lblGQLs(1, 4, 9)}, veloxgql.CodeNotFound,
			"veloxgql: CreateAcctInput.lblIDs: 2 of the 3 IDs name no Lbl"},
	} {
		_, err := (&c.in).Create(f.ctx, nil)
		wantErr(t, c.name, err, c.code, c.want)
		if got := f.state(t); got != before {
			t.Errorf("%s: the database changed:\n%s\nwant\n%s", c.name, got, before)
		}
	}

	// A kid with an acct: its ID is checked before the insert.
	k, err := (&createKidInput{Title: "t", AcctID: new(acctGQL(2))}).Create(f.ctx, nil)
	if err != nil || k.AcctID == nil || *k.AcctID != 2 {
		t.Fatalf("Create kid = %+v, %v; want acct 2", k, err)
	}
	f.log.take()
	_, err = (&createKidInput{Title: "t", AcctID: new(acctGQL(99))}).Create(f.ctx, nil)
	wantErr(t, "a missing acct", err, veloxgql.CodeNotFound, "veloxgql: CreateKidInput.acctID: no Acct has this ID")
	if got := f.log.take(); !slices.Equal(got, []string{"select accts"}) {
		t.Errorf("a missing acct: statements %q, want the check alone", got)
	}
	// No acct: no check.
	if k, err := (&createKidInput{Title: "u"}).Create(f.ctx, nil); err != nil || k.AcctID != nil {
		t.Errorf("Create kid with no acct = %+v, %v", k, err)
	}
	if got := f.log.take(); !slices.Equal(got, []string{"insert kids"}) {
		t.Errorf("no acct: statements %q, want the insert alone", got)
	}

	// The x given is the row inserted: one whose key exists fails with the
	// database's error, uncoded, and keeps nothing.
	before = f.state(t)
	_, err = (&createAcctInput{Name: "dup", LblIDs: lblGQLs(1)}).Create(f.ctx, &acct{ID: 1})
	if err == nil || codeOf(err) != "" {
		t.Errorf("a duplicate key: %v (code %q), want the database's error", err, codeOf(err))
	}
	if got := f.state(t); got != before {
		t.Errorf("a duplicate key: the database changed")
	}

	// Without a DB in the context nothing runs.
	if _, err := (&createAcctInput{Name: "n"}).Create(context.Background(), nil); !errors.Is(err, veloxgql.ErrNoDB) {
		t.Errorf("no DB: %v, want ErrNoDB", err)
	}
}

// TestCreateRefusedBeforeAnyStatement: a malformed or mistyped ID, a list over
// MaxPage, and a validator's refusal of a required or a given optional field
// fail before any statement; a decode error wins over a validator's.
func TestCreateRefusedBeforeAnyStatement(t *testing.T) {
	f := newInputFixture(t, veloxgql.WithLimits(veloxgql.Limits{MaxPage: 2}))
	neg := int64(-1)
	for _, c := range []struct {
		name       string
		in         createAcctInput
		is         error
		code, want string
	}{
		{"a malformed kid ID", createAcctInput{Name: "n", KidIDs: []string{kidGQL(4), "!"}}, veloxgql.ErrBadID, veloxgql.CodeBadUserInput, ""},
		{"a lbl ID for a kid", createAcctInput{Name: "n", KidIDs: lblGQLs(1)}, veloxgql.ErrWrongIDType, veloxgql.CodeBadUserInput, ""},
		{"three lbls over MaxPage 2", createAcctInput{Name: "n", LblIDs: lblGQLs(1, 2, 3)}, veloxgql.ErrPageTooLarge, veloxgql.CodeBadUserInput, ""},
		{"an empty name and a bad ID: the ID's", createAcctInput{Name: "", KidIDs: []string{"!"}}, veloxgql.ErrBadID, veloxgql.CodeBadUserInput, ""},
		{"an empty name", createAcctInput{Name: ""}, nil, veloxgql.CodeBadUserInput, "veloxgql: CreateAcctInput.name: invalid value: must not be empty"},
		{"a negative age", createAcctInput{Name: "n", Age: &neg}, nil, veloxgql.CodeBadUserInput, "veloxgql: CreateAcctInput.age: invalid value: must not be negative"},
	} {
		_, err := (&c.in).Create(f.ctx, nil)
		switch {
		case c.is != nil && !errors.Is(err, c.is):
			t.Errorf("%s: %v, want %v", c.name, err, c.is)
		case codeOf(err) != c.code:
			t.Errorf("%s: %v (code %q), want %s", c.name, err, codeOf(err), c.code)
		case c.want != "" && err.Error() != c.want:
			t.Errorf("%s: %q, want %q", c.name, err, c.want)
		case c.is != nil && !strings.HasPrefix(err.Error(), "veloxgql: CreateAcctInput."):
			t.Errorf("%s: %q names no input field", c.name, err)
		}
		if got := f.log.take(); len(got) != 0 {
			t.Errorf("%s: statements %q, want none", c.name, got)
		}
	}
}

// TestCreateRepointCountUnknown: a re-point whose UPDATE the driver did not
// count cannot be checked, and rolls the create back with an internal error.
func TestCreateRepointCountUnknown(t *testing.T) {
	f := newInputFixture(t)
	before := f.state(t)
	f.cnt.on.Store(true)
	_, err := (&createAcctInput{Name: "n", KidIDs: kidGQLs(4)}).Create(f.ctx, nil)
	f.cnt.on.Store(false)
	wantErr(t, "an uncounted re-point", err, "", "veloxgql: CreateAcctInput.kidIDs: row count unknown")
	if got := f.state(t); got != before {
		t.Errorf("the database changed:\n%s\nwant\n%s", got, before)
	}
}

// TestUpdateRow: an update with fields to set is one UPDATE and the row read
// back; one with nothing to set checks the row with LockRow; a row the
// UPDATE does not change is NOT_FOUND, and so is a row the update moved off
// its key; a bad id is refused before any statement.
func TestUpdateRow(t *testing.T) {
	f := newInputFixture(t)
	x, err := (&updateAcctInput{Name: new("z")}).Update(f.ctx, acctGQL(1))
	if err != nil || x.ID != 1 || x.Name != "z" || x.Age != 1 {
		t.Fatalf("Update = %+v, %v; want 1 z 1", x, err)
	}
	if got, want := f.log.take(), []string{"update accts", "select accts"}; !slices.Equal(got, want) {
		t.Errorf("statements %q, want %q", got, want)
	}
	// Nothing to set: the row is checked, not written.
	if x, err := (&updateAcctInput{}).Update(f.ctx, acctGQL(2)); err != nil || x.Name != "b" {
		t.Errorf("an empty update = %+v, %v", x, err)
	}
	if got, want := f.log.take(), []string{"select accts", "select accts"}; !slices.Equal(got, want) {
		t.Errorf("an empty update: statements %q, want %q", got, want)
	}
	for _, c := range []struct {
		name string
		in   updateAcctInput
		set  []velox.Setter[acct]
	}{
		{"a missing row", updateAcctInput{Name: new("z")}, nil},
		{"a missing row, nothing to set", updateAcctInput{}, nil},
	} {
		_, err := (&c.in).Update(f.ctx, acctGQL(99), c.set...)
		wantErr(t, c.name, err, veloxgql.CodeNotFound, "veloxgql: UpdateAcctInput: no Acct has this ID")
	}
	// The application's setter moves the row off the key it was found by.
	_, err = (&updateAcctInput{Name: new("moved")}).Update(f.ctx, acctGQL(3), accts.ID.Set(30))
	wantErr(t, "a row moved off its key", err, veloxgql.CodeNotFound, "veloxgql: UpdateAcctInput: no Acct has this ID")
	if got := f.state(t); !strings.Contains(got, "acct 3 c 3;") || strings.Contains(got, "acct 30") {
		t.Errorf("the moved row was kept: %s", got)
	}
	f.log.take()

	for _, c := range []struct {
		name, id string
		is       error
	}{
		{"a malformed id", "!", veloxgql.ErrBadID},
		{"a kid's id", kidGQL(1), veloxgql.ErrWrongIDType},
	} {
		_, err := (&updateAcctInput{Name: new("z")}).Update(f.ctx, c.id)
		if !errors.Is(err, c.is) || codeOf(err) != veloxgql.CodeBadUserInput || !strings.HasPrefix(err.Error(), "veloxgql: id: ") {
			t.Errorf("%s: %v (code %q), want BAD_USER_INPUT %v naming id", c.name, err, codeOf(err), c.is)
		}
	}
	// The input's invalid field, and one with no single meaning.
	_, err = (&updateAcctInput{Name: new("")}).Update(f.ctx, acctGQL(1))
	wantErr(t, "an empty name", err, veloxgql.CodeBadUserInput, "veloxgql: UpdateAcctInput.name: invalid value: must not be empty")
	_, err = (&updateAcctInput{AddKidIDs: kidGQLs(1, 4), RemoveKidIDs: kidGQLs(4)}).Update(f.ctx, acctGQL(1))
	wantErr(t, "a kid to add and to remove", err, veloxgql.CodeBadUserInput, "veloxgql: UpdateAcctInput: an ID is in both addKidIDs and removeKidIDs")
	_, err = (&updateAcctInput{PetID: new(kidGQL(4)), ClearPet: true}).Update(f.ctx, acctGQL(1))
	wantErr(t, "a pet and clearPet", err, veloxgql.CodeBadUserInput, "veloxgql: UpdateAcctInput: petID and clearPet together")
	if got := f.log.take(); len(got) != 0 {
		t.Errorf("refused inputs: statements %q, want none", got)
	}
}

// TestUpdateCountUnknown: an UPDATE the driver did not count is checked by
// LockRow, which tells a row that exists from one that does not.
func TestUpdateCountUnknown(t *testing.T) {
	f := newInputFixture(t)
	f.cnt.on.Store(true)
	defer f.cnt.on.Store(false)
	x, err := (&updateAcctInput{Name: new("z")}).Update(f.ctx, acctGQL(1))
	if err != nil || x.Name != "z" {
		t.Fatalf("an uncounted update = %+v, %v; want z", x, err)
	}
	if got, want := f.log.take(), []string{"update accts", "select accts", "select accts"}; !slices.Equal(got, want) {
		t.Errorf("statements %q, want %q", got, want)
	}
	_, err = (&updateAcctInput{Name: new("z")}).Update(f.ctx, acctGQL(99))
	wantErr(t, "an uncounted update of a missing row", err, veloxgql.CodeNotFound, "veloxgql: UpdateAcctInput: no Acct has this ID")
}

// TestUpdateKeyOnRow: an edge whose key is on the row is a setter of the key,
// after the fields' and the application's, whose ID is checked before the
// UPDATE; clearing it sets the key NULL; the application's setter of the key
// wins; a row the caller may not change is NOT_FOUND.
func TestUpdateKeyOnRow(t *testing.T) {
	f := newInputFixture(t)
	x, err := (&updateKidInput{AcctID: new(acctGQL(2))}).Update(f.ctx, kidGQL(4))
	if err != nil || x.AcctID == nil || *x.AcctID != 2 {
		t.Fatalf("set acct = %+v, %v; want acct 2", x, err)
	}
	if got, want := f.log.take(), []string{"select accts", "update kids", "select kids"}; !slices.Equal(got, want) {
		t.Errorf("statements %q, want %q", got, want)
	}
	if x, err := (&updateKidInput{ClearAcct: true}).Update(f.ctx, kidGQL(4)); err != nil || x.AcctID != nil {
		t.Errorf("clear acct = %+v, %v; want none", x, err)
	}
	// The application's setter of the key wins over the input's.
	x, err = (&updateKidInput{Title: new("t"), AcctID: new(acctGQL(2))}).Update(f.ctx, kidGQL(4), kids.AcctID.Set(3))
	if err != nil || x.AcctID == nil || *x.AcctID != 3 || x.Title != "t" {
		t.Errorf("the application's acct = %+v, %v; want acct 3, t", x, err)
	}
	f.log.take()
	_, err = (&updateKidInput{AcctID: new(acctGQL(99))}).Update(f.ctx, kidGQL(4))
	wantErr(t, "a missing acct", err, veloxgql.CodeNotFound, "veloxgql: UpdateKidInput.acctID: no Acct has this ID")
	if got := f.log.take(); !slices.Equal(got, []string{"select accts"}) {
		t.Errorf("a missing acct: statements %q, want the check alone", got)
	}
	for _, id := range []int64{5, 7} {
		_, err = (&updateKidInput{Title: new("t")}).Update(f.ctx, kidGQL(id))
		wantErr(t, fmt.Sprintf("kid %d", id), err, veloxgql.CodeNotFound, "veloxgql: UpdateKidInput: no Kid has this ID")
	}
	if got := f.kidOwners(t); got[5] != 0 || got[7] != 0 {
		t.Errorf("kids 5 and 7 changed: %v", got)
	}
}

// TestUpdateChildren: an edge to many is cleared, then the keys to remove
// unlinked, then those to add linked, each under the caller's scope; a kid
// another acct holds, or that the caller may not read or change, is refused,
// and the whole update with it.
func TestUpdateChildren(t *testing.T) {
	f := newInputFixture(t)
	if _, err := (&updateAcctInput{AddKidIDs: kidGQLs(4), RemoveKidIDs: kidGQLs(2)}).Update(f.ctx, acctGQL(1)); err != nil {
		t.Fatal(err)
	}
	if got, want := f.log.take(), []string{"select accts", "update kids", "update kids", "select kids", "select accts"}; !slices.Equal(got, want) {
		t.Errorf("statements %q, want %q", got, want)
	}
	if got := f.kidOwners(t); got[1] != 1 || got[2] != 0 || got[4] != 1 {
		t.Errorf("owners %v; want kids 1 and 4 of acct 1, 2 of none", got)
	}
	// Clear, then add one that was linked: it is linked again.
	if _, err := (&updateAcctInput{ClearKids: true, AddKidIDs: kidGQLs(1)}).Update(f.ctx, acctGQL(1)); err != nil {
		t.Fatal(err)
	}
	if got := f.kidOwners(t); got[1] != 1 || got[4] != 0 {
		t.Errorf("owners %v; want kid 1 of acct 1 alone", got)
	}
	// Clear leaves the kid the caller may not change.
	if _, err := (&updateAcctInput{ClearKids: true}).Update(f.ctx, acctGQL(3)); err != nil {
		t.Fatal(err)
	}
	if got := f.kidOwners(t)[6]; got != 3 {
		t.Errorf("the locked kid's acct = %d, want 3", got)
	}
	// Adding a kid the row holds already is fine.
	if _, err := (&updateAcctInput{AddKidIDs: kidGQLs(1)}).Update(f.ctx, acctGQL(1)); err != nil {
		t.Errorf("add a kid of the row's: %v", err)
	}

	before := f.state(t)
	for _, c := range []struct {
		name       string
		in         updateAcctInput
		code, want string
	}{
		{"a kid another acct holds", updateAcctInput{Name: new("renamed"), AddKidIDs: kidGQLs(4, 3)}, veloxgql.CodeBadUserInput,
			"veloxgql: UpdateAcctInput.addKidIDs: a Kid is already linked to another Acct"},
		{"a hidden kid", updateAcctInput{AddKidIDs: kidGQLs(5)}, veloxgql.CodeNotFound, "veloxgql: UpdateAcctInput.addKidIDs: no Kid has this ID"},
		{"a missing and a locked kid", updateAcctInput{AddKidIDs: kidGQLs(4, 7, 99)}, veloxgql.CodeNotFound, "veloxgql: UpdateAcctInput.addKidIDs: 2 of the 3 IDs name no Kid"},
	} {
		_, err := (&c.in).Update(f.ctx, acctGQL(1))
		wantErr(t, c.name, err, c.code, c.want)
		if got := f.state(t); got != before {
			t.Errorf("%s: the database changed:\n%s\nwant\n%s", c.name, got, before)
		}
	}
}

// TestUpdateChild: a one-to-one re-point checks the new kid, lets the current
// one go, and links the new one; one the caller may not change that still
// holds the key refuses it; clearing unlinks the kid the row holds.
func TestUpdateChild(t *testing.T) {
	f := newInputFixture(t)
	if _, err := (&updateAcctInput{PetID: new(kidGQL(4))}).Update(f.ctx, acctGQL(2)); err != nil {
		t.Fatal(err)
	}
	if got, want := f.log.take(), []string{"select accts", "select kids", "update kids", "select kids", "update kids", "select kids", "select accts"}; !slices.Equal(got, want) {
		t.Errorf("statements %q, want %q", got, want)
	}
	if got := f.kidOwners(t); got[3] != 0 || got[4] != 2 {
		t.Errorf("owners %v; want kid 4 of acct 2, kid 3 of none", got)
	}
	// Re-pointing to the kid the row holds keeps it.
	if _, err := (&updateAcctInput{PetID: new(kidGQL(4))}).Update(f.ctx, acctGQL(2)); err != nil || f.kidOwners(t)[4] != 2 {
		t.Errorf("re-point to the same kid: %v, owners %v", err, f.kidOwners(t))
	}
	// Clear alone.
	if _, err := (&updateAcctInput{ClearPet: true}).Update(f.ctx, acctGQL(2)); err != nil || f.kidOwners(t)[4] != 0 {
		t.Errorf("clear: %v, owners %v", err, f.kidOwners(t))
	}

	before := f.state(t)
	for _, c := range []struct {
		name       string
		acct, kid  int64
		code, want string
	}{
		{"a locked kid holds the key", 3, 4, veloxgql.CodeBadUserInput, "veloxgql: UpdateAcctInput.petID: the Acct is already linked to a Kid"},
		{"a hidden kid", 2, 5, veloxgql.CodeNotFound, "veloxgql: UpdateAcctInput.petID: no Kid has this ID"},
		{"a kid another acct holds", 2, 1, veloxgql.CodeBadUserInput, "veloxgql: UpdateAcctInput.petID: a Kid is already linked to another Acct"},
		{"a locked kid", 2, 7, veloxgql.CodeNotFound, "veloxgql: UpdateAcctInput.petID: no Kid has this ID"},
	} {
		_, err := (&updateAcctInput{PetID: new(kidGQL(c.kid))}).Update(f.ctx, acctGQL(c.acct))
		wantErr(t, c.name, err, c.code, c.want)
		if got := f.state(t); got != before {
			t.Errorf("%s: the database changed:\n%s\nwant\n%s", c.name, got, before)
		}
	}
}

// TestUpdateThrough: a many-to-many edge is cleared a page at a time, then
// the keys to remove unlinked, then those to add checked and linked; a link
// to a lbl the caller may not read stays, and a link that exists is kept.
func TestUpdateThrough(t *testing.T) {
	f := newInputFixture(t, veloxgql.WithLimits(veloxgql.Limits{MaxPage: 1}))
	// Remove a lbl the caller may not read: its link stays, and no DELETE runs.
	if _, err := (&updateAcctInput{RemoveLblIDs: lblGQLs(4)}).Update(f.ctx, acctGQL(1)); err != nil {
		t.Fatal(err)
	}
	if got, want := f.log.take(), []string{"select accts", "select lbls", "select accts"}; !slices.Equal(got, want) {
		t.Errorf("remove a hidden lbl: statements %q, want %q", got, want)
	}
	if got := f.links(t, 1); !slices.Equal(got, []int64{1, 2, 4}) {
		t.Errorf("acct 1's lbls = %v, want [1 2 4]", got)
	}
	if _, err := (&updateAcctInput{RemoveLblIDs: lblGQLs(1)}).Update(f.ctx, acctGQL(1)); err != nil {
		t.Fatal(err)
	}
	if got := f.links(t, 1); !slices.Equal(got, []int64{2, 4}) {
		t.Errorf("acct 1's lbls = %v, want [2 4]", got)
	}
	// Add one linked already and a new one.
	if _, err := (&updateAcctInput{AddLblIDs: lblGQLs(2)}).Update(f.ctx, acctGQL(1)); err != nil {
		t.Fatal(err)
	}
	f.log.take()
	if _, err := (&updateAcctInput{AddLblIDs: lblGQLs(3)}).Update(f.ctx, acctGQL(1)); err != nil {
		t.Fatal(err)
	}
	if got, want := f.log.take(), []string{"select accts", "select lbls", "insert acct_lbls", "select accts"}; !slices.Equal(got, want) {
		t.Errorf("add: statements %q, want %q", got, want)
	}
	if got := f.links(t, 1); !slices.Equal(got, []int64{2, 3, 4}) {
		t.Errorf("acct 1's lbls = %v, want [2 3 4]", got)
	}
	// Clear, a page of one join row at a time: the hidden lbl's stays.
	if _, err := (&updateAcctInput{ClearLbls: true}).Update(f.ctx, acctGQL(1)); err != nil {
		t.Fatal(err)
	}
	if got := f.links(t, 1); !slices.Equal(got, []int64{4}) {
		t.Errorf("acct 1's lbls after clear = %v, want [4]", got)
	}
	if got := f.links(t, 2); !slices.Equal(got, []int64{2}) {
		t.Errorf("acct 2's lbls = %v, want [2]: another acct's links stay", got)
	}

	before := f.state(t)
	_, err := (&updateAcctInput{Name: new("renamed"), ClearLbls: true, AddLblIDs: lblGQLs(4)}).Update(f.ctx, acctGQL(2))
	wantErr(t, "a hidden lbl to add", err, veloxgql.CodeNotFound, "veloxgql: UpdateAcctInput.addLblIDs: no Lbl has this ID")
	if got := f.state(t); got != before {
		t.Errorf("a hidden lbl to add: the database changed:\n%s\nwant\n%s", got, before)
	}
}

// TestWriteStatementFails: whichever statement of a write fails, the write
// returns its error as it is, and keeps nothing.
func TestWriteStatementFails(t *testing.T) {
	for _, c := range []struct {
		name  string
		stmts int // the statements of the write that succeeds
		run   func(ctx context.Context) error
	}{
		{"create", 4, func(ctx context.Context) error {
			_, err := (&createAcctInput{Name: "n", KidIDs: kidGQLs(4), LblIDs: lblGQLs(3)}).Create(ctx, nil)
			return err
		}},
		{"create, a re-point refused", 3, func(ctx context.Context) error {
			_, err := (&createAcctInput{Name: "n", KidIDs: kidGQLs(1)}).Create(ctx, nil)
			if err != nil && strings.Contains(err.Error(), "already linked") {
				return nil
			}
			return err
		}},
		{"update, children and lbls", 13, func(ctx context.Context) error {
			_, err := (&updateAcctInput{
				Name: new("z"), ClearKids: true, RemoveKidIDs: kidGQLs(2), AddKidIDs: kidGQLs(4),
				ClearLbls: true, RemoveLblIDs: lblGQLs(2), AddLblIDs: lblGQLs(3),
			}).Update(ctx, acctGQL(1))
			return err
		}},
		{"update, a child re-pointed", 7, func(ctx context.Context) error {
			_, err := (&updateAcctInput{ClearPet: false, PetID: new(kidGQL(4))}).Update(ctx, acctGQL(2))
			return err
		}},
		{"update, a child cleared", 3, func(ctx context.Context) error {
			_, err := (&updateAcctInput{ClearPet: true}).Update(ctx, acctGQL(2))
			return err
		}},
		{"update, a key on the row", 3, func(ctx context.Context) error {
			_, err := (&updateKidInput{AcctID: new(acctGQL(2))}).Update(ctx, kidGQL(4))
			return err
		}},
		{"update, nothing to set", 2, func(ctx context.Context) error {
			_, err := (&updateAcctInput{}).Update(ctx, acctGQL(1))
			return err
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newInputFixture(t)
			before := f.state(t)
			for i := 1; ; i++ {
				f.log.failAt = i
				err := c.run(f.ctx)
				f.log.failAt = 0
				n := len(f.log.take())
				if err == nil {
					if i != c.stmts+1 || n != c.stmts {
						t.Errorf("succeeded with statement %d to fail, of %d; want %d statements", i, n, c.stmts)
					}
					break
				}
				if !errors.Is(err, errInjected) || codeOf(err) != "" {
					t.Fatalf("statement %d failed: %v (code %q), want the injected error as it is", i, err, codeOf(err))
				}
				if n != i {
					t.Errorf("statement %d failed after %d statements", i, n)
				}
				if got := f.state(t); got != before {
					t.Fatalf("statement %d failed: the database changed:\n%s\nwant\n%s", i, got, before)
				}
			}
		})
	}
}

// TestKeyHelpersAs: the helpers of keys of a named Go type convert each key
// decoded; their decode errors are the input's, naming the field.
func TestKeyHelpersAs(t *testing.T) {
	type acctID int64
	conv := func(k int64) acctID { return acctID(k) }
	f := newInputFixture(t)
	u, key := gqlimpl.NewUpdateAs[int64, acctID](f.ctx, gqlimpl.InputConn[acct]("Acct", int(accts.ID.I), acctInputGQL), acctGQL(2), conv)
	if key != 2 {
		t.Errorf("NewUpdateAs key = %v, want 2", key)
	}
	if k := gqlimpl.KeyAs(u, "a", acctGQL(3), "Acct", conv); k != 3 {
		t.Errorf("KeyAs = %v, want 3", k)
	}
	if k := gqlimpl.OptionalKeyAs(u, "b", new(acctGQL(4)), "Acct", conv); k == nil || *k != 4 {
		t.Errorf("OptionalKeyAs = %v, want 4", k)
	}
	if k := gqlimpl.OptionalKeyAs(u, "b", nil, "Acct", conv); k != nil {
		t.Errorf("OptionalKeyAs(nil) = %v, want nil", k)
	}
	if ks := gqlimpl.KeysAs(u, "c", mapIDs(acctGQL, []int64{5, 6, 5}), "Acct", conv); !slices.Equal(ks, []acctID{5, 6}) {
		t.Errorf("KeysAs = %v, want [5 6]", ks)
	}
	add, rm := gqlimpl.AddRemoveAs(u, "Acct", "addIDs", mapIDs(acctGQL, []int64{1}), "removeIDs", mapIDs(acctGQL, []int64{2}), conv)
	if !slices.Equal(add, []acctID{1}) || !slices.Equal(rm, []acctID{2}) {
		t.Errorf("AddRemoveAs = %v, %v; want [1], [2]", add, rm)
	}
	// Nothing failed: the update runs, and changes nothing.
	if p, err := u.Exec(key, nil); err != nil || p.(*acct).Name != "b" {
		t.Errorf("Exec = %+v, %v", p, err)
	}

	// An ID in both lists, then a malformed one: the decode error wins.
	u, _ = gqlimpl.NewUpdateAs[int64, acctID](f.ctx, gqlimpl.InputConn[acct]("Acct", int(accts.ID.I), acctInputGQL), acctGQL(2), conv)
	gqlimpl.AddRemoveAs(u, "Acct", "addIDs", mapIDs(acctGQL, []int64{1}), "removeIDs", mapIDs(acctGQL, []int64{1}), conv)
	_, err := u.Exec(2, nil)
	wantErr(t, "an ID in both", err, veloxgql.CodeBadUserInput, "veloxgql: UpdateAcctInput: an ID is in both addIDs and removeIDs")
	gqlimpl.KeyAs(u, "a", "!", "Acct", conv)
	if _, err := u.Exec(2, nil); !errors.Is(err, veloxgql.ErrBadID) || !strings.HasPrefix(err.Error(), "veloxgql: UpdateAcctInput.a: ") {
		t.Errorf("a malformed ID after an ID in both: %v, want the decode error naming UpdateAcctInput.a", err)
	}
	u, _ = gqlimpl.NewUpdateAs[int64, acctID](f.ctx, gqlimpl.InputConn[acct]("Acct", int(accts.ID.I), acctInputGQL), "!", conv)
	if _, err := u.Exec(2, nil); !errors.Is(err, veloxgql.ErrBadID) || !strings.HasPrefix(err.Error(), "veloxgql: id: ") {
		t.Errorf("NewUpdateAs of a malformed id: %v, want the decode error naming id", err)
	}
	if got := f.log.take(); !slices.Equal(got, []string{"select accts", "select accts"}) {
		t.Errorf("statements %q, want the first Exec's alone", got)
	}
}

// TestDecodeJSONInput: a JSON value of the wrong shape is a decode error of
// the input, which wins over the input's other errors.
func TestDecodeJSONInput(t *testing.T) {
	f := newInputFixture(t)
	c := gqlimpl.NewCreate(f.ctx, gqlimpl.InputConn[acct]("Acct", 0, acctInputGQL), new(acct), int(accts.Name.I))
	if got := gqlimpl.DecodeJSON[[]int](c, "tags", veloxgql.JSON(`[1, 2]`)); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("DecodeJSON = %v, want [1 2]", got)
	}
	gqlimpl.NotZero(c, "age", int64(0))
	if got := gqlimpl.DecodeJSON[[]int](c, "tags", veloxgql.JSON(`{"a": 1}`)); got != nil {
		t.Errorf("DecodeJSON of an object = %v, want nil", got)
	}
	err := c.Insert()
	wantErr(t, "an object for a list", err, veloxgql.CodeBadUserInput, "veloxgql: CreateAcctInput.tags: invalid value: not a JSON value of the field's type")
	if got := f.log.take(); len(got) != 0 {
		t.Errorf("statements %q, want none", got)
	}
}

// TestNoIDsNoCheck: Exists and CheckExists of no IDs run nothing; a field
// given, or its clear field alone, is no null to look for.
func TestNoIDsNoCheck(t *testing.T) {
	f := newInputFixture(t)
	c := gqlimpl.NewCreate(f.ctx, gqlimpl.InputConn[kid]("Kid", 0, kidInputGQL), new(kid), int(kids.Title.I))
	gqlimpl.Exists(c, "acctID", "Acct", velox.TableOf[acct](), int(accts.ID.I), []int64{})
	if err := gqlimpl.CheckExists(f.ctx, f.db.QueryTable(velox.TableOf[acct]()).Where(velox.Predicate(accts.ID.In())), 0, "at", "Acct"); err != nil {
		t.Errorf("CheckExists of no IDs: %v", err)
	}
	if got := f.log.take(); len(got) != 0 {
		t.Errorf("statements %q, want none", got)
	}
	u, _ := gqlimpl.NewUpdate[int64](f.ctx, gqlimpl.InputConn[acct]("Acct", int(accts.ID.I), acctInputGQL), acctGQL(1))
	if gqlimpl.Cleared(u, "age", true, "clearAge", false) {
		t.Error("Cleared of a field given: true")
	}
	if !gqlimpl.Cleared(u, "age", false, "clearAge", true) {
		t.Error("Cleared of clearAge: false")
	}
	if _, err := u.Exec(1, nil); err != nil {
		t.Errorf("Exec: %v", err)
	}
}
