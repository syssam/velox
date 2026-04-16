package gqlrelay

import "errors"

// NodeDescriptor describes an entity node for introspection.
type NodeDescriptor struct {
	ID     any                `json:"id"`
	Type   string             `json:"type"`
	Fields []*FieldDescriptor `json:"fields,omitempty"`
	Edges  []*EdgeDescriptor  `json:"edges,omitempty"`
}

// FieldDescriptor describes a field on a node.
type FieldDescriptor struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value any    `json:"value,omitempty"`
}

// EdgeDescriptor describes an edge on a node.
type EdgeDescriptor struct {
	Name string `json:"name"`
	Type string `json:"type"`
	IDs  []any  `json:"ids,omitempty"`
}

// ErrNodeNotFound is returned when a node cannot be resolved.
var ErrNodeNotFound = errors.New("could not resolve to a node with the global ID")
