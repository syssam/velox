package gogen

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/syssam/velox2/contrib/graphql/internal/sdl"
	"github.com/syssam/velox2/plugin"
)

// fieldGQLFunc is the unexported function of t's entity that names, for a
// validation error, the input field of an entity field.
func fieldGQLFunc(t *sdl.Type) string { return lowerFirst(t.Entity.Name) + "FieldGQL" }

// writeMutations writes t's mutation inputs and mutations: CreateXInput and
// CreateX, and the function they name input fields with.
func (r *renderer) writeMutations(f *file, t *sdl.Type) error {
	if t.Create == nil {
		return nil
	}
	if err := r.writeFieldGQL(f, t); err != nil {
		return err
	}
	in := "Create" + t.Name + "Input"
	if err := r.writeInput(f, t, in, "create"+t.Name, t.Create); err != nil {
		return err
	}
	return r.writeCreate(f, t, in)
}

// writeFieldGQL writes t's <entity>FieldGQL: the GraphQL name of the input
// field that sets an entity field, by the field's Go name, which a
// validation error carries; "" for a field in no input. A foreign key's is
// its edge's <edge>ID.
func (r *renderer) writeFieldGQL(f *file, t *sdl.Type) error {
	e := t.Entity
	name := fieldGQLFunc(t)
	if err := r.declare(name, "schema "+e.Name); err != nil {
		return err
	}
	type c struct{ goName, gql string }
	var cases []c
	add := func(goName, gql string) {
		if !slices.ContainsFunc(cases, func(x c) bool { return x.goName == goName }) {
			cases = append(cases, c{goName, gql})
		}
	}
	for _, in := range slices.Concat(t.Create, t.Update) {
		switch {
		case in.Kind == sdl.InputValue:
			add(e.Fields[in.Field].GoName, in.Name)
		case in.Kind == sdl.InputEdgeID && e.Edges[in.Edge].OwnsKey:
			if fk := fieldOf(e, e.Edges[in.Edge].Field); fk != nil {
				add(fk.GoName, in.Name)
			}
		}
	}
	f.p("")
	f.doc("", fmt.Sprintf("%s is the GraphQL name of the mutation-input field that sets the %s field whose Go name is goField, for the errors of its validators: \"\" for a field no input sets.", name, e.Name))
	f.p("func %s(goField string) string {", name)
	if len(cases) > 0 {
		f.p("\tswitch goField {")
		for _, x := range cases {
			f.p("\tcase %q:", x.goName)
			f.p("\t\treturn %q", x.gql)
		}
		f.p("\t}")
	}
	f.p("\treturn \"\"")
	f.p("}")
	return nil
}

// fieldOf is e's field named name; nil when e has none.
func fieldOf(e *plugin.Entity, name string) *plugin.Field {
	for _, fd := range e.Fields {
		if fd.Name == name {
			return fd
		}
	}
	return nil
}

// writeInput writes the Go type of the mutation input in, whose fields are
// fs, and its String and GoString, which leave out the sensitive fields.
func (r *renderer) writeInput(f *file, t *sdl.Type, in, mutation string, fs []sdl.InputField) error {
	e := t.Entity
	if err := r.declare(in, "schema "+e.Name); err != nil {
		return err
	}
	seen := map[string]string{} // as gqlgen matches names → the Go name
	types := make([]string, len(fs))
	for i, fi := range fs {
		key := strings.ToLower(strings.ReplaceAll(fi.GoName, "_", ""))
		if prev, ok := seen[key]; ok {
			return fmt.Errorf("schema %s: the fields %s and %s of %s are one Go field to gqlgen, which ignores case and underscores; rename one with graphql.FieldName", e.Name, prev, fi.GoName, in)
		}
		seen[key] = fi.GoName
		if fi.GoName == "String" || fi.GoName == "GoString" {
			return fmt.Errorf("schema %s: the field %s of %s is the Go field %s, which is the input's method; rename it with graphql.FieldName", e.Name, fi.Name, in, fi.GoName)
		}
		typ, err := r.inputType(f, t, fi)
		if err != nil {
			return err
		}
		types[i] = typ
	}
	f.p("")
	f.doc("", fmt.Sprintf("%s is the GraphQL input %[1]s, the input of %s. A field that is a pointer, or a list, is optional: nil means not given.", in, mutation))
	f.p("type %s struct {", in)
	for i, fi := range fs {
		switch fi.Kind {
		case sdl.InputValue:
			if c := e.Fields[fi.Field].Comment; c != "" {
				f.doc("\t", c)
			}
		case sdl.InputEdgeID:
			f.doc("\t", fmt.Sprintf("%s is the ID of the %s the edge %s leads to.", fi.GoName, fi.Target, e.Edges[fi.Edge].Name))
		case sdl.InputEdgeIDs:
			f.doc("\t", fmt.Sprintf("%s are the IDs of the %s nodes the edge %s leads to.", fi.GoName, fi.Target, e.Edges[fi.Edge].Name))
		}
		f.p("\t%s %s", fi.GoName, types[i])
	}
	f.p("}")
	f.p("")

	// String: a pointer prints as what it points to, or <nil>, and JSON as
	// its text.
	var names, args []string
	f.p("// String returns the input's fields except its sensitive ones.")
	f.p("func (in %s) String() string {", in)
	for i, fi := range fs {
		if fi.Field >= 0 && e.Fields[fi.Field].Sensitive {
			continue
		}
		v := "in." + fi.GoName
		json := fi.Kind == sdl.InputValue && fi.GoType == "veloxgql.JSON"
		switch {
		case strings.HasPrefix(types[i], "*"):
			p := "p" + strconv.Itoa(i)
			f.p("\tvar %s any", p)
			f.p("\tif %s != nil {", v)
			if json {
				f.p("\t\t%s = string(*%s)", p, v)
			} else {
				f.p("\t\t%s = *%s", p, v)
			}
			f.p("\t}")
			v = p
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

// inputType is the Go type of the input field fi: its GoType for a value,
// a string for an ID and a []string for a list of IDs; a pointer when it
// is not Required, but for a list.
func (r *renderer) inputType(f *file, t *sdl.Type, fi sdl.InputField) (string, error) {
	var typ string
	switch fi.Kind {
	case sdl.InputValue:
		fd := t.Entity.Fields[fi.Field]
		var err error
		if typ, err = r.qualify(f, t.Entity, fd, fi.GoType, fi.GoPkg); err != nil {
			return "", err
		}
	case sdl.InputEdgeID:
		typ = "string"
	case sdl.InputEdgeIDs:
		return "[]string", nil
	default:
		return "", fmt.Errorf("velox-graphql writes no create-input field of kind %d, which is a bug: schema %s, field %s", fi.Kind, t.Entity.Name, fi.Name)
	}
	if !fi.Required {
		typ = "*" + typ
	}
	return typ, nil
}

// idKey is how the generated code decodes the IDs of the type tt: the Go
// type of the key (the key field's own), and the decode call's type
// arguments and its conversion, as gqlimpl.DecodeID's or DecodeIDAs's.
type idKey struct {
	typ  string // the key's Go type, as the file names it
	args string // "[int64]", or "As[int64, idspkg.UserID]"
	conv string // ", gqlimpl.AsInt[idspkg.UserID, int64]" for DecodeIDAs; else ""
	fd   *plugin.Field
}

// idKeyOf is the idKey of the Node type tt, the target of an edge of e.
func (r *renderer) idKeyOf(f *file, e *plugin.Entity, tt *sdl.Type) (idKey, error) {
	if tt == nil || !tt.Node {
		return idKey{}, fmt.Errorf("schema %s: an input takes IDs of a type that is not a Node, which is a bug", e.Name)
	}
	tgt := tt.Entity
	tk := tgt.Fields[tgt.PrimaryKey[0]]
	elem := cmp.Or(tk.ElemType, tt.Key)
	if elem == tt.Key {
		return idKey{typ: tt.Key, args: "[" + tt.Key + "]", fd: tk}, nil
	}
	if err := r.converts(tgt, tk, tt.Key); err != nil {
		return idKey{}, err
	}
	typ, err := r.qualify(f, tgt, tk, elem, tk.PkgPath)
	if err != nil {
		return idKey{}, err
	}
	var as string
	switch tt.Key {
	case "string":
		as = "AsString"
	case "[16]byte":
		as = "AsUUID"
	default:
		as = "AsInt"
	}
	return idKey{typ: typ, args: "As[" + tt.Key + ", " + typ + "]", conv: fmt.Sprintf(", %s.%s[%s, %s]", f.gqlimpl(), as, typ, tt.Key), fd: tk}, nil
}

// sameGoType reports whether the fields a and b hold the same Go type; a
// field whose model gives none holds velox's own, as any other may.
func sameGoType(a, b *plugin.Field) bool {
	return a.ElemType == "" || b.ElemType == "" || a.ElemType == b.ElemType && a.PkgPath == b.PkgPath
}

// keyConv is the Go conversion of a key held by from to the Go type of the
// field to, which holds it too ("idspkg.UserID"); "" when both hold the
// same Go type. base is the key's base type, as veloxgql decodes it.
func (r *renderer) keyConv(f *file, toEnt *plugin.Entity, to, from *plugin.Field, base string) (string, error) {
	if sameGoType(to, from) {
		return "", nil
	}
	if err := r.converts(toEnt, to, base); err != nil {
		return "", err
	}
	return r.qualify(f, toEnt, to, to.ElemType, to.PkgPath)
}

// valueConv is the Go conversion of the input field fi's value to the Go
// type of fd, its field ("idspkg.Code"): "" when the input holds fd's own
// Go type.
func (r *renderer) valueConv(f *file, e *plugin.Entity, fd *plugin.Field, fi sdl.InputField) (string, error) {
	if fd.ElemType == "" || fd.ElemType == fi.GoType && (fd.PkgPath == fi.GoPkg || fd.PkgPath == "") {
		return "", nil
	}
	var base string
	switch fd.Type {
	case "string", "enum":
		base = "string"
	case "bytes":
		base = "[]byte"
	case "uuid":
		base = "[16]byte"
	case "time":
		// converts refuses any other Go type when the model gives its kind,
		// and this when it does not (a model written by hand).
		if err := r.converts(e, fd, "time.Time"); err != nil {
			return "", err
		}
		return "", fmt.Errorf("schema %s, field %s: velox-graphql converts a time field only from time.Time, and its Go type is %s; give the field time.Time", e.Name, fd.Name, fd.ElemType)
	default:
		base = fd.Type
	}
	if err := r.converts(e, fd, base); err != nil {
		return "", err
	}
	return r.qualify(f, e, fd, fd.ElemType, fd.PkgPath)
}

// goType is the Go type expression typ of the field fd, as the file names
// it: each package it names imported, or unqualified when it is the
// generated package. typ may be composite, as a JSON field's []ids.Tag or
// map[string]x.T.
func (r *renderer) goType(f *file, e *plugin.Entity, fd *plugin.Field, typ string) (string, error) {
	fset := token.NewFileSet()
	x, err := parser.ParseExprFrom(fset, "", typ, 0)
	if err != nil {
		return "", fmt.Errorf("schema %s, field %s: velox-graphql cannot read its Go type %s: %w", e.Name, fd.Name, typ, err)
	}
	type edit struct {
		from, to int
		text     string
	}
	var edits []edit
	ast.Inspect(x, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || err != nil {
			return err == nil
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		pkg := ""
		if fd.PkgPath != "" && path.Base(fd.PkgPath) == id.Name {
			pkg = fd.PkgPath
		}
		var q string
		if q, err = r.qualify(f, e, fd, id.Name+"."+sel.Sel.Name, pkg); err != nil {
			return false
		}
		edits = append(edits, edit{fset.Position(sel.Pos()).Offset, fset.Position(sel.End()).Offset, q})
		return false
	})
	if err != nil {
		return "", err
	}
	for i := len(edits) - 1; i >= 0; i-- {
		typ = typ[:edits[i].from] + edits[i].text + typ[edits[i].to:]
	}
	return typ, nil
}

// writeCreate writes CreateX, which decodes its input in, then in one
// transaction checks the IDs it names, inserts the entity and writes its
// edges.
func (r *renderer) writeCreate(f *file, t *sdl.Type, in string) error {
	e, n := t.Entity, t.Name
	what := "schema " + e.Name
	fn := "Create" + n
	if err := r.declare(fn, what); err != nil {
		return err
	}
	ctx, velox, impl := f.use("context", "context"), f.velox(), f.gqlimpl()
	at := func(fi sdl.InputField) string { return inputAt(in, fi) }

	dec, order, err := r.decodes(f, t, in, t.Create)
	if err != nil {
		return err
	}

	f.p("")
	f.doc("", fmt.Sprintf("%s creates one %s from input, with its edges, and returns it, as the mutation create%[2]s does. It first decodes every ID and JSON value of input, and fails before any statement on one it cannot decode (BAD_USER_INPUT). It then writes through the DB veloxgql.DBFrom reads from ctx, in one transaction (gqlimpl.Atomic): the operation's, or one of its own. The tenant scope and the policies apply as to any write: each ID must name a row the caller may read, and a row it re-points one the caller may change, or %[1]s fails with NOT_FOUND and keeps nothing. A validator's refusal is BAD_USER_INPUT, naming the input field; velox's other errors are returned as they are.", fn, n))
	f.p("func %s(ctx %s.Context, input %s) (*%s, error) {", fn, ctx, in, e.Name)
	writeDecodes(f, t.Create, dec, order)
	f.p("\tvar out *%s", e.Name)
	f.p("\terr%s %s.Atomic(ctx, func(ctx %s.Context, db *%s.DB) error {", map[bool]string{true: " =", false: " :="}[len(order) > 0], impl, ctx, velox)
	f.p("\t\tvar x %s", e.Name)

	// given opens the block of a field that is not Required: it runs only
	// when the field is given. It returns the block's indent and its end.
	given := func(i int) (string, func()) {
		switch fi := t.Create[i]; {
		case fi.Kind == sdl.InputEdgeIDs:
			f.p("\t\tif len(%s) > 0 {", dec[i].v)
		case !fi.Required:
			f.p("\t\tif input.%s != nil {", fi.GoName)
		default:
			return "\t\t", func() {}
		}
		return "\t\t\t", func() { f.p("\t\t}") }
	}
	ret := func(indent, err string) {
		f.p("%sif err := %s; err != nil {", indent, err)
		f.p("%s\treturn err", indent)
		f.p("%s}", indent)
	}

	// The unique edges whose key is on the row, and the M2M edges: their
	// IDs are checked before the insert.
	for i, fi := range t.Create {
		if fi.Kind != sdl.InputEdgeID && fi.Kind != sdl.InputEdgeIDs {
			continue
		}
		ed, d := e.Edges[fi.Edge], dec[i]
		tt := r.types[fi.Target]
		tgt := tt.Entity
		pk := tgt.Handles + "." + d.key.fd.GoName
		switch {
		case fi.Kind == sdl.InputEdgeID && ed.OwnsKey:
			fk := fieldOf(e, ed.Field)
			if fk == nil {
				return fmt.Errorf("schema %s, edge %s: its foreign key %s is no field of %s", e.Name, ed.Name, ed.Field, e.Name)
			}
			conv, err := r.keyConv(f, e, fk, d.key.fd, tt.Key)
			if err != nil {
				return err
			}
			indent, end := given(i)
			ret(indent, fmt.Sprintf("%s.Exist(ctx, db.Query[%s]().Where(%s.In(%s)), 1, %s, %q)", impl, tgt.Name, pk, d.v, at(fi), fi.Target))
			setKey(f, indent, "x."+fk.GoName, d.v, conv, "v"+strconv.Itoa(i), fk.Pointer)
			end()
		case fi.Kind == sdl.InputEdgeIDs && ed.Kind == plugin.M2M:
			indent, end := given(i)
			ret(indent, fmt.Sprintf("%s.Exist(ctx, db.Query[%s]().Where(%s.In(%s...)), len(%[4]s), %s, %q)", impl, tgt.Name, pk, d.v, at(fi), fi.Target))
			end()
		}
	}

	// The fields.
	for i, fi := range t.Create {
		if fi.Kind != sdl.InputValue {
			continue
		}
		fd := e.Fields[fi.Field]
		dst, tmp := "x."+fd.GoName, "v"+strconv.Itoa(i)
		indent, end := given(i)
		if d, ok := dec[i]; ok { // JSON
			if fd.Pointer {
				f.p("%s%s = &%s", indent, dst, d.v)
			} else {
				f.p("%s%s = %s", indent, dst, d.v)
			}
			end()
			continue
		}
		conv, err := r.valueConv(f, e, fd, fi)
		if err != nil {
			return err
		}
		src := "input." + fi.GoName
		switch {
		case fi.Required:
			setKey(f, indent, dst, src, conv, tmp, fd.Pointer)
		case fd.Pointer && conv == "":
			f.p("%s%s = %s", indent, dst, src)
		default:
			setKey(f, indent, dst, "*"+src, conv, tmp, fd.Pointer)
		}
		end()
	}

	f.p("\t\tif err := db.Insert(&x).Exec(ctx); err != nil {")
	f.p("\t\t\treturn %s.WriteError(err, %q, %q, %s)", impl, in, r.outPkg+"."+e.Name, fieldGQLFunc(t))
	f.p("\t\t}")

	// The edges whose key is on the targets' rows, and the join rows.
	for i, fi := range t.Create {
		if fi.Kind != sdl.InputEdgeID && fi.Kind != sdl.InputEdgeIDs {
			continue
		}
		ed, d := e.Edges[fi.Edge], dec[i]
		tt := r.types[fi.Target]
		tgt := tt.Entity
		pk := tgt.Handles + "." + d.key.fd.GoName
		if fi.Kind == sdl.InputEdgeID && ed.OwnsKey {
			continue
		}
		if len(e.PrimaryKey) != 1 {
			return fmt.Errorf("schema %s, edge %s: velox-graphql writes it by the key of %s, which is %d columns; skip its create input with graphql.Skip(graphql.SkipMutationCreateInput)", e.Name, ed.Name, e.Name, len(e.PrimaryKey))
		}
		ok := e.Fields[e.PrimaryKey[0]]
		if ed.Kind == plugin.M2M {
			j := r.ents[ed.Join]
			if j == nil || len(j.Fields) != 2 {
				return fmt.Errorf("schema %s, edge %s: the model has no join entity %s of two fields", e.Name, ed.Name, ed.Join)
			}
			// velox-gen's join entity holds the key of the edge declared
			// with edge.To first: an inverse edge's owner is its second.
			own, other := j.Fields[0], j.Fields[1]
			if ed.Inverse {
				own, other = other, own
			}
			indent, end := given(i)
			f.p("%srows := make([]%s, len(%s))", indent, j.Name, d.v)
			f.p("%sfor i, k := range %s {", indent, d.v)
			f.p("%s\trows[i] = %s{%s: x.%s, %s: k}", indent, j.Name, own.GoName, ok.GoName, other.GoName)
			f.p("%s}", indent)
			ret(indent, fmt.Sprintf("db.InsertAll(rows).OnConflict(%s.%s, %s.%s).DoNothing().Exec(ctx)", j.Handles, own.GoName, j.Handles, other.GoName))
			end()
			continue
		}
		// The target's rows hold the key: re-point them.
		tfk := fieldOf(tgt, ed.Field)
		if tfk == nil {
			return fmt.Errorf("schema %s, edge %s: its foreign key %s is no field of %s", e.Name, ed.Name, ed.Field, tgt.Name)
		}
		conv, err := r.keyConv(f, tgt, tfk, ok, cmp.Or(t.Key, tfk.Type))
		if err != nil {
			return err
		}
		v := "x." + ok.GoName
		if conv != "" {
			v = conv + "(" + v + ")"
		}
		// Only a child no owner holds is re-pointed, as in ent: one another
		// owner holds is not taken from it. The UPDATE's count checks the
		// IDs, as the tenant scope and the target's Read and Write policies
		// leave out a row the caller may not change; Repointed tells a child
		// the caller may read that another owner holds from one it may not
		// change: it counts, after the UPDATE, the rows of the IDs whose key
		// is another owner's. A key that is never NULL is held by an owner
		// on every row.
		indent, end := given(i)
		ids, n := pk+".In("+d.v+"...)", "len("+d.v+")"
		if fi.Kind == sdl.InputEdgeID {
			ids, n = pk+".EQ("+d.v+")", "1"
		}
		fkh := tgt.Handles + "." + tfk.GoName
		repointed := func(got, linked string) string {
			return fmt.Sprintf("%s.Repointed(ctx, %s, %s, db.Query[%s]().Where(%s), %s, %q, %q)", impl, got, n, tgt.Name, linked, at(fi), fi.Target, t.Name)
		}
		if !tfk.Nullable {
			ret(indent, repointed("0", ids))
		} else {
			f.p("%sif n, err := db.Update[%s]().Set(%s.Set(%s)).Where(%s, %s.IsNull()).Exec(ctx); err != nil {", indent, tgt.Name, fkh, v, ids, fkh)
			f.p("%s\treturn err", indent)
			f.p("%s} else if err := %s; err != nil {", indent, repointed("n", ids+", "+fkh+".NotNull(), "+fkh+".NEQ("+v+")"))
			f.p("%s\treturn err", indent)
			f.p("%s}", indent)
		}
		end()
	}
	f.p("\t\tout = &x")
	f.p("\t\treturn nil")
	f.p("\t})")
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	f.p("\treturn out, nil")
	f.p("}")
	return nil
}

// decoded is an ID, a list of IDs or a JSON value of a mutation input, which
// the mutation decodes before any statement.
type decoded struct {
	v    string // the variable: k<i> for IDs, j<i> for JSON, of the field i
	typ  string // its Go type
	call string // the decode, of the input field
	key  idKey  // an ID's
}

// inputAt is the Go string literal of the input field fi of in, as errors
// name it: "CreatePostInput.authorID".
func inputAt(in string, fi sdl.InputField) string { return strconv.Quote(in + "." + fi.Name) }

// decodes are the decodes of t's mutation input in, whose fields are fs, by
// the index of their input field, and those indexes in order.
func (r *renderer) decodes(f *file, t *sdl.Type, in string, fs []sdl.InputField) (map[int]decoded, []int, error) {
	e, impl := t.Entity, f.gqlimpl()
	at := func(fi sdl.InputField) string { return inputAt(in, fi) }
	dec := map[int]decoded{}
	var order []int
	for i, fi := range fs {
		switch fi.Kind {
		case sdl.InputEdgeID, sdl.InputEdgeIDs:
			k, err := r.idKeyOf(f, e, r.types[fi.Target])
			if err != nil {
				return nil, nil, err
			}
			d := decoded{v: "k" + strconv.Itoa(i), typ: k.typ, key: k}
			src := "input." + fi.GoName
			if fi.Kind == sdl.InputEdgeIDs {
				d.typ = "[]" + k.typ
				d.call = fmt.Sprintf("%s.DecodeIDs%s(ctx, %s, %s, %q%s)", impl, k.args, at(fi), src, fi.Target, k.conv)
			} else {
				if !fi.Required {
					src = "*" + src
				}
				d.call = fmt.Sprintf("%s.DecodeID%s(%s, %s, %q%s)", impl, k.args, at(fi), src, fi.Target, k.conv)
			}
			dec[i] = d
			order = append(order, i)
		case sdl.InputValue:
			fd := e.Fields[fi.Field]
			if fd.Type != "json" {
				continue
			}
			elem := cmp.Or(fd.ElemType, strings.TrimPrefix(fd.GoType, "*"))
			if elem == "" {
				return nil, nil, fmt.Errorf("schema %s, field %s: the model gives no Go type for the JSON field", e.Name, fd.Name)
			}
			typ, err := r.goType(f, e, fd, elem)
			if err != nil {
				return nil, nil, err
			}
			src := "input." + fi.GoName
			if !fi.Required {
				src = "*" + src
			}
			dec[i] = decoded{v: "j" + strconv.Itoa(i), typ: typ, call: fmt.Sprintf("%s.FromJSON[%s](%s, %s)", impl, typ, at(fi), src)}
			order = append(order, i)
		}
	}
	return dec, order, nil
}

// writeDecodes writes the decodes, each of a field given: it returns its
// error before any statement.
func writeDecodes(f *file, fs []sdl.InputField, dec map[int]decoded, order []int) {
	if len(order) == 0 {
		return
	}
	f.p("\tvar (")
	for _, i := range order {
		f.p("\t\t%s %s", dec[i].v, dec[i].typ)
	}
	f.p("\t\terr error")
	f.p("\t)")
	for _, i := range order {
		d, fi := dec[i], fs[i]
		indent := "\t"
		if !fi.Required && fi.Kind != sdl.InputEdgeIDs {
			f.p("\tif input.%s != nil {", fi.GoName)
			indent = "\t\t"
		}
		f.p("%sif %s, err = %s; err != nil {", indent, d.v, d.call)
		f.p("%s\treturn nil, err", indent)
		f.p("%s}", indent)
		if indent != "\t" {
			f.p("\t}")
		}
	}
}

// setKey writes dst = src, through the Go conversion conv when it is not "",
// and, when ptr, through the variable tmp, whose address dst takes.
func setKey(f *file, indent, dst, src, conv, tmp string, ptr bool) {
	v := src
	if conv != "" {
		v = conv + "(" + src + ")"
	}
	switch {
	case !ptr:
		f.p("%s%s = %s", indent, dst, v)
	case conv == "" && !strings.HasPrefix(src, "*"):
		f.p("%s%s = &%s", indent, dst, src)
	default:
		f.p("%s%s := %s", indent, tmp, v)
		f.p("%s%s = &%s", indent, dst, tmp)
	}
}
