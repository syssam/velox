package sdl

import (
	"fmt"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/plugin"
)

// mutationsOf computes o's mutation inputs, CreateXInput and UpdateXInput,
// and adds its mutations to the root Mutation, for the kinds its Mutations
// annotation lists.
func (g *graph) mutationsOf(o *object) error {
	e := o.ent
	where := "schema " + e.Name
	var create, update, del bool
	for _, k := range o.ann.Mutations {
		switch k {
		case graphql.Create():
			create = true
		case graphql.Update():
			update = true
		case graphql.Delete():
			del = true
		default:
			return fmt.Errorf("%s: graphql.Mutations: %q is not a mutation; the mutations are create, update and delete", where, string(k))
		}
	}
	for _, c := range []struct {
		on   bool
		kind string
		skip graphql.SkipMode
		name string
	}{
		{create, "create", graphql.SkipMutationCreateInput, "SkipMutationCreateInput"},
		{update, "update", graphql.SkipMutationUpdateInput, "SkipMutationUpdateInput"},
	} {
		if c.on && o.ann.Skip&c.skip != 0 {
			return fmt.Errorf("%s: graphql.Mutations lists %s, and graphql.Skip(%s) leaves out its input", where, c.kind, c.name)
		}
	}
	for _, c := range []struct {
		on   bool
		kind string
	}{{update, "update"}, {del, "delete"}} {
		if c.on && !o.node {
			return fmt.Errorf("%s: graphql.Mutations lists %s, which takes an ID, and its key is %d columns: it is not a Node", where, c.kind, len(e.PrimaryKey))
		}
	}

	if create && o.node {
		pk := e.Fields[e.PrimaryKey[0]]
		if !pk.AutoID && !pk.HasDefault {
			return fmt.Errorf("%s: graphql.Mutations lists create, and its key %s is supplied by the caller (no auto ID, no default): v1 does not generate createX for such a schema, as the key is never an input", where, pk.Name)
		}
	}
	if create {
		in := "Create" + o.name + "Input"
		fs, err := g.inputFields(o, in, true)
		if err != nil {
			return err
		}
		if len(fs) == 0 {
			return fmt.Errorf("%s: graphql.Mutations lists create, and its create input would have no fields", where)
		}
		if err := g.types.add(in, where); err != nil {
			return err
		}
		o.create = fs
		g.mutation = append(g.mutation, gqlField{
			name: "create" + o.name, args: "(input: " + in + "!)", typ: o.name + "!",
			desc: "Creates one " + o.name + ".", where: where,
		})
	}
	if update {
		// An update input with no fields gets no type, and no updateX.
		in := "Update" + o.name + "Input"
		fs, err := g.inputFields(o, in, false)
		if err != nil {
			return err
		}
		if len(fs) > 0 {
			if err := g.types.add(in, where); err != nil {
				return err
			}
			o.update = fs
			g.mutation = append(g.mutation, gqlField{
				name: "update" + o.name, args: "(id: ID!, input: " + in + "!)", typ: o.name + "!",
				desc: "Updates the " + o.name + " with this ID: only the fields given change.", where: where,
			})
		}
	}
	if del {
		g.mutation = append(g.mutation, gqlField{
			name: "delete" + o.name, args: "(id: ID!)", typ: "ID!",
			desc: "Deletes the " + o.name + " with this ID, and returns the ID.", where: where,
		})
	}
	return nil
}

// inputFields are the fields of o's create input, or its update input:
//   - neither has the primary key, the tenant or a foreign key; the edges
//     stand for the foreign keys;
//   - neither has a field whose Skip leaves out the input, or that
//     CreateInput(false) or UpdateInput(false) leaves out, or with an
//     UpdateDefault and no Default unless CreateInput(true) or
//     UpdateInput(true) brings it in;
//   - the update input has no immutable field or edge;
//   - on create, a field is required unless it is nullable or has a Default
//     (or DefaultFunc): an UpdateDefault fills on update only; a unique
//     edge's ID is required when the edge is;
//   - on update, every field is optional, and a nullable field can be
//     cleared;
//   - an edge to a type that is not a Node has no ID and is in neither;
//   - an edge's inputs follow its foreign key's flags, on whichever entity
//     holds it (see edgeInputs).
func (g *graph) inputFields(o *object, typ string, create bool) ([]gqlField, error) {
	e := o.ent
	skip, explicit := graphql.SkipMutationUpdateInput, func(a graphql.Annotation) *bool { return a.UpdateInput }
	if create {
		skip, explicit = graphql.SkipMutationCreateInput, func(a graphql.Annotation) *bool { return a.CreateInput }
	}
	fs := newInputFields(typ)
	for i, f := range e.Fields {
		a := o.fanns[i]
		switch {
		case o.node && i == e.PrimaryKey[0], i == e.Tenant, f.Implicit != "", o.fks[f.Name], a.Skip&skip != 0, !create && f.Immutable:
			continue
		}
		in := !f.UpdateDefault || f.HasDefault
		if p := explicit(a); p != nil {
			in = *p
		}
		if !in {
			continue
		}
		where := "schema " + e.Name + ", field " + f.Name
		name, err := o.fieldName(i)
		if err != nil {
			return nil, err
		}
		t, err := g.fieldType(o, f, a, where)
		if err != nil {
			return nil, err
		}
		if create && !f.Nullable && !f.HasDefault {
			t += "!"
		}
		if err := fs.add(gqlField{name: name, typ: t, desc: f.Comment, where: where}); err != nil {
			return nil, err
		}
		if !create && f.Nullable {
			if err := fs.add(gqlField{name: "clear" + upper(name), typ: "Boolean", where: where}); err != nil {
				return nil, err
			}
		}
	}
	for i := range e.Edges {
		if o.eanns[i].Skip&skip != 0 {
			continue
		}
		add, err := g.edgeInputs(o, i, create)
		if err != nil {
			return nil, err
		}
		for _, f := range add {
			if err := fs.add(f); err != nil {
				return nil, err
			}
		}
	}
	return fs.list, nil
}

// edgeInputs are the input fields of o's edge i, on create or on update.
// An edge stands for a foreign key, and its inputs only offer what a write
// of that key may do:
//   - a unique edge whose key is on o's row (OwnsKey) sets it: <edge>ID, and
//     on update clear<Edge> when the key is nullable;
//   - an edge whose key is on the target's rows (O2M, and an O2O whose key
//     the other entity holds) re-points those rows: <edge>IDs, or
//     add/remove/clear on update; an O2O sets <edge>ID, and clears it on
//     update. A required (non-null) key cannot be cleared, so remove and
//     clear are left out; an immutable key cannot be moved, so the edge has
//     no input at all, on create too, where it would re-point existing rows;
//   - an immutable key on o's row is set on create only;
//   - an M2M edge writes join rows: <edge>IDs, add/remove/clear;
//   - an edge to a type that is not a Node has no ID and no input. When that
//     edge is required and holds its key on o's row, createX could never
//     succeed: an error.
func (g *graph) edgeInputs(o *object, i int, create bool) ([]gqlField, error) {
	ed := o.ent.Edges[i]
	t := g.byName[ed.Target]
	where := "schema " + o.ent.Name + ", edge " + ed.Name
	if t == nil || !t.node {
		// A required key on o's own row that createX could never set. When
		// the target's row holds the key, createX writes none of its own.
		if create && ed.Unique && ed.Required && ed.OwnsKey {
			why := "is skipped"
			if t != nil {
				why = "is not a Node"
			}
			return nil, fmt.Errorf("%s: a required edge whose target %s %s: create%s could never set it; skip the edge's create input with graphql.Skip(graphql.SkipMutationCreateInput) if a hook sets it", where, ed.Target, why, o.name)
		}
		return nil, nil
	}
	name, err := o.edgeName(i)
	if err != nil {
		return nil, err
	}
	fk := g.keyField(ed)
	immutable := ed.Immutable || fk != nil && fk.Immutable
	nullable := fk == nil || fk.Nullable
	one, many := upper(plugin.Singular(name)), plugin.Singular(name)
	var out []gqlField
	switch {
	case ed.Kind == "M2M":
		if immutable && !create {
			break
		}
		if create {
			out = []gqlField{{name: many + "IDs", typ: "[ID!]"}}
		} else {
			out = []gqlField{{name: "add" + one + "IDs", typ: "[ID!]"}, {name: "remove" + one + "IDs", typ: "[ID!]"}, {name: "clear" + upper(name), typ: "Boolean"}}
		}
	case ed.Unique && ed.OwnsKey:
		switch {
		case create && ed.Required:
			out = []gqlField{{name: name + "ID", typ: "ID!"}}
		case create:
			out = []gqlField{{name: name + "ID", typ: "ID"}}
		case immutable:
		default:
			out = []gqlField{{name: name + "ID", typ: "ID"}}
			if !ed.Required && nullable {
				out = append(out, gqlField{name: "clear" + upper(name), typ: "Boolean"})
			}
		}
	case immutable: // the key is on the target's rows, and cannot move
	case ed.Unique:
		typ := "ID"
		if create && ed.Required {
			typ = "ID!"
		}
		out = []gqlField{{name: name + "ID", typ: typ}}
		if !create && nullable {
			out = append(out, gqlField{name: "clear" + upper(name), typ: "Boolean"})
		}
	case create:
		out = []gqlField{{name: many + "IDs", typ: "[ID!]"}}
	default:
		out = []gqlField{{name: "add" + one + "IDs", typ: "[ID!]"}}
		if nullable {
			out = append(out, gqlField{name: "remove" + one + "IDs", typ: "[ID!]"}, gqlField{name: "clear" + upper(name), typ: "Boolean"})
		}
	}
	for j := range out {
		out[j].where = where
	}
	return out, nil
}

// keyField is the foreign-key field ed stands for, on whichever entity holds
// it; nil for an M2M edge, or a field the model does not have.
func (g *graph) keyField(ed *plugin.Edge) *plugin.Field {
	if ed.Field == "" {
		return nil
	}
	h := g.ents[ed.FieldOn]
	if h == nil {
		return nil
	}
	for _, f := range h.Fields {
		if f.Name == ed.Field {
			return f
		}
	}
	return nil
}

// inputDesc is the description of o's create or update input.
func inputDesc(o *object, create bool) string {
	if create {
		return "The input of create" + o.name + "."
	}
	return "The input of update" + o.name + ": a field not given is left as it is."
}
