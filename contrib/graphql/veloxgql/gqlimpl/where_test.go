package gqlimpl_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
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
	IDGT    *string

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

func (w *personWhere) Pred(ctx context.Context) (velox.Pred[person], error) {
	f := gqlimpl.NewFilter[person](ctx, "PersonWhereInput")
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Not(f, "not", w.Not)
	gqlimpl.And(f, "and", w.And)
	gqlimpl.Or(f, "or", w.Or)
	gqlimpl.ID(f, "id", w.ID, "Person", people.ID.EQ)
	gqlimpl.IDAs(f, "idNEQ", w.IDNEQ, "Person", gqlimpl.AsInt[personID, int64], peopleAs.ID.NEQ)
	gqlimpl.IDList(f, "idIn", w.IDIn, "Person", people.ID.In)
	gqlimpl.IDListAs(f, "idNotIn", w.IDNotIn, "Person", gqlimpl.AsInt[personID, int64], peopleAs.ID.NotIn)
	gqlimpl.ID(f, "idGT", w.IDGT, "Person", people.ID.GT)
	gqlimpl.Value(f, "name", w.Name, people.Name.EQ)
	gqlimpl.Value(f, "nameNEQ", w.NameNEQ, people.Name.NEQ)
	gqlimpl.List(f, "nameIn", w.NameIn, people.Name.In)
	gqlimpl.List(f, "nameNotIn", w.NameNotIn, people.Name.NotIn)
	gqlimpl.Value(f, "nameGT", w.NameGT, people.Name.GT)
	gqlimpl.Value(f, "nameContains", w.NameContains, people.Name.Contains)
	gqlimpl.Value(f, "nameHasPrefix", w.NameHasPrefix, people.Name.HasPrefix)
	gqlimpl.Value(f, "nameHasSuffix", w.NameHasSuffix, people.Name.HasSuffix)
	gqlimpl.ValueAs(f, "label", w.Label, gqlimpl.AsString[personName, string], peopleAs.Name.EQ)
	gqlimpl.ListAs(f, "labelNotIn", w.LabelNotIn, gqlimpl.AsString[personName, string], peopleAs.Name.NotIn)
	gqlimpl.Value(f, "age", w.Age, people.Age.EQ)
	gqlimpl.Value(f, "ageNEQ", w.AgeNEQ, people.Age.NEQ)
	gqlimpl.List(f, "ageIn", w.AgeIn, people.Age.In)
	gqlimpl.List(f, "ageNotIn", w.AgeNotIn, people.Age.NotIn)
	gqlimpl.Value(f, "ageGT", w.AgeGT, people.Age.GT)
	gqlimpl.Value(f, "ageGTE", w.AgeGTE, people.Age.GTE)
	gqlimpl.Value(f, "ageLT", w.AgeLT, people.Age.LT)
	gqlimpl.Value(f, "ageLTE", w.AgeLTE, people.Age.LTE)
	gqlimpl.Is(f, "isA", w.IsA, isA)
	return f.Pred()
}

func isA() velox.Pred[person] { return people.Name.EQ("a") }

func i64p(n int64) *int64 { return new(n) }

func personID64(id int64) string { return veloxgql.EncodeID("Person", id) }

// where returns the keys of the people w selects, in key order, and fails
// the test on an error.
func where(t *testing.T, db *velox.DB, w *personWhere) []int64 {
	t.Helper()
	ctx := context.Background()
	p, err := w.Pred(ctx)
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
		{"ID", &personWhere{ID: new(personID64(3))}, []int64{3}},
		{"IDNEQ", &personWhere{IDNEQ: new(personID64(3))}, except(3)},
		{"IDIn", &personWhere{IDIn: []string{personID64(3), personID64(5)}}, []int64{3, 5}},
		{"IDNotIn", &personWhere{IDNotIn: []string{personID64(3), personID64(5)}}, except(3, 5)},
		{"Name", &personWhere{Name: new("b")}, []int64{3, 6, 8}},
		{"NameNEQ", &personWhere{NameNEQ: new("b")}, except(3, 6, 8)},
		{"NameIn", &personWhere{NameIn: []string{"a", "f"}}, []int64{2, 4, 10}},
		{"NameNotIn", &personWhere{NameNotIn: []string{"a", "f"}}, except(2, 4, 10)},
		{"NameGT", &personWhere{NameGT: new("d")}, []int64{7, 10}},
		{"NameContains", &personWhere{NameContains: new("e")}, []int64{7}},
		{"NameHasPrefix", &personWhere{NameHasPrefix: new("d")}, []int64{5, 9}},
		{"NameHasSuffix", &personWhere{NameHasSuffix: new("c")}, []int64{1}},
		// LIKE's wildcards are the client's text, not patterns.
		{"NameContains %", &personWhere{NameContains: new("%")}, nil},
		{"NameHasPrefix _", &personWhere{NameHasPrefix: new("_")}, nil},
		{"Label", &personWhere{Label: new("d")}, []int64{5, 9}},
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
		{"Is true", &personWhere{IsA: new(true)}, []int64{2, 4}},
		{"Is false", &personWhere{IsA: new(false)}, except(2, 4)},
		// Every predicate given holds.
		{"several", &personWhere{NameIn: []string{"b", "d"}, AgeLT: i64p(95), IDNEQ: new(personID64(8))}, []int64{6, 9}},
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
		{"and", &personWhere{And: []*personWhere{{Name: new("b")}, {AgeGT: i64p(93)}}}, []int64{3, 6}},
		{"or", &personWhere{Or: []*personWhere{{Name: new("a")}, {Name: new("f")}}}, []int64{2, 4, 10}},
		{"or of one", &personWhere{Or: []*personWhere{{Name: new("a")}}}, []int64{2, 4}},
		{"not", &personWhere{Not: &personWhere{Name: new("b")}}, except(3, 6, 8)},
		{"nested", &personWhere{Not: &personWhere{Or: []*personWhere{
			{Name: new("a")},
			{And: []*personWhere{{NameIn: []string{"b", "d"}}, {AgeLT: i64p(93)}}},
		}}}, []int64{1, 3, 5, 6, 7, 10}},
		{"fields and logic", &personWhere{NameIn: []string{"a", "b"}, Or: []*personWhere{{Age: i64p(98)}, {Age: i64p(92)}}}, []int64{2, 8}},
		// An empty and or or list says nothing: no condition.
		{"and []", &personWhere{And: []*personWhere{}}, all},
		{"or []", &personWhere{Or: []*personWhere{}}, all},
		// An empty input holds for every row: so not of it for none, and an
		// or with it for every row.
		{"not {}", &personWhere{Not: &personWhere{}}, nil},
		{"or with {}", &personWhere{Or: []*personWhere{{}, {Name: new("a")}}}, all},
		{"and with {}", &personWhere{And: []*personWhere{{}, {Name: new("a")}}}, []int64{2, 4}},
		// A nil entry, which gqlgen never gives, is an empty input.
		{"or with nil", &personWhere{Or: []*personWhere{nil, {Name: new("a")}}}, all},
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
			w = &personWhere{Or: []*personWhere{{Name: new("x")}, w}}
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
		if got := where(t, db, nest(&personWhere{Name: new("a")}, veloxgql.DefaultLimits().MaxFilterDepth, kind)); !slices.Equal(got, want) {
			t.Errorf("%s at depth 5: %v, want %v", kind, got, want)
		}
		st.take()
		_, err := nest(&personWhere{Name: new("a")}, veloxgql.DefaultLimits().MaxFilterDepth+1, kind).Pred(ctx)
		if !errors.Is(err, veloxgql.ErrFilterTooDeep) || !strings.HasPrefix(err.Error(), "veloxgql: PersonWhereInput") {
			t.Errorf("%s at depth 6: %v, want ErrFilterTooDeep", kind, err)
		}
		if ran := st.take(); len(ran) != 0 {
			t.Errorf("%s at depth 6 ran %v", kind, ran)
		}
	}
	list := func(n int) []*personWhere {
		ws := make([]*personWhere, n)
		for i := range ws {
			ws[i] = &personWhere{Name: new("a")}
		}
		return ws
	}
	if got := where(t, db, &personWhere{Or: list(veloxgql.DefaultLimits().MaxFilterWidth)}); !slices.Equal(got, []int64{2, 4}) {
		t.Errorf("or of 100: %v", got)
	}
	if got := where(t, db, &personWhere{And: list(veloxgql.DefaultLimits().MaxFilterWidth)}); !slices.Equal(got, []int64{2, 4}) {
		t.Errorf("and of 100: %v", got)
	}
	st.take()
	for name, w := range map[string]*personWhere{
		"or":        {Or: list(veloxgql.DefaultLimits().MaxFilterWidth + 1)},
		"and":       {And: list(veloxgql.DefaultLimits().MaxFilterWidth + 1)},
		"nested or": {Not: &personWhere{Or: list(veloxgql.DefaultLimits().MaxFilterWidth + 1)}},
	} {
		_, err := w.Pred(ctx)
		if !errors.Is(err, veloxgql.ErrFilterTooWide) || !strings.HasPrefix(err.Error(), "veloxgql: PersonWhereInput") {
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
		"IDGT":          {IDGT: &post},
		"nested":        {Or: []*personWhere{{Name: new("a")}, {ID: &post}}},
		"under a not":   {Not: &personWhere{IDIn: []string{post}}},
		"after a field": {Name: new("a"), IDNotIn: []string{personID64(2), post}},
	} {
		_, err := w.Pred(ctx)
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
		"IDGT":   {IDGT: &bad},
		"string": {ID: new(veloxgql.EncodeID("Person", "x"))},
	} {
		if _, err := w.Pred(ctx); !errors.Is(err, veloxgql.ErrBadID) {
			t.Errorf("%s: %v, want ErrBadID", name, err)
		}
	}
	if ran := st.take(); len(ran) != 0 {
		t.Errorf("the refused IDs ran %v", ran)
	}
}

// TestWhereKeyOps: the key's ordered predicate compares keys, and an empty
// idIn, a list given with no ID, selects no row, as an empty In does.
func TestWhereKeyOps(t *testing.T) {
	db, _ := peopleDB(t)
	if got := where(t, db, &personWhere{IDGT: new(personID64(7))}); !slices.Equal(got, []int64{8, 9, 10}) {
		t.Errorf("idGT 7: %v, want [8 9 10]", got)
	}
	if got := where(t, db, &personWhere{IDIn: []string{}}); len(got) != 0 {
		t.Errorf("an empty idIn: %v, want no row", got)
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
	if got := gqlimpl.AsString[code]("x"); got != code("x") {
		t.Errorf("AsString: %v", got)
	}
	if got := gqlimpl.AsInt[small](int64(7)); got != small(7) {
		t.Errorf("AsInt: %v", got)
	}
	if got := gqlimpl.AsFloat[ratio](float32(1.5)); got != ratio(1.5) {
		t.Errorf("AsFloat: %v", got)
	}
	if got := gqlimpl.AsBool[flag](true); got != flag(true) {
		t.Errorf("AsBool: %v", got)
	}
	if got := gqlimpl.AsUUID[ref]([16]byte{1, 2}); got != (ref{1, 2}) {
		t.Errorf("AsUUID: %v", got)
	}
}

// shared is a where argument that holds leaf 100 times at each of depth
// levels, the one input everywhere, as gqlgen unmarshals a GraphQL variable
// used at many places: 100^depth inputs, from a request of a few kilobytes.
func shared(leaf *personWhere, depth int) *personWhere {
	w := leaf
	for range depth {
		ws := make([]*personWhere, veloxgql.DefaultLimits().MaxFilterWidth)
		for i := range ws {
			ws[i] = w
		}
		w = &personWhere{Or: ws}
	}
	return w
}

// TestWhereBudget is Review Focus 4 for the whole argument: MaxFilterNodes
// bounds the inputs, predicates and In and NotIn values of one where
// argument, however its lists and depth multiply, and over it the filter
// fails with ErrFilterTooLarge at once, before any statement runs.
func TestWhereBudget(t *testing.T) {
	db, st := peopleDB(t)
	ctx := context.Background()
	st.take()
	w := shared(&personWhere{NameIn: []string{"a", "b"}, AgeGT: i64p(1)}, veloxgql.DefaultLimits().MaxFilterDepth)
	start := time.Now()
	_, err := w.Pred(ctx)
	if !errors.Is(err, veloxgql.ErrFilterTooLarge) || !strings.Contains(err.Error(), "more than 1000") {
		t.Fatalf("100^5 inputs: %v, want ErrFilterTooLarge over the budget", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("100^5 inputs took %v to refuse", d)
	}
	if ran := st.take(); len(ran) != 0 {
		t.Errorf("the refused filter ran %v", ran)
	}
	// It stops after about MaxFilterNodes nodes: a few allocations each.
	if n := testing.AllocsPerRun(5, func() { _, _ = w.Pred(ctx) }); n > 20*float64(veloxgql.DefaultLimits().MaxFilterNodes) {
		t.Errorf("refusing 100^5 inputs allocates %v times", n)
	}

	// The count, to the last one: the input, its or and its ageIn with its
	// values; and 9 inputs in the or, each with a nameIn of 98 values. Each
	// input, predicate and value counts one: 1 + 1 + (1+97) + 9*(1+1+98) =
	// 1000.
	in := func(n int) []int64 {
		vs := make([]int64, n)
		for i := range vs {
			vs[i] = int64(100 - i) // every age, 90 to 99, among them
		}
		return vs
	}
	names := make([]string, 98)
	for i := range names {
		names[i] = string(rune('a' + i%26))
	}
	budget := func(ages int) *personWhere {
		ws := make([]*personWhere, 9)
		for i := range ws {
			ws[i] = &personWhere{NameIn: names}
		}
		return &personWhere{Or: ws, AgeIn: in(ages)}
	}
	if got := where(t, db, budget(97)); !slices.Equal(got, all) {
		t.Errorf("a budget of 1000: %v", got)
	}
	if _, err := budget(98).Pred(ctx); !errors.Is(err, veloxgql.ErrFilterTooLarge) {
		t.Errorf("1001: %v, want ErrFilterTooLarge", err)
	}
	// Each where argument has its own budget: a context the application
	// holds starts one, and a nested input's context continues the one it
	// came from.
	f := gqlimpl.NewFilter[person](ctx, "PersonWhereInput")
	for range 9 {
		gqlimpl.List(f, "ageIn", in(100), people.Age.In) // 909 of 1000
	}
	spy := &ctxSpy{}
	gqlimpl.Not(f, "not", spy)
	if _, err := f.Pred(); err != nil || spy.ctx == nil {
		t.Fatalf("909: %v", err)
	}
	if _, err := budget(97).Pred(ctx); err != nil {
		t.Errorf("a second argument of 1000: %v", err)
	}
	// A nested input shares it.
	if _, err := budget(97).Pred(spy.ctx); !errors.Is(err, veloxgql.ErrFilterTooLarge) {
		t.Errorf("a nested input of 1000 after 909: %v, want ErrFilterTooLarge", err)
	}
}

// ctxSpy is a where input that holds for every row, and keeps the context
// its Pred is called with.
type ctxSpy struct{ ctx context.Context }

func (s *ctxSpy) Pred(ctx context.Context) (velox.Pred[person], error) {
	s.ctx = ctx
	return velox.And[person](), nil
}

// TestWhereValueLists: an In or a NotIn list of more than MaxFilterWidth
// values fails with ErrFilterTooWide, naming its field.
func TestWhereValueLists(t *testing.T) {
	_, st := peopleDB(t)
	ctx := context.Background()
	st.take()
	many := make([]string, veloxgql.DefaultLimits().MaxFilterWidth+1)
	for i := range many {
		many[i] = personID64(int64(i + 1))
	}
	ages := make([]int64, veloxgql.DefaultLimits().MaxFilterWidth+1)
	for name, c := range map[string]struct {
		w     *personWhere
		field string
	}{
		"In":       {&personWhere{NameIn: many}, "nameIn"},
		"NotIn":    {&personWhere{AgeNotIn: ages}, "ageNotIn"},
		"ListAs":   {&personWhere{LabelNotIn: many}, "labelNotIn"},
		"IDList":   {&personWhere{IDIn: many}, "idIn"},
		"IDListAs": {&personWhere{IDNotIn: many}, "idNotIn"},
	} {
		_, err := c.w.Pred(ctx)
		if !errors.Is(err, veloxgql.ErrFilterTooWide) || !strings.HasPrefix(err.Error(), "veloxgql: PersonWhereInput."+c.field+": ") {
			t.Errorf("%s of 101: %v, want ErrFilterTooWide of %s", name, err, c.field)
		}
	}
	if ran := st.take(); len(ran) != 0 {
		t.Errorf("the refused lists ran %v", ran)
	}
	// 100 is the most.
	if _, err := (&personWhere{IDIn: many[:veloxgql.DefaultLimits().MaxFilterWidth]}).Pred(ctx); err != nil {
		t.Errorf("100 IDs: %v", err)
	}
}

// TestWhereErrorsNameTheField: an error names the path of fields that held
// its cause, and never the key.
func TestWhereErrorsNameTheField(t *testing.T) {
	ctx := context.Background()
	post := veloxgql.EncodeID("Post", int64(3))
	for _, c := range []struct {
		w    *personWhere
		want string
	}{
		{&personWhere{ID: &post}, "veloxgql: PersonWhereInput.id: Person ID: ID of another type"},
		{&personWhere{Or: []*personWhere{{}, {IDNotIn: []string{post}}}}, "veloxgql: PersonWhereInput.or[1]: PersonWhereInput.idNotIn: Person ID: ID of another type"},
		{&personWhere{Not: &personWhere{And: []*personWhere{{IDNEQ: new("x")}}}}, "veloxgql: PersonWhereInput.not: PersonWhereInput.and[0]: PersonWhereInput.idNEQ: Person ID: malformed ID: not canonical base64url without padding"},
		{nest(&personWhere{}, veloxgql.DefaultLimits().MaxFilterDepth+1, "not"), "veloxgql: PersonWhereInput.not: PersonWhereInput.not: PersonWhereInput.not: PersonWhereInput.not: PersonWhereInput.not: PersonWhereInput.not: PersonWhereInput: filter is nested too deep: it is nested 6 deep, and the most is 5"},
	} {
		if _, err := c.w.Pred(ctx); err == nil || err.Error() != c.want {
			t.Errorf("%v, want %q", err, c.want)
		}
	}
}

// TestFilterBudgetError: a where argument over its MaxFilterNodes fails
// with ErrFilterTooLarge, "filter is too large", and a list over
// MaxFilterWidth still with ErrFilterTooWide. The limits are the
// extension's, read at the top of the argument, and its nested inputs have
// the same.
func TestFilterBudgetError(t *testing.T) {
	ctx := context.Background()
	names := func(n int) []string { return make([]string, n) }
	ws := make([]*personWhere, 10)
	for i := range ws {
		ws[i] = &personWhere{NameIn: names(99)}
	}
	_, err := (&personWhere{Or: ws}).Pred(ctx) // 1 + 1 + 10*(1+1+99) > 1000
	if !errors.Is(err, veloxgql.ErrFilterTooLarge) || !strings.Contains(err.Error(), "filter is too large") {
		t.Errorf("over MaxFilterNodes: %v, want ErrFilterTooLarge", err)
	}
	if errors.Is(err, veloxgql.ErrFilterTooWide) {
		t.Errorf("over MaxFilterNodes: %v, is ErrFilterTooWide too", err)
	}
	_, err = (&personWhere{NameIn: names(101)}).Pred(ctx)
	if !errors.Is(err, veloxgql.ErrFilterTooWide) || errors.Is(err, veloxgql.ErrFilterTooLarge) {
		t.Errorf("a list over MaxFilterWidth: %v, want ErrFilterTooWide alone", err)
	}

	// Under an extension's limits, at the top and in a nested input.
	small := veloxgql.NewExtension(&velox.DB{}, veloxgql.WithLimits(veloxgql.Limits{MaxFilterWidth: 2, MaxFilterDepth: 1, MaxFilterNodes: 5})).NewContext(ctx)
	if _, err := (&personWhere{NameIn: names(3)}).Pred(small); !errors.Is(err, veloxgql.ErrFilterTooWide) {
		t.Errorf("3 values under MaxFilterWidth 2: %v, want ErrFilterTooWide", err)
	}
	if _, err := (&personWhere{Not: &personWhere{Not: &personWhere{}}}).Pred(small); !errors.Is(err, veloxgql.ErrFilterTooDeep) {
		t.Errorf("depth 2 under MaxFilterDepth 1: %v, want ErrFilterTooDeep", err)
	}
	if _, err := (&personWhere{NameIn: names(2), AgeIn: []int64{1, 2}}).Pred(small); !errors.Is(err, veloxgql.ErrFilterTooLarge) {
		t.Errorf("7 nodes under MaxFilterNodes 5: %v, want ErrFilterTooLarge", err)
	}
	f := gqlimpl.NewFilter[person](small, "PersonWhereInput")
	spy := &ctxSpy{}
	gqlimpl.Not(f, "not", spy)
	if _, err := f.Pred(); err != nil {
		t.Fatal(err)
	}
	if _, err := (&personWhere{NameIn: names(3)}).Pred(spy.ctx); !errors.Is(err, veloxgql.ErrFilterTooWide) {
		t.Errorf("a nested input's 3 values under MaxFilterWidth 2: %v, want ErrFilterTooWide", err)
	}
}

// TestPredDepthFromContext: the depth travels in the context. An input
// nested MaxFilterDepth deep passes, and one level deeper fails.
func TestPredDepthFromContext(t *testing.T) {
	ctx := context.Background()
	most := veloxgql.DefaultLimits().MaxFilterDepth
	if _, err := nest(&personWhere{}, most, "not").Pred(ctx); err != nil {
		t.Errorf("nested %d deep: %v", most, err)
	}
	_, err := nest(&personWhere{}, most+1, "not").Pred(ctx)
	if !errors.Is(err, veloxgql.ErrFilterTooDeep) || !strings.Contains(err.Error(), "it is nested 6 deep, and the most is 5") {
		t.Errorf("nested %d deep: %v, want ErrFilterTooDeep", most+1, err)
	}
}

// TestPredTwiceOnOneContext: an application calls Pred twice with one
// context, and each call has a full budget at depth 0. The input costs 510
// nodes (1 + 101 + 101 + 3×102 + 1), more than half of the budget of 600
// the context gives, so a second call that spent the first's would fail.
func TestPredTwiceOnOneContext(t *testing.T) {
	limits := func(nodes int) context.Context {
		return veloxgql.NewExtension(&velox.DB{}, veloxgql.WithLimits(veloxgql.Limits{MaxFilterNodes: nodes})).NewContext(context.Background())
	}
	in := func(n int) []int64 { return make([]int64, n) }
	w := &personWhere{AgeIn: in(100), AgeNotIn: in(100), Or: []*personWhere{{AgeIn: in(100)}, {AgeIn: in(100)}, {AgeIn: in(100)}}}
	// The cost is what the comment says: it fits 510, not 509.
	if _, err := w.Pred(limits(510)); err != nil {
		t.Fatalf("under a budget of 510: %v", err)
	}
	if _, err := w.Pred(limits(509)); !errors.Is(err, veloxgql.ErrFilterTooLarge) {
		t.Fatalf("under a budget of 509: %v, want ErrFilterTooLarge", err)
	}
	ctx := limits(600)
	for i := range 3 {
		if _, err := w.Pred(ctx); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	// The context is not changed by a call, so a repeat costs what a first
	// call does.
	onCtx := testing.AllocsPerRun(100, func() { _, _ = w.Pred(ctx) })
	fresh := testing.AllocsPerRun(100, func() { _, _ = w.Pred(context.Background()) })
	if onCtx != fresh {
		t.Errorf("Pred allocates %v times on a used context, %v on a fresh one", onCtx, fresh)
	}
	// A nested input's Pred, called directly, starts at depth 0.
	deep := nest(&personWhere{}, veloxgql.DefaultLimits().MaxFilterDepth, "not")
	for i := range 2 {
		if _, err := deep.Pred(ctx); err != nil {
			t.Fatalf("direct call %d: %v", i+1, err)
		}
	}
	if _, err := deep.Not.Pred(ctx); err != nil {
		t.Errorf("a nested input called directly: %v", err)
	}
}

// TestPredErrorPath: an error of a nested input names the path to it.
func TestPredErrorPath(t *testing.T) {
	post := veloxgql.EncodeID("Post", int64(3))
	w := &personWhere{Or: []*personWhere{{}, {Not: &personWhere{IDIn: []string{post}}}}}
	_, err := w.Pred(context.Background())
	want := "veloxgql: PersonWhereInput.or[1]: PersonWhereInput.not: PersonWhereInput.idIn: Person ID: ID of another type"
	if err == nil || err.Error() != want {
		t.Errorf("%v, want %q", err, want)
	}
}
