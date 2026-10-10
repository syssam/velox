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
	e := &entityInfo{name: "Post", fields: map[string]bool{"Title": true, "OwnerID": true}, edges: map[string]bool{"Owner": true, "Tags": true, "Parent": true, "Parents": true},
		singular: map[string]string{"Tag": "Tags", "Parent": "Parents"}}
	for name, want := range map[string]string{
		"SetTitle":           "Set|Title||field",
		"SetNillableOwnerID": "SetNillable|OwnerID||field", // the field wins over the edge and its ID
		"AddTagIDs":          "Add|Tags|IDs|edge",
		"ClearOwner":         "Clear|Owner||edge",
		"SetOwner":           "Set|Owner||edge",
		"AddParentIDs":       "Add|Parents|IDs|edge", // not the to-one edge Parent
		"SetParentID":        "Set|Parent|ID|edge",
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

// An insertion at an end of a replaced range goes with the range or with
// its neighbor by what it does.
func TestReplaceKeepsWhatBelongsToTheNeighbors(t *testing.T) {
	// c.M(g(x)): the rule for g(x) opened a wrapper at its start; the rule
	// for the outer call then replaces ".M(" up to that same place.
	f := &fileEdits{src: []byte("c.M(g(x))")}
	f.seq++
	f.edits = append(f.edits, edit{start: 4, end: 4, text: "w(", seq: f.seq, opens: true}) // around g(x)
	f.add(8, 8, ")")                                                                       // closes it
	f.add(1, 1, ".h")                                                                      // continues c
	f.drop(1, 4)
	f.add(1, 4, "(")
	if got := string(apply(f.src, 0, f.edits)); got != "c.h(w(g(x)))" {
		t.Errorf("got %s", got)
	}
}

func TestEdgeOfSingular(t *testing.T) {
	edges := map[string]bool{"Likes": true, "LikedPosts": true, "Tags": true, "Categories": true, "CategoryTags": true, "Children": true, "Owner": true,
		"Companies": true, "CompanyMembers": true, "Replies": true, "ReplyLikes": true, "Men": true, "Managers": true,
		"Parent": true, "Parents": true, "Staff": true}
	for singular, want := range map[string]string{
		"Like": "Likes", "LikedPost": "LikedPosts", "Tag": "Tags", "Category": "Categories", "CategoryTag": "CategoryTags", "Child": "Children", "Zebra": "",
		"Company": "Companies", "CompanyMember": "CompanyMembers", "Reply": "Replies", "ReplyLike": "ReplyLikes", "Man": "Men", "Manager": "Managers",
		"Parent": "Parents", "Staff": "Staff",
	} {
		if got := edgeOfSingular(singular, edges); got != want {
			t.Errorf("%s: got %q, want %q", singular, got, want)
		}
	}
}
