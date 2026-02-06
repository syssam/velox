package sql

import (
	"testing"

	"github.com/syssam/velox/dialect"
)

func BenchmarkInsertBuilder_Default(b *testing.B) {
	for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
		b.Run(d, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Dialect(d).Insert("users").Default().Returning("id").Query()
			}
		})
	}
}

func BenchmarkInsertBuilder_Small(b *testing.B) {
	for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
		b.Run(d, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Dialect(d).Insert("users").
					Columns("id", "age", "first_name", "last_name", "nickname", "spouse_id", "created_at", "updated_at").
					Values(1, 30, "Ariel", "Mashraki", "a8m", 2, "2009-11-10 23:00:00", "2009-11-10 23:00:00").
					Returning("id").
					Query()
			}
		})
	}
}

func BenchmarkSelectBuilder_Simple(b *testing.B) {
	for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
		b.Run(d, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Dialect(d).Select("id", "name", "email").
					From(Table("users")).
					Query()
			}
		})
	}
}

func BenchmarkSelectBuilder_WithJoins(b *testing.B) {
	for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
		b.Run(d, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				users := Table("users").As("u")
				posts := Table("posts").As("p")
				Dialect(d).Select("u.id", "u.name", "p.title").
					From(users).
					Join(posts).On(users.C("id"), posts.C("user_id")).
					Where(EQ("u.active", true)).
					OrderBy("u.created_at").
					Limit(10).
					Query()
			}
		})
	}
}

func BenchmarkSelectBuilder_Complex(b *testing.B) {
	for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
		b.Run(d, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Dialect(d).Select("*").
					From(Table("users")).
					Where(
						And(
							EQ("status", "active"),
							Or(
								GT("age", 18),
								EQ("role", "admin"),
							),
							In("department", "engineering", "product", "design"),
							NotNull("email"),
						),
					).
					OrderBy("created_at", "name").
					Limit(100).
					Offset(50).
					Query()
			}
		})
	}
}

func BenchmarkUpdateBuilder_Simple(b *testing.B) {
	for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
		b.Run(d, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Dialect(d).Update("users").
					Set("name", "John").
					Set("updated_at", "2024-01-01 00:00:00").
					Where(EQ("id", 1)).
					Query()
			}
		})
	}
}

func BenchmarkUpdateBuilder_Multiple(b *testing.B) {
	for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
		b.Run(d, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Dialect(d).Update("users").
					Set("first_name", "John").
					Set("last_name", "Doe").
					Set("email", "john@example.com").
					Set("age", 30).
					Set("status", "active").
					Set("updated_at", "2024-01-01 00:00:00").
					Where(In("id", 1, 2, 3, 4, 5)).
					Query()
			}
		})
	}
}

func BenchmarkDeleteBuilder_Simple(b *testing.B) {
	for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
		b.Run(d, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Dialect(d).Delete("users").
					Where(EQ("id", 1)).
					Query()
			}
		})
	}
}

func BenchmarkDeleteBuilder_WithConditions(b *testing.B) {
	for _, d := range []string{dialect.SQLite, dialect.MySQL, dialect.Postgres} {
		b.Run(d, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				Dialect(d).Delete("users").
					Where(
						And(
							EQ("status", "deleted"),
							LT("deleted_at", "2023-01-01"),
							NotIn("role", "admin", "moderator"),
						),
					).
					Query()
			}
		})
	}
}

func BenchmarkPredicates_Simple(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = EQ("name", "John")
		_ = NEQ("status", "deleted")
		_ = GT("age", 18)
		_ = LT("score", 100)
	}
}

func BenchmarkPredicates_Compound(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = And(
			EQ("status", "active"),
			Or(
				GT("age", 18),
				EQ("role", "admin"),
			),
			In("department", "eng", "product"),
			NotNull("email"),
			Contains("name", "John"),
		)
	}
}
