package gqlimpl_test

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

// TestFromJSONNumbers: a number in an any is the float64 a hook expects when that says
// the same number, and a json.Number only when it would not. A typed place, and a type
// with its own UnmarshalJSON, are left to the decoder.
func TestFromJSONNumbers(t *testing.T) {
	const at = "In.j"
	for raw, want := range map[string]any{
		`30`:                     float64(30),
		`1.5`:                    1.5,
		`0.1`:                    0.1,
		`1E2`:                    float64(100),
		`-0`:                     math.Copysign(0, -1),
		`9007199254740992`:       float64(9007199254740992),
		`1e30`:                   1e30,
		`9007199254740993`:       json.Number("9007199254740993"),
		`0.10000000000000000001`: json.Number("0.10000000000000000001"),
		`1e400`:                  json.Number("1e400"),
		`1e-400`:                 json.Number("1e-400"),
	} {
		got, err := gqlimpl.FromJSON[any](at, veloxgql.JSON(raw))
		if err != nil || got != want {
			t.Errorf("%s: %T %v, %v; want %T %v", raw, got, got, err, want, want)
		}
	}
	m, err := gqlimpl.FromJSON[map[string]any](at, veloxgql.JSON(`{"age":30,"big":9007199254740993,"list":[1.5,[2]],"deep":{"n":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	if age, ok := m["age"].(float64); !ok || age != 30 {
		t.Errorf("a hook's m[age].(float64): %T %v", m["age"], m["age"])
	}
	if m["big"] != json.Number("9007199254740993") {
		t.Errorf("big: %T %v", m["big"], m["big"])
	}
	if list := m["list"].([]any); list[0] != 1.5 || list[1].([]any)[0] != float64(2) {
		t.Errorf("list: %#v", list)
	}
	if deep := m["deep"].(map[string]any); deep["n"] != float64(3) {
		t.Errorf("deep: %#v", deep)
	}
	type holds struct {
		Any    any
		Ptr    *any
		List   []any
		Keyed  map[string]any
		Nested struct{ Any any }
		Arr    [1]any
		Int    int64
		Num    json.Number
		PtrMap *map[string][]any
	}
	h, err := gqlimpl.FromJSON[holds](at, veloxgql.JSON(`{"Any":1,"Ptr":2,"List":[3],"Keyed":{"k":4},"Nested":{"Any":5},"Arr":[6],"Int":7,"Num":8,"PtrMap":{"k":[9]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if h.Any != float64(1) || *h.Ptr != float64(2) || h.List[0] != float64(3) || h.Keyed["k"] != float64(4) || h.Nested.Any != float64(5) ||
		h.Arr[0] != float64(6) || h.Int != 7 || h.Num != "8" || (*h.PtrMap)["k"][0] != float64(9) {
		t.Errorf("the places: %+v", h)
	}
}

// TestFromJSONArrayLength: a Go array takes a list of its length, or the decoder pads or
// cuts it to one: [1,2] stored [1,2,0], and [1,2,3,4] stored [1,2,3].
func TestFromJSONArrayLength(t *testing.T) {
	const at = "In.j"
	type inner struct{ T [2]string }
	for _, c := range []struct {
		json string
		fail func() error
	}{
		{`[1,2]`, func() error { _, err := gqlimpl.FromJSON[[3]int](at, veloxgql.JSON(`[1,2]`)); return err }},
		{`[1,2,3,4]`, func() error { _, err := gqlimpl.FromJSON[[3]int](at, veloxgql.JSON(`[1,2,3,4]`)); return err }},
		{`[]`, func() error { _, err := gqlimpl.FromJSON[*[3]int](at, veloxgql.JSON(`[]`)); return err }},
		{`[[1]]`, func() error { _, err := gqlimpl.FromJSON[[][2]int](at, veloxgql.JSON(`[[1]]`)); return err }},
		{`{"T":["a"]}`, func() error { _, err := gqlimpl.FromJSON[inner](at, veloxgql.JSON(`{"T":["a"]}`)); return err }},
		{`{"k":[1]}`, func() error {
			_, err := gqlimpl.FromJSON[map[string][2]int](at, veloxgql.JSON(`{"k":[1]}`))
			return err
		}},
	} {
		err := c.fail()
		if codeOf(err) != veloxgql.CodeBadUserInput || !strings.Contains(err.Error(), "In.j: invalid value: a list of") || strings.Contains(err.Error(), "gqlimpl_test") {
			t.Errorf("%s: %v", c.json, err)
		}
	}
	got, err := gqlimpl.FromJSON[[3]int](at, veloxgql.JSON(`[1,2,3]`))
	if err != nil || got != [3]int{1, 2, 3} {
		t.Errorf("the length: %v, %v", got, err)
	}
}

// TestFromJSONExact: FromJSON keeps a number's digits, and refuses a null the type
// cannot hold and a key it has not, naming the field and no Go type.
func TestFromJSONExact(t *testing.T) {
	const at = "In.j"
	m, err := gqlimpl.FromJSON[map[string]any](at, veloxgql.JSON(`{"n":9007199254740993,"s":[1.5],"z":null}`))
	if err != nil || m["n"] != json.Number("9007199254740993") || m["z"] != nil {
		t.Errorf("a number in an any: %v, %v", m, err)
	}
	type inner struct{ A string }
	type tagged struct {
		Name   string  `json:"name,omitempty"`
		Skip   string  `json:"-"`
		Opt    *string `json:"opt"`
		Inner  inner
		Innerp *inner
		Own    time.Time
		Any    any
		Tags   []string
		Arr    [1]int
		ByKey  map[string]int
	}
	for _, ok := range []string{
		`{"name":"a"}`, `{"NAME":"a"}`, `{"opt":null}`, `{"Innerp":null}`, `{"Own":null}`, `{"Any":null}`, `{"Tags":null}`, `{"ByKey":null}`,
		`{"Tags":["null"]}`, `{"Innerp":{"A":"x"}}`, `{"name":"null"}`,
	} {
		if _, err := gqlimpl.FromJSON[tagged](at, veloxgql.JSON(ok)); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{
		`{"name":null}`, `{"NAME":null}`, `{"Inner":null}`, `{"Inner":{"A":null}}`, `{"Innerp":{"A":null}}`, `{"Tags":["a",null]}`, `{"ByKey":{"a":null}}`, `{"Arr":null}`,
	} {
		_, err := gqlimpl.FromJSON[tagged](at, veloxgql.JSON(bad))
		if codeOf(err) != veloxgql.CodeBadUserInput || !strings.Contains(err.Error(), "In.j: invalid value: null") {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if _, err := gqlimpl.FromJSON[[]string](at, veloxgql.JSON(`null`)); err != nil {
		t.Errorf("a null list: %v", err)
	}
	if _, err := gqlimpl.FromJSON[string](at, veloxgql.JSON(`null`)); codeOf(err) != veloxgql.CodeBadUserInput {
		t.Errorf("a null string: %v", err)
	}
	_, err = gqlimpl.FromJSON[tagged](at, veloxgql.JSON(`{"nope":1}`))
	if codeOf(err) != veloxgql.CodeBadUserInput || !strings.Contains(err.Error(), `the key "nope"`) || strings.Contains(err.Error(), "gqlimpl_test") {
		t.Errorf("an unknown key: %v", err)
	}
}

// TestNullOutsideAField: outside a gqlgen field no null can be read. Served,
// TestUpdateNullClears and TestUpdateNullEdgeRefused (cmd/velox-graphql) have the rest.
func TestNullOutsideAField(t *testing.T) {
	if gqlimpl.NullGiven(context.Background(), "input", "n") {
		t.Error("a null given")
	}
	if err := gqlimpl.NullEdge(context.Background(), "In.n", "input", "n", "clearN"); err != nil {
		t.Error(err)
	}
}

// open has no Read policy, and guarded has one, for TestFKScoped.
type (
	open    struct{}
	guarded struct{}
)

func (*guarded) Policy() velox.Policy[guarded] {
	return velox.Policy[guarded]{Read: func(context.Context) (velox.Pred[guarded], error) { return velox.And[guarded](), nil }}
}

// TestFKScoped: with no Read policy on the target a foreign key's predicate
// is the column's alone, and with one it holds the edge's too.
// TestForeignKeyFilterHidden and TestForeignKeyFilterOtherScope (cmd/velox-graphql)
// serve it.
func TestFKScoped(t *testing.T) {
	calls := 0
	has := func() velox.Pred[person] { calls++; return velox.And[person]() }
	op := func(int64) velox.Pred[person] { return velox.And[person]() }
	list := func(...int64) velox.Pred[person] { return velox.And[person]() }
	null := func() velox.Pred[person] { return velox.And[person]() }

	gqlimpl.FK[open](op, has)(1)
	gqlimpl.FKList[open](list, has)(1)
	gqlimpl.FKIsNull[open](null, has)()
	gqlimpl.FKNotNull[open](null, has)()
	if calls != 0 {
		t.Errorf("no policy, but the edge's Has ran %d times", calls)
	}
	gqlimpl.FK[guarded](op, has)(1)
	gqlimpl.FKList[guarded](list, has)(1)
	gqlimpl.FKIsNull[guarded](null, has)()
	gqlimpl.FKNotNull[guarded](null, has)()
	if calls != 4 {
		t.Errorf("a Read policy, and the edge's Has ran %d times, want 4", calls)
	}
}
