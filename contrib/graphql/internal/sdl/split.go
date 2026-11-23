package sdl

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/syssam/velox/plugin"
)

// sharedFile is the file of the SDL that is not an entity's: all of it
// unless Config.PerEntity.
const sharedFile = "velox.graphql"

// emit is what the SDL writes and where (ADR 0125): under Config.Entities a
// part of the schema's types, and under Config.PerEntity the file each
// element goes to. A nil *emit writes everything into sharedFile.
type emit struct {
	full    map[*object]bool   // the objects whose type and inputs are written
	inputs  map[string]bool    // the inputs written of the objects that are not full, by name
	dropped map[string]bool    // the type names the SDL does not declare: a field naming one is left out
	enumAt  map[string]*object // by name, the object whose block declares an enum; absent for the shared file
	shared  []gqlEnum          // the enums the shared file declares, in the objects' order
	files   map[*object]string // the file of each object written, under Config.PerEntity
	skip    map[string]bool    // the enums not written: no written element names them
	byInput map[string]*object // each input's object, by the input's name
	subset  bool               // Config.Entities names a part of the schema
}

// writesType reports whether o's type is written.
func (e *emit) writesType(o *object) bool { return e == nil || e.full[o] }

// writes reports whether o's input name is written.
func (e *emit) writes(o *object, name string) bool {
	return e == nil || e.full[o] || e.inputs[name]
}

// written reports whether anything of o is written.
func (e *emit) written(o *object) bool { return e == nil || e.full[o] || e.partial(o) }

// partial reports whether some of o's inputs, and not its type, are written.
func (e *emit) partial(o *object) bool {
	if e == nil || e.full[o] {
		return false
	}
	for _, n := range inputNames(o) {
		if e.inputs[n] {
			return true
		}
	}
	return false
}

// file is the file o's elements go to.
func (e *emit) file(o *object) string {
	if e == nil || e.files[o] == "" {
		return sharedFile
	}
	return e.files[o]
}

// inputNames are the names of the inputs o has.
func inputNames(o *object) []string {
	var out []string
	if o.order != nil {
		out = append(out, o.name+"Order")
	}
	if o.hasWhere {
		out = append(out, o.name+"WhereInput")
	}
	if o.create != nil {
		out = append(out, "Create"+o.name+"Input")
	}
	if o.update != nil {
		out = append(out, "Update"+o.name+"Input")
	}
	return out
}

// inputFieldsOf are the fields of o's input name.
func inputFieldsOf(o *object, name string) []gqlField {
	switch name {
	case o.name + "WhereInput":
		return o.where
	case "Create" + o.name + "Input":
		return o.create
	case "Update" + o.name + "Input":
		return o.update
	}
	return nil // the order's: a direction and its own enum
}

// typeRef finds the type names of a field's arguments.
var typeRef = regexp.MustCompile(`:\s*\[*\s*([_A-Za-z][_0-9A-Za-z]*)`)

// refs are the type names f names: its type's and its arguments'.
func refs(f gqlField) []string {
	out := []string{strings.Trim(f.typ, "[]! ")}
	for _, m := range typeRef.FindAllStringSubmatch(f.args, -1) {
		out = append(out, m[1])
	}
	return out
}

// plan returns what g writes under cfg, or nil when it writes all of it to
// one file.
func (g *graph) plan(cfg Config) (*emit, error) {
	if !cfg.PerEntity && len(cfg.Entities) == 0 {
		return nil, nil
	}
	e := &emit{full: map[*object]bool{}, inputs: map[string]bool{}, dropped: map[string]bool{}, enumAt: map[string]*object{},
		files: map[*object]string{}, skip: map[string]bool{}, byInput: map[string]*object{}, subset: len(cfg.Entities) > 0}
	if err := e.choose(g, cfg.Entities); err != nil {
		return nil, err
	}
	e.closeInputs(g)
	for _, o := range g.objects {
		if e.full[o] {
			continue
		}
		e.dropped[o.name], e.dropped[o.name+"Connection"], e.dropped[o.name+"Edge"] = true, true, true
		for _, n := range inputNames(o) {
			if !e.inputs[n] {
				e.dropped[n] = true
			}
		}
		if !e.inputs[o.name+"Order"] {
			e.dropped[o.name+"OrderField"] = true
		}
	}
	e.placeEnums(g, cfg.PerEntity)
	if cfg.PerEntity {
		if err := e.name(g, cfg.Veloxe); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// choose marks the objects of names full, every object when names is empty.
func (e *emit) choose(g *graph, names []string) error {
	if len(names) == 0 {
		for _, o := range g.objects {
			e.full[o] = true
		}
		return nil
	}
	var unknown, typeless []string
	for _, n := range names {
		switch o := g.byName[n]; {
		case o != nil:
			e.full[o] = true
		case g.ents[n] != nil:
			typeless = append(typeless, n)
		default:
			unknown = append(unknown, n)
		}
	}
	switch {
	case len(unknown) > 0:
		return fmt.Errorf("velox-graphql: entities= names %s, which the schema package does not declare", strings.Join(unknown, ", "))
	case len(typeless) > 0:
		return fmt.Errorf("velox-graphql: entities= names %s, which has no GraphQL type: a join entity, or graphql.Skip(graphql.SkipType)", strings.Join(typeless, ", "))
	}
	return nil
}

// closeInputs marks written each input a written input names, however far:
// graphql-go binds an input to its Go struct and refuses a Go field its SDL
// lacks, so a where input keeps the predicates on an edge to an object left
// out, and that object's where input is written.
func (e *emit) closeInputs(g *graph) {
	for _, o := range g.objects {
		for _, n := range inputNames(o) {
			e.byInput[n] = o
		}
	}
	var work []string
	for _, o := range g.objects {
		if e.full[o] {
			work = append(work, inputNames(o)...)
		}
	}
	for len(work) > 0 {
		n := work[len(work)-1]
		work = work[:len(work)-1]
		for _, f := range inputFieldsOf(e.byInput[n], n) {
			for _, r := range refs(f) {
				if t := e.byInput[r]; t != nil && !e.full[t] && !e.inputs[r] {
					e.inputs[r] = true
					work = append(work, r)
				}
			}
		}
	}
}

// keeps reports whether a field of a written element is written: it names
// no type the SDL leaves out.
func (e *emit) keeps(f gqlField) bool {
	if e == nil || len(e.dropped) == 0 {
		return true
	}
	return !slices.ContainsFunc(refs(f), func(r string) bool { return e.dropped[r] })
}

// placeEnums decides where each enum is declared: in the block of the
// object that declares it, unless that object is not written, or under
// perEntity another object's elements name it too; then in the shared file.
// Under Config.Entities an enum no written element names is not written.
func (e *emit) placeEnums(g *graph, perEntity bool) {
	users := map[string]map[*object]bool{}
	use := func(o *object, fs []gqlField) {
		for _, f := range fs {
			if !e.keeps(f) {
				continue
			}
			for _, r := range refs(f) {
				if users[r] == nil {
					users[r] = map[*object]bool{}
				}
				users[r][o] = true
			}
		}
	}
	for _, o := range g.objects {
		if e.full[o] {
			use(o, o.fields)
		}
		for _, n := range inputNames(o) {
			if e.writes(o, n) {
				use(o, inputFieldsOf(o, n))
			}
		}
	}
	for _, o := range g.objects {
		for _, en := range o.enums {
			if en.name == "" || en.shared {
				continue
			}
			us := users[en.name]
			switch {
			case len(us) == 0 && e.subset:
				e.skip[en.name] = true
			case e.written(o) && (!perEntity || len(us) == 0 || (len(us) == 1 && us[o])):
				e.enumAt[en.name] = o
			default:
				e.shared = append(e.shared, en)
			}
		}
	}
}

// name gives each object written its file: velox_<label>.graphql, or under
// compat=veloxe velox_<name in lower case>.graphql, as veloxe named it.
func (e *emit) name(g *graph, veloxe bool) error {
	by := map[string]string{}
	for _, o := range g.objects {
		if !e.full[o] && !e.partial(o) {
			continue
		}
		stem := o.ent.Label
		if veloxe {
			stem = strings.ToLower(o.ent.Name)
		}
		f := "velox_" + stem + ".graphql"
		if prev := by[f]; prev != "" {
			return fmt.Errorf("velox-graphql: sdlfiles=entity writes schema %s and schema %s both to %s", prev, o.ent.Name, f)
		}
		by[f] = o.ent.Name
		e.files[o] = f
	}
	return nil
}

// writesEnumAt reports whether o's block declares en.
func (e *emit) writesEnumAt(o *object, en gqlEnum) bool {
	if en.name == "" || en.shared {
		return false
	}
	if e == nil {
		return true
	}
	return !e.skip[en.name] && e.enumAt[en.name] == o
}

// files are the SDL's files as w wrote them: sharedFile first, then each
// entity's, in the order written; or sharedFile alone.
func (w *writer) files() []plugin.File {
	if w.parts == nil {
		return []plugin.File{{Path: sharedFile, Content: w.String()}}
	}
	out := make([]plugin.File, 0, len(w.fileOrder))
	for _, name := range w.fileOrder {
		out = append(out, plugin.File{Path: name, Content: strings.TrimLeft(w.parts[name].String(), "\n")})
	}
	return out
}
