// Package shapes is the vx schema-shape matrix: one generated project that
// covers many schema shapes at once, with tests that execute each shape
// against SQLite. It is the vx counterpart of velox's tests/matrix.
//
//	schema/   the shapes: one small entity (or a few) each
//	types/    custom Go types bound with GoType(), deliberately outside schema/
//	db/       generated output; regenerate after any change to schema/ or to vx/gen
//
//	*_test.go                    one behavior test per shape
//	compile_check_test.go        mixing entities must not compile
//	known_gaps_test.go           shapes the generator refuses, with the error it gives
//	corrected_test.go            former gaps, now asserting the corrected behavior
//
// Regenerate, from vx/:
//
//	go generate ./tests/shapes/
//
// Run, from vx/ (-short skips the cases that run the generator and a build):
//
//	go test -race -count=1 -timeout=30m ./tests/shapes/...
//
// The tests create their tables from the schema graph with velox's migrator
// (db_test.go), which lets them vary the DDL; TestGeneratedMigrate checks that
// the generated migrate package produces the same tables.
//
// A generator or runtime bug found here is pinned, not worked around: a shape
// that does not generate or compile becomes a case of gapCases with the
// outcome it has today, a shape that misbehaves at run time a TestKnownGap_*
// test that asserts the wrong behavior. Either pin fails the day the bug is
// fixed, and its message says where the assertion of the correct behavior
// belongs. No runtime gap is pinned at present.
//
// Never run `go mod tidy` in this module; use `go get <module>@<version>`.
package shapes

//go:generate go run ../../cmd/vxgen -schema ./schema -target ./db -package github.com/syssam/velox/vx/tests/shapes/db
