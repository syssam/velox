package codegen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/internal/ddl"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/load"
	"github.com/syssam/velox/schema/mixin"
)

// buildPackage is build of a package that declares the options p.
func buildPackage(t *testing.T, p schema.Package, ss ...schema.Interface) (*Graph, error) {
	t.Helper()
	b, err := load.MarshalPackage("github.com/syssam/velox/internal/codegen", p, ss...)
	if err != nil {
		t.Fatal(err)
	}
	var m loadjson.Model
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Errors) > 0 {
		t.Fatalf("model errors: %+v", m.Errors)
	}
	return Build(&m)
}

// pnAudit is a mixin whose index has one StorageKey for every table that
// lists it, as a veloxe application's audit mixin has.
type pnAudit struct{ mixin.Schema }

func (pnAudit) Fields() []schema.Field {
	return []schema.Field{field.Int64("workspace_id"), field.Time("created_at")}
}

func (pnAudit) Indexes() []schema.Index {
	return []schema.Index{index.Fields("workspace_id", "created_at").StorageKey("ix_workspace_created_at")}
}

type pnTesting struct{ schema.Schema }

func (pnTesting) Mixin() []schema.Mixin { return []schema.Mixin{pnAudit{}} }
func (pnTesting) Fields() []schema.Field {
	return []schema.Field{field.String("test_key"), field.String("a_rather_long_column_name_for_an_index")}
}

func (pnTesting) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("test_key").StorageKey("ab_testing_ix_key"), // already the table's
		index.Fields("workspace_id", "test_key"),                 // derived, from the entity's name
		index.Fields("workspace_id", "a_rather_long_column_name_for_an_index").StorageKey("ix_long_enough_to_pass_the_postgres_limit_once_prefixed"),
	}
}

func (pnTesting) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("ab_testing")}
}

type pnMetric struct{ schema.Schema }

func (pnMetric) Mixin() []schema.Mixin  { return []schema.Mixin{pnAudit{}} }
func (pnMetric) Fields() []schema.Field { return []schema.Field{field.String("name")} }
func (pnMetric) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("ab_test_metric")}
}

// Without options, a mixin's StorageKey on two tables is refused (rule 44).
// With PrefixIndexNames, each index is "<table>_<name>" unless it starts with
// "<table>_" already, a derived name included, as veloxe applications' hook
// named them; with VeloxeNames, a name the prefix makes too long is capped as
// veloxe capped it.
func TestPackagePrefixIndexNames(t *testing.T) {
	if _, err := build(t, pnTesting{}, pnMetric{}); err == nil || !strings.Contains(err.Error(), `"ix_workspace_created_at"`) {
		t.Fatalf("without options: %v", err)
	}
	g, err := buildPackage(t, schema.Package{PrefixIndexNames: true, Names: schema.VeloxeNames}, pnTesting{}, pnMetric{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Names != "veloxe" {
		t.Errorf("Names %q", g.Names)
	}
	long := "ab_testing_ix_long_enough_to_pass_the_postgres_limit_once_prefixed"
	want := map[string][]string{
		"pnTesting": {"ab_testing_ix_workspace_created_at", "ab_testing_ix_key", "ab_testing_pntesting_workspace_id_test_key", long},
		"pnMetric":  {"ab_test_metric_ix_workspace_created_at"},
	}
	for ent, names := range want {
		var got []string
		for _, ix := range g.Entity(ent).Indexes {
			got = append(got, ix.Name)
		}
		if strings.Join(got, " ") != strings.Join(names, " ") {
			t.Errorf("%s: %v, want %v", ent, got, names)
		}
	}
	if ix := g.Entity("pnTesting").Indexes[1]; ix.Derived {
		t.Error("a name already the table's is not marked derived")
	}
	s, err := Tables(g, velox.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	if got, capped := indexNamesOf(s, "ab_testing"), ddl.VeloxeSymbol(velox.Postgres, long); !strings.Contains(got, capped) || len(capped) != 63 {
		t.Errorf("ab_testing's indexes %s lack %s", got, capped)
	}
	// ent's cap, the zero Names.
	g, err = buildPackage(t, schema.Package{PrefixIndexNames: true}, pnTesting{}, pnMetric{})
	if err != nil {
		t.Fatal(err)
	}
	s, err = Tables(g, velox.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	if got := indexNamesOf(s, "ab_testing"); !strings.Contains(got, ddl.Symbol(velox.Postgres, long)) {
		t.Errorf("ent's cap: %s", got)
	}
}

// indexNamesOf are the names of the indexes of table in s, space-separated.
func indexNamesOf(s *migrate.Schema, table string) string {
	var names []string
	for _, tb := range s.Tables {
		if tb.Name == table {
			for _, ix := range tb.Indexes {
				names = append(names, ix.Name)
			}
		}
	}
	return strings.Join(names, " ")
}

// Load reads the package's one exported schema.Package into the model, so
// every velox-gen command applies it; a second one, or an unexported one,
// which the export data would not show, is refused at its line.
func TestLoadPackageOptions(t *testing.T) {
	opts := "\nvar Velox = schema.Package{PrefixIndexNames: true, Names: schema.VeloxeNames}\n"
	m, err := Load(module(t, map[string]string{"schema/user.go": userSchema + opts}), "./schema", offline())
	if err != nil {
		t.Fatal(err)
	}
	if m.Options == nil || !m.Options.PrefixIndexNames || m.Options.Names != "veloxe" {
		t.Fatalf("options %+v", m.Options)
	}
	for name, src := range map[string]string{
		"two":        opts + "var Other schema.Package\n",
		"unexported": "\nvar velox = schema.Package{PrefixIndexNames: true}\n",
	} {
		_, err := Load(module(t, map[string]string{"schema/user.go": userSchema + src}), "./schema", offline())
		want := map[string]string{"two": "Other is a second schema.Package", "unexported": "the schema.Package velox is unexported"}[name]
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "user.go:") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
