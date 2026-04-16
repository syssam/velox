package gqlrelay

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrNodeNotFound(t *testing.T) {
	assert.EqualError(t, ErrNodeNotFound, "could not resolve to a node with the global ID")
}
