// Command veloxvet runs veloxvet's analyzers, as go vet's tool or alone:
//
//	go vet -vettool=$(which veloxvet) ./...
//	veloxvet ./...
package main

import (
	"golang.org/x/tools/go/analysis/multichecker"

	"github.com/syssam/velox/veloxvet"
)

func main() { multichecker.Main(veloxvet.Analyzers...) }
