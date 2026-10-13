package orm

import (
	"bytes"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/syssam/velox/internal/colgen"
)

// columns.go is what its generator writes.
func TestColumnsFileIsCurrent(t *testing.T) {
	for file, source := range map[string]func() ([]byte, error){"columns.go": colgen.Source, "columns_calls_test.go": colgen.Calls} {
		want, err := source()
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is not what internal/colgen writes: go generate ./orm", file)
		}
	}
}

// TestEveryMethodOfEveryColumnType calls all of them. The column types are
// twenty-four with the same few methods, each written by the generator from
// one line: a wrong operator in one, or a type that has a method it should
// not, is a mistake nothing else would show. What a method returns and which
// methods a type has are said here, apart from the generator.
func TestEveryMethodOfEveryColumnType(t *testing.T) {
	cmp := func(op Op) any { return P{Op: op, Col: "c", Val: 1} }
	txt := func(op Op) any { return P{Op: op, Col: "c", Val: "x"} }
	want := map[string]any{
		"EQ": cmp(OpEQ), "NEQ": cmp(OpNEQ), "GT": cmp(OpGT), "GTE": cmp(OpGTE), "LT": cmp(OpLT), "LTE": cmp(OpLTE),
		"In": P{Op: OpIn, Col: "c", Val: []any{1, 2}}, "NotIn": P{Op: OpNotIn, Col: "c", Val: []any{1, 2}},
		"Contains": txt(OpContains), "HasPrefix": txt(OpHasPrefix), "HasSuffix": txt(OpHasSuffix),
		"EqualFold": txt(OpEqualFold), "ContainsFold": txt(OpContainsFold),
		"IsNil": P{Op: OpIsNil, Col: "c"}, "NotNil": P{Op: OpNotNil, Col: "c"},
		"Set":                SetterOf[P]{Kind: SetValue, Col: "c", Val: 1},
		"SetNillable":        SetterOf[P]{Kind: SetValue, Col: "c", Val: 1},
		"SetNillable of nil": SetterOf[P]{},
		"Clear":              SetterOf[P]{Kind: SetNull, Col: "c"},
		"Keep":               SetterOf[P]{Kind: SetKeep, Col: "c"},
		"Add":                SetterOf[P]{Kind: SetAdd, Col: "c", Val: 1},
		"Asc":                OrderOf[P]{Col: "c"},
		"Desc":               OrderOf[P]{Col: "c", Desc: true},
		"AsValue":            "c", "AsBool": "c", "AsEnum": "c", "AsOrdered": "c", "AsNumber": "c", "AsString": "c",
	}
	// The methods of a level: those of the level below it, and its own.
	equal := []string{"EQ", "NEQ", "Asc", "Desc", "AsBool"}
	set := append(slices.Clone(equal), "In", "NotIn", "AsEnum")
	ordered := append(slices.Clone(set), "GT", "GTE", "LT", "LTE", "AsOrdered")
	levels := map[string][]string{
		"Value":   nil,
		"Bool":    equal,
		"Enum":    set,
		"Ordered": ordered,
		"Number":  append(slices.Clone(ordered), "Add", "AsNumber"),
		"String":  append(slices.Clone(ordered), "Contains", "HasPrefix", "HasSuffix", "EqualFold", "ContainsFold", "AsString"),
	}
	got := map[string][]string{}
	eachColumnMethod(func(typ, method string, v any) {
		got[typ] = append(got[typ], method)
		if w, ok := want[method]; !ok || !reflect.DeepEqual(v, w) {
			t.Errorf("%s.%s: %#v, want %#v", typ, method, v, w)
		}
	})
	types := 0
	for level, methods := range levels {
		for _, nullable := range []bool{false, true} {
			for _, auto := range []bool{false, true} {
				typ, has := level+"Column", append(slices.Clone(methods), "IsNil", "NotNil", "Set", "SetNillable", "SetNillable of nil", "AsValue")
				if auto {
					typ, has = "Auto"+typ, append(has, "Keep")
				}
				if nullable {
					typ, has = "Nullable"+typ, append(has, "Clear")
				}
				types++
				slices.Sort(has)
				slices.Sort(got[typ])
				if !slices.Equal(got[typ], has) {
					t.Errorf("%s has the methods %v, want %v", typ, got[typ], has)
				}
			}
		}
	}
	if len(got) != types {
		t.Errorf("%d column types were called, %d are expected", len(got), types)
	}
}

type (
	userPred P
	postPred P
	status   string
)

// What the methods of a column build is the engine's data for it: the
// operator, the column, the value as it was given.
func TestColumnMethods(t *testing.T) {
	var (
		name    = StringColumn[string, userPred]("name")
		age     = NullableNumberColumn[int, userPred]("age")
		seen    = AutoOrderedColumn[time.Time, userPred]("seen_at")
		state   = EnumColumn[status, userPred]("status")
		active  = BoolColumn[bool, userPred]("active")
		profile = NullableValueColumn[map[string]any, userPred]("profile")
	)
	now := time.Now()
	for what, c := range map[string]struct{ got, want any }{
		"EQ":           {name.EQ("ann"), userPred{Op: OpEQ, Col: "name", Val: "ann"}},
		"NEQ":          {active.NEQ(true), userPred{Op: OpNEQ, Col: "active", Val: true}},
		"In":           {state.In("a", "b"), userPred{Op: OpIn, Col: "status", Val: []any{status("a"), status("b")}}},
		"In of none":   {state.In(), userPred{Op: OpIn, Col: "status", Val: []any{}}},
		"NotIn":        {age.NotIn(1), userPred{Op: OpNotIn, Col: "age", Val: []any{1}}},
		"GT":           {age.GT(18), userPred{Op: OpGT, Col: "age", Val: 18}},
		"GTE":          {seen.GTE(now), userPred{Op: OpGTE, Col: "seen_at", Val: now}},
		"LT":           {age.LT(65), userPred{Op: OpLT, Col: "age", Val: 65}},
		"LTE":          {name.LTE("m"), userPred{Op: OpLTE, Col: "name", Val: "m"}},
		"Contains":     {name.Contains("n"), userPred{Op: OpContains, Col: "name", Val: "n"}},
		"HasPrefix":    {name.HasPrefix("a"), userPred{Op: OpHasPrefix, Col: "name", Val: "a"}},
		"HasSuffix":    {name.HasSuffix("n"), userPred{Op: OpHasSuffix, Col: "name", Val: "n"}},
		"EqualFold":    {name.EqualFold("ANN"), userPred{Op: OpEqualFold, Col: "name", Val: "ANN"}},
		"ContainsFold": {name.ContainsFold("N"), userPred{Op: OpContainsFold, Col: "name", Val: "N"}},
		"IsNil":        {profile.IsNil(), userPred{Op: OpIsNil, Col: "profile"}},
		"NotNil":       {name.NotNil(), userPred{Op: OpNotNil, Col: "name"}},
		"Set":          {name.Set("bob"), SetterOf[userPred]{Kind: SetValue, Col: "name", Val: "bob"}},
		"SetNillable":  {age.SetNillable(new(7)), SetterOf[userPred]{Kind: SetValue, Col: "age", Val: 7}},
		"SetNillable of nil, which assigns nothing": {age.SetNillable(nil), SetterOf[userPred]{}},
		"Clear": {profile.Clear(), SetterOf[userPred]{Kind: SetNull, Col: "profile"}},
		"Keep":  {seen.Keep(), SetterOf[userPred]{Kind: SetKeep, Col: "seen_at"}},
		"Add":   {age.Add(1), SetterOf[userPred]{Kind: SetAdd, Col: "age", Val: 1}},
		"Asc":   {name.Asc(), OrderOf[userPred]{Col: "name"}},
		"Desc":  {state.Desc(), OrderOf[userPred]{Col: "status", Desc: true}},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: %#v, want %#v", what, c.got, c.want)
		}
	}
	// Seen as the plain type of a level it has, a column is the same column.
	if got := recent(seen.AsOrdered(), now); !reflect.DeepEqual(got, userPred{Op: OpGTE, Col: "seen_at", Val: now}) {
		t.Errorf("through AsOrdered: %#v", got)
	}
	if got := age.AsNumber().Add(2); !reflect.DeepEqual(got, SetterOf[userPred]{Kind: SetAdd, Col: "age", Val: 2}) {
		t.Errorf("through AsNumber: %#v", got)
	}
	if got := profile.AsValue().IsNil(); !reflect.DeepEqual(got, userPred{Op: OpIsNil, Col: "profile"}) {
		t.Errorf("through AsValue: %#v", got)
	}
	if got := name.AsString().AsEnum().AsBool().EQ("x"); !reflect.DeepEqual(got, userPred{Op: OpEQ, Col: "name", Val: "x"}) {
		t.Errorf("through every level: %#v", got)
	}
}

// recent is written once for whichever entity it is used with.
func recent[E Predicate](at *OrderedColumn[time.Time, E], since time.Time) E { return at.GTE(since) }

// An edge builds conditions on its neighbors and changes to them, typed by
// both ends.
func TestEdgeMethods(t *testing.T) {
	author := &ToOne[postPred, userPred, int]{Meta: &EdgeMeta{Name: "author"}}
	tags := &ToMany[postPred, userPred, string]{Meta: &EdgeMeta{Name: "tags"}}
	name := StringColumn[string, userPred]("name")
	for what, c := range map[string]struct{ got, want any }{
		"Has":                  {author.Has(), postPred{Op: OpHasEdge, Edge: author.Meta}},
		"HasWith":              {author.HasWith(name.EQ("ann")), postPred{Op: OpHasEdge, Edge: author.Meta, Subs: []P{{Op: OpEQ, Col: "name", Val: "ann"}}}},
		"Set":                  {author.Set(7), SetterOf[postPred]{Kind: EdgeAdd, Edge: author.Meta, IDs: []any{7}}},
		"Clear":                {author.Clear(), SetterOf[postPred]{Kind: EdgeClear, Edge: author.Meta}},
		"many Has":             {tags.Has(), postPred{Op: OpHasEdge, Edge: tags.Meta}},
		"many HasWith of none": {tags.HasWith(), postPred{Op: OpHasEdge, Edge: tags.Meta, Subs: []P{}}},
		"Add":                  {tags.Add("a", "b"), SetterOf[postPred]{Kind: EdgeAdd, Edge: tags.Meta, IDs: []any{"a", "b"}}},
		"Remove":               {tags.Remove("a"), SetterOf[postPred]{Kind: EdgeRemove, Edge: tags.Meta, IDs: []any{"a"}}},
		"many Clear":           {tags.Clear(), SetterOf[postPred]{Kind: EdgeClear, Edge: tags.Meta}},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s: %#v, want %#v", what, c.got, c.want)
		}
	}
}
