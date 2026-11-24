package codegen

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Table asset's column depreciation_method and table asset_depreciation's
// column method give one not-null name, and each table declares a check of
// one name.
type tcAsset struct{ schema.Schema }

func (tcAsset) Fields() []schema.Field {
	return []schema.Field{field.Enum("depreciation_method").Values("linear", "declining").
		SchemaType(map[string]string{dialect.MySQL: "varchar(16)"})}
}

func (tcAsset) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Table("asset"), sqlschema.Checks(map[string]string{"asset_depreciation_method_check": "depreciation_method <> ''"})}
}

type tcAssetDepreciation struct{ schema.Schema }

func (tcAssetDepreciation) Fields() []schema.Field {
	return []schema.Field{field.Enum("method").Values("linear", "declining").
		SchemaType(map[string]string{dialect.MySQL: "varchar(16)"})}
}

func (tcAssetDepreciation) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Table("asset_depreciation"), sqlschema.Checks(map[string]string{"asset_depreciation_method_check": "method <> ''"})}
}

// PostgreSQL keeps check and not-null constraint names per table, and SQLite
// does not compare them, so two tables may share one; MySQL keeps check names
// per database, so Tables refuses the second there.
func TestConstraintNamesPerTable(t *testing.T) {
	g := mustBuild(t, tcAsset{}, tcAssetDepreciation{})
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
		if _, err := Tables(g, d); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
	_, err := Tables(g, velox.MySQL)
	if err == nil || !strings.Contains(err.Error(), `check name "asset_depreciation_method_check" is also a check's of table asset`) {
		t.Errorf("MySQL: %v", err)
	}
	// A foreign key of table pets with the name of table users' check.
	g = mustBuild(t, tcCheckUser{}, Pet{})
	if _, err := Tables(g, velox.Postgres); err != nil {
		t.Errorf("PostgreSQL, foreign key and check: %v", err)
	}
	_, err = Tables(g, velox.MySQL)
	if err == nil || !strings.Contains(err.Error(), `check name "users_pets" is also a foreign key's of table pets`) {
		t.Errorf("MySQL, foreign key and check: %v", err)
	}
}

// tcCheckUser's check has the name of its edge's foreign key, which is in
// table pets.
type tcCheckUser struct{ schema.Schema }

func (tcCheckUser) Fields() []schema.Field { return []schema.Field{field.Int("age")} }
func (tcCheckUser) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", Pet.Type).StorageKey(edge.Symbol("users_pets"))}
}

func (tcCheckUser) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Table("users"), sqlschema.Checks(map[string]string{"users_pets": "age > 0"})}
}

// tcAllocation is an application's PaymentAllocation: the assoc edge
// of a one-to-one pair to its own schema binds the key field.
type tcAllocation struct{ schema.Schema }

func (tcAllocation) Fields() []schema.Field {
	return []schema.Field{field.Int64("reversed_by_id").Optional().Nillable()}
}

func (tcAllocation) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("reversed_by", tcAllocation.Type).Field("reversed_by_id").Unique(),
		edge.From("reversal_of", tcAllocation.Type).Ref("reversed_by").Unique(),
	}
}

func (tcAllocation) Annotations() []schema.Annotation {
	return []schema.Annotation{sqlschema.Table("payment_allocation")}
}

// The key stays in reversed_by_id, unique, with its foreign key named;
// the inverse edge gets no column of its own.
func TestSelfOneToOneKeyOnAssoc(t *testing.T) {
	g := mustBuild(t, tcAllocation{})
	e := g.Entity("tcAllocation")
	for _, ed := range e.Edges {
		if ed.Field != "reversed_by_id" || !ed.KeyOnAssoc || ownFK(ed) == ed.Inverse {
			t.Errorf("edge %s: field %q, KeyOnAssoc %v, ownFK %v", ed.Name, ed.Field, ed.KeyOnAssoc, ownFK(ed))
		}
	}
	if len(e.Fields) != 2 {
		t.Errorf("fields %d: an implicit key was added", len(e.Fields))
	}
	s, err := Tables(g, velox.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	tb := s.Tables[0]
	if len(tb.ForeignKeys) != 1 || tb.ForeignKeys[0].Name != "payment_allocation_payment_allocation_reversed_by" || tb.ForeignKeys[0].Columns[0] != "reversed_by_id" {
		t.Errorf("foreign keys %+v", tb.ForeignKeys)
	}
	if !strings.Contains(indexNamesOf(s, "payment_allocation"), "reversed_by_id") {
		t.Errorf("no unique index on reversed_by_id: %s", indexNamesOf(s, "payment_allocation"))
	}
}
