package velox_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestWithTagsMerges(t *testing.T) {
	bg := context.Background()
	parent := velox.WithTags(bg, "route", "a", "user", "1")
	child := velox.WithTags(parent, "route", "b", "team", "x")
	if got, want := velox.TagsOf(child), []velox.Tag{{"route", "b"}, {"user", "1"}, {"team", "x"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("child: %v, want %v", got, want)
	}
	if got, want := velox.TagsOf(parent), []velox.Tag{{"route", "a"}, {"user", "1"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("parent: %v, want %v", got, want)
	}
	if got := velox.TagsOf(bg); got != nil {
		t.Errorf("no tags: %v", got)
	}
}

func TestWithTagsNoPairs(t *testing.T) {
	bg := context.Background()
	if got := velox.WithTags(bg); got != bg {
		t.Errorf("WithTags(bg) = %v, want bg itself", got)
	}
	parent := velox.WithTags(bg, "route", "a")
	if got := velox.WithTags(parent); got != parent {
		t.Errorf("WithTags(parent) = %v, want parent itself", got)
	}
	if got, want := velox.TagsOf(velox.WithTags(parent)), []velox.Tag{{"route", "a"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("tags %v, want %v", got, want)
	}
}

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
	db := velox.OpenFake(velox.MySQL, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		tags, d = st.Tags, st.Dialect
		return nil
	}))
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(velox.WithTags(ctx, "route", "a")); err != nil {
		t.Fatal(err)
	}
	if want := []velox.Tag{{"route", "a"}}; !reflect.DeepEqual(tags, want) || d != velox.MySQL {
		t.Errorf("Tags %v, Dialect %v; want %v, MySQL", tags, d, want)
	}
}

func TestTagsEditedByAHookStayInTheStatement(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Tags[0].Value = "changed"
		return nil
	}))
	tctx := velox.WithTags(ctx, "route", "a")
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(tctx); err != nil {
		t.Fatal(err)
	}
	if got, want := velox.TagsOf(tctx), []velox.Tag{{"route", "a"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("context's tags: %v, want %v", got, want)
	}
}
