package veloxvet

// The hand-kept tables, for TestTablesMatchAPI.
var (
	Roles       = rolesNames()
	InsertKeeps = insertKeeps
	InsertRuns  = insertRuns
)

// rolesNames are the keys of roles.
func rolesNames() map[string]bool {
	out := map[string]bool{}
	for k := range roles {
		out[k] = true
	}
	return out
}
