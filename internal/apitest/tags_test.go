package apitest

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	velox "github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/fakeconn"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestWithTagsOddCountTakesAnEmptyValue(t *testing.T) {
	var tags []velox.Tag
	db := openFake(velox.MySQL, &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		tags = st.Tags
		return nil
	}))
	// Three strings, built at run time: staticcheck refuses an odd literal.
	ctx := velox.WithTags(context.Background(), strings.Fields("route a user")...)
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := []velox.Tag{{Key: "route", Value: "a"}, {Key: "user"}}; !slices.Equal(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
}

func TestHooksSeeTheTags(t *testing.T) {
	var tags []velox.Tag
	var d velox.Dialect
	db := openFake(velox.MySQL, &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		tags, d = st.Tags, st.Dialect
		return nil
	}))
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(velox.WithTags(ctx, "route", "a")); err != nil {
		t.Fatal(err)
	}
	if want := []velox.Tag{{Key: "route", Value: "a"}}; !reflect.DeepEqual(tags, want) || d != velox.MySQL {
		t.Errorf("Tags %v, Dialect %v; want %v, MySQL", tags, d, want)
	}
}
