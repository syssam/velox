package orm

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/syssam/velox"
)

// This package defines no error of its own. The ones an application tests
// after a query or a mutation are package velox's, whichever runtime ran it:
// velox.ErrNotFound, velox.ErrNotSingular, velox.ErrNoEntity,
// *velox.ValidationError, *velox.ConstraintError with velox.ErrUnique and its
// other kinds, *velox.NotLoadedError, and velox.IsNotFound and the rest for
// each.

// GeneratedCodeIsVersion1 is what a generated tree asserts at compile time:
//
//	const _ = orm.GeneratedCodeIsVersion1
//
// A tree written by a generator older or newer than this package then fails
// to compile on that line, which says what to do (generate the code again
// with the velox-gen of this module), where it would fail on whatever the
// two disagree about. The number goes up, and the old constant goes away,
// whenever this package changes what generated code calls.
const GeneratedCodeIsVersion1 = true

// NotFound returns the error of a query or mutation of the entity that
// matched no row: a *velox.NotFoundError, which is velox.ErrNotFound and
// reads "velox: sales_order not found".
func (m *Meta) NotFound() error { return velox.NewNotFoundError(m.label()) }

// notSingular is the error of a read or write of one row that matched more.
func (m *Meta) notSingular() error { return velox.NewNotSingularError(m.label()) }

// invalid is the error of a value of a field or an edge refused before it
// reached the database.
func (m *Meta) invalid(name string, err error) error {
	return &velox.ValidationError{Entity: m.Name, Field: name, Name: name, Err: err}
}

// label is the entity as an error names it.
func (m *Meta) label() string { return snake(m.Name) }

// snake writes a name of the schema in lower case with underscores:
// SalesOrder is sales_order, HTTPLog is http_log.
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			prev, _ := utf8.DecodeLastRuneInString(s[:i])
			next, size := utf8.DecodeRuneInString(s[i+utf8.RuneLen(r):])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && size > 0 && unicode.IsLower(next)) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// Apply calls each function on q and returns q: for the parts of a query
// that were collected before the query was built.
//
//	var parts []func(*entity.UserQuery)
//	if adultsOnly {
//		parts = append(parts, func(q *entity.UserQuery) { q.Where(user.Age.GTE(18)) })
//	}
//	us, err := orm.Apply(db.User.Query(conn), parts...).All(ctx)
func Apply[Q any](q Q, fns ...func(Q)) Q {
	for _, fn := range fns {
		if fn != nil {
			fn(q)
		}
	}
	return q
}
