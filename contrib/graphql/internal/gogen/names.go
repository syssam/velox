package gogen

import (
	"strings"

	"github.com/syssam/velox/plugin"
)

// recv is the receiver of e's methods: the first letter of e's name in lower
// case, as Go names a receiver, or its first two when the methods' bodies
// declare the letter (v, n, b).
func recv(e *plugin.Entity) string {
	r := strings.ToLower(e.Name[:1])
	if r == "v" || r == "n" || r == "b" {
		if len(e.Name) > 1 {
			return strings.ToLower(e.Name[:2])
		}
		return r + r
	}
	return r
}

// locals are the names the generated code declares inside its functions,
// and the packages and builtins it names: an imported package of the
// application's named one of them is imported under another name, so that
// none shadows the other.
var locals = map[string]bool{
	"ctx": true, "id": true, "ids": true, "typ": true, "err": true, "k": true, "db": true, "x": true, "input": true, "in": true,
	"rows": true, "by": true, "u": true, "key": true, "set": true, "i": true, "j": true, "out": true, "c": true, "n": true, "e": true,
	"s": true, "b": true, "w": true, "v": true, "o": true, "f": true, "q": true, "ok": true, "fields": true, "cols": true, "batch": true,
	"context": true, "json": true, "errors": true, "fmt": true, "io": true, "velox": true, "veloxgql": true, "gqlgraph": true,
	"any": true, "bool": true, "string": true, "int": true, "len": true, "make": true, "append": true,
	"nil": true, "error": true, "true": true, "false": true,
	// Every letter, for a receiver (recv).
	"a": true, "d": true, "g": true, "h": true, "l": true, "m": true, "p": true, "r": true, "t": true, "y": true, "z": true,
}

// localSuffixes end the locals the generated code names after an input field:
// <field>Key, the decoded key of an ID the field gives (<field>Keys for a list
// of IDs); <field>JSON, the
// decoded JSON value; and <field>Val, the field's value converted for the row.
var localSuffixes = []string{"Key", "Keys", "JSON", "Val"}

// camel is the Go name name with its leading capitals lower case, as Go
// writes an unexported name: WriterID is writerID, and URLPath is urlPath.
func camel(name string) string {
	n := 0
	for n < len(name) && name[n] >= 'A' && name[n] <= 'Z' {
		n++
	}
	switch {
	case n == 0:
		return name
	case n == len(name) || n == 1:
		return strings.ToLower(name[:n]) + name[n:]
	}
	return strings.ToLower(name[:n-1]) + name[n-1:]
}

// indexedLocal reports whether name is one of the locals the generated code
// may name after an input field, or a clear<Edge>, whether an update clears
// an edge.
func indexedLocal(name string) bool {
	if rest, ok := strings.CutPrefix(name, "clear"); ok && rest != "" {
		return true
	}
	for _, suffix := range localSuffixes {
		if len(name) > len(suffix) && strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// named is a schema element's name, with its GraphQL name when that differs:
// "name (displayName)".
func named(name, gql string) string {
	if gql == "" || gql == name {
		return name
	}
	return name + " (" + gql + ")"
}
