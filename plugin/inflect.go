// The rule tables below are copied from github.com/go-openapi/inflect
// v0.19.0, the version ent pins: one entry per Add* call of its
// NewDefaultRuleset, in that order. That package's licence follows.
//
// Copyright (c) 2011 Chris Farmiloe
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to
// deal in the Software without restriction, including without limitation the
// rights to use, copy, modify, merge, publish, distribute, sublicense, and/or
// sell copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
// FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS
// IN THE SOFTWARE.

package plugin

import (
	"slices"
	"strings"
	"sync"
)

// inflectRules are NewDefaultRuleset's calls: kind is "plural" (AddPlural, or
// AddPluralExact when exact), "singular" (AddSingular, AddSingularExact),
// "irregular" (AddIrregular(a, b)) or "uncountable" (AddUncountable(a)). For
// a plural or singular rule, a is the suffix and b its replacement.
var inflectRules = [...]struct {
	kind, a, b string
	exact      bool
}{
	{"plural", "s", "s", false},
	{"plural", "testis", "testes", false},
	{"plural", "axis", "axes", false},
	{"plural", "octopus", "octopi", false},
	{"plural", "virus", "viri", false},
	{"plural", "octopi", "octopi", false},
	{"plural", "viri", "viri", false},
	{"plural", "alias", "aliases", false},
	{"plural", "status", "statuses", false},
	{"plural", "bus", "buses", false},
	{"plural", "buffalo", "buffaloes", false},
	{"plural", "tomato", "tomatoes", false},
	{"plural", "tum", "ta", false},
	{"plural", "ium", "ia", false},
	{"plural", "ta", "ta", false},
	{"plural", "ia", "ia", false},
	{"plural", "sis", "ses", false},
	{"plural", "lf", "lves", false},
	{"plural", "rf", "rves", false},
	{"plural", "afe", "aves", false},
	{"plural", "bfe", "bves", false},
	{"plural", "cfe", "cves", false},
	{"plural", "dfe", "dves", false},
	{"plural", "efe", "eves", false},
	{"plural", "gfe", "gves", false},
	{"plural", "hfe", "hves", false},
	{"plural", "ife", "ives", false},
	{"plural", "jfe", "jves", false},
	{"plural", "kfe", "kves", false},
	{"plural", "lfe", "lves", false},
	{"plural", "mfe", "mves", false},
	{"plural", "nfe", "nves", false},
	{"plural", "ofe", "oves", false},
	{"plural", "pfe", "pves", false},
	{"plural", "qfe", "qves", false},
	{"plural", "rfe", "rves", false},
	{"plural", "sfe", "sves", false},
	{"plural", "tfe", "tves", false},
	{"plural", "ufe", "uves", false},
	{"plural", "vfe", "vves", false},
	{"plural", "wfe", "wves", false},
	{"plural", "xfe", "xves", false},
	{"plural", "yfe", "yves", false},
	{"plural", "zfe", "zves", false},
	{"plural", "hive", "hives", false},
	{"plural", "quy", "quies", false},
	{"plural", "by", "bies", false},
	{"plural", "cy", "cies", false},
	{"plural", "dy", "dies", false},
	{"plural", "fy", "fies", false},
	{"plural", "gy", "gies", false},
	{"plural", "hy", "hies", false},
	{"plural", "jy", "jies", false},
	{"plural", "ky", "kies", false},
	{"plural", "ly", "lies", false},
	{"plural", "my", "mies", false},
	{"plural", "ny", "nies", false},
	{"plural", "py", "pies", false},
	{"plural", "qy", "qies", false},
	{"plural", "ry", "ries", false},
	{"plural", "sy", "sies", false},
	{"plural", "ty", "ties", false},
	{"plural", "vy", "vies", false},
	{"plural", "wy", "wies", false},
	{"plural", "xy", "xies", false},
	{"plural", "zy", "zies", false},
	{"plural", "x", "xes", false},
	{"plural", "ch", "ches", false},
	{"plural", "ss", "sses", false},
	{"plural", "sh", "shes", false},
	{"plural", "matrix", "matrices", false},
	{"plural", "vertix", "vertices", false},
	{"plural", "indix", "indices", false},
	{"plural", "matrex", "matrices", false},
	{"plural", "vertex", "vertices", false},
	{"plural", "index", "indices", false},
	{"plural", "mouse", "mice", false},
	{"plural", "louse", "lice", false},
	{"plural", "mice", "mice", false},
	{"plural", "lice", "lice", false},
	{"plural", "ox", "oxen", true},
	{"plural", "oxen", "oxen", true},
	{"plural", "quiz", "quizzes", true},
	{"singular", "s", "", false},
	{"singular", "news", "news", false},
	{"singular", "ta", "tum", false},
	{"singular", "ia", "ium", false},
	{"singular", "analyses", "analysis", false},
	{"singular", "bases", "basis", false},
	{"singular", "diagnoses", "diagnosis", false},
	{"singular", "parentheses", "parenthesis", false},
	{"singular", "prognoses", "prognosis", false},
	{"singular", "synopses", "synopsis", false},
	{"singular", "theses", "thesis", false},
	{"singular", "analyses", "analysis", false},
	{"singular", "aves", "afe", false},
	{"singular", "bves", "bfe", false},
	{"singular", "cves", "cfe", false},
	{"singular", "dves", "dfe", false},
	{"singular", "eves", "efe", false},
	{"singular", "gves", "gfe", false},
	{"singular", "hves", "hfe", false},
	{"singular", "ives", "ife", false},
	{"singular", "jves", "jfe", false},
	{"singular", "kves", "kfe", false},
	{"singular", "lves", "lfe", false},
	{"singular", "mves", "mfe", false},
	{"singular", "nves", "nfe", false},
	{"singular", "oves", "ofe", false},
	{"singular", "pves", "pfe", false},
	{"singular", "qves", "qfe", false},
	{"singular", "rves", "rfe", false},
	{"singular", "sves", "sfe", false},
	{"singular", "tves", "tfe", false},
	{"singular", "uves", "ufe", false},
	{"singular", "vves", "vfe", false},
	{"singular", "wves", "wfe", false},
	{"singular", "xves", "xfe", false},
	{"singular", "yves", "yfe", false},
	{"singular", "zves", "zfe", false},
	{"singular", "hives", "hive", false},
	{"singular", "tives", "tive", false},
	{"singular", "lves", "lf", false},
	{"singular", "rves", "rf", false},
	{"singular", "quies", "quy", false},
	{"singular", "bies", "by", false},
	{"singular", "cies", "cy", false},
	{"singular", "dies", "dy", false},
	{"singular", "fies", "fy", false},
	{"singular", "gies", "gy", false},
	{"singular", "hies", "hy", false},
	{"singular", "jies", "jy", false},
	{"singular", "kies", "ky", false},
	{"singular", "lies", "ly", false},
	{"singular", "mies", "my", false},
	{"singular", "nies", "ny", false},
	{"singular", "pies", "py", false},
	{"singular", "qies", "qy", false},
	{"singular", "ries", "ry", false},
	{"singular", "sies", "sy", false},
	{"singular", "ties", "ty", false},
	{"singular", "vies", "vy", false},
	{"singular", "wies", "wy", false},
	{"singular", "xies", "xy", false},
	{"singular", "zies", "zy", false},
	{"singular", "series", "series", false},
	{"singular", "movies", "movie", false},
	{"singular", "xes", "x", false},
	{"singular", "ches", "ch", false},
	{"singular", "sses", "ss", false},
	{"singular", "shes", "sh", false},
	{"singular", "mice", "mouse", false},
	{"singular", "lice", "louse", false},
	{"singular", "buses", "bus", false},
	{"singular", "oes", "o", false},
	{"singular", "shoes", "shoe", false},
	{"singular", "crises", "crisis", false},
	{"singular", "axes", "axis", false},
	{"singular", "testes", "testis", false},
	{"singular", "octopi", "octopus", false},
	{"singular", "viri", "virus", false},
	{"singular", "statuses", "status", false},
	{"singular", "aliases", "alias", false},
	{"singular", "oxen", "ox", true},
	{"singular", "vertices", "vertex", false},
	{"singular", "indices", "index", false},
	{"singular", "matrices", "matrix", false},
	{"singular", "quizzes", "quiz", true},
	{"singular", "databases", "database", false},
	{"irregular", "person", "people", false},
	{"irregular", "man", "men", false},
	{"irregular", "child", "children", false},
	{"irregular", "sex", "sexes", false},
	{"irregular", "move", "moves", false},
	{"irregular", "zombie", "zombies", false},
	{"uncountable", "equipment", "", false},
	{"uncountable", "information", "", false},
	{"uncountable", "rice", "", false},
	{"uncountable", "money", "", false},
	{"uncountable", "species", "", false},
	{"uncountable", "series", "", false},
	{"uncountable", "fish", "", false},
	{"uncountable", "sheep", "", false},
	{"uncountable", "jeans", "", false},
	{"uncountable", "police", "", false},
}

// inflectRule is one lookup rule: a word that ends in suffix, or with exact
// is suffix, has it replaced by replacement.
type inflectRule struct {
	suffix, replacement string
	exact               bool
}

// ruleset is inflect's Ruleset, cut to what Pluralize and Singularize use.
type ruleset struct {
	plurals, singulars []inflectRule
	uncountables       map[string]bool
}

// defaultRules is inflect's default ruleset, built on first use.
var defaultRules = sync.OnceValue(newRuleset)

// newRuleset replays inflectRules with inflect's semantics: each add puts its
// rule first, and a plural or singular add deletes its suffix from the
// uncountables.
func newRuleset() *ruleset {
	rs := &ruleset{uncountables: map[string]bool{}}
	plural := func(s, r string, exact bool) {
		delete(rs.uncountables, s)
		rs.plurals = slices.Insert(rs.plurals, 0, inflectRule{s, r, exact})
	}
	singular := func(s, r string, exact bool) {
		delete(rs.uncountables, s)
		rs.singulars = slices.Insert(rs.singulars, 0, inflectRule{s, r, exact})
	}
	for _, r := range inflectRules {
		switch r.kind {
		case "plural":
			plural(r.a, r.b, r.exact)
		case "singular":
			singular(r.a, r.b, r.exact)
		case "irregular":
			delete(rs.uncountables, r.a)
			delete(rs.uncountables, r.b)
			plural(r.a, r.b, false)
			plural(r.b, r.b, false)
			singular(r.b, r.a, false)
		case "uncountable":
			rs.uncountables[strings.ToLower(r.a)] = true
		}
	}
	return rs
}

// inflect is inflect's Pluralize and Singularize over rules: "" and an
// uncountable word (by its last space-separated word, lower-cased) are
// returned as they are; otherwise the first rule that matches,
// case-sensitively, replaces its suffix. With no match, fallback is the
// result.
func (rs *ruleset) inflect(rules []inflectRule, word, fallback string) string {
	if word == "" {
		return word
	}
	words := strings.Split(word, " ")
	if rs.uncountables[strings.ToLower(words[len(words)-1])] {
		return word
	}
	for _, r := range rules {
		if r.exact {
			if word == r.suffix {
				return r.replacement
			}
		} else if strings.HasSuffix(word, r.suffix) {
			return word[:len(word)-len(r.suffix)] + r.replacement
		}
	}
	return fallback
}
