package codegen

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// A Unique field that is Optional and not Nillable has one Default for every
// unset row, so the second create without it fails on the index. One with no
// Default at all is rule 37's.
type uniqueOptional struct{ schema.Schema }

func (uniqueOptional) Fields() []schema.Field {
	return []schema.Field{field.String("slug").Unique().Optional()}
}

type uniqueOptionalDefault struct{ schema.Schema }

func (uniqueOptionalDefault) Fields() []schema.Field {
	return []schema.Field{field.String("slug").Unique().Optional().Default("x")}
}

// uniqueOptionalSchemaDefault's SchemaDefault would give each row its own
// value only if velox left the column out of the INSERT, which it does not.
type uniqueOptionalSchemaDefault struct{ schema.Schema }

func (uniqueOptionalSchemaDefault) Fields() []schema.Field {
	return []schema.Field{field.String("slug").Unique().Optional().Default("x").
		SchemaDefault(map[string]string{field.Postgres: "gen_random_uuid()::text"})}
}

type uniqueOptionalIndex struct{ schema.Schema }

func (uniqueOptionalIndex) Fields() []schema.Field {
	return []schema.Field{field.String("slug").Optional().Default("x")}
}

func (uniqueOptionalIndex) Indexes() []schema.Index {
	return []schema.Index{index.Fields("slug").Unique()}
}

type uniqueOptionalOK struct{ schema.Schema }

func (uniqueOptionalOK) Fields() []schema.Field {
	return []schema.Field{
		field.String("a").Unique().Optional().Nillable(),
		field.String("b").Unique().Optional().DefaultFunc(func() string { return "" }),
		field.String("c").Unique(),
		field.String("d").Optional().Default(""),
	}
}

func (uniqueOptionalOK) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("d").Unique().Where("d <> ''"),
		index.Fields("c", "d").Unique(),
	}
}

func TestBuildUniqueOptionalRefused(t *testing.T) {
	const why = "an Optional field that is Unique holds one value on every unset row, so the second row without it fails; make it Nillable, whose NULLs do not collide, or give it a DefaultFunc that a value a hook fills can stand in for, as velox-gen cannot see a hook"
	for _, c := range []struct {
		s    schema.Interface
		want string
	}{
		{uniqueOptional{}, `schema uniqueOptional: field "slug": an Optional field needs a Default, a DefaultFunc or Nillable: unset, it has no value to store, and velox writes no zero in its place`},
		{uniqueOptionalDefault{}, `schema uniqueOptionalDefault: field "slug": ` + why},
		{uniqueOptionalIndex{}, `schema uniqueOptionalIndex: field "slug": ` + why},
		{uniqueOptionalSchemaDefault{}, `schema uniqueOptionalSchemaDefault: field "slug": ` + why},
	} {
		_, err := build(t, c.s)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%T:\n got  %v\nwant %s", c.s, err, c.want)
			continue
		}
		if n := len(err.(interface{ Unwrap() []error }).Unwrap()); n != 1 {
			t.Errorf("%T: %d errors, want 1: %v", c.s, n, err)
		}
	}
	mustBuild(t, uniqueOptionalOK{})
}

// Two edges cannot share one foreign-key field: the key holds one row's ID.
type twoEdgesOneKey struct{ schema.Schema }

func (twoEdgesOneKey) Fields() []schema.Field {
	return []schema.Field{field.Int64("target_id")}
}

func (twoEdgesOneKey) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("b", keyTargetB.Type).Ref("rows").Unique().Required().Field("target_id"),
		edge.From("c", keyTargetC.Type).Ref("rows").Unique().Required().Field("target_id"),
	}
}

type keyTargetB struct{ schema.Schema }

func (keyTargetB) Edges() []schema.Edge {
	return []schema.Edge{edge.To("rows", twoEdgesOneKey.Type)}
}

type keyTargetC struct{ schema.Schema }

func (keyTargetC) Edges() []schema.Edge {
	return []schema.Edge{edge.To("rows", twoEdgesOneKey.Type)}
}

func TestBuildTwoEdgesOnOneKeyRefused(t *testing.T) {
	_, err := build(t, twoEdgesOneKey{}, keyTargetB{}, keyTargetC{})
	want := `schema twoEdgesOneKey: edge "c": binds field "target_id", which holds edge "b"'s foreign key: a key holds one row's ID; declare a field for each edge`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v\nwant %s", err, want)
	}
}

// A name that is not ASCII has no table, column or label velox can derive:
// plugin.Snake and plugin.Plural work on bytes, as ent's do.
type Café struct{ schema.Schema }

type accents struct{ schema.Schema }

func (accents) Fields() []schema.Field { return []schema.Field{field.String("prénom")} }

func (accents) Edges() []schema.Edge { return []schema.Edge{edge.To("é", Pet.Type)} }

func TestBuildNonASCIINamesRefused(t *testing.T) {
	_, err := build(t, Café{})
	want := `schema Café: its name is not ASCII, and velox derives table, column and Go names from it: rename the schema`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v\nwant %s", err, want)
	}
	_, err = build(t, accents{}, Pet{})
	for _, want := range []string{
		`schema accents: field "prénom": its name is not ASCII, and velox derives column and Go names from it: rename it, and give the column its name with StorageKey`,
		`schema accents: edge "é": its name is not ASCII, and velox derives column and Go names from it: rename it`,
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("got %v\nwant %s", err, want)
		}
	}
}

// A Default that is the Go zero value stores what an unset field stores, so
// it is no loss on a bool or number that is not Nillable.
type zeroDefaults struct{ schema.Schema }

func (zeroDefaults) Fields() []schema.Field {
	return []schema.Field{
		field.Bool("active").Default(false),
		field.Int64("views").Default(0),
		field.Float("score").Optional().Default(0),
		field.Uint8("rank").Default(0),
		field.String("s").Default(""),
	}
}

type nonZeroDefault struct{ schema.Schema }

func (nonZeroDefault) Fields() []schema.Field {
	return []schema.Field{field.Int64("views").Default(-0).Min(-5), field.Bool("on").Default(true)}
}

// defaultWaysOut has the lines rule 36's message tells an ent user to write
// for field.Bool("on").Default(true) and field.Float("score").Default(1.5).
type defaultWaysOut struct{ schema.Schema }

func (defaultWaysOut) Fields() []schema.Field {
	return []schema.Field{
		field.Bool("on").Nillable().Default(true),
		field.Bool("active").SchemaDefault(map[string]string{field.Postgres: "true", field.MySQL: "1", field.SQLite: "1"}),
		field.Float("score").SchemaDefault(map[string]string{field.Postgres: "1.5", field.MySQL: "1.5", field.SQLite: "1.5"}),
	}
}

// The ways out build, and give the columns the message says: Nillable a
// NULL-able column with the default, SchemaDefault a NOT NULL column with
// it, as an ent database has.
func TestBuildDefaultWaysOut(t *testing.T) {
	for _, d := range allDialects {
		tb := table(t, tables(t, d, defaultWaysOut{}), "default_ways_outs")
		one := map[velox.Dialect]string{velox.Postgres: "true", velox.MySQL: "1", velox.SQLite: "1"}[d]
		for _, c := range []struct {
			name, def string
			null      bool
		}{{"on", one, true}, {"active", one, false}, {"score", "1.5", false}} {
			if col := column(t, tb, c.name); col.Default != c.def || col.Nullable != c.null {
				t.Errorf("%v: %s: DEFAULT %q, NULL %v; want %q, %v", d, c.name, col.Default, col.Nullable, c.def, c.null)
			}
		}
	}
}

func TestBuildZeroDefaultAllowed(t *testing.T) {
	mustBuild(t, zeroDefaults{})
	_, err := build(t, nonZeroDefault{})
	// The message spells the ent user's ways out with the field's own line.
	want := `schema nonZeroDefault: field "on": field.Bool("on") with Default(true): velox inserts a Go struct, whose false cannot be told from unset, so the Default would overwrite an explicit false (ent fills it only when the builder never set the field). ` +
		`Write field.Bool("on").Nillable().Default(true), a pointer whose nil stores true (the column then holds NULL); or rename it so that false is the default ("disabled" for "enabled"); or drop the Default and set the value in the struct. ` +
		`To keep an ent database's column, NOT NULL DEFAULT true, add .SchemaDefault(map[string]string{field.Postgres: "true", field.MySQL: "1", field.SQLite: "1"}), which only rows written outside velox take. ` +
		`graphql.CreateDefault(true) gives a GraphQL create input the default`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v\nwant %s", err, want)
	}
	if n := len(err.(interface{ Unwrap() []error }).Unwrap()); n != 1 {
		t.Errorf("%d errors, want 1, as -0 is zero: %v", n, err)
	}
}

// A number whose check refuses 0 never stores 0, so reading 0 as unset loses
// nothing, and a non-zero Default or DefaultFunc on it is allowed.
type zeroInvalidDefaults struct{ schema.Schema }

func (zeroInvalidDefaults) Fields() []schema.Field {
	return []schema.Field{
		field.Int("seats").Positive().Default(100),
		field.Int("port").Min(1).Default(8080),
		field.Int("rank").Range(1, 9).Default(5),
		field.Int("debt").Negative().Default(-1),
		field.Int("below").Max(-1).Default(-3),
		field.Int("count").Positive().DefaultFunc(func() int { return 1 }),
	}
}

// zeroValid has the same lines with a check that allows 0, or none, and a
// bool: each is refused, one error a field.
type zeroValid struct{ schema.Schema }

func (zeroValid) Fields() []schema.Field {
	return []schema.Field{
		field.Int("a").NonNegative().Default(1),
		field.Int("b").Min(0).Default(1),
		field.Int("c").Min(-1).Default(1),
		field.Int("d").Max(0).Default(-1),
		field.Int("e").Range(0, 9).Default(1),
		field.Int("f").Default(1),
		field.Bool("g").Default(true),
	}
}

func TestBuildZeroInvalidDefault(t *testing.T) {
	mustBuild(t, zeroInvalidDefaults{})
	_, err := build(t, zeroValid{})
	if err == nil {
		t.Fatal("no error")
	}
	if n := len(err.(interface{ Unwrap() []error }).Unwrap()); n != 7 {
		t.Errorf("%d errors, want 7: %v", n, err)
	}
	if !strings.Contains(err.Error(), "or, if the field is never 0, add Positive() or Min(1)") {
		t.Errorf("no way out for a number: %v", err)
	}
}
