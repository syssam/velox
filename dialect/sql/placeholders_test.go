package sql

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
)

var placeholderRE = regexp.MustCompile(`\$(\d+)`)

// checkPlaceholders renders q three times and checks each render.
func checkPlaceholders(t *testing.T, name string, q Querier) {
	t.Helper()
	var first string
	for i := range 3 {
		s, args := q.Query()
		ms := placeholderRE.FindAllStringSubmatch(s, -1)
		seen := map[int]bool{}
		for j, m := range ms {
			n, _ := strconv.Atoi(m[1])
			seen[n] = true
			require.Equalf(t, j+1, n, "%s render %d: placeholder order %s", name, i, s)
		}
		require.Equalf(t, len(args), len(seen), "%s render %d: %s args=%v", name, i, s, args)
		if i == 0 {
			first = s
		} else {
			require.Equalf(t, first, s, "%s render %d differs", name, i)
		}
	}
}

// TestPostgresPlaceholders_AcrossShapes pins that PostgreSQL placeholders
// are numbered $1..$n in order, match the arguments, and come out the same
// on every render, for each way a query nests another: IN and EXISTS
// subqueries, FROM and JOIN subqueries, unions, WITH, LimitPerPartition,
// one subquery used twice, a subquery rendered on its own after being
// nested, UPDATE ... FromSelect and the count window. Renders reused state
// (the running total, the output buffer), so a second render drifted.
func TestPostgresPlaceholders_AcrossShapes(t *testing.T) {
	d := Dialect(dialect.Postgres)
	mk := func() *Selector {
		return d.Select("id").From(Table("users")).Where(EQ("a", 1))
	}
	t.Run("in-subquery", func(t *testing.T) {
		sub := mk()
		checkPlaceholders(t, "in", d.Select().From(Table("t")).Where(And(EQ("x", 0), In("id", sub), EQ("y", 2))))
	})
	t.Run("exists", func(t *testing.T) {
		sub := mk()
		checkPlaceholders(t, "exists", d.Select().From(Table("t")).Where(And(EQ("x", 0), Exists(sub), EQ("y", 2))))
	})
	t.Run("from-sub", func(t *testing.T) {
		sub := mk().As("s")
		checkPlaceholders(t, "from", d.Select().From(sub).Where(EQ("y", 2)))
	})
	t.Run("union", func(t *testing.T) {
		a, b := mk(), mk()
		checkPlaceholders(t, "union", a.Union(b).Where(EQ("z", 3)))
	})
	t.Run("union-then-where-in", func(t *testing.T) {
		a, b := mk(), mk()
		u := a.UnionAll(b)
		checkPlaceholders(t, "u", d.Select().From(Table("t")).Where(And(EQ("x", 0), In("id", u))))
	})
	t.Run("with", func(t *testing.T) {
		w := With("cte").As(mk())
		checkPlaceholders(t, "with", d.Select().Prefix(w).From(w).Where(EQ("x", 9)))
	})
	t.Run("with-in-sub", func(t *testing.T) {
		w := With("cte").As(mk())
		sub := d.Select("id").Prefix(w).From(w).Where(EQ("q", 1))
		checkPlaceholders(t, "withsub", d.Select().From(Table("t")).Where(And(EQ("x", 0), In("id", sub))))
	})
	t.Run("join-sub", func(t *testing.T) {
		sub := mk().As("s")
		s := d.Select().From(Table("t")).Where(EQ("x", 0))
		s.Join(sub).On(s.C("id"), sub.C("id"))
		checkPlaceholders(t, "join", s)
	})
	t.Run("lpp", func(t *testing.T) {
		s := d.Select("id", "g").From(Table("t")).Where(EQ("x", 0)).OrderBy("id")
		s.LimitPerPartition("g", 3)
		checkPlaceholders(t, "lpp", s)
		checkPlaceholders(t, "lpp-in", d.Select().From(Table("u")).Where(And(EQ("k", 1), In("id", s))))
	})
	t.Run("same-sub-twice", func(t *testing.T) {
		sub := mk()
		checkPlaceholders(t, "twice", d.Select().From(Table("t")).Where(And(In("id", sub), EQ("m", 5), In("id2", sub))))
	})
	t.Run("sub-rendered-standalone-after-nested", func(t *testing.T) {
		sub := mk()
		checkPlaceholders(t, "outer", d.Select().From(Table("t")).Where(And(EQ("x", 0), EQ("y", 0), In("id", sub))))
		checkPlaceholders(t, "standalone", sub)
	})
	t.Run("update-fromselect", func(t *testing.T) {
		sub := mk()
		sel := d.Select().From(Table("users")).Where(In("id", sub))
		checkPlaceholders(t, "upd", d.Update("users").Set("name", "n").FromSelect(sel))
		checkPlaceholders(t, "del", d.Delete("users").FromSelect(sel))
	})
	t.Run("count-window", func(t *testing.T) {
		inner := mk()
		inner.Limit(2)
		checkPlaceholders(t, "cw", d.Select(Count("*")).From(inner.As("t1")))
	})
	t.Run("selectexpr-arg", func(t *testing.T) {
		s := d.Select().AppendSelectExprAs(ExprFunc(func(b *Builder) { b.Arg(7) }), "v").From(Table("t")).Where(EQ("x", 1))
		checkPlaceholders(t, "se", s)
		checkPlaceholders(t, "se-in", d.Select().From(Table("u")).Where(And(EQ("k", 1), In("id", s))))
	})
}
