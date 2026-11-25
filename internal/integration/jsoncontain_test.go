package integration

import (
	"database/sql"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
)

// jsonContainDocs are the attrs documents of TestJSONContainParity, by id:
// a match, matches of the same value written otherwise, nested arrays,
// values of other types, missing keys, a scalar and an array on the path,
// an array as the document, and a numeric key into an array.
var jsonContainDocs = map[int64]string{
	1:  `{"p": {"t": "pro", "n": 1.50, "b": true, "z": null}}`,
	2:  `{"p": {"t": "Pro", "n": 1.5, "b": false}}`,
	3:  `{"p": {"t": ["pro"], "n": [1.5], "b": [true], "z": [null]}}`,
	4:  `{"p": {"t": 5, "n": "1.5", "b": "true", "z": 0}}`,
	5:  `{"p": {}}`,
	6:  `{"q": 1}`,
	7:  `{"p": "pro"}`,
	8:  `{"p": {"t": "pro", "n": 2, "b": true, "z": null, "extra": 1}}`,
	9:  `["pro"]`,
	10: `{"p": [{"t": "pro"}]}`,
	11: `{"p": {"n": 1e2}}`,
	12: `{"p": {"n": 9007199254740993}}`,
	13: `{"a": ["x", "y"]}`,
}

// TestJSONContainParity: Text's, Number's and Bool's EQ, Text's In and
// ValueIsNull keep the same rows on every dialect, though PostgreSQL writes
// them as containment in a filter under an even number of NOTs and in the
// typed form under an odd number: a negated condition still matches only
// rows whose path holds a value of the type.
func TestJSONContainParity(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		for id := int64(1); id <= int64(len(jsonContainDocs)); id++ {
			tg.exec(t, "INSERT INTO json_recs (id, tags, attrs, shape) VALUES (?, '[]', ?, '{}')", id, jsonContainDocs[id])
		}
		ids := func(p velox.Pred[jrec]) []int64 {
			t.Helper()
			// The id alone is read: document 9 is no object.
			rows, err := tg.db.Query[jrec]().Columns(JR.ID).Where(p).OrderBy(JR.ID.Asc()).AllowUnbounded().All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var got []int64
			for _, r := range rows {
				got = append(got, r.ID)
			}
			return got
		}
		pt, pn, pb := JR.Attrs.Text("p", "t"), JR.Attrs.Number("p", "n"), JR.Attrs.Bool("p", "b")
		for name, tc := range map[string]struct {
			p    velox.Pred[jrec]
			want []int64
		}{
			"text EQ":              {pt.EQ("pro"), []int64{1, 8}},
			"text EQ, case":        {pt.EQ("Pro"), []int64{2}},
			"text In":              {pt.In("pro", "Pro", "x"), []int64{1, 2, 8}},
			"not text EQ":          {velox.Not(pt.EQ("pro")), []int64{2}},
			"not not text EQ":      {velox.Not(velox.Not(pt.EQ("pro"))), []int64{1, 8}},
			"not text In":          {velox.Not(pt.In("pro", "Pro")), nil},
			"or, a negated branch": {velox.Or(pt.EQ("pro"), velox.Not(pb.EQ(true))), []int64{1, 2, 8}},
			"and":                  {velox.And(pt.EQ("pro"), pn.EQ(2)), []int64{8}},
			"not and":              {velox.Not(velox.And(pt.EQ("pro"), pn.EQ(2))), []int64{1, 2, 11, 12}},
			"number EQ, 1.50":      {pn.EQ(1.5), []int64{1, 2}},
			"number EQ, 1e2":       {pn.EQ(100), []int64{11}},
			"not number EQ":        {velox.Not(pn.EQ(1.5)), []int64{8, 11, 12}},
			"bool EQ true":         {pb.EQ(true), []int64{1, 8}},
			"bool EQ false":        {pb.EQ(false), []int64{2}},
			"not bool EQ":          {velox.Not(pb.EQ(true)), []int64{2}},
			"value is null":        {JR.Attrs.ValueIsNull("p", "z"), []int64{1, 8}},
			"not value is null":    {velox.Not(JR.Attrs.ValueIsNull("p", "z")), []int64{2, 3, 4, 5, 6, 7, 9, 10, 11, 12, 13}},
			"not not value null":   {velox.Not(velox.Not(JR.Attrs.ValueIsNull("p", "z"))), []int64{1, 8}},
			"scalar on the path":   {JR.Attrs.Text("p").EQ("pro"), []int64{7}},
			"array on the path":    {JR.Attrs.Text("p", "t").EQ("pro"), []int64{1, 8}},
		} {
			if got := ids(tc.p); !slices.Equal(got, tc.want) {
				t.Errorf("%s: ids %v, want %v", name, got, tc.want)
			}
		}
		// An update's and a delete's WHERE are filters too.
		if n, err := tg.db.Update[jrec]().Set(JR.Tags.Set([]string{"paid"})).Where(pt.EQ("pro")).Exec(ctx); err != nil || n != 2 {
			t.Errorf("update: %d, %v", n, err)
		}
		if n, err := tg.db.Delete[jrec]().Where(velox.Not(velox.Not(pb.EQ(false)))).Exec(ctx); err != nil || n != 1 {
			t.Errorf("delete: %d, %v", n, err)
		}
		// A numeric key: jsonb_extract_path reads it as an array index on
		// PostgreSQL, where velox keeps the typed form; MySQL's and
		// SQLite's paths read object keys only.
		var idx []int64
		if tg.d == velox.Postgres {
			idx = []int64{13}
		}
		if got := ids(JR.Attrs.Text("a", "0").EQ("x")); !slices.Equal(got, idx) {
			t.Errorf("index key: ids %v, want %v", got, idx)
		}
		if tg.d != velox.Postgres {
			return
		}
		// PostgreSQL compares a number as numeric with the float64's
		// shortest decimal, in both forms: 9007199254740993 is not
		// 9007199254740992, and NaN matches nothing.
		big := float64(1 << 53)
		if got := ids(pn.EQ(big)); got != nil {
			t.Errorf("big EQ: %v", got)
		}
		if got := ids(velox.Not(pn.EQ(big))); !slices.Equal(got, []int64{1, 8, 11, 12}) {
			t.Errorf("not big EQ: %v", got)
		}
		if got := ids(pn.EQ(math.NaN())); got != nil {
			t.Errorf("NaN: %v", got)
		}
	})
}

// TestJSONContainIndex: on PostgreSQL, a GIN index of the jsonb_path_ops
// class serves Text's EQ and In, ValueIsNull, an array's Has and HasSome,
// and an Or of Has and EQ, as the plan with sequential scans off shows.
func TestJSONContainIndex(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		if tg.d != velox.Postgres {
			t.Skip("PostgreSQL's GIN index")
		}
		jsonSetup(t, tg)
		tg.exec(t, "CREATE INDEX json_recs_attrs_gin ON json_recs USING gin (attrs jsonb_path_ops)")
		tg.exec(t, "INSERT INTO json_recs (id, tags, attrs, shape) VALUES (1, '[]', '{\"plan\": \"pro\", \"tags\": [\"a\"]}', '{}')")
		for name, p := range map[string]velox.Pred[jrec]{
			"text EQ":       JR.Attrs.Text("plan").EQ("pro"),
			"text In":       JR.Attrs.Text("plan").In("pro", "team"),
			"value is null": JR.Attrs.ValueIsNull("plan"),
			"number EQ":     JR.Attrs.Number("n").EQ(2),
			"bool EQ":       JR.Attrs.Bool("on").EQ(true),
			"array Has":     JR.Attrs.Array("tags").Has("a"),
			"array HasSome": JR.Attrs.Array("tags").HasSome("a", "b"),
			"Has or EQ":     velox.Or(JR.Attrs.Array("tags").Has("a"), JR.Attrs.Text("tags").EQ("a")),
		} {
			text, args, err := tg.db.Query[jrec]().Columns(JR.ID).Where(p).Limit(10).SQL(ctx)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := explainNoSeqScan(tg.direct, text, args)
			if err != nil {
				t.Fatalf("%s: %v\n%s", name, err, text)
			}
			if !strings.Contains(plan, "json_recs_attrs_gin") {
				t.Errorf("%s: the plan does not use the index:\n%s\n%s", name, text, plan)
			}
		}
	})
}

// explainNoSeqScan is PostgreSQL's plan of the query text with args, with
// sequential scans off, so that an index is used wherever one can be.
func explainNoSeqScan(db *sql.DB, text string, args []any) (string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		return "", err
	}
	rows, err := tx.QueryContext(ctx, "EXPLAIN "+text, args...)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", err
		}
		plan = append(plan, line)
	}
	return strings.Join(plan, "\n"), rows.Err()
}
