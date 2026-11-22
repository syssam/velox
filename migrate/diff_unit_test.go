package migrate_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// keyTables is orgs and a table name whose foreign key org_id to orgs has
// the indexes ix.
func keyTables(name string, ix ...migrate.Index) []migrate.Table {
	return []migrate.Table{
		{Name: "orgs", Columns: []migrate.Column{{Name: "id", Type: "bigint", Identity: true}}, PrimaryKey: []string{"id"}},
		{
			Name:       name,
			Columns:    []migrate.Column{{Name: "id", Type: "bigint", Identity: true}, {Name: "org_id", Type: "bigint"}},
			PrimaryKey: []string{"id"},
			Indexes:    ix,
			ForeignKeys: []migrate.ForeignKey{{
				Name: "users_org", Columns: []string{"org_id"}, RefTable: migrate.TableRef{Name: "orgs"}, RefColumns: []string{"id"},
			}},
		},
	}
}

// TestDiffDropsKeyIndexEarly: on MySQL, a plan that drops the last index of
// a foreign key's columns drops the key first, then the index, ahead of the
// other steps, and adds the key back after; MySQL refuses to drop a key's
// last index (1553). A renamed table's index is dropped under its name in
// from, before the rename.
func TestDiffDropsKeyIndexEarly(t *testing.T) {
	t.Parallel()
	byKey := migrate.Index{Name: "users_org_id", Columns: []string{"org_id"}}
	from := &migrate.Schema{Dialect: velox.MySQL, Tables: keyTables("users", byKey)}
	to := &migrate.Schema{Dialect: velox.MySQL, Tables: keyTables("users")}
	wantLines(t, "in place", diff(t, from, to, migrate.DiffOptions{}), []string{
		"drop-foreign-key users users_org",
		"drop-index users users_org_id",
		"add-foreign-key users users_org",
	})

	renamed := &migrate.Schema{Dialect: velox.MySQL, Tables: keyTables("members")}
	opts := migrate.DiffOptions{RenameTables: map[migrate.TableRef]migrate.TableRef{{Name: "users"}: {Name: "members"}}}
	wantLines(t, "renamed", diff(t, from, renamed, opts), []string{
		"drop-foreign-key users users_org",
		"drop-index users users_org_id",
		"rename-table members users members",
		"add-foreign-key members users_org",
	})

	// An index that still leads with the key's columns keeps the key.
	wider := migrate.Index{Name: "users_org_id_id", Columns: []string{"org_id", "id"}}
	kept := &migrate.Schema{Dialect: velox.MySQL, Tables: keyTables("users", wider)}
	wantLines(t, "kept", diff(t, from, kept, migrate.DiffOptions{}), []string{
		"add-index users users_org_id_id",
		"drop-index users users_org_id",
	})
}

// TestDiffRenamesInRefOrder: table renames are planned in the order of their
// names in from, by schema then name, whatever the map's order.
func TestDiffRenamesInRefOrder(t *testing.T) {
	t.Parallel()
	table := func(schema, name string) migrate.Table {
		return migrate.Table{Schema: schema, Name: name, Columns: []migrate.Column{{Name: "id", Type: "bigint"}}, PrimaryKey: []string{"id"}}
	}
	from := &migrate.Schema{Dialect: velox.Postgres, Tables: []migrate.Table{table("b", "a"), table("a", "z"), table("a", "m")}}
	to := &migrate.Schema{Dialect: velox.Postgres, Tables: []migrate.Table{table("b", "a2"), table("a", "z2"), table("a", "m2")}}
	opts := migrate.DiffOptions{RenameTables: map[migrate.TableRef]migrate.TableRef{
		{Schema: "b", Name: "a"}: {Schema: "b", Name: "a2"},
		{Schema: "a", Name: "z"}: {Schema: "a", Name: "z2"},
		{Schema: "a", Name: "m"}: {Schema: "a", Name: "m2"},
	}}
	for range 5 { // a map's order changes from run to run
		cs := diff(t, from, to, opts)
		got := slices.DeleteFunc(lines(cs), func(l string) bool { return !strings.HasPrefix(l, "rename-table ") })
		want := []string{"rename-table a.m2 a.m a.m2", "rename-table a.z2 a.z a.z2", "rename-table b.a2 b.a b.a2"}
		if !slices.Equal(got, want) {
			t.Fatalf("renames %q, want %q", got, want)
		}
	}
}
