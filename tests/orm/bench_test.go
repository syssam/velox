package tests

import (
	"context"
	"fmt"
	"testing"

	client "github.com/syssam/velox/tests/orm/velox"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect"
	velsql "github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/orm/velox/user"
)

// benchDB returns a handle and a velox client over one SQLite database
// holding n users, so each benchmark below runs the same operation through
// both on the same data.
func benchDB(b *testing.B, n int) (*velox.DB, *integration.Client, []int) {
	b.Helper()
	ctx := context.Background()
	dsn := fmt.Sprintf("file:ormbench%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", dbSeq.Add(1))
	drv, err := velsql.Open(dialect.SQLite, dsn)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = drv.Close() })
	ref := integration.NewClient(integration.Driver(drv))
	if err := ref.Schema.Create(ctx); err != nil {
		b.Fatal(err)
	}
	db := velox.OpenDB(dialect.SQLite, drv.DB())
	ids := make([]int, n)
	for i := range ids {
		u, err := client.User.Create(db).
			Set(user.Name.Set(fmt.Sprintf("u%d", i)), user.Email.Set(fmt.Sprintf("u%d@example.com", i)), user.Age.Set(20+i%50)).
			Save(ctx)
		if err != nil {
			b.Fatal(err)
		}
		ids[i] = u.ID
	}
	return db, ref, ids
}

func BenchmarkPointLookup(b *testing.B) {
	ctx := context.Background()
	db, ref, ids := benchDB(b, 100)
	b.Run("orm", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if _, err := client.User.Query(db).Where(user.ID.EQ(ids[i%len(ids)])).Only(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("velox", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if _, err := ref.User.Get(ctx, ids[i%len(ids)]); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkRead500(b *testing.B) {
	ctx := context.Background()
	db, ref, _ := benchDB(b, 500)
	b.Run("orm", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			us, err := client.User.Query(db).Limit(500).All(ctx)
			if err != nil || len(us) != 500 {
				b.Fatal(len(us), err)
			}
		}
	})
	b.Run("velox", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			us, err := ref.User.Query().Limit(500).All(ctx)
			if err != nil || len(us) != 500 {
				b.Fatal(len(us), err)
			}
		}
	})
}

func BenchmarkCreate(b *testing.B) {
	ctx := context.Background()
	db, ref, _ := benchDB(b, 0)
	b.Run("orm", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			_, err := client.User.Create(db).
				Set(user.Name.Set("n"), user.Email.Set(fmt.Sprintf("orm%d@example.com", i)), user.Age.Set(30)).
				Save(ctx)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("velox", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			_, err := ref.User.Create().SetName("n").SetEmail(fmt.Sprintf("velox%d@example.com", i)).SetAge(30).Save(ctx)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkUpdateOne(b *testing.B) {
	ctx := context.Background()
	db, ref, ids := benchDB(b, 100)
	b.Run("orm/save", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if _, err := client.User.Update(db).Set(user.Age.Set(20 + i%50)).Where(user.ID.EQ(ids[i%len(ids)])).SaveOne(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("orm/exec", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if _, err := client.User.Update(db).Set(user.Age.Set(20 + i%50)).Where(user.ID.EQ(ids[i%len(ids)])).Exec(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("velox/save", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if _, err := ref.User.UpdateOneID(ids[i%len(ids)]).SetAge(20 + i%50).Save(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("velox/exec", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			if err := ref.User.UpdateOneID(ids[i%len(ids)]).SetAge(20 + i%50).Exec(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// benchOpt is an optional as an API layer has one (see orm.MutCore.Fill).
type benchOpt[T any] struct {
	v   T
	set bool
}

func (o benchOpt[T]) ValueOK() (T, bool) { return o.v, o.set }

// BenchmarkCreateForms compares the ways to give a create its values: the
// statement is the same, so the difference is what building it costs.
func BenchmarkCreateForms(b *testing.B) {
	ctx := context.Background()
	db, _, _ := benchDB(b, 0)
	seq := 0
	next := func() (string, string) {
		seq++
		return fmt.Sprintf("u%d", seq), fmt.Sprintf("u%d@example.com", seq)
	}
	nick := "n"
	b.Run("setters", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			name, email := next()
			if _, err := client.User.Create(db).Set(user.Name.Set(name), user.Email.Set(email), user.Age.Set(30), user.Nickname.SetNillable(&nick)).Save(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	type ptrInput struct {
		Name, Email *string
		Age         *int
		Nickname    *string
	}
	b.Run("fill-pointers", func(b *testing.B) {
		b.ReportAllocs()
		age := 30
		for b.Loop() {
			name, email := next()
			if _, err := client.User.Create(db).Fill(ptrInput{Name: &name, Email: &email, Age: &age, Nickname: &nick}).Save(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	type optInput struct {
		Name, Email string
		Age         benchOpt[*int]
		Nickname    benchOpt[*string]
	}
	b.Run("fill-optionals", func(b *testing.B) {
		b.ReportAllocs()
		age := 30
		for b.Loop() {
			name, email := next()
			if _, err := client.User.Create(db).Fill(optInput{Name: name, Email: email, Age: benchOpt[*int]{&age, true}, Nickname: benchOpt[*string]{&nick, true}}).Save(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
}
