package veloxgql_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// The application's own Go types of person's columns, which a where input
// takes as their base types and Pred converts.
type (
	personID   int64
	personName string
)

// people's columns under the application's own Go types, as velox-gen
// writes the handles of a field with a GoType.
var peopleAs = struct {
	ID   velox.NumberCol[person, personID]
	Name velox.StringCol[person, personName]
}{
	ID:   velox.NumberCol[person, personID]{I: 0},
	Name: velox.StringCol[person, personName]{I: 1},
}

// personWhere is a where input of person, as velox-graphql writes one: a
// field for each predicate, and Pred with one helper call for each.
type personWhere struct {
	Not *personWhere
	And []*personWhere
	Or  []*personWhere

	ID      *string
	IDNEQ   *string
	IDIn    []string
	IDNotIn []string

	Name          *string
	NameNEQ       *string
	NameIn        []string
	NameNotIn     []string
	NameGT        *string
	NameContains  *string
	NameHasPrefix *string
	NameHasSuffix *string

	// The name through its own Go type, as for a field of type personName.
	Label      *string
	LabelNotIn []string

	Age      *int64
	AgeNEQ   *int64
	AgeIn    []int64
	AgeNotIn []int64
	AgeGT    *int64
	AgeGTE   *int64
	AgeLT    *int64
	AgeLTE   *int64

	// IsA is true for the people named a, and false for the others, as
	// IsNil, NotNil and has<Edge> are.
	IsA *bool
}

func (w *personWhere) Pred(ctx context.Context, depth int) (velox.Pred[person], error) {
	f := veloxgql.NewFilter[person](ctx, "PersonWhereInput", depth)
	if w == nil {
		return f.Pred()
	}
	veloxgql.Not(f, w.Not)
	veloxgql.And(f, w.And)
	veloxgql.Or(f, w.Or)
	veloxgql.ID(f, w.ID, "Person", people.ID.EQ)
	veloxgql.IDAs(f, w.IDNEQ, "Person", veloxgql.AsInt[personID, int64], peopleAs.ID.NEQ)
	veloxgql.IDList(f, w.IDIn, "Person", people.ID.In)
	veloxgql.IDListAs(f, w.IDNotIn, "Person", veloxgql.AsInt[personID, int64], peopleAs.ID.NotIn)
	veloxgql.Value(f, w.Name, people.Name.EQ)
	veloxgql.Value(f, w.NameNEQ, people.Name.NEQ)
	veloxgql.List(f, w.NameIn, people.Name.In)
	veloxgql.List(f, w.NameNotIn, people.Name.NotIn)
	veloxgql.Value(f, w.NameGT, people.Name.GT)
	veloxgql.Value(f, w.NameContains, people.Name.Contains)
	veloxgql.Value(f, w.NameHasPrefix, people.Name.HasPrefix)
	veloxgql.Value(f, w.NameHasSuffix, people.Name.HasSuffix)
	veloxgql.ValueAs(f, w.Label, veloxgql.AsString[personName, string], peopleAs.Name.EQ)
	veloxgql.ListAs(f, w.LabelNotIn, veloxgql.AsString[personName, string], peopleAs.Name.NotIn)
	veloxgql.Value(f, w.Age, people.Age.EQ)
	veloxgql.Value(f, w.AgeNEQ, people.Age.NEQ)
	veloxgql.List(f, w.AgeIn, people.Age.In)
	veloxgql.List(f, w.AgeNotIn, people.Age.NotIn)
	veloxgql.Value(f, w.AgeGT, people.Age.GT)
	veloxgql.Value(f, w.AgeGTE, people.Age.GTE)
	veloxgql.Value(f, w.AgeLT, people.Age.LT)
	veloxgql.Value(f, w.AgeLTE, people.Age.LTE)
	veloxgql.Is(f, w.IsA, isA)
	return f.Pred()
}

func isA() velox.Pred[person] { return people.Name.EQ("a") }

func strp(s string) *string { return &s }
func i64p(n int64) *int64   { return &n }
func boolp(b bool) *bool    { return &b }

func personID64(id int64) string { return veloxgql.EncodeID("Person", id) }

// where returns the keys of the people w selects, in key order, and fails
// the test on an error.
func where(t *testing.T, db *velox.DB, w *personWhere) []int64 {
	t.Helper()
	ctx := context.Background()
	p, err := w.Pred(ctx, 0)
	if err != nil {
		t.Fatalf("Pred: %v", err)
	}
	rows, err := db.Query[person]().Where(p).OrderBy(people.ID.Asc()).Unbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// The people are 1..10, named c a b a d b e b d f, aged 100 - id.
var all = []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

func except(ids ...int64) []int64 {
	return slices.DeleteFunc(slices.Clone(all), func(id int64) bool { return slices.Contains(ids, id) })
}

// TestWhereOps: each helper adds its predicate, on the column's own Go type
// or converted to the column's from the input's.
func TestWhereOps(t *testing.T) {
	db, _ := peopleDB(t)
	for _, c := range []struct {
		name string
		w    *personWhere
		want []int64
	}{
		{"ID", &personWhere{ID: strp(personID64(3))}, []int64{3}},
		{"IDNEQ", &personWhere{IDNEQ: strp(personID64(3))}, except(3)},
		{"IDIn", &personWhere{IDIn: []string{personID64(3), personID64(5)}}, []int64{3, 5}},
		{"IDNotIn", &personWhere{IDNotIn: []string{personID64(3), personID64(5)}}, except(3, 5)},
		{"Name", &personWhere{Name: strp("b")}, []int64{3, 6, 8}},
		{"NameNEQ", &personWhere{NameNEQ: strp("b")}, except(3, 6, 8)},
		{"NameIn", &personWhere{NameIn: []string{"a", "f"}}, []int64{2, 4, 10}},
		{"NameNotIn", &personWhere{NameNotIn: []string{"a", "f"}}, except(2, 4, 10)},
		{"NameGT", &personWhere{NameGT: strp("d")}, []int64{7, 10}},
		{"NameContains", &personWhere{NameContains: strp("e")}, []int64{7}},
		{"NameHasPrefix", &personWhere{NameHasPrefix: strp("d")}, []int64{5, 9}},
		{"NameHasSuffix", &personWhere{NameHasSuffix: strp("c")}, []int64{1}},
		// LIKE's wildcards are the client's text, not patterns.
		{"NameContains %", &personWhere{NameContains: strp("%")}, nil},
		{"NameHasPrefix _", &personWhere{NameHasPrefix: strp("_")}, nil},
		{"Label", &personWhere{Label: strp("d")}, []int64{5, 9}},
		{"LabelNotIn", &personWhere{LabelNotIn: []string{"b", "d"}}, except(3, 5, 6, 8, 9)},
		{"Age", &personWhere{Age: i64p(97)}, []int64{3}},
		{"AgeNEQ", &personWhere{AgeNEQ: i64p(97)}, except(3)},
		{"AgeIn", &personWhere{AgeIn: []int64{91, 99}}, []int64{1, 9}},
		{"AgeNotIn", &personWhere{AgeNotIn: []int64{91, 99}}, except(1, 9)},
		{"AgeGT", &personWhere{AgeGT: i64p(97)}, []int64{1, 2}},
		{"AgeGTE", &personWhere{AgeGTE: i64p(97)}, []int64{1, 2, 3}},
		{"AgeLT", &personWhere{AgeLT: i64p(92)}, []int64{9, 10}},
		{"AgeLTE", &personWhere{AgeLTE: i64p(92)}, []int64{8, 9, 10}},
		// An empty list is a list with nothing in it, not an absent one.
		{"NameIn []", &personWhere{NameIn: []string{}}, nil},
		{"NameNotIn []", &personWhere{NameNotIn: []string{}}, all},
		{"IDIn []", &personWhere{IDIn: []string{}}, nil},
		{"Is true", &personWhere{IsA: boolp(true)}, []int64{2, 4}},
		{"Is false", &personWhere{IsA: boolp(false)}, except(2, 4)},
		// Every predicate given holds.
		{"several", &personWhere{NameIn: []string{"b", "d"}, AgeLT: i64p(95), IDNEQ: strp(personID64(8))}, []int64{6, 9}},
	} {
		if got := where(t, db, c.w); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestWhereLogic: not, and, or, their nesting, and what an input that says
// nothing means.
func TestWhereLogic(t *testing.T) {
	db, _ := peopleDB(t)
	for _, c := range []struct {
		name string
		w    *personWhere
		want []int64
	}{
		{"nil", nil, all},
		{"empty", &personWhere{}, all},
		{"and", &personWhere{And: []*personWhere{{Name: strp("b")}, {AgeGT: i64p(93)}}}, []int64{3, 6}},
		{"or", &personWhere{Or: []*personWhere{{Name: strp("a")}, {Name: strp("f")}}}, []int64{2, 4, 10}},
		{"or of one", &personWhere{Or: []*personWhere{{Name: strp("a")}}}, []int64{2, 4}},
		{"not", &personWhere{Not: &personWhere{Name: strp("b")}}, except(3, 6, 8)},
		{"nested", &personWhere{Not: &personWhere{Or: []*personWhere{
			{Name: strp("a")},
			{And: []*personWhere{{NameIn: []string{"b", "d"}}, {AgeLT: i64p(93)}}},
		}}}, []int64{1, 3, 5, 6, 7, 10}},
		{"fields and logic", &personWhere{NameIn: []string{"a", "b"}, Or: []*personWhere{{Age: i64p(98)}, {Age: i64p(92)}}}, []int64{2, 8}},
		// An empty and or or list says nothing: no condition.
		{"and []", &personWhere{And: []*personWhere{}}, all},
		{"or []", &personWhere{Or: []*personWhere{}}, all},
		// An empty input holds for every row: so not of it for none, and an
		// or with it for every row.
		{"not {}", &personWhere{Not: &personWhere{}}, nil},
		{"or with {}", &personWhere{Or: []*personWhere{{}, {Name: strp("a")}}}, all},
		{"and with {}", &personWhere{And: []*personWhere{{}, {Name: strp("a")}}}, []int64{2, 4}},
		// A nil entry, which gqlgen never gives, is an empty input.
		{"or with nil", &personWhere{Or: []*personWhere{nil, {Name: strp("a")}}}, all},
	} {
		if got := where(t, db, c.w); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// nest is w inside n levels of kind ("not", "and" or "or"), so that w is at
// depth n.
func nest(w *personWhere, n int, kind string) *personWhere {
	for range n {
		switch kind {
		case "not":
			w = &personWhere{Not: w}
		case "and":
			w = &personWhere{And: []*personWhere{w}}
		default:
			w = &personWhere{Or: []*personWhere{{Name: strp("x")}, w}}
		}
	}
	return w
}

// TestWhereDepthAndWidth is Review Focus 4: a where nested deeper than
// MaxFilterDepth, and an and or an or of more than MaxFilterWidth, fail
// before any statement runs.
func TestWhereDepthAndWidth(t *testing.T) {
	db, st := peopleDB(t)
	ctx := context.Background()
	for _, kind := range []string{"not", "and", "or"} {
		// Depth 5 is the most: the input at the bottom is at depth 5.
		want := []int64{2, 4}
		if kind == "not" {
			want = except(2, 4) // five nots
		}
		if got := where(t, db, nest(&personWhere{Name: strp("a")}, veloxgql.MaxFilterDepth, kind)); !slices.Equal(got, want) {
			t.Errorf("%s at depth 5: %v, want %v", kind, got, want)
		}
		st.take()
		_, err := nest(&personWhere{Name: strp("a")}, veloxgql.MaxFilterDepth+1, kind).Pred(ctx, 0)
		if !errors.Is(err, veloxgql.ErrFilterTooDeep) || !strings.HasPrefix(err.Error(), "veloxgql: PersonWhereInput: ") {
			t.Errorf("%s at depth 6: %v, want ErrFilterTooDeep", kind, err)
		}
		if ran := st.take(); len(ran) != 0 {
			t.Errorf("%s at depth 6 ran %v", kind, ran)
		}
	}
	// Pred's depth is where the input is.
	if _, err := (&personWhere{}).Pred(ctx, veloxgql.MaxFilterDepth); err != nil {
		t.Errorf("Pred at depth 5: %v", err)
	}
	if _, err := (&personWhere{}).Pred(ctx, veloxgql.MaxFilterDepth+1); !errors.Is(err, veloxgql.ErrFilterTooDeep) {
		t.Errorf("Pred at depth 6: %v, want ErrFilterTooDeep", err)
	}

	list := func(n int) []*personWhere {
		ws := make([]*personWhere, n)
		for i := range ws {
			ws[i] = &personWhere{Name: strp("a")}
		}
		return ws
	}
	if got := where(t, db, &personWhere{Or: list(veloxgql.MaxFilterWidth)}); !slices.Equal(got, []int64{2, 4}) {
		t.Errorf("or of 100: %v", got)
	}
	if got := where(t, db, &personWhere{And: list(veloxgql.MaxFilterWidth)}); !slices.Equal(got, []int64{2, 4}) {
		t.Errorf("and of 100: %v", got)
	}
	st.take()
	for name, w := range map[string]*personWhere{
		"or":        {Or: list(veloxgql.MaxFilterWidth + 1)},
		"and":       {And: list(veloxgql.MaxFilterWidth + 1)},
		"nested or": {Not: &personWhere{Or: list(veloxgql.MaxFilterWidth + 1)}},
	} {
		_, err := w.Pred(ctx, 0)
		if !errors.Is(err, veloxgql.ErrFilterTooWide) || !strings.HasPrefix(err.Error(), "veloxgql: PersonWhereInput: ") {
			t.Errorf("%s of 101: %v, want ErrFilterTooWide", name, err)
		}
	}
	if ran := st.take(); len(ran) != 0 {
		t.Errorf("the refused filters ran %v", ran)
	}
}

// TestWhereIDs is Review Focus 1 for where: an ID of another type, in any
// ID predicate, fails with ErrWrongIDType; a malformed one with ErrBadID.
// Neither runs a statement, and neither error holds the key.
func TestWhereIDs(t *testing.T) {
	_, st := peopleDB(t)
	ctx := context.Background()
	post := veloxgql.EncodeID("Post", int64(3))
	for name, w := range map[string]*personWhere{
		"ID":            {ID: &post},
		"IDNEQ":         {IDNEQ: &post},
		"IDIn":          {IDIn: []string{personID64(1), post}},
		"IDNotIn":       {IDNotIn: []string{post}},
		"nested":        {Or: []*personWhere{{Name: strp("a")}, {ID: &post}}},
		"under a not":   {Not: &personWhere{IDIn: []string{post}}},
		"after a field": {Name: strp("a"), IDNotIn: []string{personID64(2), post}},
	} {
		_, err := w.Pred(ctx, 0)
		if !errors.Is(err, veloxgql.ErrWrongIDType) {
			t.Errorf("%s: %v, want ErrWrongIDType", name, err)
		}
		if err != nil && strings.Contains(err.Error(), "3") {
			t.Errorf("%s: the error holds the key: %v", name, err)
		}
	}
	bad := "not an ID"
	for name, w := range map[string]*personWhere{
		"ID":     {ID: &bad},
		"IDIn":   {IDIn: []string{bad}},
		"string": {ID: strp(veloxgql.EncodeID("Person", "x"))},
	} {
		if _, err := w.Pred(ctx, 0); !errors.Is(err, veloxgql.ErrBadID) {
			t.Errorf("%s: %v, want ErrBadID", name, err)
		}
	}
	if ran := st.take(); len(ran) != 0 {
		t.Errorf("the refused IDs ran %v", ran)
	}
}

// TestWhereConversions: the conversions from an input's Go type to a
// column's.
func TestWhereConversions(t *testing.T) {
	type (
		code  string
		small int16
		ratio float32
		flag  bool
		ref   [16]byte
	)
	if got := veloxgql.AsString[code]("x"); got != code("x") {
		t.Errorf("AsString: %v", got)
	}
	if got := veloxgql.AsInt[small](int64(7)); got != small(7) {
		t.Errorf("AsInt: %v", got)
	}
	if got := veloxgql.AsFloat[ratio](float32(1.5)); got != ratio(1.5) {
		t.Errorf("AsFloat: %v", got)
	}
	if got := veloxgql.AsBool[flag](true); got != flag(true) {
		t.Errorf("AsBool: %v", got)
	}
	if got := veloxgql.AsUUID[ref]([16]byte{1, 2}); got != (ref{1, 2}) {
		t.Errorf("AsUUID: %v", got)
	}
}
