// Package app is a small application written against velox's generated code:
// what vxmigrate is tested on. Its test runs before the migration and after.
package app

import (
	"context"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/tests/integration"
	postclient "github.com/syssam/velox/tests/integration/client/post"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/filter"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/predicate"
	"github.com/syssam/velox/tests/integration/tag"
	"github.com/syssam/velox/tests/integration/user"
)

// Store wraps the client by embedding it, as applications do.
type Store struct {
	*integration.Client
	name string
}

func (s *Store) AddUser(ctx context.Context, name, email string, age int, nick *string) (*entity.User, error) {
	return s.User.Create().
		SetName(name).
		SetEmail(email).
		SetAge(age).
		SetNillableNickname(nick).
		SetRole(roleOf("user")).
		Save(ctx)
}

func (s *Store) AddPost(ctx context.Context, author *entity.User, title string) (*entity.Post, error) {
	b := s.Post.Create().SetTitle(title)
	b.SetAuthorID(author.ID)
	return b.Save(ctx)
}

func (s *Store) AddPosts(ctx context.Context, author *entity.User, titles ...string) ([]*entity.Post, error) {
	bs := make([]*postclient.PostCreate, len(titles))
	for i, title := range titles {
		bs[i] = s.Post.Create().SetTitle(title).SetAuthor(author)
	}
	return s.Post.CreateBulk(bs...).Save(ctx)
}

// olderThan has nothing to add for an age that is not positive.
func olderThan(age int) predicate.User {
	if age <= 0 {
		return nil
	}
	return user.AgeField.GT(age)
}

// named is a condition written against the SQL builder.
func named(name string) predicate.User {
	return func(s *sql.Selector) {
		s.Where(sql.EQ(s.C(user.FieldName), name))
	}
}

func (s *Store) Adults(ctx context.Context, over int) ([]*entity.User, error) {
	q := s.User.Query().Where(user.AgeField.GTE(18))
	if p := olderThan(over); p != nil {
		q = q.Where(p)
	}
	return q.Order(user.ByName(sql.OrderDesc())).All(ctx)
}

func (s *Store) Named(ctx context.Context, name string) (*entity.User, error) {
	u, err := s.User.Query().Where(named(name)).Only(ctx)
	if integration.IsNotFound(err) {
		return nil, nil
	}
	return u, err
}

func (s *Store) AuthorsOfDrafts(ctx context.Context) (int, error) {
	return s.User.Query().Where(user.HasPostsWith(post.StatusField.EQ(post.StatusDraft))).Count(ctx)
}

func (s *Store) HasEmail(ctx context.Context, email string) (bool, error) {
	return s.User.Query().Where(user.EmailField.EQ(email), user.NicknameField.IsNil()).Exist(ctx)
}

func (s *Store) Promote(ctx context.Context, id int) (*entity.User, error) {
	return s.User.UpdateOneID(id).SetRole(user.RoleAdmin).ClearNickname().AddAge(1).Save(ctx)
}

func (s *Store) Rename(ctx context.Context, id int, name string) error {
	return s.User.UpdateOneID(id).SetName(name).Exec(ctx)
}

func (s *Store) Publish(ctx context.Context) (int, error) {
	return s.Post.Update().Where(post.StatusField.EQ(post.StatusDraft)).SetStatus(post.StatusPublished).Save(ctx)
}

func (s *Store) Remove(ctx context.Context, id int) error {
	return s.User.DeleteOneID(id).Exec(ctx)
}

func (s *Store) RemovePosts(ctx context.Context, author int) (int, error) {
	return s.Post.Delete().Where(post.HasAuthorWith(user.IDField.EQ(author))).Exec(ctx)
}

func (s *Store) Find(ctx context.Context, id int) (*entity.User, error) {
	return s.User.Get(ctx, id)
}

func (s *Store) Posts(ctx context.Context, after *gqlrelay.Cursor, first *int, orderBy *entity.PostOrder, where *filter.PostWhereInput) (*entity.PostConnection, error) {
	return s.Post.Query().Paginate(ctx, after, first, nil, nil, entity.WithPostOrder(orderBy), entity.WithPostFilter(where.Filter))
}

// Transfer moves every post of one user to another, in a transaction.
func (s *Store) Transfer(ctx context.Context, from, to int) error {
	tx, err := s.Tx(ctx)
	if err != nil {
		return err
	}
	if err := moveAll(ctx, tx.Post, from, to); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.User.Get(ctx, to); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// moveAll takes the client of one entity, bound to whatever it runs on.
func moveAll(ctx context.Context, posts *postclient.PostClient, from, to int) error {
	_, err := posts.Update().Where(post.HasAuthorWith(user.IDField.EQ(from))).SetAuthorID(to).Save(ctx)
	return err
}

func (s *Store) WithPostsStarting(ctx context.Context, prefix string) ([]*entity.User, error) {
	return s.User.Query().
		WithPosts(func(q entity.PostQuerier) { q.Where(post.TitleField.HasPrefix(prefix)) }).
		Order(integration.Asc(user.FieldID)).
		All(ctx)
}

func (s *Store) IDs(ctx context.Context) ([]int, error) {
	return s.User.Query().Order(user.ByID()).IDs(ctx)
}

func (s *Store) AddTag(ctx context.Context, name string) (*entity.Tag, error) {
	return s.Tag.Create().SetName(name).Save(ctx)
}

// Label puts tags on a post, and Unlabel takes them off.
func (s *Store) Label(ctx context.Context, postID int, tagIDs ...int) error {
	return s.Post.UpdateOneID(postID).AddTagIDs(tagIDs...).Exec(ctx)
}

func (s *Store) Unlabel(ctx context.Context, postID int, tagIDs ...int) error {
	return s.Post.UpdateOneID(postID).RemoveTagIDs(tagIDs...).Exec(ctx)
}

func (s *Store) TagCount(ctx context.Context, postID int) (int, error) {
	return s.Tag.Query().Where(tag.HasPostsWith(post.IDField.EQ(postID))).Count(ctx)
}

func roleOf(s string) user.Role { return user.Role(s) }

// Kind describes a user in a log line.
func Kind(u *entity.User) string { return integration.TypeUser + ":" + u.Role.String() }
