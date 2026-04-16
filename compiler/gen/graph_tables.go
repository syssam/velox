package gen

import (
	"fmt"

	"github.com/syssam/velox/dialect/sql/schema"
	"github.com/syssam/velox/schema/field"
)

// Tables returns the schema definitions of SQL tables for the graph.
func (g *Graph) Tables() (all []*schema.Table, err error) {
	// --- Build entity tables from graph nodes ---
	tables := make(map[string]*schema.Table)
	for _, n := range g.MutableNodes() {
		table := schema.NewTable(n.Table()).
			SetComment(n.sqlComment()).
			SetPos(n.Pos())
		if n.HasOneFieldID() {
			table.AddPrimary(n.ID.PK())
		}
		switch ant := n.EntSQL(); {
		case ant == nil:
		case ant.Skip:
			continue
		default:
			table.SetAnnotation(ant).SetSchema(ant.Schema)
		}
		for _, f := range n.Fields {
			if a := f.EntSQL(); a != nil && a.Skip {
				continue
			}
			if !f.IsEdgeField() {
				table.AddColumn(f.Column())
			}
		}
		switch {
		case tables[table.Name] == nil:
			tables[table.Name] = table
			all = append(all, table)
		case tables[table.Name].Schema != table.Schema:
			return nil, fmt.Errorf("cannot use the same table name %q in different schemas: %q, %q", table.Name, tables[table.Name].Schema, table.Schema)
		default:
			return nil, fmt.Errorf("duplicate table name %q in schema %q", table.Name, table.Schema)
		}
	}
	// --- Add foreign keys and join tables for edges ---
	for _, n := range g.Nodes {
		// Foreign key and its reference, or a join table.
		for _, e := range n.Edges {
			if e.IsInverse() {
				continue
			}
			switch e.Rel.Type {
			case O2O, O2M:
				// The "owner" is the table that owns the relation (we set
				// the foreign-key on) and "ref" is the referenced table.
				owner, ref := tables[e.Rel.Table], tables[n.Table()]
				column := fkColumn(e, owner, ref.PrimaryKey[0])
				// If it's not a circular reference (self-referencing table),
				// and the inverse edge is required, make it non-nullable.
				if n != e.Type && e.Ref != nil && !e.Ref.Optional {
					column.Nullable = false
				}
				mayAddColumn(owner, column)
				owner.AddForeignKey(&schema.ForeignKey{
					RefTable:   ref,
					OnDelete:   deleteAction(e, column),
					Columns:    []*schema.Column{column},
					RefColumns: []*schema.Column{ref.PrimaryKey[0]},
					Symbol:     fkSymbol(e, owner, ref),
				})
			case M2O:
				ref, owner := tables[e.Type.Table()], tables[e.Rel.Table]
				column := fkColumn(e, owner, ref.PrimaryKey[0])
				// If it's not a circular reference (self-referencing table),
				// and the edge is non-optional (required), make it non-nullable.
				if n != e.Type && !e.Optional {
					column.Nullable = false
				}
				mayAddColumn(owner, column)
				owner.AddForeignKey(&schema.ForeignKey{
					RefTable:   ref,
					OnDelete:   deleteAction(e, column),
					Columns:    []*schema.Column{column},
					RefColumns: []*schema.Column{ref.PrimaryKey[0]},
					Symbol:     fkSymbol(e, owner, ref),
				})
			case M2M:
				// If there is an edge schema for the association (i.e. edge.Through).
				if e.Through != nil || (e.Ref != nil && e.Ref.Through != nil) {
					continue
				}
				// M2M edges require exactly 2 columns for join table
				if len(e.Rel.Columns) < 2 {
					return nil, fmt.Errorf("M2M edge %q on type %q has insufficient columns for join table (got %d, need 2)", e.Name, n.Name, len(e.Rel.Columns))
				}
				t1, t2 := tables[n.Table()], tables[e.Type.Table()]
				if len(t1.PrimaryKey) == 0 || len(t2.PrimaryKey) == 0 {
					return nil, fmt.Errorf("M2M edge %s.%s requires both related types to have a primary key", n.Name, e.Name)
				}
				c1 := &schema.Column{Name: e.Rel.Columns[0], Type: field.TypeInt, SchemaType: n.ID.def.SchemaType}
				if ref := n.ID; ref.UserDefined {
					c1.Type = ref.Type.Type
					c1.Size = ref.size()
				}
				c2 := &schema.Column{Name: e.Rel.Columns[1], Type: field.TypeInt, SchemaType: e.Type.ID.def.SchemaType}
				if ref := e.Type.ID; ref.UserDefined {
					c2.Type = ref.Type.Type
					c2.Size = ref.size()
				}
				ant := e.EntSQL()
				s1, s2 := fkSymbols(e, c1, c2)
				all = append(all, &schema.Table{
					Name: e.Rel.Table,
					// Join tables get the position of the edge owner.
					Pos: n.Pos(),
					// Search for edge annotation, or
					// default to edge owner annotation.
					Schema: func() string {
						if ant != nil && ant.Schema != "" {
							return ant.Schema
						}
						if sqlAnt := n.EntSQL(); sqlAnt != nil && sqlAnt.Schema != "" {
							return sqlAnt.Schema
						}
						return ""
					}(),
					Annotation: ant,
					Columns:    []*schema.Column{c1, c2},
					PrimaryKey: []*schema.Column{c1, c2},
					ForeignKeys: []*schema.ForeignKey{
						{
							RefTable:   t1,
							OnDelete:   schema.Cascade,
							Columns:    []*schema.Column{c1},
							RefColumns: []*schema.Column{t1.PrimaryKey[0]},
							Symbol:     s1,
						},
						{
							RefTable:   t2,
							OnDelete:   schema.Cascade,
							Columns:    []*schema.Column{c2},
							RefColumns: []*schema.Column{t2.PrimaryKey[0]},
							Symbol:     s2,
						},
					},
				})
			}
		}
		if n.HasCompositeID() {
			if err := addCompositePK(tables[n.Table()], n); err != nil {
				return nil, err
			}
		}
	}
	// --- Append indexes after all columns (including FK columns) are added ---
	for _, n := range g.Nodes {
		table := tables[n.Table()]
		if table == nil {
			continue // Skip views — they don't have table entries
		}
		for _, idx := range n.Indexes {
			table.AddIndex(idx.Name, idx.Unique, idx.Columns)
			// Set the sqlschema.IndexAnnotation from the schema if exists.
			index, _ := table.Index(idx.Name)
			index.Annotation = sqlIndexAnnotate(idx.Annotations)
		}
	}
	if err := ensureUniqueFKs(tables); err != nil {
		return nil, err
	}
	return
}

// Views returns all schema views
func (g *Graph) Views() (views []*schema.Table, err error) {
	for _, n := range g.Nodes {
		if !n.IsView() {
			continue
		}
		view := schema.NewView(n.Table()).
			SetComment(n.sqlComment()).
			SetPos(n.Pos())
		switch ant := n.EntSQL(); {
		case ant == nil:
		case ant.Skip:
			continue
		default:
			view.SetAnnotation(ant).SetSchema(ant.Schema)
		}
		for _, f := range n.Fields {
			if a := f.EntSQL(); a != nil && a.Skip {
				continue
			}
			view.AddColumn(f.Column())
		}
		views = append(views, view)
	}
	return
}

// mayAddColumn adds the given column if it does not already exist in the table.
func mayAddColumn(t *schema.Table, c *schema.Column) {
	if !t.HasColumn(c.Name) {
		t.AddColumn(c)
	}
}

// fkColumn returns the foreign key column for the given edge.
func fkColumn(e *Edge, owner *schema.Table, refPK *schema.Column) *schema.Column {
	// If the foreign-key also functions as a primary key, it cannot be nullable.
	ispk := len(owner.PrimaryKey) == 1 && owner.PrimaryKey[0].Name == e.Rel.Column()
	column := &schema.Column{Name: e.Rel.Column(), Size: refPK.Size, Type: refPK.Type, SchemaType: refPK.SchemaType, Nullable: !ispk}
	// O2O relations are enforced using a unique index.
	column.Unique = e.Rel.Type == O2O
	// Foreign key was defined as an edge field.
	if e.Rel.fk != nil && e.Rel.fk.Field != nil {
		fc := e.Rel.fk.Field.Column()
		column.Comment, column.Default, column.Collation, column.Attr, column.Enums = fc.Comment, fc.Default, fc.Collation, fc.Attr, fc.Enums
	}
	return column
}

func addCompositePK(t *schema.Table, n *Type) error {
	columns := make([]*schema.Column, 0, len(n.EdgeSchema.ID))
	for _, id := range n.EdgeSchema.ID {
		for _, f := range n.Fields {
			if !f.IsEdgeField() || id != f {
				continue
			}
			c, ok := t.Column(f.StorageKey())
			if !ok {
				return fmt.Errorf("missing column %q for edge field %q.%q", f.StorageKey(), n.Name, f.Name)
			}
			columns = append(columns, c)
		}
	}
	t.PrimaryKey = columns
	return nil
}

// fkSymbol returns the symbol of the foreign-key constraint for edges of type O2M, M2O and O2O.
// It returns the symbol of the storage-key if it was provided, and generate custom one otherwise.
func fkSymbol(e *Edge, ownerT, refT *schema.Table) string {
	if k, _ := e.StorageKey(); k != nil && len(k.Symbols) == 1 {
		return k.Symbols[0]
	}
	return fmt.Sprintf("%s_%s_%s", ownerT.Name, refT.Name, e.Name)
}

// fkSymbols is like fkSymbol but for M2M edges.
func fkSymbols(e *Edge, c1, c2 *schema.Column) (string, string) {
	s1 := fmt.Sprintf("%s_%s", e.Rel.Table, c1.Name)
	s2 := fmt.Sprintf("%s_%s", e.Rel.Table, c2.Name)
	if k, _ := e.StorageKey(); k != nil {
		if len(k.Symbols) > 0 {
			s1 = k.Symbols[0]
		}
		if len(k.Symbols) > 1 {
			s2 = k.Symbols[1]
		}
	}
	return s1, s2
}

// ensureUniqueFKs ensures constraint names are unique.
func ensureUniqueFKs(tables map[string]*schema.Table) error {
	fks := make(map[string]*schema.Table)
	for _, t := range tables {
		for _, fk := range t.ForeignKeys {
			switch other, ok := fks[fk.Symbol]; {
			case !ok:
				fks[fk.Symbol] = t
			case ok && other.Name != t.Name:
				a, b := t.Name, other.Name
				// Keep reporting order consistent.
				if a > b {
					a, b = b, a
				}
				return fmt.Errorf("duplicate foreign-key symbol %q found in tables %q and %q", fk.Symbol, a, b)
			case ok:
				return fmt.Errorf("duplicate foreign-key symbol %q found in table %q", fk.Symbol, t.Name)
			}
		}
	}
	return nil
}

// deleteAction returns the referential action for DELETE operations of the given edge.
func deleteAction(e *Edge, c *schema.Column) schema.ReferenceOption {
	action := schema.NoAction
	if c.Nullable {
		action = schema.SetNull
	}
	if ant := e.EntSQL(); ant != nil && ant.OnDelete != "" {
		action = ant.OnDelete
	}
	return action
}
