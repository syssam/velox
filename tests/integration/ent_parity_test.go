package integration_test

// Ent-parity behavioral tests. Each test pins a contract that was verified
// by comparing velox's generated output against the Ent reference implementation.
// Structural guards live in compiler/gen/sql/wiring_test.go; these tests verify
// the end-to-end runtime behavior of those structures.

import (
	_ "modernc.org/sqlite"
)
