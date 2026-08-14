package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testschema mixes ID types: Token has a uuid.UUID id, every other
// entity has an int id. Each generated NodeResolver type-asserts the id
// to its own ID type and returns a plain error on mismatch:
//
//	typedID, ok := id.(int)
//	if !ok {
//		return nil, fmt.Errorf("velox: NodeResolver: unexpected id type %T", id)
//	}
//
// Client.Noder iterates runtime.NodeResolvers() and only skips
// NotFound errors — every other error aborts the whole lookup. So a
// type mismatch from an unrelated entity's resolver kills the lookup
// for the entity that would have matched.
//
// NodeResolvers() returns a map, and Go randomizes map iteration order,
// so the failure is probabilistic: Noder(ctx, intID) succeeds only when
// the int-keyed resolvers happen to be visited before Token's.
//
// This matters because Noder backs the Relay node(id:) field — the
// standard GraphQL entry point for refetching any object.
func TestNoder_MixedIDTypes_IsOrderDependent(t *testing.T) {
	ctx := context.Background()
	client := openTestClient(t)
	u := createUser(t, client, "alice", "alice@x")

	const attempts = 200
	var ok, typeMismatch int
	var lastErr error

	for range attempts {
		n, err := client.Noder(ctx, u.ID)
		switch {
		case err == nil && n != nil:
			ok++
		default:
			typeMismatch++
			lastErr = err
		}
	}

	t.Logf("Noder(int id) over %d attempts: success=%d failure=%d lastErr=%v",
		attempts, ok, typeMismatch, lastErr)

	assert.Equal(t, attempts, ok,
		"Noder must resolve an int-keyed entity regardless of resolver iteration "+
			"order; a uuid-keyed resolver's type-assertion error must be skipped "+
			"like NotFound, not abort the lookup (failures=%d, err=%v)",
		typeMismatch, lastErr)
}

// Noders has the same loop and the same abort-on-any-error behavior.
func TestNoders_MixedIDTypes_IsOrderDependent(t *testing.T) {
	ctx := context.Background()
	client := openTestClient(t)
	a := createUser(t, client, "alice", "alice@x")
	b := createUser(t, client, "bob", "bob@x")

	const attempts = 200
	var ok int
	var lastErr error

	for range attempts {
		ns, err := client.Noders(ctx, []int{a.ID, b.ID})
		if err == nil && len(ns) == 2 && ns[0] != nil && ns[1] != nil {
			ok++
			continue
		}
		lastErr = err
	}

	t.Logf("Noders(int ids) over %d attempts: success=%d lastErr=%v", attempts, ok, lastErr)
	require.Equal(t, attempts, ok,
		"Noders must resolve int-keyed entities regardless of iteration order (err=%v)", lastErr)
}
