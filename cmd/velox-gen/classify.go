package main

import "go/types"

// classify picks a column's handle type from its field's type (design §2): a
// pointer takes the Null form of its element's handle, whose value type is
// the element.
func classify(t types.Type, cfg config) (handle string, value types.Type) {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		h, v := level(p.Elem(), cfg)
		return "Null" + h, v
	}
	return level(t, cfg)
}

// level is the handle of a type that is not a pointer: OrderedCol for
// time.Time and types registered as ordered; NumberCol for decimal.Decimal,
// types registered as numbers, and integers and floats; StringCol for
// strings; Col for everything else. Named types go by their underlying type.
func level(t types.Type, cfg config) (string, types.Type) {
	if n, ok := types.Unalias(t).(*types.Named); ok {
		switch id := typeID(n); {
		case id == "time.Time" || cfg.ordered[id]:
			return "OrderedCol", t
		case id == "github.com/shopspring/decimal.Decimal" || cfg.number[id]:
			return "NumberCol", t
		}
	}
	if b, ok := t.Underlying().(*types.Basic); ok {
		switch {
		case b.Info()&types.IsString != 0:
			return "StringCol", t
		case b.Info()&(types.IsInteger|types.IsFloat) != 0:
			return "NumberCol", t
		}
	}
	return "Col", t
}

// typeID names a named type as the -ordered and -number flags do:
// "import/path.Name".
func typeID(n *types.Named) string {
	o := n.Origin().Obj()
	if o.Pkg() == nil {
		return o.Name()
	}
	return o.Pkg().Path() + "." + o.Name()
}
