package gqlcore

import (
	"context"
	"testing"
)

// ctxKey marks a context as one of a fake engine.
type ctxKey string

// engineReader is a Reader of a fake engine: it knows the fields of the
// contexts that hold its name under ctxKey("engine").
type engineReader struct{ name string }

func (r engineReader) In(ctx context.Context) bool { return ctx.Value(ctxKey("engine")) == r.name }

func (r engineReader) Field(ctx context.Context) (Field, bool) {
	if !r.In(ctx) {
		return Field{}, false
	}
	return Field{Name: r.name}, true
}

func (engineReader) Wait(context.Context) func() { return func() {} }

// withRegistered runs with no registered readers, and restores those that
// were.
func withRegistered(t *testing.T) {
	t.Helper()
	old := registered.Load()
	registered.Store(nil)
	t.Cleanup(func() { registered.Store(old) })
}

// TestRegister: the first registered reader that knows a context finds its
// field, a context of no engine has no field and no source, and Register
// refuses a nil reader.
func TestRegister(t *testing.T) {
	withRegistered(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("Register of a nil reader did not panic")
			}
		}()
		Register(nil)
	}()
	bg := context.Background()
	if SourceOf(bg) != nil {
		t.Error("a source with no reader registered")
	}
	a, b := engineReader{"a"}, engineReader{"b"}
	Register(a)
	Register(b)
	inB := context.WithValue(bg, ctxKey("engine"), "b")
	if f, ok := FieldOf(inB); !ok || f.Name != "b" {
		t.Errorf("FieldOf in engine b = %+v, %v; want b's", f, ok)
	}
	if SourceOf(inB) != b {
		t.Error("SourceOf in engine b is not b")
	}
	if _, ok := FieldOf(bg); ok || SourceOf(bg) != nil {
		t.Error("a field or a source in a context of no engine")
	}
}

// TestWait: the Wait of a field is its source's, and does nothing outside a
// field.
func TestWait(t *testing.T) {
	withRegistered(t)
	wait(context.Background())()
	Register(engineReader{"a"})
	wait(context.WithValue(context.Background(), ctxKey("engine"), "a"))()
}

// TestTableEdges: an empty Add does nothing, and a Selected whose compute
// stores the same key leaves the stored result.
func TestTableEdges(t *testing.T) {
	ctx := context.Background()
	Add(ctx, nil)

	tab := NewTable()
	got, ok := tab.Selected("f", "Post", func() ([]string, bool) {
		tab.Selected("f", "Post", func() ([]string, bool) { return []string{"stored"}, true })
		return []string{"late"}, false
	})
	if !ok || len(got) != 1 || got[0] != "stored" {
		t.Errorf("Selected = %v, %v; want the first stored result", got, ok)
	}
}
