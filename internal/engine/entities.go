package engine

import (
	"cmp"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
)

// EntityLink is one entity of a generated package's EntityTable: Name, its
// name in the schema, Table, its table, Type, its Go type's package path and
// name (TypeKey), and Get, which returns its metadata. Type is a string, not
// a reflect.Type: every package that reaches EntityInfo would compile the
// type descriptors of reflect.Type's methods.
type EntityLink struct {
	Name, Table, Type string
	Get               func() (*EntityInfo, error)
}

// EntityTable is the entities of one generated package, which a condition by
// name (velox.HasEdge, HasEdgeWith and FieldInSelect) finds its target in when
// its statement runs. Every descriptor of the package names the one table, so
// it is built once, on first use, whatever the number of entities.
type EntityTable struct {
	once    sync.Once
	byName  map[string]int
	byTable map[string][]int // a table name is unique only within its database schema
	byType  map[string]int   // by TypeKey
	links   []EntityLink
}

// Load builds t from build the first time and returns t. build is a
// generated list of LinkEntity calls, which do not panic; were it to, the
// table would stay empty and every lookup fail, never widen.
func (t *EntityTable) Load(build func() []EntityLink) *EntityTable {
	t.once.Do(func() {
		t.links = build()
		t.byName = make(map[string]int, len(t.links))
		t.byTable = make(map[string][]int, len(t.links))
		t.byType = make(map[string]int, len(t.links))
		for i, l := range t.links {
			t.byName[l.Name] = i
			t.byTable[l.Table] = append(t.byTable[l.Table], i)
			if l.Type != "" {
				t.byType[l.Type] = i
			}
		}
	})
	return t
}

// TypeKey is t's package path and name, as targetName gives it for a
// relation's One[T] or Many[T]: EntityLink.Type.
func TypeKey(t reflect.Type) string { return t.PkgPath() + "." + t.Name() }

// entity returns the metadata of the entity name names: the one of that name
// in the schema or, failing that, the one with that table. A name that is one
// entity's and another's table, or the table of two entities, is ambiguous and
// an error, as is a name that is neither.
func (t *EntityTable) entity(name string) (*EntityInfo, error) {
	i, byName := t.byName[name]
	tables := t.byTable[name]
	switch {
	case byName && (len(tables) > 1 || len(tables) == 1 && tables[0] != i):
		return nil, fmt.Errorf("%q is the name of entity %s and the table of another; name the entity", name, t.links[i].Name)
	case !byName && len(tables) > 1:
		return nil, fmt.Errorf("%q is the table of %d entities; name the entity", name, len(tables))
	case !byName && len(tables) == 1:
		i = tables[0]
	case !byName:
		return nil, fmt.Errorf("no entity or table %q", name)
	}
	return t.links[i].Get()
}

// target returns the metadata of the entity of Go type key (TypeKey), the
// target of a relation; ok is false when the table has none.
func (t *EntityTable) target(key string) (*EntityInfo, bool, error) {
	i, ok := t.byType[key]
	if !ok {
		return nil, false, nil
	}
	in, err := t.links[i].Get()
	return in, true, err
}

// entityTable is the table in's descriptor names, or an error that names in
// when it names none: a hand-written descriptor, or one of a package with no
// behaviour, which velox-gen gives no table.
func (in *EntityInfo) entityTable() (*EntityTable, error) {
	if in.entities == nil {
		return nil, fmt.Errorf("%s has no entity table: velox-gen writes one in a package whose schema declares a Policy, Hooks or Interceptors; elsewhere use the relation's typed condition (Rel.Has, Rel.HasWith)", in.Name)
	}
	return in.entities(), nil
}

// PackageEntities returns a copy of the metadata of every entity of the
// table in's descriptor names, sorted by Name. err is in's own build error,
// returned as it is. An entity whose metadata fails to build is an error;
// every such error is joined, and no entities are returned with them.
func PackageEntities(in *EntityInfo, err error) ([]*EntityInfo, error) {
	if err != nil {
		return nil, err
	}
	if in == nil {
		return nil, errors.New("velox: the entity has no metadata")
	}
	t, err := in.entityTable()
	if err != nil {
		return nil, err
	}
	out := make([]*EntityInfo, 0, len(t.links))
	var errs []error
	for _, l := range t.links {
		x, err := l.Get()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, x.clone())
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	slices.SortFunc(out, func(a, b *EntityInfo) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

// clone returns a copy of in that shares no memory a caller can change:
// its slices are copied, and so is a Default that holds references.
func (in *EntityInfo) clone() *EntityInfo {
	c := *in
	c.Columns = slices.Clone(in.Columns)
	for i := range c.Columns {
		if col := &c.Columns[i]; col.copyDefault {
			col.Default = defaultValue(col.Default).Interface()
		}
	}
	c.PrimaryKey = slices.Clone(in.PrimaryKey)
	c.Indexes = slices.Clone(in.Indexes)
	for i := range c.Indexes {
		c.Indexes[i].Columns = slices.Clone(c.Indexes[i].Columns)
	}
	return &c
}
