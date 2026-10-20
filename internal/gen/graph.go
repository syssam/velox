// Package gen resolves the model of a schema package into a Graph: the
// entities with their tables, Go names, IDs, relations and foreign keys. Build
// also checks the rules a model must keep. velox-gen describes the Graph and
// generates code from it.
package gen

import (
	"encoding/json"

	"github.com/syssam/velox2/schema/load"
)

// Graph is the resolved model of one schema package.
type Graph struct {
	Package  string    `json:"package"`  // the schema package's import path
	Entities []*Entity `json:"entities"` // sorted by Name
}

// Entity is one schema, resolved.
type Entity struct {
	Name        string                     `json:"name"`
	Label       string                     `json:"label"` // plugin.Snake(Name)
	Table       string                     `json:"table"`
	TableSchema string                     `json:"table_schema,omitempty"`
	Fields      []*Field                   `json:"fields"` // Fields[0] is the ID; then the model's order; then implicit foreign keys
	Edges       []*Edge                    `json:"edges"`
	Indexes     []*Index                   `json:"indexes,omitempty"`
	Tenant      string                     `json:"tenant,omitempty"` // the tenant field's Name
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
}

// Field is a field of an entity: a field of the model, the ID velox adds when
// the schema declares none, or a foreign key velox adds for an edge.
//
// Position, from load.Field, is meaningful only for a declared field: one with
// !AutoID && Implicit == "". The ID velox adds and the implicit foreign keys
// have no position in the schema and leave it zero.
type Field struct {
	load.Field
	GoName   string `json:"go_name"`
	Column   string `json:"column"`
	Pointer  bool   `json:"pointer,omitempty"`  // Nillable: the Go field is a pointer, unless its type's zero value is nil already
	AutoID   bool   `json:"auto_id,omitempty"`  // the ID velox added
	Implicit string `json:"implicit,omitempty"` // the edge whose foreign key this field holds, when velox added it
}

// Edge is an edge of an entity, with its relation resolved.
type Edge struct {
	Name        string                     `json:"name"`
	GoName      string                     `json:"go_name"`
	Target      string                     `json:"target"`
	Unique      bool                       `json:"unique,omitempty"`
	Required    bool                       `json:"required,omitempty"`
	Inverse     bool                       `json:"inverse,omitempty"`
	Bidi        bool                       `json:"bidi,omitempty"`
	Ref         string                     `json:"ref,omitempty"` // the paired edge on Target
	Rel         Rel                        `json:"rel"`
	Field       string                     `json:"field,omitempty"`     // the foreign-key field, on whichever entity's table holds the column; "" for M2M
	Immutable   bool                       `json:"immutable,omitempty"` // the edge cannot be changed after create
	Symbols     []string                   `json:"symbols,omitempty"`   // the foreign-key constraint names: one, or two for M2M
	Comment     string                     `json:"comment,omitempty"`
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
}

// Rel is the relation an edge stands for, and where it is stored.
type Rel struct {
	Kind    string   `json:"kind"`    // "O2O", "O2M", "M2O", "M2M"
	Table   string   `json:"table"`   // the table holding the foreign key, or the join table
	Columns []string `json:"columns"` // the foreign key; for M2M the join table's owner and target columns
}

// Index is an index of an entity's table.
type Index struct {
	Name        string                     `json:"name"`
	Columns     []string                   `json:"columns"`
	Unique      bool                       `json:"unique,omitempty"`
	Where       string                     `json:"where,omitempty"`
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
}

// Error is one rule a schema breaks. Build joins every Error it finds.
type Error struct {
	Schema string // the schema type's name
	Where  string // `field "name"`, `edge "posts"`, `index 0`, or ""
	Reason string
}

// Error returns `schema User: field "name": reason`, or `schema User: reason`
// when Where is "".
func (e *Error) Error() string {
	if e.Where == "" {
		return "schema " + e.Schema + ": " + e.Reason
	}
	return "schema " + e.Schema + ": " + e.Where + ": " + e.Reason
}

// Entity returns the entity named name, or nil.
func (g *Graph) Entity(name string) *Entity {
	for _, e := range g.Entities {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// Field returns the entity's first field named name, or nil.
func (e *Entity) Field(name string) *Field {
	for _, f := range e.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// Edge returns the entity's first edge named name, or nil.
func (e *Entity) Edge(name string) *Edge {
	for _, ed := range e.Edges {
		if ed.Name == name {
			return ed
		}
	}
	return nil
}
