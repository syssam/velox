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
// *BlockedError holding every such finding, and no plan. An unindexed-fk
// finding an Allow covers is left out of the plan's findings. Render gets
// opts.LockTimeout and opts.Allow as they are. Diff's and
// Render's errors come through as they are. NewPlan does not change its
// arguments.
func NewPlan(from, to *Schema, opts PlanOptions) (*Plan, error) {
	changes, err := Diff(from, to, opts.Diff)
	if err != nil {
		return nil, err
	}
	findings := unallowed(lintChanges(from, to, changes).out, opts.Allow)
	if blocked := Blocked(findings, opts.Allow); len(blocked) > 0 {
		return nil, &BlockedError{Findings: blocked, renames: renameHints(changes, blocked)}
	}
	scripts, err := Render(from, to, changes, RenderOptions{LockTimeout: opts.LockTimeout, Allow: opts.Allow})
	if err != nil {
		return nil, err
	}
	return &Plan{Changes: changes, Findings: findings, Scripts: scripts}, nil
}

// renameHints is "table.old=new" for each drop-column of blocked with an
// add-column of the same type, nullability and default in its table: a
// rename Diff could not tell from a drop and an add.
func renameHints(changes []Change, blocked []Finding) []string {
	var out []string
	for _, f := range blocked {
		if f.Code != "drop-column" || f.Change < 0 || f.Change >= len(changes) {
			continue
		}
		d, ok := changes[f.Change].(dropColumn)
		if !ok {
			continue
		}
		for _, c := range changes {
			a, ok := c.(addColumn)
			if ok && a.Table == d.Table && a.Column.Type == d.Column.Type && a.Column.Identity == d.Column.Identity {
				out = append(out, colTarget(d.Table, d.Column.Name)+"="+a.Column.Name)
			}
		}
	}
	return out
}
