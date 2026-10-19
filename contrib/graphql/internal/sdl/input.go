package sdl

import (
	"fmt"

	"github.com/syssam/velox2/contrib/graphql"
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
//     or an UpdateDefault; a unique edge's ID is required when the edge is;
//   - on update, every field is optional, and a nullable field or an edge
//     that is not required can be cleared;
//   - an edge to a type that is not a Node has no ID and is in neither.
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
		if create && !f.Nullable && !f.HasDefault && !f.UpdateDefault {
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
	for i, ed := range e.Edges {
		a, t := o.eanns[i], g.byName[ed.Target]
		if t == nil || !t.node || a.Skip&skip != 0 || !create && ed.Immutable {
			continue
		}
		where := "schema " + e.Name + ", edge " + ed.Name
		name, err := o.edgeName(i)
		if err != nil {
			return nil, err
		}
		var add []gqlField
		switch {
		case ed.Unique && create && ed.Required:
			add = []gqlField{{name: name + "ID", typ: "ID!"}}
		case ed.Unique && create:
			add = []gqlField{{name: name + "ID", typ: "ID"}}
		case ed.Unique:
			add = []gqlField{{name: name + "ID", typ: "ID"}}
			if !ed.Required {
				add = append(add, gqlField{name: "clear" + upper(name), typ: "Boolean"})
			}
		case create:
			add = []gqlField{{name: singular(name) + "IDs", typ: "[ID!]"}}
		default:
			add = []gqlField{
				{name: "add" + upper(singular(name)) + "IDs", typ: "[ID!]"},
				{name: "remove" + upper(singular(name)) + "IDs", typ: "[ID!]"},
				{name: "clear" + upper(name), typ: "Boolean"},
			}
		}
		for _, f := range add {
			f.where = where
			if err := fs.add(f); err != nil {
				return nil, err
			}
		}
	}
	return fs.list, nil
}

// inputDesc is the description of o's create or update input.
func inputDesc(o *object, create bool) string {
	if create {
		return "The input of create" + o.name + "."
	}
	return "The input of update" + o.name + ": a field not given is left as it is."
}
