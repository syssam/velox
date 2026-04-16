package sql

import (
	"encoding/json"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// genSnapshot generates the internal/schema.go file with a schema snapshot.
// This is part of the schema/snapshot feature.
func genSnapshot(h gen.GeneratorHelper) *jen.File {
	f := h.NewFile("internal")
	graph := h.Graph()

	f.ImportName("encoding/json", "json")

	// Snapshot struct that contains schema information
	f.Comment("Snapshot stores a snapshot of the schema for auto-solving merge conflicts.")
	f.Type().Id("Snapshot").Struct(
		jen.Id("Nodes").Index().Id("Node").Tag(map[string]string{"json": "nodes"}),
	)

	f.Comment("Node represents a schema node (entity) snapshot.")
	f.Type().Id("Node").Struct(
		jen.Id("Name").String().Tag(map[string]string{"json": "name"}),
		jen.Id("Fields").Index().Id("FieldSnapshot").Tag(map[string]string{"json": "fields"}),
		jen.Id("Edges").Index().Id("EdgeSnapshot").Tag(map[string]string{"json": "edges"}),
	)

	f.Comment("FieldSnapshot represents a field in the schema snapshot.")
	f.Type().Id("FieldSnapshot").Struct(
		jen.Id("Name").String().Tag(map[string]string{"json": "name"}),
		jen.Id("Type").String().Tag(map[string]string{"json": "type"}),
		jen.Id("Optional").Bool().Tag(map[string]string{"json": "optional,omitempty"}),
		jen.Id("Nillable").Bool().Tag(map[string]string{"json": "nillable,omitempty"}),
		jen.Id("Unique").Bool().Tag(map[string]string{"json": "unique,omitempty"}),
		jen.Id("Immutable").Bool().Tag(map[string]string{"json": "immutable,omitempty"}),
	)

	f.Comment("EdgeSnapshot represents an edge in the schema snapshot.")
	f.Type().Id("EdgeSnapshot").Struct(
		jen.Id("Name").String().Tag(map[string]string{"json": "name"}),
		jen.Id("Type").String().Tag(map[string]string{"json": "type"}),
		jen.Id("Unique").Bool().Tag(map[string]string{"json": "unique,omitempty"}),
		jen.Id("Inverse").Bool().Tag(map[string]string{"json": "inverse,omitempty"}),
	)

	// Build the snapshot data
	snapshot := buildSnapshotData(graph)
	snapshotJSON, _ := json.MarshalIndent(snapshot, "", "  ")

	// CurrentSnapshot holds the current schema snapshot
	f.Comment("CurrentSnapshot is the JSON representation of the current schema.")
	f.Comment("This is used for detecting merge conflicts and auto-resolving them.")
	f.Const().Id("CurrentSnapshot").Op("=").Lit(string(snapshotJSON))

	// GetSnapshot function
	f.Comment("GetSnapshot returns the current schema snapshot.")
	f.Func().Id("GetSnapshot").Params().Op("*").Id("Snapshot").Block(
		jen.Var().Id("s").Id("Snapshot"),
		jen.If(jen.Id("err").Op(":=").Qual("encoding/json", "Unmarshal").Call(
			jen.Index().Byte().Parens(jen.Id("CurrentSnapshot")),
			jen.Op("&").Id("s"),
		), jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil()),
		),
		jen.Return(jen.Op("&").Id("s")),
	)

	return f
}

// snapshotNode represents a node for the snapshot
type snapshotNode struct {
	Name   string          `json:"name"`
	Fields []snapshotField `json:"fields"`
	Edges  []snapshotEdge  `json:"edges"`
}

type snapshotField struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Optional  bool   `json:"optional,omitempty"`
	Nillable  bool   `json:"nillable,omitempty"`
	Unique    bool   `json:"unique,omitempty"`
	Immutable bool   `json:"immutable,omitempty"`
}

type snapshotEdge struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Unique  bool   `json:"unique,omitempty"`
	Inverse bool   `json:"inverse,omitempty"`
}

type snapshotData struct {
	Nodes []snapshotNode `json:"nodes"`
}

func buildSnapshotData(graph *gen.Graph) snapshotData {
	data := snapshotData{
		Nodes: make([]snapshotNode, 0, len(graph.Nodes)),
	}

	for _, t := range graph.Nodes {
		node := snapshotNode{
			Name:   t.Name,
			Fields: make([]snapshotField, 0, len(t.Fields)),
			Edges:  make([]snapshotEdge, 0, len(t.Edges)),
		}

		for _, f := range t.Fields {
			fieldType := "unknown"
			if f.Type != nil {
				fieldType = f.Type.Type.String()
			}
			node.Fields = append(node.Fields, snapshotField{
				Name:      f.Name,
				Type:      fieldType,
				Optional:  f.Optional,
				Nillable:  f.Nillable,
				Unique:    f.Unique,
				Immutable: f.Immutable,
			})
		}

		for _, e := range t.Edges {
			node.Edges = append(node.Edges, snapshotEdge{
				Name:    e.Name,
				Type:    e.Type.Name,
				Unique:  e.Unique,
				Inverse: e.IsInverse(),
			})
		}

		data.Nodes = append(data.Nodes, node)
	}

	return data
}
