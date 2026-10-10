package orm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A where input as the generator writes one, with a field of each kind of
// Go type the decoder has a rule for.
type (
	decPred  P
	decColor string
	// decSelf reads itself: its UnmarshalGQL is the rule, whatever the plan
	// says of its names.
	decSelf string
	// decMoney reads itself from text, as a decimal does.
	decMoney struct{ cents int64 }
)

func (s *decSelf) UnmarshalGQL(v any) error {
	str, ok := v.(string)
	if !ok {
		return fmt.Errorf("decSelf from %T", v)
	}
	*s = decSelf("self:" + str)
	return nil
}

func (m *decMoney) UnmarshalText(b []byte) error {
	whole, frac, _ := strings.Cut(string(b), ".")
	var w, f int64
	if _, err := fmt.Sscanf(whole, "%d", &w); err != nil {
		return fmt.Errorf("%q is not an amount", b)
	}
	if frac != "" {
		if _, err := fmt.Sscanf((frac + "00")[:2], "%d", &f); err != nil {
			return fmt.Errorf("%q is not an amount", b)
		}
	}
	m.cents = w*100 + f
	return nil
}

func (m decMoney) MarshalText() ([]byte, error) {
	return fmt.Appendf(nil, "%d.%02d", m.cents/100, m.cents%100), nil
}

type decChild struct {
	Predicates []decPred   `json:"-"`
	Not        *decChild   `json:"not,omitempty"`
	Or         []*decChild `json:"or,omitempty"`
	And        []*decChild `json:"and,omitempty"`

	Title *string `json:"title,omitempty"`
}

type decInput struct {
	Predicates []decPred   `json:"-"`
	Not        *decInput   `json:"not,omitempty"`
	Or         []*decInput `json:"or,omitempty"`
	And        []*decInput `json:"and,omitempty"`

	ID          *int64      `json:"id,omitempty"`
	IDIn        []int64     `json:"idIn,omitempty"`
	Small       *uint8      `json:"small,omitempty"`
	Big         *uint64     `json:"big,omitempty"`
	SmallIn     []uint8     `json:"smallIn,omitempty"`
	Name        *string     `json:"name,omitempty"`
	NameIsNil   bool        `json:"nameIsNil,omitempty"`
	Color       *decColor   `json:"color,omitempty"`
	ColorIn     []decColor  `json:"colorIn,omitempty"`
	Self        *decSelf    `json:"self,omitempty"`
	At          *time.Time  `json:"at,omitempty"`
	Price       *decMoney   `json:"price,omitempty"`
	PriceIn     []decMoney  `json:"priceIn,omitempty"`
	Ratio       *float64    `json:"ratio,omitempty"`
	Active      *bool       `json:"active,omitempty"`
	Data        *[]byte     `json:"data,omitempty"`
	UID         *uuid.UUID  `json:"uid,omitempty"`
	HasKids     *bool       `json:"hasKids,omitempty"`
	HasKidsWith []*decChild `json:"hasKidsWith,omitempty"`
}

var (
	decColors    = map[string]string{"RED": "red", "DARK_BLUE": "dark-blue"}
	decKids      = &EdgeMeta{}
	decChildPlan = []WhereField{{Col: "title", Op: OpEQ}}
	decPlan      = []WhereField{
		{Col: "id", Op: OpEQ},
		{Col: "id", Op: OpIn},
		{Col: "small", Op: OpEQ},
		{Col: "big", Op: OpEQ},
		{Col: "small", Op: OpIn},
		{Col: "name", Op: OpEQ},
		{Col: "name", Op: OpIsNil},
		{Col: "color", Op: OpEQ, Enum: decColors},
		{Col: "color", Op: OpIn, Enum: decColors},
		{Col: "self", Op: OpEQ, Enum: decColors},
		{Col: "at", Op: OpEQ},
		{Col: "price", Op: OpEQ},
		{Col: "price", Op: OpIn},
		{Col: "ratio", Op: OpEQ},
		{Col: "active", Op: OpEQ},
		{Col: "data", Op: OpEQ},
		{Col: "uid", Op: OpEQ},
		{Op: OpHasEdge, Edge: decKids},
		{Op: OpHasEdge, Edge: decKids, With: true, Neighbor: decChildPlan},
	}
)

func decode(t *testing.T, doc map[string]any) *decInput {
	t.Helper()
	var in decInput
	if err := DecodeWhere(&in, decPlan, doc); err != nil {
		t.Fatalf("decoding %v: %v", doc, err)
	}
	return &in
}

func TestDecodeWhere_Values(t *testing.T) {
	// A 64-bit id as a client that keeps numbers as floats sends it: a
	// string, with every digit.
	if in := decode(t, map[string]any{"id": "9007199254740993"}); in.ID == nil || *in.ID != 9007199254740993 {
		t.Errorf("an id given as a string: %v", in.ID)
	}
	for _, v := range []any{42, int64(42), float64(42), json.Number("42")} {
		if in := decode(t, map[string]any{"id": v}); in.ID == nil || *in.ID != 42 {
			t.Errorf("an id given as %T: %v", v, in.ID)
		}
	}
	// One value where a list goes is a list of one; the elements of a list
	// are read like a single value each.
	if in := decode(t, map[string]any{"idIn": "7"}); !reflect.DeepEqual(in.IDIn, []int64{7}) {
		t.Errorf("one value for a list: %v", in.IDIn)
	}
	if in := decode(t, map[string]any{"idIn": []any{"1", 2, json.Number("3")}}); !reflect.DeepEqual(in.IDIn, []int64{1, 2, 3}) {
		t.Errorf("a list of ids: %v", in.IDIn)
	}
	// A list of small numbers has the Go type of bytes; it is a list
	// because the operator takes one.
	if in := decode(t, map[string]any{"smallIn": []any{1, 255}}); !reflect.DeepEqual(in.SmallIn, []uint8{1, 255}) {
		t.Errorf("a list of uint8: %v", in.SmallIn)
	}
	// The upper half of a uint64, which no int64 holds.
	for _, v := range []any{"18446744073709551615", json.Number("18446744073709551615"), uint64(18446744073709551615)} {
		if in := decode(t, map[string]any{"big": v}); in.Big == nil || *in.Big != 18446744073709551615 {
			t.Errorf("the largest uint64 given as %T: %v", v, in.Big)
		}
	}
	if in := decode(t, map[string]any{"big": 7}); in.Big == nil || *in.Big != 7 {
		t.Errorf("a uint64 given as an int: %v", in.Big)
	}
	if in := decode(t, map[string]any{"data": "aGk="}); in.Data == nil || string(*in.Data) != "hi" {
		t.Errorf("bytes: %v", in.Data)
	}

	// An enum by its name in the schema, or as the value stored.
	if in := decode(t, map[string]any{"color": "DARK_BLUE"}); in.Color == nil || *in.Color != "dark-blue" {
		t.Errorf("an enum by name: %v", in.Color)
	}
	if in := decode(t, map[string]any{"colorIn": []any{"RED", "dark-blue"}}); !reflect.DeepEqual(in.ColorIn, []decColor{"red", "dark-blue"}) {
		t.Errorf("enums by name and by value: %v", in.ColorIn)
	}
	// A type with an UnmarshalGQL of its own decides.
	if in := decode(t, map[string]any{"self": "RED"}); in.Self == nil || *in.Self != "self:RED" {
		t.Errorf("a type that reads itself: %v", in.Self)
	}

	at := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)
	if in := decode(t, map[string]any{"at": "2026-01-02T03:04:05.123456789Z"}); in.At == nil || !in.At.Equal(at) {
		t.Errorf("a time as a string: %v", in.At)
	}
	if in := decode(t, map[string]any{"at": at}); in.At == nil || !in.At.Equal(at) {
		t.Errorf("a time as a time: %v", in.At)
	}
	for _, v := range []any{"10.5", json.Number("10.50"), 10.5} {
		if in := decode(t, map[string]any{"price": v}); in.Price == nil || in.Price.cents != 1050 {
			t.Errorf("a type read from text, given as %T: %v", v, in.Price)
		}
	}
	if in := decode(t, map[string]any{"priceIn": []any{"1", "2.25"}}); !reflect.DeepEqual(in.PriceIn, []decMoney{{100}, {225}}) {
		t.Errorf("a list of them: %v", in.PriceIn)
	}
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	if in := decode(t, map[string]any{"uid": id.String()}); in.UID == nil || *in.UID != id {
		t.Errorf("a uuid: %v", in.UID)
	}
	if in := decode(t, map[string]any{"ratio": 2, "active": false, "nameIsNil": true, "hasKids": true}); in.Ratio == nil || *in.Ratio != 2 ||
		in.Active == nil || *in.Active || !in.NameIsNil || in.HasKids == nil || !*in.HasKids {
		t.Errorf("a float, a false that was given, two flags: %+v", in)
	}

	// null is an absent key.
	if in := decode(t, map[string]any{"name": nil, "idIn": nil, "not": nil, "hasKidsWith": nil}); !reflect.DeepEqual(in, &decInput{}) {
		t.Errorf("nulls: %+v", in)
	}
	var in decInput
	if err := DecodeWhere(&in, decPlan, nil); err != nil || !reflect.DeepEqual(in, decInput{}) {
		t.Errorf("no object at all: %v %+v", err, in)
	}
}

func TestDecodeWhere_Refuses(t *testing.T) {
	for _, c := range []struct {
		doc  any
		want string
	}{
		// Dropping a condition it cannot read would widen the query.
		{map[string]any{"title": "x"}, `decInput has no field "title"`},
		{map[string]any{"Predicates": []any{}}, `no field "Predicates"`},
		{map[string]any{"-": 1}, `no field "-"`},
		{[]any{}, "given as an object"},
		{map[string]any{"id": 42.5}, "id: 42.5 is not a whole number"},
		{map[string]any{"id": "4x"}, `id: "4x" is not a whole number`},
		{map[string]any{"id": true}, "id: expected a whole number, got bool"},
		{map[string]any{"small": 300}, "small: 300 overflows uint8"},
		{map[string]any{"small": -1}, "small: -1 overflows uint8"},
		{map[string]any{"small": "256"}, "small: 256 overflows uint8"},
		{map[string]any{"big": "18446744073709551616"}, `big: "18446744073709551616" is not a whole number`},
		{map[string]any{"big": "-1"}, "big: -1 overflows uint64"},
		{map[string]any{"idIn": []any{1, nil}}, "idIn[1]: null in a list"},
		{map[string]any{"idIn": []any{1, "b"}}, `idIn[1]: "b" is not a whole number`},
		{map[string]any{"name": 5}, "name: expected a string, got int"},
		// A name of another case is not that enum's: DRAFT is, Draft is not.
		{map[string]any{"color": "Red"}, `color: "Red" is not a value of orm.decColor`},
		{map[string]any{"colorIn": []any{"RED", "GREEN"}}, `colorIn[1]: "GREEN" is not a value`},
		{map[string]any{"color": 1}, "color: expected a string, got int"},
		{map[string]any{"at": "yesterday"}, `at: "yesterday" is not an RFC 3339 time`},
		{map[string]any{"at": 5}, "at: a time is given as an RFC 3339 string"},
		{map[string]any{"price": "ten"}, `price: "ten" is not an amount`},
		{map[string]any{"price": true}, "price: cannot read a orm.decMoney from bool"},
		{map[string]any{"uid": "nope"}, "uid: "},
		{map[string]any{"active": "yes"}, "active: expected a boolean, got string"},
		{map[string]any{"nameIsNil": 1}, "nameIsNil: expected a boolean, got int"},
		{map[string]any{"ratio": "2"}, "ratio: expected a number, got string"},
		{map[string]any{"not": "x"}, "not: orm: a where input is given as an object"},
		{map[string]any{"and": []any{map[string]any{"id": 1}, nil}}, "and[1]: null where an input goes"},
		{map[string]any{"or": []any{map[string]any{"nope": 1}}}, `or[0]: orm: decInput has no field "nope"`},
		{map[string]any{"hasKidsWith": []any{map[string]any{"name": "x"}}}, `hasKidsWith[0]: orm: decChild has no field "name"`},
	} {
		var in decInput
		err := DecodeWhere(&in, decPlan, c.doc)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: got %v, want an error with %q", c.doc, err, c.want)
		}
	}
	if err := DecodeWhere(decInput{}, decPlan, map[string]any{}); err == nil {
		t.Error("an input given by value has nowhere to be decoded into")
	}
	if err := DecodeWhere(&decInput{}, decPlan[1:], map[string]any{}); err == nil {
		t.Error("a plan of another input is not this one's")
	}
}

func TestDecodeWhere_Nested(t *testing.T) {
	in := decode(t, map[string]any{
		"not": map[string]any{"name": "a"},
		// One object where a list of them goes.
		"or": map[string]any{"id": 1},
		"and": []any{
			map[string]any{"color": "RED"},
			map[string]any{"hasKidsWith": map[string]any{"not": map[string]any{"title": "t"}}},
		},
	})
	if in.Not == nil || in.Not.Name == nil || *in.Not.Name != "a" {
		t.Errorf("not: %+v", in.Not)
	}
	if len(in.Or) != 1 || in.Or[0].ID == nil || *in.Or[0].ID != 1 {
		t.Errorf("or: %+v", in.Or)
	}
	if len(in.And) != 2 || *in.And[0].Color != "red" {
		t.Fatalf("and: %+v", in.And)
	}
	kids := in.And[1].HasKidsWith
	if len(kids) != 1 || kids[0].Not == nil || *kids[0].Not.Title != "t" {
		t.Errorf("the neighbors' input, through its own plan: %+v", kids)
	}
	// What was decoded is the input a caller in Go would have built.
	a, one, red, title := "a", int64(1), decColor("red"), "t"
	built := &decInput{
		Not: &decInput{Name: &a},
		Or:  []*decInput{{ID: &one}},
		And: []*decInput{{Color: &red}, {HasKidsWith: []*decChild{{Not: &decChild{Title: &title}}}}},
	}
	if got, want := Where(in, decPlan), Where(built, decPlan); got.Op == 0 || !reflect.DeepEqual(got, want) {
		t.Errorf("the predicate of the decoded input:\n got %+v\nwant %+v", got, want)
	}

	// Nesting is counted like Where counts it, through an edge as well.
	deep := func(n int, leaf map[string]any) map[string]any {
		for range n {
			leaf = map[string]any{"not": leaf}
		}
		return leaf
	}
	if err := DecodeWhere(&decInput{}, decPlan, deep(MaxWhereDepth, map[string]any{"id": 1})); err != nil {
		t.Errorf("%d levels: %v", MaxWhereDepth, err)
	}
	if err := DecodeWhere(&decInput{}, decPlan, deep(MaxWhereDepth+1, map[string]any{"id": 1})); !errors.Is(err, ErrWhereDepth) {
		t.Errorf("%d levels: %v", MaxWhereDepth+1, err)
	}
	through := deep(MaxWhereDepth, map[string]any{"hasKidsWith": map[string]any{"title": "t"}})
	if err := DecodeWhere(&decInput{}, decPlan, through); !errors.Is(err, ErrWhereDepth) {
		t.Errorf("one level more on the other side of an edge: %v", err)
	}
}

// EncodeWhere and DecodeWhere agree: an input written as a document and read
// back is the same input, which is what keeping a filter needs.
func TestEncodeWhere_ReadsBack(t *testing.T) {
	name, color, at, id := "n", decColor("dark-blue"), time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC), int64(1)<<62
	in := &decInput{
		Name: &name, Color: &color, At: &at, ID: &id, NameIsNil: true,
		IDIn:        []int64{1, 2},
		PriceIn:     []decMoney{{1050}},
		Not:         &decInput{ColorIn: []decColor{"red"}},
		HasKidsWith: []*decChild{{Title: &name}},
	}
	var b bytes.Buffer
	EncodeWhere(&b, in)
	dec := json.NewDecoder(&b)
	dec.UseNumber() // as a document store hands numbers over, without loss
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	var back decInput
	if err := DecodeWhere(&back, decPlan, doc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&back, in) {
		t.Errorf("read back:\n got %+v\nwant %+v", back, *in)
	}
}
