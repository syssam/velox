package velox_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func TestRewriteCanRefuse(t *testing.T) {
	refused := errors.New("refused")
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(context.Context, *velox.Statement) error { return refused }))
	if _, err := db.Query[testmodel.Invoice]().All(ctx); !errors.Is(err, refused) || e.Calls != 0 {
		t.Fatalf("%v, calls %d", err, e.Calls)
	}
}

func TestAroundsWrapInOrder(t *testing.T) {
	var trace []string
	around := func(name string) velox.Around {
		return func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			if st.SQL == "" {
				t.Error("around ran before rendering")
			}
			trace = append(trace, name+">")
			err := next(ctx)
			trace = append(trace, "<"+name)
			return err
		}
	}
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithAround(around("a")), velox.WithAround(around("b")))
	if _, err := db.Query[testmodel.Invoice]().All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := []string{"a>", "b>", "<b", "<a"}; !reflect.DeepEqual(trace, want) {
		t.Errorf("%v, want %v", trace, want)
	}
}

func TestAroundMustCallNext(t *testing.T) {
	lazy := func(context.Context, *velox.Statement, func(context.Context) error) error { return nil }
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, velox.WithAround(lazy))
	if _, err := db.Query[testmodel.Invoice]().All(ctx); err == nil || !strings.Contains(err.Error(), "without calling next") {
		t.Fatalf("%v", err)
	}
}
