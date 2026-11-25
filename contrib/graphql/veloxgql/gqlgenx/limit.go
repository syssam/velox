package gqlgenx

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx/internal/request"
)

// CostLimit returns the gqlgen handler extension that refuses a request that
// costs too much, before any resolver runs, so no statement is sent. It is
// opt-in, for a server open to clients:
//
//	srv.Use(gqlgenx.CostLimit(db))
//
// It reads the limits of db (veloxgql.LimitsOf), those its connections and
// edges read under, so that what it counts is what they read; a nil db has
// veloxgql.DefaultLimits(). A request whose Cost is over MaxCost, or whose
// introspection, counted by the fields of its response, is over
// MaxIntrospectionCost, fails with veloxgql.ErrTooCostly, the code
// veloxgql.CodeTooCostly and data null.
//
// Every limit of veloxgql.Limits but MaxCost bounds one argument or one
// edge, not a request: a query that nests connections reads up to MaxPage to
// the power of its depth rows, and aliases repeat a field as many times as
// the client writes it. Cost multiplies by first and last, counts every
// alias, every statement and every branch of a fragment. It reads the
// schema the server's Use gives Validate; gqlgen's
// extension.FixedComplexityLimit counts fields, not rows, and is not needed
// with it. Make one per server: the server's Use gives it the server's
// schema.
func CostLimit(db *velox.DB) graphql.HandlerExtension {
	return &costLimit{limits: veloxgql.LimitsOf(db)}
}

// costLimit is CostLimit's extension.
type costLimit struct {
	limits veloxgql.Limits
	// schema is the server's, which Validate stores.
	schema *ast.Schema
}

var _ graphql.OperationContextMutator = (*costLimit)(nil)

// ExtensionName implements graphql.HandlerExtension.
func (*costLimit) ExtensionName() string { return "VeloxCostLimit" }

// Validate implements graphql.HandlerExtension: it stores the server's
// schema.
func (c *costLimit) Validate(es graphql.ExecutableSchema) error {
	c.schema = es.Schema()
	return nil
}

// MutateOperationContext implements graphql.OperationContextMutator: it
// refuses a request whose Cost is over MaxCost with veloxgql.ErrTooCostly.
// gqlgen runs it after validation and before any resolver.
func (c *costLimit) MutateOperationContext(_ context.Context, rc *graphql.OperationContext) *gqlerror.Error {
	err := request.CostError(c.schema, rc.Operation, rc.Variables, c.limits)
	if err == nil {
		return nil
	}
	return &gqlerror.Error{Err: err, Message: err.Error(), Extensions: err.Extensions()}
}

// RequestLimit returns the gqlgen handler extension that refuses a request
// whose document or variables are over the limits of db (veloxgql.LimitsOf;
// veloxgql.DefaultLimits() for a nil db), before gqlgen parses it, on every
// transport. It is opt-in, for a server open to clients:
//
//	srv.Use(gqlgenx.RequestLimit(db))
//
// gqlgen's validation is quadratic in the fields that share a response name,
// so it parses the request itself, under MaxTokens, and refuses one over
// MaxBodyBytes, MaxTokens, MaxFields, MaxFragments, MaxDepth, MaxNameBytes or
// MaxLiteralBytes, or whose variables are over MaxBodyBytes, MaxDepth or
// MaxVariableNodes, with veloxgql.ErrRequestTooLarge (BAD_USER_INPUT), or the
// parser's error over MaxTokens. That is a second parse, which a query cache
// of gqlgen's does not spare. Use it before
// extension.AutomaticPersistedQuery, so that the cache never stores a refused
// query. Make one per server: the server's Use gives it the server's schema,
// whose names and inputs it weighs.
func RequestLimit(db *velox.DB) graphql.HandlerExtension {
	l := veloxgql.LimitsOf(db)
	return &requestLimit{limits: l, bounds: request.NewBounds(l, nil)}
}

// requestLimit is RequestLimit's extension.
type requestLimit struct {
	limits veloxgql.Limits
	// bounds is what the check of a request reads, of the limits and the
	// server's schema, which Validate gives it.
	bounds request.Bounds
}

var _ graphql.OperationParameterMutator = (*requestLimit)(nil)

// ExtensionName implements graphql.HandlerExtension.
func (*requestLimit) ExtensionName() string { return "VeloxRequestLimit" }

// Validate implements graphql.HandlerExtension: it weighs the server's
// schema.
func (r *requestLimit) Validate(es graphql.ExecutableSchema) error {
	r.bounds = request.NewBounds(r.limits, es.Schema())
	return nil
}

// MutateOperationParameters implements graphql.OperationParameterMutator: it
// refuses a request whose document or variables are over a limit. gqlgen
// runs it before it parses or validates, whichever the transport.
func (r *requestLimit) MutateOperationParameters(_ context.Context, p *graphql.RawParams) *gqlerror.Error {
	return request.Check(p.OperationName, p.Query, p.Variables, r.bounds)
}
