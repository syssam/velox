package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// execInfo is the EntityInfo of Exec's statements: the name their errors and
// the Around hooks see. Exec scans nothing, so it has no table or columns.
var execInfo = EntityInfo{Name: "Exec"}

// rowsInfo is the EntityInfo of Rows's statements, as execInfo is Exec's.
var rowsInfo = EntityInfo{Name: "Rows"}

// ErrNoReason is the error of raw SQL run without a reason.
func ErrNoReason(name string) error {
	return fmt.Errorf("velox: %s: raw SQL needs a reason, as it applies no policy", name)
}

// RawColumns maps the columns r returns, by name, to in's columns. It refuses
// a column in has no field for, and a column returned twice, which would
// scan into one field twice.
func RawColumns(in *EntityInfo, r Rows) ([]int, error) {
	names, err := resultColumns(r)
	if err != nil {
		return nil, err
	}
	cols := make([]int, len(names))
	seen := make(map[int]bool, len(names))
	var unknown, twice []string
	for i, name := range names {
		c, ok := in.Column(name)
		switch {
		case !ok:
			unknown = append(unknown, strconv.Quote(name))
		case seen[c] && !slices.Contains(twice, strconv.Quote(name)):
			twice = append(twice, strconv.Quote(name))
		}
		seen[c], cols[i] = true, c
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("the raw SQL returned %s, which %s has no field for", strings.Join(unknown, ", "), in.Name)
	}
	if len(twice) > 0 {
		return nil, fmt.Errorf("the raw SQL returned %s more than once", strings.Join(twice, ", "))
	}
	return cols, nil
}

// resultColumns returns the names of the columns r returns: database/sql's
// rows and the test executor's report them with Columns, pgx's with
// FieldDescriptions, which names a pgconn type, so it is called by its name.
func resultColumns(r Rows) ([]string, error) {
	if c, ok := r.(*countedRows); ok {
		r = c.Rows
	}
	if c, ok := r.(interface{ Columns() ([]string, error) }); ok {
		return c.Columns()
	}
	if m := reflect.ValueOf(r).MethodByName("FieldDescriptions"); m.IsValid() && m.Type().NumIn() == 0 && m.Type().NumOut() == 1 {
		fds := m.Call(nil)[0]
		names := make([]string, fds.Len())
		for i := range names {
			names[i] = fds.Index(i).FieldByName("Name").String()
		}
		return names, nil
	}
	return nil, fmt.Errorf("the driver's rows (%T) do not name their columns", r)
}

// prepareRaw is prepare's work for raw SQL, which is the caller's: there is
// nothing to resolve, guard, rewrite, scope or render. It sets what the
// Around hooks read (observe), gives them a copy of the arguments to change,
// sets the fingerprint, of the caller's SQL, and appends the SQL comment,
// after trimming trailing semicolons and white space: a comment after a
// statement's terminator would make a second, empty statement, which MySQL
// without multiStatements refuses. When the last line holds "--", the comment
// goes on a line of its own, so that a line comment does not swallow it.
func (db *DB) prepareRaw(ctx context.Context, st *Statement) {
	db.observe(ctx, st)
	if len(db.Cfg.Arounds) > 0 {
		st.Args = slices.Clone(st.Args)
	}
	st.Fingerprint = Fingerprint(st.SQL)
	if db.Cfg.Comment != nil {
		q := strings.TrimRight(st.SQL, "; \t\r\n")
		if strings.Contains(q[strings.LastIndexByte(q, '\n')+1:], "--") {
			q += "\n"
		}
		st.SQL = q + db.Cfg.Comment.Text(st)
	}
}

// refuseTxEnd is the error of raw SQL that, run in a transaction, would end it: its
// first keyword, after comments and empty statements, is COMMIT, END, ROLLBACK (but
// not ROLLBACK TO), ABORT, BEGIN, START or PREPARE TRANSACTION, or, as MySQL commits
// before and after it, DDL other than a TEMPORARY table's. Tx would go on as if the
// transaction were open: its later statements commit alone, and its error rolls
// nothing back. Only the first statement of the text is read, as a CREATE TRIGGER's
// or a CREATE FUNCTION's body holds semicolons: see noteRawEnd for SQLite, which runs
// the rest.
func refuseTxEnd(d Dialect, st *Statement) error {
	kw, rest := sqlWord(d, st.SQL)
	switch strings.ToUpper(kw) {
	case "COMMIT", "END", "ABORT", "BEGIN", "START":
	case "ROLLBACK":
		next, more := sqlWord(d, rest)
		if strings.EqualFold(next, "WORK") || strings.EqualFold(next, "TRANSACTION") {
			next, _ = sqlWord(d, more)
		}
		if strings.EqualFold(next, "TO") {
			return nil
		}
	case "PREPARE":
		if next, _ := sqlWord(d, rest); !strings.EqualFold(next, "TRANSACTION") {
			return nil
		}
	case "CREATE", "DROP":
		if next, _ := sqlWord(d, rest); d != MySQL || strings.EqualFold(next, "TEMPORARY") {
			return nil
		}
		return fmt.Errorf("velox: %s: %w: MySQL commits before and after %s; run it outside the Tx", st.Entity.Name, ErrTxControl, strings.ToUpper(kw))
	case "ALTER", "TRUNCATE", "RENAME":
		if d != MySQL {
			return nil
		}
		return fmt.Errorf("velox: %s: %w: MySQL commits before and after %s; run it outside the Tx", st.Entity.Name, ErrTxControl, strings.ToUpper(kw))
	default:
		return nil
	}
	return fmt.Errorf("velox: %s: %w: %s; return from fn to end the Tx, or use a nested Tx to undo part of it", st.Entity.Name, ErrTxControl, strings.ToUpper(kw))
}

// sqlWord returns the first word of q after white space, comments and empty
// statements, and what follows it; "" when q starts with something else. MySQL runs
// what is in a /*! comment, so there the word is the comment's first.
func sqlWord(d Dialect, q string) (word, rest string) {
	for {
		q = strings.TrimLeft(q, " \t\r\n\f\v;")
		switch {
		case strings.HasPrefix(q, "--"), d == MySQL && strings.HasPrefix(q, "#"):
			_, q, _ = strings.Cut(q, "\n")
			continue
		case d == MySQL && (strings.HasPrefix(q, "/*!") || strings.HasPrefix(q, "/*M!")):
			q = strings.TrimLeft(q[strings.IndexByte(q, '!')+1:], "0123456789")
			continue
		case strings.HasPrefix(q, "/*"):
			q = skipComment(d, q)
			continue
		}
		n := 0
		for n < len(q) && (q[n] == '_' || 'a' <= q[n] && q[n] <= 'z' || 'A' <= q[n] && q[n] <= 'Z') {
			n++
		}
		return q[:n], q[n:]
	}
}

// skipComment returns q, which starts with a block comment, after it; "" when it does
// not end. Only PostgreSQL nests them.
func skipComment(d Dialect, q string) string {
	depth := 0
	for i := 0; i+1 < len(q); i++ {
		switch q[i : i+2] {
		case "/*":
			if depth == 0 || d == Postgres {
				depth++
				i++
			}
		case "*/":
			if depth--; depth == 0 {
				return q[i+2:]
			}
			i++
		}
	}
	return ""
}

// noteRawEnd breaks db's transaction when raw SQL that just succeeded has ended it.
// On SQLite the rest of a multi-statement text runs too, past refuseTxEnd, so velox
// asks as it does after a failed statement (sqliteRolledBack). On PostgreSQL, pgx
// and lib/pq run all of a text sent by the simple protocol, as one without
// arguments is, so velox asks after a text that holds more than one statement
// (postgresTxGone). The statement's work may be durable by then; its error and the
// Tx's say the transaction is gone. Text that ends the transaction and begins
// another cannot be told.
func (db *DB) noteRawEnd(ctx context.Context, st *Statement) error {
	if db.txRoot.broken != nil {
		return nil //nolint:nilerr // already broken: the Tx reports that, not this statement
	}
	gone := false
	switch db.D {
	case SQLite:
		gone = db.sqliteRolledBack(ctx)
	case Postgres:
		gone = db.postgresTxGone(ctx)
	}
	if !gone {
		return nil
	}
	db.txRoot.broken = fmt.Errorf("raw SQL ended the transaction, so it can only roll back: %w", ErrTxControl)
	return stmtErr(st, db.txRoot.broken)
}

// multiStatement reports whether q holds a semicolon before its trailing ones:
// more than one statement, or a semicolon in a literal, a comment or a function's
// body, for which the probe is wasted but harmless.
func multiStatement(q string) bool {
	return strings.Contains(strings.TrimRight(q, "; \t\r\n"), ";")
}

// postgresTxGone reports whether PostgreSQL's transaction has gone. A savepoint
// asks: outside a transaction block SAVEPOINT fails (25P01); inside one it is
// made and released. Either failing counts as gone: a failed SAVEPOINT aborts an
// open transaction too. Past the transaction's deadline nothing is sent.
func (db *DB) postgresTxGone(ctx context.Context) bool {
	if db.pastTxDeadline() {
		return false
	}
	pctx, cancel := db.rollbackContext(ctx, db.txDeadline)
	defer cancel()
	if _, err := db.Ex.Exec(pctx, "SAVEPOINT velox_open", nil); err != nil {
		return true
	}
	_, err := db.Ex.Exec(pctx, "RELEASE SAVEPOINT velox_open", nil)
	return err != nil
}

// RawExec is DB.Exec, its arguments given as a slice.
func (db *DB) RawExec(ctx context.Context, reason, query string, args []any) (int64, error) {
	db = db.In(ctx)
	if NoReason(reason) {
		return 0, ErrNoReason(execInfo.Name)
	}
	st := &Statement{Op: OpRawExec, Entity: &execInfo, SQL: query, Args: args, PolicyBypass: reason, InTx: db.InTx}
	var res execResult
	if err := db.Run(ctx, st, nil, &res); err != nil {
		return 0, err
	}
	return res.Affected, nil
}

// RawRows is DB.Rows: it runs query as Raw's Iter does and hands yield each
// row, inside the Around hooks. It reports whether yield stopped the loop,
// after which the caller must not call yield again.
func (db *DB) RawRows(ctx context.Context, reason, query string, args []any, yield func(Rows) bool) (stopped bool, err error) {
	db = db.In(ctx)
	if NoReason(reason) {
		return false, ErrNoReason(rowsInfo.Name)
	}
	st := &Statement{Op: OpRawQuery, Entity: &rowsInfo, SQL: query, Args: args, PolicyBypass: reason, InTx: db.InTx, iter: true}
	timed := len(db.Cfg.Arounds) > 0
	yielded := false
	err = db.Run(ctx, st, func(r Rows) error {
		if yielded || stopped {
			return ErrIterAgain
		}
		for r.Next() {
			yielded, st.handed = true, true
			start := time.Now()
			more := yield(r)
			if timed {
				st.Result.LoopBody += time.Since(start)
			}
			if !more {
				stopped = true
				return nil
			}
		}
		return nil
	}, nil)
	return stopped, err
}
