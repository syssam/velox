package orm

// SPIKE (not committed): generic column types, as velox had them.

// SetterOf is an assignment of a mutation of the entity whose predicate type is E.
type SetterOf[E Predicate] S

// OrderOf is an order term of a query of the entity whose predicate type is E.
type OrderOf[E Predicate] O

//go:noinline
func values[T any](vs []T) []any {
	a := make([]any, len(vs))
	for i, v := range vs {
		a[i] = v
	}
	return a
}

// ValueColumn is a column.
type ValueColumn[T any, E Predicate] string

//go:noinline
func (c *ValueColumn[T, E]) IsNil() E  { return E(P{Op: OpIsNil, Col: string(*c)}) }
//go:noinline
func (c *ValueColumn[T, E]) NotNil() E { return E(P{Op: OpNotNil, Col: string(*c)}) }
//go:noinline
func (c *ValueColumn[T, E]) Set(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: v}
}
//go:noinline
func (c *ValueColumn[T, E]) SetNillable(v *T) SetterOf[E] {
	if v == nil {
		return SetterOf[E]{}
	}
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: *v}
}

// NullableValueColumn is a column.
type NullableValueColumn[T any, E Predicate] string

//go:noinline
func (c *NullableValueColumn[T, E]) IsNil() E  { return E(P{Op: OpIsNil, Col: string(*c)}) }
//go:noinline
func (c *NullableValueColumn[T, E]) NotNil() E { return E(P{Op: OpNotNil, Col: string(*c)}) }
//go:noinline
func (c *NullableValueColumn[T, E]) Set(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: v}
}
//go:noinline
func (c *NullableValueColumn[T, E]) SetNillable(v *T) SetterOf[E] {
	if v == nil {
		return SetterOf[E]{}
	}
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: *v}
}
//go:noinline
func (c *NullableValueColumn[T, E]) Clear() SetterOf[E] {
	return SetterOf[E]{Kind: SetNull, Col: string(*c)}
}

// BoolColumn is a column.
type BoolColumn[T any, E Predicate] string

//go:noinline
func (c *BoolColumn[T, E]) EQ(v T) E  { return E(P{Op: OpEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *BoolColumn[T, E]) NEQ(v T) E { return E(P{Op: OpNEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *BoolColumn[T, E]) IsNil() E  { return E(P{Op: OpIsNil, Col: string(*c)}) }
//go:noinline
func (c *BoolColumn[T, E]) NotNil() E { return E(P{Op: OpNotNil, Col: string(*c)}) }
//go:noinline
func (c *BoolColumn[T, E]) Set(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: v}
}
//go:noinline
func (c *BoolColumn[T, E]) SetNillable(v *T) SetterOf[E] {
	if v == nil {
		return SetterOf[E]{}
	}
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: *v}
}
//go:noinline
func (c *BoolColumn[T, E]) Asc() OrderOf[E]  { return OrderOf[E]{Col: string(*c)} }
//go:noinline
func (c *BoolColumn[T, E]) Desc() OrderOf[E] { return OrderOf[E]{Col: string(*c), Desc: true} }

// NullableBoolColumn is a column.
type NullableBoolColumn[T any, E Predicate] string

//go:noinline
func (c *NullableBoolColumn[T, E]) EQ(v T) E  { return E(P{Op: OpEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableBoolColumn[T, E]) NEQ(v T) E { return E(P{Op: OpNEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableBoolColumn[T, E]) IsNil() E  { return E(P{Op: OpIsNil, Col: string(*c)}) }
//go:noinline
func (c *NullableBoolColumn[T, E]) NotNil() E { return E(P{Op: OpNotNil, Col: string(*c)}) }
//go:noinline
func (c *NullableBoolColumn[T, E]) Set(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: v}
}
//go:noinline
func (c *NullableBoolColumn[T, E]) SetNillable(v *T) SetterOf[E] {
	if v == nil {
		return SetterOf[E]{}
	}
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: *v}
}
//go:noinline
func (c *NullableBoolColumn[T, E]) Clear() SetterOf[E] {
	return SetterOf[E]{Kind: SetNull, Col: string(*c)}
}
//go:noinline
func (c *NullableBoolColumn[T, E]) Asc() OrderOf[E]  { return OrderOf[E]{Col: string(*c)} }
//go:noinline
func (c *NullableBoolColumn[T, E]) Desc() OrderOf[E] { return OrderOf[E]{Col: string(*c), Desc: true} }

// EnumColumn is a column.
type EnumColumn[T any, E Predicate] string

//go:noinline
func (c *EnumColumn[T, E]) EQ(v T) E     { return E(P{Op: OpEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *EnumColumn[T, E]) NEQ(v T) E    { return E(P{Op: OpNEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *EnumColumn[T, E]) In(vs ...T) E { return E(P{Op: OpIn, Col: string(*c), Val: values(vs)}) }
//go:noinline
func (c *EnumColumn[T, E]) NotIn(vs ...T) E {
	return E(P{Op: OpNotIn, Col: string(*c), Val: values(vs)})
}
//go:noinline
func (c *EnumColumn[T, E]) IsNil() E  { return E(P{Op: OpIsNil, Col: string(*c)}) }
//go:noinline
func (c *EnumColumn[T, E]) NotNil() E { return E(P{Op: OpNotNil, Col: string(*c)}) }
//go:noinline
func (c *EnumColumn[T, E]) Set(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: v}
}
//go:noinline
func (c *EnumColumn[T, E]) SetNillable(v *T) SetterOf[E] {
	if v == nil {
		return SetterOf[E]{}
	}
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: *v}
}
//go:noinline
func (c *EnumColumn[T, E]) Asc() OrderOf[E]  { return OrderOf[E]{Col: string(*c)} }
//go:noinline
func (c *EnumColumn[T, E]) Desc() OrderOf[E] { return OrderOf[E]{Col: string(*c), Desc: true} }

// NullableEnumColumn is a column.
type NullableEnumColumn[T any, E Predicate] string

//go:noinline
func (c *NullableEnumColumn[T, E]) EQ(v T) E  { return E(P{Op: OpEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableEnumColumn[T, E]) NEQ(v T) E { return E(P{Op: OpNEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableEnumColumn[T, E]) In(vs ...T) E {
	return E(P{Op: OpIn, Col: string(*c), Val: values(vs)})
}
//go:noinline
func (c *NullableEnumColumn[T, E]) NotIn(vs ...T) E {
	return E(P{Op: OpNotIn, Col: string(*c), Val: values(vs)})
}
//go:noinline
func (c *NullableEnumColumn[T, E]) IsNil() E  { return E(P{Op: OpIsNil, Col: string(*c)}) }
//go:noinline
func (c *NullableEnumColumn[T, E]) NotNil() E { return E(P{Op: OpNotNil, Col: string(*c)}) }
//go:noinline
func (c *NullableEnumColumn[T, E]) Set(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: v}
}
//go:noinline
func (c *NullableEnumColumn[T, E]) SetNillable(v *T) SetterOf[E] {
	if v == nil {
		return SetterOf[E]{}
	}
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: *v}
}
//go:noinline
func (c *NullableEnumColumn[T, E]) Clear() SetterOf[E] {
	return SetterOf[E]{Kind: SetNull, Col: string(*c)}
}
//go:noinline
func (c *NullableEnumColumn[T, E]) Asc() OrderOf[E]  { return OrderOf[E]{Col: string(*c)} }
//go:noinline
func (c *NullableEnumColumn[T, E]) Desc() OrderOf[E] { return OrderOf[E]{Col: string(*c), Desc: true} }

// OrdColumn is a column.
type OrdColumn[T any, E Predicate] string

//go:noinline
func (c *OrdColumn[T, E]) EQ(v T) E     { return E(P{Op: OpEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *OrdColumn[T, E]) NEQ(v T) E    { return E(P{Op: OpNEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *OrdColumn[T, E]) In(vs ...T) E { return E(P{Op: OpIn, Col: string(*c), Val: values(vs)}) }
//go:noinline
func (c *OrdColumn[T, E]) NotIn(vs ...T) E {
	return E(P{Op: OpNotIn, Col: string(*c), Val: values(vs)})
}
//go:noinline
func (c *OrdColumn[T, E]) GT(v T) E  { return E(P{Op: OpGT, Col: string(*c), Val: v}) }
//go:noinline
func (c *OrdColumn[T, E]) GTE(v T) E { return E(P{Op: OpGTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *OrdColumn[T, E]) LT(v T) E  { return E(P{Op: OpLT, Col: string(*c), Val: v}) }
//go:noinline
func (c *OrdColumn[T, E]) LTE(v T) E { return E(P{Op: OpLTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *OrdColumn[T, E]) IsNil() E  { return E(P{Op: OpIsNil, Col: string(*c)}) }
//go:noinline
func (c *OrdColumn[T, E]) NotNil() E { return E(P{Op: OpNotNil, Col: string(*c)}) }
//go:noinline
func (c *OrdColumn[T, E]) Set(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: v}
}
//go:noinline
func (c *OrdColumn[T, E]) SetNillable(v *T) SetterOf[E] {
	if v == nil {
		return SetterOf[E]{}
	}
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: *v}
}
//go:noinline
func (c *OrdColumn[T, E]) Asc() OrderOf[E]  { return OrderOf[E]{Col: string(*c)} }
//go:noinline
func (c *OrdColumn[T, E]) Desc() OrderOf[E] { return OrderOf[E]{Col: string(*c), Desc: true} }

// NullableOrdColumn is a column.
type NullableOrdColumn[T any, E Predicate] string

//go:noinline
func (c *NullableOrdColumn[T, E]) EQ(v T) E  { return E(P{Op: OpEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableOrdColumn[T, E]) NEQ(v T) E { return E(P{Op: OpNEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableOrdColumn[T, E]) In(vs ...T) E {
	return E(P{Op: OpIn, Col: string(*c), Val: values(vs)})
}
//go:noinline
func (c *NullableOrdColumn[T, E]) NotIn(vs ...T) E {
	return E(P{Op: OpNotIn, Col: string(*c), Val: values(vs)})
}
//go:noinline
func (c *NullableOrdColumn[T, E]) GT(v T) E  { return E(P{Op: OpGT, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableOrdColumn[T, E]) GTE(v T) E { return E(P{Op: OpGTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableOrdColumn[T, E]) LT(v T) E  { return E(P{Op: OpLT, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableOrdColumn[T, E]) LTE(v T) E { return E(P{Op: OpLTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableOrdColumn[T, E]) IsNil() E  { return E(P{Op: OpIsNil, Col: string(*c)}) }
//go:noinline
func (c *NullableOrdColumn[T, E]) NotNil() E { return E(P{Op: OpNotNil, Col: string(*c)}) }
//go:noinline
func (c *NullableOrdColumn[T, E]) Set(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: v}
}
//go:noinline
func (c *NullableOrdColumn[T, E]) SetNillable(v *T) SetterOf[E] {
	if v == nil {
		return SetterOf[E]{}
	}
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: *v}
}
//go:noinline
func (c *NullableOrdColumn[T, E]) Clear() SetterOf[E] {
	return SetterOf[E]{Kind: SetNull, Col: string(*c)}
}
//go:noinline
func (c *NullableOrdColumn[T, E]) Asc() OrderOf[E]  { return OrderOf[E]{Col: string(*c)} }
//go:noinline
func (c *NullableOrdColumn[T, E]) Desc() OrderOf[E] { return OrderOf[E]{Col: string(*c), Desc: true} }

// StringColumn is a column.
type StringColumn[T any, E Predicate] string

//go:noinline
func (c *StringColumn[T, E]) EQ(v T) E     { return E(P{Op: OpEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *StringColumn[T, E]) NEQ(v T) E    { return E(P{Op: OpNEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *StringColumn[T, E]) In(vs ...T) E { return E(P{Op: OpIn, Col: string(*c), Val: values(vs)}) }
//go:noinline
func (c *StringColumn[T, E]) NotIn(vs ...T) E {
	return E(P{Op: OpNotIn, Col: string(*c), Val: values(vs)})
}
//go:noinline
func (c *StringColumn[T, E]) GT(v T) E  { return E(P{Op: OpGT, Col: string(*c), Val: v}) }
//go:noinline
func (c *StringColumn[T, E]) GTE(v T) E { return E(P{Op: OpGTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *StringColumn[T, E]) LT(v T) E  { return E(P{Op: OpLT, Col: string(*c), Val: v}) }
//go:noinline
func (c *StringColumn[T, E]) LTE(v T) E { return E(P{Op: OpLTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *StringColumn[T, E]) Contains(v string) E {
	return E(P{Op: OpContains, Col: string(*c), Val: v})
}
//go:noinline
func (c *StringColumn[T, E]) HasPrefix(v string) E {
	return E(P{Op: OpHasPrefix, Col: string(*c), Val: v})
}
//go:noinline
func (c *StringColumn[T, E]) HasSuffix(v string) E {
	return E(P{Op: OpHasSuffix, Col: string(*c), Val: v})
}
//go:noinline
func (c *StringColumn[T, E]) EqualFold(v string) E {
	return E(P{Op: OpEqualFold, Col: string(*c), Val: v})
}
//go:noinline
func (c *StringColumn[T, E]) ContainsFold(v string) E {
	return E(P{Op: OpContainsFold, Col: string(*c), Val: v})
}
//go:noinline
func (c *StringColumn[T, E]) IsNil() E  { return E(P{Op: OpIsNil, Col: string(*c)}) }
//go:noinline
func (c *StringColumn[T, E]) NotNil() E { return E(P{Op: OpNotNil, Col: string(*c)}) }
//go:noinline
func (c *StringColumn[T, E]) Set(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: v}
}
//go:noinline
func (c *StringColumn[T, E]) SetNillable(v *T) SetterOf[E] {
	if v == nil {
		return SetterOf[E]{}
	}
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: *v}
}
//go:noinline
func (c *StringColumn[T, E]) Asc() OrderOf[E]  { return OrderOf[E]{Col: string(*c)} }
//go:noinline
func (c *StringColumn[T, E]) Desc() OrderOf[E] { return OrderOf[E]{Col: string(*c), Desc: true} }

// NullableStringColumn is a column.
type NullableStringColumn[T any, E Predicate] string

//go:noinline
func (c *NullableStringColumn[T, E]) EQ(v T) E  { return E(P{Op: OpEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableStringColumn[T, E]) NEQ(v T) E { return E(P{Op: OpNEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableStringColumn[T, E]) In(vs ...T) E {
	return E(P{Op: OpIn, Col: string(*c), Val: values(vs)})
}
//go:noinline
func (c *NullableStringColumn[T, E]) NotIn(vs ...T) E {
	return E(P{Op: OpNotIn, Col: string(*c), Val: values(vs)})
}
//go:noinline
func (c *NullableStringColumn[T, E]) GT(v T) E  { return E(P{Op: OpGT, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableStringColumn[T, E]) GTE(v T) E { return E(P{Op: OpGTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableStringColumn[T, E]) LT(v T) E  { return E(P{Op: OpLT, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableStringColumn[T, E]) LTE(v T) E { return E(P{Op: OpLTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableStringColumn[T, E]) Contains(v string) E {
	return E(P{Op: OpContains, Col: string(*c), Val: v})
}
//go:noinline
func (c *NullableStringColumn[T, E]) HasPrefix(v string) E {
	return E(P{Op: OpHasPrefix, Col: string(*c), Val: v})
}
//go:noinline
func (c *NullableStringColumn[T, E]) HasSuffix(v string) E {
	return E(P{Op: OpHasSuffix, Col: string(*c), Val: v})
}
//go:noinline
func (c *NullableStringColumn[T, E]) EqualFold(v string) E {
	return E(P{Op: OpEqualFold, Col: string(*c), Val: v})
}
//go:noinline
func (c *NullableStringColumn[T, E]) ContainsFold(v string) E {
	return E(P{Op: OpContainsFold, Col: string(*c), Val: v})
}
//go:noinline
func (c *NullableStringColumn[T, E]) IsNil() E  { return E(P{Op: OpIsNil, Col: string(*c)}) }
//go:noinline
func (c *NullableStringColumn[T, E]) NotNil() E { return E(P{Op: OpNotNil, Col: string(*c)}) }
//go:noinline
func (c *NullableStringColumn[T, E]) Set(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: v}
}
//go:noinline
func (c *NullableStringColumn[T, E]) SetNillable(v *T) SetterOf[E] {
	if v == nil {
		return SetterOf[E]{}
	}
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: *v}
}
//go:noinline
func (c *NullableStringColumn[T, E]) Clear() SetterOf[E] {
	return SetterOf[E]{Kind: SetNull, Col: string(*c)}
}
//go:noinline
func (c *NullableStringColumn[T, E]) Asc() OrderOf[E] { return OrderOf[E]{Col: string(*c)} }
//go:noinline
func (c *NullableStringColumn[T, E]) Desc() OrderOf[E] {
	return OrderOf[E]{Col: string(*c), Desc: true}
}

// NumberColumn is a column.
type NumberColumn[T any, E Predicate] string

//go:noinline
func (c *NumberColumn[T, E]) EQ(v T) E     { return E(P{Op: OpEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *NumberColumn[T, E]) NEQ(v T) E    { return E(P{Op: OpNEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *NumberColumn[T, E]) In(vs ...T) E { return E(P{Op: OpIn, Col: string(*c), Val: values(vs)}) }
//go:noinline
func (c *NumberColumn[T, E]) NotIn(vs ...T) E {
	return E(P{Op: OpNotIn, Col: string(*c), Val: values(vs)})
}
//go:noinline
func (c *NumberColumn[T, E]) GT(v T) E  { return E(P{Op: OpGT, Col: string(*c), Val: v}) }
//go:noinline
func (c *NumberColumn[T, E]) GTE(v T) E { return E(P{Op: OpGTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *NumberColumn[T, E]) LT(v T) E  { return E(P{Op: OpLT, Col: string(*c), Val: v}) }
//go:noinline
func (c *NumberColumn[T, E]) LTE(v T) E { return E(P{Op: OpLTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *NumberColumn[T, E]) IsNil() E  { return E(P{Op: OpIsNil, Col: string(*c)}) }
//go:noinline
func (c *NumberColumn[T, E]) NotNil() E { return E(P{Op: OpNotNil, Col: string(*c)}) }
//go:noinline
func (c *NumberColumn[T, E]) Set(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: v}
}
//go:noinline
func (c *NumberColumn[T, E]) SetNillable(v *T) SetterOf[E] {
	if v == nil {
		return SetterOf[E]{}
	}
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: *v}
}
//go:noinline
func (c *NumberColumn[T, E]) Add(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetAdd, Col: string(*c), Val: v}
}
//go:noinline
func (c *NumberColumn[T, E]) Asc() OrderOf[E]  { return OrderOf[E]{Col: string(*c)} }
//go:noinline
func (c *NumberColumn[T, E]) Desc() OrderOf[E] { return OrderOf[E]{Col: string(*c), Desc: true} }

// NullableNumberColumn is a column.
type NullableNumberColumn[T any, E Predicate] string

//go:noinline
func (c *NullableNumberColumn[T, E]) EQ(v T) E  { return E(P{Op: OpEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableNumberColumn[T, E]) NEQ(v T) E { return E(P{Op: OpNEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableNumberColumn[T, E]) In(vs ...T) E {
	return E(P{Op: OpIn, Col: string(*c), Val: values(vs)})
}
//go:noinline
func (c *NullableNumberColumn[T, E]) NotIn(vs ...T) E {
	return E(P{Op: OpNotIn, Col: string(*c), Val: values(vs)})
}
//go:noinline
func (c *NullableNumberColumn[T, E]) GT(v T) E  { return E(P{Op: OpGT, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableNumberColumn[T, E]) GTE(v T) E { return E(P{Op: OpGTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableNumberColumn[T, E]) LT(v T) E  { return E(P{Op: OpLT, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableNumberColumn[T, E]) LTE(v T) E { return E(P{Op: OpLTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *NullableNumberColumn[T, E]) IsNil() E  { return E(P{Op: OpIsNil, Col: string(*c)}) }
//go:noinline
func (c *NullableNumberColumn[T, E]) NotNil() E { return E(P{Op: OpNotNil, Col: string(*c)}) }
//go:noinline
func (c *NullableNumberColumn[T, E]) Set(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: v}
}
//go:noinline
func (c *NullableNumberColumn[T, E]) SetNillable(v *T) SetterOf[E] {
	if v == nil {
		return SetterOf[E]{}
	}
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: *v}
}
//go:noinline
func (c *NullableNumberColumn[T, E]) Clear() SetterOf[E] {
	return SetterOf[E]{Kind: SetNull, Col: string(*c)}
}
//go:noinline
func (c *NullableNumberColumn[T, E]) Add(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetAdd, Col: string(*c), Val: v}
}
//go:noinline
func (c *NullableNumberColumn[T, E]) Asc() OrderOf[E] { return OrderOf[E]{Col: string(*c)} }
//go:noinline
func (c *NullableNumberColumn[T, E]) Desc() OrderOf[E] {
	return OrderOf[E]{Col: string(*c), Desc: true}
}

// AutoColumn is a column.
type AutoColumn[T any, E Predicate] string

//go:noinline
func (c *AutoColumn[T, E]) EQ(v T) E     { return E(P{Op: OpEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *AutoColumn[T, E]) NEQ(v T) E    { return E(P{Op: OpNEQ, Col: string(*c), Val: v}) }
//go:noinline
func (c *AutoColumn[T, E]) In(vs ...T) E { return E(P{Op: OpIn, Col: string(*c), Val: values(vs)}) }
//go:noinline
func (c *AutoColumn[T, E]) NotIn(vs ...T) E {
	return E(P{Op: OpNotIn, Col: string(*c), Val: values(vs)})
}
//go:noinline
func (c *AutoColumn[T, E]) GT(v T) E  { return E(P{Op: OpGT, Col: string(*c), Val: v}) }
//go:noinline
func (c *AutoColumn[T, E]) GTE(v T) E { return E(P{Op: OpGTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *AutoColumn[T, E]) LT(v T) E  { return E(P{Op: OpLT, Col: string(*c), Val: v}) }
//go:noinline
func (c *AutoColumn[T, E]) LTE(v T) E { return E(P{Op: OpLTE, Col: string(*c), Val: v}) }
//go:noinline
func (c *AutoColumn[T, E]) IsNil() E  { return E(P{Op: OpIsNil, Col: string(*c)}) }
//go:noinline
func (c *AutoColumn[T, E]) NotNil() E { return E(P{Op: OpNotNil, Col: string(*c)}) }
//go:noinline
func (c *AutoColumn[T, E]) Set(v T) SetterOf[E] {
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: v}
}
//go:noinline
func (c *AutoColumn[T, E]) SetNillable(v *T) SetterOf[E] {
	if v == nil {
		return SetterOf[E]{}
	}
	return SetterOf[E]{Kind: SetValue, Col: string(*c), Val: *v}
}
//go:noinline
func (c *AutoColumn[T, E]) Keep() SetterOf[E] { return SetterOf[E]{Kind: SetKeep, Col: string(*c)} }
//go:noinline
func (c *AutoColumn[T, E]) Asc() OrderOf[E]   { return OrderOf[E]{Col: string(*c)} }
//go:noinline
func (c *AutoColumn[T, E]) Desc() OrderOf[E]  { return OrderOf[E]{Col: string(*c), Desc: true} }
