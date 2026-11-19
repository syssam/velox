package schema

// Package is a schema package's options for velox-gen. velox-gen reads them
// from the one exported variable of this type in the package, whatever its
// name, and every velox-gen command (generate, verify, describe, migrate)
// applies them alike:
//
//	// Velox is the package's options for velox-gen.
//	var Velox = schema.Package{PrefixIndexNames: true, Names: schema.VeloxeNames}
//
// A package without one has the zero Package: ent's names.
type Package struct {
	// PrefixIndexNames names each index of a table "<table>_<name>", unless
	// its name already starts with "<table>_": the name a StorageKey gives,
	// and the one velox derives as ent does. PostgreSQL wants index names
	// unique in a database schema, and a mixin's index, listed by many
	// schemas, has one StorageKey for every table. veloxe applications did
	// this with a codegen hook. A name the prefix makes longer than the
	// dialect allows is shortened as Names says, as ent and veloxe shorten
	// every index name.
	PrefixIndexNames bool

	// Names is how velox shortens an index or foreign-key name it derives,
	// or one PrefixIndexNames makes, that is longer than the dialect allows:
	// EntNames, the zero value, or VeloxeNames.
	Names Names
}

// Names is how velox shortens a long index or foreign-key name.
type Names int

const (
	// EntNames shortens a name as ent does: its first bytes, "_", and the
	// hex of its MD5.
	EntNames Names = iota

	// VeloxeNames shortens a name as veloxe did: its first bytes, "_", and
	// the hex of its 128-bit FNV-1 hash. A database veloxe migrated has
	// these names.
	VeloxeNames
)
