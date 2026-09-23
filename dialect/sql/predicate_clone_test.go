package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox/dialect"
)

// Every predicate closure must write through the builder it is handed, not
// the *Predicate it was created on. A cloned selector re-runs the closures
// against a fresh builder; writing to the captured predicate sends part of
// the clause into the original and renders `WHERE "a"$1` in the clone.
func TestPredicate_ClonedSelectorRendersEveryOperator(t *testing.T) {
	tests := []struct {
		name string
		pred *Predicate
		want string
	}{
		{"LT", LT("a", 1), `SELECT "a" FROM "t" WHERE "a" < $1`},
		{"LTE", LTE("a", 1), `SELECT "a" FROM "t" WHERE "a" <= $1`},
		{"GT", GT("a", 1), `SELECT "a" FROM "t" WHERE "a" > $1`},
		{"GTE", GTE("a", 1), `SELECT "a" FROM "t" WHERE "a" >= $1`},
		{"EQ", EQ("a", 1), `SELECT "a" FROM "t" WHERE "a" = $1`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Dialect(dialect.Postgres).Select("a").From(Table("t")).Where(tt.pred)
			q, args := s.Clone().Query()
			assert.Equal(t, tt.want, q)
			assert.Equal(t, []any{1}, args)
			// The original still renders after the clone ran.
			q, _ = s.Query()
			assert.Equal(t, tt.want, q)
		})
	}
}

// SQLite's ESCAPE clause is written from inside the LIKE closures; a clone
// must carry it, and running the clone must not leak it into the original.
func TestPredicate_ClonedSelectorKeepsLikeEscape(t *testing.T) {
	for name, pred := range map[string]*Predicate{
		"HasPrefix":     HasPrefix("a", "x_"),
		"ContainsFold":  ContainsFold("a", "x_"),
		"ColumnsPrefix": ColumnsHasPrefix("a", "b"),
	} {
		t.Run(name, func(t *testing.T) {
			s := Dialect(dialect.SQLite).Select("a").From(Table("t")).Where(pred)
			q, args := s.Clone().Query()
			assert.Contains(t, q, "ESCAPE ?")
			orig, origArgs := s.Query()
			assert.Equal(t, orig, q)
			assert.Equal(t, origArgs, args)
		})
	}
}
