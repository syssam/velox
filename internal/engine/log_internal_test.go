package engine

import "testing"

// TestSameArg: bytes compare by content, and a value Go cannot compare is
// not the same, so its statement's arguments are all hidden.
func TestSameArg(t *testing.T) {
	for _, c := range []struct {
		a, b any
		want bool
	}{
		{[]byte("x"), []byte("x"), true},
		{[]byte("x"), "x", false},
		{int64(1), int64(1), true},
		{int64(1), 1, false},
		{[]int{1}, []int{1}, false},
		{map[string]int{}, map[string]int{}, false},
	} {
		if got := sameArg(c.a, c.b); got != c.want {
			t.Errorf("sameArg(%v, %v) = %v", c.a, c.b, got)
		}
	}
}

// TestShownArgsUnknown: raw SQL and a statement whose arguments differ from
// a new rendering are not attributed.
func TestShownArgsUnknown(t *testing.T) {
	if s := ShownArgs(Postgres, &Statement{Op: OpRawQuery, Args: []any{1}}); s != nil {
		t.Errorf("raw: %v", s)
	}
	in := &EntityInfo{Name: "T", Table: "t", Columns: []ColumnInfo{{Name: "id"}, {Name: "pw", Sensitive: true}}, PrimaryKey: []int{0}}
	st := &Statement{Op: OpSelect, Entity: in, Where: []Predicate{NewPredicate(Node{Op: OpEQ, Col: 1, V: "s"})}, Limit: 2}
	_, args, err := RenderStmt(Postgres, st)
	if err != nil {
		t.Fatal(err)
	}
	st.Args = args
	if s := ShownArgs(Postgres, st); len(s) != 2 || s[0] != ArgSensitive || s[1] != ArgShown {
		t.Errorf("rendered: %v", s)
	}
	if got := MaskedArgs(Postgres, st); got[0] != SensitiveArg || got[1] != 2 {
		t.Errorf("masked: %v", got)
	}
	st.Args = []any{"s"}
	if s := ShownArgs(Postgres, st); s != nil {
		t.Errorf("fewer args: %v", s)
	}
}
