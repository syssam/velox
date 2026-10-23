// Package migrate models a database's schema as a value, so that a program
// can compare two of them and read or write one as JSON.
//
// A [Schema] is the database as one dialect sees it: every name is the
// database's name, and every type is spelled as the dialect spells it. The
// model is plain data. Nothing in this package opens a connection, and the
// functions that take a *Schema never write through it.
package migrate
