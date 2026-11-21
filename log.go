package velox

import (
	"log/slog"

	"github.com/syssam/velox/internal/engine"
)

// LogOption configures WithLog: LogArgs.
type LogOption = engine.LogOption

// LogArgs makes WithLog print each statement's arguments, masked: the value
// of a column whose ColumnDescriptor says Sensitive is always printed as
// "<sensitive>", and an argument velox cannot tie to a column as
// "<hidden>". velox ties an argument to a column where it compares, assigns
// or inserts it, and a limit or offset to none, and shows those. It cannot
// tie an Expr's, a Calc's or a SetExpr's bound values, nor any of raw SQL
// (Raw, Exec), so those are hidden, as is every argument of a statement
// whose arguments an Around hook changed before WithLog's hook saw them.
func LogArgs() LogOption { return engine.LogArgs() }

// WithLog logs each statement to l at Debug level, as ent's Debug does,
// after it has run: the attributes sql, the text sent; args, only with
// LogArgs; duration, from sending it to its last row, without Iter's loop
// body; rows, those a write affected or a read returned; and err, when it
// failed. Nothing is logged, and nothing more is done, while l does not
// enable Debug for the statement's context.
//
// A driver's message can echo a value, as MySQL's "Duplicate entry 'x' for
// key …" does, so err is the error's text only with LogArgs and when every
// argument of the statement was shown. Otherwise it is redacted: a
// constraint violation's entity, kind, constraint name and driver code, or
// any other error's Go types and driver code, with "the message is hidden".
//
//	db, err := velox.Connect("postgres", dsn, velox.WithLog(slog.Default(), velox.LogArgs()))
//
// It is an Around hook, in the place it is given among WithAround's: one
// given before it wraps it, and its duration includes the ones given after.
// Without WithLog, velox adds nothing. A nil l panics.
func WithLog(l *slog.Logger, opts ...LogOption) Option { return engine.WithLog(l, opts...) }
