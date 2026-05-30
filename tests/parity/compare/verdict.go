package compare

// Verdict is the three-way classification of a differential run, given whether
// velox and ent each match the reference oracle.
type Verdict string

const (
	// Pass: both velox and ent match the reference. No divergence.
	Pass Verdict = "pass"
	// VeloxBug: ent matches the reference but velox does not — the divergence
	// is velox's.
	VeloxBug Verdict = "velox_bug"
	// ReferenceSuspect: neither velox nor ent matches the reference — the
	// reference oracle (or the op program) is more likely wrong than both ORMs.
	ReferenceSuspect Verdict = "reference_suspect"
	// EntDivergent: velox matches the reference but ent does not — ent is the
	// outlier.
	EntDivergent Verdict = "ent_divergent"
)

// Classify maps (veloxMatchesRef, entMatchesRef) to a Verdict:
//
//	(true,  true)  -> Pass
//	(false, true)  -> VeloxBug
//	(false, false) -> ReferenceSuspect
//	(true,  false) -> EntDivergent
func Classify(veloxMatchesRef, entMatchesRef bool) Verdict {
	switch {
	case veloxMatchesRef && entMatchesRef:
		return Pass
	case !veloxMatchesRef && entMatchesRef:
		return VeloxBug
	case !veloxMatchesRef && !entMatchesRef:
		return ReferenceSuspect
	default: // veloxMatchesRef && !entMatchesRef
		return EntDivergent
	}
}
