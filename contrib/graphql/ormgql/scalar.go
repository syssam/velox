package ormgql

import (
	"bytes"
	"fmt"
	"go/token"

	"github.com/syssam/velox/contrib/graphql/graphqlgen"
	"github.com/syssam/velox/internal/ormgen"
)

// scalarDecls are the names the root package declares for a scalar called
// name: the type, and the four functions an engine binds the scalar to.
func scalarDecls(name string) []string {
	return []string{name, "Marshal" + name, "Marshal" + name + "Ptr", "Unmarshal" + name, "Unmarshal" + name + "Ptr"}
}

// scalarNames takes, in the root package, the names of the scalars that
// stand for the Go type of a JSON field. What the package gives of the
// library under one of them is left out, as it is for an entity of that
// name; a name the package declares itself is an error.
func (ext *extension) scalarNames(nodes bool) error {
	g := ext.g
	taken := map[string]string{}
	for _, e := range g.Entities() {
		taken[e.Name] = "the entity " + e.Name
		taken[e.Client] = "the client of the entity " + e.Name
	}
	if nodes {
		for _, n := range []string{"Noder", "NodeType", "NodeTypes"} {
			taken[n] = "what the root package declares for the nodes of the schema"
		}
	}
	for _, s := range ext.scalars {
		if s.PkgPath == "" || !token.IsExported(s.TypeName) {
			return fmt.Errorf("velox: graphql: the scalar %s is the Go type %s, which the generated package cannot name; give the JSON field an exported type of a package", s.Name, s.TypeName)
		}
		if !token.IsExported(s.Name) {
			return fmt.Errorf("velox: graphql: the scalar %s would be a type of the generated package that nothing outside it can name", s.Name)
		}
		for _, n := range scalarDecls(s.Name) {
			if what := taken[n]; what != "" {
				return fmt.Errorf("velox: graphql: the scalar %s needs the name %s in the root package, which is %s", s.Name, n, what)
			}
			taken[n] = "of the scalar " + s.Name
		}
		g.ReserveRoot(scalarDecls(s.Name)...)
	}
	return nil
}

// scalarFiles generates what the root package holds of the scalars: for
// each, the Go type under the scalar's name, which is what the schema binds
// the scalar to, and the functions that write and read its value.
//
// They are in the root package, where velox has them: an application that
// names velox.MarshalAddress keeps doing so.
func (ext *extension) scalarFiles() (map[string][]byte, error) {
	if len(ext.scalars) == 0 {
		return nil, nil
	}
	g := ext.g
	im := g.Imports()
	rt := im.Use(gqlinputPath, "gqlinput")
	var b bytes.Buffer
	for _, s := range ext.scalars {
		ext.scalar(&b, s, im.Use(s.PkgPath, s.PkgName), rt)
	}
	var out bytes.Buffer
	out.WriteString(ormgen.Header)
	fmt.Fprintf(&out, "package %s\n\n", ormgen.RootPackage(g.Package()))
	out.WriteString(im.Block())
	out.WriteString("\n")
	out.Write(b.Bytes())
	return ormgen.Split(out.Bytes(), "scalars_gql")
}

func (ext *extension) scalar(b *bytes.Buffer, s graphqlgen.JSONScalar, pkg, rt string) {
	fmt.Fprintf(b, `// %[1]s is the scalar %[1]s of the GraphQL schema: the Go type of a JSON
// field, under the name the schema binds the scalar to. On the wire it is
// the JSON the column holds.
type %[1]s = %[2]s.%[3]s

// Marshal%[1]s returns a %[1]s as it is on the wire.
func Marshal%[1]s(v %[1]s) ([]byte, error) { return %[4]s.EncodeScalar(v) }

// Marshal%[1]sPtr is Marshal%[1]s for a field that may hold nothing: nil is null.
func Marshal%[1]sPtr(v *%[1]s) ([]byte, error) { return %[4]s.EncodeScalarPtr(v) }

// Unmarshal%[1]s reads a %[1]s as a GraphQL engine hands it over: the value as
// JSON decodes it, or its JSON text (see gqlinput.DecodeScalar).
func Unmarshal%[1]s(v any) (%[1]s, error) { return %[4]s.DecodeScalar[%[1]s](v) }

// Unmarshal%[1]sPtr is Unmarshal%[1]s for a field that may hold nothing: null is nil.
func Unmarshal%[1]sPtr(v any) (*%[1]s, error) { return %[4]s.DecodeScalarPtr[%[1]s](v) }

`, s.Name, pkg, s.TypeName, rt)
}
