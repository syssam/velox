package sql

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
)

// ---------------------------------------------------------------------------
// Builder helpers
// ---------------------------------------------------------------------------

func TestBuilderTotal(t *testing.T) {
	b := &Builder{}
	require.Equal(t, 0, b.Total())
	b.Arg("a")
	require.Equal(t, 1, b.Total())
}

func TestBuilderString(t *testing.T) {
	d := Dialect(dialect.Postgres)
	s := d.String(func(b *Builder) {
		b.Ident("users")
	})
	require.Equal(t, `"users"`, s)
}

func TestBuilderExpr(t *testing.T) {
	d := Dialect(dialect.Postgres)
	q := d.Expr(func(b *Builder) {
		b.WriteString("NOW()")
	})
	query, args := q.Query()
	require.Equal(t, "NOW()", query)
	require.Empty(t, args)
}

func TestDialectBuilderColumn(t *testing.T) {
	q := Dialect(dialect.Postgres).Column("id").Type("int")
	query, _ := q.Query()
	require.Contains(t, query, `"id"`)
	require.Contains(t, query, "int")
}

func TestDialectBuilderSelectExpr(t *testing.T) {
	q := Dialect(dialect.Postgres).
		SelectExpr(Expr("COUNT(*)")).
		From(Table("users"))
	query, _ := q.Query()
	require.Equal(t, `SELECT COUNT(*) FROM "users"`, query)
}

func TestBuilderWriteOpInvalid(t *testing.T) {
	b := &Builder{}
	b.WriteOp(Op(999))
	require.Error(t, b.Err())
}

func TestBuilderArgf(t *testing.T) {
	b := &Builder{}
	b.SetDialect(dialect.MySQL)

	// nil arg
	b.Argf("?", nil)
	require.Equal(t, "NULL", b.String())

	// raw arg
	b2 := &Builder{}
	r := &raw{s: "UNHEX(?)"}
	b2.Argf("UNHEX(?)", r)
	require.Equal(t, "UNHEX(?)", b2.String())
}

func TestBuilderClone(t *testing.T) {
	b := &Builder{}
	b.SetDialect(dialect.Postgres)
	b.Arg("hello")
	c := b.clone()
	require.Equal(t, dialect.Postgres, c.Dialect())
	require.Equal(t, 1, c.total)
	require.Equal(t, []any{"hello"}, c.args)
}

// ---------------------------------------------------------------------------
// SelectTable / view
// ---------------------------------------------------------------------------

func TestSelectTableView(t *testing.T) {
	// view() is called internally; just ensure C() works
	t1 := Table("users")
	require.Equal(t, "`users`.`id`", t1.C("id"))

	// With alias
	t2 := Table("users").As("u")
	require.Equal(t, "`u`.`id`", t2.C("id"))
}

// ---------------------------------------------------------------------------
// Selector helpers
// ---------------------------------------------------------------------------

func TestSelectorNew(t *testing.T) {
	s := Dialect(dialect.Postgres).Select("id").From(Table("users"))
	s2 := s.New()
	require.NotNil(t, s2)
	q, _ := s2.Select("name").From(Table("orders")).Query()
	require.Equal(t, `SELECT "name" FROM "orders"`, q)
}

func TestSelectorSelectDistinct(t *testing.T) {
	s := Select().From(Table("users")).SelectDistinct("name", "age")
	query, _ := s.Query()
	require.Contains(t, query, "DISTINCT")
	require.Contains(t, query, "`name`")
}

func TestSelectorOffset(t *testing.T) {
	s := Select("id").From(Table("users")).Limit(10).Offset(20)
	query, _ := s.Query()
	require.Contains(t, query, "OFFSET 20")
	require.Contains(t, query, "LIMIT 10")
}

func TestSelectorSetDistinct(t *testing.T) {
	s := Select("id").From(Table("users"))
	s.SetDistinct(true)
	query, _ := s.Query()
	require.Contains(t, query, "DISTINCT")
	s.SetDistinct(false)
	query2, _ := s.Query()
	require.NotContains(t, query2, "DISTINCT")
}

func TestSelectorFromExpr(t *testing.T) {
	raw := Expr("(SELECT 1)")
	s := Select("*").FromExpr(raw)
	query, _ := s.Query()
	require.Contains(t, query, "(SELECT 1)")
}

func TestSelectorP(t *testing.T) {
	s := Select("id").From(Table("users")).Where(EQ("id", 1))
	require.NotNil(t, s.P())
}

func TestSelectorSetP(t *testing.T) {
	s := Select("id").From(Table("users"))
	p := EQ("name", "foo")
	s.SetP(p)
	q, _ := s.Query()
	require.Contains(t, q, "WHERE")
}

func TestSelectorFromSelect(t *testing.T) {
	s1 := Select("id").From(Table("users")).Where(EQ("active", true))
	s2 := Select("id").From(Table("users"))
	s2.FromSelect(s1)
	q, _ := s2.Query()
	require.Contains(t, q, "WHERE")
}

func TestSelectorNot(t *testing.T) {
	s := Select("id").From(Table("users"))
	s.Not().Where(EQ("active", true))
	q, _ := s.Query()
	require.Contains(t, q, "NOT")
}

func TestSelectorTableName(t *testing.T) {
	s := Select("id").From(Table("users"))
	require.Equal(t, "users", s.TableName())

	// With sub-selector
	sub := Select("id").From(Table("accounts")).As("a")
	s2 := Select("id").From(sub)
	require.Equal(t, "a", s2.TableName())

	// With no from
	s3 := &Selector{}
	require.Equal(t, "", s3.TableName())

	// With queryView
	s4 := Select("id").FromExpr(Expr("(SELECT 1)"))
	require.Equal(t, "", s4.TableName())
}

func TestSelectorHaving(t *testing.T) {
	s := Select("name", "COUNT(*)").
		From(Table("users")).
		GroupBy("name").
		Having(GT("age", 18))
	query, args := s.Query()
	require.Contains(t, query, "HAVING")
	require.Contains(t, query, "GROUP BY")
	require.Equal(t, []any{18}, args)
}

func TestSelectorOrderColumns(t *testing.T) {
	s := Select("id").From(Table("users")).OrderBy("name", "age")
	cols := s.OrderColumns()
	require.Equal(t, []string{"name", "age"}, cols)
}

func TestAscDescExpr(t *testing.T) {
	a := Asc("name")
	require.Equal(t, "`name` ASC", a)

	expr := DescExpr(Expr("age"))
	q, _ := expr.Query()
	require.Equal(t, "age DESC", q)
}

func TestOrderExprFunc(t *testing.T) {
	s := Select("id").From(Table("users")).
		OrderExprFunc(func(b *Builder) {
			b.WriteString("FIELD(status, 1, 2, 3)")
		})
	q, _ := s.Query()
	require.Contains(t, q, "ORDER BY FIELD(status, 1, 2, 3)")
}

func TestSelectorUnionError(t *testing.T) {
	// self-union should produce an error
	s := Select("id").From(Table("users"))
	s.Union(s)
	_, _ = s.Query()
	require.Error(t, s.Err())
}

func TestSelectorExceptAll(t *testing.T) {
	s1 := Select("id").From(Table("users"))
	s2 := Select("id").From(Table("admins"))
	s1.ExceptAll(s2)
	q, _ := s1.Query()
	require.Contains(t, q, "EXCEPT ALL")

	// SQLite should error
	ss := Dialect(dialect.SQLite).Select("id").From(Table("users"))
	ss.ExceptAll(Select("id").From(Table("admins")))
	require.Error(t, ss.Err())
}

func TestSelectorIntersectAll(t *testing.T) {
	s1 := Select("id").From(Table("users"))
	s2 := Select("id").From(Table("admins"))
	s1.IntersectAll(s2)
	q, _ := s1.Query()
	require.Contains(t, q, "INTERSECT ALL")

	// SQLite should error
	ss := Dialect(dialect.SQLite).Select("id").From(Table("users"))
	ss.IntersectAll(Select("id").From(Table("admins")))
	require.Error(t, ss.Err())
}

// ---------------------------------------------------------------------------
// Wrapper (SetDialect/Dialect/Total/SetTotal)
// ---------------------------------------------------------------------------

func TestWrapper(t *testing.T) {
	inner := Select("id").From(Table("users"))
	inner.SetDialect(dialect.Postgres)
	w := &Wrapper{format: "(%s)", wrapped: inner}

	w.SetDialect(dialect.Postgres)
	require.Equal(t, dialect.Postgres, w.Dialect())

	w.SetTotal(5)
	require.Equal(t, 5, w.Total())

	q, _ := w.Query()
	require.Contains(t, q, `"users"`)

	// Without state (plain Querier)
	plainW := &Wrapper{format: "(%s)", wrapped: Expr("1")}
	plainW.SetDialect(dialect.Postgres)
	require.Equal(t, "", plainW.Dialect())
	require.Equal(t, 0, plainW.Total())
	plainW.SetTotal(3) // no-op, shouldn't panic
}

// ---------------------------------------------------------------------------
// PartitionExpr / WindowBuilder
// ---------------------------------------------------------------------------

func TestWindowPartitionExpr(t *testing.T) {
	w := Window(func(b *Builder) {
		b.WriteString("ROW_NUMBER()")
	}).PartitionExpr(Expr("department_id")).OrderBy("salary")
	q, _ := w.Query()
	require.Contains(t, q, "PARTITION BY department_id")
	require.Contains(t, q, "ORDER BY `salary`")
}

// ---------------------------------------------------------------------------
// Predicate helpers
// ---------------------------------------------------------------------------

func TestColumnsOp(t *testing.T) {
	p := ColumnsOp("age", "min_age", OpGTE)
	b := &Builder{}
	b.Join(p)
	q := b.String()
	require.Contains(t, q, "`age`")
	require.Contains(t, q, "`min_age`")
}

func TestLikeBuiltIn(t *testing.T) {
	p := Like("name", "%foo%")
	b := &Builder{}
	b.Join(p)
	q := b.String()
	require.Contains(t, q, "LIKE")
}

func TestLowerFunc(t *testing.T) {
	s := Lower("email")
	require.Equal(t, "LOWER(`email`)", s)
}

func TestAvgFunc(t *testing.T) {
	s := Avg("score")
	require.Equal(t, "AVG(`score`)", s)

	f := &Func{}
	f.Avg("score")
	require.Equal(t, "AVG(`score`)", f.String())
}

// ---------------------------------------------------------------------------
// INSERT – Set / Default / Columns / UpdateSet helpers
// ---------------------------------------------------------------------------

func TestInsertSet(t *testing.T) {
	q, args := Insert("users").Set("name", "foo").Set("age", 10).Query()
	require.Equal(t, "INSERT INTO `users` (`name`, `age`) VALUES (?, ?)", q)
	require.Equal(t, []any{"foo", 10}, args)
}

func TestInsertDefault(t *testing.T) {
	// MySQL
	q, _ := Dialect(dialect.MySQL).Insert("users").Default().Query()
	require.Equal(t, "INSERT INTO `users` VALUES ()", q)

	// PostgreSQL
	q, _ = Dialect(dialect.Postgres).Insert("users").Default().Query()
	require.Equal(t, `INSERT INTO "users" DEFAULT VALUES`, q)

	// SQLite
	q, _ = Dialect(dialect.SQLite).Insert("users").Default().Query()
	require.Equal(t, "INSERT INTO `users` DEFAULT VALUES", q)
}

func TestUpdateSetColumns(t *testing.T) {
	u := &UpdateSet{
		UpdateBuilder: Update("users"),
		columns:       []string{"name", "age"},
	}
	u.UpdateBuilder.SetNull("active")
	require.Equal(t, []string{"name", "age"}, u.Columns())
	require.Equal(t, []string{"active", "name", "age"}, u.UpdateColumns())
}

// ---------------------------------------------------------------------------
// UpdateBuilder – FromSelect / Empty / Limit
// ---------------------------------------------------------------------------

func TestUpdateFromSelect(t *testing.T) {
	s := Select("id").From(Table("users")).Where(EQ("active", true))
	u := Update("users").Set("name", "foo").FromSelect(s)
	q, args := u.Query()
	require.Contains(t, q, "WHERE")
	require.NotEmpty(t, args)
}

func TestUpdateEmpty(t *testing.T) {
	u := Update("users")
	require.True(t, u.Empty())
	u.Set("name", "foo")
	require.False(t, u.Empty())
}

func TestUpdateLimit(t *testing.T) {
	// MySQL allows LIMIT
	q, _ := Dialect(dialect.MySQL).Update("users").Set("x", 1).Limit(5).Query()
	require.Contains(t, q, "LIMIT 5")

	// Postgres does not allow LIMIT
	u := Dialect(dialect.Postgres).Update("users").Set("x", 1)
	u.Limit(5)
	require.Error(t, u.Err())
}

// ---------------------------------------------------------------------------
// DDL – ViewBuilder IfNotExists / Column / Name / C
// ---------------------------------------------------------------------------

func TestViewBuilderIfNotExists(t *testing.T) {
	t1 := Table("users")
	q, _ := CreateView("v_users").
		IfNotExists().
		As(Select(t1.C("id")).From(t1)).
		Query()
	require.Contains(t, q, "IF NOT EXISTS")
}

func TestViewBuilderColumn(t *testing.T) {
	t1 := Table("users")
	q, _ := CreateView("v_users").
		Column(Column("id").Type("int")).
		As(Select(t1.C("id")).From(t1)).
		Query()
	require.Contains(t, q, "`id`")
}

func TestWithBuilderNameAndC(t *testing.T) {
	w := With("cte")
	require.Equal(t, "cte", w.Name())
	require.Equal(t, "`cte`.`id`", w.C("id"))
}

func TestWithBuilderView(t *testing.T) {
	// view() makes WithBuilder implement TableView; just use it as FROM
	w := With("cte").As(Select("id").From(Table("users")))
	s := Select("id").From(w)
	q, _ := s.Query()
	require.Contains(t, q, "cte")
}

// ---------------------------------------------------------------------------
// scan helpers – ScanInt / ScanBool / ScanString / SelectValues
// ---------------------------------------------------------------------------

func TestScanInt(t *testing.T) {
	mock := sqlmock.NewRows([]string{"count"}).AddRow(42)
	n, err := ScanInt(toRows(mock))
	require.NoError(t, err)
	require.Equal(t, 42, n)
}

func TestScanBool(t *testing.T) {
	mock := sqlmock.NewRows([]string{"active"}).AddRow(true)
	b, err := ScanBool(toRows(mock))
	require.NoError(t, err)
	require.True(t, b)
}

func TestScanString(t *testing.T) {
	mock := sqlmock.NewRows([]string{"name"}).AddRow("hello")
	s, err := ScanString(toRows(mock))
	require.NoError(t, err)
	require.Equal(t, "hello", s)
}

func TestSelectValuesSetGet(t *testing.T) {
	var sv SelectValues
	sv.Set("name", "alice")
	sv.Set("age", NullInt64{Int64: 30, Valid: true})

	v, err := sv.Get("name")
	require.NoError(t, err)
	require.Equal(t, "alice", v)

	v2, err := sv.Get("age")
	require.NoError(t, err)
	require.Equal(t, int64(30), v2)

	// Missing key
	_, err = sv.Get("missing")
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// sql.go – FieldsHasPrefix / AndPredicates / OrPredicates / combineFuncs
// ---------------------------------------------------------------------------

func TestFieldsHasPrefix(t *testing.T) {
	p := FieldsHasPrefix("title", "prefix")
	s := Dialect(dialect.MySQL).Select("*").From(Table("posts"))
	p(s)
	q, _ := s.Query()
	require.Contains(t, q, "LIKE")
}

func TestAndPredicatesMultiple(t *testing.T) {
	p1 := FieldEQ("name", "foo")
	p2 := FieldEQ("age", 10)
	combined := AndPredicates(p1, p2)
	s := Dialect(dialect.MySQL).Select("*").From(Table("users"))
	combined(s)
	q, _ := s.Query()
	require.Contains(t, q, "AND")
}

func TestOrPredicatesMultiple(t *testing.T) {
	p1 := FieldEQ("name", "foo")
	p2 := FieldEQ("name", "bar")
	combined := OrPredicates(p1, p2)
	s := Dialect(dialect.MySQL).Select("*").From(Table("users"))
	combined(s)
	q, _ := s.Query()
	require.Contains(t, q, "OR")
}

func TestAndFuncs(t *testing.T) {
	f1 := func(s *Selector) { s.Where(EQ("a", 1)) }
	f2 := func(s *Selector) { s.Where(EQ("b", 2)) }
	combined := AndFuncs(f1, f2)
	s := Dialect(dialect.MySQL).Select("*").From(Table("t"))
	combined(s)
	q, _ := s.Query()
	require.Contains(t, q, "AND")
}

func TestAndFuncsSingle(t *testing.T) {
	f1 := func(s *Selector) { s.Where(EQ("a", 1)) }
	combined := AndFuncs(f1)
	s := Dialect(dialect.MySQL).Select("*").From(Table("t"))
	combined(s)
	q, _ := s.Query()
	require.Contains(t, q, "WHERE")
	require.NotContains(t, q, "AND")
}

func TestOrFuncs(t *testing.T) {
	f1 := func(s *Selector) { s.Where(EQ("a", 1)) }
	f2 := func(s *Selector) { s.Where(EQ("b", 2)) }
	combined := OrFuncs(f1, f2)
	s := Dialect(dialect.MySQL).Select("*").From(Table("t"))
	combined(s)
	q, _ := s.Query()
	require.Contains(t, q, "OR")
}

// ---------------------------------------------------------------------------
// PredicateAnd / PredicateOr / PredicateNot – generic typed predicate helpers
// ---------------------------------------------------------------------------

func TestPredicateAnd(t *testing.T) {
	type UserPredicate func(*Selector)
	p1 := UserPredicate(func(s *Selector) { s.Where(EQ("a", 1)) })
	p2 := UserPredicate(func(s *Selector) { s.Where(EQ("b", 2)) })
	combined := PredicateAnd(p1, p2)
	s := Dialect(dialect.MySQL).Select("*").From(Table("t"))
	combined(s)
	query, _ := s.Query()
	require.Contains(t, query, "AND")
}

func TestPredicateOr(t *testing.T) {
	type UserPredicate func(*Selector)
	p1 := UserPredicate(func(s *Selector) { s.Where(EQ("a", 1)) })
	p2 := UserPredicate(func(s *Selector) { s.Where(EQ("b", 2)) })
	combined := PredicateOr(p1, p2)
	s := Dialect(dialect.MySQL).Select("*").From(Table("t"))
	combined(s)
	query, _ := s.Query()
	require.Contains(t, query, "OR")
}

func TestPredicateNot(t *testing.T) {
	type UserPredicate func(*Selector)
	p1 := UserPredicate(func(s *Selector) { s.Where(EQ("a", 1)) })
	combined := PredicateNot(p1)
	s := Dialect(dialect.MySQL).Select("*").From(Table("t"))
	combined(s)
	query, _ := s.Query()
	require.Contains(t, query, "NOT")
}

func TestPredicateAnd_RetainsType(t *testing.T) {
	// Verify the return type matches the input type (generic constraint ~func(*Selector)).
	type UserPredicate func(*Selector)
	p1 := UserPredicate(func(s *Selector) { s.Where(EQ("x", 1)) })
	result := PredicateAnd(p1)
	require.NotNil(t, result)
}

// ---------------------------------------------------------------------------
// Driver – Open / Close / VarFromContext / WithIntVar / NullScanner
// ---------------------------------------------------------------------------

func TestDriverOpen(t *testing.T) {
	_, err := Open("invalid-driver", "invalid-dsn")
	require.Error(t, err)
}

func TestDriverClose(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectClose()
	drv := OpenDB(dialect.MySQL, db)
	require.NoError(t, drv.Close())
}

func TestVarFromContext(t *testing.T) {
	ctx := context.Background()

	// Missing key
	_, ok := VarFromContext(ctx, "foo")
	require.False(t, ok)

	// Present key
	ctx = WithVar(ctx, "foo", "bar")
	v, ok := VarFromContext(ctx, "foo")
	require.True(t, ok)
	require.Equal(t, "bar", v)
}

func TestWithIntVar(t *testing.T) {
	ctx := WithIntVar(context.Background(), "limit", 100)
	v, ok := VarFromContext(ctx, "limit")
	require.True(t, ok)
	require.Equal(t, "100", v)
}

func TestNullScanner(t *testing.T) {
	ns := &NullScanner{S: new(sql.NullString)}

	// NULL value
	require.NoError(t, ns.Scan(nil))
	require.False(t, ns.Valid)

	// non-NULL value
	require.NoError(t, ns.Scan("hello"))
	require.True(t, ns.Valid)
}

func TestConnExecInvalidArgs(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	c := Conn{ExecQuerier: db, dialect: dialect.MySQL}
	// pass non-[]any as args
	err = c.Exec(context.Background(), "SELECT 1", "not-a-slice", nil)
	require.Error(t, err)
}

func TestConnQueryInvalidArgs(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	c := Conn{ExecQuerier: db, dialect: dialect.MySQL}
	rows := &Rows{}
	err = c.Query(context.Background(), "SELECT 1", "not-a-slice", rows)
	require.Error(t, err)
}

func TestConnQueryInvalidResult(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	c := Conn{ExecQuerier: db, dialect: dialect.MySQL}
	err = c.Query(context.Background(), "SELECT 1", []any{}, "not-rows")
	require.Error(t, err)
}

func TestConnExecInvalidResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(1, 1))
	c := Conn{ExecQuerier: db, dialect: dialect.MySQL}
	err = c.Exec(context.Background(), "UPDATE", []any{}, "not-a-result-ptr")
	require.Error(t, err)
}

func TestDriverBeginTx(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectBegin()
	drv := OpenDB(dialect.MySQL, db)
	tx, err := drv.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, tx)
	mock.ExpectRollback()
	_ = tx.Rollback()
}
