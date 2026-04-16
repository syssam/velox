package sql

// PredicateFunc is a constraint type for predicate functions.
// It allows generic field types to work with any predicate type that is
// based on func(*Selector).
type PredicateFunc interface {
	~func(*Selector)
}

// StringField is a generic string field that provides type-safe predicate methods.
// It dramatically reduces generated code by defining predicates once via generics.
//
// Usage:
//
//	var Email = sql.StringField[predicate.User]("email")
//	query.Where(user.Email.EQ("test@example.com"))
//	query.Where(user.Email.Contains("@gmail"))
type StringField[P PredicateFunc] string

// Name returns the field name.
func (f StringField[P]) Name() string { return string(f) }

// EQ returns a predicate that checks if the field equals the given value.
func (f StringField[P]) EQ(v string) P {
	return P(FieldEQ(string(f), v))
}

// NEQ returns a predicate that checks if the field does not equal the given value.
func (f StringField[P]) NEQ(v string) P {
	return P(FieldNEQ(string(f), v))
}

// In returns a predicate that checks if the field value is in the given list.
func (f StringField[P]) In(vs ...string) P {
	return P(FieldIn(string(f), vs...))
}

// NotIn returns a predicate that checks if the field value is not in the given list.
func (f StringField[P]) NotIn(vs ...string) P {
	return P(FieldNotIn(string(f), vs...))
}

// GT returns a predicate that checks if the field is greater than the given value.
func (f StringField[P]) GT(v string) P {
	return P(FieldGT(string(f), v))
}

// GTE returns a predicate that checks if the field is greater than or equal to the given value.
func (f StringField[P]) GTE(v string) P {
	return P(FieldGTE(string(f), v))
}

// LT returns a predicate that checks if the field is less than the given value.
func (f StringField[P]) LT(v string) P {
	return P(FieldLT(string(f), v))
}

// LTE returns a predicate that checks if the field is less than or equal to the given value.
func (f StringField[P]) LTE(v string) P {
	return P(FieldLTE(string(f), v))
}

// Contains returns a predicate that checks if the field contains the given substring.
func (f StringField[P]) Contains(v string) P {
	return P(FieldContains(string(f), v))
}

// ContainsFold returns a predicate that checks if the field contains the given substring (case-insensitive).
func (f StringField[P]) ContainsFold(v string) P {
	return P(FieldContainsFold(string(f), v))
}

// HasPrefix returns a predicate that checks if the field has the given prefix.
func (f StringField[P]) HasPrefix(v string) P {
	return P(FieldHasPrefix(string(f), v))
}

// HasSuffix returns a predicate that checks if the field has the given suffix.
func (f StringField[P]) HasSuffix(v string) P {
	return P(FieldHasSuffix(string(f), v))
}

// EqualFold returns a predicate that checks if the field equals the given value (case-insensitive).
func (f StringField[P]) EqualFold(v string) P {
	return P(FieldEqualFold(string(f), v))
}

// IsNull returns a predicate that checks if the field is NULL.
func (f StringField[P]) IsNull() P {
	return P(FieldIsNull(string(f)))
}

// NotNull returns a predicate that checks if the field is not NULL.
func (f StringField[P]) NotNull() P {
	return P(FieldNotNull(string(f)))
}

// IsNil is an alias for IsNull (for Ent compatibility).
func (f StringField[P]) IsNil() P {
	return f.IsNull()
}

// NotNil is an alias for NotNull (for Ent compatibility).
func (f StringField[P]) NotNil() P {
	return f.NotNull()
}

// IntField is a generic integer field that provides type-safe predicate methods.
type IntField[P PredicateFunc] string

// Name returns the field name.
func (f IntField[P]) Name() string { return string(f) }

// EQ returns a predicate that checks if the field equals the given value.
func (f IntField[P]) EQ(v int) P {
	return P(FieldEQ(string(f), v))
}

// NEQ returns a predicate that checks if the field does not equal the given value.
func (f IntField[P]) NEQ(v int) P {
	return P(FieldNEQ(string(f), v))
}

// In returns a predicate that checks if the field value is in the given list.
func (f IntField[P]) In(vs ...int) P {
	return P(FieldIn(string(f), vs...))
}

// NotIn returns a predicate that checks if the field value is not in the given list.
func (f IntField[P]) NotIn(vs ...int) P {
	return P(FieldNotIn(string(f), vs...))
}

// GT returns a predicate that checks if the field is greater than the given value.
func (f IntField[P]) GT(v int) P {
	return P(FieldGT(string(f), v))
}

// GTE returns a predicate that checks if the field is greater than or equal to the given value.
func (f IntField[P]) GTE(v int) P {
	return P(FieldGTE(string(f), v))
}

// LT returns a predicate that checks if the field is less than the given value.
func (f IntField[P]) LT(v int) P {
	return P(FieldLT(string(f), v))
}

// LTE returns a predicate that checks if the field is less than or equal to the given value.
func (f IntField[P]) LTE(v int) P {
	return P(FieldLTE(string(f), v))
}

// IsNull returns a predicate that checks if the field is NULL.
func (f IntField[P]) IsNull() P {
	return P(FieldIsNull(string(f)))
}

// NotNull returns a predicate that checks if the field is not NULL.
func (f IntField[P]) NotNull() P {
	return P(FieldNotNull(string(f)))
}

// IsNil is an alias for IsNull (for Ent compatibility).
func (f IntField[P]) IsNil() P {
	return f.IsNull()
}

// NotNil is an alias for NotNull (for Ent compatibility).
func (f IntField[P]) NotNil() P {
	return f.NotNull()
}

// Int64Field is a generic int64 field that provides type-safe predicate methods.
type Int64Field[P PredicateFunc] string

// Name returns the field name.
func (f Int64Field[P]) Name() string { return string(f) }

// EQ returns a predicate that checks if the field equals the given value.
func (f Int64Field[P]) EQ(v int64) P {
	return P(FieldEQ(string(f), v))
}

// NEQ returns a predicate that checks if the field does not equal the given value.
func (f Int64Field[P]) NEQ(v int64) P {
	return P(FieldNEQ(string(f), v))
}

// In returns a predicate that checks if the field value is in the given list.
func (f Int64Field[P]) In(vs ...int64) P {
	return P(FieldIn(string(f), vs...))
}

// NotIn returns a predicate that checks if the field value is not in the given list.
func (f Int64Field[P]) NotIn(vs ...int64) P {
	return P(FieldNotIn(string(f), vs...))
}

// GT returns a predicate that checks if the field is greater than the given value.
func (f Int64Field[P]) GT(v int64) P {
	return P(FieldGT(string(f), v))
}

// GTE returns a predicate that checks if the field is greater than or equal to the given value.
func (f Int64Field[P]) GTE(v int64) P {
	return P(FieldGTE(string(f), v))
}

// LT returns a predicate that checks if the field is less than the given value.
func (f Int64Field[P]) LT(v int64) P {
	return P(FieldLT(string(f), v))
}

// LTE returns a predicate that checks if the field is less than or equal to the given value.
func (f Int64Field[P]) LTE(v int64) P {
	return P(FieldLTE(string(f), v))
}

// IsNull returns a predicate that checks if the field is NULL.
func (f Int64Field[P]) IsNull() P {
	return P(FieldIsNull(string(f)))
}

// NotNull returns a predicate that checks if the field is not NULL.
func (f Int64Field[P]) NotNull() P {
	return P(FieldNotNull(string(f)))
}

// IsNil is an alias for IsNull (for Ent compatibility).
func (f Int64Field[P]) IsNil() P {
	return f.IsNull()
}

// NotNil is an alias for NotNull (for Ent compatibility).
func (f Int64Field[P]) NotNil() P {
	return f.NotNull()
}

// Float64Field is a generic float64 field that provides type-safe predicate methods.
type Float64Field[P PredicateFunc] string

// Name returns the field name.
func (f Float64Field[P]) Name() string { return string(f) }

// EQ returns a predicate that checks if the field equals the given value.
func (f Float64Field[P]) EQ(v float64) P {
	return P(FieldEQ(string(f), v))
}

// NEQ returns a predicate that checks if the field does not equal the given value.
func (f Float64Field[P]) NEQ(v float64) P {
	return P(FieldNEQ(string(f), v))
}

// In returns a predicate that checks if the field value is in the given list.
func (f Float64Field[P]) In(vs ...float64) P {
	return P(FieldIn(string(f), vs...))
}

// NotIn returns a predicate that checks if the field value is not in the given list.
func (f Float64Field[P]) NotIn(vs ...float64) P {
	return P(FieldNotIn(string(f), vs...))
}

// GT returns a predicate that checks if the field is greater than the given value.
func (f Float64Field[P]) GT(v float64) P {
	return P(FieldGT(string(f), v))
}

// GTE returns a predicate that checks if the field is greater than or equal to the given value.
func (f Float64Field[P]) GTE(v float64) P {
	return P(FieldGTE(string(f), v))
}

// LT returns a predicate that checks if the field is less than the given value.
func (f Float64Field[P]) LT(v float64) P {
	return P(FieldLT(string(f), v))
}

// LTE returns a predicate that checks if the field is less than or equal to the given value.
func (f Float64Field[P]) LTE(v float64) P {
	return P(FieldLTE(string(f), v))
}

// IsNull returns a predicate that checks if the field is NULL.
func (f Float64Field[P]) IsNull() P {
	return P(FieldIsNull(string(f)))
}

// NotNull returns a predicate that checks if the field is not NULL.
func (f Float64Field[P]) NotNull() P {
	return P(FieldNotNull(string(f)))
}

// IsNil is an alias for IsNull (for Ent compatibility).
func (f Float64Field[P]) IsNil() P {
	return f.IsNull()
}

// NotNil is an alias for NotNull (for Ent compatibility).
func (f Float64Field[P]) NotNil() P {
	return f.NotNull()
}

// BoolField is a generic boolean field that provides type-safe predicate methods.
type BoolField[P PredicateFunc] string

// Name returns the field name.
func (f BoolField[P]) Name() string { return string(f) }

// EQ returns a predicate that checks if the field equals the given value.
func (f BoolField[P]) EQ(v bool) P {
	return P(FieldEQ(string(f), v))
}

// NEQ returns a predicate that checks if the field does not equal the given value.
func (f BoolField[P]) NEQ(v bool) P {
	return P(FieldNEQ(string(f), v))
}

// IsNull returns a predicate that checks if the field is NULL.
func (f BoolField[P]) IsNull() P {
	return P(FieldIsNull(string(f)))
}

// NotNull returns a predicate that checks if the field is not NULL.
func (f BoolField[P]) NotNull() P {
	return P(FieldNotNull(string(f)))
}

// IsNil is an alias for IsNull (for Ent compatibility).
func (f BoolField[P]) IsNil() P {
	return f.IsNull()
}

// NotNil is an alias for NotNull (for Ent compatibility).
func (f BoolField[P]) NotNil() P {
	return f.NotNull()
}

// TimeField is a generic time field that provides type-safe predicate methods.
// T is the actual time type (e.g., time.Time).
type TimeField[P PredicateFunc, T any] string

// Name returns the field name.
func (f TimeField[P, T]) Name() string { return string(f) }

// EQ returns a predicate that checks if the field equals the given value.
func (f TimeField[P, T]) EQ(v T) P {
	return P(FieldEQ(string(f), v))
}

// NEQ returns a predicate that checks if the field does not equal the given value.
func (f TimeField[P, T]) NEQ(v T) P {
	return P(FieldNEQ(string(f), v))
}

// In returns a predicate that checks if the field value is in the given list.
func (f TimeField[P, T]) In(vs ...T) P {
	return P(FieldInGeneric(string(f), vs...))
}

// NotIn returns a predicate that checks if the field value is not in the given list.
func (f TimeField[P, T]) NotIn(vs ...T) P {
	return P(FieldNotInGeneric(string(f), vs...))
}

// GT returns a predicate that checks if the field is greater than the given value.
func (f TimeField[P, T]) GT(v T) P {
	return P(FieldGT(string(f), v))
}

// GTE returns a predicate that checks if the field is greater than or equal to the given value.
func (f TimeField[P, T]) GTE(v T) P {
	return P(FieldGTE(string(f), v))
}

// LT returns a predicate that checks if the field is less than the given value.
func (f TimeField[P, T]) LT(v T) P {
	return P(FieldLT(string(f), v))
}

// LTE returns a predicate that checks if the field is less than or equal to the given value.
func (f TimeField[P, T]) LTE(v T) P {
	return P(FieldLTE(string(f), v))
}

// IsNull returns a predicate that checks if the field is NULL.
func (f TimeField[P, T]) IsNull() P {
	return P(FieldIsNull(string(f)))
}

// NotNull returns a predicate that checks if the field is not NULL.
func (f TimeField[P, T]) NotNull() P {
	return P(FieldNotNull(string(f)))
}

// IsNil is an alias for IsNull (for Ent compatibility).
func (f TimeField[P, T]) IsNil() P {
	return f.IsNull()
}

// NotNil is an alias for NotNull (for Ent compatibility).
func (f TimeField[P, T]) NotNil() P {
	return f.NotNull()
}

// EnumField is a generic enum field that provides type-safe predicate methods.
// T is the enum type (must be ~string).
type EnumField[P PredicateFunc, T ~string] string

// Name returns the field name.
func (f EnumField[P, T]) Name() string { return string(f) }

// EQ returns a predicate that checks if the field equals the given value.
func (f EnumField[P, T]) EQ(v T) P {
	return P(FieldEQ(string(f), v))
}

// NEQ returns a predicate that checks if the field does not equal the given value.
func (f EnumField[P, T]) NEQ(v T) P {
	return P(FieldNEQ(string(f), v))
}

// In returns a predicate that checks if the field value is in the given list.
func (f EnumField[P, T]) In(vs ...T) P {
	return P(FieldInGeneric(string(f), vs...))
}

// NotIn returns a predicate that checks if the field value is not in the given list.
func (f EnumField[P, T]) NotIn(vs ...T) P {
	return P(FieldNotInGeneric(string(f), vs...))
}

// IsNull returns a predicate that checks if the field is NULL.
func (f EnumField[P, T]) IsNull() P {
	return P(FieldIsNull(string(f)))
}

// NotNull returns a predicate that checks if the field is not NULL.
func (f EnumField[P, T]) NotNull() P {
	return P(FieldNotNull(string(f)))
}

// IsNil is an alias for IsNull (for Ent compatibility).
func (f EnumField[P, T]) IsNil() P {
	return f.IsNull()
}

// NotNil is an alias for NotNull (for Ent compatibility).
func (f EnumField[P, T]) NotNil() P {
	return f.NotNull()
}

// UUIDField is a generic UUID field that provides type-safe predicate methods.
// T is the UUID type.
type UUIDField[P PredicateFunc, T any] string

// Name returns the field name.
func (f UUIDField[P, T]) Name() string { return string(f) }

// EQ returns a predicate that checks if the field equals the given value.
func (f UUIDField[P, T]) EQ(v T) P {
	return P(FieldEQ(string(f), v))
}

// NEQ returns a predicate that checks if the field does not equal the given value.
func (f UUIDField[P, T]) NEQ(v T) P {
	return P(FieldNEQ(string(f), v))
}

// In returns a predicate that checks if the field value is in the given list.
func (f UUIDField[P, T]) In(vs ...T) P {
	return P(FieldInGeneric(string(f), vs...))
}

// NotIn returns a predicate that checks if the field value is not in the given list.
func (f UUIDField[P, T]) NotIn(vs ...T) P {
	return P(FieldNotInGeneric(string(f), vs...))
}

// GT returns a predicate that checks if the field is greater than the given value.
func (f UUIDField[P, T]) GT(v T) P {
	return P(FieldGT(string(f), v))
}

// GTE returns a predicate that checks if the field is greater than or equal to the given value.
func (f UUIDField[P, T]) GTE(v T) P {
	return P(FieldGTE(string(f), v))
}

// LT returns a predicate that checks if the field is less than the given value.
func (f UUIDField[P, T]) LT(v T) P {
	return P(FieldLT(string(f), v))
}

// LTE returns a predicate that checks if the field is less than or equal to the given value.
func (f UUIDField[P, T]) LTE(v T) P {
	return P(FieldLTE(string(f), v))
}

// IsNull returns a predicate that checks if the field is NULL.
func (f UUIDField[P, T]) IsNull() P {
	return P(FieldIsNull(string(f)))
}

// NotNull returns a predicate that checks if the field is not NULL.
func (f UUIDField[P, T]) NotNull() P {
	return P(FieldNotNull(string(f)))
}

// IsNil is an alias for IsNull (for Ent compatibility).
func (f UUIDField[P, T]) IsNil() P {
	return f.IsNull()
}

// NotNil is an alias for NotNull (for Ent compatibility).
func (f UUIDField[P, T]) NotNil() P {
	return f.NotNull()
}

// OtherField is a generic field for custom types that provides basic predicate methods.
// T is the field's Go type.
type OtherField[P PredicateFunc, T any] string

// Name returns the field name.
func (f OtherField[P, T]) Name() string { return string(f) }

// EQ returns a predicate that checks if the field equals the given value.
func (f OtherField[P, T]) EQ(v T) P {
	return P(FieldEQ(string(f), v))
}

// NEQ returns a predicate that checks if the field does not equal the given value.
func (f OtherField[P, T]) NEQ(v T) P {
	return P(FieldNEQ(string(f), v))
}

// In returns a predicate that checks if the field value is in the given list.
func (f OtherField[P, T]) In(vs ...T) P {
	return P(FieldInGeneric(string(f), vs...))
}

// NotIn returns a predicate that checks if the field value is not in the given list.
func (f OtherField[P, T]) NotIn(vs ...T) P {
	return P(FieldNotInGeneric(string(f), vs...))
}

// GT returns a predicate that checks if the field is greater than the given value.
func (f OtherField[P, T]) GT(v T) P {
	return P(FieldGT(string(f), v))
}

// GTE returns a predicate that checks if the field is greater than or equal to the given value.
func (f OtherField[P, T]) GTE(v T) P {
	return P(FieldGTE(string(f), v))
}

// LT returns a predicate that checks if the field is less than the given value.
func (f OtherField[P, T]) LT(v T) P {
	return P(FieldLT(string(f), v))
}

// LTE returns a predicate that checks if the field is less than or equal to the given value.
func (f OtherField[P, T]) LTE(v T) P {
	return P(FieldLTE(string(f), v))
}

// IsNull returns a predicate that checks if the field is NULL.
func (f OtherField[P, T]) IsNull() P {
	return P(FieldIsNull(string(f)))
}

// NotNull returns a predicate that checks if the field is not NULL.
func (f OtherField[P, T]) NotNull() P {
	return P(FieldNotNull(string(f)))
}

// IsNil is an alias for IsNull (for Ent compatibility).
func (f OtherField[P, T]) IsNil() P {
	return f.IsNull()
}

// NotNil is an alias for NotNull (for Ent compatibility).
func (f OtherField[P, T]) NotNil() P {
	return f.NotNull()
}

// FieldInGeneric is a generic version of FieldIn for use with generic types.
func FieldInGeneric[T any](name string, vs ...T) func(*Selector) {
	return func(s *Selector) {
		v := make([]any, len(vs))
		for i := range vs {
			v[i] = vs[i]
		}
		s.Where(In(s.C(name), v...))
	}
}

// FieldNotInGeneric is a generic version of FieldNotIn for use with generic types.
func FieldNotInGeneric[T any](name string, vs ...T) func(*Selector) {
	return func(s *Selector) {
		v := make([]any, len(vs))
		for i := range vs {
			v[i] = vs[i]
		}
		s.Where(NotIn(s.C(name), v...))
	}
}
