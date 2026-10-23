// Package migrate models a database's schema as a value, so that a program
// can compare two of them, read or write one as JSON, and plan the SQL that
// turns one into the other.
//
// A [Schema] is the database as one dialect sees it: every name is the
// database's name, and every type is spelled as the dialect spells it. The
// model is plain data. Nothing in this package opens a connection, and the
// functions that take a *Schema never write through it.
//
// Planning has four parts. [Diff] lists the changes from one schema to
// another, in the order they run. [Lint] says which of them need care: a
// review, or an allowance for a destructive change. [Blocked] is the
// destructive findings no [Allow] covers. [Render] writes the changes as
// scripts: on PostgreSQL, values added to enum types in a script of their
// own, the main script, and an online script of units for what must not
// hold a lock for long. [NewPlan] runs the four in one call, and refuses a
// plan with a destructive change not allowed with a [*BlockedError].
package migrate
