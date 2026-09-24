package integration_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	"github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/predicate"
	"github.com/syssam/velox/tests/integration/user"
)

// TestMultiDialect_CursorKeysetWalks pins that walking a connection page by
// page visits every row exactly once, in the order the database sorts them,
// forward and backward, for the cases the keyset predicates got wrong:
//
//   - a NULL order value: `(col, id) > (NULL, id)` is NULL, so a walk that
//     reached a NULL row stopped (single order) or never matched (multi);
//     where NULLs sort differs by dialect (first in ascending order on SQLite
//     and MySQL, last on PostgreSQL), so the predicate must too;
//   - a time order value stored at an offset other than the process's local
//     zone: the cursor decoded it in the local zone, and SQLite, which
//     compares times as text, returned an empty or repeated page.
//
// Cursors round-trip through their GraphQL encoding, as between requests.
func TestMultiDialect_CursorKeysetWalks(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		ctx := context.Background()
		// A named zone other than UTC and the CI machine's: the time bug needs
		// an offset that differs from the decoding process's local zone.
		// (modernc sqlite cannot read back an unnamed time.FixedZone.)
		zone, err := time.LoadLocation("America/New_York")
		require.NoError(t, err)
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, zone)
		nicks := []*string{nil, ptr("b"), nil, ptr("a"), ptr("b"), nil, ptr("c")}
		for i, n := range nicks {
			_, err := c.User.Create().SetName(fmt.Sprintf("u%d", i)).SetEmail(fmt.Sprintf("u%d@ck", i)).
				SetAge(30).SetRole(user.RoleUser).SetNillableNickname(n).
				SetCreatedAt(base.Add(time.Duration(i%3) * time.Hour)).SetUpdatedAt(base).Save(ctx)
			require.NoError(t, err)
		}

		type column struct {
			name  string
			value func(*entity.User) any
		}
		nickname := column{user.FieldNickname, func(u *entity.User) any {
			if u.Nickname == nil {
				return nil
			}
			return *u.Nickname
		}}
		createdAt := column{user.FieldCreatedAt, func(u *entity.User) any { return u.CreatedAt }}

		orderBy := func(cols []column, dirs []gqlrelay.OrderDirection, reverse bool) func(*sql.Selector) {
			return func(s *sql.Selector) {
				all := append(slices.Clone(cols), column{name: user.FieldID})
				for i, col := range all {
					dir := dirs[min(i, len(dirs)-1)]
					if reverse == (dir == gqlrelay.OrderDirectionAsc) {
						s.OrderBy(sql.Desc(s.C(col.name)))
					} else {
						s.OrderBy(sql.Asc(s.C(col.name)))
					}
				}
			}
		}
		cursorOf := func(u *entity.User, cols []column) *gqlrelay.Cursor {
			cur := gqlrelay.Cursor{ID: u.ID}
			if len(cols) == 1 {
				cur.Value = cols[0].value(u)
			} else {
				vals := make([]any, len(cols))
				for i, col := range cols {
					vals[i] = col.value(u)
				}
				cur.Value = vals
			}
			rt := roundTripCursor(t, &cur)
			return &rt
		}
		preds := func(cols []column, dirs []gqlrelay.OrderDirection, after, before *gqlrelay.Cursor) []func(*sql.Selector) {
			if len(cols) == 1 {
				return gqlrelay.CursorsPredicate(after, before, user.FieldID, cols[0].name, dirs[0])
			}
			names := make([]string, len(cols))
			for i, col := range cols {
				names[i] = col.name
			}
			ps, err := gqlrelay.MultiCursorsPredicate(after, before, &gqlrelay.MultiCursorsOptions{
				FieldID: user.FieldID, DirectionID: dirs[len(dirs)-1], Fields: names, Directions: dirs,
			})
			require.NoError(t, err)
			return ps
		}
		page := func(cols []column, dirs []gqlrelay.OrderDirection, after, before *gqlrelay.Cursor) []*entity.User {
			q := c.User.Query().Order(orderBy(cols, dirs, before != nil)).Limit(2)
			for _, p := range preds(cols, dirs, after, before) {
				q = q.Where(predicate.User(p))
			}
			us, err := q.All(ctx)
			require.NoError(t, err)
			if before != nil {
				slices.Reverse(us)
			}
			return us
		}
		idsOf := func(us []*entity.User) []int {
			out := make([]int, len(us))
			for i, u := range us {
				out[i] = u.ID
			}
			return out
		}

		for _, tc := range []struct {
			name string
			cols []column
			dirs []gqlrelay.OrderDirection
		}{
			{"nickname_asc", []column{nickname}, []gqlrelay.OrderDirection{gqlrelay.OrderDirectionAsc}},
			{"nickname_desc", []column{nickname}, []gqlrelay.OrderDirection{gqlrelay.OrderDirectionDesc}},
			{"created_at_asc", []column{createdAt}, []gqlrelay.OrderDirection{gqlrelay.OrderDirectionAsc}},
			{"created_at_desc", []column{createdAt}, []gqlrelay.OrderDirection{gqlrelay.OrderDirectionDesc}},
			{"multi_nickname_asc_created_desc", []column{nickname, createdAt},
				[]gqlrelay.OrderDirection{gqlrelay.OrderDirectionAsc, gqlrelay.OrderDirectionDesc}},
			{"multi_created_asc_nickname_desc", []column{createdAt, nickname},
				[]gqlrelay.OrderDirection{gqlrelay.OrderDirectionAsc, gqlrelay.OrderDirectionDesc}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				all, err := c.User.Query().Order(orderBy(tc.cols, tc.dirs, false)).All(ctx)
				require.NoError(t, err)
				want := idsOf(all)
				require.Len(t, want, len(nicks))

				var fwd []int
				var after *gqlrelay.Cursor
				for range len(nicks) + 1 {
					us := page(tc.cols, tc.dirs, after, nil)
					if len(us) == 0 {
						break
					}
					fwd = append(fwd, idsOf(us)...)
					after = cursorOf(us[len(us)-1], tc.cols)
				}
				require.Equal(t, want, fwd, "forward walk")

				var bwd []int
				before := cursorOf(all[len(all)-1], tc.cols)
				for range len(nicks) + 1 {
					us := page(tc.cols, tc.dirs, nil, before)
					if len(us) == 0 {
						break
					}
					bwd = append(idsOf(us), bwd...)
					before = cursorOf(us[0], tc.cols)
				}
				require.Equal(t, want[:len(want)-1], bwd, "backward walk from the last row")
			})
		}
	})
}

func ptr[T any](v T) *T { return &v }
