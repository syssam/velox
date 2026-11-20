package gen_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"

	"example.com/tmp/gen"
)

// Post's Policy returns a pointer: velox refuses the entity at its first
// use instead of leaving the policy out.
func TestWrongPolicySignatureIsRefused(t *testing.T) {
	err := velox.CheckEntity[gen.Post]()
	if _, ok := errors.AsType[*velox.SchemaError](err); !ok || !strings.Contains(err.Error(), "*schema.Post's Policy method is not func() velox.Policy: velox would not call it") {
		t.Fatalf("got %v", err)
	}
}
