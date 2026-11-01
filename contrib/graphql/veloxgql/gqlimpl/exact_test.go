package gqlimpl_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

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

// TestNullClearsOutsideAField: outside a gqlgen field no null can be read. Served,
// TestUpdateNullRefused (cmd/velox-graphql) has the rest.
func TestNullClearsOutsideAField(t *testing.T) {
	if err := gqlimpl.NullClears(context.Background(), "In.n", "input", "n", "clearN"); err != nil {
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

// TestFKScoped: with no Read policy on the target a foreign key's predicate is the
// column's alone, and with one it holds the edge's too. TestForeignKeyFilterHidden
// (cmd/velox-graphql) serves it.
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
