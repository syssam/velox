package runtime

import (
	"context"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/schema/field"
)

// testQuery is a minimal QueryReader and FieldCollectable used to exercise
// the runtime query helpers without a generated query package. Generated
// queries hold the same state as typed fields on their own struct.
type testQuery struct {
	Driver      dialect.Driver
	Table       string
	Columns     []string
	IDColumn    string
	FKColumns   []string
	IDFieldType field.Type
	Ctx         *QueryContext
	Path        func(context.Context) (*sql.Selector, error)
	Predicates  []func(*sql.Selector)
	Order       []func(*sql.Selector)
	Modifiers   []func(*sql.Selector)
	Edges       []EdgeLoad
	WithFKs     bool
}

var (
	_ QueryReader      = (*testQuery)(nil)
	_ FieldCollectable = (*testQuery)(nil)
)

func newTestQuery(drv dialect.Driver, table string, columns []string, idColumn string, fkColumns []string, typeName string) *testQuery {
	return &testQuery{
		Driver:    drv,
		Table:     table,
		Columns:   columns,
		IDColumn:  idColumn,
		FKColumns: fkColumns,
		Ctx:       &QueryContext{Type: typeName},
	}
}

func (q *testQuery) GetDriver() dialect.Driver                             { return q.Driver }
func (q *testQuery) GetTable() string                                      { return q.Table }
func (q *testQuery) GetColumns() []string                                  { return q.Columns }
func (q *testQuery) GetIDColumn() string                                   { return q.IDColumn }
func (q *testQuery) GetFKColumns() []string                                { return q.FKColumns }
func (q *testQuery) GetIDFieldType() field.Type                            { return q.IDFieldType }
func (q *testQuery) GetCtx() *QueryContext                                 { return q.Ctx }
func (q *testQuery) GetPath() func(context.Context) (*sql.Selector, error) { return q.Path }
func (q *testQuery) GetPredicates() []func(*sql.Selector)                  { return q.Predicates }
func (q *testQuery) GetOrder() []func(*sql.Selector)                       { return q.Order }
func (q *testQuery) GetModifiers() []func(*sql.Selector)                   { return q.Modifiers }
func (q *testQuery) GetWithFKs() bool                                      { return q.WithFKs }

func (q *testQuery) WithEdgeLoad(name string, opts ...LoadOption) {
	q.Edges = append(q.Edges, EdgeLoad{Name: name, Opts: opts})
	q.WithFKs = true
}

func (q *testQuery) Where(ps ...func(*sql.Selector)) { q.Predicates = append(q.Predicates, ps...) }
func (q *testQuery) SetLimit(n int)                  { q.Ctx.Limit = &n }
func (q *testQuery) SetOffset(n int)                 { q.Ctx.Offset = &n }
func (q *testQuery) SetUnique(v bool)                { q.Ctx.Unique = &v }

func (q *testQuery) AddOrder(o ...func(*sql.Selector)) {
	q.Order = append(q.Order, o...)
}

func (q *testQuery) AddModifier(m ...func(*sql.Selector)) {
	q.Modifiers = append(q.Modifiers, m...)
}

// BuildSelector matches the build-function signature ScanAll/ScanFirst take.
func (q *testQuery) BuildSelector(ctx context.Context) (*sql.Selector, error) {
	return BuildSelectorFrom(ctx, q)
}

// EdgeLoad records one WithEdgeLoad call on a testQuery.
type EdgeLoad struct {
	Name  string
	Label string
	Opts  []LoadOption
}
