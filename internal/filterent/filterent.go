// Package filterent holds the entity a velox.Filter is called for. The root
// names it velox.FilterEntity, an alias, as it names the engine's types
// (decision 0020), and veloxtest makes a stand-in of it here, with no hook
// between the packages. It is not in the engine: a type there with methods
// that DB reaches is described by every package that uses a DB (decision
// 0151).
package filterent

import "github.com/syssam/velox/internal/engine"

// Entity is the entity a Filter is called for. Name is its name in the
// schema, the generated TypeX constant's value. HasColumn reports whether
// it has a column of the given name, as FieldEQ names one, so a filter can
// narrow every entity having some column, such as deleted_at, without a
// list of them:
//
//	velox.WithFilter(func(ctx context.Context, _ *velox.DB, e velox.FilterEntity, op velox.Op) (velox.Predicate, error) {
//		if !e.HasColumn("deleted_at") {
//			return velox.Allow(), nil
//		}
//		return velox.FieldIsNull("deleted_at"), nil
//	})
//
// Both read the entity's own metadata: nothing global, no lock. The zero
// Entity names nothing and has no column.
type Entity struct{ in *engine.EntityInfo }

// New is the Entity of in's metadata.
func New(in *engine.EntityInfo) Entity { return Entity{in} }

// StandIn is an Entity named name that has exactly the given columns:
// veloxtest.FilterEntity.
func StandIn(name string, columns ...string) Entity {
	return Entity{engine.StandInInfo(name, columns...)}
}

// Name is the entity's name in the schema: "Invoice".
func (e Entity) Name() string {
	if e.in == nil {
		return ""
	}
	return engine.EntityName(e.in)
}

// HasColumn reports whether the entity has a column of the given name, as
// FieldEQ finds one.
func (e Entity) HasColumn(column string) bool {
	if e.in == nil {
		return false
	}
	_, ok := engine.ColumnNamed(e.in, column)
	return ok
}
