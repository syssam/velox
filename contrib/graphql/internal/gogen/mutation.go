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

	"github.com/syssam/velox/contrib/graphql/internal/english"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// fieldGQLFunc is the unexported function of t's entity that names, for a
// validation error, the input field of an entity field.
func fieldGQLFunc(t *sdl.Type) string { return lowerFirst(t.Entity.Name) + "FieldGQL" }

// writeMutations writes t's mutation inputs and mutations: CreateXInput and
// CreateX, UpdateXInput and UpdateX, and the function they name input fields
// with, which a type with either has; and DeleteX.
func (r *renderer) writeMutations(f *file, t *sdl.Type) error {
	if t.Create != nil || t.Update != nil {
		if err := r.writeFieldGQL(f, t); err != nil {
			return err
		}
	}
	if t.Create != nil {
		in := "Create" + t.Name + "Input"
		if err := r.writeInput(f, t, in, "create"+t.Name, t.Create); err != nil {
			return err
		}
		if err := r.writeCreate(f, t, in); err != nil {
			return err
		}
	}
	if t.Update != nil {
		in := "Update" + t.Name + "Input"
		if err := r.writeInput(f, t, in, "update"+t.Name, t.Update); err != nil {
			return err
		}
		if err := r.writeUpdate(f, t, in); err != nil {
			return err
		}
	}
	if t.Delete {
		return r.writeDelete(f, t)
	}
	return nil
}

// isList reports whether an input field of the kind k is a list of IDs.
func isList(k sdl.InputKind) bool {
	return k == sdl.InputEdgeIDs || k == sdl.InputAddIDs || k == sdl.InputRemoveIDs
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

// fkOf is the foreign-key field ed.Field of on, the entity that holds it
// (e for an edge that owns its key, else the target). Its error names e, ed
// and on.
func fkOf(e *plugin.Entity, ed *plugin.Edge, on *plugin.Entity) (*plugin.Field, error) {
	fk := fieldOf(on, ed.Field)
	if fk == nil {
		return nil, fmt.Errorf("schema %s, edge %s: its foreign key %s is no field of %s", e.Name, ed.Name, ed.Field, on.Name)
	}
	return fk, nil
}

// joinCols is the join entity of the M2M edge ed of e, and its two fields:
// own holds e's key, and other the target's. An inverse edge's owner is the
// join entity's second field. Its error names e and ed.
func (r *renderer) joinCols(e *plugin.Entity, ed *plugin.Edge) (j *plugin.Entity, own, other *plugin.Field, err error) {
	j = r.ents[ed.Join]
	if j == nil || len(j.Fields) != 2 {
		return nil, nil, nil, fmt.Errorf("schema %s, edge %s: the model has no join entity %s of two fields", e.Name, ed.Name, ed.Join)
	}
	// velox-gen's join entity holds the key of the edge declared with
	// edge.To first: an inverse edge's owner is its second.
	own, other = j.Fields[0], j.Fields[1]
	if ed.Inverse {
		own, other = other, own
	}
	return j, own, other, nil
}

// checkExists returns the gqlimpl.CheckExists call on the rows of tgt whose
// key pk is keys: with list, the slice keys names, all len(keys) of them
// expected (pk.In); without, the one key keys names (pk.EQ).
func checkExists(impl, tgt, pk, keys string, list bool, at, typ string) string {
	pred, n := "EQ("+keys+")", "1"
	if list {
		pred, n = "In("+keys+"...)", "len("+keys+")"
	}
	return fmt.Sprintf("%s.CheckExists(ctx, db.Query[%s]().Where(%s.%s), %s, %s, %q)", impl, tgt, pk, pred, n, at, typ)
}

// writeRepointed writes the link of a target-held key: the UPDATE that sets
// fkh to v on the rows ids names whose key is NULL, then gqlimpl.Repointed
// on a count of cnt expected rows. With countAfter false (writeCreate) the
// count is the UPDATE's own row count. With countAfter true (writeUpdate) it
// is a separate Count of the rows of ids that hold v, after the UPDATE,
// because a re-link to the same owner changes no row on MySQL without
// clientFoundRows.
func (r *renderer) writeRepointed(f *file, indent string, tgt *plugin.Entity, fkh, v, ids, cnt string, countAfter bool, fi sdl.InputField, at func(sdl.InputField) string, owner string) {
	impl := f.gqlimpl()
	set := fmt.Sprintf("db.Update[%s]().Set(%s.Set(%s)).Where(%s, %s.IsNull()).Exec(ctx)", tgt.Name, fkh, v, ids, fkh)
	if countAfter {
		f.p("%sif _, err := %s; err != nil {", indent, set)
		f.p("%s\treturn err", indent)
		f.p("%s}", indent)
		f.p("%sif n, err := db.Query[%s]().Where(%s, %s.EQ(%s)).Count(ctx); err != nil {", indent, tgt.Name, ids, fkh, v)
	} else {
		f.p("%sif n, err := %s; err != nil {", indent, set)
	}
	f.p("%s\treturn err", indent)
	f.p("%s} else if err := %s.Repointed(ctx, n, %s, db.Query[%s]().Where(%s, %s.NotNull(), %[6]s.NEQ(%s)), %s, %q, %q); err != nil {", indent, impl, cnt, tgt.Name, ids, fkh, v, at(fi), fi.Target, owner)
	f.p("%s\treturn err", indent)
	f.p("%s}", indent)
}

// writeInput writes the Go type of the mutation input in, whose fields are
// fs, and its String and GoString, which leave out the sensitive fields'
// values.
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
	doc := fmt.Sprintf("%s is the GraphQL input %[1]s, the input of %s. A field that is a pointer, or a list, is optional: nil means not given.", in, mutation)
	if slices.ContainsFunc(fs, func(fi sdl.InputField) bool { return fi.Kind == sdl.InputClear || fi.Kind == sdl.InputClearEdge }) {
		doc += " A clear field that is true clears a field or an edge, and is refused with the field or the edge's ID given too."
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
// a string for an ID, a []string for a list of IDs and a bool for a clear;
// a pointer when it is not Required, but for a list or a bool.
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
	case sdl.InputEdgeIDs, sdl.InputAddIDs, sdl.InputRemoveIDs:
		return "[]string", nil
	case sdl.InputClear, sdl.InputClearEdge:
		return "bool", nil
	default:
		return "", fmt.Errorf("velox-graphql writes no mutation-input field of kind %d, which is a bug: schema %s, field %s", fi.Kind, t.Entity.Name, fi.Name)
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
	for _, edit := range slices.Backward(edits) {
		typ = typ[:edit.from] + edit.text + typ[edit.to:]
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
	f.doc("", fmt.Sprintf("%s creates %s %s from input, with its edges, as the mutation %s does, and returns the row as written. It runs atomically on the DB veloxgql.DBFromContext reads from ctx, under the tenant scope and the policies. See \"Mutations\" in the documentation of package github.com/syssam/velox/contrib/graphql for what each input field does, what the caller may link and change, and the errors.", fn, english.Article(n), n, "create"+n))
	f.p("func %s(ctx %s.Context, input %s) (*%s, error) {", fn, ctx, in, e.Name)
	writeDecodes(f, t.Create, dec, order, nil)

	// given opens, at indent, the block of a field that is not Required:
	// it runs only when the field is given. It returns the block's indent
	// and its end.
	given := func(indent string, i int) (string, func()) {
		switch fi := t.Create[i]; {
		case fi.Kind == sdl.InputEdgeIDs:
			f.p("%sif len(%s) > 0 {", indent, dec[i].v)
		case !fi.Required:
			f.p("%sif input.%s != nil {", indent, fi.GoName)
		default:
			return indent, func() {}
		}
		return indent + "\t", func() { f.p("%s}", indent) }
	}

	// The fields, and the columns they set, whose validators run before any
	// statement: an invalid field costs no query.
	f.p("\tvar x %s", e.Name)
	var required []string
	for _, fi := range t.Create {
		if fi.Kind == sdl.InputValue && fi.Required {
			required = append(required, e.Handles+"."+e.Fields[fi.Field].GoName)
		}
	}
	f.p("\tcols := []%s.Column[%s]{%s}", velox, e.Name, strings.Join(required, ", "))
	for i, fi := range t.Create {
		if fi.Kind != sdl.InputValue {
			continue
		}
		fd := e.Fields[fi.Field]
		dst, tmp := "x."+fd.GoName, "v"+strconv.Itoa(i)
		// An insert fills the Default into NULL as well, and the input's nil
		// pointer cannot say the client gave null.
		if fd.Nullable && fd.HasDefault && !fi.Required {
			f.p("\tif err := %s.NotNull(ctx, %s, %q, %q); err != nil {", impl, at(fi), "input", fi.Name)
			f.p("\t\treturn nil, err")
			f.p("\t}")
		}
		indent, end := given("\t", i)
		col := func() {
			if !fi.Required {
				f.p("%scols = append(cols, %s.%s)", indent, e.Handles, fd.GoName)
			}
		}
		if d, ok := dec[i]; ok { // JSON
			if fd.Pointer {
				f.p("%s%s = &%s", indent, dst, d.v)
			} else {
				f.p("%s%s = %s", indent, dst, d.v)
			}
			col()
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
		// An insert fills the Default into a zero value, so the client's "" or
		// zero time would not be what it stores. An enum's input is never "".
		if fd.HasDefault && !fd.Pointer && fd.Type != "enum" {
			f.p("%sif err := %s.NotZero(%s, %s); err != nil {", indent, impl, at(fi), dst)
			f.p("%s\treturn nil, err", indent)
			f.p("%s}", indent)
		}
		col()
		end()
	}
	f.p("\tif err := %s.Validate(&x, cols...); err != nil {", velox)
	f.p("\t\treturn nil, %s.WriteError(err, %q, %q, %s)", impl, in, r.outPkg+"."+e.Name, fieldGQLFunc(t))
	f.p("\t}")

	f.p("\tvar out *%s", e.Name)
	f.p("\terr%s %s.Atomic(ctx, func(ctx %s.Context, db *%s.DB) error {", map[bool]string{true: " =", false: " :="}[len(order) > 0], impl, ctx, velox)
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
			fk, err := fkOf(e, ed, e)
			if err != nil {
				return err
			}
			conv, err := r.keyConv(f, e, fk, d.key.fd, tt.Key)
			if err != nil {
				return err
			}
			indent, end := given("\t\t", i)
			ret(indent, checkExists(impl, tgt.Name, pk, d.v, false, at(fi), fi.Target))
			setKey(f, indent, "x."+fk.GoName, d.v, conv, "v"+strconv.Itoa(i), fk.Pointer)
			end()
		case fi.Kind == sdl.InputEdgeIDs && ed.Kind == plugin.M2M:
			indent, end := given("\t\t", i)
			ret(indent, checkExists(impl, tgt.Name, pk, d.v, true, at(fi), fi.Target))
			end()
		}
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
			j, own, other, err := r.joinCols(e, ed)
			if err != nil {
				return err
			}
			indent, end := given("\t\t", i)
			f.p("%srows := make([]%s, len(%s))", indent, j.Name, d.v)
			f.p("%sfor i, k := range %s {", indent, d.v)
			f.p("%s\trows[i] = %s{%s: x.%s, %s: k}", indent, j.Name, own.GoName, ok.GoName, other.GoName)
			f.p("%s}", indent)
			ret(indent, fmt.Sprintf("db.InsertAll(rows).OnConflict(%s.%s, %s.%s).DoNothing().Exec(ctx)", j.Handles, own.GoName, j.Handles, other.GoName))
			end()
			continue
		}
		// The target's rows hold the key: re-point them.
		tfk, err := fkOf(e, ed, tgt)
		if err != nil {
			return err
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
		// is another owner's. sdl gives a key that is never NULL no such
		// input: it is an owner's on every row.
		if !tfk.Nullable {
			return fmt.Errorf("schema %s, edge %s: an input re-points %s's rows, whose key %s is never NULL, which is a bug", e.Name, ed.Name, tgt.Name, tfk.Name)
		}
		indent, end := given("\t\t", i)
		ids, n := pk+".In("+d.v+"...)", "len("+d.v+")"
		if fi.Kind == sdl.InputEdgeID {
			ids, n = pk+".EQ("+d.v+")", "1"
		}
		fkh := tgt.Handles + "." + tfk.GoName
		r.writeRepointed(f, indent, tgt, fkh, v, ids, n, false, fi, at, t.Name)
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
		case sdl.InputEdgeID, sdl.InputEdgeIDs, sdl.InputAddIDs, sdl.InputRemoveIDs:
			k, err := r.idKeyOf(f, e, r.types[fi.Target])
			if err != nil {
				return nil, nil, err
			}
			d := decoded{v: "k" + strconv.Itoa(i), typ: k.typ, key: k}
			src := "input." + fi.GoName
			if isList(fi.Kind) {
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

// writeDecodes writes the decodes: first self's, the decode of the ID of the
// row a mutation changes, when it is not nil; then each of a field given. It
// returns the error of each before any statement.
func writeDecodes(f *file, fs []sdl.InputField, dec map[int]decoded, order []int, self *decoded) {
	if len(order) == 0 && self == nil {
		return
	}
	f.p("\tvar (")
	if self != nil {
		f.p("\t\t%s %s", self.v, self.typ)
	}
	for _, i := range order {
		f.p("\t\t%s %s", dec[i].v, dec[i].typ)
	}
	f.p("\t\terr error")
	f.p("\t)")
	if self != nil {
		f.p("\tif %s, err = %s; err != nil {", self.v, self.call)
		f.p("\t\treturn nil, err")
		f.p("\t}")
	}
	for _, i := range order {
		d, fi := dec[i], fs[i]
		indent := "\t"
		if !fi.Required && !isList(fi.Kind) {
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

// writeUpdate writes UpdateX, which decodes the ID of the row and its input
// in, refuses an input with no single meaning, then in one transaction sets
// the row's fields and its keys, checks the row, writes its edges, and reads
// the row back.
func (r *renderer) writeUpdate(f *file, t *sdl.Type, in string) error {
	e, n := t.Entity, t.Name
	what := "schema " + e.Name
	fn, mutation := "Update"+n, "update"+n
	if err := r.declare(fn, what); err != nil {
		return err
	}
	if len(e.PrimaryKey) != 1 {
		return fmt.Errorf("%s: updateX takes the ID of a key of one column, and %s's is %d columns, which is a bug", what, e.Name, len(e.PrimaryKey))
	}
	ctx, velox, impl := f.use("context", "context"), f.velox(), f.gqlimpl()
	at := func(fi sdl.InputField) string { return inputAt(in, fi) }
	fs := t.Update

	self, err := r.idKeyOf(f, e, t)
	if err != nil {
		return err
	}
	selfDec := &decoded{v: "key", typ: self.typ, key: self, call: fmt.Sprintf("%s.DecodeID%s(%q, id, %q%s)", impl, self.args, mutation+".id", n, self.conv)}
	pk := e.Handles + "." + self.fd.GoName
	dec, order, err := r.decodes(f, t, in, fs)
	if err != nil {
		return err
	}

	f.p("")
	f.doc("", fmt.Sprintf("%s updates the %s with the ID id from input, as the mutation %s does, and returns the row as written. It runs atomically on the DB veloxgql.DBFromContext reads from ctx, under the tenant scope and the policies. See \"Mutations\" in the documentation of package github.com/syssam/velox/contrib/graphql for what each input field does, what the caller may link and change, and the errors.", fn, n, mutation))
	f.p("func %s(ctx %s.Context, id string, input %s) (*%s, error) {", fn, ctx, in, e.Name)
	writeDecodes(f, fs, dec, order, selfDec)

	// The inputs with no single meaning.
	for i, fi := range fs {
		var other int
		switch fi.Kind {
		case sdl.InputClear:
			other = slices.IndexFunc(fs, func(x sdl.InputField) bool { return x.Kind == sdl.InputValue && x.Field == fi.Field })
		case sdl.InputClearEdge:
			other = slices.IndexFunc(fs, func(x sdl.InputField) bool { return x.Kind == sdl.InputEdgeID && x.Edge == fi.Edge })
		case sdl.InputRemoveIDs:
			add := slices.IndexFunc(fs, func(x sdl.InputField) bool { return x.Kind == sdl.InputAddIDs && x.Edge == fi.Edge })
			if add < 0 {
				continue
			}
			f.p("\tif err = %s.Disjoint(%q, %q, %q, %s, %s); err != nil {", impl, in, fs[add].Name, fi.Name, dec[add].v, dec[i].v)
			f.p("\t\treturn nil, err")
			f.p("\t}")
			continue
		default:
			continue
		}
		if other < 0 {
			continue
		}
		f.p("\tif input.%s != nil && input.%s {", fs[other].GoName, fi.GoName)
		f.p("\t\treturn nil, %s.InputErrorf(%q)", impl, fmt.Sprintf("%s: %s and %s together", in, fs[other].Name, fi.Name))
		f.p("\t}")
		// The input's nil pointer leaves the column, and null means to clear it.
		f.p("\tif input.%s == nil && !input.%s {", fs[other].GoName, fi.GoName)
		f.p("\t\tif err = %s.NullClears(ctx, %s, %q, %q, %q); err != nil {", impl, at(fs[other]), "input", fs[other].Name, fi.Name)
		f.p("\t\t\treturn nil, err")
		f.p("\t\t}")
		f.p("\t}")
	}

	// The setters of the fields, whose validators run before any statement:
	// an invalid field costs no query.
	f.p("\tvar set []%s.Setter[%s]", velox, e.Name)
	for i, fi := range fs {
		switch fi.Kind {
		case sdl.InputValue:
			fd := e.Fields[fi.Field]
			h := e.Handles + "." + fd.GoName
			f.p("\tif input.%s != nil {", fi.GoName)
			v := "*input." + fi.GoName
			if d, ok := dec[i]; ok { // JSON
				v = d.v
			} else {
				conv, err := r.valueConv(f, e, fd, fi)
				if err != nil {
					return err
				}
				if conv != "" {
					v = conv + "(" + v + ")"
				}
			}
			f.p("\t\tset = append(set, %s.Set(%s))", h, v)
			f.p("\t}")
		case sdl.InputClear:
			null, err := setNull(e, e.Fields[fi.Field])
			if err != nil {
				return err
			}
			f.p("\tif input.%s {", fi.GoName)
			f.p("\t\tset = append(set, %s)", null)
			f.p("\t}")
		}
	}
	f.p("\tif err = %s.ValidateSet(set...); err != nil {", velox)
	f.p("\t\treturn nil, %s.WriteError(err, %q, %q, %s)", impl, in, r.outPkg+"."+e.Name, fieldGQLFunc(t))
	f.p("\t}")

	f.p("\tvar out *%s", e.Name)
	f.p("\terr = %s.Atomic(ctx, func(ctx %s.Context, db *%s.DB) error {", impl, ctx, velox)
	ret := func(indent, call string) {
		f.p("%sif err := %s; err != nil {", indent, call)
		f.p("%s\treturn err", indent)
		f.p("%s}", indent)
	}
	exec := func(indent, call string) {
		f.p("%sif _, err := %s; err != nil {", indent, call)
		f.p("%s\treturn err", indent)
		f.p("%s}", indent)
	}

	// The setters of the keys on the row, after the fields'.
	for i, fi := range fs {
		switch fi.Kind {
		case sdl.InputEdgeID, sdl.InputClearEdge:
			ed := e.Edges[fi.Edge]
			if !ed.OwnsKey {
				continue
			}
			fk, err := fkOf(e, ed, e)
			if err != nil {
				return err
			}
			h := e.Handles + "." + fk.GoName
			if fi.Kind == sdl.InputClearEdge {
				null, err := setNull(e, fk)
				if err != nil {
					return err
				}
				f.p("\t\tif input.%s {", fi.GoName)
				f.p("\t\t\tset = append(set, %s)", null)
				f.p("\t\t}")
				continue
			}
			d := dec[i]
			tt := r.types[fi.Target]
			conv, err := r.keyConv(f, e, fk, d.key.fd, tt.Key)
			if err != nil {
				return err
			}
			v := d.v
			if conv != "" {
				v = conv + "(" + v + ")"
			}
			f.p("\t\tif input.%s != nil {", fi.GoName)
			ret("\t\t\t", checkExists(impl, tt.Entity.Name, tt.Entity.Handles+"."+d.key.fd.GoName, d.v, false, at(fi), fi.Target))
			f.p("\t\t\tset = append(set, %s.Set(%s))", h, v)
			f.p("\t\t}")
		}
	}

	// The row: the UPDATE, or, when there is nothing to set, a locking
	// existence check under the same scope, the Write policy's too
	// (Writable): no edge of a row the caller may read but not change is
	// written.
	f.p("\t\tif len(set) > 0 {")
	f.p("\t\t\tn, err := db.Update[%s]().Set(set...).Where(%s.EQ(key)).Exec(ctx)", e.Name, pk)
	f.p("\t\t\tif err != nil {")
	f.p("\t\t\t\treturn %s.WriteError(err, %q, %q, %s)", impl, in, r.outPkg+"."+e.Name, fieldGQLFunc(t))
	f.p("\t\t\t}")
	f.p("\t\t\tif n == 0 {")
	f.p("\t\t\t\treturn %s.NotFound(%q, %q)", impl, mutation, n)
	f.p("\t\t\t}")
	// -1: the driver did not report the count, and the row is checked
	// as when there is nothing to set.
	f.p("\t\t\tif n < 0 {")
	r.lockedCheck(f, e, pk, mutation, n, impl, velox, "\t\t\t\t")
	f.p("\t\t\t}")
	f.p("\t\t} else {")
	r.lockedCheck(f, e, pk, mutation, n, impl, velox, "\t\t\t")
	f.p("\t\t}")

	// The edges whose key is on the targets' rows, and the join rows: each
	// cleared, then its IDs removed, then its IDs added.
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
		tgt := tt.Entity
		tkey := tgt.Fields[tgt.PrimaryKey[0]]
		tpk := tgt.Handles + "." + tkey.GoName

		if ed.Kind == plugin.M2M {
			j, own, other, err := r.joinCols(e, ed)
			if err != nil {
				return err
			}
			jo, jt := j.Handles+"."+own.GoName, j.Handles+"."+other.GoName
			if clr >= 0 || rm >= 0 {
				// Join rows are scoped by their sides' tenants, by no policy:
				// only those to a target the caller may read, under the tenant scope and the
				// target's Read policy, as add's CheckExists checks, are
				// deleted. A link to a target hidden from the caller stays.
				k, err := r.idKeyOf(f, e, tt)
				if err != nil {
					return err
				}
				un := "unlink" + strconv.Itoa(ei)
				f.p("\t\t%s := func(ks []%s) error {", un, k.typ)
				f.p("\t\t\tvs, err := %s.Readable(ctx, db.Query[%s]().Where(%s.In(ks...)).Columns(%s), len(ks), func(x *%s) %s { return x.%s })", impl, tgt.Name, tpk, tpk, tgt.Name, k.typ, tkey.GoName)
				f.p("\t\t\tif err != nil || len(vs) == 0 {")
				f.p("\t\t\t\treturn err")
				f.p("\t\t\t}")
				f.p("\t\t\t_, err = db.Delete[%s]().Where(%s.EQ(key), %s.In(vs...)).Exec(ctx)", j.Name, jo, jt)
				f.p("\t\t\treturn err")
				f.p("\t\t}")
				if clr >= 0 {
					f.p("\t\tif input.%s {", fs[clr].GoName)
					ret("\t\t\t", fmt.Sprintf("%s.UnlinkAll(ctx, db.Query[%s]().Where(%s.EQ(key)).OrderBy(%s.Asc(), %s.Asc()), func(j *%s) %s { return j.%s }, %s)", impl, j.Name, jo, jo, jt, j.Name, k.typ, other.GoName, un))
					f.p("\t\t}")
				}
				if rm >= 0 {
					v := dec[rm].v
					f.p("\t\tif len(%s) > 0 {", v)
					ret("\t\t\t", fmt.Sprintf("%s(%s)", un, v))
					f.p("\t\t}")
				}
			}
			if add >= 0 {
				v := dec[add].v
				f.p("\t\tif len(%s) > 0 {", v)
				ret("\t\t\t", checkExists(impl, tgt.Name, tpk, v, true, at(fs[add]), fs[add].Target))
				f.p("\t\t\trows := make([]%s, len(%s))", j.Name, v)
				f.p("\t\t\tfor i, c := range %s {", v)
				f.p("\t\t\t\trows[i] = %s{%s: key, %s: c}", j.Name, own.GoName, other.GoName)
				f.p("\t\t\t}")
				ret("\t\t\t", fmt.Sprintf("db.InsertAll(rows).OnConflict(%s, %s).DoNothing().Exec(ctx)", jo, jt))
				f.p("\t\t}")
			}
			continue
		}

		// The target's rows hold the key. Only a child no owner holds, or
		// this one, is taken, as on create: one another owner holds is
		// refused. Whether each ID to add is linked is counted after the
		// UPDATE, which matches no child this row already holds: the
		// UPDATE's own row count is not read, since MySQL counts only the
		// rows it changed unless the DSN has clientFoundRows=true.
		tfk, err := fkOf(e, ed, tgt)
		if err != nil {
			return err
		}
		if !tfk.Nullable {
			return fmt.Errorf("schema %s, edge %s: an input re-points %s's rows, whose key %s is never NULL, which is a bug", e.Name, ed.Name, tgt.Name, tfk.Name)
		}
		conv, err := r.keyConv(f, tgt, tfk, ok, cmp.Or(t.Key, tfk.Type))
		if err != nil {
			return err
		}
		v := "key"
		if conv != "" {
			v = conv + "(key)"
		}
		fkh := tgt.Handles + "." + tfk.GoName
		tnull, err := setNull(tgt, tfk)
		if err != nil {
			return err
		}
		if clr >= 0 {
			f.p("\t\tif input.%s {", fs[clr].GoName)
			exec("\t\t\t", fmt.Sprintf("db.Update[%s]().Set(%s).Where(%s.EQ(%s)).Exec(ctx)", tgt.Name, tnull, fkh, v))
			f.p("\t\t}")
		}
		if rm >= 0 {
			d := dec[rm].v
			f.p("\t\tif len(%s) > 0 {", d)
			exec("\t\t\t", fmt.Sprintf("db.Update[%s]().Set(%s).Where(%s.In(%s...), %s.EQ(%s)).Exec(ctx)", tgt.Name, tnull, tpk, d, fkh, v))
			f.p("\t\t}")
		}
		// Each link (writeRepointed) counts the IDs linked to this row after
		// the UPDATE under the tenant and the Read policy, not the Write
		// policy: a child already linked here that the Write policy hides is
		// a link that exists, and adding it again changes nothing. A child
		// with a NULL key the Write policy hides is not linked by the
		// UPDATE, counts 0, and is NOT_FOUND.
		if add >= 0 {
			d := dec[add].v
			f.p("\t\tif len(%s) > 0 {", d)
			r.writeRepointed(f, "\t\t\t", tgt, fkh, v, tpk+".In("+d+"...)", "len("+d+")", true, fs[add], at, t.Name)
			f.p("\t\t}")
		}
		if one >= 0 {
			// The new child must be one the caller may read, or the
			// re-point is NOT_FOUND, as for an ID that names no child:
			// otherwise StillLinked, which leaves the new child out, would
			// tell a guess of a hidden child's ID from any other ID. The
			// child this row holds now, unless it is the new one, is let go
			// next: the key is unique. That UPDATE is under the caller's
			// scope, so a child it leaves holding the key is one the caller
			// may not change, hidden by the tenant or a policy: StillLinked
			// refuses the re-point before the link would fail on the unique
			// key, and the child stays linked. It looks whatever the
			// UPDATE's row count, which may be unknown (-1).
			d := dec[one].v
			f.p("\t\tif input.%s != nil {", fs[one].GoName)
			ret("\t\t\t", checkExists(impl, tgt.Name, tpk, d, false, at(fs[one]), fs[one].Target))
			exec("\t\t\t", fmt.Sprintf("db.Update[%s]().Set(%s).Where(%s.EQ(%s), %s.NEQ(%s)).Exec(ctx)", tgt.Name, tnull, fkh, v, tpk, d))
			ret("\t\t\t", fmt.Sprintf("%s.StillLinked(ctx, db.Query[%s]().Where(%s.EQ(%s), %s.NEQ(%s)), %s, %q, %q)", impl, tgt.Name, fkh, v, tpk, d, at(fs[one]), fs[one].Target, t.Name))
			r.writeRepointed(f, "\t\t\t", tgt, fkh, v, tpk+".EQ("+d+")", "1", true, fs[one], at, t.Name)
			f.p("\t\t}")
		}
	}

	// The row as it is afterwards: the row this update wrote, which the
	// caller could read before it, so read back under the tenant scope
	// without the Read policy, as CreateX returns the row it inserted. An
	// update that moves the row out of the caller's Read view succeeds.
	f.p("\t\tx, err := db.Query[%s]().Where(%s.EQ(key)).One(%s.WithoutPolicy(ctx, %q))", e.Name, pk, velox, "graphql: the row this update wrote")
	f.p("\t\tif %s.IsNotFound(err) {", velox)
	f.p("\t\t\treturn %s.NotFound(%q, %q)", impl, mutation, n)
	f.p("\t\t}")
	f.p("\t\tif err != nil {")
	f.p("\t\t\treturn err")
	f.p("\t\t}")
	f.p("\t\tout = x")
	f.p("\t\treturn nil")
	f.p("\t})")
	f.p("\tif err != nil {")
	f.p("\t\treturn nil, err")
	f.p("\t}")
	f.p("\treturn out, nil")
	f.p("}")
	return nil
}

// writeDelete writes DeleteX, which decodes the ID of the row and, in one
// transaction, deletes the row's join rows, after a locking Exists under the scope of
// its DELETE has found it, then the row, and returns the ID.
func (r *renderer) writeDelete(f *file, t *sdl.Type) error {
	e, n := t.Entity, t.Name
	what := "schema " + e.Name
	fn, mutation := "Delete"+n, "delete"+n
	if err := r.declare(fn, what); err != nil {
		return err
	}
	if len(e.PrimaryKey) != 1 {
		return fmt.Errorf("%s: deleteX takes the ID of a key of one column, and %s's is %d columns, which is a bug", what, e.Name, len(e.PrimaryKey))
	}
	self, err := r.idKeyOf(f, e, t)
	if err != nil {
		return err
	}
	ok := self.fd
	pk := e.Handles + "." + ok.GoName

	// The join entities of e's M2M edges, from either side of a pair, each
	// once, with the columns of each that hold e's key: both of them for an
	// edge from e to e.
	type join struct {
		ent  *plugin.Entity
		cols []*plugin.Field
	}
	var joins []*join
	add := func(j *plugin.Entity, cols ...*plugin.Field) {
		i := slices.IndexFunc(joins, func(x *join) bool { return x.ent == j })
		if i < 0 {
			joins = append(joins, &join{ent: j})
			i = len(joins) - 1
		}
		for _, c := range cols {
			if !slices.Contains(joins[i].cols, c) {
				joins[i].cols = append(joins[i].cols, c)
			}
		}
	}
	for _, ed := range e.Edges {
		if ed.Kind != plugin.M2M {
			continue
		}
		j, own, other, err := r.joinCols(e, ed)
		if err != nil {
			return err
		}
		if ed.Target == e.Name {
			add(j, own, other)
		} else {
			add(j, own)
		}
	}

	ctx, velox, impl := f.use("context", "context"), f.velox(), f.gqlimpl()
	idv := "key"
	if self.conv != "" {
		idv = t.Key + "(key)"
	}
	f.p("")
	joinRows := ""
	if len(joins) > 0 {
		joinRows = ", and the join rows that hold its key"
	}
	doc := fmt.Sprintf("%s deletes the %s with the ID id%s, as the mutation %s does, and returns id encoded again from the key. It runs atomically on the DB veloxgql.DBFromContext reads from ctx, under the tenant scope and the policies. See \"Mutations\" in the documentation of package github.com/syssam/velox/contrib/graphql for what it deletes and the errors.", fn, n, joinRows, mutation)
	f.doc("", doc)
	f.p("func %s(ctx %s.Context, id string) (string, error) {", fn, ctx)
	f.p("\tkey, err := %s.DecodeID%s(%q, id, %q%s)", impl, self.args, mutation+".id", n, self.conv)
	f.p("\tif err != nil {")
	f.p("\t\treturn \"\", err")
	f.p("\t}")
	f.p("\terr = %s.Atomic(ctx, func(ctx %s.Context, db *%s.DB) error {", impl, ctx, velox)
	if len(joins) > 0 {
		// The row is checked under the DELETE's scope before its join rows,
		// which no policy scopes, are touched: those of a row the
		// caller may not delete are never deleted.
		r.lockedCheck(f, e, pk, mutation, n, impl, velox, "\t\t")
		for _, j := range joins {
			var ps []string
			for _, c := range j.cols {
				conv, err := r.keyConv(f, j.ent, c, ok, t.Key)
				if err != nil {
					return err
				}
				v := "key"
				if conv != "" {
					v = conv + "(key)"
				}
				ps = append(ps, fmt.Sprintf("%s.%s.EQ(%s)", j.ent.Handles, c.GoName, v))
			}
			where := ps[0]
			if len(ps) > 1 {
				where = fmt.Sprintf("%s.Or(%s)", velox, strings.Join(ps, ", "))
			}
			f.p("\t\tif _, err := db.Delete[%s]().Where(%s).Exec(ctx); err != nil {", j.ent.Name, where)
			f.p("\t\t\treturn err")
			f.p("\t\t}")
		}
	}
	f.p("\t\tn, err := db.Delete[%s]().Where(%s.EQ(key)).Exec(ctx)", e.Name, pk)
	f.p("\t\tif err != nil {")
	f.p("\t\t\treturn err")
	f.p("\t\t}")
	f.p("\t\tif n == 0 {")
	f.p("\t\t\treturn %s.NotFound(%q, %q)", impl, mutation, n)
	f.p("\t\t}")
	// -1: the driver did not report the count. The row may not have been
	// deleted, so the mutation fails closed and the transaction rolls back.
	f.p("\t\tif n < 0 {")
	f.p("\t\t\treturn %s.CountUnknown(%q)", impl, mutation)
	f.p("\t\t}")
	f.p("\t\treturn nil")
	f.p("\t})")
	f.p("\tif err != nil {")
	f.p("\t\treturn \"\", err")
	f.p("\t}")
	f.p("\treturn %s.EncodeID(%q, %s), nil", f.veloxgql(), n, idv)
	f.p("}")
	return nil
}

// setNull is the setter that sets the nullable field fd of e to NULL: its
// handle's SetNull when the Go field is a pointer, and else Set(nil), for a
// field whose Go type holds nil (a slice, a map, a pointer or an interface),
// whose handle has no SetNull: its ColumnDesc says Nullable, or its Go type
// is a pointer, so the core binds that nil as SQL NULL. A field of another
// Go type cannot be cleared: an error, which velox-gen's rule 48 keeps from
// its models.
func setNull(e *plugin.Entity, fd *plugin.Field) (string, error) {
	h := e.Handles + "." + fd.GoName
	switch {
	case fd.Pointer:
		return h + ".SetNull()", nil
	case fd.Kind == "slice" || fd.Kind == "map" || fd.Kind == "ptr" || fd.Kind == "interface" || fd.Type == "bytes":
		return h + ".Set(nil)", nil
	}
	return "", fmt.Errorf("schema %s, field %s: the model says its column holds NULL, and its Go type %s holds no nil; make the field Nillable", e.Name, fd.Name, cmp.Or(fd.ElemType, fd.GoType))
}

// lockedCheck writes the check of an update's or a delete's row: a Writable
// Exists, under the scope of the write, which finds no row the caller may not
// change. Where the dialect has locking reads (everywhere but SQLite) it
// locks the row with ForUpdate, which Atomic's transaction lets it do, so the
// row stays the caller's until the edges are written. SQLite has none, and
// its transactions lock the database. The dialect is read at run time, from
// the DB the operation runs on.
func (r *renderer) lockedCheck(f *file, e *plugin.Entity, pk, mutation, n, impl, velox, ind string) {
	f.p("%schk := db.Query[%s]().Where(%s.EQ(key)).Writable()", ind, e.Name, pk)
	f.p("%sif db.Dialect() != %s.SQLite {", ind, velox)
	f.p("%s\tchk = chk.ForUpdate()", ind)
	f.p("%s}", ind)
	f.p("%sif ok, err := chk.Exists(ctx); err != nil {", ind)
	f.p("%s\treturn err", ind)
	f.p("%s} else if !ok {", ind)
	f.p("%s\treturn %s.NotFound(%q, %q)", ind, impl, mutation, n)
	f.p("%s}", ind)
}
