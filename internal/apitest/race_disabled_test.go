//go:build !race

package apitest

// raceEnabled says the tests run under the race detector (see
// race_enabled_test.go).
const raceEnabled = false
