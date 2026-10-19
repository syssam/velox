package sdl

import (
	"fmt"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// Check loads sdl, a velox.graphqls, with gqlparser, beside others: the
// application's own schema files, as gqlgen loads them all. It returns the
// schema, or the first error: a syntax error, a type or directive that
// nothing declares, or any other rule of the GraphQL specification broken.
//
// velox.graphqls declares everything it uses except the directives the
// application puts on elements with graphql.Directives; those the
// application declares in its own files, so a check of the SDL with such
// directives needs those files among others.
func Check(sdl string, others ...string) (*ast.Schema, error) {
	srcs := make([]*ast.Source, 0, 1+len(others))
	srcs = append(srcs, &ast.Source{Name: "velox.graphqls", Input: sdl})
	for i, o := range others {
		srcs = append(srcs, &ast.Source{Name: fmt.Sprintf("other%d.graphqls", i+1), Input: o})
	}
	s, err := gqlparser.LoadSchema(srcs...)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// parse checks the syntax of sdl, which is all the plugin can check on its
// own: the application's directives are declared elsewhere.
func parse(sdl string) error {
	_, err := parser.ParseSchema(&ast.Source{Name: "velox.graphqls", Input: sdl})
	return err
}
