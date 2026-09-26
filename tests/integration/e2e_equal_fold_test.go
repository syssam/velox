package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/user"
)

// TestMultiDialect_EqualFoldIsNotAPattern pins that EqualFold compares a
// value, wildcards included, as itself. On PostgreSQL it renders ILIKE, a
// pattern match, and passed the value unescaped: EqualFold("%") matched
// every row, and a GraphQL client reached it through nameEqualFold.
func TestMultiDialect_EqualFoldIsNotAPattern(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		ctx := context.Background()
		for i, name := range []string{"abc", "Ab%", "a_c", `a\c`} {
			_, err := c.User.Create().SetName(name).SetEmail(string(rune('a'+i)) + "@ef").SetAge(30).
				SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
			require.NoError(t, err)
		}
		for value, want := range map[string][]string{
			"%":   nil,
			"AB%": {"Ab%"},
			"A_C": {"a_c"},
			`A\C`: {`a\c`},
			"ABC": {"abc"},
			"a%":  nil,
		} {
			names, err := c.User.Query().Where(user.NameField.EqualFold(value)).Order(user.ByName()).
				Select(user.FieldName).Strings(ctx)
			require.NoError(t, err, "EqualFold(%q)", value)
			if want == nil {
				want = []string{}
			}
			if names == nil {
				names = []string{}
			}
			require.Equal(t, want, names, "EqualFold(%q)", value)
		}
	})
}

// TestSQLite_EqualFoldFoldsOnlyASCII pins a documented limitation
// (docs/architecture-overview.md §4.9): SQLite's lower() folds only ASCII,
// so a non-ASCII upper-case value stored in the column does not fold.
func TestSQLite_EqualFoldFoldsOnlyASCII(t *testing.T) {
	c := openTestClient(t)
	ctx := context.Background()
	for i, name := range []string{"ÜBER", "über"} {
		_, err := c.User.Create().SetName(name).SetEmail(string(rune('a'+i)) + "@ascii").SetAge(30).
			SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
		require.NoError(t, err)
	}
	names, err := c.User.Query().Where(user.NameField.EqualFold("ÜBER")).Select(user.FieldName).Strings(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"über"}, names, "the stored lower-case value matches; the stored upper-case one does not fold")
}
