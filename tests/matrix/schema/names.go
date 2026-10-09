package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
)

// Shape 13: entities named like packages the generated code imports. Each
// only has to generate, compile, and survive a create + query.

func labelOnly() []velox.Field { return []velox.Field{field.String("label")} }

// Time collides with package time.
type Time struct{ velox.Schema }

// Fields of the Time.
func (Time) Fields() []velox.Field { return labelOnly() }

// Field collides with schema/field.
type Field struct{ velox.Schema }

// Fields of the Field.
func (Field) Fields() []velox.Field { return labelOnly() }

// Errors collides with package errors.
type Errors struct{ velox.Schema }

// Fields of the Errors.
func (Errors) Fields() []velox.Field { return labelOnly() }

// Strings collides with package strings.
type Strings struct{ velox.Schema }

// Fields of the Strings.
func (Strings) Fields() []velox.Field { return labelOnly() }

// Math collides with package math.
type Math struct{ velox.Schema }

// Fields of the Math.
func (Math) Fields() []velox.Field { return labelOnly() }

// Runtime collides with velox/runtime.
type Runtime struct{ velox.Schema }

// Fields of the Runtime.
func (Runtime) Fields() []velox.Field { return labelOnly() }

// An entity named Context generates a root package that does not compile
// (Tx gets a Context field and a Context method); pinned in
// ../known_gaps_test.go.
//
// An entity named Schema is rejected by the generator ("schema name conflicts
// with ent predeclared identifier"), so it cannot be part of this list.
