package privacy

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecision_IsError(t *testing.T) {
	var err error = Allow
	assert.NotNil(t, err)
	assert.Equal(t, "velox/privacy: allow rule", err.Error())

	err = Deny
	assert.Equal(t, "velox/privacy: deny rule", err.Error())

	err = Skip
	assert.Equal(t, "velox/privacy: skip rule", err.Error())
}

func TestDecision_ErrorsIs(t *testing.T) {
	assert.True(t, errors.Is(Allow, Allow))
	assert.True(t, errors.Is(Deny, Deny))
	assert.True(t, errors.Is(Skip, Skip))

	assert.False(t, errors.Is(Allow, Deny))
	assert.False(t, errors.Is(Allow, Skip))
	assert.False(t, errors.Is(Deny, Skip))
}

func TestDecision_WrappedErrorsIs(t *testing.T) {
	err := Allowf("test %s", "reason")
	assert.True(t, errors.Is(err, Allow))
	assert.False(t, errors.Is(err, Deny))

	err = Denyf("access denied: %s", "no permission")
	assert.True(t, errors.Is(err, Deny))

	err = Skipf("skipping: %s", "not applicable")
	assert.True(t, errors.Is(err, Skip))
}

func TestDecision_TypeAssertion(t *testing.T) {
	var d *Decision
	require.True(t, errors.As(Allow, &d))
	assert.Equal(t, "velox/privacy: allow rule", d.Error())

	require.True(t, errors.As(Deny, &d))
	assert.Equal(t, "velox/privacy: deny rule", d.Error())
}

func TestDecision_IsDecision(t *testing.T) {
	assert.True(t, IsDecision(Allow))
	assert.True(t, IsDecision(Deny))
	assert.True(t, IsDecision(Skip))
	assert.True(t, IsDecision(Allowf("wrapped")))
	assert.True(t, IsDecision(Denyf("wrapped")))
	assert.False(t, IsDecision(fmt.Errorf("random error")))
	assert.False(t, IsDecision(nil))
}
