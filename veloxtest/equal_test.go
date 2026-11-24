package veloxtest_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	ts "github.com/syssam/velox/internal/testmodel/schema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/veloxtest"
)

// TestEqualIgnoresTheSource: an entity read through a DB is Equal to the
// same entity built by hand, though reflect.DeepEqual sees its Source;
// another field, or another loaded relation, is not Equal.
func TestEqualIgnoresTheSource(t *testing.T) {
	ctx := context.Background()
	db := veloxtest.Open(t, "sqlite", "", []schema.Interface{ts.Sample{}, ts.Line{}})
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := testmodel.Sample{Name: "a", Status: "new", When: when, Blob: []byte("b")}
	if err := db.Insert(&s).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Insert(&testmodel.Line{SampleID: s.ID}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := db.Query[testmodel.Sample]().With(testmodel.Samples.Lines).First(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got.When = got.When.UTC()
	lines, _ := got.Edges.Lines.Get()
	want := testmodel.Sample{ID: s.ID, Name: "a", Status: "new", When: when, Blob: []byte("b")}
	want.Edges.Lines = velox.LoadedMany([]testmodel.Line{{ID: lines[0].ID, SampleID: s.ID}})
	if reflect.DeepEqual(*got, want) {
		t.Error("reflect.DeepEqual does not see the Source: the test proves nothing")
	}
	if !veloxtest.Equal(*got, want) || !veloxtest.Equal(got, &want) {
		t.Errorf("Equal:\n got %v\nwant %v", got, want)
	}
	other := want
	other.Name = "b"
	if veloxtest.Equal(*got, other) {
		t.Error("Equal of another name")
	}
	other = want
	other.Edges.Lines = velox.LoadedMany([]testmodel.Line{})
	if veloxtest.Equal(*got, other) {
		t.Error("Equal of other lines")
	}
	if veloxtest.Equal(*got, &want) || !veloxtest.Equal(nil, nil) || veloxtest.Equal(nil, 1) {
		t.Error("Equal of values of other types, or of nil")
	}
	if !veloxtest.Equal(map[string][]int{"a": {1}}, map[string][]int{"a": {1}}) || veloxtest.Equal(map[string]int{"a": 1}, map[string]int{"b": 1}) {
		t.Error("Equal of maps")
	}
	type node struct{ Next *node }
	a, b := &node{}, &node{}
	a.Next, b.Next = a, b
	if !veloxtest.Equal(a, b) {
		t.Error("Equal of two cycles")
	}
}
