package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox"
)

type namedPolicy string

func (namedPolicy) EvalMutation(context.Context, velox.Mutation) error { return nil }
func (namedPolicy) EvalQuery(context.Context, velox.Query) error       { return nil }

// TestEntityPolicy_ReadsTheRegisteredVariable pins that the registry holds
// the entity's policy variable, not a copy of its value at registration: a
// lookup after the variable is replaced returns the replacement, as the
// entity's own client (which reads the variable) would see it.
func TestEntityPolicy_ReadsTheRegisteredVariable(t *testing.T) {
	const name = "PolicyRegistryTestEntity"
	t.Cleanup(func() {
		policyMu.Lock()
		delete(policyRegistry, name)
		policyMu.Unlock()
	})

	var v velox.Policy = namedPolicy("init")
	RegisterEntityPolicy(name, &v)
	assert.Equal(t, namedPolicy("init"), EntityPolicy(name))

	v = namedPolicy("replaced")
	assert.Equal(t, namedPolicy("replaced"), EntityPolicy(name))

	v = nil
	assert.Nil(t, EntityPolicy(name))

	RegisterEntityPolicy("PolicyRegistryTestNil", nil)
	assert.Nil(t, EntityPolicy("PolicyRegistryTestNil"))
	assert.Nil(t, EntityPolicy("NoSuchEntity"))
}
