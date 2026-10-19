package gen

import (
	"go/token"
	"strings"
	"unicode"
	"unicode/utf8"
)

// acronyms are the words pascal writes in upper case: ent's list.
var acronyms = map[string]bool{
	"ACL": true, "API": true, "ASCII": true, "AWS": true, "CPU": true, "CSS": true, "DNS": true, "EOF": true,
	"GB": true, "GUID": true, "HCL": true, "HTML": true, "HTTP": true, "HTTPS": true, "ID": true, "IP": true,
	"JSON": true, "KB": true, "LHS": true, "MAC": true, "MB": true, "QPS": true, "RAM": true, "RHS": true,
	"RPC": true, "SLA": true, "SMTP": true, "SQL": true, "SSH": true, "SSO": true, "TCP": true, "TLS": true,
	"TTL": true, "UDP": true, "UI": true, "UID": true, "URI": true, "URL": true, "UTF8": true, "UUID": true,
	"VM": true, "XML": true, "XMPP": true, "XSRF": true, "XSS": true,
}

// pascal is the Go name of a snake_case name: org_id → OrgID, api_key →
// APIKey. It splits on "_"; a word in the acronym list is upper-cased, and any
// other has its first letter raised. The result need not be an identifier:
// pascal("in-progress") is "In-progress".
func pascal(s string) string {
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

// snake is the snake_case of an identifier: OrgID → org_id, HTTPServer →
// http_server, S3Bucket → s3bucket, OAuth2Token → oauth2token. It is ent's
// snake (entc/gen/func.go), ported exactly, so that labels, default tables and
// foreign-key columns match ent's: an underscore goes before an upper-case
// letter that is neither first nor last when the previous letter is lower
// case, or when the next is lower case and the previous is a letter that did
// not itself start a word. A digit never starts a word.
func snake(s string) string {
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

// plural is the plural of an English noun: user → users, category →
// categories, box → boxes. It is cmd/velox-gen's rule.
func plural(s string) string {
	switch {
	case len(s) > 1 && s[len(s)-1] == 'y' && !strings.ContainsRune("aeiouAEIOU", rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"), strings.HasSuffix(s, "z"),
		strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	}
	return s + "s"
}

// ident reports whether s can name a Go declaration.
func ident(s string) bool { return token.IsIdentifier(s) && !token.IsKeyword(s) }
