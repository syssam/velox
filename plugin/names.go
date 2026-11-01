package plugin

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Singular is the singular of an English noun: categories → category,
// boxes → box, friends → friend. Its rules are ent's, copied from
// go-openapi/inflect v0.19.0: Singular("children") is child, Singular("men")
// is man. It is case-sensitive, as [Plural] is. velox-gen names join columns
// and join entities with it, and a plugin can name things after edges the
// same way. Its outputs never change: a change would rename columns in
// existing databases.
func Singular(s string) string {
	rs := defaultRules()
	return rs.inflect(rs.singulars, s, s)
}

// acronyms are the words GoName writes in upper case: ent's list.
var acronyms = map[string]bool{
	"ACL": true, "API": true, "ASCII": true, "AWS": true, "CPU": true, "CSS": true, "DNS": true, "EOF": true,
	"GB": true, "GUID": true, "HCL": true, "HTML": true, "HTTP": true, "HTTPS": true, "ID": true, "IP": true,
	"JSON": true, "KB": true, "LHS": true, "MAC": true, "MB": true, "QPS": true, "RAM": true, "RHS": true,
	"RPC": true, "SLA": true, "SMTP": true, "SQL": true, "SSH": true, "SSO": true, "TCP": true, "TLS": true,
	"TTL": true, "UDP": true, "UI": true, "UID": true, "URI": true, "URL": true, "UTF8": true, "UUID": true,
	"VM": true, "XML": true, "XMPP": true, "XSRF": true, "XSS": true,
}

// GoName is the Go name of a snake_case name: org_id → OrgID, api_key →
// APIKey. It splits on "_"; a word in the acronym list is upper-cased, and any
// other has its first letter raised. The result need not be an identifier:
// GoName("in-progress") is "In-progress". Its output must never change: it
// names Go identifiers in users' code.
func GoName(s string) string {
	words := strings.Split(s, "_")
	for i, w := range words {
		if u := strings.ToUpper(w); acronyms[u] {
			words[i] = u
			continue
		}
		if r, n := utf8.DecodeRuneInString(w); n > 0 {
			words[i] = string(unicode.ToUpper(r)) + w[n:]
		}
	}
	return strings.Join(words, "")
}

// Snake is the snake_case of an identifier: OrgID → org_id, HTTPServer →
// http_server, S3Bucket → s3bucket, OAuth2Token → oauth2token. It is ent's
// snake (entc/gen/func.go), ported exactly, so that labels, default tables and
// foreign-key columns match ent's. Like ent's, it works on bytes, so it is for
// ASCII identifiers: a letter outside ASCII comes out mangled (CaféBar gives
// caf_ã©bar). An underscore goes before an upper-case letter that is neither
// first nor last when the previous letter is lower case, or when the next is
// lower case and the previous is a letter that did not itself start a word. A
// digit never starts a word. Its output must never change: it names tables,
// columns and labels that live in databases.
func Snake(s string) string {
	var (
		j int
		b strings.Builder
	)
	for i := range len(s) {
		r := rune(s[i])
		if i > 0 && i < len(s)-1 && unicode.IsUpper(r) {
			if unicode.IsLower(rune(s[i-1])) ||
				j != i-1 && unicode.IsLower(rune(s[i+1])) && unicode.IsLetter(rune(s[i-1])) {
				j = i
				b.WriteString("_")
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// Plural is the plural of an English noun: user → users, category →
// categories, box → boxes. Its rules are ent's, copied from go-openapi/inflect
// v0.19.0. It is case-sensitive, so Plural("Person") is Persons while
// Plural("person") is people. velox-gen names tables with it, as ent does:
// the snake_case of the plural of the Go name. Its outputs never change: a
// change would rename tables in existing databases.
func Plural(s string) string {
	rs := defaultRules()
	return rs.inflect(rs.plurals, s, s+"s")
}

// GoPlural is the plural velox-gen names Go identifiers with, ent's plural():
// [Plural] of name, with "Slice" appended when Plural leaves name unchanged.
// It is Users for User, StatusSlice for Status. Its outputs never change: they
// name Go identifiers in users' code.
func GoPlural(name string) string {
	p := Plural(name)
	if p == name {
		p += "Slice"
	}
	return p
}
