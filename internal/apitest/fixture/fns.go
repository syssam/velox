package fixture

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/mixin"
)

// Fns is a Descriptor.Schema made of functions, for tests that compose
// behaviour without declaring a type for it: velox reads each function as
// the behaviour of one mixin, the policies', then the hooks', then the
// interceptors', each in order.
type Fns struct {
	Policy       []func() velox.Policy
	Hooks        []func() []velox.Hook
	Interceptors []func() []velox.Interceptor
}

// Mixin returns a mixin for each function of f.
func (f Fns) Mixin() []schema.Mixin {
	var out []schema.Mixin
	for _, p := range f.Policy {
		out = append(out, policyFn{fn: p})
	}
	for _, h := range f.Hooks {
		out = append(out, hooksFn{fn: h})
	}
	for _, i := range f.Interceptors {
		out = append(out, interceptorsFn{fn: i})
	}
	return out
}

type policyFn struct {
	mixin.Schema
	fn func() velox.Policy
}

func (m policyFn) Policy() velox.Policy { return m.fn() }

type hooksFn struct {
	mixin.Schema
	fn func() []velox.Hook
}

func (m hooksFn) Hooks() []velox.Hook { return m.fn() }

type interceptorsFn struct {
	mixin.Schema
	fn func() []velox.Interceptor
}

func (m interceptorsFn) Interceptors() []velox.Interceptor { return m.fn() }

// Org is the tenancy mixin of OrgPolicy and OrgHooks.
type Org struct{ mixin.Schema }

// Policy is OrgPolicy.
func (Org) Policy() velox.Policy { return OrgPolicy() }

// Hooks is OrgHooks.
func (Org) Hooks() []velox.Hook { return OrgHooks() }
