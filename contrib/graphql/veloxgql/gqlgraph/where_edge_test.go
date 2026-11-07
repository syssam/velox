package gqlgraph_test

import (
	"context"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

// childWhere is a where input of child, as velox-gen would write it, with
// the fields the relation helpers' tests use.
type childWhere struct {
	Title          *string
	EditorIDIsNil  *bool
	EditorIDNotNil *bool
}

func (w *childWhere) predicate(ctx context.Context) (velox.Predicate, error) {
	f := gqlgraph.NewFilter(ctx, gqlgraph.TableType[child]("Child"))
	if w == nil {
		return f.Pred()
	}
	gqlgraph.Field(f, "title", int(children.Title.I), gqlgraph.Ops[string]{EQ: w.Title})
	gqlgraph.Field(f, "editorID", int(children.EditorID.I), gqlgraph.Ops[bool]{IsNil: w.EditorIDIsNil, NotNil: w.EditorIDNotNil})
	return f.Pred()
}

// filtered is the IDs of the rows of E that the filter build makes match,
// in ID order, and the error of the filter or the query.
func filtered[E any](t *testing.T, db *velox.DB, build func(f *gqlgraph.Filter)) ([]int64, error) {
	t.Helper()
	ctx := context.Background()
	tbl := untyped.TableOf[E]()
	f := gqlgraph.NewFilter(ctx, gqlgraph.TableType[E]("In"))
	build(f)
	p, err := f.Pred()
	if err != nil {
		return nil, err
	}
	rows, err := untyped.Query(db, tbl).Where(p).OrderBy(velox.OrderTerm{Column: 0}).Unbounded().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]int64, rows.Len())
	for i := range out {
		out[i] = *tbl.Field(rows.At(i), 0).(*int64)
	}
	return out, nil
}

// TestWhereRelations: has<Edge> and has<Edge>With match a node by its
// related nodes, IsNil and NotNil by a nullable column, each negated for
// false; a nil flag and an empty list add nothing.
func TestWhereRelations(t *testing.T) {
	db, _, _ := edgeDB(t)
	tru, fls := new(true), new(false)
	edge, ct := int(owners.Children.I), untyped.TableOf[child]()
	for _, c := range []struct {
		name  string
		build func(f *gqlgraph.Filter)
		want  []int64
	}{
		{"hasChildren", func(f *gqlgraph.Filter) { gqlgraph.Has(f, "hasChildren", tru, edge, ct) }, []int64{1, 2}},
		{"hasChildren false", func(f *gqlgraph.Filter) { gqlgraph.Has(f, "hasChildren", fls, edge, ct) }, []int64{3}},
		{"hasChildren nil", func(f *gqlgraph.Filter) { gqlgraph.Has(f, "hasChildren", nil, edge, ct) }, []int64{1, 2, 3}},
		{"hasChildrenWith a", func(f *gqlgraph.Filter) {
			gqlgraph.HasWith(f, "hasChildrenWith", []*childWhere{{Title: new("a")}}, (*childWhere).predicate, edge, ct)
		}, []int64{1, 2}},
		{"hasChildrenWith d", func(f *gqlgraph.Filter) {
			gqlgraph.HasWith(f, "hasChildrenWith", []*childWhere{{Title: new("d")}}, (*childWhere).predicate, edge, ct)
		}, []int64{1}},
		{"hasChildrenWith both, of one child", func(f *gqlgraph.Filter) {
			gqlgraph.HasWith(f, "hasChildrenWith", []*childWhere{{Title: new("b")}, {EditorIDNotNil: tru}}, (*childWhere).predicate, edge, ct)
		}, []int64{1, 2}},
		{"hasChildrenWith nil", func(f *gqlgraph.Filter) {
			gqlgraph.HasWith(f, "hasChildrenWith", []*childWhere(nil), (*childWhere).predicate, edge, ct)
		}, []int64{1, 2, 3}},
		{"hasChildrenWith none", func(f *gqlgraph.Filter) {
			gqlgraph.HasWith(f, "hasChildrenWith", []*childWhere{}, (*childWhere).predicate, edge, ct)
		}, []int64{1, 2, 3}},
	} {
		got, err := filtered[owner](t, db, c.build)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: %v %v, want %v", c.name, got, err, c.want)
		}
	}
	for _, c := range []struct {
		name string
		w    *childWhere
		want []int64
	}{
		{"editorIDIsNil", &childWhere{EditorIDIsNil: tru}, []int64{7}},
		{"editorIDIsNil false", &childWhere{EditorIDIsNil: fls}, []int64{1, 2, 3, 4, 5, 6}},
		{"editorIDNotNil", &childWhere{EditorIDNotNil: tru}, []int64{1, 2, 3, 4, 5, 6}},
		{"editorIDNotNil false", &childWhere{EditorIDNotNil: fls}, []int64{7}},
	} {
		got, err := filtered[child](t, db, func(f *gqlgraph.Filter) { gqlgraph.And(f, "and", []*childWhere{c.w}, (*childWhere).predicate) })
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: %v %v, want %v", c.name, got, err, c.want)
		}
	}
}

// TestWhereValueTypes: a value or a list of any of the Go types an input's
// scalar binds to is converted to the column's type, a nil pointer or nil
// list adds nothing, and a value of another kind is the query's error.
func TestWhereValueTypes(t *testing.T) {
	db, _, _ := edgeDB(t)
	type small int8
	age, name := int(owners.Age.I), int(owners.Name.I)
	for _, c := range []struct {
		name string
		add  func(f *gqlgraph.Filter)
		want []int64
	}{
		{"*int", func(f *gqlgraph.Filter) { gqlgraph.Field(f, "f", age, gqlgraph.Ops[int]{EQ: new(30)}) }, []int64{1}},
		{"*int32", func(f *gqlgraph.Filter) { gqlgraph.Field(f, "f", age, gqlgraph.Ops[int32]{EQ: new(int32(20))}) }, []int64{2}},
		{"*small", func(f *gqlgraph.Filter) { gqlgraph.Field(f, "f", age, gqlgraph.Ops[small]{EQ: new(small(10))}) }, []int64{3}},
		{"nil *small", func(f *gqlgraph.Filter) { gqlgraph.Field(f, "f", age, gqlgraph.Ops[small]{}) }, []int64{1, 2, 3}},
		{"*string", func(f *gqlgraph.Filter) { gqlgraph.Field(f, "f", name, gqlgraph.Ops[string]{EQ: new("b")}) }, []int64{2}},
		{"[]int", func(f *gqlgraph.Filter) { gqlgraph.Field(f, "f", age, gqlgraph.Ops[int]{In: []int{10, 30}}) }, []int64{1, 3}},
		{"[]int32", func(f *gqlgraph.Filter) { gqlgraph.Field(f, "f", age, gqlgraph.Ops[int32]{In: []int32{20}}) }, []int64{2}},
		{"[]small", func(f *gqlgraph.Filter) { gqlgraph.Field(f, "f", age, gqlgraph.Ops[small]{In: []small{10, 20}}) }, []int64{2, 3}},
		{"[]string", func(f *gqlgraph.Filter) { gqlgraph.Field(f, "f", name, gqlgraph.Ops[string]{In: []string{"a", "c"}}) }, []int64{1, 3}},
	} {
		got, err := filtered[owner](t, db, c.add)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: %v %v, want %v", c.name, got, err, c.want)
		}
	}
	for name, build := range map[string]func(f *gqlgraph.Filter){
		"*float64 for an int":  func(f *gqlgraph.Filter) { gqlgraph.Field(f, "f", age, gqlgraph.Ops[float64]{EQ: new(1.5)}) },
		"*bool for an int":     func(f *gqlgraph.Filter) { gqlgraph.Field(f, "f", age, gqlgraph.Ops[bool]{EQ: new(true)}) },
		"[]float64 for an int": func(f *gqlgraph.Filter) { gqlgraph.Field(f, "f", age, gqlgraph.Ops[float64]{In: []float64{1.5}}) },
	} {
		if _, err := filtered[owner](t, db, build); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
