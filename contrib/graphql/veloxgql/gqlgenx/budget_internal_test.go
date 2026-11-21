package gqlgenx

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// TestValueBytesLists: a nullable list, a pointer to a list and a list of
// Bytes or JSON count their elements; a list of rows and a nil count 0.
func TestValueBytesLists(t *testing.T) {
	a, b := "ab", `c"`
	type row struct{ S string }
	type name string
	for _, c := range []struct {
		v    any
		want int64
	}{
		{[]*string{&a, nil, &b}, 2 + 3},
		{&[]string{a, b}, 2 + 3},
		{[]veloxgql.Bytes{{1, 2, 3}, {4}}, 4 + 4},
		{[]veloxgql.JSON{veloxgql.JSON(`{"a":1}`), veloxgql.JSON(`[]`)}, 7 + 2},
		{&veloxgql.JSON{'1', '2'}, 2},
		{[]name{"xyz"}, 3},
		{(*[]string)(nil), 0},
		{[]*row{{S: "abc"}}, 0},
		{[]int{1, 2}, 0},
		{json.RawMessage(`{"a":1}`), 7},
		{[]json.RawMessage{json.RawMessage(`[]`)}, 2},
		{selfList{selfList{}}, 0},
		{selfPtr(nil), 0},
	} {
		if got := valueBytes(c.v); got != c.want {
			t.Errorf("valueBytes(%#v) = %d, want %d", c.v, got, c.want)
		}
	}
}

// selfList and selfPtr hold themselves: sized ends at maxSizedDepth, where it
// recursed without end and overflowed the stack.
type (
	selfList []selfList
	selfPtr  *selfPtr
)

// TestDeferEndsTimeout: an operation's Timeout is let go of when its last
// response is out, which for @defer is a later one: done ran on the first
// alone, and with HasNext set it left the timer and its callback to the
// deadline, on a websocket as long as the connection.
func TestDeferEndsTimeout(t *testing.T) {
	e := &Extension{limits: veloxgql.Limits{Timeout: time.Hour}}
	ctx := context.WithValue(context.Background(), operationKey{}, new(operation))
	more, last := true, false
	fctx, r, done := e.operationResponse(ctx)
	done(&graphql.Response{HasNext: &more})
	if fctx.Err() != nil {
		t.Fatal("the first response ended the Timeout, with more to come")
	}
	_, r2, done2 := e.operationResponse(ctx)
	if r2 != r {
		t.Fatal("a later response is not the first one's")
	}
	done2(&graphql.Response{HasNext: &more})
	if fctx.Err() != nil {
		t.Fatal("a middle response ended the Timeout")
	}
	_, _, done3 := e.operationResponse(ctx)
	done3(&graphql.Response{HasNext: &last})
	if !errors.Is(fctx.Err(), context.Canceled) {
		t.Errorf("after the last response the Timeout's context is %v, want cancelled", fctx.Err())
	}
	if r.why.Load() != 0 {
		t.Errorf("letting go of it stopped the response: %d", r.why.Load())
	}
}
