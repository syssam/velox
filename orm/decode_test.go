package orm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"reflect"
	"slices"
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

func (s decSelf) MarshalGQL(w io.Writer) {
	_, _ = fmt.Fprintf(w, "%q", strings.TrimPrefix(string(s), "self:"))
}

type (
	// decRatio is a float of a type of its own.
	decRatio float64
	// decLevel is text that checks what it reads.
	decLevel string
)

func (l *decLevel) UnmarshalText(b []byte) error {
	if s := string(b); s != "low" && s != "high" {
		return fmt.Errorf("%q is not a level", s)
	}
	*l = decLevel(b)
	return nil
}

func (l decLevel) MarshalText() ([]byte, error) { return []byte(l), nil }

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
		{map[string]any{"not": "x"}, "not: velox: a where input is given as an object"},
		{map[string]any{"and": []any{map[string]any{"id": 1}, nil}}, "and[1]: null where an input goes"},
		{map[string]any{"or": []any{map[string]any{"nope": 1}}}, `or[0]: velox: decInput has no field "nope"`},
		{map[string]any{"hasKidsWith": []any{map[string]any{"name": "x"}}}, `hasKidsWith[0]: velox: decChild has no field "name"`},
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
// back is the same input, which is what keeping a filter needs. The cases
// are the ones a writer that follows the struct's tags gets wrong.
func TestEncodeWhere_ReadsBack(t *testing.T) {
	name, color, self := "n", decColor("dark-blue"), decSelf("self:x")
	at := time.Date(2026, 5, 6, 7, 8, 9, 123456789, time.UTC)
	id, big, small, ratio, yes, no := int64(1)<<62+1, uint64(1)<<63+1, uint8(200), 0.1, true, false
	data, uid := []byte{0, 1, 2}, uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	for label, in := range map[string]*decInput{
		"nothing":         {},
		"a value each":    {Name: &name, Color: &color, Self: &self, At: &at, ID: &id, Big: &big, Small: &small, Ratio: &ratio, Active: &no, Data: &data, UID: &uid, NameIsNil: true, Price: &decMoney{1050}},
		"lists":           {IDIn: []int64{1, -(1<<53 + 1)}, PriceIn: []decMoney{{1050}}, ColorIn: []decColor{"red", "dark-blue"}},
		"a list of bytes": {SmallIn: []uint8{0xD3, 0x4D, 0x76}}, // base64 "0012" to a writer of JSON
		// Among no values is the condition nothing matches; left out, it
		// would be no condition.
		"an empty list":   {IDIn: []int64{}, ColorIn: []decColor{}},
		"nested":          {Not: &decInput{ColorIn: []decColor{"red"}}, Or: []*decInput{{Name: &name}, {Not: &decInput{ID: &id}}}, And: []*decInput{{HasKids: &yes}}},
		"through an edge": {HasKids: &no, HasKidsWith: []*decChild{{Title: &name}, {Not: &decChild{Title: &name}}}},
		// An input that fails the query as it is fails it read back too.
		"an empty input inside": {Not: &decInput{}},
	} {
		text, err := EncodeWhere(in, decPlan)
		if err != nil {
			t.Errorf("%s: %v", label, err)
			continue
		}
		// As the text, and as the object a reader that keeps numbers as
		// floats makes of it.
		var floats any
		if err := json.Unmarshal(text, &floats); err != nil {
			t.Errorf("%s: %s is not JSON: %v", label, text, err)
			continue
		}
		for how, doc := range map[string]any{"text": text, "object": floats} {
			var back decInput
			if err := DecodeWhere(&back, decPlan, doc); err != nil {
				t.Errorf("%s, read as %s from %s: %v", label, how, text, err)
				continue
			}
			if got, want := Where(&back, decPlan), Where(in, decPlan); !reflect.DeepEqual(got, want) {
				t.Errorf("%s, read as %s from %s:\n got %+v\nwant %+v", label, how, text, got, want)
			}
		}
	}

	// A time is the instant it was, whatever zone it was written in.
	there := time.Date(2026, 5, 6, 7, 8, 9, 5, time.FixedZone("", 8*3600))
	text, err := EncodeWhere(&decInput{At: &there}, decPlan)
	var back decInput
	if err == nil {
		err = DecodeWhere(&back, decPlan, text)
	}
	if err != nil || !back.At.Equal(there) {
		t.Errorf("%s read back as %v, %v", text, back.At, err)
	}

	// What the document looks like: names of the schema, an enum by its
	// name, an integer a float would round as digits.
	text, err = EncodeWhere(&decInput{Color: &color, ID: &id, IDIn: []int64{}, NameIsNil: true}, decPlan)
	if want := `{"id":"4611686018427387905","idIn":[],"nameIsNil":true,"color":"DARK_BLUE"}`; err != nil || string(text) != want {
		t.Errorf("got %s %v, want %s", text, err, want)
	}
	if text, err := EncodeWhere((*decInput)(nil), decPlan); err != nil || string(text) != "null" {
		t.Errorf("no input: %s %v", text, err)
	}
}

// What cannot be written is an error, and never a document with fewer
// conditions: read back, that would be a wider filter.
func TestEncodeWhere_Refuses(t *testing.T) {
	name, bogus := "n", decColor("purple")
	late, inf, nan := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), math.Inf(1), math.NaN()
	var none []byte
	for label, c := range map[string]struct {
		in   *decInput
		want string
	}{
		"conditions added in Go":    {&decInput{Name: &name, Predicates: []decPred{{Op: OpEQ, Col: "id", Val: 1}}}, "Predicates"},
		"and inside":                {&decInput{Not: &decInput{Predicates: []decPred{{Op: OpEQ, Col: "id", Val: 1}}}}, "not: velox: decInput holds conditions added in Go"},
		"a time with no form":       {&decInput{Name: &name, At: &late}, "at: a time in the year 10000"},
		"a float that is no number": {&decInput{Name: &name, Ratio: &inf}, "ratio: +Inf is not a number"},
		"NaN":                       {&decInput{Ratio: &nan}, "ratio: NaN is not a number"},
		"a value of no enum":        {&decInput{Color: &bogus}, `color: "purple" is not a value of orm.decColor`},
		"what JSON writes as null":  {&decInput{Name: &name, Data: &none}, "data: a []uint8 that JSON writes as null"},
		"a nil input in a list":     {&decInput{Or: []*decInput{nil}}, "or[0]: nil where an input goes"},
	} {
		text, err := EncodeWhere(c.in, decPlan)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %s, %v; want an error with %q", label, text, err, c.want)
		}
		// The writer with no error to return leaves a document that is
		// refused, with the reason.
		var b bytes.Buffer
		WriteWhere(&b, c.in, decPlan)
		var back decInput
		err = DecodeWhere(&back, decPlan, b.Bytes())
		if err == nil || !strings.Contains(err.Error(), "could not be written") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %s was read back as %+v, %v", label, b.String(), back, err)
		}
	}
	// Nesting without end: an input that holds itself.
	loop := &decInput{}
	loop.Not = loop
	if _, err := EncodeWhere(loop, decPlan); !errors.Is(err, ErrWhereDepth) {
		t.Errorf("an input that holds itself: %v", err)
	}
}

// Found by review: each of these was read without an error, as something
// other than what the document said.
func TestDecodeWhere_Strict(t *testing.T) {
	type pair struct{ A, B int }
	type odd struct {
		Predicates []decPred `json:"-"`
		Not        *odd      `json:"not,omitempty"`
		Or         []*odd    `json:"or,omitempty"`
		And        []*odd    `json:"and,omitempty"`

		Pair  *pair     `json:"pair,omitempty"`
		Arr   *[4]byte  `json:"arr,omitempty"`
		Ratio *float32  `json:"ratio,omitempty"`
		Named *decRatio `json:"named,omitempty"`
		Level *decLevel `json:"level,omitempty"`
	}
	plan := []WhereField{{Col: "pair", Op: OpEQ}, {Col: "arr", Op: OpEQ}, {Col: "ratio", Op: OpEQ}, {Col: "named", Op: OpEQ}, {Col: "level", Op: OpEQ}}
	var typedNil map[string]any
	for _, c := range []struct {
		doc  map[string]any
		want string
	}{
		// A key the value's type does not have, as one the input does not.
		{map[string]any{"pair": map[string]any{"a": 1, "c": 3}}, `pair: cannot read a orm.pair`},
		{map[string]any{"pair": typedNil}, "pair: null where a orm.pair goes"},
		{map[string]any{"arr": []any{1, 2, 3, 4, 5, 6}}, "arr: 6 values for a [4]uint8"},
		{map[string]any{"arr": []any{1, 2}}, "arr: 2 values for a [4]uint8"},
		{map[string]any{"ratio": 1e39}, "ratio: 1e+39 is not a number a float32 holds"},
		{map[string]any{"ratio": json.Number("NaN")}, "ratio: NaN is not a number"},
		{map[string]any{"ratio": float32(math.Inf(1))}, "ratio: +Inf is not a number"},
		{map[string]any{"named": decRatio(math.NaN())}, "named: NaN is not a number"},
		// A type of text that checks what it reads is asked.
		{map[string]any{"level": "nine"}, `level: "nine" is not a level`},
	} {
		var in odd
		if err := DecodeWhere(&in, plan, c.doc); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: got %+v, %v; want an error with %q", c.doc, in, err, c.want)
		}
	}
	var in odd
	if err := DecodeWhere(&in, plan, map[string]any{"level": "low", "named": decRatio(0.5), "arr": []any{1, 2, 3, 4}}); err != nil || *in.Level != "low" || *in.Named != 0.5 || *in.Arr != [4]byte{1, 2, 3, 4} {
		t.Errorf("got %+v, %v", in, err)
	}

	for _, c := range []struct {
		doc  any
		want string
	}{
		// A float that is an integer no longer: 2^62+1 arrived as 2^62.
		{map[string]any{"id": float64(1<<62 + 1)}, "id: 4.611686018427388e+18 is too large to be exact as a number"},
		{map[string]any{"id": float64(-(1 << 53))}, "is too large to be exact"},
		{map[string]any{"big": float64(1 << 60)}, "is too large to be exact"},
		{[]byte(`{"id": 1} {"id": 2}`), "something follows it"},
		{[]byte(`{"id": `), "is not JSON"},
		{json.RawMessage(`{"nope": 1}`), `decInput has no field "nope"`},
		{map[string]any{"!error": "why"}, "could not be written: why"},
	} {
		var in decInput
		if err := DecodeWhere(&in, decPlan, c.doc); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %+v, %v; want an error with %q", c.doc, in, err, c.want)
		}
	}
	// Given as text, the numbers of a document are read whole.
	var whole decInput
	if err := DecodeWhere(&whole, decPlan, []byte(`{"id": 4611686018427387905, "big": 18446744073709551615, "idIn": [9007199254740993]}`)); err != nil ||
		*whole.ID != 1<<62+1 || *whole.Big != math.MaxUint64 || whole.IDIn[0] != 1<<53+1 {
		t.Errorf("got %+v, %v", whole, err)
	}
	if err := DecodeWhere(&whole, decPlan, map[string]any{"id": float64(1<<53 - 1)}); err != nil || *whole.ID != 1<<53-1 {
		t.Errorf("the largest exact float: %+v, %v", whole, err)
	}

	// The input becomes what the document says: what it held is gone, and a
	// document that is refused leaves it as it was.
	name := "n"
	in2 := decInput{Name: &name, IDIn: []int64{1}}
	if err := DecodeWhere(&in2, decPlan, map[string]any{"name": nil, "active": true}); err != nil || in2.Name != nil || in2.IDIn != nil || in2.Active == nil {
		t.Errorf("a second document was merged into the first: %+v, %v", in2, err)
	}
	kept := decInput{Name: &name}
	if err := DecodeWhere(&kept, decPlan, map[string]any{"active": true, "nope": 1}); err == nil || kept.Name == nil || kept.Active != nil {
		t.Errorf("a refused document changed the input: %+v, %v", kept, err)
	}

	// An empty list of alternatives or of neighbors' conditions is no
	// condition, as an absent one is: velox's and Ent's inputs read it so,
	// and a client sends it for an empty form. Pinned, since the other
	// reading (nothing matches) is as defensible and a change either way
	// changes what filters that are kept mean.
	for _, doc := range []map[string]any{{"or": []any{}}, {"and": []any{}}, {"hasKidsWith": []any{}}} {
		var in decInput
		if err := DecodeWhere(&in, decPlan, doc); err != nil {
			t.Errorf("%v: %v", doc, err)
		} else if p := Where(&in, decPlan); p.Op != 0 {
			t.Errorf("%v is the condition %+v", doc, p)
		}
	}
}

// A struct that is not the where input its plan describes is an error, not a
// panic in reflect: a plan written by hand, or handed to another input.
func TestDecodeWhere_Misfit(t *testing.T) {
	type flat struct {
		Predicates []decPred `json:"-"`
		Not        string    `json:"not,omitempty"`
		Or         []*flat   `json:"or,omitempty"`
		And        []string  `json:"and,omitempty"`

		Name *string `json:"name,omitempty"`
		Kids int     `json:"kids,omitempty"`
	}
	plan := []WhereField{{Col: "name", Op: OpIn}, {Op: OpHasEdge, Edge: decKids, With: true, Neighbor: decChildPlan}}
	for _, doc := range []map[string]any{
		{"not": map[string]any{}},
		{"and": []any{map[string]any{}}},
		{"kids": []any{map[string]any{}}},
	} {
		var in flat
		if err := DecodeWhere(&in, plan, doc); err == nil {
			t.Errorf("%v: no error, %+v", doc, in)
		}
	}
	if _, err := EncodeWhere(&flat{}, plan); err == nil {
		t.Error("written without an error")
	}
	if _, err := EncodeWhere(42, plan); err == nil {
		t.Error("a number was written as a where input")
	}
}

func TestSnake(t *testing.T) {
	for in, want := range map[string]string{
		"User": "user", "SalesOrder": "sales_order", "HTTPLog": "http_log", "OAuth2Token": "o_auth2_token",
		"user": "user", "A": "a", "": "", "PriceListItem": "price_list_item", "ID": "id",
	} {
		if got := snake(in); got != want {
			t.Errorf("snake(%q) = %q, want %q", in, got, want)
		}
	}
}

// Any input that can be written reads back as the same conditions, as text
// and as the object a reader that keeps numbers as floats makes of it.
func TestEncodeWhere_AnyInput(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	pick := func(n int) bool { return r.IntN(n) == 0 }
	ints := []int64{0, 1, -1, 1<<53 - 1, 1 << 53, -(1 << 53), math.MaxInt64, math.MinInt64, 42}
	colors := []decColor{"red", "dark-blue"}
	str := func() string { return []string{"", "a", "RED", "red", `q"uo\te`, "\u00e9", "null", "[]"}[r.IntN(8)] }
	var child func(depth int) *decChild
	child = func(depth int) *decChild {
		c := &decChild{}
		if pick(2) {
			c.Title = ptrTo(str())
		}
		if depth < 2 && pick(4) {
			c.Not = child(depth + 1)
		}
		if depth < 2 && pick(4) {
			c.Or = []*decChild{child(depth + 1)}
		}
		return c
	}
	var input func(depth int) *decInput
	input = func(depth int) *decInput {
		in := &decInput{}
		if pick(3) {
			in.ID = ptrTo(ints[r.IntN(len(ints))])
		}
		if pick(4) {
			in.IDIn = make([]int64, r.IntN(3))
			for i := range in.IDIn {
				in.IDIn[i] = ints[r.IntN(len(ints))]
			}
		}
		if pick(4) {
			in.Small = ptrTo(uint8(r.IntN(256)))
		}
		if pick(4) {
			in.Big = ptrTo(r.Uint64())
		}
		if pick(4) {
			in.SmallIn = make([]uint8, r.IntN(4))
			for i := range in.SmallIn {
				in.SmallIn[i] = uint8(r.IntN(256))
			}
		}
		if pick(3) {
			in.Name = ptrTo(str())
		}
		in.NameIsNil = pick(5)
		if pick(4) {
			in.Color = ptrTo(colors[r.IntN(2)])
		}
		if pick(4) {
			in.ColorIn = make([]decColor, r.IntN(3))
			for i := range in.ColorIn {
				in.ColorIn[i] = colors[r.IntN(2)]
			}
		}
		if pick(4) {
			in.At = ptrTo(time.Unix(r.Int64N(4e9), r.Int64N(1e9)).UTC())
		}
		if pick(4) {
			in.Price = &decMoney{r.Int64N(1e6)}
		}
		if pick(4) {
			in.Ratio = ptrTo([]float64{0, 0.1, -1.5e300, 5e-324, math.MaxFloat64, 1 << 60}[r.IntN(6)])
		}
		if pick(4) {
			in.Active = ptrTo(pick(2))
		}
		if pick(5) {
			in.Data = ptrTo([]byte{byte(r.IntN(256)), 0})
		}
		if pick(5) {
			in.HasKids = ptrTo(pick(2))
		}
		if pick(5) {
			in.HasKidsWith = make([]*decChild, r.IntN(3))
			for i := range in.HasKidsWith {
				in.HasKidsWith[i] = child(0)
			}
		}
		if depth < 3 && pick(4) {
			in.Not = input(depth + 1)
		}
		for _, list := range []*[]*decInput{&in.Or, &in.And} {
			if depth < 3 && pick(5) {
				*list = make([]*decInput, r.IntN(3))
				for i := range *list {
					(*list)[i] = input(depth + 1)
				}
			}
		}
		return in
	}
	deep := 0
	for n := range 20000 {
		in := input(0)
		want := Where(in, decPlan)
		text, err := EncodeWhere(in, decPlan)
		if errors.Is(err, ErrWhereDepth) {
			// Too deep to be written is too deep to be a condition.
			if !tooDeep(want) {
				t.Fatalf("#%d: not written for its depth, and a condition: %+v", n, want)
			}
			deep++
			continue
		}
		if err != nil {
			t.Fatalf("#%d: %+v: %v", n, in, err)
		}
		if tooDeep(want) {
			t.Fatalf("#%d: written as %s, and too deep to be a condition", n, text)
		}
		var floats any
		if err := json.Unmarshal(text, &floats); err != nil {
			t.Fatalf("#%d: %s is not JSON: %v", n, text, err)
		}
		for how, doc := range map[string]any{"text": text, "object": floats} {
			var back decInput
			if err := DecodeWhere(&back, decPlan, doc); err != nil {
				t.Fatalf("#%d, read as %s from %s: %v", n, how, text, err)
			}
			if got := Where(&back, decPlan); !reflect.DeepEqual(got, want) {
				t.Fatalf("#%d, read as %s from %s:\n got %+v\nwant %+v", n, how, text, got, want)
			}
		}
	}
	if deep == 0 || deep > 2000 {
		t.Errorf("%d of the inputs were too deep: the cases are not the ones meant", deep)
	}
}

// tooDeep reports whether a predicate fails its query for its nesting.
func tooDeep(p P) bool {
	if err, ok := p.Val.(error); ok && p.Op == opInvalid && errors.Is(err, ErrWhereDepth) {
		return true
	}
	return slices.ContainsFunc(p.Subs, tooDeep)
}

func ptrTo[T any](v T) *T { return &v }
