// Command gen writes handles.go. go generate runs it in the module root.
package main

import (
	"log"
	"os"

	"github.com/syssam/velox/internal/handlegen"
)

func main() {
	src, err := handlegen.Generate()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("handles.go", src, 0o644); err != nil {
		log.Fatal(err)
	}
}
