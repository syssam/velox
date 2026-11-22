package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Describe writes the graph: when asJSON, as the indented JSON of the model a
// plugin receives (PluginModel of the graph lowered with the default Config),
// otherwise as text, one block per entity with its fields, then its edges. A
// many-to-many edge through an edge schema says "M2M via" and its name.
func Describe(w io.Writer, g *Graph, asJSON bool) error {
	if asJSON {
		// Not HTML-escaped: a partial index's Where shows "a < b" as written.
		o, err := Lower(g, Config{})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(PluginModel(g, o))
	}
	var buf bytes.Buffer
	for i, e := range g.Entities {
		if i > 0 {
			buf.WriteByte('\n')
		}
		describeEntity(&buf, e)
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// describeEntity writes the block of one entity. The tab-aligned lines are
// written through a tabwriter and stripped of the padding after their last
// cell.
func describeEntity(buf *bytes.Buffer, e *Entity) {
	fmt.Fprintf(buf, "%s (%s)\n", e.Name, e.Table)
	var tab bytes.Buffer
	tw := tabwriter.NewWriter(&tab, 0, 0, 2, ' ', 0)
	for _, f := range e.Fields {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", f.Name, f.Type, f.Column, strings.Join(fieldFlags(f), ", "))
	}
	for _, ed := range e.Edges {
		kind := ed.Rel.Kind
		if ed.Rel.EdgeSchema != "" {
			kind += " via " + ed.Rel.EdgeSchema
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s → %s\t%s\n", ed.Name, kind, ed.Target, relStorage(ed.Rel)+onDelete(ed.Rel))
	}
	_ = tw.Flush() // writes to a bytes.Buffer
	for line := range strings.Lines(tab.String()) {
		buf.WriteString(strings.TrimRight(line, " \n"))
		buf.WriteByte('\n')
	}
}

// onDelete is the text after a relation's storage: its action, when it has one.
func onDelete(r Rel) string {
	if r.OnDelete == "" {
		return ""
	}
	return " on delete " + r.OnDelete
}

// relStorage says where a relation is stored: table.column for a foreign key,
// table(owner column, target column) for a join table.
func relStorage(r Rel) string {
	if r.Kind == "M2M" {
		return r.Table + "(" + strings.Join(r.Columns, ", ") + ")"
	}
	return r.Table + "." + strings.Join(r.Columns, ", ")
}

// fieldFlags lists the properties of a field that the text form shows.
func fieldFlags(f *Field) []string {
	var flags []string
	for _, p := range []struct {
		on   bool
		name string
	}{
		{f.AutoID, "auto"},
		{f.Optional, "optional"},
		{f.Nillable, "nillable"},
		{f.Pointer, "pointer"},
		{f.Unique, "unique"},
		{f.Immutable, "immutable"},
		{f.Version, "version"},
		{f.Sensitive, "sensitive"},
		{f.Implicit != "", "implicit(" + f.Implicit + ")"},
	} {
		if p.on {
			flags = append(flags, p.name)
		}
	}
	return flags
}
