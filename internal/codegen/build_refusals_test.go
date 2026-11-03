package codegen

import (
	"strings"
	"testing"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

// A Unique field that is Optional and not Nillable writes one value for every
// unset row, so the second create without it fails on the index.
type uniqueOptional struct{ schema.Schema }

func (uniqueOptional) Fields() []schema.Field {
	return []schema.Field{field.String("slug").Unique().Optional()}
}

type uniqueOptionalDefault struct{ schema.Schema }

func (uniqueOptionalDefault) Fields() []schema.Field {
	return []schema.Field{field.String("slug").Unique().Optional().Default("x")}
}

type uniqueOptionalIndex struct{ schema.Schema }

func (uniqueOptionalIndex) Fields() []schema.Field {
	return []schema.Field{field.String("slug").Optional()}
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
		field.String("d").Optional(),
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
		{uniqueOptional{}, `schema uniqueOptional: field "slug": ` + why},
		{uniqueOptionalDefault{}, `schema uniqueOptionalDefault: field "slug": ` + why},
		{uniqueOptionalIndex{}, `schema uniqueOptionalIndex: field "slug": ` + why},
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

func TestBuildZeroDefaultAllowed(t *testing.T) {
	mustBuild(t, zeroDefaults{})
	_, err := build(t, nonZeroDefault{})
	want := `schema nonZeroDefault: field "on": a Default or DefaultFunc on a bool or number that is not Nillable cannot be told from unset when a Go struct is inserted, so false or 0 could never be stored; make the field Nillable, give it its zero value as the Default (name it so that false or 0 is the default), or drop the Default; a GraphQL create input can apply one with graphql.CreateDefault`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v\nwant %s", err, want)
	}
	if n := len(err.(interface{ Unwrap() []error }).Unwrap()); n != 1 {
		t.Errorf("%d errors, want 1, as -0 is zero: %v", n, err)
	}
}
