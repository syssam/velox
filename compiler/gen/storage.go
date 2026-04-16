package gen

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/syssam/velox/dialect/sql"
)

// A SchemaMode defines what type of schema feature a storage driver support.
type SchemaMode uint

const (
	// Unique defines field and edge uniqueness support.
	Unique SchemaMode = 1 << iota

	// Indexes defines indexes support.
	Indexes

	// Cascade defines cascading operations (e.g. cascade deletion).
	Cascade

	// Migrate defines static schema and migration support (e.g. SQL-based).
	Migrate
)

// Support reports whether m support the given mode.
func (m SchemaMode) Support(mode SchemaMode) bool { return m&mode != 0 }

// Storage driver type for codegen.
type Storage struct {
	Name       string             // storage name.
	Builder    reflect.Type       // query builder type.
	Dialects   []string           // supported dialects.
	IdentName  string             // identifier name (fields and funcs).
	Imports    []string           // import packages needed.
	SchemaMode SchemaMode         // schema mode support.
	Ops        func(*Field) []Op  // storage specific operations.
	OpCode     func(Op) string    // operation code for predicates.
	Init       func(*Graph) error // optional init function.
}

var (
	driversMu sync.RWMutex
	drivers   = map[string]*Storage{}
)

func init() {
	RegisterDriver(&Storage{
		Name:      "sql",
		IdentName: "SQL",
		Builder:   reflect.TypeFor[*sql.Selector](),
		Dialects:  []string{"dialect.SQLite", "dialect.MySQL", "dialect.Postgres"},
		Imports: []string{
			"database/sql/driver",
			"github.com/syssam/velox/dialect/sql",
			"github.com/syssam/velox/dialect/sql/sqlgraph",
			"github.com/syssam/velox/dialect/sql/sqljson",
			"github.com/syssam/velox/schema/field",
		},
		SchemaMode: Unique | Indexes | Cascade | Migrate,
		Ops: func(_ *Field) []Op {
			// Note: EqualFold and ContainsFold are already included in stringOps
			// via fieldOps(), so we don't need to add them here.
			return nil
		},
		OpCode: opCodes(sqlCode[:]),
		Init: func(g *Graph) error {
			var with, without []string
			for _, n := range g.Nodes {
				if s, err := n.TableSchema(); err == nil && s != "" {
					with = append(with, n.Name)
				} else {
					without = append(without, n.Name)
				}
			}
			switch {
			case len(with) == 0:
				return nil
			case len(without) > 0:
				return fmt.Errorf("missing schema annotation for %s", strings.Join(without, ", "))
			default:
				if !g.featureEnabled(FeatureSchemaConfig) {
					g.Features = append(g.Features, FeatureSchemaConfig)
				}
				if !g.featureEnabled(featureMultiSchema) {
					g.Features = append(g.Features, featureMultiSchema)
				}
				return nil
			}
		},
	})
}

// RegisterDriver registers a storage driver by name. It is safe for concurrent use.
// Calling RegisterDriver with an already-registered name overwrites the previous entry.
func RegisterDriver(s *Storage) {
	driversMu.Lock()
	defer driversMu.Unlock()
	drivers[s.Name] = s
}

// GetDriver returns the storage driver registered under the given name.
// It returns an error if no driver with that name has been registered.
func GetDriver(name string) (*Storage, error) {
	driversMu.RLock()
	defer driversMu.RUnlock()
	d, ok := drivers[name]
	if !ok {
		return nil, fmt.Errorf("velox/gen: unknown storage driver %q", name)
	}
	return d, nil
}

// ListDrivers returns a sorted list of registered storage driver names.
func ListDrivers() []string {
	driversMu.RLock()
	defer driversMu.RUnlock()
	names := make([]string, 0, len(drivers))
	for name := range drivers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// NewStorage returns the storage driver type from the given string.
// It fails if the provided string is not a valid option. This function
// delegates to GetDriver for backward compatibility.
func NewStorage(s string) (*Storage, error) {
	return GetDriver(s)
}

// String implements the fmt.Stringer interface for template usage.
func (s *Storage) String() string { return s.Name }

var (
	// exceptional operation names in sql.
	sqlCode = [...]string{
		IsNil:  "IsNull",
		NotNil: "NotNull",
	}
)

func opCodes(codes []string) func(Op) string {
	return func(o Op) string {
		if int(o) < len(codes) && codes[o] != "" {
			return codes[o]
		}
		return o.Name()
	}
}

// TableSchemas returns all table schemas in ent/schema (intentionally exported).
func (g *Graph) TableSchemas() ([]string, error) {
	all := make(map[string]struct{})
	for _, n := range g.Nodes {
		s, err := n.TableSchema()
		if err != nil {
			return nil, err
		}
		all[s] = struct{}{}
		for _, e := range n.Edges {
			// {{- if and $e.M2M (not $e.Inverse) (not $e.Through) }}
			if e.M2M() && !e.IsInverse() && e.Through == nil {
				s, err := e.TableSchema()
				if err != nil {
					return nil, err
				}
				all[s] = struct{}{}
			}
		}
	}
	return slices.Sorted(maps.Keys(all)), nil
}

// TableSchema returns the schema name of where the type table resides (intentionally exported).
func (t *Type) TableSchema() (string, error) {
	switch ant := t.EntSQL(); {
	case ant == nil || ant.Schema == "":
		return "", fmt.Errorf("atlas: missing schema annotation for node %q", t.Name)
	default:
		return ant.Schema, nil
	}
}

// TableSchema returns the schema name of where the type table resides (intentionally exported).
func (e *Edge) TableSchema() (string, error) {
	switch ant := e.EntSQL(); {
	case ant == nil || ant.Schema == "":
		return e.Owner.TableSchema()
	default:
		return ant.Schema, nil
	}
}
