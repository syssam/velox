// Package app is a small application written against velox's generated code:
// what velox-migrate is tested on. Its test runs before the migration and after.
package app

import (
	"context"
	"errors"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/tests/integration"
	postclient "github.com/syssam/velox/tests/integration/client/post"
	userclient "github.com/syssam/velox/tests/integration/client/user"
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

// optional calls set when there is a value and clear when there is none.
func optional[T, B any](v *T, set func(T) B, clear func() B) {
	if v == nil {
		clear()
		return
	}
	set(*v)
}

// SetNickname sets or clears a user's nickname, and their role when given.
func (s *Store) SetNickname(ctx context.Context, id int, nick *string, role *user.Role) (*entity.User, error) {
	upd := s.User.UpdateOneID(id)
	optional(nick, upd.SetNickname, upd.ClearNickname)
	if role != nil {
		apply(*role, upd.SetRole)
	}
	return upd.Save(ctx)
}

func apply[T, B any](v T, set func(T) B) { set(v) }

// rename is a setter named without a builder: a row of a table of them.
var rename = (*userclient.UserUpdateOne).SetName

// Retitle renames a user through that table.
func (s *Store) Retitle(ctx context.Context, id int, name string) error {
	return rename(s.User.UpdateOneID(id), name).Exec(ctx)
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

// Users pages the users, with the options of the page collected first.
func (s *Store) Users(ctx context.Context, first *int, orderBy []*entity.UserOrder, where *filter.UserWhereInput, adults bool) (*entity.UserConnection, error) {
	opts := make([]entity.UserPaginateOption, 0, 3)
	order, err := entity.WithUserOrder(orderBy)
	if err != nil {
		return nil, err
	}
	opts = append(opts, order)
	if where != nil {
		opts = append(opts, entity.WithUserFilter(where.Filter))
	}
	if adults {
		opts = append(opts, entity.WithUserFilter(func() (predicate.User, error) {
			return user.AgeField.GTE(18), nil
		}))
	}
	q, ok := s.User.Query().(entity.UserPaginatable)
	if !ok {
		return nil, errors.New("users cannot be paged")
	}
	return q.Paginate(ctx, nil, first, nil, nil, opts...)
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

// Titles lists the titles of a user's posts, from the user.
func (s *Store) Titles(ctx context.Context, id int) ([]string, error) {
	u, err := s.User.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	posts, err := u.QueryPosts().Order(post.ByTitle()).All(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range posts {
		author, err := p.QueryAuthor().Only(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, author.Name+":"+p.Title)
	}
	return out, nil
}

// What follows is written once for several entities.

// idIn matches the rows with one of the ids, of whichever entity the column
// belongs to; with no ids it has nothing to add.
func idIn[P sql.PredicateFunc](id sql.IntField[P], ids ...int) P {
	if len(ids) == 0 {
		return nil
	}
	return id.In(ids...)
}

// both is the conjunction of predicates of any one entity.
func both[P ~func(*sql.Selector)](ps ...P) P {
	return P(sql.AndPredicates(ps...))
}

// titled holds the column a text search goes to.
type titled[P sql.PredicateFunc] struct {
	Text sql.StringField[P]
}

func (t titled[P]) starting(prefix string) P { return t.Text.HasPrefix(prefix) }

// CountByID counts the users and the posts with the given ids.
func (s *Store) CountByID(ctx context.Context, ids ...int) (users, posts int, err error) {
	ofUsers := []predicate.User{user.AgeField.GT(0)}
	if p := idIn(user.IDField, ids...); p != nil {
		ofUsers = append(ofUsers, p)
	}
	users, err = s.User.Query().Where(both(ofUsers...)).Count(ctx)
	if err != nil {
		return 0, 0, err
	}
	search := titled[predicate.Post]{Text: post.TitleField}
	ofPosts := []predicate.Post{search.starting("a")}
	if p := idIn(post.IDField, ids...); p != nil {
		ofPosts = append(ofPosts, p)
	}
	posts, err = s.Post.Query().Where(ofPosts...).Count(ctx)
	return users, posts, err
}

// guarded turns a where input into a predicate through whatever is handed
// to it, the way a permission check that wraps every list does.
func guarded[W, P any](where *W, toPredicate func(*W) (P, error)) (P, error) {
	return toPredicate(where)
}

// PostsMatching counts the posts a where input matches.
func (s *Store) PostsMatching(ctx context.Context, where *filter.PostWhereInput) (int, error) {
	p, err := guarded(where, (*filter.PostWhereInput).P)
	if err != nil {
		return 0, err
	}
	late := where.P
	q, err := late()
	if err != nil {
		return 0, err
	}
	return s.Post.Query().Where(p, q).Count(ctx)
}

// PostCount counts the posts of a user it is handed.
func PostCount(ctx context.Context, u *entity.User) (int, error) {
	return u.QueryPosts().Count(ctx)
}

// NamesOnly reads the users with their names and nothing else.
func (s *Store) NamesOnly(ctx context.Context) ([]*entity.User, error) {
	return s.User.Query().Order(user.ByName()).Select(user.FieldName).All(ctx)
}

// PostsPerStatus counts the posts of each status.
func (s *Store) PostsPerStatus(ctx context.Context) (map[string]int, error) {
	var rows []struct {
		Status string `json:"status"`
		N      int    `json:"n"`
	}
	err := s.Post.Query().GroupBy(post.FieldStatus).Aggregate(integration.As(integration.Count(), "n")).Scan(ctx, &rows)
	out := map[string]int{}
	for _, r := range rows {
		out[r.Status] = r.N
	}
	return out, err
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
