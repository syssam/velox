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
