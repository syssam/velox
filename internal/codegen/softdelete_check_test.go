package codegen

import (
	"strings"
	"testing"

	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin/softdelete"
)

// sdAccount soft-deletes its rows and has a unique key of each kind: a
// Unique field, a full unique index, a partial one, one over deleted_at,
// and a one-to-one edge to sdProfile.
type sdAccount struct{ schema.Schema }

func (sdAccount) Mixin() []schema.Mixin { return []schema.Mixin{softdelete.Mixin{}} }
func (sdAccount) Fields() []schema.Field {
	return []schema.Field{
		field.String("email").Unique(),
		field.String("handle"),
		field.String("slug"),
		field.String("code"),
		field.String("token").Unique(),
	}
}
func (sdAccount) Edges() []schema.Edge {
	return []schema.Edge{edge.To("profile", sdProfile.Type).Unique()}
}
func (sdAccount) Indexes() []schema.Index {
	return []schema.Index{
		index.Fields("handle").Unique(),
		index.Fields("slug").Unique().Where("deleted_at IS NULL"),
		index.Fields("code", "deleted_at").Unique(),
		index.Fields("token").Unique().StorageKey("sd_accounts_token_key"),
	}
}

// sdProfile is the other end of sdAccount's one-to-one edge; it holds the
// key, and soft-deletes too.
type sdProfile struct{ schema.Schema }

func (sdProfile) Mixin() []schema.Mixin { return []schema.Mixin{softdelete.Mixin{}} }
func (sdProfile) Edges() []schema.Edge {
	return []schema.Edge{edge.From("account", sdAccount.Type).Ref("profile").Unique()}
}

// plainAccount has the same unique field without soft delete.
type plainAccount struct{ schema.Schema }

func (plainAccount) Fields() []schema.Field { return []schema.Field{field.String("email").Unique()} }

// A soft-deleted row keeps its unique values, so velox-gen warns of every
// unique key over every row of an entity with softdelete.Mixin: a Unique
// field, a one-to-one edge's key and a full unique index. A partial index,
// one over deleted_at and an entity without the mixin get none, and a
// Unique field's own index, declared by its name, is warned of once.
func TestSoftDeleteWarnings(t *testing.T) {
	g := mustBuild(t, sdAccount{}, sdProfile{}, plainAccount{})
	got := SoftDeleteWarnings(g)
	want := []string{
		`schema sdAccount: field "email": `,
		`schema sdAccount: field "token": `,
		`schema sdAccount: index 0: `,
		`schema sdProfile: field "sd_account_profile": `,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d warnings, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(got[i], w) || !strings.Contains(got[i], "soft") {
			t.Errorf("warning %d: got %q, want it to contain %q", i, got[i], w)
		}
	}
	if !strings.Contains(got[0], `index.Fields("email").Unique().Where("deleted_at IS NULL")`) {
		t.Errorf("a field's warning spells its partial index: %q", got[0])
	}
	if !strings.Contains(got[2], `.Where("deleted_at IS NULL")`) {
		t.Errorf("an index's warning spells its Where: %q", got[2])
	}
	if !strings.Contains(got[3], "softdelete.Skip") {
		t.Errorf("an edge key's warning names softdelete.Skip: %q", got[3])
	}
}
