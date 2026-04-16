package basic_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"example.com/basic/velox"
	"example.com/basic/velox/comment"
	"example.com/basic/velox/entity"
	"example.com/basic/velox/post"
	"example.com/basic/velox/tag"
	"example.com/basic/velox/user"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"

	// Query package registers query factories via init().
	_ "example.com/basic/velox/query"
	_ "modernc.org/sqlite"
)

// openTestClient creates a new in-memory SQLite client with schema created.
func openTestClient(t *testing.T) *velox.Client {
	t.Helper()
	client, err := velox.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))
	return client
}

// now is a shared timestamp for deterministic tests.
var now = time.Now().Truncate(time.Second)

// createUser is a helper that creates a user with all required fields set.
func createUser(t *testing.T, client *velox.Client, name, email string, age int) *entity.User {
	t.Helper()
	u, err := client.User.Create().
		SetName(name).
		SetEmail(email).
		SetAge(age).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(context.Background())
	require.NoError(t, err)
	return u
}

// createTag is a helper that creates a tag.
func createTag(t *testing.T, client *velox.Client, name string) *entity.Tag {
	t.Helper()
	tg, err := client.Tag.Create().
		SetName(name).
		Save(context.Background())
	require.NoError(t, err)
	return tg
}

// seedUsers creates a standard set of test users and returns them.
func seedUsers(t *testing.T, client *velox.Client) []*entity.User {
	t.Helper()
	return []*entity.User{
		createUser(t, client, "Alice", "alice@test.com", 25),
		createUser(t, client, "Bob", "bob@test.com", 30),
		createUser(t, client, "Charlie", "charlie@test.com", 35),
	}
}

// =============================================================================
// Schema & Setup
// =============================================================================

func TestE2E_SchemaCreate(t *testing.T) {
	_ = openTestClient(t)
}

func TestE2E_SchemaCreate_Idempotent(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Creating schema twice should not error.
	require.NoError(t, client.Schema.Create(ctx))
}

// =============================================================================
// User CRUD
// =============================================================================

func TestE2E_CreateAndQueryUser(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Alice", "alice@example.com", 30)
	assert.NotZero(t, u.ID)
	assert.Equal(t, "Alice", u.Name)
	assert.Equal(t, "alice@example.com", u.Email)
	assert.Equal(t, 30, u.Age)
	assert.Equal(t, user.RoleUser, u.Role)

	// Query by ID.
	found, err := client.User.Query().
		Where(user.IDField.EQ(u.ID)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, u.ID, found.ID)
	assert.Equal(t, "Alice", found.Name)
	assert.Equal(t, "alice@example.com", found.Email)
}

func TestE2E_CreateUser_AllRoles(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	roles := []user.Role{user.RoleAdmin, user.RoleUser, user.RoleGuest}
	for i, role := range roles {
		_, err := client.User.Create().
			SetName("User" + role.String()).
			SetEmail(role.String() + "@test.com").
			SetAge(20 + i).
			SetRole(role).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}

	// Verify each role persists correctly.
	for _, role := range roles {
		users, err := client.User.Query().
			Where(user.RoleField.EQ(role)).
			All(ctx)
		require.NoError(t, err)
		require.Len(t, users, 1)
		assert.Equal(t, role, users[0].Role)
	}
}

func TestE2E_CreateUser_OptionalAge(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Age is Optional() without Default(0), so omitting it triggers a NOT NULL
	// constraint error. This matches the documented behavior: Optional() only
	// handles zero values at the ORM layer, it does NOT add DB DEFAULT.
	// To make age truly optional at the DB level, use Default(0) or Nillable().
	_, err := client.User.Create().
		SetName("NoAge").
		SetEmail("noage@test.com").
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	assert.Error(t, err, "Optional() without Default() should fail on NOT NULL column")

	// With age explicitly set to 0, it should work.
	u, err := client.User.Create().
		SetName("NoAge").
		SetEmail("noage@test.com").
		SetAge(0).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, u.Age)
}

func TestE2E_CreateUser_DefaultTimestamps(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// created_at and updated_at have function defaults (time.Now).
	// They should be auto-set when not explicitly provided.
	u, err := client.User.Create().
		SetName("DefaultTS").
		SetEmail("default@test.com").
		SetAge(25).
		SetRole(user.RoleUser).
		Save(ctx)
	require.NoError(t, err)
	assert.False(t, u.CreatedAt.IsZero(), "created_at should be auto-set")
	assert.False(t, u.UpdatedAt.IsZero(), "updated_at should be auto-set")
}

func TestE2E_UpdateUser(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Alice", "alice@test.com", 25)

	// UpdateOne returns updated entity.
	updated, err := client.User.UpdateOne(u).
		SetName("Alice Smith").
		SetAge(31).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Alice Smith", updated.Name)
	assert.Equal(t, 31, updated.Age)
	assert.Equal(t, u.ID, updated.ID)

	// Verify via independent query.
	found, err := client.User.Query().Where(user.IDField.EQ(u.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Alice Smith", found.Name)
	assert.Equal(t, 31, found.Age)
}

func TestE2E_UpdateUser_AddAge(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Alice", "alice@test.com", 25)

	// Atomic increment.
	updated, err := client.User.UpdateOne(u).
		AddAge(5).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 30, updated.Age)

	// Negative increment.
	updated, err = client.User.UpdateOne(updated).
		AddAge(-3).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 27, updated.Age)
}

func TestE2E_UpdateUser_ChangeRole(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Alice", "alice@test.com", 25)
	assert.Equal(t, user.RoleUser, u.Role)

	// Promote to admin.
	updated, err := client.User.UpdateOne(u).
		SetRole(user.RoleAdmin).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, user.RoleAdmin, updated.Role)
}

func TestE2E_UpdateBulk(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	seedUsers(t, client)

	// Update all users with age >= 30 to guest role.
	affected, err := client.User.Update().
		Where(user.AgeField.GTE(30)).
		SetRole(user.RoleGuest).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, affected) // Bob (30) and Charlie (35)

	guests, err := client.User.Query().
		Where(user.RoleField.EQ(user.RoleGuest)).
		All(ctx)
	require.NoError(t, err)
	assert.Len(t, guests, 2)
}

func TestE2E_UpdateBulk_NoneMatching(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	seedUsers(t, client)

	// No users with age > 100.
	affected, err := client.User.Update().
		Where(user.AgeField.GT(100)).
		SetName("Nobody").
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, affected)
}

func TestE2E_DeleteUser(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Alice", "alice@test.com", 25)

	err := client.User.DeleteOne(u).Exec(ctx)
	require.NoError(t, err)

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestE2E_DeleteUser_NonExistent(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Alice", "alice@test.com", 25)

	// Delete once.
	err := client.User.DeleteOne(u).Exec(ctx)
	require.NoError(t, err)

	// Delete again should return NotFoundError.
	err = client.User.DeleteOne(u).Exec(ctx)
	assert.Error(t, err)
	assert.True(t, velox.IsNotFound(err), "expected NotFoundError, got: %v", err)
}

func TestE2E_DeleteBulk(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	seedUsers(t, client)

	// Delete users with age < 30.
	deleted, err := client.User.Delete().
		Where(user.AgeField.LT(30)).
		Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted) // Alice (25)

	remaining, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, remaining)
}

// =============================================================================
// Tag CRUD (standalone entity without FK dependencies)
// =============================================================================

func TestE2E_TagCRUD(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Create.
	tg := createTag(t, client, "golang")
	assert.NotZero(t, tg.ID)
	assert.Equal(t, "golang", tg.Name)

	// Query.
	found, err := client.Tag.Query().
		Where(tag.IDField.EQ(tg.ID)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "golang", found.Name)

	// Update.
	updated, err := client.Tag.UpdateOne(tg).
		SetName("Go").
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Go", updated.Name)

	// Delete.
	err = client.Tag.DeleteOne(tg).Exec(ctx)
	require.NoError(t, err)

	count, err := client.Tag.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestE2E_TagBulkCreate(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tags, err := client.Tag.CreateBulk(
		client.Tag.Create().SetName("go"),
		client.Tag.Create().SetName("rust"),
		client.Tag.Create().SetName("python"),
	).Save(ctx)
	require.NoError(t, err)
	assert.Len(t, tags, 3)

	for _, tg := range tags {
		assert.NotZero(t, tg.ID)
	}
}

func TestE2E_TagUniqueConstraint(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createTag(t, client, "unique-tag")

	// Duplicate name should fail.
	_, err := client.Tag.Create().
		SetName("unique-tag").
		Save(ctx)
	assert.Error(t, err, "duplicate tag name should fail unique constraint")
}

// =============================================================================
// Query Operations
// =============================================================================

func TestE2E_QueryFirst(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	seedUsers(t, client)

	// First with ordering.
	first, err := client.User.Query().
		Order(user.ByAge()).
		First(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Alice", first.Name) // Age 25 (youngest)
}

func TestE2E_QueryFirst_Empty(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	_, err := client.User.Query().First(ctx)
	assert.Error(t, err)
	assert.True(t, velox.IsNotFound(err), "expected NotFoundError on empty table")
}

func TestE2E_QueryOnly(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Alice", "alice@test.com", 25)

	only, err := client.User.Query().
		Where(user.IDField.EQ(u.ID)).
		Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Alice", only.Name)
}

func TestE2E_QueryOnly_Multiple(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	seedUsers(t, client)

	// Only() with multiple results should error.
	_, err := client.User.Query().Only(ctx)
	assert.Error(t, err)
	assert.True(t, velox.IsNotSingular(err), "expected NotSingularError, got: %v", err)
}

func TestE2E_QueryOnly_NotFound(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	_, err := client.User.Query().
		Where(user.IDField.EQ(99999)).
		Only(ctx)
	assert.Error(t, err)
	assert.True(t, velox.IsNotFound(err), "expected NotFoundError, got: %v", err)
}

func TestE2E_QueryCount(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Empty.
	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	seedUsers(t, client)

	count, err = client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	// Count with filter.
	count, err = client.User.Query().
		Where(user.AgeField.GT(25)).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestE2E_QueryExist(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	exists, err := client.User.Query().Exist(ctx)
	require.NoError(t, err)
	assert.False(t, exists)

	createUser(t, client, "Alice", "alice@test.com", 25)

	exists, err = client.User.Query().Exist(ctx)
	require.NoError(t, err)
	assert.True(t, exists)

	// Exist with filter.
	exists, err = client.User.Query().
		Where(user.AgeField.GT(100)).
		Exist(ctx)
	require.NoError(t, err)
	assert.False(t, exists)
}

// =============================================================================
// Predicates
// =============================================================================

func TestE2E_PredicateComparisons(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	seedUsers(t, client) // Alice(25), Bob(30), Charlie(35)

	tests := []struct {
		name     string
		pred     func() []*entity.User
		expected []string
	}{
		{
			name: "EQ",
			pred: func() []*entity.User {
				u, _ := client.User.Query().Where(user.AgeField.EQ(30)).All(ctx)
				return u
			},
			expected: []string{"Bob"},
		},
		{
			name: "NEQ",
			pred: func() []*entity.User {
				u, _ := client.User.Query().Where(user.AgeField.NEQ(30)).Order(user.ByAge()).All(ctx)
				return u
			},
			expected: []string{"Alice", "Charlie"},
		},
		{
			name: "GT",
			pred: func() []*entity.User {
				u, _ := client.User.Query().Where(user.AgeField.GT(30)).All(ctx)
				return u
			},
			expected: []string{"Charlie"},
		},
		{
			name: "GTE",
			pred: func() []*entity.User {
				u, _ := client.User.Query().Where(user.AgeField.GTE(30)).Order(user.ByAge()).All(ctx)
				return u
			},
			expected: []string{"Bob", "Charlie"},
		},
		{
			name: "LT",
			pred: func() []*entity.User {
				u, _ := client.User.Query().Where(user.AgeField.LT(30)).All(ctx)
				return u
			},
			expected: []string{"Alice"},
		},
		{
			name: "LTE",
			pred: func() []*entity.User {
				u, _ := client.User.Query().Where(user.AgeField.LTE(30)).Order(user.ByAge()).All(ctx)
				return u
			},
			expected: []string{"Alice", "Bob"},
		},
		{
			name: "In",
			pred: func() []*entity.User {
				u, _ := client.User.Query().Where(user.AgeField.In(25, 35)).Order(user.ByAge()).All(ctx)
				return u
			},
			expected: []string{"Alice", "Charlie"},
		},
		{
			name: "NotIn",
			pred: func() []*entity.User {
				u, _ := client.User.Query().Where(user.AgeField.NotIn(25, 35)).All(ctx)
				return u
			},
			expected: []string{"Bob"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			users := tt.pred()
			require.Len(t, users, len(tt.expected))
			for i, u := range users {
				assert.Equal(t, tt.expected[i], u.Name)
			}
		})
	}
}

func TestE2E_PredicateString(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	seedUsers(t, client) // Alice, Bob, Charlie

	// Contains.
	users, err := client.User.Query().
		Where(user.NameField.Contains("li")).
		Order(user.ByName()).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, "Alice", users[0].Name)
	assert.Equal(t, "Charlie", users[1].Name)

	// HasPrefix.
	users, err = client.User.Query().
		Where(user.NameField.HasPrefix("Bo")).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "Bob", users[0].Name)

	// HasSuffix.
	users, err = client.User.Query().
		Where(user.NameField.HasSuffix("ie")).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "Charlie", users[0].Name)
}

func TestE2E_PredicateCombinators(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	seedUsers(t, client) // Alice(25), Bob(30), Charlie(35)

	// AND: age >= 25 AND age <= 30.
	users, err := client.User.Query().
		Where(
			user.And(
				user.AgeField.GTE(25),
				user.AgeField.LTE(30),
			),
		).
		Order(user.ByAge()).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, "Alice", users[0].Name)
	assert.Equal(t, "Bob", users[1].Name)

	// OR: name = "Alice" OR name = "Charlie".
	users, err = client.User.Query().
		Where(
			user.Or(
				user.NameField.EQ("Alice"),
				user.NameField.EQ("Charlie"),
			),
		).
		Order(user.ByName()).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, "Alice", users[0].Name)
	assert.Equal(t, "Charlie", users[1].Name)

	// NOT: age != 30.
	users, err = client.User.Query().
		Where(user.Not(user.AgeField.EQ(30))).
		Order(user.ByAge()).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, "Alice", users[0].Name)
	assert.Equal(t, "Charlie", users[1].Name)
}

func TestE2E_MultipleWherePredicates(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	seedUsers(t, client)

	// Multiple Where calls are ANDed together.
	users, err := client.User.Query().
		Where(user.AgeField.GTE(25)).
		Where(user.AgeField.LTE(30)).
		Order(user.ByAge()).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, "Alice", users[0].Name)
	assert.Equal(t, "Bob", users[1].Name)
}

// =============================================================================
// Ordering
// =============================================================================

func TestE2E_OrderAscDesc(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	seedUsers(t, client) // Alice(25), Bob(30), Charlie(35)

	// Ascending by name (default).
	users, err := client.User.Query().
		Order(user.ByName()).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 3)
	assert.Equal(t, "Alice", users[0].Name)
	assert.Equal(t, "Bob", users[1].Name)
	assert.Equal(t, "Charlie", users[2].Name)

	// Descending by age.
	users, err = client.User.Query().
		Order(user.ByAge(sql.OrderDesc())).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 3)
	assert.Equal(t, "Charlie", users[0].Name)
	assert.Equal(t, "Bob", users[1].Name)
	assert.Equal(t, "Alice", users[2].Name)
}

func TestE2E_OrderByID(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	seedUsers(t, client)

	// Descending by ID (reverse insertion order for autoincrement).
	users, err := client.User.Query().
		Order(user.ByID(sql.OrderDesc())).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 3)
	assert.Equal(t, "Charlie", users[0].Name)
	assert.Equal(t, "Bob", users[1].Name)
	assert.Equal(t, "Alice", users[2].Name)
}

func TestE2E_OrderByEnum(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Create users with different roles.
	_, err := client.User.Create().
		SetName("Admin").SetEmail("admin@test.com").SetAge(40).
		SetRole(user.RoleAdmin).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	require.NoError(t, err)

	_, err = client.User.Create().
		SetName("Guest").SetEmail("guest@test.com").SetAge(20).
		SetRole(user.RoleGuest).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	require.NoError(t, err)

	_, err = client.User.Create().
		SetName("Regular").SetEmail("regular@test.com").SetAge(30).
		SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	require.NoError(t, err)

	users, err := client.User.Query().
		Order(user.ByRole()).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 3)
	// Alphabetical: admin < guest < user.
	assert.Equal(t, user.RoleAdmin, users[0].Role)
	assert.Equal(t, user.RoleGuest, users[1].Role)
	assert.Equal(t, user.RoleUser, users[2].Role)
}

// =============================================================================
// Pagination
// =============================================================================

func TestE2E_Pagination(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	for i, name := range []string{"Alice", "Bob", "Charlie", "Diana", "Eve"} {
		createUser(t, client, name, name+"@test.com", 20+i*5)
	}

	// Limit.
	users, err := client.User.Query().
		Order(user.ByName()).
		Limit(2).
		All(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 2)
	assert.Equal(t, "Alice", users[0].Name)
	assert.Equal(t, "Bob", users[1].Name)

	// Offset.
	users, err = client.User.Query().
		Order(user.ByName()).
		Offset(2).
		Limit(2).
		All(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 2)
	assert.Equal(t, "Charlie", users[0].Name)
	assert.Equal(t, "Diana", users[1].Name)

	// Last page (partial).
	users, err = client.User.Query().
		Order(user.ByName()).
		Offset(4).
		Limit(2).
		All(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 1)
	assert.Equal(t, "Eve", users[0].Name)

	// Offset beyond count returns empty.
	users, err = client.User.Query().
		Order(user.ByName()).
		Offset(100).
		All(ctx)
	require.NoError(t, err)
	assert.Empty(t, users)
}

// =============================================================================
// Bulk Operations
// =============================================================================

func TestE2E_BulkCreate(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	users, err := client.User.CreateBulk(
		client.User.Create().SetName("Alice").SetEmail("alice@test.com").SetAge(25).SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now),
		client.User.Create().SetName("Bob").SetEmail("bob@test.com").SetAge(30).SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now),
		client.User.Create().SetName("Charlie").SetEmail("charlie@test.com").SetAge(35).SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now),
	).Save(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 3)

	for _, u := range users {
		assert.NotZero(t, u.ID)
	}

	// Verify all persisted.
	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

func TestE2E_BulkCreate_Tags(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tags, err := client.Tag.CreateBulk(
		client.Tag.Create().SetName("go"),
		client.Tag.Create().SetName("rust"),
		client.Tag.Create().SetName("python"),
		client.Tag.Create().SetName("java"),
		client.Tag.Create().SetName("typescript"),
	).Save(ctx)
	require.NoError(t, err)
	assert.Len(t, tags, 5)

	count, err := client.Tag.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}

// =============================================================================
// Constraints & Validation
// =============================================================================

func TestE2E_UniqueConstraint(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@test.com", 25)

	// Duplicate email should fail.
	_, err := client.User.Create().
		SetName("Bob").SetEmail("alice@test.com").SetAge(30).
		SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	assert.Error(t, err, "duplicate email should fail unique constraint")
}

func TestE2E_UniqueConstraint_DifferentEmail(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@test.com", 25)

	// Same name, different email is fine.
	u2, err := client.User.Create().
		SetName("Alice").SetEmail("alice2@test.com").SetAge(30).
		SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	assert.NotZero(t, u2.ID)
}

func TestE2E_ValidationError_MissingRequired(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Post requires title.
	_, err := client.Post.Create().
		SetContent("content only").
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	assert.Error(t, err)
	assert.True(t, velox.IsValidationError(err), "expected ValidationError, got: %T %v", err, err)
}

func TestE2E_ValidationError_MissingRequiredEdge(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Post requires author edge.
	_, err := client.Post.Create().
		SetTitle("Orphan").
		SetContent("No author").
		SetStatus(post.StatusDraft).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	assert.Error(t, err)
	assert.True(t, velox.IsValidationError(err), "expected ValidationError for missing author edge, got: %T %v", err, err)
}

func TestE2E_ValidationError_TagMissingName(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	_, err := client.Tag.Create().Save(ctx)
	assert.Error(t, err)
	assert.True(t, velox.IsValidationError(err), "expected ValidationError for missing name, got: %T %v", err, err)
}

// =============================================================================
// Transactions
// =============================================================================

func TestE2E_Transaction_Commit(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	u, err := tx.User.Create().
		SetName("TxUser").SetEmail("tx@test.com").SetAge(30).
		SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	assert.NotZero(t, u.ID)

	require.NoError(t, tx.Commit())

	// Verify user exists after commit.
	found, err := client.User.Query().Where(user.IDField.EQ(u.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "TxUser", found.Name)
}

func TestE2E_Transaction_Rollback(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	_, err = tx.User.Create().
		SetName("RollbackUser").SetEmail("rollback@test.com").SetAge(30).
		SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Rollback())

	// Verify user does NOT exist after rollback.
	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "user should not exist after rollback")
}

func TestE2E_Transaction_MultipleOps(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	// Create multiple entities in a transaction.
	_, err = tx.User.Create().
		SetName("TxUser1").SetEmail("tx1@test.com").SetAge(25).
		SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	_, err = tx.User.Create().
		SetName("TxUser2").SetEmail("tx2@test.com").SetAge(30).
		SetRole(user.RoleAdmin).SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	_, err = tx.Tag.Create().SetName("tx-tag").Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	// Verify all committed.
	userCount, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, userCount)

	tagCount, err := client.Tag.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, tagCount)
}

func TestE2E_Transaction_RollbackMultipleOps(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Pre-seed data outside transaction.
	createUser(t, client, "PreExisting", "pre@test.com", 40)

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	_, err = tx.User.Create().
		SetName("TxUser").SetEmail("tx@test.com").SetAge(25).
		SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	_, err = tx.Tag.Create().SetName("tx-tag").Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Rollback())

	// Pre-existing data should remain.
	userCount, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, userCount, "only pre-existing user should remain")

	tagCount, err := client.Tag.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, tagCount, "tx tag should be rolled back")
}

// =============================================================================
// Edge Loading
// =============================================================================

func TestE2E_EdgeNotLoaded(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@test.com", 25)

	u, err := client.User.Query().Only(ctx)
	require.NoError(t, err)

	// Accessing unloaded edge should error.
	_, err = u.Edges.PostsOrErr()
	assert.Error(t, err)
	assert.True(t, velox.IsNotLoaded(err), "expected NotLoadedError, got: %v", err)

	_, err = u.Edges.CommentsOrErr()
	assert.Error(t, err)
	assert.True(t, velox.IsNotLoaded(err), "expected NotLoadedError, got: %v", err)
}

func TestE2E_WithPosts_EagerLoad(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@test.com", 25)

	// Eager-load posts (should be empty but marked as loaded).
	u, err := client.User.Query().WithPosts().Only(ctx)
	require.NoError(t, err)

	posts, err := u.Edges.PostsOrErr()
	require.NoError(t, err)
	assert.Empty(t, posts)
}

func TestE2E_TagEdgeNotLoaded(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createTag(t, client, "golang")

	tg, err := client.Tag.Query().Only(ctx)
	require.NoError(t, err)

	_, err = tg.Edges.PostsOrErr()
	assert.Error(t, err)
	assert.True(t, velox.IsNotLoaded(err), "expected NotLoadedError for tag posts")
}

// =============================================================================
// Enum Operations
// =============================================================================

func TestE2E_EnumValues(t *testing.T) {
	roles := entity.UserRoleValues()
	assert.Len(t, roles, 3)
	assert.Contains(t, roles, entity.UserRoleAdmin)
	assert.Contains(t, roles, entity.UserRoleUser)
	assert.Contains(t, roles, entity.UserRoleGuest)
}

func TestE2E_EnumIsValid(t *testing.T) {
	assert.True(t, entity.UserRoleAdmin.IsValid())
	assert.True(t, entity.UserRoleUser.IsValid())
	assert.True(t, entity.UserRoleGuest.IsValid())
	assert.False(t, entity.UserRole("invalid").IsValid())
	assert.False(t, entity.UserRole("").IsValid())
}

func TestE2E_EnumString(t *testing.T) {
	assert.Equal(t, "admin", entity.UserRoleAdmin.String())
	assert.Equal(t, "user", entity.UserRoleUser.String())
	assert.Equal(t, "guest", entity.UserRoleGuest.String())
}

func TestE2E_EnumFilter(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Create users with different roles.
	createUser(t, client, "Alice", "alice@test.com", 25)

	_, err := client.User.Create().
		SetName("Admin").SetEmail("admin@test.com").SetAge(35).
		SetRole(user.RoleAdmin).SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.User.Create().
		SetName("Guest").SetEmail("guest@test.com").SetAge(20).
		SetRole(user.RoleGuest).SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// Filter by enum IN.
	users, err := client.User.Query().
		Where(user.RoleField.In(user.RoleAdmin, user.RoleGuest)).
		Order(user.ByRole()).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, user.RoleAdmin, users[0].Role)
	assert.Equal(t, user.RoleGuest, users[1].Role)

	// Filter by enum NEQ.
	users, err = client.User.Query().
		Where(user.RoleField.NEQ(user.RoleUser)).
		Order(user.ByRole()).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
}

func TestE2E_PostStatusEnum(t *testing.T) {
	statuses := entity.PostStatusValues()
	assert.Len(t, statuses, 3)
	assert.Contains(t, statuses, entity.PostStatusDraft)
	assert.Contains(t, statuses, entity.PostStatusPublished)
	assert.Contains(t, statuses, entity.PostStatusArchived)

	for _, s := range statuses {
		assert.True(t, s.IsValid())
	}
	assert.False(t, entity.PostStatus("invalid").IsValid())
}

// =============================================================================
// Entity Stringer
// =============================================================================

func TestE2E_EntityString(t *testing.T) {
	u := &entity.User{
		ID:    1,
		Name:  "Alice",
		Email: "alice@test.com",
		Age:   25,
		Role:  entity.UserRoleUser,
	}
	s := u.String()
	assert.Contains(t, s, "User(")
	assert.Contains(t, s, "name=Alice")
	assert.Contains(t, s, "email=alice@test.com")
	assert.Contains(t, s, "age=25")
	assert.Contains(t, s, "role=user")
}

func TestE2E_TagString(t *testing.T) {
	tg := &entity.Tag{ID: 1, Name: "golang"}
	s := tg.String()
	assert.Contains(t, s, "Tag(")
	assert.Contains(t, s, "name=golang")
}

// =============================================================================
// Entity Unwrap
// =============================================================================

// TestE2E_UserUnwrap_PanicsOnNonTx documents the Ent-parity contract: Unwrap
// only makes sense for entities produced inside a transaction, so calling it
// on a bare entity panics. Real usage appears in TestE2E_TxUnwrap_AllowsPostCommitEdgeRead.
func TestE2E_UserUnwrap_PanicsOnNonTx(t *testing.T) {
	u := &entity.User{ID: 1, Name: "Alice"}
	assert.Panics(t, func() { u.Unwrap() })
}

func TestE2E_TagUnwrap_PanicsOnNonTx(t *testing.T) {
	tg := &entity.Tag{ID: 1, Name: "golang"}
	assert.Panics(t, func() { tg.Unwrap() })
}

// TestE2E_TxUnwrap_AllowsPostCommitEdgeRead pins the core contract:
// after Unwrap(), the tx-returned entity can be used for edge reads without
// "sql: transaction has already been committed". Without Unwrap(), the same
// QueryPosts call fails because e.config.Driver still points at the done *txDriver.
func TestE2E_TxUnwrap_AllowsPostCommitEdgeRead(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	u, err := tx.User.Create().
		SetName("TxUnwrap").SetEmail("txu@test.com").SetAge(31).
		SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	// Without Unwrap, the edge query would panic / error because u.config.Driver
	// is the committed *txDriver. Unwrap swaps it to the base driver.
	_, err = u.Unwrap().QueryPosts().All(ctx)
	require.NoError(t, err)
}

// TestE2E_TxUnwrap_WithoutUnwrap_FailsAfterCommit documents the failure mode
// the contract prevents. It's the inverse guardrail for the test above.
func TestE2E_TxUnwrap_WithoutUnwrap_FailsAfterCommit(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	u, err := tx.User.Create().
		SetName("NoUnwrap").SetEmail("nou@test.com").SetAge(32).
		SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	_, err = u.QueryPosts().All(ctx)
	require.Error(t, err, "reading via committed tx driver must fail without Unwrap")
}

// =============================================================================
// Select / Projection
// =============================================================================

func TestE2E_Select(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@test.com", 25)

	// Select only specific columns.
	users, err := client.User.Query().
		Select(user.FieldName, user.FieldEmail).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "Alice", users[0].Name)
	assert.Equal(t, "alice@test.com", users[0].Email)
	// Non-selected fields should be zero values.
	assert.Equal(t, 0, users[0].Age)
}

// =============================================================================
// Query Clone
// =============================================================================

func TestE2E_QueryClone(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	seedUsers(t, client)

	// Clone a query and modify the clone.
	base := client.User.Query().Where(user.AgeField.GTE(25))
	clone := base.Clone().Where(user.AgeField.LTE(30))

	// Original should return all 3 (25, 30, 35 all >= 25).
	all, err := base.Order(user.ByAge()).All(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 3)

	// Clone should return 2 (25 <= age <= 30).
	filtered, err := clone.Order(user.ByAge()).All(ctx)
	require.NoError(t, err)
	assert.Len(t, filtered, 2)
	assert.Equal(t, "Alice", filtered[0].Name)
	assert.Equal(t, "Bob", filtered[1].Name)
}

// =============================================================================
// NillableAge / SetNillableAge
// =============================================================================

func TestE2E_SetNillableAge(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Alice", "alice@test.com", 25)

	// SetNillableAge with nil should not change the age.
	updated, err := client.User.UpdateOne(u).
		SetNillableAge(nil).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 25, updated.Age)

	// SetNillableAge with value should update.
	age := 30
	updated, err = client.User.UpdateOne(updated).
		SetNillableAge(&age).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 30, updated.Age)
}

// =============================================================================
// SkipDefaults (Velox Extension)
// =============================================================================

func TestE2E_SkipDefaults(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Alice", "alice@test.com", 25)
	originalUpdatedAt := u.UpdatedAt

	// Normal update should auto-set updated_at via UpdateDefault.
	updated, err := client.User.UpdateOne(u).
		SetName("Alice Updated").
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Alice Updated", updated.Name)
	// updated_at may have changed due to UpdateDefault(time.Now).

	// SkipDefaults should preserve original updated_at if we explicitly set it.
	updated, err = client.User.UpdateOne(updated).
		SetName("Alice SkipDefaults").
		SetUpdatedAt(originalUpdatedAt).
		SkipDefaults().
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Alice SkipDefaults", updated.Name)
	assert.Equal(t, originalUpdatedAt.Unix(), updated.UpdatedAt.Unix())
}

func TestE2E_SkipDefaultField(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Alice", "alice@test.com", 25)

	// SkipDefault for specific field.
	updated, err := client.User.UpdateOne(u).
		SetName("Alice SkipDefault").
		SetUpdatedAt(now).
		SkipDefault(user.FieldUpdatedAt).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Alice SkipDefault", updated.Name)
	assert.Equal(t, now.Unix(), updated.UpdatedAt.Unix())
}

// =============================================================================
// Edge HasPosts / HasComments (without FK - just checks edge existence)
// =============================================================================

func TestE2E_HasEdge_NoRelatedEntities(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@test.com", 25)

	// HasPosts() should return false since no FK relations are materialized.
	users, err := client.User.Query().
		Where(user.HasPosts()).
		All(ctx)
	require.NoError(t, err)
	assert.Empty(t, users, "user without posts should not match HasPosts()")
}

// =============================================================================
// ValidColumn
// =============================================================================

func TestE2E_ValidColumn(t *testing.T) {
	assert.True(t, user.ValidColumn("id"))
	assert.True(t, user.ValidColumn("name"))
	assert.True(t, user.ValidColumn("email"))
	assert.True(t, user.ValidColumn("age"))
	assert.True(t, user.ValidColumn("role"))
	assert.True(t, user.ValidColumn("created_at"))
	assert.True(t, user.ValidColumn("updated_at"))
	assert.False(t, user.ValidColumn("nonexistent"))
	assert.False(t, user.ValidColumn(""))

	assert.True(t, tag.ValidColumn("id"))
	assert.True(t, tag.ValidColumn("name"))
	assert.False(t, tag.ValidColumn("description"))
}

// =============================================================================
// Constants & Package Metadata
// =============================================================================

func TestE2E_PackageConstants(t *testing.T) {
	// User package.
	assert.Equal(t, "users", user.Table)
	assert.Equal(t, "user", user.Label)
	assert.Equal(t, "id", user.FieldID)
	assert.Equal(t, "name", user.FieldName)
	assert.Equal(t, "email", user.FieldEmail)
	assert.Equal(t, "age", user.FieldAge)
	assert.Equal(t, "role", user.FieldRole)
	assert.Equal(t, "posts", user.EdgePosts)
	assert.Equal(t, "comments", user.EdgeComments)

	// Tag package.
	assert.Equal(t, "tags", tag.Table)
	assert.Equal(t, "tag", tag.Label)
	assert.Equal(t, "id", tag.FieldID)
	assert.Equal(t, "name", tag.FieldName)
	assert.Equal(t, "posts", tag.EdgePosts)

	// Post package.
	assert.Equal(t, "posts", post.Table)
	assert.Equal(t, "post", post.Label)
	assert.Equal(t, "title", post.FieldTitle)
	assert.Equal(t, "content", post.FieldContent)
	assert.Equal(t, "status", post.FieldStatus)
	assert.Equal(t, "view_count", post.FieldViewCount)
}

// =============================================================================
// Post with Author (Skipped: edge-based FK columns not yet materialized)
// =============================================================================

func TestE2E_PostWithAuthor(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	author := createUser(t, client, "Alice", "alice@test.com", 25)

	p, err := client.Post.Create().
		SetTitle("Hello World").
		SetContent("First post!").
		SetStatus(post.StatusDraft).
		SetViewCount(0).
		SetAuthorID(author.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Hello World", p.Title)
	assert.Equal(t, "First post!", p.Content)

	posts, err := client.Post.Query().
		Where(post.HasAuthorWith(user.IDField.EQ(author.ID))).
		All(ctx)
	require.NoError(t, err)
	assert.Len(t, posts, 1)
	assert.Equal(t, "Hello World", posts[0].Title)
}

func TestE2E_ForeignKeyConstraint(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	_, err := client.Post.Create().
		SetTitle("Orphan Post").
		SetContent("No author").
		SetStatus(post.StatusDraft).
		SetViewCount(0).
		SetAuthorID(99999).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	assert.Error(t, err, "FK constraint should prevent orphan post")
}

// =============================================================================
// MaskNotFound
// =============================================================================

func TestE2E_MaskNotFound(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	_, err := client.User.Query().
		Where(user.IDField.EQ(99999)).
		Only(ctx)
	require.Error(t, err)

	// MaskNotFound should convert NotFoundError to nil.
	assert.NoError(t, velox.MaskNotFound(err))

	// Non-NotFound errors should pass through.
	other := assert.AnError
	assert.Equal(t, other, velox.MaskNotFound(other))
}

// =============================================================================
// Timestamps
// =============================================================================

func TestE2E_Timestamps(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Alice", "alice@test.com", 25)
	assert.Equal(t, now.Unix(), u.CreatedAt.Unix())
	assert.Equal(t, now.Unix(), u.UpdatedAt.Unix())

	// Verify timestamps survive round-trip.
	found, err := client.User.Query().Where(user.IDField.EQ(u.ID)).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, now.Unix(), found.CreatedAt.Unix())
	assert.Equal(t, now.Unix(), found.UpdatedAt.Unix())
}

// =============================================================================
// Multiple Clients
// =============================================================================

func TestE2E_MultipleClients_Isolated(t *testing.T) {
	// Each client gets its own in-memory database.
	client1 := openTestClient(t)
	client2 := openTestClient(t)
	ctx := context.Background()

	createUser(t, client1, "Client1User", "c1@test.com", 25)

	// Client2 should not see client1's data.
	count, err := client2.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// =============================================================================
// IsNode Interface
// =============================================================================

func TestE2E_IsNode(t *testing.T) {
	u := &entity.User{}
	u.IsNode()

	tg := &entity.Tag{}
	tg.IsNode()
}

// =============================================================================
// Columns Slice
// =============================================================================

func TestE2E_ColumnsSlice(t *testing.T) {
	assert.Equal(t, []string{"id", "created_at", "updated_at", "name", "email", "age", "role"}, user.Columns)
	assert.Equal(t, []string{"id", "name"}, tag.Columns)
}

// =============================================================================
// Edge Tests — O2M (User → Posts, User → Comments)
// =============================================================================

// createPost is a helper that creates a post with required fields.
func createPost(t *testing.T, client *velox.Client, title, content string, authorID int) *entity.Post {
	t.Helper()
	p, err := client.Post.Create().
		SetTitle(title).
		SetContent(content).
		SetStatus(post.StatusDraft).
		SetViewCount(0).
		SetAuthorID(authorID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(context.Background())
	require.NoError(t, err)
	return p
}

// createComment is a helper that creates a comment with required fields.
func createComment(t *testing.T, client *velox.Client, content string, postID, authorID int) *entity.Comment {
	t.Helper()
	c, err := client.Comment.Create().
		SetContent(content).
		SetPostID(postID).
		SetAuthorID(authorID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(context.Background())
	require.NoError(t, err)
	return c
}

func TestE2E_Edge_O2M_UserPosts(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	bob := createUser(t, client, "Bob", "bob@test.com", 30)

	p1 := createPost(t, client, "Alice Post 1", "Content 1", alice.ID)
	p2 := createPost(t, client, "Alice Post 2", "Content 2", alice.ID)
	_ = createPost(t, client, "Bob Post 1", "Bob content", bob.ID)

	// Eager-load Alice's posts.
	u, err := client.User.Query().
		Where(user.IDField.EQ(alice.ID)).
		WithPosts().
		Only(ctx)
	require.NoError(t, err)

	posts, err := u.Edges.PostsOrErr()
	require.NoError(t, err)
	assert.Len(t, posts, 2)

	titles := []string{posts[0].Title, posts[1].Title}
	assert.Contains(t, titles, p1.Title)
	assert.Contains(t, titles, p2.Title)

	// Bob should have 1 post.
	u2, err := client.User.Query().
		Where(user.IDField.EQ(bob.ID)).
		WithPosts().
		Only(ctx)
	require.NoError(t, err)
	bobPosts, err := u2.Edges.PostsOrErr()
	require.NoError(t, err)
	assert.Len(t, bobPosts, 1)
}

func TestE2E_Edge_O2M_UserComments(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	p := createPost(t, client, "Post", "Content", alice.ID)
	createComment(t, client, "Comment 1", p.ID, alice.ID)
	createComment(t, client, "Comment 2", p.ID, alice.ID)

	u, err := client.User.Query().
		Where(user.IDField.EQ(alice.ID)).
		WithComments().
		Only(ctx)
	require.NoError(t, err)

	comments, err := u.Edges.CommentsOrErr()
	require.NoError(t, err)
	assert.Len(t, comments, 2)
}

// =============================================================================
// Edge Tests — M2O (Post → Author, Comment → Post, Comment → Author)
// =============================================================================

func TestE2E_Edge_M2O_PostAuthor(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	createPost(t, client, "My Post", "Content", alice.ID)

	// Query post with eager-loaded author.
	p, err := client.Post.Query().
		WithAuthor().
		Only(ctx)
	require.NoError(t, err)

	author, err := p.Edges.AuthorOrErr()
	require.NoError(t, err)
	require.NotNil(t, author)
	assert.Equal(t, "Alice", author.Name)
	assert.Equal(t, alice.ID, author.ID)
}

func TestE2E_Edge_M2O_CommentEdges(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	p := createPost(t, client, "Post", "Content", alice.ID)
	createComment(t, client, "Great post!", p.ID, alice.ID)

	// Query comment with both M2O edges.
	c, err := client.Comment.Query().
		WithPost().
		WithAuthor().
		Only(ctx)
	require.NoError(t, err)

	cPost, err := c.Edges.PostOrErr()
	require.NoError(t, err)
	require.NotNil(t, cPost)
	assert.Equal(t, "Post", cPost.Title)

	cAuthor, err := c.Edges.AuthorOrErr()
	require.NoError(t, err)
	require.NotNil(t, cAuthor)
	assert.Equal(t, "Alice", cAuthor.Name)
}

// =============================================================================
// Edge Tests — M2M (Post ↔ Tag)
// =============================================================================

func TestE2E_Edge_M2M_PostTags(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	tGo := createTag(t, client, "golang")
	tRust := createTag(t, client, "rust")

	// Create post with tags via AddTagIDs.
	p, err := client.Post.Create().
		SetTitle("Go vs Rust").
		SetContent("Comparison").
		SetStatus(post.StatusPublished).
		SetViewCount(0).
		SetAuthorID(alice.ID).
		AddTagIDs(tGo.ID, tRust.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// Eager-load tags on the post.
	loaded, err := client.Post.Query().
		Where(post.IDField.EQ(p.ID)).
		WithTags().
		Only(ctx)
	require.NoError(t, err)

	tags, err := loaded.Edges.TagsOrErr()
	require.NoError(t, err)
	assert.Len(t, tags, 2)

	tagNames := []string{tags[0].Name, tags[1].Name}
	assert.Contains(t, tagNames, "golang")
	assert.Contains(t, tagNames, "rust")
}

func TestE2E_Edge_M2M_TagPosts(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	tGo := createTag(t, client, "golang")

	// Create two posts with the same tag.
	createPostWithTags := func(title string, tagIDs ...int) {
		_, err := client.Post.Create().
			SetTitle(title).
			SetContent("Content").
			SetStatus(post.StatusDraft).
			SetViewCount(0).
			SetAuthorID(alice.ID).
			AddTagIDs(tagIDs...).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}
	createPostWithTags("Post A", tGo.ID)
	createPostWithTags("Post B", tGo.ID)

	// Query tag with inverse M2M edge (posts).
	loaded, err := client.Tag.Query().
		Where(tag.IDField.EQ(tGo.ID)).
		WithPosts().
		Only(ctx)
	require.NoError(t, err)

	posts, err := loaded.Edges.PostsOrErr()
	require.NoError(t, err)
	assert.Len(t, posts, 2)
}

// =============================================================================
// Edge Tests — Nested Eager Loading
// =============================================================================

func TestE2E_Edge_NestedEagerLoad(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	p := createPost(t, client, "My Post", "Content", alice.ID)
	createComment(t, client, "Nice!", p.ID, alice.ID)
	createComment(t, client, "Thanks!", p.ID, alice.ID)

	// Eager-load user → posts → comments (nested).
	u, err := client.User.Query().
		Where(user.IDField.EQ(alice.ID)).
		WithPosts(func(pq entity.PostQuerier) {
			pq.WithComments()
		}).
		Only(ctx)
	require.NoError(t, err)

	posts, err := u.Edges.PostsOrErr()
	require.NoError(t, err)
	require.Len(t, posts, 1)

	comments, err := posts[0].Edges.CommentsOrErr()
	require.NoError(t, err)
	assert.Len(t, comments, 2)
}

func TestE2E_Edge_NestedEagerLoad_PostAuthorAndTags(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	tGo := createTag(t, client, "golang")

	_, err := client.Post.Create().
		SetTitle("Go Tips").
		SetContent("Tips content").
		SetStatus(post.StatusPublished).
		SetViewCount(10).
		SetAuthorID(alice.ID).
		AddTagIDs(tGo.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// Load post with both author and tags.
	p, err := client.Post.Query().
		WithAuthor().
		WithTags().
		Only(ctx)
	require.NoError(t, err)

	author, err := p.Edges.AuthorOrErr()
	require.NoError(t, err)
	assert.Equal(t, "Alice", author.Name)

	tags, err := p.Edges.TagsOrErr()
	require.NoError(t, err)
	assert.Len(t, tags, 1)
	assert.Equal(t, "golang", tags[0].Name)
}

// =============================================================================
// Edge Tests — Edge Traversal Queries (QueryPosts, QueryComments)
// =============================================================================

func TestE2E_Edge_Traversal_UserQueryPosts(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	createPost(t, client, "Draft", "d", alice.ID)

	p2, err := client.Post.Create().
		SetTitle("Published").
		SetContent("p").
		SetStatus(post.StatusPublished).
		SetViewCount(5).
		SetAuthorID(alice.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// Traverse from user entity to posts via QueryPosts().
	u, err := client.User.Get(ctx, alice.ID)
	require.NoError(t, err)

	posts, err := u.QueryPosts().
		Where(post.StatusField.EQ(post.StatusPublished)).
		All(ctx)
	require.NoError(t, err)
	assert.Len(t, posts, 1)
	assert.Equal(t, p2.ID, posts[0].ID)
}

func TestE2E_Edge_Traversal_PostQueryComments(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	p := createPost(t, client, "Post", "Content", alice.ID)
	createComment(t, client, "First", p.ID, alice.ID)
	createComment(t, client, "Second", p.ID, alice.ID)

	// Get post and traverse to comments.
	loaded, err := client.Post.Get(ctx, p.ID)
	require.NoError(t, err)

	comments, err := loaded.QueryComments().All(ctx)
	require.NoError(t, err)
	assert.Len(t, comments, 2)
}

func TestE2E_Edge_Traversal_PostQueryTags(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	tGo := createTag(t, client, "golang")
	tWeb := createTag(t, client, "web")

	_, err := client.Post.Create().
		SetTitle("Go Web").
		SetContent("Content").
		SetStatus(post.StatusDraft).
		SetViewCount(0).
		SetAuthorID(alice.ID).
		AddTagIDs(tGo.ID, tWeb.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	p, err := client.Post.Query().Only(ctx)
	require.NoError(t, err)

	tags, err := p.QueryTags().All(ctx)
	require.NoError(t, err)
	assert.Len(t, tags, 2)
}

func TestE2E_Edge_Traversal_CommentQueryAuthor(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	p := createPost(t, client, "Post", "Content", alice.ID)
	createComment(t, client, "Hello", p.ID, alice.ID)

	c, err := client.Comment.Query().Only(ctx)
	require.NoError(t, err)

	author, err := c.QueryAuthor().Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Alice", author.Name)
}

// =============================================================================
// Edge Tests — Update Edge Operations (AddIDs, RemoveIDs, Clear)
// =============================================================================

func TestE2E_Edge_Update_AddTagIDs(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	tGo := createTag(t, client, "golang")
	tWeb := createTag(t, client, "web")

	// Create post without tags.
	p := createPost(t, client, "Post", "Content", alice.ID)

	// Add tags via update.
	_, err := client.Post.UpdateOneID(p.ID).
		AddTagIDs(tGo.ID, tWeb.ID).
		Save(ctx)
	require.NoError(t, err)

	// Verify tags were added.
	loaded, err := client.Post.Query().
		Where(post.IDField.EQ(p.ID)).
		WithTags().
		Only(ctx)
	require.NoError(t, err)

	tags, err := loaded.Edges.TagsOrErr()
	require.NoError(t, err)
	assert.Len(t, tags, 2)
}

func TestE2E_Edge_Update_RemoveTagIDs(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	tGo := createTag(t, client, "golang")
	tWeb := createTag(t, client, "web")
	tRust := createTag(t, client, "rust")

	// Create post with 3 tags.
	p, err := client.Post.Create().
		SetTitle("Multi-tag").
		SetContent("Content").
		SetStatus(post.StatusDraft).
		SetViewCount(0).
		SetAuthorID(alice.ID).
		AddTagIDs(tGo.ID, tWeb.ID, tRust.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// Remove one tag.
	_, err = client.Post.UpdateOneID(p.ID).
		RemoveTagIDs(tWeb.ID).
		Save(ctx)
	require.NoError(t, err)

	// Verify only 2 tags remain.
	loaded, err := client.Post.Query().
		Where(post.IDField.EQ(p.ID)).
		WithTags().
		Only(ctx)
	require.NoError(t, err)

	tags, err := loaded.Edges.TagsOrErr()
	require.NoError(t, err)
	assert.Len(t, tags, 2)

	tagNames := []string{tags[0].Name, tags[1].Name}
	assert.Contains(t, tagNames, "golang")
	assert.Contains(t, tagNames, "rust")
	assert.NotContains(t, tagNames, "web")
}

func TestE2E_Edge_Update_ClearTags(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	tGo := createTag(t, client, "golang")
	tWeb := createTag(t, client, "web")

	p, err := client.Post.Create().
		SetTitle("Tagged").
		SetContent("Content").
		SetStatus(post.StatusDraft).
		SetViewCount(0).
		SetAuthorID(alice.ID).
		AddTagIDs(tGo.ID, tWeb.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// Clear all tags.
	_, err = client.Post.UpdateOneID(p.ID).
		ClearTags().
		Save(ctx)
	require.NoError(t, err)

	// Verify no tags.
	loaded, err := client.Post.Query().
		Where(post.IDField.EQ(p.ID)).
		WithTags().
		Only(ctx)
	require.NoError(t, err)

	tags, err := loaded.Edges.TagsOrErr()
	require.NoError(t, err)
	assert.Empty(t, tags)
}

func TestE2E_Edge_Update_ClearAndReplace(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	tGo := createTag(t, client, "golang")
	tRust := createTag(t, client, "rust")

	p, err := client.Post.Create().
		SetTitle("Replace tags").
		SetContent("Content").
		SetStatus(post.StatusDraft).
		SetViewCount(0).
		SetAuthorID(alice.ID).
		AddTagIDs(tGo.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// Clear existing tags and add new ones in a single update.
	_, err = client.Post.UpdateOneID(p.ID).
		ClearTags().
		AddTagIDs(tRust.ID).
		Save(ctx)
	require.NoError(t, err)

	loaded, err := client.Post.Query().
		Where(post.IDField.EQ(p.ID)).
		WithTags().
		Only(ctx)
	require.NoError(t, err)

	tags, err := loaded.Edges.TagsOrErr()
	require.NoError(t, err)
	assert.Len(t, tags, 1)
	assert.Equal(t, "rust", tags[0].Name)
}

// =============================================================================
// Edge Tests — O2M Edge Update Operations (owner side)
// =============================================================================

func TestE2E_Edge_O2M_Update_ClearPosts_RequiredFK(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	createPost(t, client, "Post 1", "c1", alice.ID)

	// Post.author is Required() — FK is NOT NULL.
	// ClearPosts tries SET user_posts = NULL, which violates the constraint.
	// This is expected behavior — you can't disassociate a required edge.
	_, err := client.User.UpdateOneID(alice.ID).ClearPosts().Save(ctx)
	assert.Error(t, err, "ClearPosts should fail when FK is NOT NULL (required edge)")
}

func TestE2E_Edge_O2M_Update_AddPostIDs(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	bob := createUser(t, client, "Bob", "bob@test.com", 30)

	// Create posts owned by Bob.
	p1 := createPost(t, client, "Post 1", "c1", bob.ID)
	p2 := createPost(t, client, "Post 2", "c2", bob.ID)

	// Verify Bob has 2 posts.
	u, err := client.User.Get(ctx, bob.ID)
	require.NoError(t, err)
	count, err := u.QueryPosts().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Verify Alice has 0 posts.
	u, err = client.User.Get(ctx, alice.ID)
	require.NoError(t, err)
	count, err = u.QueryPosts().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Transfer p1 to Alice: first clear Bob's FK, then set Alice's.
	// Since Post.author is Required (NOT NULL), we reassign via the M2O side.
	_, err = client.Post.UpdateOneID(p1.ID).SetAuthorID(alice.ID).Save(ctx)
	require.NoError(t, err)

	// Verify Alice now has 1 post.
	count, err = u.QueryPosts().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify Bob has 1 post remaining.
	u2, err := client.User.Get(ctx, bob.ID)
	require.NoError(t, err)
	count, err = u2.QueryPosts().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify p2 is still Bob's.
	posts, err := u2.QueryPosts().All(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 1)
	assert.Equal(t, p2.ID, posts[0].ID)
}

// =============================================================================
// Edge Tests — HasEdge Predicates with Data
// =============================================================================

func TestE2E_Edge_HasPosts_WithData(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	bob := createUser(t, client, "Bob", "bob@test.com", 30)
	createPost(t, client, "Alice's Post", "Content", alice.ID)

	// Alice has posts, Bob doesn't.
	withPosts, err := client.User.Query().
		Where(user.HasPosts()).
		All(ctx)
	require.NoError(t, err)
	assert.Len(t, withPosts, 1)
	assert.Equal(t, "Alice", withPosts[0].Name)

	// Bob should not appear.
	_ = bob
}

func TestE2E_Edge_HasPostsWith_Predicate(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	createPost(t, client, "Draft", "d", alice.ID)

	_, err := client.Post.Create().
		SetTitle("Published").
		SetContent("p").
		SetStatus(post.StatusPublished).
		SetViewCount(0).
		SetAuthorID(alice.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// Find users with published posts.
	users, err := client.User.Query().
		Where(user.HasPostsWith(post.StatusField.EQ(post.StatusPublished))).
		All(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 1)
	assert.Equal(t, "Alice", users[0].Name)
}

func TestE2E_Edge_HasTagsWith(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	tGo := createTag(t, client, "golang")
	tRust := createTag(t, client, "rust")

	// Post A has "golang" tag.
	_, err := client.Post.Create().
		SetTitle("Go Post").
		SetContent("Go content").
		SetStatus(post.StatusDraft).
		SetViewCount(0).
		SetAuthorID(alice.ID).
		AddTagIDs(tGo.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// Post B has "rust" tag.
	_, err = client.Post.Create().
		SetTitle("Rust Post").
		SetContent("Rust content").
		SetStatus(post.StatusDraft).
		SetViewCount(0).
		SetAuthorID(alice.ID).
		AddTagIDs(tRust.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// Find posts tagged "golang".
	goPosts, err := client.Post.Query().
		Where(post.HasTagsWith(tag.NameField.EQ("golang"))).
		All(ctx)
	require.NoError(t, err)
	assert.Len(t, goPosts, 1)
	assert.Equal(t, "Go Post", goPosts[0].Title)
}

func TestE2E_Edge_HasCommentsWith(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	bob := createUser(t, client, "Bob", "bob@test.com", 30)
	p := createPost(t, client, "Post", "Content", alice.ID)
	createComment(t, client, "Alice's comment", p.ID, alice.ID)
	createComment(t, client, "Bob's comment", p.ID, bob.ID)

	// Find posts that have comments by Bob.
	posts, err := client.Post.Query().
		Where(post.HasCommentsWith(
			comment.HasAuthorWith(user.NameField.EQ("Bob")),
		)).
		All(ctx)
	require.NoError(t, err)
	assert.Len(t, posts, 1)
}

// =============================================================================
// Edge Tests — Client-Level Edge Queries
// =============================================================================

func TestE2E_Edge_ClientQueryPosts(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	createPost(t, client, "Post 1", "c1", alice.ID)
	createPost(t, client, "Post 2", "c2", alice.ID)

	// Use client-level edge query.
	u, err := client.User.Get(ctx, alice.ID)
	require.NoError(t, err)

	posts, err := client.User.QueryPosts(u).All(ctx)
	require.NoError(t, err)
	assert.Len(t, posts, 2)
}

// =============================================================================
// Edge Tests — Count Through Edges
// =============================================================================

func TestE2E_Edge_CountPosts(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	createPost(t, client, "Post 1", "c1", alice.ID)
	createPost(t, client, "Post 2", "c2", alice.ID)
	createPost(t, client, "Post 3", "c3", alice.ID)

	u, err := client.User.Get(ctx, alice.ID)
	require.NoError(t, err)

	count, err := u.QueryPosts().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

func TestE2E_Edge_CountTags(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@test.com", 25)
	tGo := createTag(t, client, "golang")
	tWeb := createTag(t, client, "web")

	_, err := client.Post.Create().
		SetTitle("Tagged Post").
		SetContent("Content").
		SetStatus(post.StatusDraft).
		SetViewCount(0).
		SetAuthorID(alice.ID).
		AddTagIDs(tGo.ID, tWeb.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	p, err := client.Post.Query().Only(ctx)
	require.NoError(t, err)

	tagCount, err := p.QueryTags().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, tagCount)
}
