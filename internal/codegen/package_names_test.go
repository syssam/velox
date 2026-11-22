package codegen

import (
	"encoding/hex"
	"hash/fnv"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/internal/ddl"
	"github.com/syssam/velox/migrate"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

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
	return []schema.Annotation{sqlschema.Table("ab_testing")}
}

type pnMetric struct{ schema.Schema }

func (pnMetric) Mixin() []schema.Mixin  { return []schema.Mixin{pnAudit{}} }
func (pnMetric) Fields() []schema.Field { return []schema.Field{field.String("name")} }
func (pnMetric) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Table("ab_test_metric")}
}

// Each index is "<table>_<name>" unless it starts with "<table>_" already,
// a derived name included, as veloxe applications' hook named them, so a
// mixin's StorageKey on two tables is two names; a name the prefix makes too
// long is capped as veloxe capped it (decision 0083).
func TestPrefixIndexNames(t *testing.T) {
	g, err := build(t, pnTesting{}, pnMetric{})
	if err != nil {
		t.Fatal(err)
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
	if got, capped := indexNamesOf(s, "ab_testing"), ddl.Symbol(velox.Postgres, long); !strings.Contains(got, capped) || len(capped) != 63 || !strings.HasSuffix(capped, "_"+veloxeFNV(long)) {
		t.Errorf("ab_testing's indexes %s lack %s", got, capped)
	}
}

// veloxeFNV is the hex of the 128-bit FNV-1 hash of s, veloxe's cap suffix.
func veloxeFNV(s string) string {
	h := fnv.New128()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
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
