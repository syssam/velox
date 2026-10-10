// Package notes holds what vxmigrate moves with a warning, or leaves to a
// person: every line that ends in a "note:" comment must be reported with
// that text, and nothing else may be. It is written against velox's
// generated code and is not built after the migration; its test looks at
// the report and at the shape of what was rewritten.
package notes

import (
	"context"
	"errors"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/tests/integration"
	userclient "github.com/syssam/velox/tests/integration/client/user"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/filter"
	"github.com/syssam/velox/tests/integration/hook"
	"github.com/syssam/velox/tests/integration/intercept"
	"github.com/syssam/velox/tests/integration/predicate"
	"github.com/syssam/velox/tests/integration/query"
	"github.com/syssam/velox/tests/integration/user"
)

// An interceptor that tells the entity by the query's type: the query is
// *orm.Query for every entity now.
func scope() velox.Interceptor {
	return velox.TraverseFunc(func(ctx context.Context, q velox.Query) error {
		if uq, ok := q.(*query.UserQuery); ok { // note: asserted to the query type of User
			uq.Where(user.AgeField.GT(1))
		}
		return nil
	})
}

// The adapter of the generated package intercept, called.
func capped() velox.Interceptor {
	return intercept.TraverseFunc(func(ctx context.Context, q intercept.Query) error {
		q.Limit(100)
		return nil
	})
}

// A hook that tells the entity by the mutation's type: every entity's is
// *orm.Mutation now.
func stamp() velox.Hook {
	return func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			if um, ok := m.(*userclient.UserMutation); ok { // note: asserted to the mutation type of User
				_ = um
			}
			return next.Mutate(ctx, m)
		})
	}
}

// The tests of an error: velox's would compile and never hold.
func classify(err error) string {
	var missing *velox.NotFoundError // note: orm.ErrNotFound
	switch {
	case errors.As(err, &missing):
		return "missing"
	case integration.IsNotSingular(err), velox.IsNotFound(err), velox.IsValidationError(err):
		return "known"
	}
	return ""
}

// The field of a validation error that the runtime names otherwise.
func fieldOf(err error) string {
	var ve *velox.ValidationError // note: ValidationError is the runtime's type now
	if errors.As(err, &ve) {
		return ve.Name
	}
	return ""
}

// A validation error written out, with the name under both of velox's keys.
func refused() error {
	return &velox.ValidationError{Entity: "User", Field: "name", Name: "name", Err: errors.New("x")} // note: ValidationError is the runtime's type now
}

// The driver under a handle, and the handle a context carries.
func lock(ctx context.Context, tx *integration.Tx, key int64) error {
	return tx.Client().RuntimeConfig().Driver.Exec(ctx, "SELECT 1", []any{key}, nil)
}

func inContext(ctx context.Context, c *integration.Client, tx *integration.Tx) bool {
	ctx = integration.NewContext(ctx, c)
	ctx = integration.NewTxContext(ctx, tx)
	return integration.TxFromContext(ctx) != nil && integration.FromContext(ctx) != nil // note: FromContext is orm.ExecutorFrom
}

// Order terms held in a variable: one, and several.
func newest(ctx context.Context, c *integration.Client) (*entity.User, error) {
	order := integration.Desc(user.FieldCreatedAt, user.FieldName)
	return c.User.Query().Order(order).First(ctx)
}

func youngest(ctx context.Context, c *integration.Client) (*entity.User, error) {
	byAge := integration.Asc(user.FieldAge)
	return c.User.Query().Order(byAge).First(ctx)
}

// An order written as a function, and by a column named at run time.
func nullsLast(ctx context.Context, c *integration.Client, column string) ([]*entity.User, error) {
	return c.User.Query().Order(func(s *sql.Selector) { s.OrderBy(s.C(user.FieldNickname) + " NULLS LAST") }, integration.Desc(column)).All(ctx)
}

func inOrderOf(ctx context.Context, c *integration.Client, fs []func(*sql.Selector)) ([]*entity.User, error) {
	return c.User.Query().Order(fs...).All(ctx)
}

// A predicate where a function on a selector is wanted.
func asFunction(p predicate.User) func(*sql.Selector) { return p }

// The conditions an application adds to a where input, and its Filter.
func scoped(where *filter.UserWhereInput) (predicate.User, error) {
	where.AddPredicates(user.AgeField.GT(1))
	return where.Filter() // note: P of an input with no condition was an error
}

// A client taken only to query on it takes a transaction as well.
func countUsers(ctx context.Context, c *integration.Client) (int, error) {
	if c == nil {
		return 0, nil
	}
	return c.User.Query().Count(ctx)
}

func countIn(ctx context.Context, tx *integration.Tx) (int, error) {
	return countUsers(ctx, tx.Client())
}

// One that begins a transaction on it needs the database.
func begins(ctx context.Context, c *integration.Client) error {
	tx, err := c.Tx(ctx)
	if err != nil {
		return err
	}
	return tx.Rollback()
}

// An option of an eager load kept in a variable.
func loadWith(ctx context.Context, c *integration.Client, opt func(entity.PostQuerier)) ([]*entity.User, error) {
	return c.User.Query().WithPosts(opt).All(ctx)
}

// An interface of the application's that names a method of a query.
type exister interface {
	Exist(ctx context.Context) (bool, error)
}

func opened(dsn string) (*integration.Client, error) { return integration.Open("sqlite", dsn) }

// A column of the library's generic type that is asked whether it is NULL.
type scopeColumns[P sql.PredicateFunc] struct {
	Owner sql.IntField[P]
}

func unowned[P sql.PredicateFunc](c scopeColumns[P]) P { return c.Owner.IsNil() }

// The type of the fields a list is ordered by, and the tables.
func orderField() *entity.PostOrderField { return new(entity.PostOrderField) }

func tables(ctx context.Context, c *integration.Client) error { return c.Schema.Create(ctx) }

// The default of an enum field, by name.
func defaultRole() user.Role { return user.DefaultRole }

// The start of a query as a value.
func starter(c *integration.Client) func() entity.UserQuerier { return c.User.Query }

// Only some columns of the neighbors of an eager load.
func authorNames(ctx context.Context, c *integration.Client) ([]*entity.Post, error) {
	return c.Post.Query().WithAuthor(func(q entity.UserQuerier) { q.Select(user.FieldName) }).All(ctx)
}

// The library's combinator of conditions that are not predicate types.
func plain(fs ...func(*sql.Selector)) func(*sql.Selector) { return sql.AndPredicates(fs...) }

// A deferred call that comes to be the argument of another runs later still.
func cleanup(ctx context.Context, c *integration.Client) {
	defer c.User.Update().SetAge(0).ExecX(ctx) // note: a deferred (or go) call is inside a function now
	go c.User.Create().SetName("x").SaveX(ctx) // note: a deferred (or go) call is inside a function now
}

// The handle an entity was read on is named only where the text says which.
func reassigned(ctx context.Context, c *integration.Client, tx *integration.Tx, inTx bool) (int, error) {
	u, err := c.User.Query().First(ctx)
	if inTx {
		u, err = tx.User.Query().First(ctx)
	}
	if err != nil {
		return 0, err
	}
	return u.QueryPosts().Count(ctx) // note: QueryPosts is given the handle of the context
}

func unwrapped(ctx context.Context, tx *integration.Tx) (int, error) {
	u, err := tx.User.Query().First(ctx)
	if err != nil {
		return 0, err
	}
	v := u.Unwrap()
	return v.QueryPosts().Count(ctx) // note: QueryPosts is given the handle of the context
}

func handleReused(ctx context.Context, a, b *integration.Client) (int, error) {
	c := a
	u, err := c.User.Query().First(ctx)
	if err != nil {
		return 0, err
	}
	c = b
	_ = c
	return u.QueryPosts().Count(ctx) // note: QueryPosts is given the handle of the context
}

// An assignment after the use does not say what the entity was read on.
func later(ctx context.Context, c *integration.Client, u *entity.User) (int, error) {
	n, err := u.QueryPosts().Count(ctx) // note: QueryPosts is given the handle of the context
	if err != nil {
		return 0, err
	}
	u, err = c.User.Query().First(ctx)
	_ = u
	return n, err
}

func around(ctx context.Context, cs []*integration.Client) (int, error) {
	total := 0
	var prev *entity.User
	for _, c := range cs {
		if prev != nil {
			n, err := prev.QueryPosts().Count(ctx) // note: QueryPosts is given the handle of the context
			if err != nil {
				return 0, err
			}
			total += n
		}
		var err error
		prev, err = c.User.Query().First(ctx)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

// A handle that is another one each time round, and an entity kept outside.
func firstOnly(ctx context.Context, clients []*integration.Client) (int, error) {
	total := 0
	var u *entity.User
	for i, c := range clients {
		if i == 0 {
			u, _ = c.User.Query().First(ctx)
		}
		n, err := u.QueryPosts().Count(ctx) // note: QueryPosts is given the handle of the context
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// What is kept on the receiver is not a change of the handle, nor is a
// field of the entity a change of what it was read on.
type cache struct {
	c    *integration.Client
	last *entity.User
}

func (s *cache) posts(ctx context.Context) (int, error) {
	u, err := s.c.User.Query().First(ctx)
	if err != nil {
		return 0, err
	}
	s.last = u
	u.Name = "seen"
	return u.QueryPosts().Count(ctx)
}

// A handle that is a parameter, given another value.
func handleParam(ctx context.Context, c, other *integration.Client) (int, error) {
	u, err := c.User.Query().First(ctx)
	if err != nil {
		return 0, err
	}
	c = other
	_ = c
	return u.QueryPosts().Count(ctx) // note: QueryPosts is given the handle of the context
}

// An element written after the slice was read.
func element(ctx context.Context, c, other *integration.Client) (int, error) {
	us, err := c.User.Query().All(ctx)
	if err != nil || len(us) == 0 {
		return 0, err
	}
	us[0], err = other.User.Query().First(ctx)
	if err != nil {
		return 0, err
	}
	return us[0].QueryPosts().Count(ctx) // note: QueryPosts is given the handle of the context
}

// Unwrap changes the entity it is called on.
func unwrappedInPlace(ctx context.Context, tx *integration.Tx) (int, error) {
	u, err := tx.User.Query().First(ctx)
	if err != nil {
		return 0, err
	}
	u.Unwrap()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return u.QueryPosts().Count(ctx) // note: QueryPosts is given the handle of the context
}

// The values of velox's errors, next to the functions.
func missing(ctx context.Context, c *integration.Client) bool {
	_, err := c.User.Get(ctx, 9999)
	return errors.Is(err, velox.ErrNotFound) || errors.Is(err, velox.ErrNotSingular)
}

// Of several orders velox used the last.
func twoOrders(ctx context.Context, c *integration.Client, a, b *entity.PostOrder) (*entity.PostConnection, error) {
	return c.Post.Query().Paginate(ctx, nil, nil, nil, nil, entity.WithPostOrder(a), entity.WithPostOrder(b)) // note: more than one order
}

func defaultOrder(ctx context.Context, c *integration.Client) (*entity.PostConnection, error) {
	return c.Post.Query().Paginate(ctx, nil, nil, nil, nil, entity.WithPostOrder(nil))
}

// The last order is no order: the default, whatever came before.
func noOrder(ctx context.Context, c *integration.Client, a *entity.PostOrder) (*entity.PostConnection, error) {
	return c.Post.Query().Paginate(ctx, nil, nil, nil, nil, entity.WithPostOrder(a), entity.WithPostOrder(nil)) // note: more than one order
}

// An order written out where it is given.
func literalOrder(ctx context.Context, c *integration.Client, f *entity.PostOrderField) (*entity.PostConnection, error) {
	return c.Post.Query().Paginate(ctx, nil, nil, nil, nil, entity.WithPostOrder(&entity.PostOrder{Field: f}))
}

func none(ps ...predicate.User) predicate.User {
	return user.Not(ps...) // note: Not of a list that may be empty
}

func either() func(...predicate.User) predicate.User {
	return user.Or // note: Or as a value
}

// No order, kept as an option.
func keptDefault() entity.PostPaginateOption { return entity.WithPostOrder(nil) }

// Unwrap put off.
func later2(u *entity.User) {
	defer u.Unwrap()
}

// Or and Not of nothing.
func anyOf(ps ...predicate.User) predicate.User {
	return user.Or(ps...) // note: Or of a list that may be empty
}

func noneOf(ps ...predicate.User) predicate.User {
	return sql.NotPredicates(ps...) // note: Not of a list that may be empty
}

// An input with no condition.
func matching(ctx context.Context, c *integration.Client, where *filter.PostWhereInput) (int, error) {
	p, err := where.P()                                // note: P of an input with no condition was an error
	if errors.Is(err, filter.ErrEmptyPostWhereInput) { // note: an input with no condition is not an error now
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return c.Post.Query().Where(p).Count(ctx)
}

// The two results of P, returned as they are.
func asPredicate(where *filter.PostWhereInput) (predicate.Post, error) {
	return where.P() // note: P of an input with no condition was an error
}

// A limit in the option of an eager load.
func threeEach(ctx context.Context, c *integration.Client) ([]*entity.User, error) {
	return c.User.Query().WithPosts(func(q entity.PostQuerier) { q.Limit(3) }).All(ctx) // note: Limit in the option of an eager load
}

// The method a GraphQL engine resolves an edge with.
func resolved(ctx context.Context, u *entity.User) (*entity.PostConnection, error) {
	return u.Posts(ctx, nil, nil, nil, nil, nil, nil) // note: User.Posts reads the edge on the handle of the context
}

// A method value held its receiver.
func lateBuilder(c *integration.Client, names []string) {
	set := c.User.Create().SetName // note: the builder of SetName was taken when the method value was made
	for _, n := range names {
		set(n)
	}
}

// A method value of a parameter that is given another value afterwards.
func lateParam(b, other *userclient.UserUpdate) {
	set := b.SetName // note: the builder of SetName was taken when the method value was made
	b = other
	_ = b
	set("x")
}

// A builder in a variable called like the parameter the tool writes.
func shadow(c *integration.Client) func(string) *userclient.UserUpdate {
	v := c.User.Update()
	return v.SetName
}

// The first argument is itself rewritten.
func nested(ctx context.Context, c *integration.Client) ([]*entity.User, error) {
	return c.User.CreateBulk(c.User.Create().SetName("a").SetEmail("a@x"), c.User.Create().SetName("b").SetEmail("b@x")).Save(ctx)
}

func takesClient(*integration.Client) {}

func handOn(tx *integration.Tx) {
	takesClient(tx.Client()) // note: tx.Client() is the transaction itself now
}

// Predicates where a value goes that is not an argument.
type rule struct {
	name string
	cond predicate.User
}

func rules() []rule {
	return []rule{{name: "raw", cond: func(s *sql.Selector) { s.Where(sql.EQ("name", "x")) }}}
}

func pointed() []*rule {
	return []*rule{{name: "raw", cond: func(s *sql.Selector) { s.Where(sql.EQ("name", "y")) }}, {name: "none", cond: nil}}
}

// The mutation type of one entity, under another name.
type userMutation = userclient.UserMutation

func aliased(m velox.Mutation) bool {
	_, ok := m.(*userMutation) // note: asserted to the mutation type of User
	return ok
}

func byName() map[string]predicate.User {
	return map[string]predicate.User{"none": nil}
}

func applyTo(p predicate.User, s *sql.Selector) {
	p(s) // note: a predicate is called as a function
}

func isSet(p predicate.User) bool {
	switch p { // note: a switch on a predicate
	case nil:
		return false
	}
	return true
}

// An entity given by its address.
func adopt(ctx context.Context, c *integration.Client, author entity.User, title string) (*entity.Post, error) {
	return c.Post.Create().SetTitle(title).SetAuthor(&author).Save(ctx)
}

func from(ctx context.Context, c *integration.Client) (int, error) {
	return userclient.From(c).Query().Count(ctx) // note: From of a package the new tree does not have
}

// A column's name inside a call no rule rewrites.
func byNameAsc(s *sql.Selector) { s.OrderBy(sql.Asc(user.FieldName)) }

func onCreate(h velox.Hook) velox.Hook {
	return hook.On(h, velox.OpCreate) // note: the package hook of the generated tree has no counterpart
}

// The edge Likes next to the edge LikedPosts.
func like(ctx context.Context, c *integration.Client, userID int, likeIDs ...int) error {
	return c.User.UpdateOneID(userID).AddLikeIDs(likeIDs...).Exec(ctx)
}

func valid(r user.Role) bool { return r.IsValid() }

var _ = []any{scope, capped, stamp, classify, fieldOf, refused, lock, inContext, newest, youngest, starter, authorNames, plain,
	nullsLast, inOrderOf, asFunction, scoped, countIn, begins, exister(nil), opened, firstOnly, (*cache).posts, keptDefault, later2, unowned[predicate.User], orderField, tables, defaultRole, later, around, handleParam, element, unwrappedInPlace, missing, noOrder, literalOrder,
	none, either, asPredicate, lateParam, pointed, aliased, cleanup, reassigned, unwrapped, handleReused, twoOrders, defaultOrder, anyOf, noneOf, matching,
	threeEach, resolved, lateBuilder, shadow, nested, handOn, rules, byName, applyTo, isSet, adopt, from, byNameAsc, onCreate, like, valid}
