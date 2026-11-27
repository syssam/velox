package veloxvet

// The hand-kept tables, for TestTablesMatchAPI.
var (
	Roles       = rolesNames()
	InsertKeeps = insertKeeps
	InsertRuns  = insertRuns

	LockApply   = lockApply
	LockReads   = keys(lockReads)
	TxOpeners   = keys(txOpeners)
	TxResults   = txResults
	SameHandle  = sameHandle
	QueryHandle = keys(queryHandle)
)

// keys are m's keys.
func keys(m map[string]int) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

// rolesNames are the keys of roles.
func rolesNames() map[string]bool {
	out := map[string]bool{}
	for k := range roles {
		out[k] = true
	}
	return out
}
