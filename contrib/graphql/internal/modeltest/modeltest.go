// Package modeltest holds the models the GraphQL packages' tests share: one
// JSON document whose annotations cover every annotation, one of edge
// schemas, and one of an application of the old velox.
package modeltest

import (
	_ "embed"
	"encoding/json"
	"testing"

	"github.com/syssam/velox/plugin"
)

//go:embed model.json
var modelJSON []byte

//go:embed edgeschema.json
var edgeSchemaJSON []byte

//go:embed salesorder.json
var salesOrderJSON []byte

//go:embed initialisms.json
var initialismsJSON []byte

// Model returns a fresh copy of model.json, so a test may change it, with
// graphql.RelayConnection on each entity with a QueryField.
func Model(t testing.TB) *plugin.Model {
	t.Helper()
	var m plugin.Model
	if err := json.Unmarshal(modelJSON, &m); err != nil {
		t.Fatal(err)
	}
	return relay(t, &m, true)
}

// EdgeSchema returns a fresh copy of edgeschema.json: velox-gen's model of
// internal/codegen's edgeschema golden case, users in groups through the
// edge schema Membership and following users through Follow, whose note Add
// cannot fill, with GraphQL annotations added and graphql.RelayConnection
// as Model has it.
func EdgeSchema(t testing.TB) *plugin.Model {
	t.Helper()
	var m plugin.Model
	if err := json.Unmarshal(edgeSchemaJSON, &m); err != nil {
		t.Fatal(err)
	}
	return relay(t, &m, true)
}

// SalesOrder returns a fresh copy of salesorder.json: velox-gen's model of
// cmd/velox-graphql's old-velox application, customers, sales orders, memos
// and workspaces, as an application of the old velox annotates them.
func SalesOrder(t testing.TB) *plugin.Model {
	t.Helper()
	var m plugin.Model
	if err := json.Unmarshal(salesOrderJSON, &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

// Initialisms returns a fresh copy of initialisms.json: devices with API
// keys and SLA definitions, whose Go names start with initialisms
// (IPAddress, APIName, HasAPIAccess, DNSRecordName, SLABreachAt, APIKeyID,
// SLADefinitionID), beside a JSON field that a GQL method binds and a
// Bytes field, as an application of the old velox annotates them.
func Initialisms(t testing.TB) *plugin.Model {
	t.Helper()
	var m plugin.Model
	if err := json.Unmarshal(initialismsJSON, &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

// GraphQLGo returns Model without the elements engine=graphql-go refuses,
// as no gqlc binds them yet: the fields and edges graphql.FieldName renames
// (Post.author, User.name, User.groups) keep their own names.
func GraphQLGo(t testing.TB) *plugin.Model {
	t.Helper()
	m := Model(t)
	for _, e := range m.Entities {
		for _, f := range e.Fields {
			unname(t, f.Annotations)
		}
		for _, ed := range e.Edges {
			unname(t, ed.Annotations)
		}
	}
	return m
}

// relay sets graphql.RelayConnection to on on each entity of m with a
// QueryField, so that its Query field and the edges to it are connections,
// ordered and filtered; NoRelay sets it off on every entity.
func relay(t testing.TB, m *plugin.Model, on bool) *plugin.Model {
	t.Helper()
	for _, e := range m.Entities {
		raw, ok := e.Annotations["graphql"]
		if !ok {
			continue
		}
		var a map[string]json.RawMessage
		if err := json.Unmarshal(raw, &a); err != nil {
			t.Fatal(err)
		}
		if _, ok := a["query_field"]; !ok {
			continue
		}
		delete(a, "relay_connection")
		if on {
			a["relay_connection"] = json.RawMessage("true")
		}
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		e.Annotations["graphql"] = b
	}
	return m
}

// NoRelay removes graphql.RelayConnection from every entity of m and
// returns m: the JSON model as written, a schema of the old velox's whose
// Query fields are lists under the defaults.
func NoRelay(t testing.TB, m *plugin.Model) *plugin.Model {
	t.Helper()
	return relay(t, m, false)
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
