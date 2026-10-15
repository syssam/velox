package main

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// root is the library's module, whose internal/testmodel/testdata holds the
// fixtures.
const root = "../.."

func loadFixture(t *testing.T, cfg config, pattern string) []*packages.Package {
	t.Helper()
	cfg.dir = root
	ps, err := load(cfg, []string{pattern})
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func discoverFixture(t *testing.T, cfg config, fixture string) ([]*entity, string) {
	t.Helper()
	ps := loadFixture(t, cfg, "./internal/testmodel/testdata/"+fixture)
	ents, probs := discover(ps[0], cfg)
	var b strings.Builder
	for _, p := range probs {
		b.WriteString(p.msg + "\n")
	}
	return ents, b.String()
}

func TestDiscovery(t *testing.T) {
	for fixture, want := range map[string]string{
		"embedptr":   "Order: embedded pointer Base is not supported",
		"nested":     "Order: mixin Inner is inside another embedded struct; embed mixins directly in the entity, where velox applies them",
		"emptyembed": "Order: embedded Time adds no columns",
		"generic":    "Box has type parameters",
		"valuerecv":  "Order: Schema has a value receiver",
		"dupfield":   "Order: two fields are named ID",
	} {
		t.Run(fixture, func(t *testing.T) {
			if _, got := discoverFixture(t, config{}, fixture); !strings.Contains(got, want) {
				t.Errorf("problems:\n%s\nwant one containing %q", got, want)
			}
		})
	}
	// Review Focus 2: a Schema method that is not velox's makes no entity and no problem.
	t.Run("notentity", func(t *testing.T) {
		if ents, probs := discoverFixture(t, config{}, "notentity"); len(ents) != 0 || probs != "" {
			t.Errorf("entities %d, problems %q", len(ents), probs)
		}
	})
}

// TestDiscoveryOfTestmodel discovers testdata/testmodel, the test model on
// the old path (see TestTestmodelIsReproduced).
func TestDiscoveryOfTestmodel(t *testing.T) {
	ps, err := load(config{dir: "."}, []string{"./testdata/testmodel"})
	if err != nil {
		t.Fatal(err)
	}
	ents, probs := discover(ps[0], config{})
	if len(probs) != 0 {
		t.Fatal(probs)
	}
	var inv *entity
	for _, e := range ents {
		if e.name == "Invoice" {
			inv = e
		}
	}
	if inv == nil {
		t.Fatalf("Invoice not found among %d entities", len(ents))
	}
	var got []string
	for _, c := range inv.columns {
		got = append(got, c.field+" "+c.handle+" "+c.value.String())
	}
	want := "CreatedAt OrderedCol time.Time|UpdatedAt OrderedCol time.Time|ID NumberCol int64|OrgID NumberCol int64|" +
		"Number StringCol string|Total NumberCol float64|Paid Col bool|PaidAt NullOrderedCol time.Time|" +
		"Note NullStringCol string|Data Col []byte"
	if strings.Join(got, "|") != want {
		t.Errorf("columns\n got %s\nwant %s", strings.Join(got, "|"), want)
	}
}

func TestRegisteredTypes(t *testing.T) {
	id := "github.com/syssam/velox2/internal/testmodel/testdata/registered.Money"
	for _, c := range []struct {
		cfg  config
		want string
	}{
		{config{}, "Amount Col, Tax NullCol"},
		{config{number: map[string]bool{id: true}}, "Amount NumberCol, Tax NullNumberCol"},
		{config{ordered: map[string]bool{id: true}}, "Amount OrderedCol, Tax NullOrderedCol"},
	} {
		ents, probs := discoverFixture(t, c.cfg, "registered")
		if probs != "" || len(ents) != 1 {
			t.Fatalf("entities %d, problems %q", len(ents), probs)
		}
		cols := ents[0].columns[1:]
		got := fmt.Sprintf("%s %s, %s %s", cols[0].field, cols[0].handle, cols[1].field, cols[1].handle)
		if got != c.want {
			t.Errorf("%v: got %s, want %s", c.cfg, got, c.want)
		}
	}
}
