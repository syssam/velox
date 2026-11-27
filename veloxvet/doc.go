// Package veloxvet holds static checks of code that uses velox, as
// go/analysis analyzers. Run them with the veloxvet command, as go vet's
// tool:
//
//	go install github.com/syssam/velox/veloxvet/cmd/veloxvet@latest
//	go vet -vettool=$(which veloxvet) ./...
//
// or import Analyzers into another driver, such as a golangci-lint plugin.
//
// LockTx is off unless its flag is set. To run it beside the others:
//
//	go vet -vettool=$(which veloxvet) -locktx.enable ./...
//
// Naming an analyzer, as -locktx or -unbounded, runs only the analyzers
// named, so -locktx alone turns Unbounded and ZeroDefault off, and LockTx
// still needs -locktx.enable. In another driver, set the flag with
// LockTx.Flags.Set("enable", "true").
//
// The package does not import velox: it names velox's types by their import
// paths, so a program that runs it does not build velox.
package veloxvet

import "golang.org/x/tools/go/analysis"

// Analyzers are every analyzer of the package. LockTx among them does
// nothing unless its -enable flag is set.
var Analyzers = []*analysis.Analyzer{Unbounded, ZeroDefault, LockTx}
