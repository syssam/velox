package codegen

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// ddOK has the column defaults sqlschema.Default gives: a number and a string, each
// the one text on every dialect.
type ddOK struct{ schema.Schema }

func (ddOK) Annotations() []schema.Annotation { return []schema.Annotation{schema.Table("dd_oks")} }
func (ddOK) Fields() []schema.Field {
	return []schema.Field{
		field.Int("seats").Annotations(sqlschema.Default("100")),
		field.String("kind").Annotations(sqlschema.Default("'plain'")),
		field.Int("plain"),
	}
}

func TestSQLDefaultColumns(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		tb := table(t, tables(t, d, ddOK{}), "dd_oks")
		got := map[string]string{}
		for _, c := range tb.Columns {
			got[c.Name] = c.Default
		}
		if got["seats"] != "100" || got["kind"] != "'plain'" || got["plain"] != "" {
			t.Errorf("%v: defaults %v", d, got)
		}
	}
}

// ddText is a Text field, which MySQL makes a text column, whose default is an
// expression there: a literal is refused, in parentheses it is taken.
type ddText struct{ schema.Schema }

func (ddText) Fields() []schema.Field {
	return []schema.Field{
		field.Text("bad").Annotations(sqlschema.Default("'x'")),
		field.Text("good").SchemaDefault(map[string]string{field.Postgres: "'x'", field.MySQL: "('x')", field.SQLite: "'x'"}),
	}
}

func TestSQLDefaultMySQLText(t *testing.T) {
	g := mustBuild(t, ddText{})
	if _, err := Tables(g, velox.MySQL); err == nil {
		t.Fatal("no error")
	} else if got := plain(err); !strings.Contains(got, `field "bad": its MySQL SchemaDefault 'x' is a literal on a longtext column, whose default MySQL takes only as an expression: write it in parentheses, as ('x')`) ||
		strings.Contains(got, `"good"`) {
		t.Errorf("got\n%s", got)
	}
	// PostgreSQL and SQLite take it as it is.
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
		if _, err := Tables(g, d); err != nil {
			t.Errorf("%v: %v", d, err)
		}
	}
}

// ddTime has a time column whose default is the function without the column's
// precision, which MySQL refuses at apply, and one that carries it.
type ddTime struct{ schema.Schema }

func (ddTime) Fields() []schema.Field {
	return []schema.Field{
		field.Time("bad").Annotations(sqlschema.Default("CURRENT_TIMESTAMP")),
		field.Time("bad_fn").Annotations(sqlschema.DefaultExpr("now()")),
		field.Time("good").Annotations(sqlschema.Default("CURRENT_TIMESTAMP(6)")),
	}
}

func TestSQLDefaultMySQLTimePrecision(t *testing.T) {
	g := mustBuild(t, ddTime{})
	_, err := Tables(g, velox.MySQL)
	if err == nil {
		t.Fatal("no error")
	}
	got := plain(err)
	for _, w := range []string{
		`field "bad": its MySQL SchemaDefault CURRENT_TIMESTAMP on a datetime(6) column must carry the column's precision`,
		`field "bad_fn": its MySQL SchemaDefault now() on a datetime(6) column must carry the column's precision`,
		"write CURRENT_TIMESTAMP(6)",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("%q not in\n%s", w, got)
		}
	}
	if strings.Contains(got, `"good"`) {
		t.Errorf("good refused:\n%s", got)
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
		if _, err := Tables(g, d); err != nil {
			t.Errorf("%v: %v", d, err)
		}
	}
}
