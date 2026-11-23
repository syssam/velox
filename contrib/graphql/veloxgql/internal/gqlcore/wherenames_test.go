package gqlcore

import (
	"context"
	"reflect"
	"testing"
)

func TestWhereNamesSlot(t *testing.T) {
	names := func() WhereNames { return WhereNames{Input: "PostWhereInput", Fields: []string{"not", "idIn"}} }
	ctx, slot := AskWhereNames(context.Background())
	TellWhereNames(ctx, names)
	if want := names(); !reflect.DeepEqual(*slot, want) {
		t.Errorf("slot %+v, want %+v", *slot, want)
	}
	// A context without a slot is left alone, and names is not called.
	TellWhereNames(context.Background(), func() WhereNames {
		t.Error("names called without a slot")
		return WhereNames{}
	})
}

func TestGraphQLExtensionsIsExtensions(t *testing.T) {
	e := &Error{Code: CodeBadUserInput}
	if got, want := e.GraphQLExtensions(), e.Extensions(); !reflect.DeepEqual(got, want) {
		t.Errorf("GraphQLExtensions %v, want %v", got, want)
	}
}
