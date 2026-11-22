package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/syssam/velox/internal/dberr"
)

// ArgShow is what a log may show of a statement's argument.
type ArgShow uint8

// The kinds of argument a log tells apart.
const (
	// ArgHidden is an argument velox cannot tie to a column: an Expr's, a
	// raw statement's, or any when the statement cannot be read again.
	ArgHidden ArgShow = iota
	// ArgShown is the value of a column that is not Sensitive, or a value
	// velox computed, such as a limit.
	ArgShown
	// ArgSensitive is the value of a Sensitive column.
	ArgSensitive
)

// showOf is what a log may show of a value of column c.
func showOf(c *ColumnInfo) ArgShow {
	if c.Sensitive {
		return ArgSensitive
	}
	return ArgShown
}

// ShownArgs returns, for each of st.Args, what a log may show of it, from
// writing st's arguments again; nil when it cannot tell: for raw SQL, which
// velox does not render, and when the arguments written again differ from
// st.Args, as they do when an Around hook changed them. A nil result hides
// every argument.
func ShownArgs(d Dialect, st *Statement) []ArgShow {
	if st.Op == OpRawQuery || st.Op == OpRawExec || st.Entity == nil {
		return nil
	}
	w := writer{d: d, in: st.Entity, argsOnly: true, track: true}
	w.statement(st)
	if w.err != nil || len(w.args) != len(st.Args) || len(w.shown) != len(w.args) {
		return nil
	}
	for i := range w.args {
		if !sameArg(w.args[i], st.Args[i]) {
			return nil
		}
	}
	return w.shown
}

// sameArg reports whether a and b are the same argument; a value Go cannot
// compare counts as different.
func sameArg(a, b any) (same bool) {
	if x, ok := a.([]byte); ok {
		y, ok := b.([]byte)
		return ok && bytes.Equal(x, y)
	}
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return a == b
}

// Log masks: what a log prints in place of an argument it hides.
const (
	SensitiveArg = "<sensitive>"
	HiddenArg    = "<hidden>"
)

// MaskedArgs returns what a log prints for st's arguments: each one that
// ShownArgs shows, SensitiveArg for a Sensitive column's and HiddenArg
// for the rest.
func MaskedArgs(d Dialect, st *Statement) []any { return masked(ShownArgs(d, st), st.Args) }

// masked is args as shown says to print them; a nil shown hides them all.
func masked(shown []ArgShow, args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		switch {
		case shown == nil || i >= len(shown):
			out[i] = HiddenArg
		case shown[i] == ArgShown:
			out[i] = a
		case shown[i] == ArgSensitive:
			out[i] = SensitiveArg
		default:
			out[i] = HiddenArg
		}
	}
	return out
}

// LogOption configures WithLog.
type LogOption struct{ apply func(*logConfig) }

type logConfig struct{ args bool }

// LogArgs makes WithLog print the statements' arguments, masked.
func LogArgs() LogOption { return LogOption{func(c *logConfig) { c.args = true }} }

// WithLog logs each statement to l at Debug, as an Around hook in the place
// it is given among WithAround's. A nil l panics.
func WithLog(l *slog.Logger, opts ...LogOption) Option {
	if l == nil {
		panic("velox: WithLog(nil)")
	}
	var c logConfig
	for _, o := range opts {
		if o.apply != nil {
			o.apply(&c)
		}
	}
	return WithAround(func(ctx context.Context, st *Statement, next func(context.Context) error) error {
		if !l.Enabled(ctx, slog.LevelDebug) {
			return next(ctx)
		}
		// The arguments are masked before next, as a hook given after this
		// one may change them.
		var args []any
		all := false
		if c.args {
			shown := ShownArgs(st.Dialect, st)
			args, all = masked(shown, st.Args), allShown(shown, len(st.Args))
		}
		start := time.Now()
		err := next(ctx)
		logStatement(ctx, l, st, args, all, time.Since(start)-st.Result.LoopBody, c.args, err)
		return err
	})
}

// logStatement writes one statement's record. args are its arguments,
// masked before it ran, and all says none was masked; withArgs says LogArgs
// was given. err's text is printed only when withArgs and all hold.
func logStatement(ctx context.Context, l *slog.Logger, st *Statement, args []any, all bool, d time.Duration, withArgs bool, err error) {
	rows := st.Result.Affected
	if rows < 0 {
		rows = st.Result.Rows
	}
	attrs := make([]slog.Attr, 0, 5)
	attrs = append(attrs, slog.String("sql", st.SQL))
	if withArgs {
		attrs = append(attrs, slog.Any("args", args))
	}
	attrs = append(attrs, slog.Duration("duration", d), slog.Int64("rows", rows))
	if err != nil {
		if withArgs && all {
			attrs = append(attrs, slog.Any("err", err))
		} else {
			attrs = append(attrs, slog.String("err", RedactedError(st, err)))
		}
	}
	l.LogAttrs(ctx, slog.LevelDebug, "velox: statement", attrs...)
}

// allShown reports whether shown shows every one of n arguments.
func allShown(shown []ArgShow, n int) bool {
	if len(shown) != n {
		return false
	}
	for _, s := range shown {
		if s != ArgShown {
			return false
		}
	}
	return true
}

// RedactedError is what WithLog prints for err, the error of statement st,
// when it may not print err's text: a driver's message can echo a value,
// as MySQL's "Duplicate entry 'x' for key …" and PostgreSQL's "invalid
// input syntax for type uuid: \"x\"" do. For a constraint violation it
// names the entity, the kind, the constraint and the driver's code; for
// one of velox's own errors that holds no value (safeError), the entity and
// that error's fixed text; for any other error, the Go types of its chain.
// It adds the driver's code when there is one, and whether a context's end
// or velox's deadline caused the error.
func RedactedError(st *Statement, err error) string {
	entity := ""
	if st.Entity != nil {
		entity = st.Entity.Name
	}
	code, hasCode := dberr.DriverCode(err)
	ce, ok := dberr.As[*ConstraintError](err)
	if !ok {
		ce = dberr.Constraint(entity, err)
	}
	var b strings.Builder
	safe := ""
	if ce != nil {
		name := ce.Constraint
		if name == "" {
			name = "(unnamed)"
		}
		if ce.Entity != "" {
			b.WriteString("velox: ")
			b.WriteString(ce.Entity)
			b.WriteString(": ")
		}
		b.WriteString(ce.Kind.String())
		b.WriteString(" constraint ")
		b.WriteString(name)
		b.WriteString(" violated")
	} else if safe = safeError(entity, err); safe != "" {
		b.WriteString(safe)
	} else {
		b.WriteString(errTypes(err))
	}
	if hasCode {
		b.WriteString(" (code ")
		b.WriteString(code)
		b.WriteString(")")
	}
	switch {
	case dberr.Is(err, ErrTimeout):
		// safeError's text names ErrTimeout already; a constraint's does not.
		if safe == "" {
			b.WriteString(", velox's deadline passed")
		}
	case errors.Is(err, context.DeadlineExceeded):
		b.WriteString(", context deadline exceeded")
	case errors.Is(err, context.Canceled):
		b.WriteString(", context canceled")
	}
	b.WriteString("; the message is hidden, as it may hold a value")
	return b.String()
}

// errTypes is the Go types of err's chain of Unwrap() error, outermost
// first, at most five, each once.
func errTypes(err error) string {
	var ts []string
	for e := err; e != nil && len(ts) < 5; e = errors.Unwrap(e) {
		t := fmt.Sprintf("%T", e)
		if !slices.Contains(ts, t) {
			ts = append(ts, t)
		}
	}
	return strings.Join(ts, " > ")
}

// safeSentinels are velox's errors whose text is fixed and holds no value,
// in the order safeError names them. ErrOutOfRange and ErrNoShard are not
// among them, nor a ValidationError's Err, a PolicyError's Err or an
// EdgeError's keys: they may hold one.
var safeSentinels = [...]error{
	ErrStale, ErrNotFound, ErrNotSingular, ErrUnbounded, ErrNoWhere,
	ErrLimitTooLarge, ErrTooManyRows, ErrImmutable, ErrVersionSet,
	ErrPolicyUpsert, ErrOutsideTx, ErrTxControl, ErrDryRun, ErrDenied,
	ErrTimeout, ErrLocked,
}

// safeError names err, a statement's error on entity, by what of it is
// velox's own and holds no value: a ValidationError's entity and field
// alone, as its Err is the validator's; else an EdgeError's entity and edge
// and the fixed text of each of safeSentinels that err matches. It never
// prints the text of the errors that wrap them, and is "" when err matches
// none.
func safeError(entity string, err error) string {
	if ve, ok := dberr.As[*ValidationError](err); ok {
		return "velox: " + ve.Entity + "." + ve.Field + ": validation failed"
	}
	var parts []string
	if ee, ok := dberr.As[*EdgeError](err); ok {
		parts = append(parts, ee.Entity+": edge "+ee.Edge)
		entity = ""
	}
	for _, s := range safeSentinels {
		if dberr.Is(err, s) {
			parts = append(parts, s.Error())
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if entity != "" {
		parts[0] = entity + ": " + parts[0]
	}
	return "velox: " + strings.Join(parts, "; ")
}
