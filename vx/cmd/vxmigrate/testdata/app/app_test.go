package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/filter"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/user"
)

func names(us []*entity.User) (out []string) {
	for _, u := range us {
		out = append(out, u.Name)
	}
	return out
}

// show prints values with a space between them.
func show(vs ...any) string { return strings.TrimSpace(fmt.Sprintln(vs...)) }

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if show(got) != show(want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
	}
}

func TestStore(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	nick := "annie"
	ann, err := s.AddUser(ctx, "ann", "ann@example.com", 30, &nick)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := s.AddUser(ctx, "bob", "bob@example.com", 17, nil)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := s.AddUser(ctx, "cat", "cat@example.com", 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "role and kind", Kind(ann), "User:user")
	eq(t, "nickname", *ann.Nickname, "annie")
	if _, err := s.AddPost(ctx, ann, "a1"); err != nil {
		t.Fatal(err)
	}
	ps, err := s.AddPosts(ctx, bob, "b1", "ab2")
	if err != nil || len(ps) != 2 {
		t.Fatal(ps, err)
	}

	nu, np, err := s.CountByID(ctx, ann.ID)
	eq(t, "generic over entities, with an id", show(nu, np, err), "1 1 <nil>")
	nu, np, err = s.CountByID(ctx)
	eq(t, "generic over entities, with nothing to add", show(nu, np, err), "3 2 <nil>")

	n, err := s.PostsMatching(ctx, &filter.PostWhereInput{TitleNEQ: ptr("a1")})
	eq(t, "posts matching a where input", show(n, err), "2 <nil>")

	adults, err := s.Adults(ctx, 0)
	eq(t, "adults", show(names(adults), err), "[cat ann] <nil>")
	adults, err = s.Adults(ctx, 40)
	eq(t, "adults over 40", show(names(adults), err), "[cat] <nil>")
	u, err := s.Named(ctx, "bob")
	eq(t, "named", show(u.ID, err), show(bob.ID, nil))
	u, err = s.Named(ctx, "nobody")
	eq(t, "named nobody", show(u == nil, err), "true <nil>")
	n, err = s.AuthorsOfDrafts(ctx)
	eq(t, "authors of drafts", show(n, err), "2 <nil>")
	ok, err := s.HasEmail(ctx, "cat@example.com")
	eq(t, "has email", show(ok, err), "true <nil>")
	ok, err = s.HasEmail(ctx, "ann@example.com") // she has a nickname
	eq(t, "has email with a nickname", show(ok, err), "false <nil>")

	p, err := s.Promote(ctx, ann.ID)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "promoted", show(p.Role, p.Age, p.Nickname == nil), "admin 31 true")
	guest := user.RoleGuest
	p, err = s.SetNickname(ctx, ann.ID, ptr("an"), &guest)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "nickname set through a setter passed as a value", show(*p.Nickname, p.Role), "an guest")
	p, err = s.SetNickname(ctx, ann.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "nickname cleared", show(p.Nickname == nil, p.Role), "true guest")
	eq(t, "rename", s.Rename(ctx, cat.ID, "kat"), nil)
	if err := s.Rename(ctx, 9999, "x"); err == nil {
		t.Error("renaming a user that does not exist did not fail")
	}
	eq(t, "retitle", s.Retitle(ctx, cat.ID, "kat"), nil)
	got, err := s.Find(ctx, cat.ID)
	eq(t, "found", show(got.Name, err), "kat <nil>")

	first := 2
	page, err := s.Posts(ctx, nil, &first, byTitle(t), &filter.PostWhereInput{TitleNEQ: ptr("zzz")})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "page", show(len(page.Edges), page.TotalCount, page.PageInfo.HasNextPage, page.Edges[0].Node.Title), "2 3 true a1")
	page, err = s.Posts(ctx, page.PageInfo.EndCursor, &first, byTitle(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "next page", show(len(page.Edges), page.Edges[0].Node.Title), "1 b1")

	users, err := s.Users(ctx, &first, byNameDesc(t), &filter.UserWhereInput{NameNEQ: ptr("zzz")}, false)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "users", show(len(users.Edges), users.TotalCount, users.Edges[0].Node.Name), "2 3 kat")
	users, err = s.Users(ctx, nil, byNameDesc(t), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "adult users", show(len(users.Edges), users.Edges[0].Node.Name), "2 kat")

	red, err := s.AddTag(ctx, "red")
	if err != nil {
		t.Fatal(err)
	}
	blue, err := s.AddTag(ctx, "blue")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "label", s.Label(ctx, ps[0].ID, red.ID, blue.ID), nil)
	eq(t, "unlabel", s.Unlabel(ctx, ps[0].ID, red.ID), nil)
	n, err = s.TagCount(ctx, ps[0].ID)
	eq(t, "tags left", show(n, err), "1 <nil>")

	loaded, err := s.WithPostsStarting(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "eager load with a condition", show(len(loaded), len(loaded[0].Edges.Posts), len(loaded[1].Edges.Posts)), "3 1 1")

	eq(t, "transfer", s.Transfer(ctx, bob.ID, cat.ID), nil)
	if err := s.Transfer(ctx, cat.ID, 9999); err == nil {
		t.Error("a transfer to a user that does not exist did not fail")
	}
	moved, err := s.Post.Query().Where(post.HasAuthorWith(user.IDField.EQ(cat.ID))).Count(ctx)
	eq(t, "posts moved, and the failed transfer rolled back", show(moved, err), "2 <nil>")

	n, err = PostCount(withHandle(ctx, s), cat)
	eq(t, "posts counted from an entity that was handed over", show(n, err), "2 <nil>")
	narrow, err := s.NamesOnly(ctx)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "only the names were read", show(names(narrow), narrow[0].Email == "", narrow[0].ID == ann.ID), "[ann bob kat] true true")
	titles, err := s.Titles(ctx, cat.ID)
	eq(t, "titles through the entity", show(titles, err), "[kat:ab2 kat:b1] <nil>")
	per, err := s.PostsPerStatus(ctx)
	eq(t, "posts per status", show(per, err), "map[draft:3] <nil>")
	n, err = s.Publish(ctx)
	eq(t, "published", show(n, err), "3 <nil>")
	n, err = s.RemovePosts(ctx, cat.ID)
	eq(t, "posts removed", show(n, err), "2 <nil>")
	eq(t, "remove", s.Remove(ctx, bob.ID), nil)
	if err := s.Remove(ctx, bob.ID); err == nil {
		t.Error("removing a user twice did not fail")
	}
	ids, err := s.IDs(ctx)
	eq(t, "ids", show(ids, err), show([]int{ann.ID, cat.ID}, nil))
}

// The forms tests use: they panic on an error.
func TestStore_X(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	dan := s.User.Create().SetName("dan").SetEmail("dan@example.com").SetAge(20).SaveX(ctx)
	eq(t, "GetX", s.User.GetX(ctx, dan.ID).Name, "dan")
	s.User.UpdateOneID(dan.ID).SetAge(21).ExecX(ctx)
	eq(t, "SaveX of an update", s.User.UpdateOneID(dan.ID).AddAge(1).SaveX(ctx).Age, 22)
	eq(t, "ExistX with a condition", s.User.Query().Where(user.RoleField.EQ(user.RoleUser)).ExistX(ctx), true)
	eq(t, "ExistX", s.User.Query().Where(user.NameField.EQ("eve")).ExistX(ctx), false)
	s.User.DeleteOneID(dan.ID).ExecX(ctx)
	eq(t, "all gone", s.User.Query().ExistX(ctx), false)
}

func ptr[T any](v T) *T { return &v }
