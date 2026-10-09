package main

import "testing"

// The order of several changes at one place: what closes the text before
// it, then what opens around the text after it with the last-made outermost,
// then a replacement that starts there.
func TestApplyOrdersChangesAtOnePlace(t *testing.T) {
	src := []byte("f(x)")
	edits := []edit{
		{start: 0, end: 0, text: "inner(", seq: 1, opens: true},
		{start: 0, end: 0, text: "outer(", seq: 2, opens: true},
		{start: 4, end: 4, text: ")", seq: 3},
		{start: 4, end: 4, text: ")", seq: 4},
		{start: 2, end: 3, text: "y", seq: 5},
		{start: 2, end: 2, text: "wrap(", seq: 6, opens: true},
		{start: 3, end: 3, text: ")", seq: 7},
	}
	if got := string(apply(src, 0, edits)); got != "outer(inner(f(wrap(y))))" {
		t.Errorf("got %s", got)
	}
}

// A change that overlaps an earlier one is dropped rather than merged into
// text nobody wrote.
func TestApplyDropsAnOverlap(t *testing.T) {
	src := []byte("abcdef")
	edits := []edit{
		{start: 1, end: 4, text: "X", seq: 1},
		{start: 2, end: 5, text: "Y", seq: 2},
	}
	if got := string(apply(src, 0, edits)); got != "aXef" {
		t.Errorf("got %s", got)
	}
}

func TestMemberNames(t *testing.T) {
	e := &entityInfo{name: "Post", fields: map[string]bool{"Title": true, "OwnerID": true}, edges: map[string]bool{"Owner": true, "Tags": true},
		singular: map[string]string{"Tag": "Tags"}}
	for name, want := range map[string]string{
		"SetTitle":           "Set|Title||field",
		"SetNillableOwnerID": "SetNillable|OwnerID||field", // the field wins over the edge and its ID
		"AddTagIDs":          "Add|Tags|IDs|edge",
		"ClearOwner":         "Clear|Owner||edge",
		"SetOwner":           "Set|Owner||edge",
		"Where":              "|||",
	} {
		pre, mid, suf, field, edge := split(e, name, setterPrefixes, setterSuffixes)
		kind := ""
		switch {
		case field:
			kind = "field"
		case edge:
			kind = "edge"
		}
		if got := pre + "|" + mid + "|" + suf + "|" + kind; got != want {
			t.Errorf("%s: got %s, want %s", name, got, want)
		}
	}
}
