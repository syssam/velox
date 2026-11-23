package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestGuards: a guarded policy or interceptor function is called under a
// frame of its own, and under a call of itself it is skipped: a condition
// adds none, an Insert passes. A nil function stays nil.
func TestGuards(t *testing.T) {
	if guardCond(nil, kindPolicy) != nil || guardWrite(nil) != nil || guardInsert(nil) != nil {
		t.Fatal("a nil function: want nil")
	}
	ctx := context.Background()
	refused := errors.New("refused")
	var cond func(context.Context) (Predicate, error)
	cond = guardCond(func(c context.Context) (Predicate, error) {
		if !strings.HasSuffix(frameOf(c).String(), ".Interceptor") {
			t.Errorf("the frame: %s", frameOf(c))
		}
		if p, err := cond(c); err != nil || p.n.Op != OpAllow {
			t.Errorf("cond under itself: %+v, %v; want Allow", p.n, err)
		}
		return Predicate{Node{Op: OpEQ, Col: 1, V: int64(1)}}, nil
	}, kindInterceptor)
	var write func(context.Context, Op) (Predicate, error)
	write = guardWrite(func(c context.Context, _ Op) (Predicate, error) {
		if !strings.HasSuffix(frameOf(c).String(), ".Policy") {
			t.Errorf("the frame: %s", frameOf(c))
		}
		if p, err := write(c, OpUpdate); err != nil || p.n.Op != OpAllow {
			t.Errorf("write under itself: %+v, %v; want Allow", p.n, err)
		}
		return Predicate{Node{Op: OpEQ, Col: 2, V: int64(1)}}, nil
	})
	var insert func(context.Context, Row) error
	insert = guardInsert(func(c context.Context, _ Row) error {
		if err := insert(c, Row{}); err != nil {
			t.Errorf("insert under itself: %v; want it to pass", err)
		}
		return refused
	})
	if p, err := cond(ctx); err != nil || p.n.Col != 1 {
		t.Errorf("cond: %+v, %v", p.n, err)
	}
	if p, err := write(ctx, OpUpdate); err != nil || p.n.Col != 2 {
		t.Errorf("write: %+v, %v", p.n, err)
	}
	if err := insert(ctx, Row{}); !errors.Is(err, refused) {
		t.Errorf("insert: %v", err)
	}
}
