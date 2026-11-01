package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
)

// This file checks a write's foreign keys against its tenant. The tenant scope
// reads and writes rows of the tenant, but a foreign key names a row by its key
// alone: under tenant A, an Insert whose ProjectID is B's would succeed, B could
// then not delete its own project, and success or failure would tell A which of
// B's IDs exist. So a write under a tenant that sets a TenantRef's column first
// asks whether those rows are the tenant's.
//
// A TenantRef holds plain strings, so that a relation's metadata names no other
// entity's: see EdgeTo.

// TenantRef says that column Column, a foreign key, refers to Key of Schema.Table,
// which is tenant-scoped by its column Tenant. velox-gen writes one for such a
// column of a tenant-scoped entity, and for each side of a many-to-many edge's
// join entity whose target is tenant-scoped.
type TenantRef struct {
	Column int
	Schema string // "" for the default
	Table  string
	Key    string
	Tenant string
}

// tenantRef is a TenantRef as EntityInfo holds it; self is the column of the
// entity itself that Key names, on a reference to its own table, and -1 otherwise.
type tenantRef struct {
	TenantRef
	self int
}

// buildTenantRefs is d's TenantRefs for the metadata, refusing what names no
// column of its own.
func buildTenantRefs(name string, d *DescFields, cols []ColumnInfo) ([]tenantRef, error) {
	if len(d.TenantRefs) == 0 {
		return nil, nil
	}
	refs := make([]tenantRef, len(d.TenantRefs))
	for i, r := range d.TenantRefs {
		if r.Column < 0 || r.Column >= len(cols) {
			return nil, &SchemaError{Entity: name, Reason: fmt.Sprintf("a TenantRef names column %d, of %d", r.Column, len(cols))}
		}
		if r.Table == "" || r.Key == "" || r.Tenant == "" {
			return nil, &SchemaError{Entity: name, Field: cols[r.Column].Field, Reason: "a TenantRef needs a Table, a Key and a Tenant; run go tool velox-gen"}
		}
		refs[i] = tenantRef{TenantRef: r, self: -1}
		if r.Table == d.Table && r.Schema == d.Schema {
			refs[i].self = slices.IndexFunc(cols, func(c ColumnInfo) bool { return c.Name == r.Key })
		}
	}
	return refs, nil
}

// scopeRefs is the scope's part in a write of an entity with TenantRefs. It leaves
// in st.Tenant the tenant its references are checked against, nil for none. A
// join entity has no tenant of its own for the scope to read: its write needs what
// its targets' statements need, a tenant or WithAllTenants. An Add or Sub on a
// reference is refused under a tenant, as the value it makes cannot be checked.
func scopeRefs(ctx context.Context, st *Statement) error {
	in := st.Entity
	if in.Tenant < 0 {
		id, all := TenantFrom(ctx)
		switch {
		case all != "":
			st.TenantBypass = all
			return nil
		case id == nil:
			return fmt.Errorf("velox: %s: %w", in.Name, ErrNoTenant)
		case reflect.ValueOf(id).IsZero():
			return fmt.Errorf("velox: %s: %w: the tenant is the zero value", in.Name, ErrNoTenant)
		}
		st.Tenant = id
	}
	if st.Tenant == nil {
		return nil
	}
	for _, a := range st.Set {
		if a.Op != AssignSet && slices.ContainsFunc(in.refs, func(r tenantRef) bool { return r.Column == a.Column }) {
			return fmt.Errorf("velox: %s: %w: the statement does arithmetic on %s, a foreign key under a tenant", in.Name, ErrTenantRef, in.Columns[a.Column].Name)
		}
	}
	return nil
}

// checkRefs sends, for each TenantRef whose column st, an insert or update under a
// tenant, sets to a value, one SELECT for its distinct values among the tenant's rows,
// where the write goes: in its transaction, or on the connection it was routed to,
// never a replica. It runs last before the write, so it reads what the hooks left. A
// value that names no row of the tenant fails the write unsent, with one error
// whether the row does not exist or is another tenant's. A deleted target after this
// is the database's foreign key's to refuse.
func (db *DB) checkRefs(ctx context.Context, st *Statement, t Target) error {
	in := st.Entity
	if len(in.refs) == 0 || st.Tenant == nil || st.Op != OpInsert && st.Op != OpUpdate {
		return nil
	}
	if err := checkParams(db.D, st); err != nil {
		return err
	}
	for i := range in.refs {
		r := &in.refs[i]
		vals := refValues(st, r)
		if len(vals) == 0 {
			continue
		}
		w := writer{d: db.D, args: make([]any, 0, len(vals)+1)}
		w.str("SELECT ")
		w.ident(r.Key)
		w.str(" FROM ")
		if r.Schema != "" {
			w.ident(r.Schema)
			w.str(".")
		}
		w.ident(r.Table)
		w.str(" WHERE ")
		w.ident(r.Key)
		w.str(" IN (")
		for j, v := range vals {
			if j > 0 {
				w.str(", ")
			}
			w.arg(v)
		}
		w.str(") AND ")
		w.ident(r.Tenant)
		w.str(" = ")
		w.arg(st.Tenant)
		rows, err := t.Ex.Query(ctx, w.b.String(), w.args)
		if err != nil {
			return dbErr(in, err)
		}
		found := 0
		for rows.Next() {
			found++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return dbErr(in, err)
		}
		if found < len(vals) {
			return &ConstraintError{Entity: in.Name, Kind: ConstraintForeignKey, Constraint: in.Columns[r.Column].Name, Err: ErrTenantRef}
		}
	}
	return nil
}

// refValues is what st sets r's column to, without NULL, each value once. An insert's
// reference to a row it inserts itself is the database's to check, within the
// statement: those rows are not there yet, and are the tenant's (scopeInsert).
func refValues(st *Statement, r *tenantRef) []any {
	if st.Op == OpUpdate {
		for _, a := range st.Set {
			if a.Column == r.Column && a.Op == AssignSet {
				if v, ok := refValue(a.Value); ok {
					return []any{v}
				}
			}
		}
		return nil
	}
	at := slices.Index(st.Columns, r.Column)
	if at < 0 {
		return nil
	}
	self := -1
	if r.self >= 0 {
		self = slices.Index(st.Columns, r.self)
	}
	if len(st.Rows) == 1 && self < 0 {
		if v, ok := refValue(st.Rows[0][at]); ok {
			return []any{v}
		}
		return nil
	}
	seen := make(map[any]bool, len(st.Rows))
	if self >= 0 {
		for _, row := range st.Rows {
			if v, ok := refValue(row[self]); ok {
				seen[distinctKey(v)] = true
			}
		}
	}
	vals := make([]any, 0, len(st.Rows))
	for _, row := range st.Rows {
		v, ok := refValue(row[at])
		if !ok {
			continue
		}
		if k := distinctKey(v); !seen[k] {
			seen[k] = true
			vals = append(vals, v)
		}
	}
	return vals
}

// refValue is v, a value written to a foreign key, without its pointer; false when
// it binds NULL.
func refValue(v any) (any, bool) {
	k, _ := keyOf(v)
	if k == nil {
		return nil, false
	}
	if rv := reflect.ValueOf(k); rv.Kind() == reflect.Slice && rv.IsNil() {
		return nil, false
	}
	return k, true
}

// distinctKey is v as a map key: a byte slice's bytes, or the text of what == would
// panic on.
func distinctKey(v any) any {
	rv := reflect.ValueOf(v)
	switch {
	case rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8:
		return string(rv.Bytes())
	case !rv.Comparable():
		return fmt.Sprint(v)
	}
	return v
}
