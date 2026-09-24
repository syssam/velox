package sqlgraph

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/schema/field"

	_ "modernc.org/sqlite"
)

// TestCountNodes_SQLShape pins which counts render COUNT(*) and which keep
// COUNT(<column>). COUNT(*) is only emitted where it provably equals
// COUNT(<id>): a single-column primary key, no selected columns, no
// DISTINCT and no JOIN on the selector.
func TestCountNodes_SQLShape(t *testing.T) {
	usersID := func() *NodeSpec {
		return &NodeSpec{Table: "users", ID: &FieldSpec{Column: "id", Type: field.TypeInt}}
	}
	tests := []struct {
		name string
		spec func() *QuerySpec
		want string
		args []any
	}{
		{
			name: "plain",
			spec: func() *QuerySpec {
				return &QuerySpec{
					Node:      usersID(),
					Predicate: func(s *sql.Selector) { s.Where(sql.LT("age", 40)) },
					Order:     func(s *sql.Selector) { s.OrderBy("id") },
				}
			},
			want: "SELECT COUNT(*) FROM `users` WHERE `age` < ?",
			args: []any{40},
		},
		{
			// The window applies to the rows, not to the COUNT row: counting
			// "FROM users LIMIT 3 OFFSET 4" skipped the only row the
			// aggregate returns. Deliberate Ent deviation (Ent has the bug).
			name: "limit and offset count the window",
			spec: func() *QuerySpec {
				return &QuerySpec{Node: usersID(), Limit: 3, Offset: 4}
			},
			want: "SELECT COUNT(*) FROM (SELECT `users`.`id` FROM `users` LIMIT 3 OFFSET 4) AS `t1`",
		},
		{
			name: "unique keeps COUNT(DISTINCT id)",
			spec: func() *QuerySpec {
				return &QuerySpec{Node: usersID(), Unique: true}
			},
			want: "SELECT COUNT(DISTINCT `users`.`id`) FROM `users`",
		},
		{
			name: "selected column keeps COUNT(column)",
			spec: func() *QuerySpec {
				n := usersID()
				n.Columns = []string{"name"}
				return &QuerySpec{Node: n}
			},
			want: "SELECT COUNT(`users`.`name`) FROM `users`",
		},
		{
			name: "composite id already counts rows",
			spec: func() *QuerySpec {
				return &QuerySpec{Node: &NodeSpec{Table: "user_groups"}}
			},
			want: "SELECT COUNT(*) FROM `user_groups`",
		},
		{
			name: "O2M traversal without join",
			spec: func() *QuerySpec {
				step := NewStep(
					From("users", "id", 1),
					To("pets", "id"),
					Edge(O2M, false, "pets", "owner_id"),
				)
				return &QuerySpec{
					Node: &NodeSpec{Table: "pets", ID: &FieldSpec{Column: "id", Type: field.TypeInt}},
					From: Neighbors(dialect.MySQL, step),
				}
			},
			want: "SELECT COUNT(*) FROM `pets` WHERE `owner_id` = ?",
			args: []any{1},
		},
		{
			name: "M2M traversal joins keep COUNT(id)",
			spec: func() *QuerySpec {
				step := NewStep(
					From("users", "id", 1),
					To("groups", "id"),
					Edge(M2M, false, "user_groups", "user_id", "group_id"),
				)
				return &QuerySpec{
					Node: &NodeSpec{Table: "groups", ID: &FieldSpec{Column: "id", Type: field.TypeInt}},
					From: Neighbors(dialect.MySQL, step),
				}
			},
			want: "SELECT COUNT(`groups`.`id`) FROM `groups` JOIN (SELECT `user_groups`.`group_id` FROM `user_groups` WHERE `user_groups`.`user_id` = ?) AS `t1` ON `groups`.`id` = `t1`.`group_id`",
			args: []any{1},
		},
		{
			name: "order-by-neighbors join keeps COUNT(id)",
			spec: func() *QuerySpec {
				return &QuerySpec{
					Node: usersID(),
					Order: func(s *sql.Selector) {
						OrderByNeighborsCount(s, NewStep(
							From("users", "id"),
							To("pets", "owner_id"),
							Edge(O2M, false, "pets", "owner_id"),
						))
					},
				}
			},
			want: "SELECT COUNT(`users`.`id`) FROM `users` LEFT JOIN (SELECT `pets`.`owner_id`, COUNT(*) AS `count_pets` FROM `pets` GROUP BY `pets`.`owner_id`) AS `t1` ON `users`.`id` = `t1`.`owner_id`",
		},
		{
			name: "predicate right join keeps COUNT(id)",
			spec: func() *QuerySpec {
				return &QuerySpec{
					Node: usersID(),
					Predicate: func(s *sql.Selector) {
						pets := sql.Table("pets")
						s.RightJoin(pets).On(s.C("id"), pets.C("owner_id"))
					},
				}
			},
			want: "SELECT COUNT(`users`.`id`) FROM `users` RIGHT JOIN `pets` AS `t1` ON `users`.`id` = `t1`.`owner_id`",
		},
		{
			name: "raw FROM expression keeps COUNT(id)",
			spec: func() *QuerySpec {
				return &QuerySpec{
					Node:      usersID(),
					Modifiers: []func(*sql.Selector){func(s *sql.Selector) { s.FromExpr(sql.Raw("`users`")) }},
				}
			},
			want: "SELECT COUNT(`id`) FROM `users`",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			require.NoError(t, err)
			args := make([]driver.Value, 0, len(tt.args))
			for _, a := range tt.args {
				args = append(args, a)
			}
			mock.ExpectQuery(tt.want).
				WithArgs(args...).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))
			n, err := CountNodes(context.Background(), sql.OpenDB(dialect.MySQL, db), tt.spec())
			require.NoError(t, err)
			require.Equal(t, 7, n)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// openCountDB opens an in-memory SQLite database with a users table of n
// rows and a pets table where every third pet has no owner.
func openCountDB(tb testing.TB, name string, n int) *stdsql.DB {
	tb.Helper()
	db, err := stdsql.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared&_pragma=foreign_keys(1)", name))
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	_, err = db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, age INTEGER NOT NULL)")
	require.NoError(tb, err)
	_, err = db.Exec("CREATE TABLE pets (id INTEGER PRIMARY KEY AUTOINCREMENT, owner_id INTEGER)")
	require.NoError(tb, err)
	tx, err := db.Begin()
	require.NoError(tb, err)
	for i := range n {
		var name any
		if i%2 == 0 {
			name = fmt.Sprintf("u%d", i)
		}
		_, err = tx.Exec("INSERT INTO users (name, age) VALUES (?, ?)", name, i%100)
		require.NoError(tb, err)
		var owner any
		if i%3 != 0 {
			owner = i + 1
		}
		_, err = tx.Exec("INSERT INTO pets (owner_id) VALUES (?)", owner)
		require.NoError(tb, err)
	}
	require.NoError(tb, tx.Commit())
	return db
}

// TestCountNodes_SQLiteResults checks the counts against a real database,
// including the cases where COUNT(*) and COUNT(<column>) would disagree:
// a nullable selected column and a RIGHT JOIN that null-extends users.
func TestCountNodes_SQLiteResults(t *testing.T) {
	db := openCountDB(t, "sqlgraph_count_results", 30)
	drv := sql.OpenDB(dialect.SQLite, db)
	usersID := func() *NodeSpec {
		return &NodeSpec{Table: "users", ID: &FieldSpec{Column: "id", Type: field.TypeInt}}
	}
	tests := []struct {
		name string
		spec *QuerySpec
		want int
	}{
		{"all rows", &QuerySpec{Node: usersID()}, 30},
		{"predicate", &QuerySpec{Node: usersID(), Predicate: func(s *sql.Selector) { s.Where(sql.LT("age", 10)) }}, 10},
		{"nullable selected column", &QuerySpec{Node: &NodeSpec{Table: "users", Columns: []string{"name"}, ID: &FieldSpec{Column: "id", Type: field.TypeInt}}}, 15},
		{
			// 30 pets, 10 without an owner: RIGHT JOIN yields 30 rows but
			// only 20 carry a users.id. COUNT(*) would answer 30.
			"right join null-extends the node",
			&QuerySpec{Node: usersID(), Predicate: func(s *sql.Selector) {
				pets := sql.Table("pets")
				s.RightJoin(pets).On(s.C("id"), pets.C("owner_id"))
			}},
			20,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := CountNodes(context.Background(), drv, tt.spec)
			require.NoError(t, err)
			require.Equal(t, tt.want, n)
		})
	}
}

// BenchmarkCountNodes_SQLite measures CountNodes over 10k rows on SQLite,
// where COUNT(*) avoids reading the id column of every row.
func BenchmarkCountNodes_SQLite(b *testing.B) {
	db := openCountDB(b, "sqlgraph_count_bench", 10_000)
	drv := sql.OpenDB(dialect.SQLite, db)
	ctx := context.Background()
	b.Run("NoPredicate", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			spec := &QuerySpec{Node: &NodeSpec{Table: "users", ID: &FieldSpec{Column: "id", Type: field.TypeInt}}}
			if _, err := CountNodes(ctx, drv, spec); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Predicate", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			spec := &QuerySpec{
				Node:      &NodeSpec{Table: "users", ID: &FieldSpec{Column: "id", Type: field.TypeInt}},
				Predicate: func(s *sql.Selector) { s.Where(sql.LT("age", 50)) },
			}
			if _, err := CountNodes(ctx, drv, spec); err != nil {
				b.Fatal(err)
			}
		}
	})
}
