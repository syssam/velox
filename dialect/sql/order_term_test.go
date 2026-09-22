package sql

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/syssam/velox/dialect"
)

// The OrderTerm API (OrderByField/Sum/Count/Rand plus the OrderTermOptions)
// was exported with zero test coverage — every function in the block measured
// 0.0%. These pin the SQL each one renders, per dialect.
func TestOrderTerms_RenderedSQL(t *testing.T) {
	sel := func(d string) *Selector { return Dialect(d).Select("*").From(Table("users")) }

	t.Run("field with direction and nulls placement", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			opts []OrderTermOption
			want string
		}{
			{"plain", nil, "ORDER BY `users`.`name`"},
			{"desc", []OrderTermOption{OrderDesc()}, "ORDER BY `users`.`name` DESC"},
			{"asc", []OrderTermOption{OrderAsc()}, "ORDER BY `users`.`name`"},
			{"nulls first", []OrderTermOption{OrderNullsFirst()}, "ORDER BY `users`.`name` NULLS FIRST"},
			{"nulls last", []OrderTermOption{OrderNullsLast()}, "ORDER BY `users`.`name` NULLS LAST"},
			{"desc nulls last", []OrderTermOption{OrderDesc(), OrderNullsLast()}, "ORDER BY `users`.`name` DESC NULLS LAST"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s := sel(dialect.SQLite)
				OrderByField("name", tc.opts...).ToFunc()(s)
				q, _ := s.Query()
				assert.Contains(t, q, tc.want)
			})
		}
	})

	t.Run("nulls placement is NOT dialect-guarded", func(t *testing.T) {
		// Ent renders NULLS FIRST/LAST for every dialect with no guard
		// (dialect/sql/sql.go OrderFieldTerm.ToFunc), and velox matches it.
		// MySQL has no such syntax and answers with a 1064 syntax error, so a
		// caller targeting MySQL must order by `col IS NULL` instead. This is
		// the same shape as FOR SHARE on MySQL 5.x: the clause is rendered from
		// the dialect alone and the caller opts out. Pinned so that if velox
		// ever adds a capability guard, this test is the one that has to change
		// deliberately rather than the behavior drifting unnoticed.
		s := sel(dialect.MySQL)
		OrderByField("name", OrderNullsLast()).ToFunc()(s)
		q, _ := s.Query()
		assert.Contains(t, q, "NULLS LAST", "matches Ent; MySQL rejects this at execution time")
	})

	t.Run("OrderByRand is the one dialect-aware term", func(t *testing.T) {
		for d, want := range map[string]string{
			dialect.MySQL:    "RAND()",
			dialect.Postgres: "RANDOM()",
			dialect.SQLite:   "RANDOM()",
		} {
			s := sel(d)
			OrderByRand()(s)
			q, _ := s.Query()
			assert.Contains(t, q, "ORDER BY "+want, "dialect %s", d)
		}
	})

	t.Run("aggregate terms default their alias to func_field", func(t *testing.T) {
		assert.Equal(t, "sum_age", OrderBySum("age").As)
		assert.Equal(t, "count_age", OrderByCount("age").As)
		assert.Equal(t, "total", OrderBySum("age", OrderAs("total")).As)
	})

	t.Run("aggregate terms render their expression", func(t *testing.T) {
		s := sel(dialect.SQLite)
		for _, tc := range []struct {
			name string
			term *OrderExprTerm
			want string
		}{
			{"column is qualified", OrderBySum("age"), "SUM(`users`.`age`)"},
			{"count star is passed through", OrderByCount("*"), "COUNT(*)"},
			// isFunc(): anything already containing parentheses is treated as
			// an expression and must not be qualified with the table again.
			{"nested func is not requalified", OrderByCount("DISTINCT(age)"), "COUNT(DISTINCT(age))"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q, _ := tc.term.Expr(s).Query()
				assert.Equal(t, tc.want, q)
			})
		}
	})

	t.Run("OrderSelected and OrderSelectAs set the selection flags", func(t *testing.T) {
		o := NewOrderTermOptions(OrderSelected())
		assert.True(t, o.Selected)
		o = NewOrderTermOptions(OrderSelectAs("alias"))
		assert.True(t, o.Selected)
		assert.Equal(t, "alias", o.As)
	})
}
