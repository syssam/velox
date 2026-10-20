//go:build race

package velox_test

// raceEnabled says the tests run under the race detector, whose sync.Pool
// drops items at random: an allocation count that depends on a pool, such as
// encoding/json's, is then not exact.
const raceEnabled = true
