package gogen

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/syssam/velox/contrib/graphql/internal/english"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// writeMutations writes t's mutation inputs: CreateXInput and its method
// Create, UpdateXInput and its method Update, and the function that names
// their fields.
func (r *renderer) writeMutations(f *file, t *sdl.Type) error {
	if t.Create == nil && t.Update == nil {
		return nil
	}
	if _, _, err := r.writeInputGQL(f, t); err != nil {
		return err
	}
	if err := r.writeInputEdges(f, t); err != nil {
		return err
	}
	if t.Create != nil {
		in := "Create" + t.Name + "Input"
		if err := r.writeInput(f, t, in, t.Create); err != nil {
			return err
		}
		if err := r.writeCreate(f, t, in); err != nil {
			return err
		}
	}
	if t.Update != nil {
		in := "Update" + t.Name + "Input"
		if err := r.writeInput(f, t, in, t.Update); err != nil {
			return err
		}
		if err := r.writeUpdate(f, t, in); err != nil {
			return err
		}
	}
	return nil
}

// edgeFunc is the function that returns the edge ed of t's entity as the
// inputs write it: a gqlimpl.M2M, or a gqlimpl.Owned for an edge whose key
// is on the target's rows.
func edgeFunc(t *sdl.Type, ed *plugin.Edge) string {
	return lowerFirst(t.Entity.Name) + ed.GoName + "EdgeGQL"
}

// inputEdges are the edges of t's entity that its inputs write by the
// target's rows, by their index, in the edges' order: the many-to-many
// edges and those whose key is on the target's rows.
func inputEdges(t *sdl.Type) []int {
	e := t.Entity
	var out []int
	for _, fs := range [][]sdl.InputField{t.Create, t.Update} {
		for _, fi := range fs {
			switch fi.Kind {
			case sdl.InputEdgeID, sdl.InputEdgeIDs, sdl.InputAddIDs, sdl.InputRemoveIDs, sdl.InputClearEdge:
			default:
				continue
			}
			if ed := e.Edges[fi.Edge]; ed.OwnsKey && ed.Kind != plugin.M2M || slices.Contains(out, fi.Edge) {
				continue
			}
			out = append(out, fi.Edge)
		}
	}
	slices.Sort(out)
	return out
}

// writeInputEdges writes edgeFunc of each of inputEdges(t), which Create and
// Update both call.
func (r *renderer) writeInputEdges(f *file, t *sdl.Type) error {
	e := t.Entity
	impl := f.gqlimpl()
	for _, ei := range inputEdges(t) {
		ed := e.Edges[ei]
		if len(e.PrimaryKey) != 1 {
			return fmt.Errorf("schema %s, edge %s: velox-graphql writes it by the key of %s, which is %d columns; skip its inputs with graphql.Skip(graphql.SkipMutationCreateInput, graphql.SkipMutationUpdateInput)", e.Name, ed.Name, e.Name, len(e.PrimaryKey))
		}
		tt := r.typeOf[ed.Target]
		if tt == nil {
			return fmt.Errorf("schema %s, edge %s: its target %s has no GraphQL type, which is a bug", e.Name, ed.Name, ed.Target)
		}
		tk, err := r.idKeyOf(f, e, tt)
		if err != nil {
			return err
		}
		tgt := tt.Entity
		name := edgeFunc(t, ed)
		if err := r.declare(name, "schema "+e.Name+", edge "+ed.Name); err != nil {
			return err
		}
		ok := e.Fields[e.PrimaryKey[0]]
		vx := f.velox()
		f.p("")
		if ed.Kind == plugin.M2M {
			j, own, other, err := r.joinCols(e, ed)
			if err != nil {
				return err
			}
			f.doc("", fmt.Sprintf("%s is the many-to-many edge %s of %s, through %s.", name, ed.Name, e.Name, j.Name))
			f.p("func %s() %s.M2M {", name, impl)
			f.p("\treturn %s.M2M{Owner: %s.TableOf[%s](), Target: %s.TableOf[%s](), Join: %s.TableOf[%s](),", impl, vx, e.Name, vx, tgt.Name, vx, j.Name)
			f.p("\t\tOwnerKey: %s, TargetKey: %s, JoinOwner: %s, JoinTarget: %s}", colConst(e, ok), colConst(tgt, tk.fd), colConst(j, own), colConst(j, other))
			f.p("}")
			continue
		}
		// The target's rows hold the key. sdl gives a key that is never NULL
		// no input that re-points it: it is an owner's on every row.
		tfk, err := fkOf(e, ed, tgt)
		if err != nil {
			return err
		}
		if !tfk.Nullable {
			return fmt.Errorf("schema %s, edge %s: an input re-points %s's rows, whose key %s is never NULL, which is a bug", e.Name, ed.Name, tgt.Name, tfk.Name)
		}
		if _, err := r.keyConv(f, tgt, tfk, ok, cmp.Or(t.Key, tfk.Type)); err != nil {
			return err
		}
		f.doc("", fmt.Sprintf("%s is the edge %s of %s, whose key %s's rows hold.", name, ed.Name, e.Name, tgt.Name))
		f.p("func %s() %s.Owned {", name, impl)
		f.p("\treturn %s.Owned{Owner: %s.TableOf[%s](), Target: %s.TableOf[%s](), OwnerKey: %s, TargetKey: %s, FK: %s}", impl, vx, e.Name, vx, tgt.Name, colConst(e, ok), colConst(tgt, tk.fd), colConst(tgt, tfk))
		f.p("}")
	}
	return nil
}

// inputName is a field of a mutation input that sets an entity field: the
// field's Go name, and the input field's GraphQL name.
type inputName struct{ goName, gql string }

// inputNames are the fields of the input fs that set an entity field, as a
// validator's error names them: a value by its field, and an edge's
// <edge>ID by its foreign key, when the key is on the row.
func inputNames(e *plugin.Entity, fs []sdl.InputField) []inputName {
	var out []inputName
	add := func(goName, gql string) {
		if !slices.ContainsFunc(out, func(x inputName) bool { return x.goName == goName }) {
			out = append(out, inputName{goName, gql})
		}
	}
	for _, in := range fs {
		switch {
		case in.Kind == sdl.InputValue:
			add(e.Fields[in.Field].GoName, in.Name)
		case in.Kind == sdl.InputEdgeID && e.Edges[in.Edge].OwnsKey:
			if fk := fieldOf(e, e.Edges[in.Edge].Field); fk != nil {
				add(fk.GoName, in.Name)
			}
		}
	}
	return out
}

// writeInputGQL writes the function that gives, by an entity field's Go name,
// the GraphQL name of the input field that sets it, which a validator's error
// carries; "" for a field the input does not set, so that an error names no
// field of another input. It writes one, <entity>InputGQL, when t has one
// input or both name the same fields alike, and else one for each,
// <entity>CreateInputGQL and <entity>UpdateInputGQL. It returns the names of
// the create input's and the update input's.
func (r *renderer) writeInputGQL(f *file, t *sdl.Type) (create, update string, err error) {
	e := t.Entity
	cn, un := inputNames(e, t.Create), inputNames(e, t.Update)
	create, update = inputFuncs(t)
	if create == "" || update == "" || create == update {
		names, in := cn, "Create"+t.Name+"Input"
		switch {
		case t.Create == nil:
			names, in = un, "Update"+t.Name+"Input"
		case t.Update != nil:
			in += " and Update" + t.Name + "Input"
		}
		name := cmp.Or(create, update)
		if err := r.writeNameFunc(f, t, name, in, names); err != nil {
			return "", "", err
		}
		return create, update, nil
	}
	if err := r.writeNameFunc(f, t, create, "Create"+t.Name+"Input", cn); err != nil {
		return "", "", err
	}
	if err := r.writeNameFunc(f, t, update, "Update"+t.Name+"Input", un); err != nil {
		return "", "", err
	}
	return create, update, nil
}

// inputFuncs are the names of the functions writeInputGQL writes for t's
// create and update inputs: one name for both when they name the same fields
// alike, and "" for an input t does not have.
func inputFuncs(t *sdl.Type) (create, update string) {
	base := lowerFirst(t.Entity.Name)
	switch {
	case t.Create == nil && t.Update == nil:
		return "", ""
	case t.Create == nil:
		return "", base + "InputGQL"
	case t.Update == nil:
		return base + "InputGQL", ""
	case slices.Equal(inputNames(t.Entity, t.Create), inputNames(t.Entity, t.Update)):
		return base + "InputGQL", base + "InputGQL"
	}
	return base + "CreateInputGQL", base + "UpdateInputGQL"
}

// writeNameFunc writes the function name of the input (or inputs) in, by
// the fields names.
func (r *renderer) writeNameFunc(f *file, t *sdl.Type, name, in string, names []inputName) error {
	e := t.Entity
	if err := r.declare(name, "schema "+e.Name); err != nil {
		return err
	}
	f.p("")
	f.doc("", fmt.Sprintf("%s is the %s field that sets the %s field goField, or \"\". A validator's error names it.", name, in, e.Name))
	f.p("func %s(goField string) string {", name)
	if len(names) > 0 {
		f.p("\tswitch goField {")
		for _, x := range names {
			f.p("\tcase %q:", x.goName)
			f.p("\t\treturn %q", x.gql)
		}
		f.p("\t}")
	}
	f.p("\treturn \"\"")
	f.p("}")
	return nil
}

// writeInput writes the Go type of the mutation input in, whose fields are
// fs, and, when it has a sensitive field, its String and GoString, which
// leave out the sensitive fields' values.
func (r *renderer) writeInput(f *file, t *sdl.Type, in string, fs []sdl.InputField) error {
	e := t.Entity
	if err := r.declare(in, "schema "+e.Name); err != nil {
		return err
	}
	method := "Create"
	if strings.HasPrefix(in, "Update") {
		method = "Update"
	}
	sensitive := slices.ContainsFunc(fs, func(fi sdl.InputField) bool { return fi.Kind == sdl.InputValue && e.Fields[fi.Field].Sensitive })
	seen := map[string]string{} // as gqlgen matches names → the Go name
	types := make([]string, len(fs))
	for i, fi := range fs {
		key := strings.ToLower(strings.ReplaceAll(fi.GoName, "_", ""))
		if prev, ok := seen[key]; ok {
			return fmt.Errorf("schema %s: the fields %s and %s of %s are one Go field to gqlgen, which ignores case and underscores; rename one with graphql.FieldName", e.Name, prev, fi.GoName, in)
		}
		seen[key] = fi.GoName
		if fi.GoName == method || sensitive && (fi.GoName == "String" || fi.GoName == "GoString") {
			return fmt.Errorf("schema %s: the field %s of %s is the Go field %s, which is the input's method; rename it with graphql.FieldName", e.Name, fi.Name, in, fi.GoName)
		}
		typ, err := r.inputType(f, t, fi)
		if err != nil {
			return err
		}
		types[i] = typ
	}
	f.p("")
	doc := fmt.Sprintf("%s is the GraphQL input %[1]s, which %s writes. A nil pointer or list is a field not given.", in, method)
	if slices.ContainsFunc(fs, func(fi sdl.InputField) bool { return fi.Kind == sdl.InputClear || fi.Kind == sdl.InputClearEdge }) {
		doc += " A true clear field clears its field or edge."
	}
	f.doc("", doc)
	f.p("type %s struct {", in)
	for i, fi := range fs {
		switch fi.Kind {
		case sdl.InputValue:
			if c := e.Fields[fi.Field].Comment; c != "" {
				f.doc("\t", c)
			}
		case sdl.InputClear:
			f.doc("\t", fmt.Sprintf("%s sets the field %s to NULL.", fi.GoName, e.Fields[fi.Field].Name))
		case sdl.InputEdgeID:
			f.doc("\t", fmt.Sprintf("%s is the ID of the %s the edge %s leads to.", fi.GoName, fi.Target, e.Edges[fi.Edge].Name))
		case sdl.InputEdgeIDs:
			f.doc("\t", fmt.Sprintf("%s are the IDs of the %s nodes the edge %s leads to.", fi.GoName, fi.Target, e.Edges[fi.Edge].Name))
		case sdl.InputAddIDs:
			f.doc("\t", fmt.Sprintf("%s are the IDs of %s nodes to add to the edge %s.", fi.GoName, fi.Target, e.Edges[fi.Edge].Name))
		case sdl.InputRemoveIDs:
			f.doc("\t", fmt.Sprintf("%s are the IDs of %s nodes to remove from the edge %s; an ID the edge does not lead to is ignored.", fi.GoName, fi.Target, e.Edges[fi.Edge].Name))
		case sdl.InputClearEdge:
			if e.Edges[fi.Edge].Unique {
				f.doc("\t", fmt.Sprintf("%s unlinks the edge %s: it leads to no %s afterwards.", fi.GoName, e.Edges[fi.Edge].Name, fi.Target))
			} else {
				f.doc("\t", fmt.Sprintf("%s removes every %s from the edge %s, before the IDs to remove and to add.", fi.GoName, fi.Target, e.Edges[fi.Edge].Name))
			}
		}
		f.p("\t%s %s", fi.GoName, types[i])
	}
	f.p("}")
	if !sensitive {
		return nil
	}
	f.p("")

	// String: a pointer prints as what it points to, or <nil>, and JSON as
	// its text.
	var names, args []string
	f.p("// String returns the input's fields except its sensitive ones.")
	f.p("func (in %s) String() string {", in)
	for i, fi := range fs {
		if fi.Kind == sdl.InputValue && e.Fields[fi.Field].Sensitive {
			continue
		}
		v := "in." + fi.GoName
		json := fi.Kind == sdl.InputValue && fi.GoType == "veloxgql.JSON"
		switch {
		case strings.HasPrefix(types[i], "*") && json:
			v = f.gqlimpl() + ".ShowJSON(" + v + ")"
		case strings.HasPrefix(types[i], "*"):
			v = f.gqlimpl() + ".Show(" + v + ")"
		case json:
			v = "string(" + v + ")"
		}
		names = append(names, fi.GoName+"=%v")
		args = append(args, v)
	}
	f.p("\treturn %s.Sprintf(%q%s)", f.use("fmt", "fmt"), in+"("+strings.Join(names, ", ")+")", strings.Join(slices.Insert(args, 0, ""), ", "))
	f.p("}")
	f.p("")
	f.p("// GoString is String, so that %%#v does not print the sensitive fields either.")
	f.p("func (in %s) GoString() string { return in.String() }", in)
	return nil
}

// jsonOf is the Go type a JSON field fd's input, a veloxgql.JSON, decodes
// into, as the file names it; "" for any other field, whose input binds its
// Go type.
func (r *renderer) jsonOf(f *file, e *plugin.Entity, fd *plugin.Field, fi sdl.InputField) (string, error) {
	if fd.Type != "json" || fi.GoType != "veloxgql.JSON" { // a list binds its Go slice
		return "", nil
	}
	elem := cmp.Or(fd.ElemType, strings.TrimPrefix(fd.GoType, "*"))
	if elem == "" {
		return "", fmt.Errorf("schema %s, field %s: the model gives no Go type for the JSON field", e.Name, fd.Name)
	}
	return r.goType(f, e, fd, elem)
}

// keyCall is the decode of the ID src of the input field fi into a key of k,
// with the helper fn (Key, OptionalKey or Keys) on the mutation m.
func keyCall(f *file, m, fn string, k idKey, fi sdl.InputField, src string) string {
	return fmt.Sprintf("%s.%s%s(%s, %q, %s, %q%s)", f.gqlimpl(), fn, k.args, m, fi.Name, src, fi.Target, k.conv)
}

// writeCreate writes the method Create of the create input in, whose fields
// t.Create names, with the name function name: a description of the insert
// and its edges, which gqlimpl.Create runs.
func (r *renderer) writeCreate(f *file, t *sdl.Type, in string) error {
	e := t.Entity
	ctx, impl := f.use("context", "context"), f.gqlimpl()
	fs := t.Create

	f.p("")
	doc := fmt.Sprintf("Create inserts x, %s %s, with the input's fields and edges set; a nil x is a new one. It runs atomically on the DB in ctx, under the policies.", english.Article(t.Name), t.Name)
	var names []string
	for _, fd := range leftOut(t) {
		names = append(names, fd.GoName)
	}
	if len(names) > 0 {
		them, it := "them", "they are"
		if len(names) == 1 {
			them, it = "it", "it is"
		}
		doc += fmt.Sprintf(" %s leaves out %s, which an insert requires: set %s on x, or in a velox.Hook, or %s stored as the zero value.", in, strings.Join(names, ", "), them, it)
	}
	f.doc("", doc)
	f.p("//")
	f.doc("", "See \"Mutations\" in the documentation of package github.com/syssam/velox/contrib/graphql for what each input field does, what the caller may link and change, and the errors.")
	f.p("func (in *%s) Create(ctx %s.Context, x *%s) (*%s, error) {", in, ctx, e.Name, e.Name)
	var required []string
	for _, fi := range fs {
		if fi.Kind == sdl.InputValue && fi.Required {
			required = append(required, ", "+colConst(e, e.Fields[fi.Field]))
		}
	}
	f.p("\tif x == nil {")
	f.p("\t\tx = new(%s)", e.Name)
	f.p("\t}")
	f.p("\tc := %s.NewCreate(ctx, &%s, x%s)", impl, connVar(t), strings.Join(required, ""))

	// given opens, at indent, the block of a field that is not Required:
	// it runs only when the field is given. It returns the block's indent
	// and its end.
	given := func(indent string, fi sdl.InputField) (string, func()) {
		if fi.Required {
			return indent, func() {}
		}
		f.p("%sif in.%s != nil {", indent, fi.GoName)
		return indent + "\t", func() { f.p("%s}", indent) }
	}
	src := func(fi sdl.InputField) string {
		if fi.Required {
			return "in." + fi.GoName
		}
		return "*in." + fi.GoName
	}

	for _, fi := range fs {
		switch fi.Kind {
		case sdl.InputValue:
			fd := e.Fields[fi.Field]
			dst, tmp := "x."+fd.GoName, local(fi.GoName, "Val")
			// An insert fills the Default into NULL as well, and the input's nil
			// pointer cannot say the client gave null.
			if fd.Nullable && fd.HasDefault && !fi.Required {
				f.p("\t%s.NotNull(c, %q, in.%s != nil)", impl, fi.Name, fi.GoName)
			}
			indent, end := given("\t", fi)
			col := func() {
				if !fi.Required {
					f.p("%sc.Given(%s)", indent, colConst(e, fd))
				}
			}
			jt, err := r.jsonOf(f, e, fd, fi)
			if err != nil {
				return err
			}
			if jt != "" {
				v := local(fi.GoName, "JSON")
				f.p("%s%s := %s.DecodeJSON[%s](c, %q, %s)", indent, v, impl, jt, fi.Name, src(fi))
				if fd.Pointer {
					f.p("%s%s = &%s", indent, dst, v)
				} else {
					f.p("%s%s = %s", indent, dst, v)
				}
				col()
				end()
				continue
			}
			conv, err := r.valueConv(f, e, fd, fi)
			if err != nil {
				return err
			}
			switch {
			case fi.Required:
				setKey(f, indent, dst, "in."+fi.GoName, conv, tmp, fd.Pointer)
			case fd.Pointer && conv == "":
				f.p("%s%s = in.%s", indent, dst, fi.GoName)
			default:
				setKey(f, indent, dst, "*in."+fi.GoName, conv, tmp, fd.Pointer)
			}
			// An insert fills the Default into a zero value, so the client's "" or
			// zero time would not be what it stores. An enum's input is never "",
			// a zero Default stores the zero, and a list given is never nil.
			if fd.HasDefault && !fd.Pointer && fd.Type != "enum" && fd.Type != "json" && !zeroDefault(fd) {
				f.p("%s%s.NotZero(c, %q, %s)", indent, impl, fi.Name, dst)
			}
			col()
			end()

		case sdl.InputEdgeID, sdl.InputEdgeIDs:
			ed := e.Edges[fi.Edge]
			tt := r.types[fi.Target]
			k, err := r.idKeyOf(f, e, tt)
			if err != nil {
				return err
			}
			tgt := tt.Entity
			target := fmt.Sprintf("%s.TableOf[%s](), %s", f.velox(), tgt.Name, colConst(tgt, k.fd))
			if fi.Kind == sdl.InputEdgeID && ed.OwnsKey {
				// The key is on the row: set it, once its ID names a row the
				// caller may read.
				fk, err := fkOf(e, ed, e)
				if err != nil {
					return err
				}
				conv, err := r.keyConv(f, e, fk, k.fd, tt.Key)
				if err != nil {
					return err
				}
				indent, end := given("\t", fi)
				v := local(fi.GoName, "Key")
				f.p("%s%s := %s", indent, v, keyCall(f, "c", "Key", k, fi, src(fi)))
				f.p("%s%s.Exists(c, %q, %q, %s, []%s{%s})", indent, impl, fi.Name, fi.Target, target, k.typ, v)
				setKey(f, indent, "x."+fk.GoName, v, conv, local(fi.GoName, "Val"), fk.Pointer)
				end()
				continue
			}
			// A many-to-many edge's join rows, or the target's rows that hold
			// the key: inputEdges gives it an edgeFunc.
			fn := "Join"
			if ed.Kind != plugin.M2M {
				fn = "Repoint"
			}
			if fi.Kind == sdl.InputEdgeID {
				indent, end := given("\t", fi)
				v := local(fi.GoName, "Key")
				f.p("%s%s := %s", indent, v, keyCall(f, "c", "Key", k, fi, src(fi)))
				f.p("%s%s.%s(c, %q, %q, %s(), []%s{%s})", indent, impl, fn, fi.Name, fi.Target, edgeFunc(t, ed), k.typ, v)
				end()
				continue
			}
			v := local(fi.GoName, "Keys")
			f.p("\t%s := %s", v, keyCall(f, "c", "Keys", k, fi, "in."+fi.GoName))
			f.p("\t%s.%s(c, %q, %q, %s(), %s)", impl, fn, fi.Name, fi.Target, edgeFunc(t, ed), v)
		}
	}
	f.p("\tif err := c.Insert(); err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	f.p("\treturn x, nil")
	f.p("}")
	return nil
}

// writeUpdate writes the method Update of the update input in, whose fields
// t.Update names, with the name function name: a description of the UPDATE
// and its edges, which gqlimpl.Update runs.
func (r *renderer) writeUpdate(f *file, t *sdl.Type, in string) error {
	e, n := t.Entity, t.Name
	if len(e.PrimaryKey) != 1 {
		return fmt.Errorf("schema %s: an update takes the ID of a key of one column, and %s's is %d columns, which is a bug", e.Name, e.Name, len(e.PrimaryKey))
	}
	ctx, velox, impl := f.use("context", "context"), f.velox(), f.gqlimpl()
	fs := t.Update
	self, err := r.idKeyOf(f, e, t)
	if err != nil {
		return err
	}
	f.p("")
	f.doc("", fmt.Sprintf("Update updates the %s with the ID id from the input and from set, and returns the row as written. A setter of set wins over the input's for the same column.", n))
	f.p("//")
	f.doc("", "See \"Mutations\" in the documentation of package github.com/syssam/velox/contrib/graphql for what each input field does, what the caller may link and change, and the errors.")
	f.p("func (in *%s) Update(ctx %s.Context, id string, set ...%s.Setter[%s]) (*%[4]s, error) {", in, ctx, velox, e.Name)
	if self.conv != "" {
		f.p("\tu, key := %s.NewUpdate%s(ctx, &%s, id%s)", impl, strings.TrimSuffix(self.args, "]")+", "+self.typ+"]", connVar(t), self.conv)
	} else {
		f.p("\tu, key := %s.NewUpdate[%s](ctx, &%s, id)", impl, self.typ, connVar(t))
	}

	// vars are the variables of the edges' inputs, by the input field's index.
	vars := map[int]string{}
	// The fields, in order: each value's setter, the keys on the row, and the
	// decodes of the edges' IDs. Their errors come in this order.
	for i, fi := range fs {
		switch fi.Kind {
		case sdl.InputValue:
			fd := e.Fields[fi.Field]
			v := "*in." + fi.GoName
			jt, err := r.jsonOf(f, e, fd, fi)
			if err != nil {
				return err
			}
			if jt != "" {
				v = fmt.Sprintf("%s.DecodeJSON[%s](u, %q, %s)", impl, jt, fi.Name, v)
			} else {
				conv, err := r.valueConv(f, e, fd, fi)
				if err != nil {
					return err
				}
				if conv != "" {
					v = conv + "(" + v + ")"
				}
			}
			f.p("\tif in.%s != nil {", fi.GoName)
			f.p("\t\tu.Set(%s, %s)", colConst(e, fd), v)
			f.p("\t}")

		case sdl.InputClear:
			null, err := setNull(e, e.Fields[fi.Field])
			if err != nil {
				return err
			}
			cond := "in." + fi.GoName
			// The input's nil pointer leaves the column, and null means to clear it.
			if o := slices.IndexFunc(fs, func(x sdl.InputField) bool { return x.Kind == sdl.InputValue && x.Field == fi.Field }); o >= 0 {
				cond = fmt.Sprintf("%s.Cleared(u, %q, in.%s != nil, %q, in.%s)", impl, fs[o].Name, fs[o].GoName, fi.Name, fi.GoName)
			}
			f.p("\tif %s {", cond)
			f.p("\t\t%s", null)
			f.p("\t}")

		case sdl.InputEdgeID:
			ed := e.Edges[fi.Edge]
			tt := r.types[fi.Target]
			k, err := r.idKeyOf(f, e, tt)
			if err != nil {
				return err
			}
			if !ed.OwnsKey {
				// A one-to-one edge whose key is on the target: Child links it.
				v := local(fi.GoName, "Key")
				f.p("\t%s := %s", v, keyCall(f, "u", "OptionalKey", k, fi, "in."+fi.GoName))
				vars[i] = v
				continue
			}
			fk, err := fkOf(e, ed, e)
			if err != nil {
				return err
			}
			conv, err := r.keyConv(f, e, fk, k.fd, tt.Key)
			if err != nil {
				return err
			}
			v := local(fi.GoName, "Key")
			kv := v
			if conv != "" {
				kv = conv + "(" + v + ")"
			}
			f.p("\tif in.%s != nil {", fi.GoName)
			f.p("\t\t%s := %s", v, keyCall(f, "u", "Key", k, fi, "*in."+fi.GoName))
			f.p("\t\t%s.Exists(u, %q, %q, %s.TableOf[%s](), %s, []%s{%s})", impl, fi.Name, fi.Target, velox, tt.Entity.Name, colConst(tt.Entity, k.fd), k.typ, v)
			f.p("\t\tu.SetKey(%s, %s)", colConst(e, fk), kv)
			f.p("\t}")

		case sdl.InputClearEdge:
			ed := e.Edges[fi.Edge]
			cond := "in." + fi.GoName
			if o := slices.IndexFunc(fs, func(x sdl.InputField) bool { return x.Kind == sdl.InputEdgeID && x.Edge == fi.Edge }); o >= 0 {
				cond = fmt.Sprintf("%s.ClearEdge(u, %q, in.%s != nil, %q, in.%s)", impl, fs[o].Name, fs[o].GoName, fi.Name, fi.GoName)
			}
			if ed.OwnsKey && ed.Kind != plugin.M2M {
				fk, err := fkOf(e, ed, e)
				if err != nil {
					return err
				}
				if _, err := setNull(e, fk); err != nil {
					return err
				}
				f.p("\tif %s {", cond)
				f.p("\t\tu.SetKey(%s, nil)", colConst(e, fk))
				f.p("\t}")
				continue
			}
			if cond != "in."+fi.GoName {
				v := camel(fi.GoName)
				f.p("\t%s := %s", v, cond)
				cond = v
			}
			vars[i] = cond

		case sdl.InputAddIDs, sdl.InputRemoveIDs:
			if _, ok := vars[i]; ok {
				continue // decoded with its pair
			}
			k, err := r.idKeyOf(f, e, r.types[fi.Target])
			if err != nil {
				return err
			}
			v := local(fi.GoName, "Keys")
			vars[i] = v
			pair := slices.IndexFunc(fs, func(x sdl.InputField) bool {
				return x.Kind == sdl.InputRemoveIDs && fi.Kind == sdl.InputAddIDs && x.Edge == fi.Edge
			})
			if pair < 0 {
				f.p("\t%s := %s", v, keyCall(f, "u", "Keys", k, fi, "in."+fi.GoName))
				continue
			}
			rm := fs[pair]
			vars[pair] = local(rm.GoName, "Keys")
			f.p("\t%s, %s := %s.AddRemove%s(u, %q, %q, in.%s, %q, in.%s%s)", v, vars[pair], impl, k.args, fi.Target, fi.Name, fi.GoName, rm.Name, rm.GoName, k.conv)
		}
	}

	// The edges whose key is on the targets' rows, and the join rows, in
	// the edges' order: each cleared, then its IDs removed, then its IDs
	// added, after the row's UPDATE.
	ok := e.Fields[e.PrimaryKey[0]]
	for ei, ed := range e.Edges {
		if ed.OwnsKey && ed.Kind != plugin.M2M {
			continue
		}
		var clr, rm, add, one = -1, -1, -1, -1
		for i, fi := range fs {
			if fi.Edge != ei {
				continue
			}
			switch fi.Kind {
			case sdl.InputClearEdge:
				clr = i
			case sdl.InputRemoveIDs:
				rm = i
			case sdl.InputAddIDs:
				add = i
			case sdl.InputEdgeID:
				one = i
			}
		}
		if clr < 0 && rm < 0 && add < 0 && one < 0 {
			continue
		}
		tt := r.typeOf[ed.Target]
		if tt == nil {
			return fmt.Errorf("schema %s, edge %s: its target %s has no GraphQL type, which is a bug", e.Name, ed.Name, ed.Target)
		}
		tk, err := r.idKeyOf(f, e, tt)
		if err != nil {
			return err
		}
		field := func(i int) string {
			if i < 0 {
				return ""
			}
			return fs[i].Name
		}
		var parts []string
		for _, x := range []struct {
			name string
			i    int
		}{{"Clear", clr}, {"Remove", rm}, {"Add", add}} {
			if x.i >= 0 {
				parts = append(parts, x.name+": "+vars[x.i])
			}
		}
		edit := fmt.Sprintf("%s.Edit{%s}", impl, strings.Join(parts, ", "))

		if ed.Kind == plugin.M2M {
			f.p("\t%s.Through(u, %q, %q, %s, key, %s())", impl, field(add), tt.Name, edit, edgeFunc(t, ed))
			continue
		}

		// The target's rows hold the key, which holds own for the row.
		tfk, err := fkOf(e, ed, tt.Entity)
		if err != nil {
			return err
		}
		conv, err := r.keyConv(f, tt.Entity, tfk, ok, cmp.Or(t.Key, tfk.Type))
		if err != nil {
			return err
		}
		own := "key"
		if conv != "" {
			own = conv + "(key)"
		}
		if ed.Unique {
			k := "(*" + tk.typ + ")(nil)"
			if one >= 0 {
				k = vars[one]
			}
			c := "false"
			if clr >= 0 {
				c = vars[clr]
			}
			f.p("\t%s.Child(u, %q, %q, %s, %s, %s, %s())", impl, field(one), tt.Name, c, k, own, edgeFunc(t, ed))
			continue
		}
		f.p("\t%s.Children(u, %q, %q, %s, %s, %s())", impl, field(add), tt.Name, edit, own, edgeFunc(t, ed))
	}
	f.p("\tv, err := u.Exec(key, %s.Assignments(set))", velox)
	f.p("\tn, _ := v.(*%s)", e.Name)
	f.p("\treturn n, err")
	f.p("}")
	return nil
}
