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
}
