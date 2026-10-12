// Package shapes is the schema-shape matrix of package orm: one generated project that
// covers many schema shapes at once, with tests that execute each shape
// against SQLite. It is the counterpart of tests/matrix, which is the old generator's.
//
//	schema/   the shapes: one small entity (or a few) each
//	types/    custom Go types bound with GoType(), deliberately outside schema/
//	velox/    generated output; regenerate after any change to schema/ or to orm/ormgen
//
//	*_test.go                    one behavior test per shape
//	compile_check_test.go        mixing entities must not compile
//	known_gaps_test.go           shapes the generator refuses, with the error it gives
//	corrected_test.go            former gaps, now asserting the corrected behavior
//
// Regenerate, from the repository root:
//
//	go generate ./tests/orm/shapes/
//
// Run, from the repository root (-short skips the cases that run the generator and a build):
//
//	go test -race -count=1 -timeout=30m ./tests/orm/shapes/...
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
package shapes

//go:generate go run ../../../cmd/velox-gen -schema ./schema -target ./velox -package github.com/syssam/velox/tests/orm/shapes/velox
