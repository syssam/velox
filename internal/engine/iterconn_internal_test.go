package engine

import (
	"context"
	"testing"

	"github.com/syssam/velox/internal/fakeconn"
)

// TestHoldConnOnlyOnAPoolOfOne: a DB on any other pool notes nothing, and
// releasing that note leaves another holder's alone.
func TestHoldConnOnlyOnAPoolOfOne(t *testing.T) {
	db := newDB(SQLite, &fakeconn.Exec{}, nil)
	other := &connHold{caller: context.Background()}
	db.Cfg.connHold.Store(other)
	h := holdConn(context.Background(), &db, nil)
	if h != nil || holder(&db) != other {
		t.Errorf("note %v; holder %v", h, holder(&db))
	}
	releaseConn(&db, h)
	if holder(&db) != other {
		t.Errorf("holder %v", holder(&db))
	}
}

// uncomparableCtx is a context whose dynamic type == cannot compare.
type uncomparableCtx struct {
	context.Context
	tags []string
}

// TestConnHoldOwns: the holder owns the context it was given and WithoutTx
// of its fn's, and no other; an uncomparable context does not panic.
func TestConnHoldOwns(t *testing.T) {
	given := t.Context()
	child, cancelChild := context.WithCancel(given)
	defer cancelChild()
	f := &txFrame{}
	f.ctx = txCtx{Context: given, root: f}
	h := &connHold{caller: given, tx: f}
	for name, c := range map[string]struct {
		ctx  context.Context
		want bool
	}{
		"given":        {given, true},
		"WithoutTx":    {noTxCtx{&f.ctx}, true},
		"child":        {child, false},
		"background":   {context.Background(), false},
		"uncomparable": {uncomparableCtx{given, nil}, false},
	} {
		if got := h.owns(c.ctx); got != c.want {
			t.Errorf("%s: owns %v, want %v", name, got, c.want)
		}
	}
	iter := &connHold{caller: uncomparableCtx{given, nil}}
	if iter.owns(uncomparableCtx{given, nil}) {
		t.Error("an uncomparable context is owned")
	}
}
