package veloxgql

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
// resolved on the first one's context, under its limits. end lets go of that context's
// Timeout, once the last response is out.
type operation struct {
	first *response
	end   func()
}

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
	// cancel ends what its fields run, timeout is the context Timeout is on, and
	// parent the one it was added to; nil for a subscription's event.
	cancel          context.CancelFunc
	timeout, parent context.Context
}

// expired stops the response and reports true if its Timeout ran out. A deadline of
// the application's, the request's or a transaction's (velox.TxTimeout), is the
// application's: a field's context can end by one of those first.
func (r *response) expired() bool {
	if r.timeout != nil && r.parent.Err() == nil && errors.Is(r.timeout.Err(), context.DeadlineExceeded) {
		r.stop(stopTimeout)
		return true
	}
	return false
}

// failed counts err, a field's error; ctx is the field's context. A field that failed
// as the Timeout ended its context is the Timeout's, not an error of its own: at the
// deadline they all fail, before the Timeout's own callback runs. One that failed as
// the request went away counts for nothing, as no one reads its response.
func (r *response) failed(ctx context.Context) {
	switch {
	case ctx.Err() == nil:
	case r.expired():
		return
	case r.parent != nil && r.parent.Err() != nil:
		return
	}
	r.fieldError()
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
// a fixed few bytes: a string as it is escaped, []byte as base64, and JSON and
// json.RawMessage as they are. A pointer to one of those, or a list of them, counts
// what it points to or holds. Any other value is 0: a number costs one type switch, and
// a row a type switch and a look at its type. It reads a string once more, which gqlgen
// does in writing it.
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
	case json.RawMessage:
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
	// A type of the application's whose kind is one of those, a pointer to
	// one, or a list of them, such as the []*string of a [String] field.
	if !sized(reflect.TypeOf(v), 0) {
		return 0
	}
	return reflectBytes(reflect.ValueOf(v))
}

// maxSizedDepth bounds the pointers and lists sized looks through. A GraphQL list
// nests one level per [ ], so no schema comes near it, and a type that holds itself,
// such as type L []L, ends at it instead of overflowing the stack.
const maxSizedDepth = 16

// sized reports whether a value of type t can hold more than a fixed few bytes: t is
// a string, a []byte, a JSON, or a pointer to or list of one, under maxSizedDepth
// levels; depth is the levels above t. A list of rows is not, so it costs no walk.
func sized(t reflect.Type, depth int) bool {
	for ; t.Kind() == reflect.Pointer; depth++ {
		if depth >= maxSizedDepth {
			return false
		}
		t = t.Elem()
	}
	if depth >= maxSizedDepth {
		return false
	}
	switch t.Kind() {
	case reflect.String:
		return true
	case reflect.Slice:
		return t.Elem().Kind() == reflect.Uint8 || sized(t.Elem(), depth+1)
	}
	return false
}

// reflectBytes is valueBytes of v, whose type is sized, so it ends within
// maxSizedDepth levels.
func reflectBytes(v reflect.Value) int64 {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.String:
		return quotedLen(v.String())
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			if t := v.Type(); t == reflect.TypeFor[JSON]() || t == reflect.TypeFor[json.RawMessage]() {
				return int64(v.Len()) // as it is, not as base64
			}
			return int64(base64.StdEncoding.EncodedLen(v.Len()))
		}
		var n int64
		for i := range v.Len() {
			n += reflectBytes(v.Index(i))
		}
		return n
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
// its fields run in. The first of a query or a mutation starts its Timeout. done, given
// the response made, ends what the first response started when it is the last one out:
// @defer's fragments run on the first one's context until then, and each of their
// responses is one more.
func (e *Extension) operationResponse(ctx context.Context) (context.Context, *response, func(*graphql.Response)) {
	op, _ := ctx.Value(operationKey{}).(*operation)
	if op == nil {
		// A subscription's event, each under the limits anew.
		r := &response{limits: e.limits}
		return context.WithValue(ctx, responseKey{}, r), r, func(*graphql.Response) {}
	}
	done := func(resp *graphql.Response) {
		if resp == nil || resp.HasNext == nil || !*resp.HasNext {
			op.end()
		}
	}
	if op.first != nil {
		return context.WithValue(ctx, responseKey{}, op.first), op.first, done
	}
	r := &response{limits: e.limits, parent: ctx}
	op.first = r
	ctx, cancel := context.WithTimeout(ctx, e.limits.Timeout)
	r.cancel, r.timeout = cancel, ctx
	if m, _ := ctx.Value(mutationKey{}).(*mutation); m != nil && !m.plain {
		r.mutation = m
	}
	stop := context.AfterFunc(ctx, func() { r.expired() })
	op.end = func() {
		stop()
		cancel()
	}
	return context.WithValue(ctx, responseKey{}, r), r, done
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
