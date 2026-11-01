package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// execInfo is the EntityInfo of Exec's statements: the name their errors and
// the Around hooks see. Exec scans nothing, so it has no table or columns.
var execInfo = EntityInfo{Name: "Exec", Tenant: -1}

// ErrNoReason is the error of raw SQL run without a reason.
func ErrNoReason(name string) error {
	return fmt.Errorf("velox: %s: raw SQL needs a reason, as it applies neither the tenant scope nor the policy", name)
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

// RawExec is DB.Exec, its arguments given as a slice.
func (db *DB) RawExec(ctx context.Context, reason, query string, args []any) (int64, error) {
	if NoReason(reason) {
		return 0, ErrNoReason(execInfo.Name)
	}
	st := &Statement{Op: OpRawExec, Entity: &execInfo, SQL: query, Args: args, TenantBypass: reason, PolicyBypass: reason, InTx: db.InTx}
	var res execResult
	if err := db.Run(ctx, st, nil, &res); err != nil {
		return 0, err
	}
	return res.Affected, nil
}
