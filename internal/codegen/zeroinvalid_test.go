package codegen

import (
	"regexp"
	"strings"
	"testing"
)

// A non-zero literal Default that a check keeps from ever being 0 is written
// with ZeroInvalid, which the engine reads in place of refusing it. A
// DefaultFunc is not checked by the engine, and a zero Default is allowed, so
// neither has it.
func TestRenderZeroInvalid(t *testing.T) {
	files, err := Generate(mustBuild(t, zeroInvalidDefaults{}), Config{})
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, f := range files {
		all.Write(f.Data)
	}
	got := all.String()
	if n := strings.Count(got, "ZeroInvalid: true"); n != 5 {
		t.Errorf("%d ZeroInvalid, want 5 (the literal Defaults, not the DefaultFunc)", n)
	}
	for _, col := range []string{"seats", "port", "rank", "debt", "below"} {
		if !regexp.MustCompile(`Name: "` + col + `", Default: [^,]*, ZeroInvalid: true`).MatchString(squeeze(got)) {
			t.Errorf("column %s has no ZeroInvalid in\n%s", col, got)
		}
	}

	files, err = Generate(mustBuild(t, zeroDefaults{}), Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(string(f.Data), "ZeroInvalid") {
			t.Errorf("%s has ZeroInvalid for a zero Default", f.Name)
		}
	}
}
