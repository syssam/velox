package veloxgql

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// What stopped a response: a limit, in the order they are looked for.
const (
	stopBytes int32 = iota + 1
	stopErrors
	stopTimeout
)

// responseKey is the context key of a response's *response.
type responseKey struct{}

// operationKey is the context key of a query's or a mutation's *operation.
type operationKey struct{}

// operation is what a query or a mutation holds across its responses: @defer's are
// resolved on the first one's context, under its limits.
type operation struct{ first *response }

// response is what the limits keep of one response, which the fields' goroutines share:
// a subscription's event is one, and a query's or a mutation's responses share theirs.
type response struct {
	limits Limits
	// mutation is the operation's, when its resolvers run one at a time.
	mutation *mutation
	// bytes, fieldErrs and presented count its values and its errors. fieldErrs holds
	// under any presenter, and presented under ErrorPresenter sees the errors no resolver
	// returned, and keeps those past the limit from being made.
	bytes, fieldErrs, presented atomic.Int64
	// why is what stopped it, or 0.
	why atomic.Int32
	// cancel ends what its fields run, and parent is the context Timeout was added to;
	// nil for a subscription's event.
	cancel context.CancelFunc
	parent context.Context
}

// expired stops the response if ctx, which Timeout is on, ran out, and not the request
// it came from: a deadline of the application's is the application's.
func (r *response) expired(ctx context.Context) {
	if r.cancel != nil && r.parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		r.stop(stopTimeout)
	}
}

// stop stops the response, for the first reason given.
func (r *response) stop(why int32) {
	if r.why.CompareAndSwap(0, why) && r.cancel != nil {
		r.cancel()
	}
}

// fieldError counts an error a resolver returned.
func (r *response) fieldError() {
	if r.fieldErrs.Add(1) > int64(r.limits.MaxErrors) {
		r.stop(stopErrors)
	}
}

// admit reports whether a response keeps one more error.
func (r *response) admit() bool {
	if why := r.why.Load(); why != 0 && why != stopErrors {
		return false // replaced whole, and what its stop cancelled is no internal error
	}
	if r.presented.Add(1) > int64(r.limits.MaxErrors) {
		r.stop(stopErrors)
		return false
	}
	return true
}

// count counts v, a field's value.
func (r *response) count(v any) {
	if n := valueBytes(v); n > 0 && r.bytes.Add(n) > int64(r.limits.MaxResponseBytes) {
		r.stop(stopBytes)
	}
}

// valueBytes is what v, a field's value, writes in a response, if it can hold more than
// a fixed few bytes: a string as it is escaped, []byte as base64 and JSON as it is. Any
// other value is 0, so a row or a number costs one type switch. It reads a string once
// more, which gqlgen does in writing it.
func valueBytes(v any) int64 {
	switch x := v.(type) {
	case string:
		return quotedLen(x)
	case *string:
		if x != nil {
			return quotedLen(*x)
		}
	case Bytes:
		return int64(base64.StdEncoding.EncodedLen(len(x)))
	case *Bytes:
		if x != nil {
			return int64(base64.StdEncoding.EncodedLen(len(*x)))
		}
	case JSON:
		return int64(len(x))
	case []byte:
		return int64(base64.StdEncoding.EncodedLen(len(x)))
	case []string:
		var n int64
		for _, s := range x {
			n += quotedLen(s)
		}
		return n
	case nil, bool, int, int32, int64, float64:
		return 0
	}
	// A type of the application's whose kind is one of those.
	switch t := reflect.TypeOf(v); t.Kind() {
	case reflect.String:
		return quotedLen(reflect.ValueOf(v).String())
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return int64(base64.StdEncoding.EncodedLen(reflect.ValueOf(v).Len()))
		}
	}
	return 0
}

// quotedLen is the bytes of s as gqlgen quotes it, past the quotes.
func quotedLen(s string) int64 {
	n := int64(len(s))
	for i := range len(s) {
		switch c := s[i]; {
		case c == '"' || c == '\\' || c == '\t' || c == '\n' || c == '\r':
			n++
		case c < 0x20:
			n += 5 // \u00XX
		}
	}
	return n
}

// operationResponse is the response ctx, a response's context, belongs to, and the context
// its fields run in. The first of a query or a mutation starts its Timeout. done ends what
// the first response started, when the last one is out.
func (e *Extension) operationResponse(ctx context.Context) (context.Context, *response, func(*graphql.Response)) {
	op, _ := ctx.Value(operationKey{}).(*operation)
	if op == nil {
		// A subscription's event, each under the limits anew.
		r := &response{limits: e.limits}
		return context.WithValue(ctx, responseKey{}, r), r, func(*graphql.Response) {}
	}
	if op.first != nil {
		return context.WithValue(ctx, responseKey{}, op.first), op.first, func(*graphql.Response) {}
	}
	r := &response{limits: e.limits, parent: ctx}
	op.first = r
	ctx, cancel := context.WithTimeout(ctx, e.limits.Timeout)
	r.cancel = cancel
	if m, _ := ctx.Value(mutationKey{}).(*mutation); m != nil && !m.plain {
		r.mutation = m
	}
	stop := context.AfterFunc(ctx, func() { r.expired(ctx) })
	return context.WithValue(ctx, responseKey{}, r), r, func(resp *graphql.Response) {
		// @defer's fragments run on this context until the last response is out.
		if resp == nil || resp.HasNext == nil || !*resp.HasNext {
			stop()
			cancel()
		}
	}
}

// finish is resp, the response r made, or what replaces it when r stopped. ctx is the
// response's context as gqlgen gave it, which presents an error.
// No response is the end of a stream, and stays so: gqlgen's streaming transports ask
// until they get none, and an error made for it would be sent without end.
func (e *Extension) finish(ctx context.Context, r *response, resp *graphql.Response) *graphql.Response {
	why := r.why.Load()
	if why == 0 || resp == nil {
		return resp
	}
	out := &graphql.Response{}
	if resp != nil {
		*out = *resp
		out.Data, out.Errors = nil, nil
		if why == stopErrors {
			out.Errors = resp.Errors[:min(len(resp.Errors), r.limits.MaxErrors)]
		}
	}
	var err *gqlcore.Error
	switch why {
	case stopBytes:
		err = &gqlcore.Error{Code: CodeTooCostly, Err: ErrResponseTooLarge,
			Msg: fmt.Sprintf("%v: its values are over %d bytes", ErrResponseTooLarge, r.limits.MaxResponseBytes)}
	case stopErrors:
		err = &gqlcore.Error{Code: CodeTooCostly, Err: ErrTooManyErrors,
			Msg: fmt.Sprintf("%v: over %d: the rest are left out, and it stopped", ErrTooManyErrors, r.limits.MaxErrors)}
	default:
		err = &gqlcore.Error{Code: CodeTimeout, Err: ErrTimeout,
			Msg: fmt.Sprintf("%v: it ran over %v", ErrTimeout, r.limits.Timeout)}
	}
	rctx := graphql.WithFreshResponseContext(ctx)
	graphql.AddError(rctx, &gqlerror.Error{Err: err, Message: err.Error(), Extensions: err.Extensions()})
	out.Errors = append(gqlerror.List(nil), out.Errors...)
	out.Errors = append(out.Errors, graphql.GetErrors(rctx)...)
	return out
}
