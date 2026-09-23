package sql

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
)

func TestSelector_LimitPerPartition(t *testing.T) {
	t.Run("postgres O2M", func(t *testing.T) {
		d := Dialect(dialect.Postgres)
		posts := d.Table("posts")
		s := d.Select(posts.C("id"), posts.C("title"), posts.C("user_posts")).
			From(posts).
			Where(In(posts.C("user_posts"), 1, 2)).
			OrderBy(Desc(posts.C("id")))
		s.LimitPerPartition(s.C("user_posts"), 3)
		query, args := s.Query()
		require.Equal(t,
			`SELECT "id", "title", "user_posts" FROM (SELECT "posts"."id", "posts"."title", "posts"."user_posts", (ROW_NUMBER() OVER (PARTITION BY "posts"."user_posts" ORDER BY "posts"."id" DESC)) AS "velox_partition_row" FROM "posts" WHERE "posts"."user_posts" IN ($1, $2)) AS "posts" WHERE "velox_partition_row" <= $3 ORDER BY "velox_partition_row"`,
			query)
		require.Equal(t, []any{1, 2, 3}, args)
	})

	t.Run("mysql M2M partition on the join table", func(t *testing.T) {
		d := Dialect(dialect.MySQL)
		tags := d.Table("tags")
		joinT := d.Table("todo_tags")
		s := d.Select(tags.C("id"), tags.C("name")).From(tags).
			OrderBy(tags.C("id"))
		s.Join(joinT).On(tags.C("id"), joinT.C("tag_id"))
		s.Where(In(joinT.C("todo_id"), 7))
		cols := s.SelectedColumns()
		s.Select(joinT.C("todo_id")).AppendSelect(cols...)
		s.LimitPerPartition(joinT.C("todo_id"), 2)
		query, args := s.Query()
		require.Equal(t,
			"SELECT `todo_id`, `id`, `name` FROM (SELECT `t1`.`todo_id`, `tags`.`id`, `tags`.`name`, (ROW_NUMBER() OVER (PARTITION BY `t1`.`todo_id` ORDER BY `tags`.`id`)) AS `velox_partition_row` FROM `tags` JOIN `todo_tags` AS `t1` ON `tags`.`id` = `t1`.`tag_id` WHERE `t1`.`todo_id` IN (?)) AS `tags` WHERE `velox_partition_row` <= ? ORDER BY `velox_partition_row`",
			query)
		require.Equal(t, []any{7, 2}, args)
	})

	t.Run("drops DISTINCT from the ranked query", func(t *testing.T) {
		d := Dialect(dialect.SQLite)
		s := d.Select("id").From(d.Table("users")).Distinct().OrderBy("id")
		s.LimitPerPartition(s.C("group_id"), 1)
		query, _ := s.Query()
		require.NotContains(t, query, "DISTINCT")
	})

	// An ORDER BY that binds an argument moves into the window, which is
	// rendered before the WHERE: its placeholder must come first and the
	// argument must be kept. OrderExprFunc used to render its callback to a
	// bare string and drop every argument it bound.
	t.Run("order arguments are threaded through the window", func(t *testing.T) {
		const want = `SELECT "id" FROM (SELECT "posts"."id", (ROW_NUMBER() OVER (PARTITION BY "posts"."user_posts" ORDER BY CASE WHEN "posts"."title" = $1 THEN 0 ELSE 1 END)) AS "velox_partition_row" FROM "posts" WHERE "posts"."x" <> $2) AS "posts" WHERE "velox_partition_row" <= $3 ORDER BY "velox_partition_row"`
		byCase := func(s *Selector) func(*Builder) {
			return func(b *Builder) {
				b.WriteString("CASE WHEN ").Ident(s.C("title")).WriteOp(OpEQ).Arg("keep").WriteString(" THEN 0 ELSE 1 END")
			}
		}
		for name, order := range map[string]func(*Selector){
			"OrderExprFunc":     func(s *Selector) { s.OrderExprFunc(byCase(s)) },
			"OrderExpr+ExprFunc": func(s *Selector) { s.OrderExpr(ExprFunc(byCase(s))) },
		} {
			t.Run(name, func(t *testing.T) {
				d := Dialect(dialect.Postgres)
				posts := d.Table("posts")
				s := d.Select(posts.C("id")).From(posts).Where(NEQ(posts.C("x"), 5))
				order(s)
				s.LimitPerPartition(s.C("user_posts"), 2)
				query, args := s.Query()
				require.Equal(t, want, query)
				require.Equal(t, []any{"keep", 5, 2}, args)
				// Rendering is repeatable: the window used to append to its
				// own builder on every call. (A top-level selector keeps its
				// argument count between renders, so reset it first.)
				s.SetTotal(0)
				query, args = s.Query()
				require.Equal(t, want, query)
				require.Equal(t, []any{"keep", 5, 2}, args)
			})
		}
	})
}

func TestWindowBuilder_QueryIsRepeatable(t *testing.T) {
	w := RowNumber().PartitionBy("a").OrderExpr(ExprFunc(func(b *Builder) { b.Ident("b").WriteOp(OpGT).Arg(1) }))
	q1, a1 := w.Query()
	q2, a2 := w.Query()
	require.Equal(t, "ROW_NUMBER() OVER (PARTITION BY `a` ORDER BY `b` > ?)", q1)
	require.Equal(t, q1, q2)
	require.Equal(t, []any{1}, a1)
	require.Equal(t, a1, a2)
}

func TestDialectBuilderExpr_KeepsArguments(t *testing.T) {
	x := Dialect(dialect.Postgres).Expr(func(b *Builder) { b.Ident("a").WriteOp(OpEQ).Arg(7) })
	query, args := Dialect(dialect.Postgres).Select("a").From(Table("t")).Where(NEQ("b", 1)).OrderExpr(x).Query()
	require.Equal(t, `SELECT "a" FROM "t" WHERE "b" <> $1 ORDER BY "a" = $2`, query)
	require.Equal(t, []any{1, 7}, args)
}
