// Package ddl holds what the generator and the migration planner must agree
// on byte for byte: the cap on database names, the names of the helpers the
// planner makes, the canonical spelling of column types, and the spellings
// of enums: a PostgreSQL enum column's type, MySQL's enum(…) and the check
// that holds a string column to the values.
package ddl
