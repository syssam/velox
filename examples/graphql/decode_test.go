package main

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/examples/graphql/veloxgen"
	"github.com/syssam/velox/examples/graphql/veloxschema"
)

// TestDecodeWhere: a kept filter, a JSON document of PostWhereInput by its
// GraphQL names, decodes with veloxgql.DecodeWhere into the generated input
// and selects with its Pred the posts it says. An ID arrives as a string,
// publishedAt is the GraphQL name of published_at, and hasTagsWith takes the
// where input of Label, Tag's GraphQL name. A key the input lacks is an
// error.
func TestDecodeWhere(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", sqliteDSN(filepath.Join(t.TempDir(), "decode.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := open(t.Context(), sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	org := veloxschema.OrgKey.With(t.Context(), acme)
	for _, c := range []struct {
		doc  string
		want []int64
	}{
		{`{"authorID": "1"}`, []int64{1, 2}},
		{`{"not": {"idIn": ["2", "3"]}}`, []int64{1, 4}},
		{`{"status": "DRAFT"}`, []int64{2}},
		{`{"publishedAtGTE": "2026-05-01T00:00:00Z"}`, []int64{3, 4}},
		{`{"or": [{"publishedAtIsNil": true}, {"hasTagsWith": [{"name": "sql"}]}]}`, []int64{2, 3}},
	} {
		var w veloxgen.PostWhereInput
		if err := veloxgql.DecodeWhere([]byte(c.doc), &w); err != nil {
			t.Errorf("%s: %v", c.doc, err)
			continue
		}
		p, err := w.Pred(org)
		if err != nil {
			t.Errorf("%s: Pred: %v", c.doc, err)
			continue
		}
		rows, err := db.Query[veloxgen.Post]().Where(p).AllowUnbounded().All(org)
		if err != nil {
			t.Fatal(err)
		}
		var got []int64
		for _, r := range rows {
			got = append(got, r.ID)
		}
		slices.Sort(got)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s selects %v, want %v", c.doc, got, c.want)
		}
	}
	var w veloxgen.PostWhereInput
	err = veloxgql.DecodeWhere(map[string]any{"hasTagsWith": []any{map[string]any{"label": "go"}}}, &w)
	if want := `veloxgql: hasTagsWith[0].label: LabelWhereInput has no field "label"`; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("an unknown key: %v, want %q", err, want)
	}
}
