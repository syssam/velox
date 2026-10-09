package tests

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	client "github.com/syssam/velox/vx/tests/db"

	"github.com/stretchr/testify/require"

	refentity "github.com/syssam/velox/tests/integration/entity"
	veloxuser "github.com/syssam/velox/tests/integration/user"
	"github.com/syssam/velox/vx/tests/db/post"
	"github.com/syssam/velox/vx/tests/db/tag"
	"github.com/syssam/velox/vx/tests/db/user"
)

// snapshot is everything the differential test compares, read back through
// one ORM's own query API (eager loads included).
type snapshot struct {
	Users []string
	Posts []string
	Tags  []string
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// TestDifferential_RandomOperations applies the same random sequence of
// creates, updates, edge changes and deletes through vx to one database and
// through velox to another, and requires the same outcome of every step (both
// succeed with the same id, or both fail) and the same final contents.
func TestDifferential_RandomOperations(t *testing.T) {
	ctx := context.Background()
	for _, seed := range []uint64{1, 2, 3} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			db, _ := open(t)
			_, ref := open(t)
			rng := rand.New(rand.NewPCG(seed, 99))
			var users, posts, tags []int
			pick := func(ids []int) int { return ids[rng.IntN(len(ids))] }
			some := func(ids []int) []int {
				var out []int
				for _, id := range ids {
					if rng.IntN(3) == 0 {
						out = append(out, id)
					}
				}
				return out
			}
			// same requires both sides to agree on whether the step worked.
			same := func(step int, what string, vxErr, veloxErr error) bool {
				t.Helper()
				require.Equalf(t, veloxErr == nil, vxErr == nil, "step %d (%s): vx err = %v, velox err = %v", step, what, vxErr, veloxErr)
				return vxErr == nil
			}
			for step := range 400 {
				switch op := rng.IntN(12); {
				case op == 0 || len(users) == 0:
					name := fmt.Sprintf("user%d", step)
					if rng.IntN(8) == 0 {
						name = "" // rejected by the schema validator
					}
					email, age := fmt.Sprintf("u%d@example.com", step), rng.IntN(60)
					vb := client.User.Create(db).Set(user.Name.Set(name), user.Email.Set(email), user.Age.Set(age))
					rb := ref.User.Create().SetName(name).SetEmail(email).SetAge(age)
					if rng.IntN(2) == 0 {
						nick := fmt.Sprintf("nick%d", step)
						vb.Set(user.Nickname.Set(nick))
						rb.SetNickname(nick)
					}
					a, errA := vb.Save(ctx)
					b, errB := rb.Save(ctx)
					if same(step, "create user", errA, errB) {
						require.Equal(t, b.ID, a.ID)
						users = append(users, a.ID)
					}
				case op == 1 || len(tags) == 0:
					name := fmt.Sprintf("tag%d", step)
					a, errA := client.Tag.Create(db).Set(tag.Name.Set(name)).Save(ctx)
					b, errB := ref.Tag.Create().SetName(name).Save(ctx)
					if same(step, "create tag", errA, errB) {
						require.Equal(t, b.ID, a.ID)
						tags = append(tags, a.ID)
					}
				case op <= 3 || len(posts) == 0:
					title, author, ts := fmt.Sprintf("post%d", step), pick(users), some(tags)
					a, errA := client.Post.Create(db).Set(post.Title.Set(title), post.Content.Set("c"), post.Author.Set(author), post.Tags.Add(ts...)).Save(ctx)
					b, errB := ref.Post.Create().SetTitle(title).SetContent("c").SetAuthorID(author).AddTagIDs(ts...).Save(ctx)
					if same(step, "create post", errA, errB) {
						require.Equal(t, b.ID, a.ID)
						posts = append(posts, a.ID)
					}
				case op == 4:
					id, d := pick(users), rng.IntN(5)-2
					_, errA := client.User.Update(db).Set(user.Age.Add(d)).Where(user.ID.EQ(id)).SaveOne(ctx)
					_, errB := ref.User.UpdateOneID(id).AddAge(d).Save(ctx)
					same(step, "add age", errA, errB)
				case op == 5:
					id := pick(users)
					if rng.IntN(2) == 0 {
						_, errA := client.User.Update(db).Set(user.Nickname.Clear()).Where(user.ID.EQ(id)).SaveOne(ctx)
						_, errB := ref.User.UpdateOneID(id).ClearNickname().Save(ctx)
						same(step, "clear nickname", errA, errB)
					} else {
						name := fmt.Sprintf("renamed%d", step)
						if rng.IntN(6) == 0 {
							name = ""
						}
						_, errA := client.User.Update(db).Set(user.Name.Set(name)).Where(user.ID.EQ(id)).SaveOne(ctx)
						_, errB := ref.User.UpdateOneID(id).SetName(name).Save(ctx)
						same(step, "rename", errA, errB)
					}
				case op == 6:
					id, ts := pick(posts), some(tags)
					_, errA := client.Post.Update(db).Set(post.Tags.Clear(), post.Tags.Add(ts...), post.ViewCount.Add(1)).Where(post.ID.EQ(id)).SaveOne(ctx)
					_, errB := ref.Post.UpdateOneID(id).ClearTags().AddTagIDs(ts...).AddViewCount(1).Save(ctx)
					same(step, "replace tags", errA, errB)
				case op == 7:
					id, ts := pick(posts), some(tags)
					_, errA := client.Post.Update(db).Set(post.Tags.Remove(ts...)).Where(post.ID.EQ(id)).Exec(ctx)
					errB := ref.Post.UpdateOneID(id).RemoveTagIDs(ts...).Exec(ctx)
					same(step, "remove tags", errA, errB)
				case op == 8:
					id, author := pick(posts), pick(users)
					_, errA := client.Post.Update(db).Set(post.Author.Set(author)).Where(post.ID.EQ(id)).SaveOne(ctx)
					_, errB := ref.Post.UpdateOneID(id).SetAuthorID(author).Save(ctx)
					same(step, "move post", errA, errB)
				case op == 9:
					i := rng.IntN(len(posts))
					id := posts[i]
					_, errA := client.Post.Delete(db).Where(post.ID.EQ(id)).Exec(ctx)
					errB := ref.Post.DeleteOneID(id).Exec(ctx)
					if same(step, "delete post", errA, errB) {
						posts = slices.Delete(posts, i, i+1)
					}
				case op == 10:
					// Fails on both while the user still has posts.
					i := rng.IntN(len(users))
					id := users[i]
					_, errA := client.User.Delete(db).Where(user.ID.EQ(id)).Exec(ctx)
					errB := ref.User.DeleteOneID(id).Exec(ctx)
					if same(step, "delete user", errA, errB) {
						users = slices.Delete(users, i, i+1)
					}
				default:
					// A bulk update by predicate.
					lo := rng.IntN(60)
					nA, errA := client.User.Update(db).Set(user.Age.Add(1)).Where(user.Age.LT(lo)).Exec(ctx)
					nB, errB := ref.User.Update().Where(veloxuser.AgeField.LT(lo)).AddAge(1).Save(ctx)
					if same(step, "bulk add age", errA, errB) {
						require.Equal(t, nB, nA, "step %d: rows affected", step)
					}
				}
			}

			var a, b snapshot
			us, err := client.User.Query(db).OrderBy(user.ID.Asc()).All(ctx)
			require.NoError(t, err)
			for _, u := range us {
				a.Users = append(a.Users, fmt.Sprint(u.ID, u.Name, u.Email, u.Age, str(u.Nickname), u.Role))
			}
			ps, err := client.Post.Query(db).OrderBy(post.ID.Asc()).WithAuthor().WithTags(client.Tag.Query(db).OrderBy(tag.ID.Asc())).All(ctx)
			require.NoError(t, err)
			for _, p := range ps {
				var ts []int
				for _, tg := range p.Edges.Tags {
					ts = append(ts, tg.ID)
				}
				a.Posts = append(a.Posts, fmt.Sprint(p.ID, p.Title, p.Status, p.ViewCount, p.Edges.Author.ID, ts))
			}
			ts, err := client.Tag.Query(db).OrderBy(tag.ID.Asc()).All(ctx)
			require.NoError(t, err)
			for _, tg := range ts {
				a.Tags = append(a.Tags, fmt.Sprint(tg.ID, tg.Name))
			}

			rus, err := ref.User.Query().All(ctx)
			require.NoError(t, err)
			slices.SortFunc(rus, func(x, y *refentity.User) int { return x.ID - y.ID })
			for _, u := range rus {
				b.Users = append(b.Users, fmt.Sprint(u.ID, u.Name, u.Email, u.Age, str(u.Nickname), u.Role))
			}
			rps, err := ref.Post.Query().WithAuthor().WithTags().All(ctx)
			require.NoError(t, err)
			slices.SortFunc(rps, func(x, y *refentity.Post) int { return x.ID - y.ID })
			for _, p := range rps {
				var ids []int
				for _, tg := range p.Edges.Tags {
					ids = append(ids, tg.ID)
				}
				slices.Sort(ids)
				b.Posts = append(b.Posts, fmt.Sprint(p.ID, p.Title, p.Status, p.ViewCount, p.Edges.Author.ID, ids))
			}
			rts, err := ref.Tag.Query().All(ctx)
			require.NoError(t, err)
			slices.SortFunc(rts, func(x, y *refentity.Tag) int { return x.ID - y.ID })
			for _, tg := range rts {
				b.Tags = append(b.Tags, fmt.Sprint(tg.ID, tg.Name))
			}

			require.NotEmpty(t, a.Users)
			require.NotEmpty(t, a.Posts)
			require.Equal(t, b, a, "vx and velox ended with different data")
		})
	}
}
