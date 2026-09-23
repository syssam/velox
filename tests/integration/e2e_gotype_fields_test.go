package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/token"
	"github.com/syssam/velox/testschema/types"
)

// Token carries one field per custom-GoType generator path: a GoType enum
// (tier), a nillable GoType enum (grade), a GoType string with two
// validators (label), a nillable GoType string with one validator (alias)
// and a GoType int with a validator (weight).
//
// Validators used to be emitted only under FeatureValidator; when they became
// unconditional, a GoType enum validator referenced an enum type the leaf
// package does not declare and called an IsValid() the user type does not
// have, and a GoType scalar validator was declared as func(types.Label) error
// while the runtime init asserted func(string) error. Neither compiled. The
// String() fast path also passed a types.Label to strings.Builder.WriteString.
// This whole package failing to build was the symptom; these tests pin the
// behavior on top.

// requireFieldValidationError asserts err is a ValidationError on Token.field.
func requireFieldValidationError(t *testing.T, err error, field string) {
	t.Helper()
	require.Error(t, err)
	var ve *integration.ValidationError
	require.True(t, errors.As(err, &ve), "want *ValidationError, got %T: %v", err, err)
	assert.Equal(t, "Token", ve.Entity)
	assert.Equal(t, field, ve.Field)
}

func TestGoTypeFields_CreateDefaultsAndValidValues(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tk, err := client.Token.Create().SetName("defaults").Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, types.TierFree, tk.Tier)
	assert.Nil(t, tk.Grade)
	assert.Equal(t, types.Label("token"), tk.Label)
	assert.Nil(t, tk.Alias)
	assert.Equal(t, types.Weight(1), tk.Weight)

	tk, err = client.Token.Create().
		SetName("set").
		SetTier(types.TierPro).
		SetGrade(types.TierFree).
		SetLabel("custom").
		SetAlias("al").
		SetWeight(7).
		Save(ctx)
	require.NoError(t, err)

	got, err := client.Token.Get(ctx, tk.ID)
	require.NoError(t, err)
	assert.Equal(t, types.TierPro, got.Tier)
	require.NotNil(t, got.Grade)
	assert.Equal(t, types.TierFree, *got.Grade)
	assert.Equal(t, types.Label("custom"), got.Label)
	require.NotNil(t, got.Alias)
	assert.Equal(t, types.Label("al"), *got.Alias)
	assert.Equal(t, types.Weight(7), got.Weight)
}

func TestGoTypeFields_CreateRejectsInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		field string
		set   func(c *integration.Client) error
	}{
		{"tier", func(c *integration.Client) error {
			_, err := c.Token.Create().SetName("x").SetTier("enterprise").Save(context.Background())
			return err
		}},
		{"grade", func(c *integration.Client) error {
			_, err := c.Token.Create().SetName("x").SetGrade("gold").Save(context.Background())
			return err
		}},
		{"label", func(c *integration.Client) error { // NotEmpty
			_, err := c.Token.Create().SetName("x").SetLabel("").Save(context.Background())
			return err
		}},
		{"label", func(c *integration.Client) error { // MaxLen(20)
			_, err := c.Token.Create().SetName("x").SetLabel("this-label-is-far-too-long").Save(context.Background())
			return err
		}},
		{"alias", func(c *integration.Client) error {
			_, err := c.Token.Create().SetName("x").SetAlias("").Save(context.Background())
			return err
		}},
		{"weight", func(c *integration.Client) error {
			_, err := c.Token.Create().SetName("x").SetWeight(0).Save(context.Background())
			return err
		}},
	} {
		t.Run(tc.field, func(t *testing.T) {
			client := openTestClient(t)
			requireFieldValidationError(t, tc.set(client), tc.field)
			n, err := client.Token.Query().Count(context.Background())
			require.NoError(t, err)
			assert.Zero(t, n, "a rejected create must not write a row")
		})
	}
}

func TestGoTypeFields_UpdateValidates(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	tk := client.Token.Create().SetName("u").SetAlias("al").SaveX(ctx)

	_, err := client.Token.UpdateOneID(tk.ID).SetTier("enterprise").Save(ctx)
	requireFieldValidationError(t, err, "tier")
	_, err = client.Token.UpdateOneID(tk.ID).SetGrade("gold").Save(ctx)
	requireFieldValidationError(t, err, "grade")
	_, err = client.Token.UpdateOneID(tk.ID).SetLabel("").Save(ctx)
	requireFieldValidationError(t, err, "label")
	_, err = client.Token.Update().Where(token.IDField.EQ(tk.ID)).SetAlias("").Save(ctx)
	requireFieldValidationError(t, err, "alias")
	_, err = client.Token.UpdateOneID(tk.ID).SetWeight(-3).Save(ctx)
	requireFieldValidationError(t, err, "weight")

	got := client.Token.GetX(ctx, tk.ID)
	assert.Equal(t, types.TierFree, got.Tier, "rejected updates must leave the row untouched")
	assert.Equal(t, types.Label("token"), got.Label)
	require.NotNil(t, got.Alias)
	assert.Equal(t, types.Label("al"), *got.Alias)

	got, err = client.Token.UpdateOneID(tk.ID).
		SetTier(types.TierPro).
		SetGrade(types.TierPro).
		SetLabel("renamed").
		ClearAlias().
		SetWeight(3).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, types.TierPro, got.Tier)
	require.NotNil(t, got.Grade)
	assert.Equal(t, types.TierPro, *got.Grade)
	assert.Equal(t, types.Label("renamed"), got.Label)
	assert.Nil(t, got.Alias)
	assert.Equal(t, types.Weight(3), got.Weight)
}

func TestGoTypeFields_String(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tk := client.Token.Create().
		SetName("str").
		SetTier(types.TierPro).
		SetLabel("lbl").
		SaveX(ctx)
	s := tk.String()
	assert.Contains(t, s, "tier=pro")
	assert.Contains(t, s, "label=lbl")
	assert.Contains(t, s, "weight=1")
	assert.NotContains(t, s, "grade=", "a nil nillable field is omitted")
	assert.NotContains(t, s, "alias=")

	tk = client.Token.UpdateOne(tk).SetGrade(types.TierFree).SetAlias("nick").SaveX(ctx)
	s = tk.String()
	assert.Contains(t, s, "grade=free")
	assert.Contains(t, s, "alias=nick")
}
