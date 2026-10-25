package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestMain refuses to run the tests, when VELOX_ITEST_REQUIRE=1, as CI and
// scripts/ci-local.sh set it, without every database: a missing variable would
// skip the migrate tests on PostgreSQL and MySQL without a failure.
func TestMain(m *testing.M) {
	if os.Getenv("VELOX_ITEST_REQUIRE") == "1" {
		var missing []string
		for _, v := range []string{"VELOX_TEST_POSTGRES", "VELOX_TEST_MYSQL_ADMIN"} {
			if os.Getenv(v) == "" {
				missing = append(missing, v)
			}
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "velox-gen: VELOX_ITEST_REQUIRE=1 but %s unset: the PostgreSQL and MySQL tests would be skipped\n", strings.Join(missing, ", "))
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}
