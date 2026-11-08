package veloxtype

// ColumnDescriptor describes one column of a velox.Descriptor. For generated code.
// velox.ColumnInfo is its runtime view.
type ColumnDescriptor struct {
	Field         string     // the Go field name
	Name          string     // the column name
	Default       any        // a value of the field's type (or of its element type, for a pointer field), not cyclic; nil when none
	DefaultFunc   func() any // returns a value of the field's type (or of its element type, for a pointer field)
	UpdateDefault func() any // its result is what every update writes to the column; an insert does not call it
	Immutable     bool       // an update may not assign the column
	Version       bool       // the optimistic-locking version: every update adds 1
	Sensitive     bool       // the column holds a secret (velox logs no values)
	// Nullable says the column holds NULL though the field is no pointer:
	// a slice or a map, whose nil velox binds as NULL and reads NULL back
	// into. velox-gen sets it on a json field of an Optional or Nillable
	// schema field that is a slice or a map, and on a Nillable bytes field.
	// Without it, a nil slice or map in an encoded column binds what Encode
	// makes of it (json.Marshal's null), and a nil []byte is handed to the
	// driver as it is. A pointer field is nullable without it; on a field of
	// any other type it is refused.
	Nullable bool
	// RequiredEdge names the Required edge whose foreign key the column holds
	// when that key is NULL-able, as velox-gen makes the key of an edge to
	// the entity's own schema, so that the first row can have none. The
	// database cannot refuse it unset, so velox does, as ent's create check
	// does: Insert, InsertAll and an upsert refuse a row whose key is nil, and
	// Update and Save refuse setting it to nil, with a *ValidationError
	// `missing required edge "<Type>.<edge>"`, before anything is sent. Only
	// the database's ON DELETE SET NULL clears it. velox-gen leaves it empty
	// on a NOT NULL key, which the database enforces; on a column that cannot
	// be NULL it is refused. As with a field's validators and Immutable, a
	// Rewrite or Around hook can still edit the statement after the check.
	RequiredEdge string
	// Encode and Decode are the column's codec, both or neither: with them,
	// velox binds the bytes Encode returns for the field's value, and decodes
	// what it reads with Decode, into a pointer to the field. velox-gen sets
	// them to json.Marshal and json.Unmarshal for every json field, so the
	// generated file, not the core, imports encoding/json, and a program
	// whose entities hold no JSON does not link it. A hand-written Descriptor may
	// plug in another codec. Decode's data is valid only during the call: it
	// must neither change nor keep it. velox may hand it the bytes of a string
	// the driver returned, read in place, so a Decode that writes to them
	// writes to memory that may be read-only. Encode's result is kept in
	// velox.Statement.Args until the statement has run, so Encode must return a
	// new slice on every call, never a buffer it reuses. A primary-key
	// or relation-key column cannot have a codec: conditions on it are refused,
	// so the metadata is. velox never guesses a codec from the Go type, as a
	// []byte field holds bytes, not JSON.
	Encode func(v any) ([]byte, error)
	Decode func(data []byte, v any) error
}

// IndexInfo describes one index.
type IndexInfo struct {
	Columns []int // indexes into velox.EntityInfo.Columns, in the index's order
	Unique  bool  // the index is unique
}
