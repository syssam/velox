package migrate

import "time"

// PlanOptions are NewPlan's: the renames Diff cannot see, the destructive
// changes allowed, and the lock timeout the scripts carry.
type PlanOptions struct {
	Diff        DiffOptions
	Allow       []Allow
	LockTimeout time.Duration
}

// A Plan is a migration: its changes, what Lint says of them, and the
// scripts that make them.
type Plan struct {
	Changes  []Change
	Findings []Finding
	Scripts  []Script // in run order
}

// NewPlan runs Diff, Lint, Blocked and Render in one call, as velox-gen uses
// them. When a destructive finding is not allowed, it returns a
// *BlockedError holding every such finding, and no plan. Diff's and
// Render's errors come through as they are. NewPlan does not change its
// arguments.
func NewPlan(from, to *Schema, opts PlanOptions) (*Plan, error) {
	changes, err := Diff(from, to, opts.Diff)
	if err != nil {
		return nil, err
	}
	findings := Lint(from, changes)
	if blocked := Blocked(findings, opts.Allow); len(blocked) > 0 {
		return nil, &BlockedError{Findings: blocked}
	}
	scripts, err := Render(from, to, changes, RenderOptions{LockTimeout: opts.LockTimeout, Allow: opts.Allow})
	if err != nil {
		return nil, err
	}
	return &Plan{Changes: changes, Findings: findings, Scripts: scripts}, nil
}
