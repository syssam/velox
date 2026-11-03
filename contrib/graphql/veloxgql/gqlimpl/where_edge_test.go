package gqlimpl_test

import (
	"context"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

// childWhere is a where input of child, as velox-gen would write it, with
// the fields the relation helpers' tests use.
type childWhere struct {
	Title          *string
	EditorIDIsNil  *bool
	EditorIDNotNil *bool
}

func (w *childWhere) PredicateGQL(ctx context.Context) (velox.Predicate, error) {
	f := gqlimpl.NewFilter(ctx, "ChildWhereInput", velox.TableOf[child]())
	if w == nil {
		return f.Pred()
	}
	gqlimpl.Value(f, "title", w.Title, int(children.Title.I), velox.PredEQ)
	gqlimpl.Is(f, "editorIDIsNil", w.EditorIDIsNil, int(children.EditorID.I), velox.PredIsNull)
	gqlimpl.Is(f, "editorIDNotNil", w.EditorIDNotNil, int(children.EditorID.I), velox.PredNotNull)
	return f.Pred()
}

// filtered is the IDs of the rows of E that the filter build makes match,
// in ID order, and the error of the filter or the query.
func filtered[E any](t *testing.T, db *velox.DB, build func(f *gqlimpl.Filter)) ([]int64, error) {
	t.Helper()
	ctx := context.Background()
	tbl := velox.TableOf[E]()
	f := gqlimpl.NewFilter(ctx, "In", tbl)
	build(f)
	p, err := f.Pred()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryTable(tbl).Where(p).OrderBy(velox.OrderTerm{Column: 0}).Unbounded().All(ctx)
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
	edge, ct := int(owners.Children.I), velox.TableOf[child]()
	for _, c := range []struct {
		name  string
		build func(f *gqlimpl.Filter)
		want  []int64
	}{
		{"hasChildren", func(f *gqlimpl.Filter) { gqlimpl.Has(f, "hasChildren", tru, edge, ct) }, []int64{1, 2}},
		{"hasChildren false", func(f *gqlimpl.Filter) { gqlimpl.Has(f, "hasChildren", fls, edge, ct) }, []int64{3}},
		{"hasChildren nil", func(f *gqlimpl.Filter) { gqlimpl.Has(f, "hasChildren", nil, edge, ct) }, []int64{1, 2, 3}},
		{"hasChildrenWith a", func(f *gqlimpl.Filter) {
			gqlimpl.HasWith(f, "hasChildrenWith", []*childWhere{{Title: new("a")}}, edge, ct)
		}, []int64{1, 2}},
		{"hasChildrenWith d", func(f *gqlimpl.Filter) {
			gqlimpl.HasWith(f, "hasChildrenWith", []*childWhere{{Title: new("d")}}, edge, ct)
		}, []int64{1}},
		{"hasChildrenWith both, of one child", func(f *gqlimpl.Filter) {
			gqlimpl.HasWith(f, "hasChildrenWith", []*childWhere{{Title: new("b")}, {EditorIDNotNil: tru}}, edge, ct)
		}, []int64{1, 2}},
		{"hasChildrenWith nil", func(f *gqlimpl.Filter) {
			gqlimpl.HasWith(f, "hasChildrenWith", []*childWhere(nil), edge, ct)
		}, []int64{1, 2, 3}},
		{"hasChildrenWith none", func(f *gqlimpl.Filter) {
			gqlimpl.HasWith(f, "hasChildrenWith", []*childWhere{}, edge, ct)
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
		got, err := filtered[child](t, db, func(f *gqlimpl.Filter) { gqlimpl.And(f, "and", []*childWhere{c.w}) })
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
		v    any
		list bool
		col  int
		want []int64
	}{
		{"*int", new(30), false, age, []int64{1}},
		{"*int32", new(int32(20)), false, age, []int64{2}},
		{"*small, by reflect", new(small(10)), false, age, []int64{3}},
		{"nil *small", (*small)(nil), false, age, []int64{1, 2, 3}},
		{"nil *int", (*int)(nil), false, age, []int64{1, 2, 3}},
		{"nil", nil, false, age, []int64{1, 2, 3}},
		{"*string", new("b"), false, name, []int64{2}},
		{"[]int", []int{10, 30}, true, age, []int64{1, 3}},
		{"[]int32", []int32{20}, true, age, []int64{2}},
		{"[]small, by reflect", []small{10, 20}, true, age, []int64{2, 3}},
		{"nil []small", []small(nil), true, age, []int64{1, 2, 3}},
		{"nil list", nil, true, age, []int64{1, 2, 3}},
		{"[]string", []string{"a", "c"}, true, name, []int64{1, 3}},
	} {
		got, err := filtered[owner](t, db, func(f *gqlimpl.Filter) {
			if c.list {
				gqlimpl.List(f, "f", c.v, c.col, velox.PredIn)
			} else {
				gqlimpl.Value(f, "f", c.v, c.col, velox.PredEQ)
			}
		})
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: %v %v, want %v", c.name, got, err, c.want)
		}
	}
	for name, build := range map[string]func(f *gqlimpl.Filter){
		"*float64 for an int": func(f *gqlimpl.Filter) { gqlimpl.Value(f, "f", new(1.5), age, velox.PredEQ) },
		"*bool for an int":    func(f *gqlimpl.Filter) { gqlimpl.Value(f, "f", new(true), age, velox.PredEQ) },
		"[]float64 for an int": func(f *gqlimpl.Filter) {
			gqlimpl.List(f, "f", []float64{1.5}, age, velox.PredIn)
		},
	} {
		if _, err := filtered[owner](t, db, build); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
