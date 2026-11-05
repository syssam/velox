package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unsafe"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestSchemaPolicyHooks: an entity's hooks and policy, bound from its Hooks and
// Policy methods, act on its writes.
func TestSchemaPolicyHooks(t *testing.T) {
	info, err := loadDescriptor(hookedDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if len(engine.InfoHooks(info)) != 3 {
		t.Fatalf("%d hooks, want 3", len(engine.InfoHooks(info)))
	}
	e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}, Affected: 1}
	db := openFake(velox.Postgres, e)
	var x hooked
	if err := db.Insert(&x).Exec(ctx); !errors.Is(err, velox.ErrDenied) {
		t.Fatalf("insert of an untitled row: %v", err)
	}
	if x.Tag != "entity;" {
		t.Fatalf("the hook runs before the policy, so on the refused row too: Tag %q", x.Tag)
	}
	x = hooked{Title: "t"}
	if err := db.Insert(&x).Exec(ctx); err != nil || x.Tag != "entity;" {
		t.Fatalf("the insert hook, run before the write: Tag %q, err %v", x.Tag, err)
	}

	p := engine.InfoPolicy(info)
	if p == nil || p.Read == nil || p.Write != nil || p.Insert == nil {
		t.Fatalf("policy %+v", p)
	}
	// The Read policy's condition names title, which the statement resolves.
	re := &fakeconn.Exec{}
	if _, err := openFake(velox.Postgres, re).Query[hooked]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(re.SQL, `WHERE "title" = $1`) || len(re.Args) == 0 || re.Args[0] != "r" {
		t.Errorf("read: %s %v", re.SQL, re.Args)
	}
	x.Title = ""
	if err := p.Insert(ctx, unsafe.Pointer(&x)); !errors.Is(err, velox.ErrDenied) {
		t.Errorf("insert of an untitled row: %v", err)
	}
	x.Title = "t"
	if err := p.Insert(ctx, unsafe.Pointer(&x)); err != nil {
		t.Errorf("insert of a titled row: %v", err)
	}

	// The update hook's Set follows the query's own.
	if _, err := db.Update[hooked]().Set(hookedTitle.Set("t")).Where(hookedTitle.EQ("u")).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(e.SQL, `UPDATE "hooked" SET "title" = $1, "tag" = $2 WHERE `) || e.Args[0] != "t" || e.Args[1] != "updated" {
		t.Errorf("update: %s %v", e.SQL, e.Args)
	}
	// Set on a delete is the hook's error, though the hook returned nil.
	calls := e.Calls
	if _, err := db.Delete[hooked]().Where(hookedTitle.EQ("u")).Exec(ctx); err == nil || e.Calls != calls {
		t.Errorf("Set on a delete: %v, %d statements sent", err, e.Calls-calls)
	}
}
