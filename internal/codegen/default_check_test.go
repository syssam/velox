package codegen

import (
	"strings"
	"testing"

	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// everywhere is the same default text on every dialect.
func everywhere(s string) map[string]string {
	return map[string]string{field.Postgres: s, field.MySQL: s, field.SQLite: s}
}

// ndOrder has database defaults of each kind: non-zero literals, which an
// insert's zero cannot be stored over, and those it can, or that cannot
// hold a zero worth storing.
type ndOrder struct{ schema.Schema }

func (ndOrder) Fields() []schema.Field {
	return []schema.Field{
		field.Bool("a").SchemaDefault(everywhere("true")),
		field.Int("n").SchemaDefault(everywhere("5")),
		field.String("kind").Annotations(sqlschema.Default("'plain'")),
		field.Bool("both").Default(true).SchemaDefault(everywhere("true")),
		field.String("note").SchemaDefault(map[string]string{field.Postgres: "'x'::text", field.MySQL: "('x')", field.SQLite: "'x'"}),
		// No warning for the fields below.
		field.Bool("off").SchemaDefault(everywhere("false")),
		field.Int("zero").SchemaDefault(everywhere("0")),
		field.String("empty").SchemaDefault(everywhere("''")),
		field.Bool("maybe").Nillable().SchemaDefault(everywhere("true")),
		field.Int("count").Optional().Nillable().SchemaDefault(everywhere("5")),
		field.Enum("status").Values("new", "paid").SchemaDefault(everywhere("'new'")),
		field.Time("at").SchemaDefault(everywhere("CURRENT_TIMESTAMP")),
		field.String("code").SchemaDefault(map[string]string{field.Postgres: "gen_random_uuid()::text", field.MySQL: "(uuid())", field.SQLite: "(lower(hex(randomblob(16))))"}),
	}
}

// TestNonZeroDefaultWarning: velox-gen warns of a field that is not
// Nillable whose database default is a non-zero literal (D10), whether
// SchemaDefault or sqlschema.Default gives it, and a Go Default beside it;
// not of a zero literal, a Nillable field, an enum or an expression.
func TestNonZeroDefaultWarning(t *testing.T) {
	got := DefaultWarnings(mustBuild(t, ndOrder{}))
	want := []string{`field "a"`, `field "n"`, `field "kind"`, `field "both"`, `field "note"`}
	if len(got) != len(want) {
		t.Fatalf("got %d warnings, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(got[i], "schema ndOrder: "+w+": ") ||
			!strings.Contains(got[i], "the zero value cannot be stored through Insert; make it Nillable to store it") {
			t.Errorf("warning %d: got %q, want it to name %s and say why", i, got[i], w)
		}
	}
}

func TestNonZeroLiteral(t *testing.T) {
	for _, c := range []struct {
		typ, s string
		want   bool
	}{
		{"bool", "true", true}, {"bool", "TRUE", true}, {"bool", "1", true}, {"bool", "'t'", true},
		{"bool", "false", false}, {"bool", "0", false}, {"bool", "'f'", false},
		{"int64", "-1", true}, {"float64", "0.5", true}, {"int", "(5)", true}, {"int", "'7'", true},
		{"int", "0", false}, {"float64", "0.0", false}, {"int", "nextval('s')", false},
		{"string", "'x'", true}, {"string", "'it''s'", true}, {"string", "'x'::character varying", true},
		{"string", "''", false}, {"string", "''::text", false}, {"string", "'a' || 'b'", false}, {"string", "upper('x')", false},
		{"enum", "'new'", false}, {"time", "'2020-01-01'", false}, {"bytes", "'x'", false},
		{"decimal", "1.00", true}, {"decimal", "'0.5'", true}, {"decimal", "(-2)", true},
		{"decimal", "0.00", false}, {"decimal", "'0'", false}, {"decimal", "nextval('s')", false},
	} {
		if got := nonZeroLiteral(c.typ, c.s); got != c.want {
			t.Errorf("nonZeroLiteral(%q, %q) = %v, want %v", c.typ, c.s, got, c.want)
		}
	}
}

// ndMoney has decimal Other fields with database defaults: a non-zero
// literal is warned of, as a number's is; a zero one, a Nillable field and
// an Other field in no decimal column are not.
type ndMoney struct{ schema.Schema }

func (ndMoney) Fields() []schema.Field {
	dec := map[string]string{"postgres": "numeric(10,2)", "mysql": "decimal(10,2)", "sqlite": "numeric"}
	return []schema.Field{
		field.Other("fee", LwDecText("")).SchemaType(dec).SchemaDefault(everywhere("1.00")),
		field.Other("quoted", LwDecText("")).SchemaType(dec).SchemaDefault(everywhere("'2.5'")),
		// No warning for the fields below.
		field.Other("nothing", LwDecText("")).SchemaType(dec).SchemaDefault(everywhere("0.00")),
		field.Other("maybe", LwDecText("")).SchemaType(dec).Optional().Nillable().SchemaDefault(everywhere("1.00")),
		field.Other("word", LwDecText("")).SchemaType(map[string]string{"postgres": "citext"}).SchemaDefault(map[string]string{"postgres": "'x'"}),
	}
}

// TestNonZeroDecimalDefaultWarning: a decimal Other field that is not
// Nillable and whose database default is a non-zero literal is warned of,
// as a Go number's.
func TestNonZeroDecimalDefaultWarning(t *testing.T) {
	got := DefaultWarnings(mustBuild(t, ndMoney{}))
	want := []string{`field "fee"`, `field "quoted"`}
	if len(got) != len(want) {
		t.Fatalf("got %d warnings, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(got[i], "schema ndMoney: "+w+": ") {
			t.Errorf("warning %d: got %q, want it to name %s", i, got[i], w)
		}
	}
}
