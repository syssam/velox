package plugin

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Singular is velox-gen's singular of an English noun, the inverse of the
// plural it names tables and handles with: categories → category, boxes →
// box, friends → friend. A word that does not end in "s", such as children,
// is returned unchanged. velox-gen names join columns and join entities with
// it, and a plugin can name things after edges the same way. Because join
// columns are named with it, its output must never change: a change would
// rename columns in existing databases. Plugins' own names are frozen with it.
func Singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "ses"), strings.HasSuffix(s, "xes"), strings.HasSuffix(s, "zes"),
		strings.HasSuffix(s, "ches"), strings.HasSuffix(s, "shes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s") && len(s) > 1:
		return s[:len(s)-1]
	}
	return s
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
// GoName("in-progress") is "In-progress".
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
// foreign-key columns match ent's: an underscore goes before an upper-case
// letter that is neither first nor last when the previous letter is lower
// case, or when the next is lower case and the previous is a letter that did
// not itself start a word. A digit never starts a word.
func Snake(s string) string {
	var (
		j int
		b strings.Builder
	)
	for i := 0; i < len(s); i++ {
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
// categories, box → boxes. velox-gen names tables and handles with it.
func Plural(s string) string {
	switch {
	case len(s) > 1 && s[len(s)-1] == 'y' && !strings.ContainsRune("aeiouAEIOU", rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"), strings.HasSuffix(s, "z"),
		strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	}
	return s + "s"
}
