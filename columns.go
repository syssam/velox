package velox

import "github.com/syssam/velox/internal/engine"

// HasColumn reports whether the entity named entity has a column named
// column, by the names a Filter is given and FieldEQ takes: the entity's name
// in the schema and the column's in the database. It is for a WithFilter
// filter that narrows every entity having some column, such as deleted_at,
// without a list of those entities:
//
//	velox.WithFilter(func(ctx context.Context, _ *velox.DB, entity string, op velox.Op) (velox.Predicate, error) {
//		if !velox.HasColumn(entity, "deleted_at") {
//			return velox.Allow(), nil
//		}
//		return velox.FieldIsNull("deleted_at"), nil
//	})
//
// It knows the entities whose metadata velox has built, on their first use
// in the process: the entity a Filter is called for always is one. It is
// false for an entity not yet used, or of no such name. When two packages
// each have an entity of that name, one having the column is enough; a
// condition on a column the other lacks fails its statement.
//
// It is a function, not a method of DB: an entity's metadata is the
// process's, not a DB's.
func HasColumn(entity, column string) bool { return engine.HasColumn(entity, column) }
