package ormgen

import (
	"bytes"
	"fmt"
)

// predicates generates the package below the per-entity packages and package entity:
// for each entity the types of its predicates, setters and order terms. It
// holds no field, so a schema change that adds or changes a field
// leaves it, and with it every entity package but one, untouched.
func (g *generator) predicates() ([]byte, error) {
	var b bytes.Buffer
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	for _, e := range g.ents {
		w(`// %[1]s is a predicate of %[1]s. It is a type of its own, as are the
// assignments and order terms below, so those of another entity do not
// compile in a query or mutation of this one.
type %[1]s orm.P

// %[2]s is an assignment of a mutation of %[1]s.
type %[2]s orm.S

// %[3]s is an order term of a query of %[1]s.
type %[3]s orm.O

`, e.Name, e.pnames[0], e.pnames[1])
	}
	var out bytes.Buffer
	out.WriteString(header)
	out.WriteString("// Package predicate holds the typed predicates, setters and order terms of every\n// entity.\npackage predicate\n\nimport \"" + ormPath + "\"\n\n")
	out.Write(b.Bytes())
	return formatted(&out, "predicate/predicate.go")
}
