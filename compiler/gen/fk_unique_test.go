package gen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/dialect/sqlschema"
	"github.com/syssam/velox/schema/field"
)

// fkUniqueColumn builds the tables for a one-type graph whose fields and edges the caller
// supplies, and reports whether the column table.col exists and is UNIQUE.
func fkUniqueColumn(t *testing.T, fields []*load.Field, edges []*load.Edge, table, col string) (unique, found bool) {
	t.Helper()
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Storage: drivers["sql"],
		IDType:  &field.TypeInfo{Type: field.TypeInt64},
	}, &load.Schema{Name: "Customer", Fields: fields, Edges: edges})
	require.NoError(t, err)

	tables, err := graph.Tables()
	require.NoError(t, err)
	for _, tb := range tables {
		if tb.Name != table {
			continue
		}
		for _, c := range tb.Columns {
			if c.Name == col {
				return c.Unique, true
			}
		}
	}
	return false, false
}

func fkField(name string) *load.Field {
	return &load.Field{
		Name:     name,
		Info:     &field.TypeInfo{Type: field.TypeInt64},
		Optional: true,
		Nillable: true,
	}
}

func nonUniqueFKAnnotation() map[string]any {
	return map[string]any{(sqlschema.Annotation{}).Name(): sqlschema.NonUniqueFK()}
}

// The default is a guarantee, not an accident: a unidirectional Unique() edge is one-to-one,
// and a one-to-one foreign key is UNIQUE, whether the edge creates its column or binds a
// field to it. migrate_shapes_test pins the same fact end to end ("one card per owner").
func TestFKColumn_FieldBoundOneToOneIsUniqueByDefault(t *testing.T) {
	edges := []*load.Edge{{Name: "merged_into", Type: "Customer", Unique: true, Field: "merged_into_id"}}
	unique, found := fkUniqueColumn(t, []*load.Field{fkField("merged_into_id")}, edges, "customers", "merged_into_id")
	require.True(t, found, "the field-bound column must exist")
	assert.True(t, unique, "without an opt-out a one-to-one foreign key is UNIQUE")
}

func TestFKColumn_EdgeWithoutAFieldIsUniqueByDefault(t *testing.T) {
	edges := []*load.Edge{{Name: "parent", Type: "Customer", Unique: true}}
	unique, found := fkUniqueColumn(t, nil, edges, "customers", "customer_parent")
	require.True(t, found, "the edge creates its own column")
	assert.True(t, unique)
}

// The shape a real schema uses for "many rows point at one": a self-referential to-one edge
// bound to its own column, with no inverse. The opt-out declares what was meant.
func TestFKColumn_NonUniqueFKMakesAFieldBoundEdgeManyToOne(t *testing.T) {
	edges := []*load.Edge{{
		Name: "merged_into", Type: "Customer", Unique: true, Field: "merged_into_id",
		Annotations: nonUniqueFKAnnotation(),
	}}
	unique, found := fkUniqueColumn(t, []*load.Field{fkField("merged_into_id")}, edges, "customers", "merged_into_id")
	require.True(t, found)
	assert.False(t, unique, "NonUniqueFK declares many rows may point at one")
}

func TestFKColumn_NonUniqueFKAppliesToAnEdgeThatOwnsItsColumn(t *testing.T) {
	edges := []*load.Edge{{Name: "parent", Type: "Customer", Unique: true, Annotations: nonUniqueFKAnnotation()}}
	unique, found := fkUniqueColumn(t, nil, edges, "customers", "customer_parent")
	require.True(t, found)
	assert.False(t, unique)
}

// The annotation may sit on the paired edge, as OnDelete's may.
func TestFKColumn_NonUniqueFKOnTheInverseEdgeIsHonored(t *testing.T) {
	edges := []*load.Edge{
		{Name: "reversed_from", Type: "Customer", Unique: true, Field: "reversed_from_id"},
		{Name: "reversed_by", Type: "Customer", Unique: true, Inverse: true, RefName: "reversed_from", Annotations: nonUniqueFKAnnotation()},
	}
	unique, found := fkUniqueColumn(t, []*load.Field{fkField("reversed_from_id")}, edges, "customers", "reversed_from_id")
	require.True(t, found)
	assert.False(t, unique)
}

// The opt-out changes the uniqueness of that one column and nothing else about it.
func TestFKColumn_NonUniqueFKLeavesTheColumnOtherwiseIntact(t *testing.T) {
	build := func(ann map[string]any) (nullable bool, typ field.Type) {
		graph, err := NewGraph(&Config{Package: "test/gen", Storage: drivers["sql"], IDType: &field.TypeInfo{Type: field.TypeInt64}},
			&load.Schema{
				Name:   "Customer",
				Fields: []*load.Field{fkField("merged_into_id")},
				Edges:  []*load.Edge{{Name: "merged_into", Type: "Customer", Unique: true, Field: "merged_into_id", Annotations: ann}},
			})
		require.NoError(t, err)
		tables, err := graph.Tables()
		require.NoError(t, err)
		for _, c := range tables[0].Columns {
			if c.Name == "merged_into_id" {
				return c.Nullable, c.Type
			}
		}
		t.Fatal("column not found")
		return
	}
	n1, t1 := build(nil)
	n2, t2 := build(nonUniqueFKAnnotation())
	assert.Equal(t, n1, n2, "nullability must not change")
	assert.Equal(t, t1, t2, "type must not change")
}

func TestNonUniqueFKAnnotationMerges(t *testing.T) {
	merged := sqlschema.OnDelete(sqlschema.Cascade).Merge(sqlschema.NonUniqueFK())
	ant, ok := merged.(sqlschema.Annotation)
	require.True(t, ok)
	assert.True(t, ant.NonUniqueFK, "the flag must survive a merge with another annotation")
	assert.Equal(t, sqlschema.Cascade, ant.OnDelete, "and must not drop the other annotation's settings")
}
