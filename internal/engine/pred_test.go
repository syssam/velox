package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
)

func TestSound(t *testing.T) {
	leaf := engine.Node{Op: engine.OpEQ}
	for _, c := range []struct {
		name string
		n    engine.Node
		want bool
	}{
		{"a leaf", leaf, true},
		{"an And of leaves", engine.Node{Op: engine.OpAnd, Kids: []engine.Node{leaf, leaf}}, true},
		{"the zero condition", engine.Node{}, false},
		{"an invalid condition", engine.Node{Op: engine.OpInvalid}, false},
		{"an EXISTS", engine.Node{Op: engine.OpExists}, false},
		{"a nested EXISTS", engine.Node{Op: engine.OpOr, Kids: []engine.Node{leaf, {Op: engine.OpNot, Kids: []engine.Node{{Op: engine.OpExists}}}}}, false},
	} {
		if got := engine.Sound(c.n); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// TestResolveErr: the error is that of the first relation that cannot resolve,
// at any depth.
func TestResolveErr(t *testing.T) {
	bad := errors.New("no such entity")
	ref := engine.RelRef{Owner: func() (*velox.EntityInfo, error) { return nil, bad }}
	exists := engine.Node{Op: engine.OpExists, V: ref}
	leaf := engine.Node{Op: engine.OpEQ}
	for _, c := range []struct {
		name string
		n    engine.Node
		want error
	}{
		{"a leaf", leaf, nil},
		{"a RelRef that is no EXISTS", engine.Node{Op: engine.OpEQ, V: ref}, nil},
		{"an EXISTS", exists, bad},
		{"a nested EXISTS", engine.Node{Op: engine.OpAnd, Kids: []engine.Node{leaf, {Op: engine.OpOr, Kids: []engine.Node{exists}}}}, bad},
	} {
		if err := engine.ResolveErr(c.n); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	if err := engine.ResolveErr(engine.PredNode(velox.Predicate(velox.Rel[company, employee]{I: 0}.Has()))); err != nil {
		t.Errorf("a relation that resolves: %v", err)
	}
}

// TestResolveStatement: the relations of Where and of the conflict's Where
// resolve on copies, as the caller's may be shared with other runs. With nothing
// to resolve, the statement keeps what it had.
func TestResolveStatement(t *testing.T) {
	has := velox.Predicate(velox.Rel[company, employee]{I: 0}.Has())
	broken := velox.Predicate(velox.Rel[company, employee]{I: 9}.Has())
	plain := engine.ColumnEQ(0, int64(1))
	isLink := func(p velox.Predicate) bool { _, ok := engine.PredNode(p).V.(engine.Link); return ok }
	isRef := func(p velox.Predicate) bool { _, ok := engine.PredNode(p).V.(engine.RelRef); return ok }

	where, cf := []velox.Predicate{plain, has}, &velox.ConflictClause{Where: []velox.Predicate{has}}
	st := &velox.Statement{Where: where, Conflict: cf}
	if err := engine.ResolveStatement(st); err != nil {
		t.Fatal(err)
	}
	if !isLink(st.Where[1]) || !isRef(where[1]) {
		t.Errorf("Where: resolved %v, the caller's left %v", isLink(st.Where[1]), isRef(where[1]))
	}
	if st.Conflict == cf || !isLink(st.Conflict.Where[0]) || !isRef(cf.Where[0]) {
		t.Errorf("Conflict: a copy %v, resolved %v, the caller's left %v", st.Conflict != cf, isLink(st.Conflict.Where[0]), isRef(cf.Where[0]))
	}

	where, cf = []velox.Predicate{plain}, &velox.ConflictClause{Where: []velox.Predicate{plain}}
	st = &velox.Statement{Where: where, Conflict: cf}
	if err := engine.ResolveStatement(st); err != nil {
		t.Fatal(err)
	}
	if &st.Where[0] != &where[0] || st.Conflict != cf {
		t.Error("a statement with no relation was copied")
	}

	for name, st := range map[string]*velox.Statement{
		"Where":    {Where: []velox.Predicate{broken}},
		"Conflict": {Conflict: &velox.ConflictClause{Where: []velox.Predicate{broken}}},
	} {
		if err := engine.ResolveStatement(st); err == nil || !strings.Contains(err.Error(), "has no relation 9") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
