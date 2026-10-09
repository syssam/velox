package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox/dialect"
)

// TestIdentWritesWhatQuoteReturns pins that Ident, which writes the quoted
// identifier straight into the buffer, renders exactly what Quote returns.
func TestIdentWritesWhatQuoteReturns(t *testing.T) {
	idents := []string{"users", "user_id", "weird name", "has`tick", `has"quote`, "`already`", `"already"`}
	for _, d := range []string{"", dialect.MySQL, dialect.SQLite, dialect.Postgres} {
		for _, id := range idents {
			for _, qualifier := range []string{"", "t1"} {
				b := &Builder{dialect: d, qualifier: qualifier}
				if b.isIdent(id) {
					continue // Ident passes an already-quoted identifier through.
				}
				want := b.Quote(id)
				if qualifier != "" {
					want = b.Quote(qualifier) + "." + want
				}
				assert.Equal(t, want, b.Ident(id).String(), "dialect=%q ident=%q qualifier=%q", d, id, qualifier)
			}
		}
	}
}

// TestSelectTableC_QualifiesAndQuotes pins the column formatting on every
// dialect, with and without an alias and a schema.
func TestSelectTableC_QualifiesAndQuotes(t *testing.T) {
	assert.Equal(t, "`users`.`name`", Dialect(dialect.MySQL).Table("users").C("name"))
	assert.Equal(t, `"users"."name"`, Dialect(dialect.Postgres).Table("users").C("name"))
	assert.Equal(t, `"u"."name"`, Dialect(dialect.Postgres).Table("users").As("u").C("name"))
	assert.Equal(t, `"app"."users"."name"`, Dialect(dialect.Postgres).Table("users").Schema("app").C("name"))
	assert.Equal(t, "", Dialect(dialect.Postgres).Table("users").C(""))
}
