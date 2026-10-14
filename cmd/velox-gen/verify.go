package main

import (
	"fmt"
	"io"

	"golang.org/x/tools/go/packages"
)

// runVerify is written in Task 5.
func runVerify(cfg config, ps []*packages.Package, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "velox-gen: verify is not implemented yet")
	return 1
}
