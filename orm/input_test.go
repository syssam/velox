package orm

import "testing"

func TestApply(t *testing.T) {
	type q struct{ n int }
	got := Apply(&q{}, func(x *q) { x.n++ }, nil, func(x *q) { x.n += 10 })
	if got.n != 11 {
		t.Errorf("got %d", got.n)
	}
	if same := &(q{n: 3}); Apply(same) != same || same.n != 3 {
		t.Error("Apply of nothing changed the query")
	}
}
