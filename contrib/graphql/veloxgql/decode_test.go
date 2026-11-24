package veloxgql_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

// account is an entity as velox-gen writes one, for DecodeWhere's tests: a
// foreign key owner_id filtered as a field (as velox-graphql does when the
// key's edge is not in GraphQL), an enum, a city GraphQL names addressCity,
// a time and a decimal.
type account struct {
	ID       int64
	OwnerID  int64
	Status   accountStatus
	City     string
	OpenedAt time.Time
	Balance  dec
}

var accountMeta velox.MetaOnce[account]

func (*account) VeloxMeta() *velox.Meta[account] { return accountMeta.Load(accountDescriptor) }

func accountDescriptor() velox.Descriptor[account] {
	return velox.Descriptor[account]{
		Table: "accounts",
		Columns: []velox.ColumnDescriptor{
			{Field: "ID", Name: "id"}, {Field: "OwnerID", Name: "owner_id"}, {Field: "Status", Name: "status"},
			{Field: "City", Name: "city"}, {Field: "OpenedAt", Name: "opened_at"}, {Field: "Balance", Name: "balance"},
		},
		PrimaryKey: []int{0},
		FieldPtr: func(x *account, i int) any {
			return [...]any{&x.ID, &x.OwnerID, &x.Status, &x.City, &x.OpenedAt, &x.Balance}[i]
		},
	}
}

// accountStatus is an enum as velox-graphql writes one.
type accountStatus string

const (
	accountOpen   accountStatus = "open"
	accountClosed accountStatus = "closed"
)

func (e *accountStatus) UnmarshalGQL(v any) error {
	switch v {
	case "OPEN":
		*e = accountOpen
	case "CLOSED":
		*e = accountClosed
	default:
		return gqlgraph.InputErrorf("AccountStatus must be OPEN or CLOSED, not %v", v)
	}
	return nil
}

// dec is a decimal of the application's, which reads itself from text as
// shopspring's decimal.Decimal does, and is stored as a number.
type dec struct{ s string }

func (d *dec) UnmarshalText(b []byte) error {
	var f float64
	if _, err := fmt.Sscan(string(b), &f); err != nil {
		return fmt.Errorf("can't convert %s to decimal", b)
	}
	d.s = string(b)
	return nil
}

func (d dec) Value() (driver.Value, error) { return d.s, nil }

func (d *dec) Scan(v any) error { d.s = fmt.Sprint(v); return nil }

// accountType is the gqlgraph type velox-graphql writes for account, under
// filters=lenient.
var accountType = gqlgraph.Type{
	Name:           "Account",
	Table:          untyped.TableOf[account],
	Key:            []gqlgraph.Column{{Name: "id", Index: 0}},
	AlwaysRead:     []int{0},
	Fields:         []string{"id", "ownerId", "status", "addressCity", "openedAt", "balance"},
	LenientFilters: true,
	WhereFields: []gqlgraph.WhereField{
		{Name: "not", Kind: gqlgraph.WhereNot},
		{Name: "and", Kind: gqlgraph.WhereAnd},
		{Name: "or", Kind: gqlgraph.WhereOr},
		{Name: "id", Kind: gqlgraph.WhereID, Ops: gqlgraph.OpsEquality, Col: 0},
		{Name: "ownerId", Ops: gqlgraph.OpsEquality, Col: 1},
		{Name: "status", Ops: gqlgraph.OpEQ | gqlgraph.OpIn, Col: 2},
		{Name: "addressCity", Ops: gqlgraph.OpEQ | gqlgraph.OpContainsFold, Col: 3},
		{Name: "openedAt", Ops: gqlgraph.OpGT | gqlgraph.OpLT, Col: 4},
		{Name: "balance", Ops: gqlgraph.OpEQ | gqlgraph.OpGT, Col: 5},
	},
}

// accountWhereInput is the where input velox-graphql writes for account:
// no tags, its Go names not its GraphQL names.
type accountWhereInput struct {
	Not *accountWhereInput
	And []*accountWhereInput
	Or  []*accountWhereInput

	ID      *int64
	IDNEQ   *int64
	IDIn    []int64
	IDNotIn []int64

	OwnerID      *int64
	OwnerIDNEQ   *int64
	OwnerIDIn    []int64
	OwnerIDNotIn []int64

	Status   *accountStatus
	StatusIn []accountStatus

	City             *string
	CityContainsFold *string

	OpenedAtGT *time.Time
	OpenedAtLT *time.Time

	Balance   *dec
	BalanceGT *dec
}

func (w *accountWhereInput) Pred(ctx context.Context) (velox.Pred[account], error) {
	p, err := w.Predicate(ctx)
	return velox.Pred[account](p), err
}

func (w *accountWhereInput) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, &accountType, w)
}

// probeType and probeWhereInput are a where input DecodeWhere reads and no
// test lowers: a UUID key and the other Go types a field can have.
var probeType = gqlgraph.Type{
	Name:  "Probe",
	Table: untyped.TableOf[account],
	WhereFields: []gqlgraph.WhereField{
		{Name: "not", Kind: gqlgraph.WhereNot},
		{Name: "id", Kind: gqlgraph.WhereID, Ops: gqlgraph.OpEQ | gqlgraph.OpIn},
		{Name: "points", Ops: gqlgraph.OpEQ},
		{Name: "level", Ops: gqlgraph.OpEQ},
		{Name: "ratio", Ops: gqlgraph.OpEQ},
		{Name: "raw", Ops: gqlgraph.OpEQ},
		{Name: "flag", Ops: gqlgraph.OpsNull},
		{Name: "tags", Kind: gqlgraph.WhereArray, Ops: gqlgraph.OpHasSome},
		{Name: "hasAccount", Kind: gqlgraph.WhereHas},
		{Name: "hasAccountWith", Kind: gqlgraph.WhereHasWith},
	},
}

type probeWhereInput struct {
	Not            *probeWhereInput
	ID             *uuid.UUID
	IDIn           []uuid.UUID
	Points         *uint64
	Level          *int8
	Ratio          *float64
	Raw            *[]byte
	FlagIsNil      *bool
	FlagNotNil     *bool
	TagsHasSome    []string
	HasAccount     *bool
	HasAccountWith []*accountWhereInput
}

func (w *probeWhereInput) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, &probeType, w)
}

// decoded is v decoded into a new W, failing the test on an error.
func decoded[W any](t *testing.T, v any) *W {
	t.Helper()
	w := new(W)
	if err := veloxgql.DecodeWhere(v, w); err != nil {
		t.Fatalf("DecodeWhere(%v): %v", v, err)
	}
	return w
}

// jsonDoc is the JSON s as a JSON decoder gives it to an application.
func jsonDoc(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestDecodeWhereValues: each field is read by its GraphQL name into its
// Go type: an ID from a string or a number, an enum by its UnmarshalGQL, a
// time from RFC 3339, a decimal and a UUID by UnmarshalText, a single value
// where a list goes as a list of one, nested inputs, and null as nothing.
func TestDecodeWhereValues(t *testing.T) {
	w := decoded[accountWhereInput](t, jsonDoc(t, `{
		"ownerId": "123",
		"idIn": [1, "2"],
		"not": {"idIn": ["999"]},
		"addressCity": "Elsewhere",
		"status": "OPEN",
		"statusIn": "CLOSED",
		"openedAtGT": "2026-01-02T03:04:05Z",
		"balanceGT": 10.5,
		"balance": "12.50",
		"or": [{"id": 4}, {"and": {"addressCityContainsFold": "else"}}],
		"idNEQ": null
	}`))
	opened := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	want := accountWhereInput{
		OwnerID:    new(int64(123)),
		IDIn:       []int64{1, 2},
		Not:        &accountWhereInput{IDIn: []int64{999}},
		City:       new("Elsewhere"),
		Status:     new(accountOpen),
		StatusIn:   []accountStatus{accountClosed},
		OpenedAtGT: &opened,
		BalanceGT:  &dec{"10.5"},
		Balance:    &dec{"12.50"},
		Or: []*accountWhereInput{
			{ID: new(int64(4))},
			{And: []*accountWhereInput{{CityContainsFold: new("else")}}},
		},
	}
	if !reflect.DeepEqual(*w, want) {
		t.Errorf("decoded\n%+v\nwant\n%+v", *w, want)
	}

	key := uuid.MustParse("0b6e0f6c-7d3a-4a8e-9f00-3c2f5d6e7a81")
	p := decoded[probeWhereInput](t, []byte(`{
		"id": "0b6e0f6c-7d3a-4a8e-9f00-3c2f5d6e7a81",
		"idIn": ["0b6e0f6c-7d3a-4a8e-9f00-3c2f5d6e7a81"],
		"points": "18446744073709551615",
		"level": -3,
		"ratio": 0.25,
		"raw": "aGk=",
		"flagIsNil": true,
		"tagsHasSome": ["a", "b"],
		"hasAccount": false,
		"hasAccountWith": [{"ownerId": 7}],
		"not": {"points": 12345678901234567890}
	}`))
	wantP := probeWhereInput{
		ID: &key, IDIn: []uuid.UUID{key}, Points: new(uint64(18446744073709551615)), Level: new(int8(-3)),
		Ratio: new(0.25), Raw: new([]byte("hi")), FlagIsNil: new(true), TagsHasSome: []string{"a", "b"},
		HasAccount: new(false), HasAccountWith: []*accountWhereInput{{OwnerID: new(int64(7))}},
		Not: &probeWhereInput{Points: new(uint64(12345678901234567890))},
	}
	if !reflect.DeepEqual(*p, wantP) {
		t.Errorf("decoded\n%+v\nwant\n%+v", *p, wantP)
	}

	// Values of the fields' own Go types, as a caller in Go gives them.
	g := decoded[accountWhereInput](t, map[string]any{"id": int64(5), "status": accountClosed, "balanceGT": json.Number("3")})
	if *g.ID != 5 || *g.Status != accountClosed || *g.BalanceGT != (dec{"3"}) {
		t.Errorf("Go values decoded to %+v", g)
	}
	if e := decoded[accountWhereInput](t, nil); !reflect.DeepEqual(*e, accountWhereInput{}) {
		t.Errorf("null decoded to %+v, want an empty input", e)
	}
}

// TestDecodeWhereErrors: a key the input lacks, and a value of the wrong
// type, are BAD_USER_INPUT errors that name the field through the nesting,
// and leave dst as it was.
func TestDecodeWhereErrors(t *testing.T) {
	for _, c := range []struct {
		doc  string
		want string
	}{
		{`{"owner": "1"}`, `veloxgql: owner: AccountWhereInput has no field "owner"`},
		{`{"ownerID": "1"}`, `veloxgql: ownerID: AccountWhereInput has no field "ownerID"`},
		{`{"not": {"idIn": [true]}}`, `veloxgql: not.idIn[0]: expected a whole number, got a boolean`},
		{`{"not": {"idIn": [null]}}`, `veloxgql: not.idIn[0]: null in a list`},
		{`{"or": [{}, {"nope": 1}]}`, `veloxgql: or[1].nope: AccountWhereInput has no field "nope"`},
		{`{"or": [1]}`, `veloxgql: or[0]: a where input is an object, got a number`},
		{`{"id": "x1"}`, `veloxgql: id: "x1" is not a whole number`},
		{`{"id": 1.5}`, `veloxgql: id: 1.5 is not a whole number`},
		{`{"id": 9007199254740993}`, `veloxgql: id: 9007199254740992 is too large`},
		{`{"not": {"ownerIdIn": "x"}}`, `veloxgql: not.ownerIdIn[0]: "x" is not a whole number`},
		{`{"addressCity": 3}`, `veloxgql: addressCity: expected a string, got a number`},
		{`{"status": "open"}`, `veloxgql: status: AccountStatus must be OPEN or CLOSED, not open`},
		{`{"openedAtGT": "yesterday"}`, `veloxgql: openedAtGT: Time must be an RFC 3339 string`},
		{`{"balance": "ten"}`, `veloxgql: balance: ten is not a veloxgql_test.dec`},
		{`{"balance": true}`, `veloxgql: balance: expected a veloxgql_test.dec as a string, got a boolean`},
	} {
		w := accountWhereInput{City: new("kept")}
		err := veloxgql.DecodeWhere(jsonDoc(t, c.doc), &w)
		if err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.doc, err, c.want)
			continue
		}
		if x, ok := errors.AsType[interface {
			error
			Extensions() map[string]any
		}](err); !ok || x.Extensions()["code"] != veloxgql.CodeBadUserInput {
			t.Errorf("%s: %v is not BAD_USER_INPUT", c.doc, err)
		}
		if !reflect.DeepEqual(w, accountWhereInput{City: new("kept")}) {
			t.Errorf("%s: a refused document changed dst to %+v", c.doc, w)
		}
	}
	for _, c := range []struct {
		doc  string
		want string
	}{
		{`{"points": -1}`, `veloxgql: points: -1 is not a whole number of 0 or more`},
		{`{"points": "-1"}`, `veloxgql: points: "-1" is not a whole number of 0 or more`},
		{`{"level": 300}`, `veloxgql: level: 300 overflows int8`},
		{`{"ratio": "x"}`, `veloxgql: ratio: expected a number, got a string`},
		{`{"raw": 1}`, `veloxgql: raw: Bytes must be`},
		{`{"flagIsNil": "yes"}`, `veloxgql: flagIsNil: expected a boolean, got a string`},
		{`{"id": "nope"}`, `veloxgql: id: nope is not a uuid.UUID`},
		{`{"hasAccountWith": [{"status": 1}]}`, `veloxgql: hasAccountWith[0].status: AccountStatus must be`},
	} {
		var p probeWhereInput
		if err := veloxgql.DecodeWhere([]byte(c.doc), &p); err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.doc, err, c.want)
		}
	}
	const neg = "veloxgql: points: -2 is not a whole number of 0 or more"
	if err := veloxgql.DecodeWhere(map[string]any{"points": -2.0}, new(probeWhereInput)); err == nil || err.Error() != neg {
		t.Errorf("points -2.0: %v, want %q", err, neg)
	}
}

// TestDecodeWhereMisuse: dst must be a pointer to a generated where input,
// and the text one JSON object.
func TestDecodeWhereMisuse(t *testing.T) {
	type handWritten struct{ ID *int64 }
	var w accountWhereInput
	for _, c := range []struct {
		v, dst any
		want   string
	}{
		{map[string]any{}, w, "veloxgql: DecodeWhere decodes into a pointer to a where input, not a veloxgql_test.accountWhereInput"},
		{map[string]any{}, (*accountWhereInput)(nil), "veloxgql: DecodeWhere decodes into a pointer"},
		{map[string]any{}, &handWritten{}, "veloxgql: veloxgql_test.handWritten is not a where input velox-graphql generated"},
		{map[string]any{}, new(int), "veloxgql: int is not a where input velox-graphql generated"},
		{json.RawMessage(`{`), &w, "veloxgql: a where input is not JSON"},
		{[]byte(`{} {}`), &w, "veloxgql: a where input is one JSON value, and something follows it"},
		{"ownerId", &w, "veloxgql: a where input is an object, got a string"},
	} {
		if err := veloxgql.DecodeWhere(c.v, c.dst); err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("DecodeWhere(%v, %T): %v, want %q", c.v, c.dst, err, c.want)
		}
	}
}

// TestDecodeWhereFilters: the documents nextapp keeps, decoded and lowered
// with Pred, select the rows they say on SQLite.
func TestDecodeWhereFilters(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1) // an in-memory database lives as long as its one connection
	if _, err := sqlDB.Exec(`CREATE TABLE accounts (id INTEGER PRIMARY KEY, owner_id INTEGER NOT NULL, status TEXT NOT NULL,
		city TEXT NOT NULL, opened_at DATETIME NOT NULL, balance NUMERIC NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, sqlDB)
	ctx := t.Context()
	day := func(d int) time.Time { return time.Date(2026, 1, d, 0, 0, 0, 0, time.UTC) }
	rows := []account{
		{ID: 1, OwnerID: 123, Status: accountOpen, City: "Elsewhere", OpenedAt: day(1), Balance: dec{"5"}},
		{ID: 2, OwnerID: 123, Status: accountClosed, City: "Here", OpenedAt: day(2), Balance: dec{"12.5"}},
		{ID: 3, OwnerID: 10, Status: accountOpen, City: "Elsewhere", OpenedAt: day(3), Balance: dec{"20"}},
		{ID: 999, OwnerID: 10, Status: accountOpen, City: "There", OpenedAt: day(4), Balance: dec{"1"}},
	}
	if err := db.InsertAll(rows).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		doc  string
		want []int64
	}{
		{`{"ownerId": "123"}`, []int64{1, 2}},
		{`{"not": {"idIn": ["999"]}}`, []int64{1, 2, 3}},
		{`{"addressCity": "Elsewhere"}`, []int64{1, 3}},
		{`{"ownerId": "10", "status": "OPEN"}`, []int64{3, 999}},
		{`{"or": [{"statusIn": ["CLOSED"]}, {"openedAtGT": "2026-01-03T12:00:00Z"}]}`, []int64{2, 999}},
		{`{"balanceGT": "10"}`, []int64{2, 3}},
		{`{"and": [{"addressCityContainsFold": "ELSE"}, {"idNotIn": [1]}]}`, []int64{3}},
	} {
		w := decoded[accountWhereInput](t, jsonDoc(t, c.doc))
		p, err := w.Pred(ctx)
		if err != nil {
			t.Errorf("%s: Pred: %v", c.doc, err)
			continue
		}
		got, err := db.Query[account]().Where(p).AllowUnbounded().All(ctx)
		if err != nil {
			t.Fatalf("%s: %v", c.doc, err)
		}
		ids := make([]int64, len(got))
		for i, r := range got {
			ids[i] = r.ID
		}
		slices.Sort(ids)
		if !slices.Equal(ids, c.want) {
			t.Errorf("%s selects %v, want %v", c.doc, ids, c.want)
		}
	}
}
