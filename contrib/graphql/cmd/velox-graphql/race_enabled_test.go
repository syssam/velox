//go:build race

package main

// raceEnabled says the tests run under the race detector, so the generated
// module's own tests do too, where that is worth its cost.
const raceEnabled = true
