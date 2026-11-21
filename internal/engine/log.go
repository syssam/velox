package engine

import (
	"bytes"
	"context"
	"log/slog"
	"time"
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
func MaskedArgs(d Dialect, st *Statement) []any {
	shown := ShownArgs(d, st)
	out := make([]any, len(st.Args))
	for i, a := range st.Args {
		switch {
		case shown == nil:
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
		var args []any
		if c.args {
			args = MaskedArgs(st.Dialect, st)
		}
		start := time.Now()
		err := next(ctx)
		logStatement(ctx, l, st, args, time.Since(start)-st.Result.LoopBody, c.args, err)
		return err
	})
}

// logStatement writes one statement's record.
func logStatement(ctx context.Context, l *slog.Logger, st *Statement, args []any, d time.Duration, withArgs bool, err error) {
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
		attrs = append(attrs, slog.Any("err", err))
	}
	l.LogAttrs(ctx, slog.LevelDebug, "velox: statement", attrs...)
}
