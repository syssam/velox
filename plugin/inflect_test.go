package plugin

import "testing"

// TestRuleCounts pins the copied tables to NewDefaultRuleset's calls: 83
// plural, 86 singular, 6 irregular and 10 uncountable.
func TestRuleCounts(t *testing.T) {
	got := map[string]int{}
	for _, r := range inflectRules {
		got[r.kind]++
	}
	for kind, want := range map[string]int{"plural": 83, "singular": 86, "irregular": 6, "uncountable": 10} {
		if got[kind] != want {
			t.Errorf("%d %s rules, want %d", got[kind], kind, want)
		}
	}
	if len(got) != 4 {
		t.Errorf("rule kinds %v, want plural, singular, irregular and uncountable", got)
	}
}
