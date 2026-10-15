package velox

import (
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

// TestErasedLayouts pins what Load relies on when it binds Desc.Policy and
// Desc.Hooks as erasedPolicy and erasedHooks: a Policy[E] has policy's
// layout, a Hook[E] and a Mutator[E] have rawMutator's shape, and neither
// depends on E.
func TestErasedLayouts(t *testing.T) {
	type e struct{ A, B int64 }
	same := func(what string, a, b reflect.Type) {
		t.Helper()
		kinds := a.Kind() == b.Kind() || a.Kind() == reflect.Pointer && b.Kind() == reflect.UnsafePointer
		if a.Size() != b.Size() || a.Align() != b.Align() || !kinds {
			t.Errorf("%s: %v and %v differ in size, alignment or kind", what, a, b)
		}
	}
	// funcs compares two function types parameter by parameter.
	funcs := func(what string, a, b reflect.Type) {
		t.Helper()
		if a.Kind() != reflect.Func || b.Kind() != reflect.Func || a.NumIn() != b.NumIn() || a.NumOut() != b.NumOut() || a.IsVariadic() != b.IsVariadic() {
			t.Fatalf("%s: %v and %v are not alike", what, a, b)
		}
		for i := 0; i < a.NumIn(); i++ {
			same(what+" in", a.In(i), b.In(i))
		}
		for i := 0; i < a.NumOut(); i++ {
			same(what+" out", a.Out(i), b.Out(i))
		}
	}

	pt, rt := reflect.TypeFor[Policy[e]](), reflect.TypeFor[policy]()
	if pt.Size() != rt.Size() || pt.NumField() != rt.NumField() {
		t.Fatalf("Policy[E] is %v, policy %v", pt, rt)
	}
	for i := 0; i < pt.NumField(); i++ {
		if !strings.EqualFold(pt.Field(i).Name, rt.Field(i).Name) || pt.Field(i).Offset != rt.Field(i).Offset {
			t.Errorf("field %d: %s at %d, %s at %d", i, pt.Field(i).Name, pt.Field(i).Offset, rt.Field(i).Name, rt.Field(i).Offset)
		}
		funcs("policy."+pt.Field(i).Name, pt.Field(i).Type, rt.Field(i).Type)
	}
	same("Pred", reflect.TypeFor[Pred[e]](), reflect.TypeFor[Predicate]())
	same("Mutation", reflect.TypeFor[Mutation[e]](), reflect.TypeFor[mutation]())

	funcs("Desc.Policy", reflect.TypeFor[func(*e) Policy[e]](), reflect.TypeFor[erasedPolicy]())
	funcs("Desc.Hooks", reflect.TypeFor[func(*e) []Hook[e]](), reflect.TypeFor[erasedHooks]())
	if reflect.TypeFor[[]Hook[e]]().Size() != reflect.TypeFor[[]func(rawMutator) rawMutator]().Size() {
		t.Error("a []Hook[E] and a slice of erased hooks differ in size")
	}
	funcs("Hook", reflect.TypeFor[Hook[e]](), reflect.TypeFor[func(rawMutator) rawMutator]())
	m, r := reflect.TypeFor[Mutator[e]](), reflect.TypeFor[rawMutator]()
	if m.NumMethod() != 1 || r.NumMethod() != 1 || m.Method(0).Name != r.Method(0).Name {
		t.Fatalf("Mutator[E] and rawMutator differ in methods")
	}
	funcs("Mutate", m.Method(0).Type, r.Method(0).Type)
	if unsafe.Sizeof(Mutation[e]{}) != unsafe.Sizeof(mutation{}) {
		t.Error("Mutation[E] and mutation differ in size")
	}
}
