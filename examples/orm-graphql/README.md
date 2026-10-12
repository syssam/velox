# orm-graphql

The GraphQL layer `velox-gen -graphql` generates beside the ORM, on two
entities: a user and the todos they own. [`orm-basic`](../orm-basic) is the
ORM alone.

```bash
go run generate.go   # writes ./velox and ./graphql from ./schema
go test ./...        # walkthrough_test.go
```

No GraphQL engine is imported, here or by the generated code: the inputs are
plain Go types, and an engine binds to them through the `@goModel` directives
of the generated schema. `./velox` and `./graphql` are checked in.

## What `-graphql` adds

```
graphql/velox*.graphql        the schema: types, enums, where and order inputs, connections
velox/filter/                 TodoWhereInput, UserWhereInput
velox/entity/todo_gql.go      TodoOrder, IsNode, and a method per edge for an engine to bind
velox/todo/gql.go             OrderField and its constants
velox/node_gql.go             NodeTypes, for node(id:)
```

Nothing else in `./velox` differs from a tree generated without `-graphql`:
the layer is an extension of the generator (`contrib/graphql/ormgql`, see
`generate.go`), and writes files of its own.

At run time this code calls
[`contrib/graphql/gqlinput`](../../contrib/graphql/gqlinput), which is built
on package `orm`. Neither `orm` nor the root package knows GraphQL.

## A resolver

```go
// todos(after, first, before, last, orderBy: TodoOrder, where: TodoWhereInput): TodoConnection!
func Todos(ctx context.Context, ex velox.Executor, after *velox.Cursor, first *int, before *velox.Cursor, last *int,
    orderBy *entity.TodoOrder, where *filter.TodoWhereInput) (*entity.TodoConnection, error) {
    return velox.Todo.Query(ex).
        Where(where.P()).
        OrderBy(orderBy.Term()).
        Paginate(ctx, after, first, before, last)
}
```

`Paginate`, `velox.Cursor` and the connection type are the ORM's and exist
without `-graphql`. What the GraphQL layer adds is the two inputs.

## A where input is a struct and a table

```go
type TodoWhereInput struct {
    Not *TodoWhereInput; Or, And []*TodoWhereInput
    Title *string; TitleNEQ *string; TitleIn []string; ...
    HasOwnerWith []*UserWhereInput
}

// what each field means, in the order of the struct
var todoWhere = []gqlinput.WhereField{
    {Col: "title", Op: orm.OpEQ},   // title
    {Col: "title", Op: orm.OpNEQ},  // titleNEQ
    ...
}

func (i *TodoWhereInput) P() predicate.Todo { return predicate.Todo(gqlinput.Where(i, todoWhere)) }
```

The logic that turns it into a condition is written once, in `gqlinput`.
With `velox` (see [`examples/fullgql`](../fullgql)) it is generated per
entity: a function with a statement per field and operator.

The same table reads and writes the input as a document, so a filter can be
kept and applied later:

```go
var where filter.TodoWhereInput
err := json.Unmarshal([]byte(`{"status":"TODO","priorityLT":5}`), &where)  // a key it has no field for is an error
out, err := json.Marshal(where)                                            // the same document
```
