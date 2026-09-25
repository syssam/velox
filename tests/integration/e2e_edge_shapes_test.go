package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	"github.com/syssam/velox/runtime"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/tag"
	"github.com/syssam/velox/tests/integration/user"
)

// TestMultiDialect_EdgeShapes exercises the edge shapes testschema carries
// for TestTestschemaCoversGeneratorShapes, on every dialect:
//
//   - Tag.parent: a self-referencing, optional to-one edge with a hidden
//     key — eager-loading it over a root (NULL key) panicked, and the root
//     must read as loaded-and-empty (NotFound), not unloaded;
//   - User.token / Token.owner: a one-to-one edge between an int-keyed and a
//     UUID-keyed entity, set, read and cleared from both sides;
//   - Pet ordered by its nullable owner_id through the generated Paginate,
//     the single-order cursor path with NULL values.
func TestMultiDialect_EdgeShapes(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		ctx := context.Background()

		t.Run("self_ref_optional_hidden_key", func(t *testing.T) {
			root, err := c.Tag.Create().SetName("root").Save(ctx)
			require.NoError(t, err)
			child, err := c.Tag.Create().SetName("child").SetParentID(root.ID).Save(ctx)
			require.NoError(t, err)

			tags, err := c.Tag.Query().WithParent().WithChildren().Order(tag.ByID()).All(ctx)
			require.NoError(t, err)
			require.Len(t, tags, 2)
			_, err = tags[0].Edges.ParentOrErr()
			require.True(t, runtime.IsNotFound(err), "root parent: got %v", err)
			p, err := tags[1].Edges.ParentOrErr()
			require.NoError(t, err)
			require.Equal(t, root.ID, p.ID)
			kids, err := tags[0].Edges.ChildrenOrErr()
			require.NoError(t, err)
			require.Len(t, kids, 1)
			require.Equal(t, child.ID, kids[0].ID)
		})

		t.Run("one_to_one_mixed_id_types", func(t *testing.T) {
			tok, err := c.Token.Create().SetName("t1").Save(ctx)
			require.NoError(t, err)
			u, err := c.User.Create().SetName("owner").SetEmail("owner@es").SetAge(30).
				SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).SetTokenID(tok.ID).Save(ctx)
			require.NoError(t, err)

			owner, err := tok.QueryOwner().Only(ctx)
			require.NoError(t, err)
			require.Equal(t, u.ID, owner.ID)
			users, err := c.User.Query().Where(user.IDField.EQ(u.ID)).WithToken().All(ctx)
			require.NoError(t, err)
			got, err := users[0].Edges.TokenOrErr()
			require.NoError(t, err)
			require.Equal(t, tok.ID, got.ID)
			toks, err := c.Token.Query().WithOwner().All(ctx)
			require.NoError(t, err)
			o, err := toks[0].Edges.OwnerOrErr()
			require.NoError(t, err)
			require.Equal(t, u.ID, o.ID)

			_, err = c.User.UpdateOneID(u.ID).ClearToken().Save(ctx)
			require.NoError(t, err)
			n, err := tok.QueryOwner().Count(ctx)
			require.NoError(t, err)
			require.Zero(t, n, "cleared from the user side")
		})

		t.Run("single_order_nullable_paginate", func(t *testing.T) {
			u, err := c.User.Create().SetName("petowner").SetEmail("po@es").SetAge(30).
				SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
			require.NoError(t, err)
			for i, owned := range []bool{true, false, false, true, false} {
				b := c.Pet.Create().SetName(string(rune('a' + i)))
				if owned {
					b = b.SetOwnerID(u.ID)
				}
				_, err := b.Save(ctx)
				require.NoError(t, err)
			}
			var field entity.PetOrderField
			require.NoError(t, field.UnmarshalGQL("OWNER_ID"))
			order := &entity.PetOrder{Direction: gqlrelay.OrderDirectionAsc, Field: &field}
			all, err := c.Pet.Query().Paginate(ctx, nil, ptr(100), nil, nil, entity.WithPetOrder(order))
			require.NoError(t, err)
			var want, got []int
			for _, e := range all.Edges {
				want = append(want, e.Node.ID)
			}
			var after *gqlrelay.Cursor
			for range want {
				conn, err := c.Pet.Query().Paginate(ctx, after, ptr(2), nil, nil, entity.WithPetOrder(order))
				require.NoError(t, err)
				for _, e := range conn.Edges {
					got = append(got, e.Node.ID)
				}
				if !conn.PageInfo.HasNextPage {
					break
				}
				cur := roundTripCursor(t, conn.PageInfo.EndCursor)
				after = &cur
			}
			require.Len(t, want, 5)
			require.Equal(t, want, got)
		})
	})
}
