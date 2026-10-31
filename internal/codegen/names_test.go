package codegen

import "testing"

func TestIdent(t *testing.T) {
	for in, want := range map[string]bool{"Done": true, "In-progress": false, "": false, "func": false, "x1": true} {
		if got := ident(in); got != want {
			t.Errorf("ident(%q) = %v, want %v", in, got, want)
		}
	}
}
