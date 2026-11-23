package engine

import (
	"context"
	"errors"
	"testing"
	"unsafe"
)

// condOf is a Read condition that gives n's predicate, or err.
func condOf(n Node, err error) func(context.Context, *DB) (Predicate, error) {
	return func(context.Context, *DB) (Predicate, error) { return Predicate{n}, err }
}

func TestAndEach(t *testing.T) {
	eq := func(c int32) Node { return Node{Op: OpEQ, Col: c, V: int64(1)} }
	ctx := context.Background()
	boom := errors.New("boom")
	for _, c := range []struct {
		name string
		fs   []func(context.Context, *DB) (Predicate, error)
		want Node
		err  error
	}{
		{"an error stops", []func(context.Context, *DB) (Predicate, error){condOf(eq(1), nil), condOf(Node{}, boom)}, Node{}, boom},
		{"a zero condition gives the zero predicate", []func(context.Context, *DB) (Predicate, error){condOf(eq(1), nil), condOf(Node{}, nil)}, Node{}, nil},
		{"allows are skipped, none left", []func(context.Context, *DB) (Predicate, error){condOf(Node{Op: OpAllow}, nil), condOf(Node{Op: OpAllow}, nil)}, Node{Op: OpAllow}, nil},
		{"one left is itself", []func(context.Context, *DB) (Predicate, error){condOf(Node{Op: OpAllow}, nil), condOf(eq(2), nil)}, eq(2), nil},
		{"several are ANDed", []func(context.Context, *DB) (Predicate, error){condOf(eq(1), nil), condOf(eq(2), nil)}, Group(OpAnd, []Node{eq(1), eq(2)}), nil},
	} {
		// A trailing allow keeps andConds from returning a lone condition as is.
		got, err := andConds(append(c.fs, condOf(Node{Op: OpAllow}, nil)))(ctx, nil)
		if !errors.Is(err, c.err) || got.n.Op != c.want.Op || got.n.Col != c.want.Col || len(got.n.Kids) != len(c.want.Kids) {
			t.Errorf("%s: got %+v, %v; want %+v, %v", c.name, got.n, err, c.want, c.err)
		}
	}
	if andConds(nil) != nil {
		t.Error("andConds of none: want nil")
	}
}

func TestAndWriteConds(t *testing.T) {
	ctx := context.Background()
	var ops []Op
	w := func(n Node) func(context.Context, *DB, Op) (Predicate, error) {
		return func(_ context.Context, _ *DB, op Op) (Predicate, error) {
			ops = append(ops, op)
			return Predicate{n}, nil
		}
	}
	if andWriteConds(nil) != nil || andWriteConds([]func(context.Context, *DB, Op) (Predicate, error){nil}) != nil {
		t.Error("none or only nils: want nil")
	}
	a := Node{Op: OpEQ, Col: 1, V: int64(1)}
	one := andWriteConds([]func(context.Context, *DB, Op) (Predicate, error){nil, w(a)})
	if p, err := one(ctx, nil, OpDelete); err != nil || p.n.Col != 1 || len(ops) != 1 || ops[0] != OpDelete {
		t.Errorf("one left: %+v, %v, ops %v", p.n, err, ops)
	}
	ops = nil
	two := andWriteConds([]func(context.Context, *DB, Op) (Predicate, error){w(a), nil, w(Node{Op: OpEQ, Col: 2, V: int64(1)})})
	p, err := two(ctx, nil, OpUpdate)
	if err != nil || p.n.Op != OpAnd || len(p.n.Kids) != 2 || len(ops) != 2 || ops[0] != OpUpdate || ops[1] != OpUpdate {
		t.Errorf("two: %+v, %v, ops %v; want an AND of both, each given the op", p.n, err, ops)
	}
}

func TestAndPolicies(t *testing.T) {
	ctx := context.Background()
	if AndPolicies(nil) != nil || AndPolicies([]*EntityPolicy{nil, nil}) != nil {
		t.Error("none or only nils: want nil")
	}
	one := &EntityPolicy{Read: condOf(Node{Op: OpAllow}, nil)}
	if AndPolicies([]*EntityPolicy{nil, one}) != one {
		t.Error("one left: want it as is")
	}

	var order []string
	denied := errors.New("denied")
	ins := func(name string, err error) func(context.Context, *DB, unsafe.Pointer) error {
		return func(context.Context, *DB, unsafe.Pointer) error {
			order = append(order, name)
			return err
		}
	}
	eq := func(c int32) Node { return Node{Op: OpEQ, Col: c, V: int64(1)} }
	a := &EntityPolicy{Read: condOf(eq(1), nil), Query: condOf(eq(3), nil), Insert: ins("a", nil)}
	b := &EntityPolicy{Read: condOf(eq(2), nil), Write: func(context.Context, *DB, Op) (Predicate, error) { return Predicate{eq(4)}, nil }, Insert: ins("b", nil)}
	got := AndPolicies([]*EntityPolicy{a, nil, b})
	if got == a || got == b {
		t.Fatal("two policies: want a new one")
	}
	if p, err := got.Read(ctx, nil); err != nil || p.n.Op != OpAnd || len(p.n.Kids) != 2 {
		t.Errorf("Read: %+v, %v; want both ANDed", p.n, err)
	}
	if p, err := got.Query(ctx, nil); err != nil || p.n.Col != 3 {
		t.Errorf("Query: %+v, %v; want a's alone", p.n, err)
	}
	if p, err := got.Write(ctx, nil, OpUpdate); err != nil || p.n.Col != 4 {
		t.Errorf("Write: %+v, %v; want b's alone", p.n, err)
	}
	if err := got.Insert(ctx, nil, nil); err != nil || len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Errorf("Insert: %v, order %v; want a then b", err, order)
	}
	order = nil
	c := &EntityPolicy{Insert: ins("a", denied)}
	if err := AndPolicies([]*EntityPolicy{c, b}).Insert(ctx, nil, nil); !errors.Is(err, denied) || len(order) != 1 {
		t.Errorf("a denying Insert: %v, order %v; want b not run", err, order)
	}
	order = nil
	d := &EntityPolicy{Read: condOf(eq(1), nil)}
	if got := AndPolicies([]*EntityPolicy{d, a}); got.Insert == nil || got.Write != nil {
		t.Error("one Insert among several: kept as is, and no Write")
	}
}

func TestNilEncoded(t *testing.T) {
	n := 1
	for _, c := range []struct {
		name string
		col  ColumnInfo
		v    any
		want bool
	}{
		{"a nullable codec column holding nil", ColumnInfo{Nullable: true, Codec: true}, nil, true},
		{"a nil slice", ColumnInfo{Nullable: true, Codec: true}, []string(nil), true},
		{"a nil map", ColumnInfo{Nullable: true, Codec: true}, map[string]int(nil), true},
		{"a nil pointer", ColumnInfo{Nullable: true, Codec: true}, (*int)(nil), true},
		{"an empty slice", ColumnInfo{Nullable: true, Codec: true}, []string{}, false},
		{"a pointer", ColumnInfo{Nullable: true, Codec: true}, &n, false},
		{"a struct", ColumnInfo{Nullable: true, Codec: true}, struct{}{}, false},
		{"not nullable", ColumnInfo{Codec: true}, nil, false},
		{"no codec", ColumnInfo{Nullable: true}, nil, false},
	} {
		if got := nilEncoded(&c.col, c.v); got != c.want {
			t.Errorf("%s: nilEncoded = %v, want %v", c.name, got, c.want)
		}
	}
}
