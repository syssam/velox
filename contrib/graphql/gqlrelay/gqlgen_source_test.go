package gqlrelay_test

// The tests here drive collection through gqlgen's request context, which
// gqlgenrelay registers as the default source; linking it into the test
// binary is what supplies it.
import _ "github.com/syssam/velox/contrib/graphql/gqlgenrelay"
