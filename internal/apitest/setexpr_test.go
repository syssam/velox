package apitest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/internal/testmodel/actor"
	"github.com/syssam/velox/untyped"
)

// TestSetExprSQL: SetExpr writes its text in parentheses after the column,
// a handle as the column and any other argument bound, in each dialect,
// beside the other assignments and the UpdateDefault one.
func TestSetExprSQL(t *testing.T) {
	for _, tc := range []struct {
		d    velox.Dialect
		want string
	}{
		{velox.Postgres, `UPDATE "invoices" SET "total" = ("total" * $1), "note" = (LOWER("number") || $2), "paid" = $3, "updated_at" = $4 WHERE "id" = $5`},
		{velox.MySQL, "UPDATE `invoices` SET `total` = (`total` * ?), `note` = (LOWER(`number`) || ?), `paid` = ?, `updated_at` = ? WHERE `id` = ?"},
		{velox.SQLite, `UPDATE "invoices" SET "total" = ("total" * ?), "note" = (LOWER("number") || ?), "paid" = ?, "updated_at" = ? WHERE "id" = ?`},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		got, args, err := db.Update[inv]().Where(I.ID.EQ(7)).Set(
			velox.SetExpr(I.Total, "? * ?", I.Total, 2),
			velox.SetExpr(I.Note, "LOWER(?) || ?", I.Number, "-x"),
			I.Paid.Set(true),
		).SQL(ctx)
		if err != nil || got != tc.want {
			t.Errorf("%v:\n got %s %v\nwant %s", tc.d, got, err, tc.want)
			continue
		}
		if len(args) != 5 || args[0] != 2 || args[1] != "-x" || args[2] != true || args[4] != int64(7) {
			t.Errorf("%v: args %v", tc.d, args)
		}
	}
}

// TestSetExprRenderCache: two expressions of different text are two shapes;
// the same text with other values is one.
func TestSetExprRenderCache(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	upd := func(text string, v any) string {
		got, _, err := db.Update[inv]().Where(I.ID.EQ(1)).Set(velox.SetExpr(I.Total, text, I.Total, v)).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	a, b, c := upd("? * ?", 2), upd("? + ?", 2), upd("? * ?", 3)
	if a == b || !strings.Contains(b, `("total" + $1)`) {
		t.Errorf("another text, same SQL:\n%s\n%s", a, b)
	}
	if a != c {
		t.Errorf("same text, other SQL:\n%s\n%s", a, c)
	}
}

// TestSetExprRefused: what SetExpr cannot assign fails the statement before
// anything is sent.
func TestSetExprRefused(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	J := fixture.JDocs
	for name, c := range map[string]struct {
		run  func() error
		want string
	}{
		"subquery": {func() error {
			_, err := db.Update[inv]().Where(I.ID.EQ(1)).Set(velox.SetExpr(I.Total, "(SELECT 1)")).Exec(ctx)
			return err
		}, "SELECT"},
		"semicolon": {func() error {
			_, err := db.Update[inv]().Where(I.ID.EQ(1)).Set(velox.SetExpr(I.Total, "1; DROP TABLE x")).Exec(ctx)
			return err
		}, `';'`},
		"arguments": {func() error {
			_, err := db.Update[inv]().Where(I.ID.EQ(1)).Set(velox.SetExpr(I.Total, "? * ?", I.Total)).Exec(ctx)
			return err
		}, "2 ?, and 1 arguments"},
		"another entity's column": {func() error {
			_, err := db.Update[inv]().Where(I.ID.EQ(1)).Set(velox.SetExpr(I.Total, "?", It.ID)).Exec(ctx)
			return err
		}, "another entity"},
		"aggregate": {func() error {
			_, err := db.Update[inv]().Where(I.ID.EQ(1)).Set(velox.SetExpr(I.Total, "?", I.Total.Sum())).Exec(ctx)
			return err
		}, "aggregate"},
		"immutable": {func() error {
			_, err := db.Update[inv]().Where(I.ID.EQ(1)).Set(velox.SetExpr(I.OrgID, "? + 1", I.OrgID)).Exec(ctx)
			if !errors.Is(err, velox.ErrImmutable) {
				t.Errorf("immutable: %v is not ErrImmutable", err)
			}
			return err
		}, "OrgID"},
		"version": {func() error {
			P := testmodel.Papers
			_, err := db.Update[testmodel.Paper]().Where(P.Title.EQ("a")).Set(velox.SetExpr(P.Version, "? + 5", P.Version)).Exec(ctx)
			if !errors.Is(err, velox.ErrVersionSet) {
				t.Errorf("version: %v is not ErrVersionSet", err)
			}
			return err
		}, "version"},
		"encoded column": {func() error {
			_, err := db.Update[fixture.JDoc]().Where(J.ID.EQ(1)).Set(velox.SetExpr(J.Tags, "?", "[]")).Exec(ctx)
			return err
		}, "encoded column"},
		"insert": {func() error {
			err := db.Insert(&testmodel.Invoice{Number: "A"}).Set(velox.SetExpr(I.Total, "1 + 1")).Exec(ctx)
			return err
		}, "insert"},
		"key read back by One": {func() error {
			_, err := db.Update[inv]().Where(I.ID.EQ(1)).Set(velox.SetExpr(I.ID, "? + 1", I.ID)).One(ctx)
			return err
		}, "expression"},
	} {
		e.SQL = ""
		err := c.run()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want an error with %q", name, err, c.want)
		}
		if e.SQL != "" {
			t.Errorf("%s: sent %s", name, e.SQL)
		}
	}
}

// TestSetExprHookSeesNoValue: a hook's Mutation.Value reports false for a
// column an expression assigns, as velox cannot know its value.
func TestSetExprHookSeesNoValue(t *testing.T) {
	var got []bool
	hook := velox.Before(func(_ context.Context, m *velox.Mutation) error {
		_, a := m.Value("total")
		_, b := m.Value("paid")
		got = append(got, a, b)
		return nil
	}, velox.OpUpdate)
	db := openFake(velox.Postgres, &fakeconn.Exec{}, velox.WithHooks[inv](hook))
	_, err := db.Update[inv]().Where(I.ID.EQ(1)).Set(velox.SetExpr(I.Total, "? * 2", I.Total), I.Paid.Set(true)).Exec(ctx)
	if err != nil || len(got) != 2 || got[0] || !got[1] {
		t.Errorf("Value: %v, %v; want total false, paid true", got, err)
	}
}

// TestSetExprSQLite runs SetExpr on SQLite: the expression reads the row's
// own columns, the Write policy keeps the update to the context's
// organization, and One reads the computed value back.
func TestSetExprSQLite(t *testing.T) {
	_, raw := sqliteInvoices(t, "CURRENT_TIMESTAMP")
	for _, r := range []string{
		`(1, 1, 'a', 10, false, '', x'')`,
		`(2, 1, 'b', 20, false, '', x'')`,
		`(3, 2, 'c', 30, false, '', x'')`,
	} {
		if _, err := raw.Exec(`INSERT INTO invoices (id, org_id, number, total, paid, note, data) VALUES ` + r); err != nil {
			t.Fatal(err)
		}
	}
	db := velox.Open(velox.SQLite, raw)
	octx := actor.Org.With(ctx, 1)
	n, err := db.Update[inv]().All().Set(
		velox.SetExpr(I.Total, "? * ? + ?", I.Total, 2, 1),
		velox.SetExpr(I.Note, "UPPER(?) || ?", I.Number, "!"),
	).Exec(octx)
	if err != nil || n != 2 {
		t.Fatalf("update: %d, %v; want the 2 rows of org 1", n, err)
	}
	type row struct {
		total float64
		note  string
	}
	want := map[int64]row{1: {21, "A!"}, 2: {41, "B!"}, 3: {30, ""}}
	rs, err := raw.Query(`SELECT id, total, note FROM invoices`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		var id int64
		var r row
		if err := rs.Scan(&id, &r.total, &r.note); err != nil {
			t.Fatal(err)
		}
		if want[id] != r {
			t.Errorf("row %d: %+v, want %+v", id, r, want[id])
		}
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	got, err := db.Update[inv]().Where(I.ID.EQ(1)).Set(velox.SetExpr(I.Total, "? - ?", I.Total, 1)).One(octx)
	if err != nil || got.Total != 20 {
		t.Errorf("One: %+v, %v; want total 20", got, err)
	}
	if _, err := db.Update[inv]().Where(I.ID.EQ(3)).Set(velox.SetExpr(I.Total, "0")).One(octx); !errors.Is(err, velox.ErrNotFound) {
		t.Errorf("another organization's row: %v, want ErrNotFound", err)
	}
}

// TestSetExprUntyped: untyped.SetExpr is velox.SetExpr with a column index
// and Terms, for TableUpdate.Assign, and renders the same.
func TestSetExprUntyped(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	typed, _, err := db.Update[inv]().Where(I.ID.EQ(7)).Set(velox.SetExpr(I.Total, "? * ?", I.Total, 2)).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tb := untyped.TableOf[inv]()
	total := int(I.Total.I)
	got, args, err := untyped.Update(db, tb).
		Assign(untyped.SetExpr(total, "? * ?", untyped.Col(total), 2)).
		Where(tb.Pred(int(I.ID.I), untyped.PredEQ, int64(7))).SQL(ctx)
	if err != nil || got != typed || args[0] != 2 {
		t.Errorf("untyped:\n got %s %v %v\nwant %s", got, args, err, typed)
	}
	_, err = untyped.Update(db, tb).Assign(untyped.SetExpr(total, "(SELECT 1)")).All().Exec(ctx)
	if err == nil || !strings.Contains(err.Error(), "SELECT") {
		t.Errorf("untyped subquery: %v", err)
	}
}
