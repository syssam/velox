// Package dbenv reads the tests' database variables, VELOX_TEST_POSTGRES,
// VELOX_TEST_MYSQL and VELOX_TEST_MYSQL_ADMIN. When VELOX_ITEST_REQUIRE is
// 1, as CI and scripts/ci-local.sh set it, a variable that is unset fails
// the test, rather than skipping it or leaving its database out.
package dbenv

import "os"

// Required is the variable that makes a missing database a failure.
const Required = "VELOX_ITEST_REQUIRE"

// T is the part of testing.TB that dbenv uses.
type T interface {
	Helper()
	Fatalf(format string, args ...any)
	Skipf(format string, args ...any)
}

// Need returns the variable name's value. When it is unset, it fails t
// under VELOX_ITEST_REQUIRE=1, and skips t otherwise.
func Need(t T, name string) string {
	t.Helper()
	if v := Optional(t, name); v != "" {
		return v
	}
	t.Skipf("%s is not set", name)
	return ""
}

// Optional returns the variable name's value, or "" when it is unset, for a
// test that runs on each database it is given. Unset, it fails t under
// VELOX_ITEST_REQUIRE=1.
func Optional(t T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" && os.Getenv(Required) == "1" {
		t.Fatalf("%s=1 and %s is unset", Required, name)
	}
	return v
}
