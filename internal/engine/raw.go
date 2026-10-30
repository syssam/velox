package engine

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// ExecInfo is the EntityInfo of Exec's statements: the name their errors and
// the Around hooks see. Exec scans nothing, so it has no table or columns.
var ExecInfo = EntityInfo{Name: "Exec", Tenant: -1}

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
	if c, ok := r.(*CountedRows); ok {
		r = c.Rows
	}
	switch r := r.(type) {
	case interface{ Columns() ([]string, error) }:
		return r.Columns()
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
