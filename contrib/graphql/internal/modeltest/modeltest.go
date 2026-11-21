// Package modeltest holds the model the GraphQL packages' tests share: one
// JSON document whose annotations cover every annotation.
package modeltest

import (
	_ "embed"
	"encoding/json"
	"testing"

	"github.com/syssam/velox/plugin"
)

//go:embed model.json
var modelJSON []byte

// Model returns a fresh copy of model.json, so a test may change it.
func Model(t testing.TB) *plugin.Model {
	t.Helper()
	var m plugin.Model
	if err := json.Unmarshal(modelJSON, &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

// GraphQLGo returns Model without the elements engine=graphql-go refuses,
// as no gqlc binds them yet: the fields and edges graphql.FieldName renames
// (Post.author, User.name, User.groups) keep their own names, and User's
// settings, a JSON field read through a getter, is left out of the type.
func GraphQLGo(t testing.TB) *plugin.Model {
	t.Helper()
	m := Model(t)
	for _, e := range m.Entities {
		for _, f := range e.Fields {
			if e.Name == "User" && f.Name == "settings" {
				f.Annotations = map[string]json.RawMessage{"graphql": json.RawMessage(`{"skip":1}`)}
			}
			unname(t, f.Annotations)
		}
		for _, ed := range e.Edges {
			unname(t, ed.Annotations)
		}
	}
	return m
}

// unname removes field_name from the graphql annotation among anns.
func unname(t testing.TB, anns map[string]json.RawMessage) {
	raw, ok := anns["graphql"]
	if !ok {
		return
	}
	var a map[string]json.RawMessage
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	if _, ok := a["field_name"]; !ok {
		return
	}
	delete(a, "field_name")
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	anns["graphql"] = b
}
