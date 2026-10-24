package migrate

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/ddl"
)

// RenderOptions are what Render writes beside the statements.
type RenderOptions struct {
	LockTimeout time.Duration // 0: no lock-timeout directive in any script, so a PostgreSQL enum script then runs with none
	Allow       []Allow       // recorded as directives
}

// A ScriptKind says when a script runs.
type ScriptKind uint8

const (
	MainScript   ScriptKind = iota // in a transaction where the dialect has one
	EnumScript                     // before the main script, no transaction: PostgreSQL ADD VALUE
	OnlineScript                   // after the main script, no transaction: PostgreSQL CONCURRENTLY and VALIDATE
)

// String is "main", "enum" or "online".
func (k ScriptKind) String() string {
	switch k {
	case MainScript:
		return "main"
	case EnumScript:
		return "enum"
	case OnlineScript:
		return "online"
	}
	return "ScriptKind(" + strconv.Itoa(int(k)) + ")"
}

// A Script is the body of one migration file.
type Script struct {
	Kind       ScriptKind
	Directives Directives
	Statements []Statement
}

// A Statement is one SQL statement of a script. Change is the index of the
// change it renders. A statement that renders several carries the one at
// whose place it runs: the first of them in plan order for an ALTER TABLE
// that groups a table's changes; on MySQL, for the statement that adds and
// drops a table's indexes, its first add, or its first drop when it adds
// none; on SQLite, for each statement of a rebuild, the change at which the
// rebuild runs, its table's last. A PostgreSQL CREATE SCHEMA carries the
// first change that needs the schema.
type Statement struct {
	SQL    string // one statement, without its trailing semicolon
	Change int    // the index of the change it renders, or carries
	Unit   int    // statements with one Unit form a unit of a no-transaction script
	Review string // the review finding written above it, or ""
}

// Directives are the -- velox: lines at the top of a migration file.
type Directives struct {
	NoTransaction  bool          // refused on SQLite
	ForeignKeysOff bool          // SQLite
	LockTimeout    time.Duration // 0: the Config's, or off in a PostgreSQL no-transaction file
	LockTimeoutSet bool          // the file gave lock-timeout, 0 included
	Allow          []Allow
}

// Render writes the scripts that make changes, the output of Diff from from
// to to, in the order they run.
//
// On PostgreSQL it writes up to three scripts. Values added to an enum type
// go to the enum script, which runs first, outside a transaction, each
// ADD VALUE a unit. The main script runs in a transaction. The indexes,
// foreign keys and checks added to an existing table, its indexes dropped,
// and SET NOT NULL go to the online script, which runs last, outside a
// transaction and with no lock timeout, as units that can each run again
// from their first statement.
//
// On MySQL it writes one script, run statement by statement, each statement
// a unit of its own, with the lock timeout when opts.LockTimeout is set. On
// SQLite it writes one script, run in a transaction; a table SQLite cannot
// alter in place is rebuilt once, at its last change, and the script then
// turns foreign keys off.
//
// Each statement carries the review and destructive findings of its changes
// as its review line, and each destructive finding an Allow covers is
// recorded as an allow directive. A script with no statement is left out.
//
// Its errors are *Error values: either schema is nil ("nil"), the dialects
// differ or one is not PostgreSQL, MySQL or SQLite ("dialect"), or a change
// is nil, is not one Diff gives for the dialect, names what to lacks, is a
// PostgreSQL identity change that needs the name of a sequence from does
// not give, or renames a MySQL primary key, always named PRIMARY ("render"). Render does not change its arguments.
func Render(from, to *Schema, changes []Change, opts RenderOptions) ([]Script, error) {
	if from == nil || to == nil {
		return nil, &Error{Code: "nil", Reason: "Render needs two schemas"}
	}
	if from.Dialect != to.Dialect {
		return nil, &Error{Code: "dialect", Reason: fmt.Sprintf("from is a %s schema and to a %s one; Render takes two schemas of one dialect", from.Dialect, to.Dialect)}
	}
	r := &renderer{d: to.Dialect, from: from, to: to, cs: changes}
	if i := slices.Index(changes, nil); i >= 0 {
		return nil, r.refuse(i, "the change is nil")
	}
	l := lintChanges(from, to, changes)
	r.l, r.findings, r.taken = l, reviewed(l.out, len(changes)), make([]bool, len(changes))
	main := Script{Kind: MainScript}
	if opts.LockTimeout > 0 && r.d != velox.SQLite {
		main.Directives.LockTimeout, main.Directives.LockTimeoutSet = opts.LockTimeout, true
	}
	var err error
	switch r.d {
	case velox.Postgres:
		if err := r.postgres(); err != nil {
			return nil, err
		}
		return r.postgresScripts(main, opts), nil
	case velox.MySQL:
		err = r.mysql()
		for i := range r.out {
			r.out[i].Unit = i
		}
	case velox.SQLite:
		err = r.sqlite()
		main.Directives.ForeignKeysOff = r.fkOff
	default:
		return nil, &Error{Code: "dialect", Reason: fmt.Sprintf("Render has no dialect %s", r.d)}
	}
	if err != nil {
		return nil, err
	}
	if len(r.out) == 0 {
		return nil, nil
	}
	main.Statements = r.out
	main.Directives.Allow = allowed(l.out, opts.Allow)
	return []Script{main}, nil
}

// reviewed is the review and destructive findings of each of n changes,
// which their statements' review lines hold.
func reviewed(findings []Finding, n int) [][]Finding {
	out := make([][]Finding, n)
	for _, f := range findings {
		if (f.Class == Review || f.Class == Destructive) && f.Change >= 0 && f.Change < n {
			out[f.Change] = append(out[f.Change], f)
		}
	}
	return out
}

// oneLine keeps a review line on its comment line.
var oneLine = strings.NewReplacer("\r", " ", "\n", " ")

// allowed is the destructive findings allow covers, as the directives that
// record them, sorted and without repeats.
func allowed(findings []Finding, allow []Allow) []Allow {
	blocked := Blocked(findings, allow)
	var out []Allow
	for _, f := range findings {
		if f.Class == Destructive && !slices.Contains(blocked, f) {
			out = append(out, Allow{Code: f.Code, Target: f.Target})
		}
	}
	slices.SortFunc(out, func(a, b Allow) int { return cmp.Or(cmp.Compare(a.Code, b.Code), cmp.Compare(a.Target, b.Target)) })
	return slices.Compact(out)
}

// renderer holds the state of one Render.
type renderer struct {
	d        velox.Dialect
	from, to *Schema
	cs       []Change
	l        *linter
	findings [][]Finding // each change's review and destructive findings
	taken    []bool      // the change's first statement has its review line
	out      []Statement
	fkOff    bool // SQLite: the script needs foreign keys off

	enum, online []Statement // PostgreSQL: the enum and online scripts'
}

// emit adds a statement that renders the changes of the indexes in changes
// and carries the first; its review line is theirs.
func (r *renderer) emit(sql string, changes ...int) { r.emitAt(sql, changes[0], changes) }

// emitAt adds a statement that carries change at, with the review line of
// the changes of the indexes in of.
func (r *renderer) emitAt(sql string, at int, of []int) {
	r.out = append(r.out, Statement{SQL: sql, Change: at, Review: r.review(of, nil)})
}

// review is the review line of the changes of the indexes in of: each
// distinct "<code>: <message>" of their findings once, in order. A change's
// findings go on its first statement only. For a SQLite rebuild of rebuilt,
// the changes' sqlite-rebuild findings are one, saying what each does.
func (r *renderer) review(of []int, rebuilt *TableRef) string {
	var entries, whats []string
	seen := map[string]bool{}
	at := -1 // the place of the rebuild's entry
	for _, i := range of {
		if r.taken[i] {
			continue
		}
		r.taken[i] = true
		for _, f := range r.findings[i] {
			if rebuilt != nil && f.Code == "sqlite-rebuild" {
				if w := r.l.rebuildWhat[i]; !slices.Contains(whats, w) {
					whats = append(whats, w)
				}
				if at < 0 {
					at = len(entries)
					entries = append(entries, "")
				}
				continue
			}
			if e := f.Code + ": " + f.Message; !seen[e] {
				seen[e] = true
				entries = append(entries, e)
			}
		}
	}
	if at >= 0 {
		entries[at] = "sqlite-rebuild: " + rebuildMessage(strings.Join(whats, ", "), *rebuilt)
	}
	return oneLine.Replace(strings.Join(entries, "; "))
}

// refuse is the error for change i, which Diff does not give on the dialect.
func (r *renderer) refuse(i int, why string) error {
	c := r.cs[i]
	obj := ""
	if c != nil {
		obj = c.Target().String()
	}
	return &Error{Object: obj, Code: "render", Reason: fmt.Sprintf("change %d: %s; Render takes the changes Diff gives", i, why)}
}

// toTable is the table ref of to, or an error for change i.
func (r *renderer) toTable(i int, ref TableRef) (*Table, error) {
	if t := r.to.table(ref); t != nil {
		return t, nil
	}
	return nil, r.refuse(i, fmt.Sprintf("to has no table %v", ref))
}

// ident is name quoted for the dialect.
func (r *renderer) ident(name string) string { return ddl.Ident(r.d, name) }

// table is ref quoted, with its schema when it has one.
func (r *renderer) table(ref TableRef) string {
	if ref.Schema == "" {
		return r.ident(ref.Name)
	}
	return r.ident(ref.Schema) + "." + r.ident(ref.Name)
}

// idents is names quoted, joined by ", ".
func (r *renderer) idents(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = r.ident(n)
	}
	return strings.Join(q, ", ")
}

// foreignKey is fk's clause after its name: FOREIGN KEY (…) REFERENCES ….
// SQLite names the referenced table without a schema.
func (r *renderer) foreignKey(fk ForeignKey) string {
	ref := r.table(fk.RefTable)
	if r.d == velox.SQLite {
		ref = r.ident(fk.RefTable.Name)
	}
	s := fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s (%s)", r.idents(fk.Columns), ref, r.idents(fk.RefColumns))
	if fk.OnDelete != "" {
		s += " ON DELETE " + fk.OnDelete
	}
	return s
}

// writeScript writes s as the body of its file: the directives (no-transaction,
// foreign-keys off, lock-timeout <d>, then each allow <code> <target>, sorted),
// a blank line, then each statement: a "-- velox:unit" line before a unit's
// first statement in a no-transaction script, a "-- review: <text>" line when
// it has one, and the SQL with ";". Plan 2's directory writes files with it.
func writeScript(b *strings.Builder, s Script) {
	d := s.Directives
	if d.NoTransaction {
		b.WriteString("-- velox:no-transaction\n")
	}
	if d.ForeignKeysOff {
		b.WriteString("-- velox:foreign-keys off\n")
	}
	if d.LockTimeoutSet {
		b.WriteString("-- velox:lock-timeout " + d.LockTimeout.String() + "\n")
	}
	allow := slices.Clone(d.Allow)
	slices.SortFunc(allow, func(a, b Allow) int { return cmp.Or(cmp.Compare(a.Code, b.Code), cmp.Compare(a.Target, b.Target)) })
	for _, a := range allow {
		b.WriteString("-- velox:allow " + a.Code)
		if a.Target != "" {
			b.WriteString(" " + a.Target)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	for i, st := range s.Statements {
		if d.NoTransaction && (i == 0 || st.Unit != s.Statements[i-1].Unit) {
			b.WriteString("-- velox:unit\n")
		}
		if st.Review != "" {
			b.WriteString("-- review: " + st.Review + "\n")
		}
		b.WriteString(st.SQL + ";\n")
	}
}
