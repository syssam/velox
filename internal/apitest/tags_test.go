package apitest

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/fakeconn"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestWithTagsOddCountPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "WithTags") {
			t.Errorf("recovered %v", r)
		}
	}()
	// Three strings, built at run time: staticcheck refuses an odd literal.
	velox.WithTags(context.Background(), strings.Fields("route a user")...)
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
