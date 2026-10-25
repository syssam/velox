package bindmodel

import "testing"

func TestEnumMethods(t *testing.T) {
	if s := BindKindA.String(); s != "a" {
		t.Errorf("BindKindA.String() = %q", s)
	}
	if !BindKindA.IsValid() || !BindKindB.IsValid() {
		t.Error("a declared value is not valid")
	}
	for _, v := range []BindKind{"", "c"} {
		if v.IsValid() {
			t.Errorf("BindKind(%q) is valid", v)
		}
	}
}
