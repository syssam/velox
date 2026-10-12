package app

import (
	"context"
	"errors"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/tests/integration"
	postclient "github.com/syssam/velox/tests/integration/client/post"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/filter"
	"github.com/syssam/velox/tests/integration/predicate"
	"github.com/syssam/velox/tests/integration/user"
)

// What reviewers of the migration found by writing small programs and
// running them before and after: code that compiled afterwards and did
// something else. Each is kept here with a test, so that "afterwards" is
// asserted for good and not only once.

// Classify names an error by the tests of the generated package.
func Classify(err error) string {
	switch {
	case err == nil:
		return "ok"
	case integration.IsNotFound(err):
		return "not found"
	case integration.IsNotSingular(err):
		return "not singular"
	case integration.IsValidationError(err):
		return "validation"
	case integration.IsConstraintError(err):
		return "constraint"
	}
	return "other"
}

// ClassifyByLibrary does the same with the library's names, the values of
// its errors among them.
func ClassifyByLibrary(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, velox.ErrNotFound):
		return "not found"
	case errors.Is(err, velox.ErrNotSingular):
		return "not singular"
	case velox.IsValidationError(err):
		return "validation"
	case velox.IsConstraintError(err):
		return "constraint"
	}
	return "other"
}

// Missing is the error a stand-in for the store answers with: one the
// code under test takes for a missing row.
func Missing(what string) error { return velox.NewNotFoundError(what) }

// Described counts the fields of a schema: velox.Field is a name of the
// library that no generated package repeats.
func Described(fields []velox.Field) int { return len(fields) }

// Scope is the condition a caller's access adds to a list: none for one who
// sees every row.
func Scope(seesAll bool, name string) predicate.User {
	if seesAll {
		return nil
	}
	return user.NameField.EQ(name)
}

// FieldOf names the field a validation error is about.
func FieldOf(err error) string {
	var ve *velox.ValidationError
	if errors.As(err, &ve) {
		return ve.Name
	}
	return ""
}

// TheOnly returns the one user, which is an error when there are several.
func (s *Store) TheOnly(ctx context.Context) (*entity.User, error) {
	return s.User.Query().Only(ctx)
}

// BumpLater adds a year when it returns, and returns the age before that.
func (s *Store) BumpLater(ctx context.Context, id int) int {
	defer s.User.UpdateOneID(id).AddAge(1).ExecX(ctx)
	return s.User.GetX(ctx, id).Age
}

// CountOps counts the mutations by what they are.
func (s *Store) CountOps() (one, many *int) {
	one, many = new(int), new(int)
	s.Use(func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			switch {
			case m.Op().Is(velox.OpUpdateOne):
				*one++
			case m.Op().Is(velox.OpUpdate):
				*many++
			}
			return next.Mutate(ctx, m)
		})
	})
	return one, many
}

// AddPostsLate makes the builders, then the bulk, and only then says whose
// the posts are.
func (s *Store) AddPostsLate(ctx context.Context, author *entity.User, titles ...string) ([]*entity.Post, error) {
	bs := make([]*postclient.PostCreate, len(titles))
	for i, title := range titles {
		bs[i] = s.Post.Create().SetTitle(title)
	}
	bulk := s.Post.CreateBulk(bs...)
	for _, b := range bs {
		b.SetAuthor(author)
	}
	return bulk.Save(ctx)
}

// AddPostsInBatches inserts the posts size at a time.
func (s *Store) AddPostsInBatches(ctx context.Context, author *entity.User, size int, titles ...string) ([]*entity.Post, error) {
	bs := make([]*postclient.PostCreate, len(titles))
	for i, title := range titles {
		bs[i] = s.Post.Create().SetTitle(title).SetAuthor(author)
	}
	return s.Post.CreateBulk(bs...).BatchSize(size).Save(ctx)
}

// TooDeep reports whether an error is that of a filter nested too deep, and
// how deep one may be.
func TooDeep(err error) (bool, int) {
	return errors.Is(err, filter.ErrFilterDepthExceeded), filter.DefaultMaxFilterDepth
}

// Adopt is given the author by value.
func (s *Store) Adopt(ctx context.Context, author entity.User, title string) (*entity.Post, error) {
	return s.Post.Create().SetTitle(title).SetAuthor(&author).Save(ctx)
}

// TitlesByLast pages with two orders: the last one is the order.
func (s *Store) TitlesByLast(ctx context.Context, a, b *entity.PostOrder) ([]string, error) {
	conn, err := s.Post.Query().Paginate(ctx, nil, nil, nil, nil, entity.WithPostOrder(a), entity.WithPostOrder(b))
	if err != nil {
		return nil, err
	}
	var titles []string
	for _, e := range conn.Edges {
		titles = append(titles, e.Node.Title)
	}
	return titles, nil
}

// AuthorsNamed loads each post's author with the name only.
func (s *Store) AuthorsNamed(ctx context.Context) ([]*entity.Post, error) {
	return s.Post.Query().WithAuthor(func(q entity.UserQuerier) { q.Select(user.FieldName) }).All(ctx)
}

// Oldest orders by a function, and ByColumn by a column named at run time.
func (s *Store) Oldest(ctx context.Context) ([]*entity.User, error) {
	return s.User.Query().Order(func(sel *sql.Selector) { sel.OrderBy(sql.Desc(sel.C(user.FieldAge))) }).All(ctx)
}

func (s *Store) ByColumn(ctx context.Context, column string) ([]*entity.User, error) {
	return s.User.Query().Order(integration.Desc(column)).All(ctx)
}

// Backdate sets updated_at itself, and says that the update's own stamp is
// not wanted.
func (s *Store) Backdate(ctx context.Context, id int, at time.Time) error {
	return s.User.UpdateOneID(id).SetUpdatedAt(at).SkipDefault(user.FieldUpdatedAt).Exec(ctx)
}

// Touch changes a field and keeps updated_at as it is.
func (s *Store) Touch(ctx context.Context, id int) error {
	return s.User.UpdateOneID(id).AddAge(1).SkipDefault(user.FieldUpdatedAt).Exec(ctx)
}

// countUsers only queries on the client it is given.
func countUsers(ctx context.Context, c *integration.Client) (int, error) {
	return c.User.Query().Count(ctx)
}

// AddAndCount adds a user in a transaction that is rolled back, and counts
// inside it and after it.
func (s *Store) AddAndCount(ctx context.Context, name, email string) (inside, after int, err error) {
	tx, err := s.Tx(ctx)
	if err != nil {
		return 0, 0, err
	}
	if _, err = tx.User.Create().SetName(name).SetEmail(email).SetAge(1).Save(ctx); err == nil {
		inside, err = countUsers(ctx, tx.Client())
	}
	if rerr := tx.Rollback(); err == nil {
		err = rerr
	}
	if err != nil {
		return 0, 0, err
	}
	after, err = countUsers(ctx, s.Client)
	return inside, after, err
}

// Creating reports whether a mutation creates a row, by the names the
// generated root package repeats from the library.
func Creating(m integration.Mutation) bool { return m.Op().Is(integration.OpCreate) }
