package migrate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// An Error is a refusal of this package.
type Error struct {
	Pos    string // the schema's file:line, when known
	Object string // the table, column, index or constraint it is about
	Code   string // names the failure for programs; the package doc lists the codes
	Reason string

	cause error // the driver's error behind a refusal, when there is one
}

// Unwrap is the driver's error behind the refusal, or nil.
func (e *Error) Unwrap() error { return e.cause }

// Error is "migrate: " and the non-empty of Pos, Object and Reason, joined
// by ": ".
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("migrate: ")
	sep := ""
	for _, s := range [...]string{e.Pos, e.Object, e.Reason} {
		if s != "" {
			b.WriteString(sep)
			b.WriteString(s)
			sep = ": "
		}
	}
	return b.String()
}

// A BlockedError is NewPlan's refusal of a plan with destructive changes no
// Allow covers.
type BlockedError struct{ Findings []Finding }

// Error is "migrate: <n> destructive change(s) refused:", then one line per
// finding: "  [<pos>: ]<target>: <code>: <message>; allow with -allow <code>=<target>".
func (e *BlockedError) Error() string {
	var b strings.Builder
	b.WriteString("migrate: ")
	b.WriteString(strconv.Itoa(len(e.Findings)))
	if len(e.Findings) == 1 {
		b.WriteString(" destructive change refused:")
	} else {
		b.WriteString(" destructive changes refused:")
	}
	for _, f := range e.Findings {
		b.WriteString("\n  ")
		if f.Pos != "" {
			b.WriteString(f.Pos + ": ")
		}
		b.WriteString(f.Target + ": " + f.Code + ": " + f.Message + "; allow with -allow " + f.Code + "=" + f.Target)
	}
	return b.String()
}

// A SumError is the refusal of a directory whose velox.sum is missing or does
// not match its files.
type SumError struct{ Path string } // "velox.sum", as fsys names it

// Error is "migrate: <Path> is out of date; run velox-gen migrate hash if the
// edits are intended".
func (e *SumError) Error() string {
	return "migrate: " + e.Path + " is out of date; run velox-gen migrate hash if the edits are intended"
}

// A ChecksumError is the refusal of an applied version whose file was edited
// or is gone.
type ChecksumError struct {
	Version      uint64
	Path         string
	Applied, Now string // Now is "" when the file is missing

	dirty bool   // the version is dirty: WithResume found no file to resume
	was   string // the row's name, when it is not the file's: two files took one number
}

// Error is "migrate: <Path> was applied with checksum <Applied> and is now
// <Now>", or, when Now is "", "migrate: version <Version> is applied but the
// directory has no file for it". Each goes on to the ways out: migrate resolve
// -adopt and -forget. For a dirty version to resume whose file is missing it says the
// version is dirty, and how to go on.
func (e *ChecksumError) Error() string {
	if e.Now == "" && e.dirty {
		return fmt.Sprintf("migrate: version %d is dirty, and the directory has no file for it; resume it with the directory that has its file, or %s", e.Version, undoHint(e.Version))
	}
	if e.Now == "" {
		return fmt.Sprintf("migrate: version %d is applied but the directory has no file for it\n  restore the file; or, if another branch's history moved it or it was reverted by hand, run migrate resolve -version %d -forget", e.Version, e.Version)
	}
	head := "migrate: " + e.Path + " was applied with checksum " + e.Applied + " and is now " + e.Now
	if e.was != "" {
		head = fmt.Sprintf("migrate: version %d was applied as %s, with checksum %s, and the directory's file for it is %s, with checksum %s", e.Version, e.was, e.Applied, e.Path, e.Now)
	}
	return head + fmt.Sprintf("\n  restore the file; or, if the database holds what this file does, run migrate resolve -version %d -adopt; or, once what the old file did is undone by hand, run migrate resolve -version %d -forget", e.Version, e.Version)
}

// A DirtyError is a version left part-way: by a file that failed now, at
// Statement of Statements, or found dirty in the history, with Statement 0.
type DirtyError struct {
	Version               uint64
	Path                  string
	Statement, Statements int
	Err                   error

	applied *int   // the statements still applied, when the runner rolled back the file's own transaction
	missing bool   // found dirty, and the directory has no file for it
	retry   bool   // a lock kept the next statement from running: Up goes on unasked
	guide   string // MySQL: what ran, and how to tell the history; with unsure, the file's statements
	unsure  bool   // the run stopped with a statement under way, which may have run after the count
	counted int    // with unsure, the statements the history counts
	pg      bool   // with unsure, the database is PostgreSQL, else MySQL
}

// undoHint is the way out of dirty version v by undoing it.
func undoHint(v uint64) string {
	return fmt.Sprintf("undo its applied statements and run migrate resolve -version %d -undone", v)
}

// Error is, when Statement > 0, "migrate: <Path>: statement <Statement> of
// <Statements>: <Err>", then "\n  version <Version> is dirty; fix the cause
// and run migrate apply -resume, or " and, when statements ran before it,
// "undo statement 1 and " or "undo statements 1 to <Statement-1> and "
// (when the runner rolled back a transaction the file opened, the
// statements still applied instead of Statement-1), then "run migrate
// resolve -version <Version> -undone". For a version found dirty
// (Statement 0) it is "migrate: <Path>: version <Version> is dirty: <Err>",
// then "\n  fix the cause and run migrate apply -resume, or undo its
// applied statements and run migrate resolve -version <Version> -undone". A
// version found dirty whose file the directory lacks is "migrate: version
// <Version> is dirty: <Err>", then a line that says the file is missing and
// to resume with the directory that has it, or undo and resolve. On MySQL
// lines follow with the file's statements, and how to say which ran; when a
// lock kept the next statement from running, a line says apply retries it.
//
// When the run stopped with a statement under way, which the server may have
// finished after the history's count (the run was killed, lost its connection
// or was cancelled, and the statement does not commit with its count), the way
// out leads instead with checking whether it took effect, then says how to tell
// the history what ran (resolve -statements), and on MySQL lists the statements.
func (e *DirtyError) Error() string {
	var b strings.Builder
	cause := ""
	if e.Err != nil {
		cause = ": " + e.Err.Error()
	}
	if e.Statement == 0 && e.missing {
		fmt.Fprintf(&b, "migrate: version %d is dirty%s\n  the directory has no file for it; resume it with the directory that has its file, or %s",
			e.Version, cause, undoHint(e.Version))
		return b.String()
	}
	if e.unsure {
		under := e.Statement
		if under == 0 {
			fmt.Fprintf(&b, "migrate: %s: version %d is dirty%s", e.Path, e.Version, cause)
			under = e.counted + 1
		} else {
			fmt.Fprintf(&b, "migrate: %s: statement %d of %d%s", e.Path, e.Statement, e.Statements, cause)
		}
		tool, schema := "SHOW FULL PROCESSLIST", "SHOW CREATE TABLE"
		if e.pg {
			tool, schema = "pg_stat_activity", `\d of the table`
		}
		fmt.Fprintf(&b, "\n  version %d is dirty, and the run stopped with statement %d under way: it may have run on the server after the history's count of %d, or be running still. "+
			"Wait until %s no longer shows it, then check whether it took effect: %s for a schema change, its rows for an INSERT, UPDATE or DELETE",
			e.Version, under, e.counted, tool, schema)
		fmt.Fprintf(&b, "\n  if the first J statements ran, J above %d, run migrate resolve -version %d -statements J, then migrate apply -resume; "+
			"if none past %d did, fix the cause and run migrate apply -resume; or undo the statements that ran and run migrate resolve -version %d -undone",
			e.counted, e.Version, e.counted, e.Version)
		b.WriteString(e.guide)
		return b.String()
	}
	if e.Statement == 0 {
		fmt.Fprintf(&b, "migrate: %s: version %d is dirty%s\n  fix the cause and run migrate apply -resume, or %s",
			e.Path, e.Version, cause, undoHint(e.Version))
		b.WriteString(e.tail())
		return b.String()
	}
	fmt.Fprintf(&b, "migrate: %s: statement %d of %d%s\n  version %d is dirty; fix the cause and run migrate apply -resume, or ",
		e.Path, e.Statement, e.Statements, cause, e.Version)
	k := e.Statement - 1
	if e.applied != nil {
		k = *e.applied
	}
	switch {
	case k == 1:
		b.WriteString("undo statement 1 and ")
	case k > 1:
		fmt.Fprintf(&b, "undo statements 1 to %d and ", k)
	}
	fmt.Fprintf(&b, "run migrate resolve -version %d -undone", e.Version)
	b.WriteString(e.tail())
	return b.String()
}

// tail is what Error says after the way out.
func (e *DirtyError) tail() string {
	if e.retry {
		return "\n  a lock kept the statement from running: migrate apply retries it, with no -resume"
	}
	return e.guide
}

// Unwrap is Err.
func (e *DirtyError) Unwrap() error { return e.Err }

// ErrLockWait is the runner's refusal when the migration lock stays taken
// for the whole lock wait.
var ErrLockWait = errors.New("migrate: timed out waiting for the migration lock")
